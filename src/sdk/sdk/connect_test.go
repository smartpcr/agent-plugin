package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// tap records what the client wrote.
type tap struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *tap) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *tap) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// pipeHost serves methods as a host on in-process pipes and connects a client;
// wire is every line the client wrote.
func pipeHost(t *testing.T, o Options, methods map[string]jsonrpc.Handler) (h *Harness, host *jsonrpc.Conn, wire *tap) {
	t.Helper()
	cr, hw := io.Pipe()
	pr, cw := io.Pipe()
	wire = &tap{}
	hr := io.TeeReader(pr, wire)
	methods[protocol.InitializeMethod] = func(_ context.Context, raw json.RawMessage) (any, error) {
		return protocol.InitializeResult{ProtocolVersion: protocol.Version}, nil
	}
	methods["shutdown"] = func(context.Context, json.RawMessage) (any, error) { return protocol.Empty{}, nil }
	host = jsonrpc.Serve(context.Background(), hr, hw, jsonrpc.Options{Methods: methods})
	t.Cleanup(func() { _ = host.Close(); _ = hw.Close() })
	if o.ShutdownGrace == 0 {
		o.ShutdownGrace = time.Second
	}
	h, err := Connect(t.Context(), Transport{In: cr, Out: cw}, o)
	if err != nil {
		t.Fatal(err)
	}
	return h, host, wire
}

// DH-T-SDK-16: Run returns on the session.run.completed of its own message, not
// on one of another message or session; a cancelled Run context stops the wait
// and sends no session/cancel.
//
//dsh:test DH-T-SDK-16
func TestRunReturnsOnTheMatchingCompletion(t *testing.T) {
	var host *jsonrpc.Conn
	h, host, wire := pipeHost(t, Options{}, map[string]jsonrpc.Handler{
		"session/create": func(context.Context, json.RawMessage) (any, error) {
			return protocol.SessionCreateResult{SessionID: "s1", Cwd: "/w"}, nil
		},
		"session/prompt": func(_ context.Context, raw json.RawMessage) (any, error) {
			var p protocol.SessionPromptParams
			_ = json.Unmarshal(raw, &p)
			done := func(session, message, text string) {
				_ = host.Notify("session.run.completed", protocol.RunCompletedNotification{
					Scope: protocol.Scope{SessionID: session, RootSessionID: session},
					Run:   protocol.RunRef{Kind: "prompt", MessageID: message}, FinishReason: "stop", FinalResponse: text, LastSeq: 9,
				})
			}
			if p.MessageID != "never" {
				done("s1", "other", "wrong message")
				done("s2", p.MessageID, "wrong session")
				done("s1", p.MessageID, "echo: "+p.Content[0].Text)
			}
			return protocol.SessionPromptResult{MessageID: p.MessageID, Seq: 1}, nil
		},
	})
	s, err := h.Create(t.Context(), CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := s.Run(t.Context(), Input{Text: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if r.FinalResponse != "echo: hi" || r.LastSeq != 9 || !strings.HasPrefix(r.MessageID, "sdk-") {
		t.Fatalf("run %+v", r)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if _, err := s.Run(ctx, Input{Text: "hang", MessageID: "never"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancelled Run = %v", err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(wire.String(), "session/cancel") {
		t.Fatalf("a cancelled Run sent session/cancel:\n%s", wire)
	}
}

// DH-T-SDK-16: a handler's context ends when the host withdraws its request and
// its late answer is replaced by jsonrpc/cancelled; Close reports a handler
// that ignores its context.
//
//dsh:test DH-T-SDK-16
func TestHandlerContextEndsOnWithdrawal(t *testing.T) {
	entered, saw := make(chan struct{}), make(chan struct{})
	release := make(chan struct{})
	h, host, wire := pipeHost(t, Options{ShutdownGrace: 200 * time.Millisecond, Handlers: Handlers{
		Approval: func(ctx context.Context, _ protocol.ApprovalRequest) protocol.ApprovalResult {
			entered <- struct{}{}
			<-ctx.Done()
			close(saw)
			return protocol.ApprovalResult{Outcome: "allowed-once"}
		},
		Question: func(context.Context, protocol.QuestionRequest) (protocol.QuestionResult, error) {
			<-release // ignores its context
			return protocol.QuestionResult{Dismissed: true}, nil
		},
	}}, map[string]jsonrpc.Handler{})
	t.Cleanup(func() { close(release) })

	ctx, cancel := context.WithCancel(t.Context())
	called := make(chan error, 1)
	go func() {
		called <- host.Call(ctx, "approval/request", protocol.ApprovalRequest{InteractionID: "i1"}, nil)
	}()
	<-entered
	cancel()
	select {
	case <-saw:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler's context did not end on withdrawal")
	}
	<-called
	poll(t, "the cancelled answer", func() bool { return strings.Contains(wire.String(), jsonrpc.ReasonCancelled) })
	if strings.Contains(wire.String(), "allowed-once") {
		t.Fatalf("the late answer was sent:\n%s", wire)
	}

	go func() {
		_ = host.Call(context.Background(), "question/ask", protocol.QuestionRequest{InteractionID: "i2"}, nil)
	}()
	poll(t, "the question handler", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return h.running == 1
	})
	if err := h.Close(t.Context()); err == nil || !strings.Contains(err.Error(), "1 handler(s) still running") {
		t.Fatalf("Close = %v, want the straggler error", err)
	}
}

func TestConnectRefusesAnotherProtocolVersion(t *testing.T) {
	cr, hw := io.Pipe()
	hr, cw := io.Pipe()
	host := jsonrpc.Serve(context.Background(), hr, hw, jsonrpc.Options{Methods: map[string]jsonrpc.Handler{
		protocol.InitializeMethod: func(context.Context, json.RawMessage) (any, error) {
			return protocol.InitializeResult{ProtocolVersion: protocol.Version + 1}, nil
		},
	}})
	defer host.Close()
	if _, err := Connect(t.Context(), Transport{In: cr, Out: cw}, Options{ShutdownGrace: 100 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "protocol version") {
		t.Fatalf("Connect = %v, want the version error", err)
	}
}
