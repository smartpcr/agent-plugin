package jsonrpctest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// recorder is a testing.TB whose Fatalf records the failure and returns, so the
// kit's failure paths can be observed; everything else is the real test's.
type recorder struct {
	testing.TB
	mu     sync.Mutex
	fatals []string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
}

// failed returns the recorded failures and forgets them.
func (r *recorder) failed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	f := r.fatals
	r.fatals = nil
	return f
}

func (r *recorder) wantFailure(t *testing.T, substr string) {
	t.Helper()
	got := r.failed()
	if len(got) != 1 || !strings.Contains(got[0], substr) {
		t.Fatalf("failures = %q, want one containing %q", got, substr)
	}
}

// TestPeerDrivesAConnection: the peer's requests reach the Conn's handlers, its
// replies settle the Conn's calls, and closing its input ends the connection.
//
//dsh:test DH-T-SDK-1, DH-T-SDK-3
func TestPeerDrivesAConnection(t *testing.T) {
	conn, p := NewPeer(t, jsonrpc.Options{Methods: map[string]jsonrpc.Handler{
		"echo": func(_ context.Context, params json.RawMessage) (any, error) { return params, nil },
	}})
	if p.Conn() != conn {
		t.Fatal("Conn() is not the connection under test")
	}
	p.Request(jsonrpc.StringID("a"), "echo", map[string]int{"x": 1})
	if r := p.ExpectResponse(jsonrpc.StringID("a")); string(r.Result) != `{"x":1}` {
		t.Fatalf("echo = %s", r.Result)
	}
	p.Request(jsonrpc.Int64ID(2), "nope", json.RawMessage(`[]`))
	if e := p.ExpectError(jsonrpc.Int64ID(2), jsonrpc.ReasonMethodNotFound); e == nil {
		t.Fatal("no error")
	}
	p.SendLine(`nope`)
	p.ExpectError(jsonrpc.ID{}, jsonrpc.ReasonParseError)

	type answer struct {
		Got string `json:"got"`
	}
	results := make(chan error, 3)
	var calls sync.WaitGroup
	// P-C: closing the connection ends every call; then the calls are joined.
	t.Cleanup(func() { _ = conn.Close(); calls.Wait() })
	var got answer
	calls.Add(3)
	go func() { defer calls.Done(); results <- conn.Call(context.Background(), "ask", nil, &got) }()
	req := p.ExpectRequest("ask")
	p.Reply(req.ID, answer{Got: "yes"})
	if err := <-results; err != nil || got.Got != "yes" {
		t.Fatalf("Call = %v, %+v", err, got)
	}
	go func() { defer calls.Done(); results <- conn.Call(context.Background(), "nil", nil, nil) }()
	p.Reply(p.ExpectRequest("nil").ID, nil)
	if err := <-results; err != nil {
		t.Fatalf("Call answered null = %v", err)
	}
	go func() { defer calls.Done(); results <- conn.Call(context.Background(), "fail", nil, nil) }()
	p.ReplyError(p.ExpectRequest("fail").ID, &jsonrpc.Error{Code: jsonrpc.CodeApplication, Message: "m", Reason: "x/gone"})
	if err := <-results; !jsonrpc.HasReason(err, "x/gone") {
		t.Fatalf("Call answered an error = %v", err)
	}

	p.CloseInput()
	<-conn.Done()
	if !jsonrpc.HasReason(conn.Err(), jsonrpc.ReasonClosed) || !strings.Contains(conn.Err().Error(), "input ended") {
		t.Fatalf("Err = %v", conn.Err())
	}
}

