package jsonrpc_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc/jsonrpctest"
)

// receive takes one value from ch within the kit's Timeout.
func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(jsonrpctest.Timeout):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

// TestServeIncomingCancel (ported onto jsonrpctest): the peer sends a request and
// then $/cancel; malformed and unknown cancels are ignored, the handler's context
// ends and the peer receives jsonrpc/cancelled.
//
//dsh:test DH-T-SDK-3
func TestServeIncomingCancel(t *testing.T) {
	started, ended := make(chan struct{}, 1), make(chan struct{}, 1)
	_, p := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: map[string]jsonrpc.Handler{
		"wait": func(ctx context.Context, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			ended <- struct{}{}
			return "late", nil
		},
	}})
	p.Request(jsonrpc.Int64ID(7), "wait", nil)
	receive(t, started)
	p.Cancel(jsonrpc.Int64ID(99))
	p.Notify(jsonrpc.CancelMethod, map[string]any{"id": map[string]any{}})
	p.Notify(jsonrpc.CancelMethod, []int{1})
	p.Cancel(jsonrpc.Int64ID(7))
	receive(t, ended)
	p.ExpectError(jsonrpc.Int64ID(7), jsonrpc.ReasonCancelled)
}

// TestCallCancelSendsCancel (ported onto jsonrpctest): ending a caller's context
// returns at once and sends $/cancel; the late response is a fault for OnError and
// Log, never a result.
//
//dsh:test DH-T-SDK-3
func TestCallCancelSendsCancel(t *testing.T) {
	faults := make(chan error, 1)
	var logged bytes.Buffer
	conn, p := jsonrpctest.NewPeer(t, jsonrpc.Options{
		OnError: func(err error) { faults <- err },
		Log:     slog.New(slog.NewTextHandler(&logged, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- conn.Call(ctx, "slow", nil, nil) }()
	req := p.ExpectRequest("slow")
	cancel()
	if err := receive(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Call = %v, want context.Canceled", err)
	}
	n := p.ExpectNotification(jsonrpc.CancelMethod)
	if string(n.Params) != `{"id":`+req.ID.String()+`}` {
		t.Fatalf("$/cancel params = %s, want the id %s", n.Params, req.ID)
	}
	p.Reply(req.ID, 1)
	if err := receive(t, faults); !jsonrpc.HasReason(err, jsonrpc.ReasonUnknownResponse) {
		t.Fatalf("late response fault = %v", err)
	}
	// The log line is written before OnError is called, so it is visible here.
	if !strings.Contains(logged.String(), jsonrpc.ReasonUnknownResponse) {
		t.Fatalf("Log did not receive the fault: %q", logged.String())
	}
}

// TestWriteStallEndsTheConnection is the stage scenario "A stalled pipe ends the
// connection": with a peer that reads nothing and WriteStall 5 s on virtual time, a
// queued notify and three pending calls end the connection exactly at 5 s with
// jsonrpc/write-stalled, and each pending call fails once with it.
//
//dsh:test DH-T-SDK-4
func TestWriteStallEndsTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const stall = 5 * time.Second
		var logged bytes.Buffer
		conn, p := jsonrpctest.NewPeer(t, jsonrpc.Options{
			WriteStall: stall,
			Log:        slog.New(slog.NewTextHandler(&logged, nil)),
		})
		synctest.Wait() // the peer's reader is in its first read
		p.Stall()       // ...and reads nothing after it
		start := time.Now()
		if err := conn.Notify("first", nil); err != nil {
			t.Fatal(err)
		}
		if err := conn.Notify("note", nil); err != nil {
			t.Fatal(err)
		}
		calls := make([]chan error, 3)
		var callers sync.WaitGroup
		t.Cleanup(func() { _ = conn.Close(); callers.Wait() }) // P-C: unblock, then join
		for i := range calls {
			calls[i] = make(chan error, 1)
			callers.Add(1)
			go func() { defer callers.Done(); calls[i] <- conn.Call(context.Background(), "m", nil, nil) }()
		}
		synctest.Wait() // the writer is blocked and every call is pending
		select {
		case <-conn.Done():
			t.Fatal("the connection ended before WriteStall passed")
		default:
		}
		<-conn.Done()
		if got := time.Since(start); got != stall {
			t.Fatalf("the connection ended after %s, want %s", got, stall)
		}
		if !jsonrpc.HasReason(conn.Err(), jsonrpc.ReasonWriteStalled) {
			t.Fatalf("Err = %v, want %s", conn.Err(), jsonrpc.ReasonWriteStalled)
		}
		for i, ch := range calls {
			if err := <-ch; !jsonrpc.HasReason(err, jsonrpc.ReasonWriteStalled) {
				t.Fatalf("pending call %d = %v, want %s", i, err, jsonrpc.ReasonWriteStalled)
			}
		}
		synctest.Wait()
		for i, ch := range calls {
			if len(ch) != 0 {
				t.Fatalf("pending call %d failed more than once", i)
			}
		}
		if p.ExpectNotification("first") == nil {
			t.Fatal("the frame read before the stall was lost")
		}
		if !strings.Contains(logged.String(), "write stalled") {
			t.Fatalf("Log did not receive the stall: %q", logged.String())
		}
		if err := conn.Notify("after", nil); !jsonrpc.HasReason(err, jsonrpc.ReasonClosed) {
			t.Fatalf("Notify after the stall = %v", err)
		}
	})
}

// TestWritesWithinTheStallKeepTheConnection: a peer that resumes reading before
// WriteStall passes keeps the connection, and the frames arrive in enqueue order.
//
//dsh:test DH-T-SDK-4
func TestWritesWithinTheStallKeepTheConnection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, p := jsonrpctest.NewPeer(t, jsonrpc.Options{WriteStall: 5 * time.Second})
		synctest.Wait()
		p.Stall()
		for _, m := range []string{"a", "b", "c"} {
			if err := conn.Notify(m, nil); err != nil {
				t.Fatal(err)
			}
		}
		<-time.After(4 * time.Second) // virtual: the writer has waited 4 s of its 5
		p.Resume()
		for _, m := range []string{"a", "b", "c"} {
			p.ExpectNotification(m)
		}
		if err := conn.Barrier(context.Background()); err != nil {
			t.Fatalf("Barrier = %v", err)
		}
		if conn.Err() != nil {
			t.Fatalf("Err = %v, want a running connection", conn.Err())
		}
	})
}
