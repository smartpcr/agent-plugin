// Package faultserver is the test kit's fault-injecting model server
// (DH-D-UTIL-6, util-test-support.md §6.1 "faultserver"; upstream
// test-support.llm-mock-server): a real HTTP server on a loopback port that
// answers each request with the next scripted Behaviour -- a complete SSE
// stream, an HTTP status such as 429 or 5xx, a connection reset, a stall, or a
// malformed chunk -- so a client is tested against the wire, its own transport
// and decoder included.
//
// Scripts are kept per key: a test-supplied function computes a key from each
// request (a header, a path), and each key has its own cursor, so concurrent
// callers never share one arrival-ordered script and one server gives each
// caller deterministic faults (debt unit llm-mock-server.2). A request with no
// behaviour left for its key fails the test and is answered 599; a behaviour
// never requested fails the test when it ends. Mix draws a seeded random mix and
// logs the seed, so a run can be repeated; the mix is test pressure, not a
// forecast of how often a provider fails (llm-mock-server.1).
//
// Streams use the kit's SSE dialect (package model), so model.Adapter decodes
// them. A refused connection is not a server behaviour: it needs no listener and
// is scripted at the egress seam with egresstest.Fail (llm-mock-server.3).
//
// The server dials nothing and listens only on loopback; reach it through an
// egresstest recorder that allows its URL, or through any client in a test.
// It is not usable inside a synctest bubble: a socket read is not durably
// blocking (util-test-support.md §10 item 3).
package faultserver

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

type kind int

const (
	kindStream kind = iota
	kindStatus
	kindReset
	kindStall
	kindMalformed
)

// Behaviour is how the server answers one request.
type Behaviour struct {
	kind       kind
	status     int
	retryAfter time.Duration
	events     []model.Event
}

// String names the behaviour, for a failure message and Mix's log.
func (b Behaviour) String() string {
	switch b.kind {
	case kindStream:
		return fmt.Sprintf("stream(%d events)", len(b.events))
	case kindStatus:
		return fmt.Sprintf("status(%d)", b.status)
	case kindReset:
		return fmt.Sprintf("reset(after %d events)", len(b.events))
	case kindStall:
		return fmt.Sprintf("stall(after %d events)", len(b.events))
	case kindMalformed:
		return fmt.Sprintf("malformed(after %d events)", len(b.events))
	}
	return "unknown"
}

// Stream answers 200 with a text/event-stream body of events, then ends it.
func Stream(events ...model.Event) Behaviour { return Behaviour{kind: kindStream, events: events} }

// Reply answers with model.Reply(text): one text delta, usage and a stop.
func Reply(text string) Behaviour { return Stream(model.Reply(text).Events...) }

// Status answers with code and a JSON error body and no stream; a positive
// retryAfter is sent as a Retry-After header in whole seconds (rounded up).
func Status(code int, retryAfter time.Duration) Behaviour {
	return Behaviour{kind: kindStatus, status: code, retryAfter: retryAfter}
}

// Reset sends the headers and prefix, then resets the connection (a TCP reset
// where the platform allows it, else an abrupt close), so the client's read
// fails mid-stream. With no prefix the connection is reset before any byte of a
// response, and the client's request fails.
func Reset(prefix ...model.Event) Behaviour { return Behaviour{kind: kindReset, events: prefix} }

// Stall sends the headers and prefix and then nothing, until the client goes
// away or the test ends: a provider that stops sending.
func Stall(prefix ...model.Event) Behaviour { return Behaviour{kind: kindStall, events: prefix} }

// Malformed sends prefix and then an event whose data is cut off mid-JSON, and
// ends the stream.
func Malformed(prefix ...model.Event) Behaviour {
	return Behaviour{kind: kindMalformed, events: prefix}
}

// MalformedChunk is the event Malformed appends.
const MalformedChunk = "event: text\ndata: {\"text\":\"cut\n\n"

// Weighted is one choice of Mix.
type Weighted struct {
	Weight    int
	Behaviour Behaviour
}

// Mix draws n behaviours from choices with probability proportional to their
// weights, from a generator seeded with seed, and logs the seed and the draw so
// a failing run can be repeated. Equal seeds give equal mixes.
func Mix(t testing.TB, seed uint64, n int, choices ...Weighted) []Behaviour {
	t.Helper()
	total := 0
	for _, c := range choices {
		if c.Weight < 0 {
			t.Fatalf("faultserver: Mix: negative weight %d", c.Weight)
		}
		total += c.Weight
	}
	if total == 0 {
		t.Fatalf("faultserver: Mix: no choice has weight")
	}
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	out := make([]Behaviour, 0, n)
	for range n {
		pick := rng.IntN(total)
		for _, c := range choices {
			if pick < c.Weight {
				out = append(out, c.Behaviour)
				break
			}
			pick -= c.Weight
		}
	}
	t.Logf("faultserver: mix seed %d: %v", seed, out)
	return out
}

