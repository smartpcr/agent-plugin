//go:build scenario

package scenarios

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestGuardedPipelineRefusesTheWrite: a real model's batch of two reads and one
// write through the guarded pipeline (areas/tools.md DH-E-TOOLS-1; DH-D-TOOLS-2,
// DH-D-HUMAN-2; SP-6, SP-7). Authored in DH-S-10; executed in DH-S-17, which
// requires the agent loop (DH-S-11) and the headless launcher (DH-S-16).
//
// Given the built dsh, a fresh temporary home, a real API key set at run time, a
// workspace holding two files to read and one to write, three client tools
// (scenario_read twice-callable, scenario_write), a layer whose policy answers Ask
// for scenario_write, and an answerer (this client, with the approvals
// capability) that rejects every approval, when a prompt that requires reading
// both files and then writing the third is sent and the turn finishes, then every
// call the model made has exactly one tool/call before its one tool/result, in the
// model's order; both reads ran and succeeded; the write was asked about, refused
// with an approval code, its body (this client's handler) never ran and the file
// is byte-identical; and the requests rebuilt from the log by replay are
// byte-stable (two replays answer the same bytes). Cleanup: the home is deleted
// by the launcher; on failure the exported log is kept (its path is logged).
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key, handed to the program as
// DEEPSEEK_API_KEY; DSH_SCENARIO_TOOLS1_MODEL the model id on route
// deepseek-official; DSH_SCENARIO_TOOLS1_LAYER a configuration layer, a template
// whose {workspace} is the workspace directory, that makes the policy ask before
// scenario_write; DSH_SCENARIO_TOOLS1_PROMPT a prompt, a template over
// {workspace}, that reads a.txt and b.txt with scenario_read and then writes
// c.txt with scenario_write; DSH_SCENARIO_TOOLS1_REPLAY the session/query text
// whose result is the provider requests rebuilt from the log.
//
//dsh:scenario DH-E-TOOLS-1
func TestGuardedPipelineRefusesTheWrite(t *testing.T) {
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	model := need(t, "DSH_SCENARIO_TOOLS1_MODEL", "a model on deepseek-official")
	layerT := need(t, "DSH_SCENARIO_TOOLS1_LAYER", "a layer whose policy asks before scenario_write")
	promptT := need(t, "DSH_SCENARIO_TOOLS1_PROMPT", "a prompt that reads two files and writes a third")
	replay := need(t, "DSH_SCENARIO_TOOLS1_REPLAY", "the session/query text that rebuilds the requests")

	ws := t.TempDir()
	files := map[string][]byte{"a.txt": []byte("alpha\n"), "b.txt": []byte("bravo\n"), "c.txt": []byte("charlie, untouched\n")}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(ws, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]string{"workspace": filepath.ToSlash(ws)}

	var (
		mu     sync.Mutex
		reads  []string
		writes int
		asked  []protocol.ApprovalRequest
	)
	pathArg := func(raw json.RawMessage) (string, error) {
		var a struct {
			Path string `json:"path"`
		}
		err := json.Unmarshal(raw, &a)
		return a.Path, err
	}
	fail := func(err error) protocol.ToolCallResult {
		return protocol.ToolCallResult{IsError: true, Content: []protocol.ContentBlock{{Type: "text", Text: err.Error()}}}
	}
	read := sdkTool{
		Decl: protocol.ToolDeclaration{
			Name:        "scenario_read",
			Description: "Reads a file of the scenario's workspace by its name.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
		},
		Answer: func(p protocol.ToolCallParams) protocol.ToolCallResult {
			name, err := pathArg(p.Arguments)
			if err != nil {
				return fail(err)
			}
			mu.Lock()
			reads = append(reads, filepath.Base(name))
			mu.Unlock()
			data, err := os.ReadFile(filepath.Join(ws, filepath.Base(name)))
			if err != nil {
				return fail(err)
			}
			return protocol.ToolCallResult{Content: []protocol.ContentBlock{{Type: "text", Text: string(data)}}}
		},
	}
	write := sdkTool{
		Decl: protocol.ToolDeclaration{
			Name:        "scenario_write",
			Description: "Writes text to a file of the scenario's workspace.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"},"text":{"type":"string"}},"required":["path","text"]}`),
		},
		Answer: func(p protocol.ToolCallParams) protocol.ToolCallResult {
			mu.Lock()
			writes++
			mu.Unlock()
			// The body of a refused call must never run; were it to, it writes, so
			// the unchanged file below is evidence on its own.
			var a struct {
				Path string `json:"path"`
				Text string `json:"text"`
			}
			if err := json.Unmarshal(p.Arguments, &a); err != nil {
				return fail(err)
			}
			if err := os.WriteFile(filepath.Join(ws, filepath.Base(a.Path)), []byte(a.Text), 0o600); err != nil {
				return fail(err)
			}
			return protocol.ToolCallResult{Content: []protocol.ContentBlock{{Type: "text", Text: "written"}}}
		},
	}
	reject := sdkOptions{Approve: func(r protocol.ApprovalRequest) protocol.ApprovalResult {
		mu.Lock()
		asked = append(asked, r)
		mu.Unlock()
		return protocol.ApprovalResult{Outcome: "rejected"}
	}}

	h := startSDKWith(t, dshBinary(t), []string{"DEEPSEEK_API_KEY=" + key},
		[]string{"--layer", writeLayer(t, expand(layerT, vars))}, reject, read, write)
	session := h.create(t, &protocol.ModelRoute{Provider: "deepseek-official", Model: model})
	h.keepExport(t, session)

	run := h.awaitRun(t, session, h.prompt(t, session, expand(promptT, vars)), 10*time.Minute)
	evs := inRun(h.history(t, session), run.FirstSeq, run.LastSeq)
	logRequestIDs(t, evs)

	// The model's order: the tool_use blocks of its messages, in sequence.
	var order []string
	for _, m := range ofType(evs, "assistant/message") {
		var s wireSettlement
		decode(t, m, &s)
		for _, b := range s.Message.Blocks {
			if b.Kind == "tool_use" && b.ToolUse != nil {
				order = append(order, b.ToolUse.ID)
			}
		}
	}
	type bracket struct {
		Name     string
		Call     int64
		Result   int64
		IsError  bool
		Raw      json.RawMessage
		calls    int
		resultsN int
	}
	byID := map[string]*bracket{}
	var started, settled []string
	for _, e := range ofType(evs, "tool/call", "tool/result") {
		var p struct {
			CallID  string `json:"call_id"`
			Name    string `json:"name"`
			IsError bool   `json:"is_error"`
		}
		decode(t, e, &p)
		b := byID[p.CallID]
		if b == nil {
			b = &bracket{}
			byID[p.CallID] = b
		}
		if e.Type == "tool/call" {
			b.Name, b.Call = p.Name, e.Seq
			b.calls++
			started = append(started, p.CallID)
		} else {
			b.Result, b.IsError, b.Raw = e.Seq, p.IsError, e.Data
			b.resultsN++
			settled = append(settled, p.CallID)
		}
	}
	if len(order) < 3 {
		t.Fatalf("the model made %d tool calls, want at least two reads and a write", len(order))
	}
	if !slices.Equal(started, order) || !slices.Equal(settled, order) {
		t.Fatalf("tool/call order %v and tool/result order %v, want the model's order %v", started, settled, order)
	}
	var writeID string
	for _, id := range order {
		b := byID[id]
		if b.calls != 1 || b.resultsN != 1 || b.Call >= b.Result {
			t.Fatalf("call %s has %d tool/call and %d tool/result (seqs %d, %d); want one each, call first", id, b.calls, b.resultsN, b.Call, b.Result)
		}
		switch b.Name {
		case "scenario_read":
			if b.IsError {
				t.Fatalf("read %s failed: %s", id, b.Raw)
			}
		case "scenario_write":
			writeID = id
			if !b.IsError || !bytes.Contains(b.Raw, []byte("approval/")) {
				t.Fatalf("write %s was not refused by the approval: %s", id, b.Raw)
			}
		}
	}
	mu.Lock()
	nReads, nWrites := slices.Clone(reads), writes
	nAsked := slices.Clone(asked)
	mu.Unlock()
	slices.Sort(nReads)
	if !slices.Contains(nReads, "a.txt") || !slices.Contains(nReads, "b.txt") {
		t.Fatalf("the reads ran on %v, want a.txt and b.txt", nReads)
	}
	if writeID == "" || nWrites != 0 {
		t.Fatalf("the write call %q ran its body %d times, want a write call that never ran", writeID, nWrites)
	}
	if len(nAsked) == 0 || nAsked[0].ToolName != "scenario_write" || nAsked[0].CallID != writeID {
		t.Fatalf("approvals asked %+v, want one for the write call %s", nAsked, writeID)
	}
	if data, err := os.ReadFile(filepath.Join(ws, "c.txt")); err != nil || !bytes.Equal(data, files["c.txt"]) {
		t.Fatalf("c.txt is %q (%v), want it unchanged", data, err)
	}

	// The requests rebuilt from the log are byte-stable.
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
		t.Fatalf("two replays differ:\n%s\n---\n%s", first, second)
	}
}
