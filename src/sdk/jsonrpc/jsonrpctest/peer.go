// Package jsonrpctest is a fake peer for tests that drive a jsonrpc.Conn from a
// script (DH-D-SDK-1, plan step 14.2.3). NewPeer serves a Conn over two io.Pipe
// pairs; the returned Peer is the far end: it sends frames (Send, Request, Notify,
// Cancel, Reply), reads the frames the Conn writes (Expect and its typed forms), and
// can stop reading (Stall) to make the Conn's writer block. Everything it starts is
// released, then joined, in t.Cleanup (QG-5). It imports the standard library and
// the codec only (RM-3).
package jsonrpctest

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// Timeout bounds every wait of the kit that should succeed: one Expect, and the join
// of the peer's goroutines at cleanup. Inside a testing/synctest bubble it is virtual.
var Timeout = 10 * time.Second

// Peer is the scripted far end of a connection under test. Its methods are safe
// for concurrent use; a failed expectation fails the test with t.Fatalf.
type Peer struct {
	t    testing.TB
	conn *jsonrpc.Conn

	lines  chan []byte  // frames to send, in order; nil closes the Conn's input
	frames chan frameOf // frames the Conn wrote, closed when the output ends
	quit   chan struct{}

	mu   sync.Mutex
	gate chan struct{} // non-nil while the peer is stalled
}

type frameOf struct {
	f   jsonrpc.Frame
	err error
}

// NewPeer serves a Conn with o and returns it with its scripted peer. The Conn and
// the peer's goroutines are closed and joined when the test ends.
func NewPeer(t testing.TB, o jsonrpc.Options) (*jsonrpc.Conn, *Peer) {
	t.Helper()
	connIn, peerOut := io.Pipe()
	peerIn, connOut := io.Pipe()
	p := &Peer{
		t:      t,
		lines:  make(chan []byte, 256),
		frames: make(chan frameOf, 256),
		quit:   make(chan struct{}),
	}
	p.conn = jsonrpc.Serve(context.Background(), connIn, connOut, o)
	sent, read, ended := make(chan struct{}), make(chan struct{}), make(chan struct{})
	go p.send(peerOut, sent)
	go p.read(peerIn, read)
	go func() {
		// The Conn's output ends with the Conn, so Expect sees the end.
		defer close(ended)
		<-p.conn.Done()
		_ = connOut.Close()
	}()
	t.Cleanup(func() {
		// Unblock, then join (M1-D-GATE-24 rule 3).
		_ = p.conn.Close()
		close(p.quit)
		_ = peerOut.Close()
		_ = connIn.Close()
		_ = peerIn.Close()
		_ = connOut.Close()
		deadline := time.After(Timeout)
		for _, done := range []<-chan struct{}{p.conn.Done(), sent, read, ended} {
			select {
			case <-done:
			case <-deadline:
				t.Errorf("jsonrpctest: the peer was not released within %s", Timeout)
				return
			}
		}
	})
	return p.conn, p
}

// Conn returns the connection under test.
func (p *Peer) Conn() *jsonrpc.Conn { return p.conn }

func (p *Peer) send(w *io.PipeWriter, done chan<- struct{}) {
	defer close(done)
	for {
		select {
		case line := <-p.lines:
			if line == nil {
				_ = w.Close()
				return
			}
			if _, err := w.Write(line); err != nil {
				return
			}
		case <-p.quit:
			return
		}
	}
}

func (p *Peer) read(r io.Reader, done chan<- struct{}) {
	defer close(done)
	defer close(p.frames)
	rd := jsonrpc.NewReader(r, 0)
	for p.flowing() {
		f, err := rd.Read()
		select {
		case p.frames <- frameOf{f, err}:
		case <-p.quit:
			return
		}
		if err != nil {
			return
		}
	}
}

// flowing waits while the peer is stalled; false once the peer is released.
func (p *Peer) flowing() bool {
	for {
		p.mu.Lock()
		gate := p.gate
		p.mu.Unlock()
		if gate == nil {
			return true
		}
		select {
		case <-gate:
		case <-p.quit:
			return false
		}
	}
}

// Stall stops the peer reading the Conn's output, so the Conn's writer blocks on
// its next frame. A frame already being read when Stall is called is still
// delivered to Expect.
func (p *Peer) Stall() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gate == nil {
		p.gate = make(chan struct{})
	}
}

// Resume undoes Stall.
func (p *Peer) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.gate != nil {
		close(p.gate)
		p.gate = nil
	}
}

// Send queues f for the Conn. Frames reach the Conn in the order they are sent.
func (p *Peer) Send(f jsonrpc.Frame) {
	p.t.Helper()
	b, err := jsonrpc.Encode(f)
	if err != nil {
		p.t.Fatalf("jsonrpctest: encoding a frame to send: %v", err)
		return
	}
	p.queue(append(b, '\n'))
}