// HeaderKey is a key function reading the request header name.
func HeaderKey(name string) func(*http.Request) string {
	return func(r *http.Request) string { return r.Header.Get(name) }
}

// Options configure a server.
type Options struct {
	// Key computes the script key of a request; nil keys every request "".
	Key func(*http.Request) string
}

// Request is one request the server received.
type Request struct {
	Key    string
	Method string
	Path   string
	Header http.Header
	Body   []byte
	// Behaviour is what answered it; Unscripted when nothing was left.
	Behaviour  Behaviour
	Unscripted bool
}

// Server is a running fault server.
type Server struct {
	t       testing.TB
	key     func(*http.Request) string
	srv     *httptest.Server
	closing chan struct{}

	mu       sync.Mutex
	ended    bool
	scripts  map[string][]Behaviour
	cursor   map[string]int
	requests []Request
}

// New starts a server on a loopback port. It is closed when the test ends: a
// stalled response is released first, then every connection is closed, then
// every behaviour still scripted fails the test.
func New(t testing.TB, o Options) *Server {
	t.Helper()
	s := &Server{t: t, key: o.Key, closing: make(chan struct{}), scripts: map[string][]Behaviour{}, cursor: map[string]int{}}
	if s.key == nil {
		s.key = func(*http.Request) string { return "" }
	}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.close)
	return s
}

// URL is the server's base URL, http://127.0.0.1:<port>.
func (s *Server) URL() string { return s.srv.URL }

// Script appends behaviours to key's script.
func (s *Server) Script(key string, bs ...Behaviour) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts[key] = append(s.scripts[key], bs...)
}

// Served is how far key's cursor has advanced: the requests of key answered
// with a scripted behaviour.
func (s *Server) Served(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cursor[key]
}

// Requests is every request received so far, in arrival order.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

func (s *Server) close() {
	s.mu.Lock()
	s.ended = true
	var left []string
	for k, bs := range s.scripts {
		if n := len(bs) - s.cursor[k]; n > 0 {
			left = append(left, fmt.Sprintf("%q: %v", k, bs[s.cursor[k]:]))
		}
	}
	s.mu.Unlock()
	close(s.closing)
	s.srv.CloseClientConnections()
	s.srv.Close()
	if len(left) > 0 {
		s.t.Errorf("faultserver: behaviour(s) never requested: %v", left)
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	key := s.key(r)
	s.mu.Lock()
	req := Request{Key: key, Method: r.Method, Path: r.URL.Path, Header: r.Header.Clone(), Body: body}
	var b Behaviour
	script := s.scripts[key]
	if i := s.cursor[key]; i < len(script) {
		b = script[i]
		s.cursor[key] = i + 1
	} else {
		req.Unscripted = true
	}
	req.Behaviour = b
	s.requests = append(s.requests, req)
	report := req.Unscripted && !s.ended
	s.mu.Unlock()
	if req.Unscripted {
		if report {
			s.t.Errorf("faultserver: unscripted request %s %s for key %q", r.Method, r.URL.Path, key)
		}
		http.Error(w, "faultserver: no behaviour left", 599)
		return
	}
	switch b.kind {
	case kindStatus:
		if b.retryAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int((b.retryAfter+time.Second-1)/time.Second)))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(b.status)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"status": b.status, "message": http.StatusText(b.status)}})
	case kindStream:
		s.stream(w, b.events)
	case kindMalformed:
		s.stream(w, b.events)
		_, _ = io.WriteString(w, MalformedChunk)
	case kindStall:
		s.stream(w, b.events)
		select {
		case <-r.Context().Done():
		case <-s.closing:
		}
	case kindReset:
		if len(b.events) > 0 {
			s.stream(w, b.events)
		}
		reset(w)
	}
}

// stream writes the SSE headers and events and flushes them.
func (s *Server) stream(w http.ResponseWriter, events []model.Event) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encode(events))
	_ = http.NewResponseController(w).Flush()
}

// reset takes the connection from the server and closes it with no linger, so
// the peer sees a reset rather than an orderly end.
func reset(w http.ResponseWriter) {
	conn, buf, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return
	}
	if buf != nil {
		_ = buf.Flush()
	}
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = conn.Close()
}

// encode is the SSE text of events in the kit's dialect.
func encode(events []model.Event) []byte {
	var b bytes.Buffer
	for _, e := range events {
		fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", e.Name, e.Data)
	}
	return b.Bytes()
}
