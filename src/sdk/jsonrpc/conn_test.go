package jsonrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

const waitFor = 10 * time.Second

// peer is the far end of a connection under test, driven line by line.
type peer struct {
	t     *testing.T
	c     *Conn
	lines chan string
	in    *io.PipeWriter
	out   *Reader
}

func newPeer(t *testing.T, ctx context.Context, o Options) *peer {
	t.Helper()
	toConn, in := io.Pipe()
	fromConn, out := io.Pipe()
	p := &peer{t: t, lines: make(chan string, 64), in: in, out: NewReader(fromConn, 0)}
	p.c = Serve(ctx, toConn, out, o)
	sent := make(chan struct{})
	go func() {
		defer close(sent)
		for l := range p.lines {
			if _, err := in.Write([]byte(l + "\n")); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		p.c.Close()
		close(p.lines)
		in.Close()
		fromConn.Close()
		<-p.c.Done()
		<-sent
	})
	return p
}

func (p *peer) send(line string) { p.lines <- line }

func (p *peer) recv() Frame {
	p.t.Helper()
	type res struct {
		f   Frame
		err error
	}
	ch := make(chan res, 1)
	go func() {
		f, err := p.out.Read()
		ch <- res{f, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			p.t.Fatalf("peer read: %v", r.err)
		}
		return r.f
	case <-time.After(waitFor):
		p.t.Fatal("peer read timed out")
		return nil
	}
}

func (p *peer) recvResponse(id ID) *Response {
	p.t.Helper()
	r, ok := p.recv().(*Response)
	if !ok {
		p.t.Fatal("peer expected a response")
	}
	if r.ID != id {
		p.t.Fatalf("response id = %s, want %s", r.ID, id)
	}
	return r
}

func (p *peer) recvError(id ID, reason string) *Error {
	p.t.Helper()
	r := p.recvResponse(id)
	if r.Error == nil || r.Error.Reason != reason {
		p.t.Fatalf("response %s = %+v (error %v), want reason %s", id, r, r.Error, reason)
	}
	return r.Error
}

// async runs fn on its own goroutine and returns its result channel.
func async(t *testing.T, fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	// fn returns once the connection it waits on closes, which the peer's own
	// cleanup guarantees; the buffered channel lets it exit unread.
	t.Cleanup(func() {})
	return ch
}

func await[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(waitFor):
		t.Fatal("timed out")
		var zero T
		return zero
	}
}

func awaitDone(t *testing.T, c *Conn) {
	t.Helper()
	select {
	case <-c.Done():
	case <-time.After(waitFor):
		t.Fatal("the connection did not end")
	}
}

type msg struct {
	A int `json:"a"`
}

func TestServeAnswersRequests(t *testing.T) {
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	p := newPeer(t, context.Background(), Options{Methods: map[string]Handler{
		"echo":     func(_ context.Context, params json.RawMessage) (any, error) { return params, nil },
		"reason":   func(context.Context, json.RawMessage) (any, error) { return nil, reasonErr{"session/gone", "s1"} },
		"secret":   func(context.Context, json.RawMessage) (any, error) { return nil, errors.New("/etc/shadow") },
		"panics":   func(context.Context, json.RawMessage) (any, error) { panic("boom") },
		"chan":     func(context.Context, json.RawMessage) (any, error) { return make(chan int), nil },
		"oversize": func(context.Context, json.RawMessage) (any, error) { return strings.Repeat("x", 1024), nil },
	}, MaxLineBytes: 512})

	p.send(`{"jsonrpc":"2.0","id":1,"method":"echo","params":{"a":1}}`)
	if r := p.recvResponse(Int64ID(1)); r.Error != nil || string(r.Result) != `{"a":1}` {
		t.Fatalf("echo = %+v", r)
	}
	p.send(`{"jsonrpc":"2.0","id":"s","method":"nope"}`)
	p.recvError(StringID("s"), ReasonMethodNotFound)
	p.send(`{"jsonrpc":"2.0","id":2,"method":"reason"}`)
	if e := p.recvError(Int64ID(2), "session/gone"); e.Code != CodeApplication || e.Message != "s1" {
		t.Fatalf("reason error = %+v", e)
	}
	p.send(`{"jsonrpc":"2.0","id":3,"method":"secret"}`)
	if e := p.recvError(Int64ID(3), ReasonInternal); strings.Contains(e.Message, "shadow") {
		t.Fatalf("internal text crossed the wire: %q", e.Message)
	}
	p.send(`{"jsonrpc":"2.0","id":4,"method":"panics"}`)
	p.recvError(Int64ID(4), ReasonInternal)
	p.send(`{"jsonrpc":"2.0","id":5,"method":"chan"}`)
	p.recvError(Int64ID(5), ReasonInternal)
	p.send(`{"jsonrpc":"2.0","id":6,"method":"oversize"}`)
	p.recvError(Int64ID(6), ReasonLineTooLong)
}

