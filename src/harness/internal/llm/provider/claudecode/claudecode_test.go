package claudecode_test

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/claudecode"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// planted is the token shape the fixtures plant in the client's text and the
// stand-in's standard error; it must never reach a failure.
const planted = "PLANTED"

var probeTool = vocab.ToolSchema{Name: "probe", Description: "Echo a text",
	Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`)}

// rig is one stand-in client: its record directory and the route config that
// starts it.
type rig struct {
	dir string
	cfg claudecode.Config
}

// newRig plays fixture (a testdata name, or an absolute path) with the stand-in
// settings extra.
func newRig(t *testing.T, fixture string, extra ...string) *rig {
	t.Helper()
	exe := must(os.Executable())
	dir := t.TempDir()
	if !filepath.IsAbs(fixture) {
		fixture = must(filepath.Abs(filepath.Join("testdata", fixture+".jsonl")))
	}
	env := append([]string{roleEnv + "=claude", recordEnv + "=" + dir, fixtureEnv + "=" + fixture,
		versionEnv + "=2.1.312 (Claude Code)"}, extra...)
	return &rig{dir: dir, cfg: claudecode.Config{Program: exe, Env: env, TempDir: t.TempDir(), Grace: 200 * time.Millisecond}}
}

func (r *rig) runtime(t *testing.T) *llm.Runtime {
	t.Helper()
	rt := llm.New()
	must(rt.Register("claude-code", must(claudecode.Route(r.cfg))))
	return rt
}

// seen is what the stand-in recorded; ok is false when it never ran past the
// probe.
func (r *rig) seen(t *testing.T) (seen, bool) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(r.dir, "seen.json"))
	if errors.Is(err, os.ErrNotExist) {
		return seen{}, false
	}
	var s seen
	if err != nil || json.Unmarshal(raw, &s) != nil {
		t.Fatalf("the stand-in's record: %v", err)
	}
	return s, true
}

// probes is how many times the stand-in was asked its version.
func (r *rig) probes() int {
	raw, _ := os.ReadFile(filepath.Join(r.dir, "probes"))
	return strings.Count(string(raw), "\n")
}

// newSession is a session on claude-code with a system and a user message,
// declaring the probe tool when tools is set, and its store.
func newSession(t *testing.T, tools bool) (session.Handle, store.Store) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	st := memstore.New()
	h := must(session.NewService(root, st, session.Config{}).Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	hdr := event.RequestHeaderPayload{Route: claudecode.RouteID}
	if tools {
		hdr.Tools = []vocab.ToolSchema{probeTool}
	}
	must(h.AppendGroup(bg, []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		must(event.RequestHeader.Draft(hdr)),
		must(event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: must(vocab.RestoreMessage("s1", vocab.RoleSystem, nil, vocab.TextBlock("Be brief.")))})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello")))})),
	}))
	return h, st
}

// sink appends each settlement to the session, as the driver does.
type sink struct {
	h       session.Handle
	records []llm.Attempt
	seqs    []event.Seq
}

func (s *sink) Settle(a llm.Attempt) error {
	s.records = append(s.records, a)
	r, err := s.h.Append(bg, must(a.Draft()))
	if err != nil {
		return err
	}
	s.seqs = append(s.seqs, r.Seq)
	return nil
}

// attempt prepares and streams one attempt on the session's next request.
func attempt(t *testing.T, rt *llm.Runtime, h session.Handle) ([]vocab.Chunk, *sink) {
	t.Helper()
	p := must(rt.Prepare(bg, claudecode.RouteID, "sonnet", vocab.Controls{}))
	s := &sink{h: h}
	var chunks []vocab.Chunk
	for c := range p.Stream(bg, must(h.NextRequest()), s) {
		chunks = append(chunks, c)
	}
	if len(s.records) != 1 {
		t.Fatalf("settlements = %d, want exactly one", len(s.records))
	}
	return chunks, s
}

func finishOf(t *testing.T, chunks []vocab.Chunk) vocab.Finish {
	t.Helper()
	f, ok := chunks[len(chunks)-1].(vocab.Finish)
	if !ok {
		t.Fatalf("the stream ended with %T, not a Finish", chunks[len(chunks)-1])
	}
	return f
}

// logged reads the settlement at seq back from the store and returns its
// client invocation, as a reader of the log sees it.
func logged(t *testing.T, st store.Store, h session.Handle, seq event.Seq) *event.ClientInvocation {
	t.Helper()
	r := must(st.OpenRead(bg, string(h.ID())))
	defer func() { _ = r.Close(bg) }()
	recs := must(r.Read(bg, 0, 1000, store.ReadOptions{WithAux: true}))
	for _, rec := range recs {
		if rec.Seq != seq {
			continue
		}
		switch rec.Type {
		case event.AssistantMessage.Name():
			return must(event.AssistantMessage.Decode(rec)).Client
		case event.AssistantAttempt.Name():
			return must(event.AssistantAttempt.Decode(rec)).Client
		}
	}
	t.Fatalf("no settlement at seq %d", seq)
	return nil
}

func rebuilt(t *testing.T, st store.Store, h session.Handle, asOf event.Seq) surface.Built {
	t.Helper()
	r := must(st.OpenRead(bg, string(h.ID())))
	t.Cleanup(func() { _ = r.Close(bg) })
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return r.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	return must(surface.Rebuild(bg, event.Default(), read, string(h.ID()), asOf))
}

// TestClaudeCodeSettlesOnce is Scenario "A client route settles once": a
// stand-in replaying a recorded success settles one assistant/message with
// usage, and the logged invocation is the one started: the argument list
// rebuilt from the log (Args with {attempt}) equals the logged list and its
// digest, the request rebuilt from the log at requestAsOf renders to the
// standard input and attempt files whose digests were logged, and both equal
// what the stand-in received. The client ran in an empty private directory
// holding only the attempt files, which is gone afterwards; the version was
// probed once, in the same environment.
//
//dsh:test DH-T-LLM-20, DH-T-CLAUDECODECLI-1
func TestClaudeCodeSettlesOnce(t *testing.T) {
	r := newRig(t, "text")
	h, st := newSession(t, true)
	chunks, s := attempt(t, r.runtime(t), h)
	a := s.records[0]
	if !a.Succeeded() || must(a.Draft()).Type() != "assistant/message" || finishOf(t, chunks).Reason != vocab.FinishStop {
		t.Fatalf("attempt = %+v", a.Finish)
	}
	if a.Usage == nil || a.Usage.Input != 12 || a.Usage.Output != 5 || a.Usage.CacheRead != 4 || a.Usage.Total != 21 {
		t.Fatalf("usage = %+v, want the result line's", a.Usage)
	}
	if got := a.Message.Blocks(); len(got) != 1 || got[0].Text.Text != "Hello there." {
		t.Fatalf("message = %+v", got)
	}
	inv := logged(t, st, h, s.seqs[0])
	if inv == nil {
		t.Fatal("the settlement logged no client invocation")
	}
	if inv.Client != claudecode.Client || inv.Version != "2.1.312" || inv.Program != filepath.Base(r.cfg.Program) || inv.Model != "claude-sonnet-4-5" || inv.Denials != 0 || inv.Retries != 0 {
		t.Fatalf("invocation = %+v", inv)
	}
	// The argument list rebuilt from the log.
	want := claudecode.Args(claudecode.ArgsSpec{Dir: clientroute.AttemptPlaceholder, Model: "sonnet", SessionID: inv.SessionID, Tools: true})
	if !slices.Equal(inv.Args, want) || inv.ArgsDigest != clientroute.ArgsDigest(want) {
		t.Fatalf("logged args %q (%s), rebuilt %q", inv.Args, inv.ArgsDigest, want)
	}
	// The request rebuilt from the log renders to what was written.
	re := rebuilt(t, st, h, a.RequestAsOf)
	if re.Digest() != a.RequestDigest {
		t.Fatalf("rebuilt digest %s, settled %s", re.Digest(), a.RequestDigest)
	}
	rr := must(clientroute.Render(re, clientroute.RenderOptions{}))
	if inv.StdinDigest != clientroute.Digest(rr.Prompt) {
		t.Fatalf("stdin digest %s, rebuilt %s", inv.StdinDigest, clientroute.Digest(rr.Prompt))
	}
	files := map[string]string{}
	for _, f := range inv.Files {
		files[f.Name] = f.Digest
	}
	if files[claudecode.SystemFile] != clientroute.Digest(rr.System) || files[claudecode.ToolsFile] != clientroute.Digest(rr.Tools) || files[claudecode.MCPFile] == "" {
		t.Fatalf("file digests %v", files)
	}
	// What the stand-in received is what was logged.
	got, ok := r.seen(t)
	if !ok {
		t.Fatal("the stand-in did not run")
	}
	if !slices.Equal(clientroute.Placeholder(got.Args, got.Cwd), inv.Args) {
		t.Fatalf("received args %q, logged %q", got.Args, inv.Args)
	}
	if clientroute.Digest([]byte(got.Stdin)) != inv.StdinDigest || got.Stdin != "hello" {
		t.Fatalf("received stdin %q", got.Stdin)
	}
	if !slices.Equal(got.Files, []string{claudecode.MCPFile, claudecode.SystemFile, claudecode.ToolsFile}) {
		t.Fatalf("the working directory held %v", got.Files)
	}
	if !slices.Equal(got.Tools, []string{"probe"}) {
		t.Fatalf("the listing server listed %v", got.Tools)
	}
	if _, err := os.Stat(got.Cwd); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the attempt directory is left behind: %v", err)
	}
	if n := r.probes(); n != 1 {
		t.Fatalf("probes = %d, want 1", n)
	}
	if !slices.Contains(inv.Env, "CLAUDE_CODE_MAX_RETRIES") || slices.ContainsFunc(inv.Env, func(n string) bool { return strings.Contains(n, "=") }) {
		t.Fatalf("logged env names %v", inv.Env)
	}
}

// TestClaudeCodeNoAmbientCredential is Scenario "No ambient credential reaches
// the client": with credentials and provider overrides in the host's
// environment, the stand-in reports every one absent, and the route's own
// settings present.
//
//dsh:test DH-T-LLM-20, DH-T-CLAUDECODECLI-1
func TestClaudeCodeNoAmbientCredential(t *testing.T) {
	for _, kv := range [][2]string{
		{"ANTHROPIC_API_KEY", "sk-ant-api03-" + planted}, {"GH_TOKEN", "gho_" + planted}, {"GITHUB_TOKEN", "ghu_" + planted},
		{"ANTHROPIC_AUTH_TOKEN", "x"}, {"CLAUDE_CODE_OAUTH_TOKEN", "x"}, {"ANTHROPIC_BASE_URL", "http://127.0.0.1:1"},
		{"ANTHROPIC_MODEL", "other"}, {"CLAUDE_CODE_USE_BEDROCK", "1"}, {"DSH_HOME", "/h"}, {"CLAUDE_CODE_MAX_RETRIES", "9"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	r := newRig(t, "text")
	h, _ := newSession(t, false)
	_, s := attempt(t, r.runtime(t), h)
	if !s.records[0].Succeeded() {
		t.Fatalf("attempt = %+v", s.records[0].Finish)
	}
	got, _ := r.seen(t)
	for _, n := range []string{"ANTHROPIC_API_KEY", "GH_TOKEN", "GITHUB_TOKEN", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
		"ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL", "CLAUDE_CODE_USE_BEDROCK", "DSH_HOME"} {
		if !slices.Contains(got.Absent, n) {
			t.Errorf("%s reached the client", n)
		}
	}
	for n, v := range map[string]string{"CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_CODE_SKIP_PROMPT_HISTORY": "1", "DISABLE_AUTOUPDATER": "1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"} {
		if got.Env[n] != v {
			t.Errorf("%s = %q, want %q", n, got.Env[n], v)
		}
	}
	if slices.Contains(got.Args, "--mcp-config") || len(got.Files) != 1 {
		t.Errorf("a request without tools passed the MCP configuration: %q, files %v", got.Args, got.Files)
	}
}

// TestClaudeCodeStreamMapping: recorded text, reasoning-and-text, one tool call
// and two parallel tool calls give the tabled chunks and finish; unknown line
// types are ignored. A tool call is returned unexecuted (M1-D-PIPE-6 A): the
// listing server listed it, the client denied it, the call comes back as
// ToolCallDelta under the harness's name and Finish{ToolCalls}, and the
// listing server was stopped with the tree.
//
//dsh:test DH-T-LLM-21, DH-T-CLAUDECODECLI-2
func TestClaudeCodeStreamMapping(t *testing.T) {
	t.Run("reasoning", func(t *testing.T) {
		r := newRig(t, "reasoning")
		h, _ := newSession(t, false)
		chunks, s := attempt(t, r.runtime(t), h)
		var kinds []string
		for _, c := range chunks {
			switch c := c.(type) {
			case vocab.BlockStart:
				kinds = append(kinds, "start:"+string(c.Kind))
			case vocab.ReasoningDelta:
				kinds = append(kinds, "reasoning:"+c.Text)
			case vocab.TextDelta:
				kinds = append(kinds, "text:"+c.Text)
			case vocab.BlockEnd:
				kinds = append(kinds, "end")
			case vocab.UsageChunk:
				kinds = append(kinds, "usage")
			case vocab.Finish:
				kinds = append(kinds, "finish:"+string(c.Reason))
			}
		}
		want := []string{"start:reasoning", "reasoning:The user greets.", "end", "start:text", "text:Hi.", "end", "usage", "finish:stop"}
		if !slices.Equal(kinds, want) {
			t.Fatalf("chunks %v, want %v", kinds, want)
		}
		b := s.records[0].Message.Blocks()
		if len(b) != 2 || b[0].Reasoning.Signature != "c2lnbmF0dXJl" || b[1].Text.Text != "Hi." {
			t.Fatalf("message = %+v", b)
		}
	})
	for _, tc := range []struct {
		fixture string
		calls   []string // the returned calls' ids
		args    []string
	}{
		{"tool", []string{"toolu_01SYNTH"}, []string{`{"text":"ping"}`}},
		{"parallel", []string{"toolu_01A", "toolu_01B"}, []string{`{"text":"a"}`, `{"text":"b"}`}},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			r := newRig(t, tc.fixture)
			h, st := newSession(t, true)
			chunks, s := attempt(t, r.runtime(t), h)
			a := s.records[0]
			if finishOf(t, chunks).Reason != vocab.FinishToolCalls || !a.Succeeded() || must(a.Draft()).Type() != "assistant/message" {
				t.Fatalf("finish = %+v", a.Finish)
			}
			var named []string
			for _, c := range chunks {
				if d, ok := c.(vocab.ToolCallDelta); ok && d.Name != "" {
					named = append(named, d.Name)
				}
			}
			var ids, args []string
			for _, b := range a.Message.Blocks() {
				if b.Kind == vocab.KindToolUse {
					if b.ToolUse.Name != "probe" {
						t.Fatalf("tool call name %q, want the harness's name", b.ToolUse.Name)
					}
					ids, args = append(ids, b.ToolUse.ID), append(args, string(b.ToolUse.Args))
				}
			}
			if !slices.Equal(ids, tc.calls) || !slices.Equal(args, tc.args) || len(named) != len(tc.calls) || named[0] != "probe" {
				t.Fatalf("calls %v %v (deltas %v), want %v %v", ids, args, named, tc.calls, tc.args)
			}
			inv := logged(t, st, h, s.seqs[0])
			if inv.Denials != len(tc.calls) {
				t.Fatalf("denials = %d, want %d", inv.Denials, len(tc.calls))
			}
			got, _ := r.seen(t)
			if got.Server == 0 || !slices.Equal(got.Tools, []string{"probe"}) {
				t.Fatalf("the listing server did not list the tool: %+v", got)
			}
			ctx, cancel := context.WithTimeout(bg, 30*time.Second)
			defer cancel()
			procwait.Until(ctx, t, func() bool { return subprocesstest.Exited(got.Server) && subprocesstest.Exited(got.Client) })
		})
	}
}

// countingAdapter is a second route that must never be prepared.
type countingAdapter struct{ prepares atomic.Int32 }

func (c *countingAdapter) Prepare(context.Context, vocab.RouteID, string, vocab.CallConfig) (llm.Call, error) {
	c.prepares.Add(1)
	return nil, errors.New("not used")
}

type noCall struct{}

func (noCall) Attempt(context.Context, session.Request) iter.Seq2[vocab.Chunk, error] {
	return func(func(vocab.Chunk, error) bool) {}
}

// TestClaudeCodeFailureMapping: each structured failure gives its code and
// class as exactly one assistant/attempt; signed out and a rejected sign-in
// are Permanent, so the route's policy never retries them, and a second
// registered route is never prepared; a breach (a tool ran in the client, the
// listing server was asked to run one) is llm/client-contract; a key source
// other than the sign-in is llm/invalid-credential; a token planted in the
// client's text and standard error never reaches a failure.
//
//dsh:test DH-T-LLM-22, DH-T-CLAUDECODECLI-3
func TestClaudeCodeFailureMapping(t *testing.T) {
	empty := filepath.Join(t.TempDir(), "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, fixture string
		tools         bool
		extra         []string
		code          vocab.Code
		class         vocab.Class
		status        int
	}{
		{"signed out", "signed-out", false, nil, vocab.CodeSignInRequired, vocab.Permanent, 0},
		{"401", "rejected-401", false, nil, vocab.CodeAuth, vocab.Permanent, 401},
		{"key source", "api-key-source", false, nil, vocab.CodeInvalidCredential, vocab.Permanent, 0},
		{"tool ran", "tool-ran", true, nil, vocab.CodeClientContract, vocab.Permanent, 0},
		{"listing asked to run", "tool", true, []string{modeEnv + "=call"}, vocab.CodeClientContract, vocab.Permanent, 0},
		{"rate limit", "rate-limit", false, nil, vocab.CodeRateLimit, vocab.Transient, 429},
		{"no result", "no-result", false, nil, vocab.CodeStreamClosed, vocab.Transient, 0},
		{"unknown option", empty, false, []string{stderrEnv + "=error: unknown option '--max-turns' " + planted, exitEnv + "=1"}, vocab.CodeClientUpgradeRequired, vocab.Permanent, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, tc.fixture, append([]string{stderrEnv + "=sk-ant-oat01-" + planted}, tc.extra...)...)
			rt := r.runtime(t)
			other := &countingAdapter{}
			must(rt.Register("other", llm.Route{ID: "other/route", Models: []llm.Model{{Name: "m"}}, Adapter: other}))
			h, st := newSession(t, tc.tools)
			p := must(rt.Prepare(bg, claudecode.RouteID, "sonnet", vocab.Controls{}))
			s := &sink{h: h}
			for range p.Stream(bg, must(h.NextRequest()), s) {
			}
			if len(s.records) != 1 {
				t.Fatalf("settlements = %d", len(s.records))
			}
			a := s.records[0]
			f := a.Finish.Failure
			if must(a.Draft()).Type() != "assistant/attempt" || f == nil || f.Code != tc.code || f.Class != tc.class || f.Status != tc.status {
				t.Fatalf("finish = %+v (%+v), want %s %s status %d", a.Finish, f, tc.code, tc.class, tc.status)
			}
			if strings.Contains(f.Message, planted) || strings.Contains(f.Message, "Not logged in") || strings.Contains(f.Message, "API Error") {
				t.Fatalf("the client's text reached the failure: %q", f.Message)
			}
			if _, stop := p.Retry().Next(f, 1, 0, 0.5); tc.class == vocab.Permanent && stop == "" {
				t.Fatalf("the route's policy would retry %s", f.Code)
			}
			if other.prepares.Load() != 0 {
				t.Fatal("another route was prepared")
			}
			if inv := logged(t, st, h, s.seqs[0]); inv == nil || inv.SessionID == "" {
				t.Fatalf("a failed attempt logged no invocation: %+v", inv)
			}
		})
	}
	var _ llm.Call = noCall{}
}

// TestClaudeCodeRefusedBeforeInput: a client below the floor, one whose
// version line is not recognised and a missing one fail Prepare with
// llm/client-upgrade-required, and the client never receives the request; a
// control the client cannot take is refused before the probe.
//
//dsh:test DH-T-LLM-20, DH-T-CLAUDECODECLI-1
func TestClaudeCodeRefusedBeforeInput(t *testing.T) {
	for name, version := range map[string]string{"too old": "2.1.258 (Claude Code)", "unparsable": "Claude Code, some version"} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, "text", versionEnv+"="+version)
			_, err := r.runtime(t).Prepare(bg, claudecode.RouteID, "sonnet", vocab.Controls{})
			var f *vocab.Failure
			if !errors.As(err, &f) || f.Code != vocab.CodeClientUpgradeRequired || f.Class != vocab.Permanent {
				t.Fatalf("prepare = %v", err)
			}
			if _, ran := r.seen(t); ran || r.probes() != 1 {
				t.Fatalf("the client ran (%v) or was probed %d times, want only one probe", ran, r.probes())
			}
		})
	}
	t.Run("missing", func(t *testing.T) {
		r := newRig(t, "text")
		r.cfg.Program = filepath.Join(t.TempDir(), "claude")
		_, err := r.runtime(t).Prepare(bg, claudecode.RouteID, "sonnet", vocab.Controls{})
		var f *vocab.Failure
		if !errors.As(err, &f) || f.Code != vocab.CodeClientUpgradeRequired {
			t.Fatalf("prepare = %v", err)
		}
	})
	t.Run("controls", func(t *testing.T) {
		r := newRig(t, "text")
		temp := 0.5
		for _, c := range []vocab.Controls{{Temperature: &temp}, {TopP: &temp}, {Stop: []string{"x"}}, {MaxOutputTokens: 10},
			{ToolChoice: vocab.ToolChoice{Mode: vocab.ToolChoiceRequired}}} {
			_, err := r.runtime(t).Prepare(bg, claudecode.RouteID, "sonnet", c)
			var f *vocab.Failure
			if !errors.As(err, &f) || f.Code != vocab.CodeUnsupportedOption {
				t.Errorf("controls %+v: prepare = %v", c, err)
			}
		}
		if r.probes() != 0 {
			t.Fatalf("an unsupported control was probed for (%d)", r.probes())
		}
	})
}

// TestClaudeCodeCancelStopsTheTree: a stand-in that has started the listing
// server and stalls mid-message is cancelled: the stream ends with
// Finish{Aborted}, one assistant/attempt with llm/aborted and the logged
// invocation settles, the client and the listing server are both gone, and the
// attempt directory is removed.
//
//dsh:test DH-T-LLM-23, DH-T-CLAUDECODECLI-4
func TestClaudeCodeCancelStopsTheTree(t *testing.T) {
	r := newRig(t, "slow", modeEnv+"=hang")
	h, _ := newSession(t, true)
	p := must(r.runtime(t).Prepare(bg, claudecode.RouteID, "sonnet", vocab.Controls{}))
	ctx, cancel := context.WithCancel(bg)
	t.Cleanup(cancel)
	s := &sink{h: h}
	var fin vocab.Finish
	var got seen
	for c := range p.Stream(ctx, must(h.NextRequest()), s) {
		switch c := c.(type) {
		case vocab.TextDelta:
			// The stand-in records before it streams: its tree is known now.
			got, _ = r.seen(t)
			cancel()
		case vocab.Finish:
			fin = c
		}
	}
	if got.Client == 0 || got.Server == 0 {
		t.Fatalf("the stand-in did not start its tree: %+v", got)
	}
	if fin.Reason != vocab.FinishAborted || len(s.records) != 1 || s.records[0].Finish.Failure.Code != vocab.CodeAborted {
		t.Fatalf("finish = %+v, settlements %d", fin, len(s.records))
	}
	if inv := s.records[0].Client; inv == nil || inv.SessionID == "" {
		t.Fatalf("the cancelled attempt settled no invocation: %+v", inv)
	}
	wctx, wcancel := context.WithTimeout(bg, 30*time.Second)
	defer wcancel()
	procwait.Until(wctx, t, func() bool { return subprocesstest.Exited(got.Client) && subprocesstest.Exited(got.Server) })
	procwait.Until(wctx, t, func() bool { _, err := os.Stat(got.Cwd); return errors.Is(err, os.ErrNotExist) })
}

// TestClaudeCodeRoute: the route is claude-code with the alias catalogue and
// the default retry policy; an extra variable that the route removes is
// refused; a call for another route is llm/no-route.
//
//dsh:test DH-T-LLM-20
func TestClaudeCodeRoute(t *testing.T) {
	route := must(claudecode.Route(claudecode.Config{}))
	if route.ID != claudecode.RouteID || len(route.Models) != 3 || route.Models[0].Name != "sonnet" || route.Retry != llm.DefaultRetry() {
		t.Fatalf("route = %+v", route)
	}
	for _, kv := range []string{"ANTHROPIC_API_KEY=x", "ANTHROPIC_BASE_URL=http://x", "CLAUDE_CODE_MAX_RETRIES=3", "DSH_X=1", "nope"} {
		if _, err := claudecode.Route(claudecode.Config{Env: []string{kv}}); err == nil {
			t.Errorf("extra %q was accepted", kv)
		}
	}
	custom := must(claudecode.Route(claudecode.Config{Models: []llm.Model{{Name: "claude-opus-4-1"}}, MaxConcurrent: 2, Retry: llm.PersistentRetry()}))
	if custom.Models[0].Name != "claude-opus-4-1" || custom.MaxConcurrent != 2 || custom.Retry != llm.PersistentRetry() {
		t.Fatalf("custom route = %+v", custom)
	}
	if _, err := route.Adapter.Prepare(bg, "other", "sonnet", vocab.CallConfig{}); err == nil {
		t.Fatal("a call for another route was prepared")
	}
}
