package sdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// CreateOptions create a session; TimeZone is sent only when set.
type CreateOptions = protocol.SessionCreateParams

// Session is one session the connection holds the write lease of.
type Session struct {
	h       *Harness
	ID      string
	Cwd     string
	LastSeq int64
}

// Input is one message: Text, or Content when it is set. MessageID is chosen by
// the client when empty; Delivery is "follow-up" (the default) or "steer".
type Input struct {
	Text      string
	Content   []protocol.ContentBlock
	MessageID string
	Delivery  string
}

// Receipt is the host's receipt of a queued message.
type Receipt = protocol.SessionPromptResult

// RunResult is the session.run.completed of a run.
type RunResult struct {
	MessageID     string
	Turn          int64
	FinishReason  string
	FinalResponse string
	FirstSeq      int64
	LastSeq       int64
}

// runKey names the run a waiter waits for.
type runKey struct{ session, message string }

// notify delivers each session.run.completed to the Run waiting for it (a run
// nobody waits for is dropped) and each session.event to the subscriptions.
func (h *Harness) notify(method string, raw json.RawMessage) {
	switch method {
	case "session.event":
		h.event(raw)
		return
	case "session.run.completed":
	default:
		return
	}
	var n protocol.RunCompletedNotification
	if json.Unmarshal(raw, &n) != nil || n.Run.Kind != "prompt" {
		return
	}
	key := runKey{n.SessionID, n.Run.MessageID}
	h.mu.Lock()
	ch := h.runs[key]
	delete(h.runs, key)
	h.mu.Unlock()
	if ch != nil {
		ch <- n
	}
}

// Create creates a session.
func (h *Harness) Create(ctx context.Context, o CreateOptions) (*Session, error) {
	var r protocol.SessionCreateResult
	if err := h.conn.Call(ctx, "session/create", o, &r); err != nil {
		return nil, fmt.Errorf("sdk: session/create: %w", err)
	}
	return &Session{h: h, ID: r.SessionID, Cwd: r.Cwd, LastSeq: r.LastSeq}, nil
}

func (s *Session) promptParams(in Input) (protocol.SessionPromptParams, error) {
	p := protocol.SessionPromptParams{SessionID: s.ID, Content: in.Content, Delivery: in.Delivery, MessageID: in.MessageID}
	if len(p.Content) == 0 {
		if in.Text == "" {
			return p, errors.New("sdk: the input is empty")
		}
		p.Content = []protocol.ContentBlock{{Type: "text", Text: in.Text}}
	}
	return p, nil
}

// Prompt queues a message and returns the receipt without waiting for the run.
func (s *Session) Prompt(ctx context.Context, in Input) (Receipt, error) {
	p, err := s.promptParams(in)
	if err != nil {
		return Receipt{}, err
	}
	var r Receipt
	if err := s.h.conn.Call(ctx, "session/prompt", p, &r); err != nil {
		return Receipt{}, fmt.Errorf("sdk: session/prompt: %w", err)
	}
	return r, nil
}

// Run queues a message and returns on the session.run.completed of its message
// id. Ending ctx stops the wait only; the run goes on.
func (s *Session) Run(ctx context.Context, in Input) (RunResult, error) {
	p, err := s.promptParams(in)
	if err != nil {
		return RunResult{}, err
	}
	if p.MessageID == "" {
		var b [16]byte
		_, _ = rand.Read(b[:])
		p.MessageID = "sdk-" + hex.EncodeToString(b[:])
	}
	// The waiter is in place before the prompt is sent, so a run that completes
	// before the receipt arrives is not missed.
	key := runKey{s.ID, p.MessageID}
	ch := make(chan protocol.RunCompletedNotification, 1)
	s.h.mu.Lock()
	if _, dup := s.h.runs[key]; dup {
		s.h.mu.Unlock()
		return RunResult{}, fmt.Errorf("sdk: a Run already waits for message %q", p.MessageID)
	}
	s.h.runs[key] = ch
	s.h.mu.Unlock()
	defer func() {
		s.h.mu.Lock()
		if s.h.runs[key] == ch {
			delete(s.h.runs, key)
		}
		s.h.mu.Unlock()
	}()
	if err := s.h.conn.Call(ctx, "session/prompt", p, nil); err != nil {
		return RunResult{}, fmt.Errorf("sdk: session/prompt: %w", err)
	}
	select {
	case n := <-ch:
		return RunResult{MessageID: n.Run.MessageID, Turn: n.Turn, FinishReason: n.FinishReason, FinalResponse: n.FinalResponse, FirstSeq: n.FirstSeq, LastSeq: n.LastSeq}, nil
	case <-ctx.Done():
		return RunResult{}, ctx.Err()
	case <-s.h.conn.Done():
		return RunResult{}, fmt.Errorf("sdk: the connection ended before message %q completed: %w", p.MessageID, s.h.conn.Err())
	}
}

// Close releases the session's write lease.
func (s *Session) Close(ctx context.Context) error {
	return s.call(ctx, "session/close", protocol.SessionRef{SessionID: s.ID}, &protocol.LastSeqResult{})
}

func (s *Session) call(ctx context.Context, method string, params, result any) error {
	if err := s.h.conn.Call(ctx, method, params, result); err != nil {
		return fmt.Errorf("sdk: %s: %w", method, err)
	}
	return nil
}
