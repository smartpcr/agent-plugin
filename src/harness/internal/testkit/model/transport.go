// Package model is the test kit's scripted model (DH-D-UTIL-6, TC-06a). A
// Transport is an http.RoundTripper that answers each request with the next
// scripted Turn as a text/event-stream body, the wire shape of a streaming model
// endpoint; an Adapter is an llm.Adapter that posts the request through any
// RoundTripper and decodes that stream into vocab chunks. Together they let a
// test drive the real llm runtime, a real session and a real sink through one
// attempt with no network and no provider key.
//
// The wire is the kit's own minimal dialect, one SSE event per line pair:
//
//	event: text     data: {"text":"..."}
//	event: tool     data: {"id":"c1","name":"echo","args":{...}}
//	event: usage    data: {"input":3,"output":2}
//	event: finish   data: {"reason":"stop"}
//	event: error    data: {"code":"llm/server","message":"..."}
//
// A provider driver's own decoder is tested against its recorded dialect by the
// driver's area; this one exists so tests above the driver need none.
//
// Scripted (scripted.go) is the chunk-level scripted model: an llm.Adapter that
// plays vocab chunks, a failure, or a block until cancelled, with no wire at
// all, for tests that script exactly what a driver yields.
package model

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// Event is one SSE event of a scripted turn.
type Event struct {
	Name string // text, tool, usage, finish or error
	Data string // the JSON payload
}

// Turn is one scripted response.
type Turn struct {
	// Status is the HTTP status; 0 is 200. A turn with another status has no
	// stream: its body is Events' text, if any.
	Status int
	Events []Event
	// Hang holds the stream open after Events until the request is cancelled,
	// the shape of a provider that stops sending.
	Hang bool
}

// Text is an event appending s to the reply.
func Text(s string) Event { return Event{"text", mustJSON(map[string]string{"text": s})} }

// Tool is one complete tool call block: id, tool name and the JSON object of
// its arguments ("" is {}). It closes a text block before it, so text after it
// opens a new one.
func Tool(id, name, args string) Event {
	if args == "" {
		args = "{}"
	}
	return Event{"tool", mustJSON(map[string]any{"id": id, "name": name, "args": json.RawMessage(args)})}
}

// Calls is a complete turn that asks for the tool calls in order and finishes
// with tool_calls.
func Calls(calls ...Event) Turn {
	return Turn{Events: append(append([]Event(nil), calls...), Usage(vocab.Usage{Input: 1, Output: 1}), Finish(vocab.FinishToolCalls))}
}

// Usage is a usage event.
func Usage(u vocab.Usage) Event { return Event{"usage", mustJSON(u)} }

// Finish is a finish event.
func Finish(r vocab.FinishReason) Event {
	return Event{"finish", mustJSON(map[string]string{"reason": string(r)})}
}

// Error is an in-stream failure event.
func Error(code vocab.Code, message string) Event {
	return Event{"error", mustJSON(map[string]string{"code": string(code), "message": message})}
}

// Reply is a complete turn: the text in one delta, usage and a stop.
func Reply(text string) Turn {
	return Turn{Events: []Event{Text(text), Usage(vocab.Usage{Input: 1, Output: 1}), Finish(vocab.FinishStop)}}
}

// Status is a turn refused with an HTTP status and no stream.
func Status(code int) Turn { return Turn{Status: code} }

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// Request is one request the transport received.
type Request struct {
	Method, URL string
	Header      http.Header
	Body        []byte
}

// Transport serves scripted turns in order. A request with no turn left fails the
// test and gets a 599 response; a turn still scripted when the test ends fails it
// too, so a script always matches the calls made. Hanging bodies still open when
// the test ends are closed, so no read outlives the test.
//
// A test waits on the transport through signals, never by polling (M1-D-GATE-24):
// WaitRequests blocks until a number of requests has arrived, and PrefixServed is
// closed once the client has consumed a response's scripted events.
type Transport struct {
	t        testing.TB
	mu       sync.Mutex
	turns    []Turn
	requests []Request
	bodies   []*hanging
	// arrived is closed and replaced each time a request is recorded, waking
	// every WaitRequests call to recheck the count.
	arrived chan struct{}
	// served holds the PrefixServed channel of each request number (1-based),
	// made by whichever comes first: the request or a PrefixServed call.
	served map[int]chan struct{}
}

// NewTransport is a transport scripted with turns, checked when the test ends.
func NewTransport(t testing.TB, turns ...Turn) *Transport {
	t.Helper()
	tr := &Transport{t: t, turns: turns, arrived: make(chan struct{}), served: map[int]chan struct{}{}}
	t.Cleanup(func() {
		tr.mu.Lock()
		defer tr.mu.Unlock()
		for _, b := range tr.bodies {
			_ = b.Close()
		}
		tr.bodies = nil
		if n := len(tr.turns); n != 0 {
			t.Errorf("model: %d scripted turn(s) never requested", n)
		}
	})
	return tr
}

