// Package replay is the component llm.replay (DH-D-UTIL-7, C54;
// DH-ADR-UTIL-7): it answers every model attempt of a run from a recorded session
// log, so the built binary replays a committed scenario without a key. The log is
// the product's own export format (session.jsonl in the scenario directory); its
// assistant/message and assistant/attempt events, in order, are the script. The
// component listens on llm.StreamAround: before serving attempt n it compares the
// live request's digest with the n-th recorded one and fails the attempt with
// replay/request-mismatch when they differ, so a replay also checks what the
// binary sent (DH-O-4). A recorded failed attempt replays as that failure. An
// attempt past the script fails with replay/unscripted, and a run that ends with
// script left fails its unload with replay/unconsumed.
//
// Children are not bound by lineage yet: a session with children (H-10) is
// recorded open, and its replay fails with replay/unscripted or a mismatch.
package replay

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Name is the catalogue name of Component.
const Name = "llm.replay"

// ServedBy names the replay in each attempt record it serves.
const ServedBy = "replay"

// SessionFile is the recorded log in a scenario directory.
const SessionFile = "session.jsonl"

// The reasons of the replay's failures (util-test-support.md sec. 6.2).
const (
	Unscripted      herr.Reason = "replay/unscripted"
	Unconsumed      herr.Reason = "replay/unconsumed"
	RequestMismatch herr.Reason = "replay/request-mismatch"
)

// The modes. ModeReplay (the default) plays the recorded replies and checks each
// request; ModeRefresh plays them without the check, so the runner can rewrite
// the expected log and requests from the run (an authored script's first run, or
// a reviewed prompt change). Record, which needs a key, is later work.
const (
	ModeReplay  = "replay"
	ModeRefresh = "refresh"
)

// Config is replay.Config (util-test-support.md sec. 6.2).
type Config struct {
	// Scenario is the scenario directory holding session.jsonl; required.
	Scenario string `json:"scenario"`
	// Mode is "replay" (the default) or "refresh".
	Mode string `json:"mode,omitempty"`
	// Route, when set, is registered as a stub route with the one model Model
	// ("replay" when empty), so a recorded route no loaded provider registers can
	// still be bound without a key. The stub is never reached: the replay serves
	// every attempt.
	Route string `json:"route,omitempty"`
	Model string `json:"model,omitempty"`
	// Requests, when set, is a directory that receives each live request's
	// canonical form as NNN.json in attempt order, for the runner to compare.
	Requests string `json:"requests,omitempty"`
}

// Turn is one recorded attempt: the digest of the request it answered and its
// settlement (the assembled message, or the failure, or both).
type Turn struct {
	Digest  string
	Message *vocab.Message
	Usage   *vocab.Usage
	Failure *vocab.Failure
}

// Load reads the script of an export file: its assistant/message and
// assistant/attempt events, in log order.
func Load(r io.Reader) ([]Turn, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 64<<20)
	var turns []Turn
	n := 0
	for sc.Scan() {
		n++
		var line struct {
			Format *int   `json:"dsh_export"`
			Type   string `json:"type"`
			Data   string `json:"data"`
		}
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			return nil, fmt.Errorf("replay: line %d: %w", n, err)
		}
		if n == 1 && line.Format == nil {
			return nil, errors.New("replay: line 1 is not an export header")
		}
		if line.Type != "assistant/message" && line.Type != "assistant/attempt" {
			continue
		}
		var p struct {
			Message       *vocab.Message `json:"message"`
			RequestDigest string         `json:"request_digest"`
			Usage         *vocab.Usage   `json:"usage"`
			Failure       *vocab.Failure `json:"failure"`
		}
		if err := json.Unmarshal([]byte(line.Data), &p); err != nil {
			return nil, fmt.Errorf("replay: line %d: %s: %w", n, line.Type, err)
		}
		if p.Message == nil && p.Failure == nil {
			return nil, fmt.Errorf("replay: line %d: %s has neither a message nor a failure", n, line.Type)
		}
		turns = append(turns, Turn{Digest: p.RequestDigest, Message: p.Message, Usage: p.Usage, Failure: p.Failure})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("replay: %w", err)
	}
	if n == 0 {
		return nil, errors.New("replay: the log is empty")
	}
	return turns, nil
}

// Player answers attempts from a script, in order.
type Player struct {
	requests string
	check    bool

	mu    sync.Mutex
	turns []Turn
	next  int
}

// NewPlayer plays turns in mode; requests, when set, is Config.Requests.
func NewPlayer(turns []Turn, mode, requests string) *Player {
	return &Player{turns: turns, requests: requests, check: mode != ModeRefresh}
}