func TestServeKeepsReadingAfterBadLines(t *testing.T) {
	p := newPeer(t, context.Background(), Options{Methods: map[string]Handler{
		"ok": func(context.Context, json.RawMessage) (any, error) { return true, nil },
	}})
	p.send(`nope`)
	p.recvError(ID{}, ReasonParseError)
	p.send(`[{"jsonrpc":"2.0","id":1,"method":"ok"}]`)
	p.recvError(ID{}, ReasonInvalidRequest)
	p.send(`{"jsonrpc":"2.0","id":1,"method":"ok"}`)
	if r := p.recvResponse(Int64ID(1)); string(r.Result) != "true" {
		t.Fatalf("ok = %+v", r)
	}
}

func TestServeClosesOnOversizedLine(t *testing.T) {
	p := newPeer(t, context.Background(), Options{MaxLineBytes: 64})
	p.send(strings.Repeat("z", 65))
	p.recvError(ID{}, ReasonLineTooLong)
	awaitDone(t, p.c)
	if !HasReason(p.c.Err(), ReasonLineTooLong) {
		t.Fatalf("Err = %v, want %s", p.c.Err(), ReasonLineTooLong)
	}
}

func TestServeInFlightBound(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	p := newPeer(t, context.Background(), Options{MaxInFlight: 1, Methods: map[string]Handler{
		"hold": func(context.Context, json.RawMessage) (any, error) {
			started <- struct{}{}
			<-release
			return "held", nil
		},
	}})
	p.send(`{"jsonrpc":"2.0","id":1,"method":"hold"}`)
	await(t, started)
	p.send(`{"jsonrpc":"2.0","id":1,"method":"hold"}`)
	p.recvError(Int64ID(1), ReasonInvalidRequest)
	p.send(`{"jsonrpc":"2.0","id":2,"method":"hold"}`)
	p.recvError(Int64ID(2), ReasonBusy)
	close(release)
	if r := p.recvResponse(Int64ID(1)); string(r.Result) != `"held"` {
		t.Fatalf("hold = %+v", r)
	}
}

func TestServeIncomingCancel(t *testing.T) {
	started := make(chan struct{}, 1)
	p := newPeer(t, context.Background(), Options{Methods: map[string]Handler{
		"wait": func(ctx context.Context, _ json.RawMessage) (any, error) {
			started <- struct{}{}
			<-ctx.Done()
			return "late", nil
		},
	}})
	p.send(`{"jsonrpc":"2.0","id":7,"method":"wait"}`)
	await(t, started)
	p.send(`{"jsonrpc":"2.0","method":"$/cancel","params":{"id":99}}`)
	p.send(`{"jsonrpc":"2.0","method":"$/cancel","params":{"id":{}}}`)
	p.send(`{"jsonrpc":"2.0","method":"$/cancel","params":[1]}`)
	p.send(`{"jsonrpc":"2.0","method":"$/cancel","params":{"id":7}}`)
	p.recvError(Int64ID(7), ReasonCancelled)
}