// Script appends turns to the script.
func (tr *Transport) Script(turns ...Turn) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.turns = append(tr.turns, turns...)
}

// Requests is every request received so far, in order.
func (tr *Transport) Requests() []Request {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return append([]Request(nil), tr.requests...)
}

// WaitRequests blocks until the transport has received at least n requests and
// returns nil, or returns ctx's error (its cause, when one was given) when ctx
// ends first. It is woken by each request; it does not poll.
func (tr *Transport) WaitRequests(ctx context.Context, n int) error {
	for {
		tr.mu.Lock()
		got, arrived := len(tr.requests), tr.arrived
		tr.mu.Unlock()
		if got >= n {
			return nil
		}
		select {
		case <-arrived:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// PrefixServed is closed once the client has read the whole scripted text of the
// response to request n (1-based, counted like WaitRequests) and asked for more:
// a hanging turn's reader is then blocked after its prefix, and a complete turn's
// reader has reached the end of its stream. The kit's adapter decodes on the
// reading goroutine, so by then every event of the turn has been handed to the
// llm runtime. A request answered with a status, or never made, never closes it.
func (tr *Transport) PrefixServed(n int) <-chan struct{} {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.servedLocked(n)
}

func (tr *Transport) servedLocked(n int) chan struct{} {
	ch, ok := tr.served[n]
	if !ok {
		ch = make(chan struct{})
		tr.served[n] = ch
	}
	return ch
}

// RoundTrip answers req with the next turn.
func (tr *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(req.Body)
		_ = req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	tr.mu.Lock()
	tr.requests = append(tr.requests, Request{Method: req.Method, URL: req.URL.String(), Header: req.Header.Clone(), Body: body})
	served := tr.servedLocked(len(tr.requests))
	close(tr.arrived)
	tr.arrived = make(chan struct{})
	var turn Turn
	ok := len(tr.turns) > 0
	if ok {
		turn, tr.turns = tr.turns[0], tr.turns[1:]
	}
	tr.mu.Unlock()
	if !ok {
		tr.t.Errorf("model: unscripted request %s %s", req.Method, req.URL)
		return respond(req, 599, "text/plain", io.NopCloser(strings.NewReader("no scripted turn"))), nil
	}
	if turn.Status != 0 && turn.Status != http.StatusOK {
		return respond(req, turn.Status, "text/plain", io.NopCloser(strings.NewReader(encode(turn.Events)))), nil
	}
	var rc io.ReadCloser = &complete{Reader: strings.NewReader(encode(turn.Events)), served: signal(served)}
	if turn.Hang {
		h := newHanging(encode(turn.Events), req, signal(served))
		tr.mu.Lock()
		tr.bodies = append(tr.bodies, h)
		tr.mu.Unlock()
		rc = h
	}
	return respond(req, http.StatusOK, "text/event-stream", rc), nil
}

func respond(req *http.Request, status int, typ string, body io.ReadCloser) *http.Response {
	return &http.Response{
		Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), StatusCode: status,
		Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
		Header: http.Header{"Content-Type": {typ}}, Body: body, ContentLength: -1, Request: req,
	}
}

// encode is the SSE text of events.
func encode(events []Event) string {
	var b bytes.Buffer
	for _, e := range events {
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", e.Name, e.Data)
	}
	return b.String()
}

// signal is a once-only close of ch.
func signal(ch chan struct{}) func() { return sync.OnceFunc(func() { close(ch) }) }

// complete yields its text, then io.EOF; the first read past the text closes the
// request's PrefixServed channel.
type complete struct {
	*strings.Reader
	served func()
}

func (c *complete) Read(p []byte) (int, error) {
	if c.Len() == 0 {
		c.served()
	}
	return c.Reader.Read(p)
}

func (c *complete) Close() error { return nil }

// hanging yields its text, then blocks until the request is cancelled or the body
// is closed. Close is idempotent and unblocks a read in progress. The first read
// past the text closes the request's PrefixServed channel before it blocks.
type hanging struct {
	*strings.Reader
	req    *http.Request
	once   sync.Once
	closed chan struct{}
	served func()
}

func newHanging(text string, req *http.Request, served func()) *hanging {
	return &hanging{Reader: strings.NewReader(text), req: req, closed: make(chan struct{}), served: served}
}

func (h *hanging) Read(p []byte) (int, error) {
	if h.Len() > 0 {
		return h.Reader.Read(p)
	}
	h.served()
	select {
	case <-h.req.Context().Done():
		return 0, h.req.Context().Err()
	case <-h.closed:
		return 0, io.ErrClosedPipe
	}
}

func (h *hanging) Close() error {
	h.once.Do(func() { close(h.closed) })
	return nil
}