// Listen is the StreamAround listener: it serves the next turn, or fails the
// attempt when the script is spent or the request is not the recorded one. A
// normalised recording holds a typed token, not a digest, in place of each
// request digest (the digest covers the run's ids); its requests are checked by
// the runner, which compares them normalised with the committed ones.
func (p *Player) Listen(_ *cordis.Context, req llm.StreamRequest, _ func() (llm.StreamDecision, error)) (llm.StreamDecision, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := p.next
	p.next++
	if p.requests != "" {
		if err := os.WriteFile(filepath.Join(p.requests, fmt.Sprintf("%03d.json", n)), []byte(req.Request.Canonical()+"\n"), 0o600); err != nil {
			return nil, fmt.Errorf("replay: writing request %d: %w", n, err)
		}
	}
	if n >= len(p.turns) {
		return nil, herr.Newf(Unscripted, "attempt %d has no recorded reply (the script has %d); re-record the scenario", n, len(p.turns))
	}
	t := p.turns[n]
	if got := req.Request.Digest(); p.check && strings.HasPrefix(t.Digest, "sha256:") && t.Digest != got {
		return nil, herr.Newf(RequestMismatch, "attempt %d sent request %s, the recording answered %s; re-record the scenario or fix the change", n, got, t.Digest)
	}
	return llm.Serve{Stream: t.Stream(), ServedBy: ServedBy}, nil
}

// Unconsumed fails when turns of the script were not played.
func (p *Player) Unconsumed() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if left := len(p.turns) - p.next; left > 0 {
		return herr.Newf(Unconsumed, "the run ended with %d of %d recorded replies unplayed; re-record the scenario", left, len(p.turns))
	}
	return nil
}

// Stream is the turn as a model stream: each block opened, filled and closed,
// the usage, then the finish (the recorded failure, else tool_calls when the
// message calls a tool, else stop).
func (t Turn) Stream() iter.Seq2[vocab.Chunk, error] {
	return func(yield func(vocab.Chunk, error) bool) {
		finish := vocab.Finish{Reason: vocab.FinishStop}
		if t.Message != nil {
			for i, b := range t.Message.Blocks() {
				if !yield(vocab.BlockStart{Index: i, Kind: b.Kind}, nil) {
					return
				}
				var delta vocab.Chunk
				switch {
				case b.Text != nil:
					delta = vocab.TextDelta{Index: i, Text: b.Text.Text}
				case b.Reasoning != nil:
					delta = vocab.ReasoningDelta{Index: i, Text: b.Reasoning.Text}
				case b.ToolUse != nil:
					finish.Reason = vocab.FinishToolCalls
				}
				if delta != nil && !yield(delta, nil) {
					return
				}
				if !yield(vocab.BlockEnd{Index: i, Block: b}, nil) {
					return
				}
			}
		}
		if t.Usage != nil && !yield(vocab.UsageChunk{Usage: *t.Usage}, nil) {
			return
		}
		if t.Failure != nil {
			finish = vocab.Finish{Reason: vocab.FinishError, Failure: t.Failure}
		}
		yield(finish, nil)
	}
}

// stub is the adapter of the stub route; the replay serves every attempt
// before any adapter is reached.
type stub struct{}

func (stub) Prepare(context.Context, vocab.RouteID, string, vocab.CallConfig) (llm.Call, error) {
	return stub{}, nil
}

func (stub) Attempt(context.Context, session.Request) iter.Seq2[vocab.Chunk, error] {
	return func(yield func(vocab.Chunk, error) bool) {
		f, _ := vocab.NewFailure(vocab.CodeInvalidRequest, "replay: the stub route has no provider; only the replay serves it")
		yield(vocab.Finish{Reason: vocab.FinishError, Failure: f}, nil)
	}
}

// Component is llm.replay: it loads the scenario's script, registers the stub
// route when Config.Route is set, listens on llm.StreamAround and, at unload,
// fails with replay/unconsumed when script is left.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	switch {
	case cfg.Scenario == "":
		return errors.New("replay: config.scenario names no scenario directory")
	case cfg.Mode != "" && cfg.Mode != ModeReplay && cfg.Mode != ModeRefresh:
		return fmt.Errorf("replay: mode %q is not supported; only %q and %q", cfg.Mode, ModeReplay, ModeRefresh)
	}
	f, err := os.Open(filepath.Join(cfg.Scenario, SessionFile))
	if err != nil {
		return fmt.Errorf("replay: %w", err)
	}
	turns, err := Load(f)
	f.Close()
	if err != nil {
		return err
	}
	rt, err := llm.RuntimeKey.Get(ctx)
	if err != nil {
		return err
	}
	p := NewPlayer(turns, cfg.Mode, cfg.Requests)
	if cfg.Route != "" {
		model := cfg.Model
		if model == "" {
			model = "replay"
		}
		if _, err := ctx.Effect(Name+".route", func() (cordis.Disposer, error) {
			reg, err := rt.Register(Name, llm.Route{ID: vocab.RouteID(cfg.Route), Models: []llm.Model{{Name: model}}, Adapter: stub{}})
			if err != nil {
				return nil, err
			}
			return func() error { reg.Close(); return nil }, nil
		}); err != nil {
			return err
		}
	}
	if _, err := llm.StreamAround.On(ctx, p.Listen); err != nil {
		return err
	}
	_, err = ctx.Effect(Name+".unconsumed", func() (cordis.Disposer, error) { return p.Unconsumed, nil })
	return err
}, cordis.WithInject(llm.RuntimeKey.Name()))