// SendLine queues one raw line (a newline is added), for malformed input.
func (p *Peer) SendLine(line string) { p.queue([]byte(line + "\n")) }

// CloseInput ends the Conn's input once every frame queued before it is sent.
func (p *Peer) CloseInput() { p.queue(nil) }

func (p *Peer) queue(line []byte) {
	select {
	case p.lines <- line:
	case <-p.quit:
	}
}

// Request sends request id for method with params (any value json.Marshal accepts,
// or a json.RawMessage; nil sends none).
func (p *Peer) Request(id jsonrpc.ID, method string, params any) {
	p.t.Helper()
	if raw, ok := p.raw(params); ok {
		p.Send(&jsonrpc.Request{ID: id, Method: method, Params: raw})
	}
}

// Notify sends a notification.
func (p *Peer) Notify(method string, params any) {
	p.t.Helper()
	if raw, ok := p.raw(params); ok {
		p.Send(&jsonrpc.Notification{Method: method, Params: raw})
	}
}

// Cancel sends $/cancel for request id.
func (p *Peer) Cancel(id jsonrpc.ID) {
	p.t.Helper()
	p.Notify(jsonrpc.CancelMethod, map[string]jsonrpc.ID{"id": id})
}

// Reply answers the Conn's request id with result.
func (p *Peer) Reply(id jsonrpc.ID, result any) {
	p.t.Helper()
	raw, ok := p.raw(result)
	if !ok {
		return
	}
	if raw == nil {
		raw = json.RawMessage("null")
	}
	p.Send(&jsonrpc.Response{ID: id, Result: raw})
}

// ReplyError answers the Conn's request id with e.
func (p *Peer) ReplyError(id jsonrpc.ID, e *jsonrpc.Error) {
	p.t.Helper()
	p.Send(&jsonrpc.Response{ID: id, Error: e})
}

// raw encodes v; false (after failing the test) when it does not encode.
func (p *Peer) raw(v any) (json.RawMessage, bool) {
	p.t.Helper()
	switch v := v.(type) {
	case nil:
		return nil, true
	case json.RawMessage:
		return v, true
	}
	b, err := json.Marshal(v)
	if err != nil {
		p.t.Fatalf("jsonrpctest: encoding %T: %v", v, err)
		return nil, false
	}
	return b, true
}

// Expect returns the next frame the Conn writes, failing the test when none comes
// within Timeout or the output ended.
func (p *Peer) Expect() jsonrpc.Frame {
	p.t.Helper()
	select {
	case r, ok := <-p.frames:
		switch {
		case !ok:
			p.t.Fatalf("jsonrpctest: the connection's output has ended")
		case r.err != nil:
			p.t.Fatalf("jsonrpctest: reading the connection's output: %v", r.err)
		default:
			return r.f
		}
	case <-time.After(Timeout):
		p.t.Fatalf("jsonrpctest: no frame within %s", Timeout)
	}
	return nil
}

// ExpectRequest expects the next frame to be a request for method and returns it.
func (p *Peer) ExpectRequest(method string) *jsonrpc.Request {
	p.t.Helper()
	f := p.Expect()
	if r, ok := f.(*jsonrpc.Request); ok && r.Method == method {
		return r
	}
	if f != nil {
		p.t.Fatalf("jsonrpctest: got %s, want a request for %s", describe(f), method)
	}
	return nil
}

// ExpectNotification expects the next frame to be a notification of method.
func (p *Peer) ExpectNotification(method string) *jsonrpc.Notification {
	p.t.Helper()
	f := p.Expect()
	if n, ok := f.(*jsonrpc.Notification); ok && n.Method == method {
		return n
	}
	if f != nil {
		p.t.Fatalf("jsonrpctest: got %s, want a notification of %s", describe(f), method)
	}
	return nil
}

// ExpectResponse expects the next frame to be the response to id.
func (p *Peer) ExpectResponse(id jsonrpc.ID) *jsonrpc.Response {
	p.t.Helper()
	f := p.Expect()
	if r, ok := f.(*jsonrpc.Response); ok && r.ID == id {
		return r
	}
	if f != nil {
		p.t.Fatalf("jsonrpctest: got %s, want the response to %s", describe(f), id)
	}
	return nil
}

// ExpectError expects the next frame to be an error response to id with reason.
func (p *Peer) ExpectError(id jsonrpc.ID, reason string) *jsonrpc.Error {
	p.t.Helper()
	r := p.ExpectResponse(id)
	if r == nil {
		return nil
	}
	if r.Error == nil || r.Error.Reason != reason {
		p.t.Fatalf("jsonrpctest: response %s is %s, want error %s", id, describe(r), reason)
		return nil
	}
	return r.Error
}

// describe renders a frame for a failure message.
func describe(f jsonrpc.Frame) string {
	b, err := jsonrpc.Encode(f)
	if err != nil {
		return "an unencodable frame"
	}
	return string(b)
}
