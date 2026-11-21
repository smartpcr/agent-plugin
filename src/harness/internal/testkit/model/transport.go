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
//	event: usage    data: {"input":3,"output":2}
//	event: finish   data: {"reason":"stop"}
//	event: error    data: {"code":"llm/server","message":"..."}
//
// A provider driver's own decoder is tested against its recorded dialect by the
// driver's area; this one exists so tests above the driver need none.
package model

import (
	"bytes"
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
	Name string // text, usage, finish or error
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
// too, so a script always matches the calls made.
type Transport struct {
	t        testing.TB
	mu       sync.Mutex
	turns    []Turn
	requests []Request
}

// NewTransport is a transport scripted with turns, checked when the test ends.
func NewTransport(t testing.TB, turns ...Turn) *Transport {
	t.Helper()
	tr := &Transport{t: t, turns: turns}
	t.Cleanup(func() {
		tr.mu.Lock()
		defer tr.mu.Unlock()
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
	var rc io.ReadCloser = io.NopCloser(strings.NewReader(encode(turn.Events)))
	if turn.Hang {
		rc = &hanging{Reader: strings.NewReader(encode(turn.Events)), req: req}
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

// hanging yields its text, then blocks until the request is cancelled.
type hanging struct {
	*strings.Reader
	req *http.Request
}

func (h *hanging) Read(p []byte) (int, error) {
	if h.Len() > 0 {
		return h.Reader.Read(p)
	}
	<-h.req.Context().Done()
	return 0, h.req.Context().Err()
}

func (h *hanging) Close() error { return nil }
