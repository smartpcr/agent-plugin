//go:build scenario

package scenarios

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestRequestsReceivedEqualRequestsRebuilt: the requests a real model API received
// equal the requests rebuilt from the log (areas/core-session.md DH-E-SESSION-1;
// DH-D-SESSION-4, DH-D-SESSION-3, DH-D-SESSION-1). Authored in DH-S-11; executed
// in DH-S-17, which requires the agent loop (DH-S-11) and the headless launcher
// (DH-S-16).
//
// Given the built dsh, a fresh temporary home, a real API key set at run time and
// never written to disk, a recording proxy set as the provider's base address and
// a workspace holding one text file, when (1) the host starts headless, (2) a
// prompt that makes the model read the file with a tool is sent and the turn ends,
// (3) a second prompt is sent and its turn ends, (4) the session is exported and
// (5) every request is rebuilt from the log at its requestAsOf and encoded with the
// same adapter, then the proxy recorded as many requests as the log has
// settlements (assistant/message and assistant/attempt), each rebuilt body equals
// the recorded body, a failed attempt's request matches as well, and no recorded
// request lacks a settlement. Cleanup: the launcher stops the host and deletes the
// home; the proxy closes with the test. On failure the export is kept (its path is
// logged).
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key, handed to the program as
// DEEPSEEK_API_KEY; DSH_SCENARIO_SESSION1_OFFICIAL the base URL of DeepSeek's
// Messages endpoint, fronted by the proxy as {official}; DSH_SCENARIO_SESSION1_MODEL
// the model id on route deepseek-official; DSH_SCENARIO_SESSION1_LAYER a layer, a
// template over {official} and {workspace}, that points the route at the proxy,
// selects the headless profile and sends no request other than the turns' (no
// title route); DSH_SCENARIO_SESSION1_PROMPT a prompt, a template over {workspace},
// that reads notes.txt with scenario_read and answers; DSH_SCENARIO_SESSION1_REPLAY
// the session/query text whose result is the request bodies rebuilt from the log
// (as for DH-E-TOOLS-1).
//
//dsh:scenario DH-E-SESSION-1
func TestRequestsReceivedEqualRequestsRebuilt(t *testing.T) {
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	officialURL := need(t, "DSH_SCENARIO_SESSION1_OFFICIAL", "the base URL of DeepSeek's Messages endpoint")
	model := need(t, "DSH_SCENARIO_SESSION1_MODEL", "a model on deepseek-official")
	layerT := need(t, "DSH_SCENARIO_SESSION1_LAYER", "a headless layer routing the model through {official}")
	promptT := need(t, "DSH_SCENARIO_SESSION1_PROMPT", "a prompt that reads notes.txt with scenario_read")
	replay := need(t, "DSH_SCENARIO_SESSION1_REPLAY", "the session/query text that rebuilds the requests")

	ws := t.TempDir()
	writeFiles(t, ws, map[string]string{"notes.txt": "the answer is forty-two\n"})
	proxy := startProxy(t, map[string]string{"official": officialURL})
	vars := map[string]string{"workspace": filepath.ToSlash(ws), "official": proxy.base("official")}

	h := startSDK(t, dshBinary(t), []string{"DEEPSEEK_API_KEY=" + key},
		[]string{"--layer", writeLayer(t, expand(layerT, vars))}, readTool(ws, nil))
	session := h.create(t, &protocol.ModelRoute{Provider: "deepseek-official", Model: model})
	h.keepExport(t, session)

	run1 := h.awaitRun(t, session, h.prompt(t, session, expand(promptT, vars)), 10*time.Minute)
	evs1 := inRun(h.history(t, session), run1.FirstSeq, run1.LastSeq)
	logRequestIDs(t, evs1)
	if n := len(ofType(evs1, "tool/result")); n == 0 {
		t.Fatal("turn 1 made no tool call: the prompt must make the model read the file")
	}
	run2 := h.awaitRun(t, session, h.prompt(t, session, "Thank you. In one word, what was the answer?"), 10*time.Minute)
	logRequestIDs(t, inRun(h.history(t, session), run2.FirstSeq, run2.LastSeq))

	settlements := ofType(h.history(t, session), "assistant/message", "assistant/attempt")
	sent := sentBodies(proxy, "official")
	if len(sent) != len(settlements) {
		t.Fatalf("the proxy recorded %d requests, the log has %d settlements: a recorded request with no settlement, or one never sent", len(sent), len(settlements))
	}
	assertRebuiltEqualSent(t, h, session, replay, sent)
}

// readTool is the client tool scenario_read over the files of ws; seen, when set,
// is told every name it is asked for (it may be called concurrently).
func readTool(ws string, seen func(name string)) sdkTool {
	return sdkTool{
		Decl: protocol.ToolDeclaration{
			Name:        "scenario_read",
			Description: "Reads a file of the scenario's workspace by its name.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
		Answer: func(p protocol.ToolCallParams) protocol.ToolCallResult {
			var a struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(p.Arguments, &a); err != nil {
				return toolError(err)
			}
			name := filepath.Base(a.Path)
			if seen != nil {
				seen(name)
			}
			data, err := os.ReadFile(filepath.Join(ws, name))
			if err != nil {
				return toolError(err)
			}
			return protocol.ToolCallResult{Content: []protocol.ContentBlock{{Type: "text", Text: string(data)}}}
		},
	}
}

// toolError is a client tool's failed result carrying err.
func toolError(err error) protocol.ToolCallResult {
	return protocol.ToolCallResult{IsError: true, Content: []protocol.ContentBlock{{Type: "text", Text: err.Error()}}}
}

// writeFiles writes name -> text into dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// sentBodies are the POST bodies the proxy forwarded to upstream, in order.
func sentBodies(p *recordingProxy, upstream string) []json.RawMessage {
	var out []json.RawMessage
	for _, r := range p.requests(upstream) {
		if r.Method == "POST" {
			out = append(out, r.Body)
		}
	}
	return out
}

// assertRebuiltEqualSent asks the host, twice, to rebuild session's requests from
// its log with replay, and checks the two answers are the same bytes and that the
// rebuilt bodies equal sent one for one, in order.
func assertRebuiltEqualSent(t *testing.T, h *sdkHost, session, replay string, sent []json.RawMessage) {
	t.Helper()
	rebuild := func() json.RawMessage {
		var q protocol.SessionQueryResult
		h.call(t, "session/query", protocol.SessionQueryParams{SessionID: session, Content: []protocol.ContentBlock{{Type: "text", Text: replay}}}, &q)
		return q.Result
	}
	first, second := rebuild(), rebuild()
	if len(bytes.TrimSpace(first)) == 0 || strings.TrimSpace(string(first)) == "null" {
		t.Fatalf("%q rebuilt no requests", replay)
	}
	if !bytes.Equal(first, second) {
		t.Fatalf("two rebuilds differ:\n%s\n---\n%s", first, second)
	}
	rebuilt := rebuiltRequests(t, first)
	if len(sent) == 0 || len(rebuilt) != len(sent) {
		t.Fatalf("the log rebuilt %d requests, the provider was sent %d", len(rebuilt), len(sent))
	}
	for i := range sent {
		if !jsonEqual(t, rebuilt[i], sent[i]) {
			t.Fatalf("request %d: rebuilt differs from sent\nrebuilt: %s\nsent:    %s", i, rebuilt[i], sent[i])
		}
	}
}

// jsonEqual compares two JSON documents by value; either failing to decode fails
// the scenario.
func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, a)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}
