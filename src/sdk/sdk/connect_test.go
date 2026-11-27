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

// The session API of sdk-acp-typert.md sec. 6.1 reaches the host: Resume, List,
// Wait, Cancel (inbox "keep" by default), History, and Events, which yields one
// session's events in order and ends a subscriber that falls behind.
//
//dsh:test DH-T-SDK-16
func TestTheSessionAPIReachesTheHost(t *testing.T) {
	var mu sync.Mutex
	got := map[string]string{}
	record := func(method string, result any) jsonrpc.Handler {
		return func(_ context.Context, raw json.RawMessage) (any, error) {
			mu.Lock()
			got[method] = string(raw)
			mu.Unlock()
			return result, nil
		}
	}
	h, host, _ := pipeHost(t, Options{}, map[string]jsonrpc.Handler{
		"session/resume":  record("session/resume", protocol.SessionResumeResult{SessionID: "s1", Cwd: "/w", LastSeq: 4}),
		"session/list":    record("session/list", protocol.SessionListResult{Sessions: []protocol.SessionSummary{{SessionID: "s1"}}, NextCursor: "c2"}),
		"session/wait":    record("session/wait", protocol.LastSeqResult{LastSeq: 4}),
		"session/cancel":  record("session/cancel", protocol.SessionCancelResult{Cancelled: true}),
		"session/history": record("session/history", protocol.SessionHistoryResult{Events: []protocol.Event{{Seq: 3}, {Seq: 4}}}),
	})
	s, err := h.Resume(t.Context(), "s1", ResumeOptions{PermissionMode: "plan"})
	if err != nil || s.ID != "s1" || s.Cwd != "/w" || s.LastSeq != 4 {
		t.Fatalf("Resume = %+v, %v", s, err)
	}
	page, err := h.List(t.Context(), ListOptions{Cursor: "c1", Limit: 1})
	if err != nil || len(page.Sessions) != 1 || page.NextCursor != "c2" {
		t.Fatalf("List = %+v, %v", page, err)
	}
	if err := s.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := s.Cancel(t.Context(), CancelOptions{}); err != nil {
		t.Fatal(err)
	}
	events, err := s.History(t.Context(), 2, 10)
	if err != nil || len(events) != 2 || events[1].Seq != 4 {
		t.Fatalf("History = %+v, %v", events, err)
	}
	for method, want := range map[string]string{
		"session/resume":  `{"sessionId":"s1","permissionMode":"plan"}`,
		"session/list":    `{"cursor":"c1","limit":1}`,
		"session/wait":    `{"sessionId":"s1"}`,
		"session/cancel":  `{"sessionId":"s1","inbox":"keep"}`,
		"session/history": `{"sessionId":"s1","afterSeq":2,"limit":10}`,
	} {
		if got[method] != want {
			t.Fatalf("%s params %s, want %s", method, got[method], want)
		}
	}

	live, slow := s.Events(8), s.Events(1)
	for _, n := range []struct {
		session string
		seq     int64
	}{{"s1", 5}, {"s2", 99}, {"s1", 6}} {
		if err := host.Notify("session.event", protocol.SessionEventNotification{Scope: protocol.Scope{SessionID: n.session}, Event: protocol.Event{Seq: n.seq}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, want := range []int64{5, 6} {
		if e := <-live.C(); e.Seq != want {
			t.Fatalf("event seq %d, want %d", e.Seq, want)
		}
	}
	if e := <-slow.C(); e.Seq != 5 {
		t.Fatalf("slow subscriber's first event seq %d", e.Seq)
	}
	if _, open := <-slow.C(); open || !errors.Is(slow.Err(), ErrSlowConsumer) {
		t.Fatalf("slow subscriber open %v, err %v; want ended with ErrSlowConsumer", open, slow.Err())
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, open := <-live.C(); open {
		t.Fatal("a subscription outlived the connection")
	}
	poll(t, "the subscription's end", func() bool { return live.Err() != nil })
	if late := s.Events(1); late.Err() == nil {
		t.Fatal("a subscription made after the connection ended is open")
	}
}

// Once Close has stopped admitting handlers, a host request the codec
// dispatched earlier but scheduled only now never runs its handler, so Close's
// accounting cannot miss it.
//
//dsh:test DH-T-SDK-16
func TestCloseAdmitsNoHandlerAfterItsAccounting(t *testing.T) {
	h, _, _ := pipeHost(t, Options{}, map[string]jsonrpc.Handler{})
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	ran := false
	wrapped := h.handler(func(context.Context, json.RawMessage) (any, error) { ran = true; return nil, nil })
	if _, err := wrapped(t.Context(), nil); !errors.Is(err, errClosing) || ran {
		t.Fatalf("handler after Close: err %v, ran %v", err, ran)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.running != 0 {
		t.Fatalf("running %d after Close", h.running)
	}
}

// failCloser is the host's input whose Close fails after closing the pipe.
type failCloser struct{ *io.PipeWriter }

func (w failCloser) Close() error {
	return errors.Join(w.PipeWriter.Close(), errors.New("closer broke"))
}

// Close closes the transport's Out even when the connection has already ended,
// and returns its failure.
//
//dsh:test DH-T-SDK-16
func TestCloseReturnsAFailedTransportCloser(t *testing.T) {
	cr, hw := io.Pipe()
	hr, cw := io.Pipe()
	host := jsonrpc.Serve(context.Background(), hr, hw, jsonrpc.Options{Methods: map[string]jsonrpc.Handler{
		protocol.InitializeMethod: func(context.Context, json.RawMessage) (any, error) {
			return protocol.InitializeResult{ProtocolVersion: protocol.Version}, nil
		},
	}})
	t.Cleanup(func() { _ = host.Close() })
	h, err := Connect(t.Context(), Transport{In: cr, Out: failCloser{cw}}, Options{ShutdownGrace: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := hw.Close(); err != nil {
		t.Fatal(err)
	}
	<-h.Done()
	if err := h.Close(t.Context()); err == nil || !strings.Contains(err.Error(), "closer broke") {
		t.Fatalf("Close = %v, want the closer's failure", err)
	}
	if _, err := cw.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("write after Close = %v, want the pipe closed", err)
	}
}
