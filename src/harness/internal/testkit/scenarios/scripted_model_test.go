//go:build scenario

package scenarios

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
)

// The scripted model endpoint of the DH-S-12 scenarios that need a direct caller
// rather than a real model (DH-E-FS-1, DH-E-FS-3): a loopback server speaking the
// Anthropic Messages stream (the dialect of llm/wire/anthropic) that answers each
// request that declares tools with the next scripted tool call, and a request
// with nothing left to call with a short closing text. The calls the scenario
// scripts therefore go through dsh's whole tool pipeline (policy, approval, the
// fenced seam) exactly as a model's would; only the choice of call is fixed.
//
// Operator decision fs-scripted-model-vs-replay: this endpoint stands in for the
// replay provider (C54) until DH-S-16 lands it; the scenarios switch to the replay
// provider then, keeping their Given/When/Then. The endpoint is reached by a layer
// (a run-time input of each scenario, a template over {endpoint}) that registers a
// route on it with a scenario-local key; no real key exists anywhere.

// scriptedCall is one tool call the endpoint answers with. Tag is the
// scenario's own label for the call; it is never sent.
type scriptedCall struct {
	Name string
	Args any
	Tag  string
}

// scriptedModel is a running scripted endpoint.
type scriptedModel struct {
	// URL is the endpoint's root; the route posts to URL + "/v1/messages".
	URL string

	mu      sync.Mutex
	queue   []scriptedCall
	sent    map[string]scriptedCall // tool_use id -> call
	next    int
	replies int
	bodies  []string // every request body received, in order
}

// startScriptedModel serves a scripted endpoint on a loopback port until cleanup.
func startScriptedModel(t *testing.T) *scriptedModel {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	m := &scriptedModel{URL: "http://" + ln.Addr().String(), sent: map[string]scriptedCall{}}
	srv := &http.Server{Handler: http.HandlerFunc(m.serve)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = srv.Serve(ln)
	}()
	t.Cleanup(func() {
		_ = srv.Close()
		<-done
		_ = ln.Close()
	})
	return m
}

// script queues calls; the endpoint answers them one per request, in order.
func (m *scriptedModel) script(calls ...scriptedCall) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = append(m.queue, calls...)
}

// pending is the number of scripted calls not yet answered.
func (m *scriptedModel) pending() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.queue)
}

// call is the scripted call the endpoint sent as tool_use id.
func (m *scriptedModel) call(id string) (scriptedCall, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.sent[id]
	return c, ok
}

// requests returns the body of every request the endpoint received, in order.
func (m *scriptedModel) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.bodies...)
}

func (m *scriptedModel) serve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
		http.NotFound(w, r)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var req struct {
		Tools []json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// A request without tools (a title, a summary) never consumes a scripted call.
	var c *scriptedCall
	var id string
	m.mu.Lock()
	m.replies++
	m.bodies = append(m.bodies, string(body))
	if len(req.Tools) > 0 && len(m.queue) > 0 {
		next := m.queue[0]
		m.queue = m.queue[1:]
		m.next++
		id = fmt.Sprintf("toolu_scenario_%06d", m.next)
		m.sent[id] = next
		c = &next
	}
	m.mu.Unlock()

	w.Header().Set("content-type", "text/event-stream")
	w.Header().Set("request-id", fmt.Sprintf("req_scenario_%d", m.replies))
	ev := func(typ string, data any) {
		raw, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, raw)
	}
	ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{
		"id": fmt.Sprintf("msg_scenario_%d", m.replies), "type": "message", "role": "assistant", "content": []any{},
		"usage": map[string]int{"input_tokens": 1, "output_tokens": 0},
	}})
	stop := "end_turn"
	if c != nil {
		args, _ := json.Marshal(c.Args)
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "tool_use", "id": id, "name": c.Name, "input": map[string]any{}}})
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": string(args)}})
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		stop = "tool_use"
	} else {
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "text", "text": ""}})
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": "Done."}})
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	}
	ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop},
		"usage": map[string]int{"output_tokens": 1}})
	ev("message_stop", map[string]any{"type": "message_stop"})
}