func TestServeNotificationsAndStrayResponses(t *testing.T) {
	notes := make(chan string, 8)
	faults := make(chan error, 8)
	p := newPeer(t, context.Background(), Options{
		OnNotify: func(method string, _ json.RawMessage) { notes <- method },
		OnError:  func(err error) { faults <- err },
	})
	for _, m := range []string{"a", "b", "c"} {
		p.send(`{"jsonrpc":"2.0","method":"` + m + `"}`)
	}
	for _, want := range []string{"a", "b", "c"} {
		if got := await(t, notes); got != want {
			t.Fatalf("notification %q, want %q", got, want)
		}
	}
	p.send(`{"jsonrpc":"2.0","id":42,"result":1}`)
	if err := await(t, faults); !HasReason(err, ReasonUnknownResponse) {
		t.Fatalf("fault = %v, want %s", err, ReasonUnknownResponse)
	}
	p.send(`{"jsonrpc":"2.0","id":null,"error":{"code":-32700,"message":"bad"}}`)
	if err := await(t, faults); !HasReason(err, ReasonParseError) {
		t.Fatalf("fault = %v, want %s", err, ReasonParseError)
	}
}

func TestCall(t *testing.T) {
	p := newPeer(t, context.Background(), Options{})
	var got msg
	done := async(t, func() error { return p.c.Call(context.Background(), "get", msg{A: 1}, &got) })
	req, ok := p.recv().(*Request)
	if !ok || req.Method != "get" || string(req.Params) != `{"a":1}` {
		t.Fatalf("peer received %+v", req)
	}
	p.send(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"result":{"a":2}}`)
	if err := await(t, done); err != nil || got.A != 2 {
		t.Fatalf("Call = %v, %+v", err, got)
	}

	done = async(t, func() error { return p.c.Call(context.Background(), "fail", json.RawMessage(`[1]`), nil) })
	req = p.recv().(*Request)
	p.send(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"error":{"code":-32000,"message":"m","data":{"code":"session/gone"}}}`)
	if err := await(t, done); !HasReason(err, "session/gone") {
		t.Fatalf("Call = %v, want session/gone", err)
	}

	done = async(t, func() error { return p.c.Call(context.Background(), "typed", nil, &got) })
	req = p.recv().(*Request)
	if req.Params != nil {
		t.Fatalf("nil params sent as %s", req.Params)
	}
	p.send(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"result":"not an object"}`)
	if err := await(t, done); err == nil || !strings.Contains(err.Error(), "decoding the result of typed") {
		t.Fatalf("Call = %v, want a decode error", err)
	}

	done = async(t, func() error { return p.c.Call(context.Background(), "untyped", nil, nil) })
	req = p.recv().(*Request)
	p.send(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"result":null}`)
	if err := await(t, done); err != nil {
		t.Fatalf("Call = %v", err)
	}

	if err := p.c.Call(context.Background(), "m", make(chan int), nil); !HasReason(err, ReasonInvalidParams) {
		t.Fatalf("Call(chan) = %v, want %s", err, ReasonInvalidParams)
	}
	if err := p.c.Call(context.Background(), "", nil, nil); !HasReason(err, ReasonInvalidRequest) {
		t.Fatalf("Call(no method) = %v, want %s", err, ReasonInvalidRequest)
	}
}

func TestCallCancelSendsCancel(t *testing.T) {
	faults := make(chan error, 1)
	p := newPeer(t, context.Background(), Options{OnError: func(err error) { faults <- err }})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := async(t, func() error { return p.c.Call(ctx, "slow", nil, nil) })
	req := p.recv().(*Request)
	cancel()
	if err := await(t, done); !errors.Is(err, context.Canceled) {
		t.Fatalf("Call = %v, want context.Canceled", err)
	}
	n, ok := p.recv().(*Notification)
	if !ok || n.Method != CancelMethod || string(n.Params) != `{"id":`+req.ID.String()+`}` {
		t.Fatalf("peer received %+v, want $/cancel of %s", n, req.ID)
	}
	p.send(`{"jsonrpc":"2.0","id":` + req.ID.String() + `,"result":1}`)
	if err := await(t, faults); !HasReason(err, ReasonUnknownResponse) {
		t.Fatalf("late response fault = %v", err)
	}
}