// TestPeerExpectationsFail: every Expect form fails the test, naming what came,
// when the frame is not the one expected, none comes within Timeout, or the
// output has ended; frames that do not encode fail the test before they are sent.
//
//dsh:test DH-T-SDK-1
func TestPeerExpectationsFail(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		conn, p := NewPeer(rec, jsonrpc.Options{})
		note := func() {
			if err := conn.Notify("n", nil); err != nil {
				t.Fatal(err)
			}
		}
		note()
		if p.ExpectRequest("m") != nil {
			t.Fatal("ExpectRequest returned a notification")
		}
		rec.wantFailure(t, "want a request for m")
		note()
		if p.ExpectNotification("other") != nil {
			t.Fatal("ExpectNotification returned the wrong notification")
		}
		rec.wantFailure(t, "want a notification of other")
		note()
		if p.ExpectResponse(jsonrpc.Int64ID(1)) != nil {
			t.Fatal("ExpectResponse returned a notification")
		}
		rec.wantFailure(t, "want the response to 1")
		note()
		if p.ExpectError(jsonrpc.Int64ID(1), jsonrpc.ReasonBusy) != nil {
			t.Fatal("ExpectError returned a notification")
		}
		rec.wantFailure(t, "want the response to 1")

		p.Request(jsonrpc.Int64ID(3), "nope", nil)
		if p.ExpectError(jsonrpc.Int64ID(3), jsonrpc.ReasonBusy) != nil {
			t.Fatal("ExpectError returned the wrong reason")
		}
		rec.wantFailure(t, "want error jsonrpc/busy")

		start := time.Now()
		if p.Expect() != nil {
			t.Fatal("Expect returned a frame nobody wrote")
		}
		rec.wantFailure(t, "no frame within")
		if waited := time.Since(start); waited != Timeout {
			t.Fatalf("Expect waited %s, want %s", waited, Timeout)
		}

		p.Send(&jsonrpc.Request{Method: "m"}) // a request id is never null
		rec.wantFailure(t, "encoding a frame to send")
		p.Request(jsonrpc.Int64ID(4), "m", make(chan int))
		got := rec.failed()
		if len(got) == 0 || !strings.Contains(got[0], "encoding chan int") {
			t.Fatalf("failures = %q, want an encoding failure", got)
		}
		if d := describe(&jsonrpc.Response{ID: jsonrpc.Int64ID(1)}); !strings.Contains(d, `"result":null`) {
			t.Fatalf("describe = %s", d)
		}
		if d := describe(&jsonrpc.Request{Method: "m"}); d != "an unencodable frame" {
			t.Fatalf("describe(unencodable) = %s", d)
		}

		_ = conn.Close()
		p.CloseInput()
		<-conn.Done()
		synctest.Wait()
		if p.Expect() != nil {
			t.Fatal("Expect returned a frame after the output ended")
		}
		got = rec.failed()
		if len(got) != 1 || !(strings.Contains(got[0], "has ended") || strings.Contains(got[0], "reading the connection's output")) {
			t.Fatalf("failures = %q, want the end of the output", got)
		}
	})
}

// TestPeerStall: while stalled the peer reads nothing more, so the Conn's Barrier
// waits; Resume lets the frames through in order. Stall and Resume are idempotent.
//
//dsh:test DH-T-SDK-4
func TestPeerStall(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, p := NewPeer(t, jsonrpc.Options{})
		synctest.Wait()
		p.Stall()
		p.Stall()
		for _, m := range []string{"a", "b"} {
			if err := conn.Notify(m, nil); err != nil {
				t.Fatal(err)
			}
		}
		barrier, joined := make(chan error, 1), make(chan struct{})
		t.Cleanup(func() { _ = conn.Close(); <-joined }) // P-C: unblock, then join
		go func() { defer close(joined); barrier <- conn.Barrier(context.Background()) }()
		synctest.Wait()
		if len(barrier) != 0 {
			t.Fatal("Barrier returned while the peer was stalled")
		}
		p.Resume()
		p.Resume()
		p.ExpectNotification("a")
		p.ExpectNotification("b")
		if err := <-barrier; err != nil {
			t.Fatalf("Barrier = %v", err)
		}
	})
}

// TestPeerReleasedWhileStalled: a peer stalled at the end of the test is still
// released and joined by its cleanup.
//
//dsh:test DH-T-SDK-3
func TestPeerReleasedWhileStalled(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		conn, p := NewPeer(t, jsonrpc.Options{})
		synctest.Wait()
		p.Stall()
		if err := conn.Notify("a", nil); err != nil {
			t.Fatal(err)
		}
		if err := conn.Notify("b", nil); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
	})
}