func TestCallPendingBound(t *testing.T) {
	p := newPeer(t, context.Background(), Options{MaxPending: 2})
	first := async(t, func() error { return p.c.Call(context.Background(), "a", nil, nil) })
	second := async(t, func() error { return p.c.Call(context.Background(), "b", nil, nil) })
	p.recv()
	p.recv()
	if err := p.c.Call(context.Background(), "c", nil, nil); !HasReason(err, ReasonTooManyPending) {
		t.Fatalf("third Call = %v, want %s", err, ReasonTooManyPending)
	}
	p.c.Close()
	for _, ch := range []<-chan error{first, second} {
		if err := await(t, ch); !HasReason(err, ReasonClosed) {
			t.Fatalf("pending Call = %v, want %s", err, ReasonClosed)
		}
	}
	if err := p.c.Call(context.Background(), "d", nil, nil); !HasReason(err, ReasonClosed) {
		t.Fatalf("Call after Close = %v", err)
	}
	if err := p.c.Notify("n", nil); !HasReason(err, ReasonClosed) {
		t.Fatalf("Notify after Close = %v", err)
	}
	if err := p.c.Barrier(context.Background()); !HasReason(err, ReasonClosed) {
		t.Fatalf("Barrier after Close = %v", err)
	}
}

func TestNotifyAndBarrier(t *testing.T) {
	p := newPeer(t, context.Background(), Options{MaxLineBytes: 64})
	if err := p.c.Notify("n", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	barrier := async(t, func() error { return p.c.Barrier(context.Background()) })
	if n, ok := p.recv().(*Notification); !ok || n.Method != "n" || string(n.Params) != `{"a":1}` {
		t.Fatalf("peer received %+v", n)
	}
	if err := await(t, barrier); err != nil {
		t.Fatalf("Barrier = %v", err)
	}
	if err := p.c.Notify("n", make(chan int)); !HasReason(err, ReasonInvalidParams) {
		t.Fatalf("Notify(chan) = %v", err)
	}
	if err := p.c.Notify("n", []string{strings.Repeat("x", 64)}); !HasReason(err, ReasonLineTooLong) {
		t.Fatalf("Notify(oversized) = %v", err)
	}
	if err := p.c.Call(context.Background(), "n", []string{strings.Repeat("x", 64)}, nil); !HasReason(err, ReasonLineTooLong) {
		t.Fatalf("Call(oversized) = %v", err)
	}
}

func TestBarrierContext(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { pw.Close() })
	blocked, _ := io.Pipe()
	t.Cleanup(func() { blocked.Close() })
	c := Serve(context.Background(), blocked, pw, Options{})
	t.Cleanup(func() { c.Close() })
	if err := c.Notify("stuck", nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Barrier(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Barrier = %v, want context.Canceled", err)
	}
	pr.Close()
	awaitDone(t, c)
	if !HasReason(c.Err(), ReasonClosed) {
		t.Fatalf("Err after a write failure = %v", c.Err())
	}
}

func TestServeEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := newPeer(t, ctx, Options{})
	if p.c.Err() != nil {
		t.Fatalf("Err while running = %v", p.c.Err())
	}
	cancel()
	awaitDone(t, p.c)
	if !HasReason(p.c.Err(), ReasonClosed) || !strings.Contains(p.c.Err().Error(), "context") {
		t.Fatalf("Err = %v", p.c.Err())
	}

	q := newPeer(t, context.Background(), Options{})
	q.in.Close()
	awaitDone(t, q.c)
	if !HasReason(q.c.Err(), ReasonClosed) || !strings.Contains(q.c.Err().Error(), "input ended") {
		t.Fatalf("Err at EOF = %v", q.c.Err())
	}
}

func TestQueueBackpressure(t *testing.T) {
	q := newQueue(10)
	if err := q.push(make([]byte, 8), nil, false); err != nil {
		t.Fatal(err)
	}
	if err := q.push(make([]byte, 8), nil, true); err != nil {
		t.Fatal("a control frame must not wait")
	}
	pushed := async(t, func() error { return q.push(make([]byte, 8), nil, false) })
	for range 2 {
		it, ok := q.pop()
		if !ok {
			t.Fatal("pop failed")
		}
		q.release(len(it.line))
	}
	if err := await(t, pushed); err != nil {
		t.Fatal(err)
	}
	q.close()
	if _, ok := q.pop(); ok {
		t.Fatal("pop after close")
	}
	if err := q.push(nil, nil, false); !HasReason(err, ReasonClosed) {
		t.Fatalf("push after close = %v", err)
	}
}
