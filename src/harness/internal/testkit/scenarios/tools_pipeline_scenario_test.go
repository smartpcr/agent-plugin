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
// DH-D-TOOLS-5, DH-D-HUMAN-2; SP-6, SP-7; on the signed-in client route of
// areas/llm.md DH-D-LLM-12). Authored in DH-S-10 and retargeted onto the
// signed-in client routes by gap-closure-plan 18.1.1 (M1-D-CLOSE-4 A); executed
// in DH-S-17, which requires the agent loop (DH-S-11) and the headless launcher
// (DH-S-16).
//
// The boundary. A client route starts the user's own official client, which
// talks to its vendor itself, so no proxy can sit between the client and the
// vendor, and "the request that was sent" is what dsh handed the client: SP-1
// holds at the client invocation (M1-D-PIPE-4 A, DH-ADR-LLM-10), as in
// DH-E-SESSION-1 (TestRequestsReceivedEqualRequestsRebuilt). Each settlement
// records the argument list, its digest, the digest of the standard input dsh
// wrote and the digest of each attempt file. The system text and the tool
// declarations are in those bytes: the tool declarations are the attempt file
// tools.json on both routes (as MCP entries, DH-ADR-LLM-8); the system text is
// the attempt file system.txt on claude-code and the leading <system> section
// of standard input on copilot-cli, whose client cannot replace its own
// (claude-code-cli.md and github-copilot-cli.md sec. 5).
//
// Given the built dsh, a fresh temporary home, no API key anywhere, the user's
// own installed official client signed in as themselves with its route
// registered by a layer, a workspace holding two files to read and one to
// write, three client tools (scenario_read twice-callable, scenario_write), a
// layer whose policy answers Ask for scenario_write, and an answerer (this
// client, with the approvals capability) that rejects every approval, when, per
// route, a prompt that requires reading both files and then writing the third
// is sent and the turn finishes, then every call the model made has exactly one
// tool/call before its one tool/result, in the model's order; both reads ran
// and succeeded; the write was asked about, refused with an approval code, its
// body (this client's handler) never ran and the file is byte-identical; and
// each client invocation rebuilt from the log by replay equals the one its
// settlement recorded, one for one and in order: the argument list and its
// digest, the standard input by digest and the request's attempt files by
// digest, so the system text and tool declarations are included as above (and
// two replays answer the same bytes). The attempt file mcp.json names the
// attempt directory's absolute paths and is not compared, as in DH-T-LLM-20. The client runs only while an attempt runs
// and returns each tool call unexecuted (M1-D-PIPE-6 A), so the pipeline that
// guards the calls is dsh's. Cleanup: the home is deleted by the launcher;
// nothing of the user's sign-in is touched; on failure the exported log is kept
// (its path is logged) and the client invocations are logged.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_TOOLS1_LAYER a configuration layer, a template over
// {route} and {workspace} (the workspace directory), that registers that client
// route, makes the policy ask before scenario_write and sends no request other
// than the turn's (no title route); DSH_SCENARIO_<route>_MODEL (<route> is
// CLAUDE_CODE or COPILOT_CLI) the model; DSH_SCENARIO_TOOLS1_PROMPT a prompt, a
// template over {workspace}, that reads a.txt and b.txt with scenario_read and
// then writes c.txt with scenario_write; DSH_SCENARIO_TOOLS1_REPLAY the
// session/query text whose result is the client invocations rebuilt from the
// log, in settlement order (rebuiltInvocations), each with the attempt files it
// rebuilt ({name, digest} under "files"). The client is the one on the PATH,
// signed in beforehand by the user.
//
//dsh:scenario DH-E-TOOLS-1
func TestGuardedPipelineRefusesTheWrite(t *testing.T) {
	selfCheckInvocationOracle(t)
	layerT := need(t, "DSH_SCENARIO_TOOLS1_LAYER", "a layer over {route} and {workspace} that registers that client route and asks before scenario_write")
	promptT := need(t, "DSH_SCENARIO_TOOLS1_PROMPT", "a prompt that reads two files and writes a third")
	replay := need(t, "DSH_SCENARIO_TOOLS1_REPLAY", "the session/query text that rebuilds the client invocations")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		guardedPipeline(t, bin, r, layerT, promptT, replay)
	})
}

// guardedPipeline is DH-E-TOOLS-1 on route r.
func guardedPipeline(t *testing.T, bin string, r clientRoute, layerT, promptT, replay string) {
	t.Helper()
	model := r.model(t)
	ws := t.TempDir()
	files := map[string][]byte{"a.txt": []byte("alpha\n"), "b.txt": []byte("bravo\n"), "c.txt": []byte("charlie, untouched\n")}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(ws, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	vars := map[string]string{"workspace": filepath.ToSlash(ws), "route": r.ID}

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

	// No environment entry: the launcher's scrubbed environment holds no
	// credential, and the client finds the user's own sign-in in its
	// configuration home.
	h := startSDKWith(t, bin, nil, []string{"--layer", writeLayer(t, expand(layerT, vars))}, reject, read, write)
	session := h.create(t, &protocol.ModelRoute{Provider: r.ID, Model: model})
	h.keepExport(t, session)

	run := h.awaitRun(t, session, h.prompt(t, session, expand(promptT, vars)), 10*time.Minute)
	all := h.history(t, session)
	evs := inRun(all, run.FirstSeq, run.LastSeq)
	logInvocations(t, all)

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

	// The client invocations rebuilt from the log are the ones dsh started,
	// rebuilding them is byte-stable, and the system text and tool declarations
	// are among the rebuilt bytes.
	settlements := ofType(all, "assistant/message", "assistant/attempt")
	rebuilt := assertInvocationsRebuilt(t, h, session, replay, settlements, r)
	var q protocol.SessionQueryResult
	h.call(t, "session/query", protocol.SessionQueryParams{SessionID: session, Content: []protocol.ContentBlock{{Type: "text", Text: replay}}}, &q)
	rebuiltFiles := tools1RebuiltFiles(t, q.Result)
	if len(rebuiltFiles) != len(settlements) {
		t.Fatalf("the replay rebuilt the attempt files of %d invocations, the log has %d settlements", len(rebuiltFiles), len(settlements))
	}
	// The request files only: mcp.json names the attempt directory's absolute
	// paths, so its digest is the attempt's own and is not rebuilt.
	content := []string{tools1ToolsFile}
	systemFile := tools1SystemFile[r.ID]
	if systemFile != "" {
		content = append(content, systemFile)
	}
	for i, e := range settlements {
		logged, again := tools1Digests(tools1SettledFiles(t, e)), tools1Digests(rebuiltFiles[i])
		for _, name := range content {
			if logged[name] == "" {
				t.Fatalf("%s %d records attempt files %v, want %s (the %s)", e.Type, e.Seq, logged, name, tools1FileHolds(name))
			}
			if again[name] != logged[name] {
				t.Fatalf("%s %d: %s (the %s) rebuilt with digest %q, logged %s", e.Type, e.Seq, name, tools1FileHolds(name), again[name], logged[name])
			}
		}
		if systemFile == "" && (rebuilt[i].Stdin == nil || !strings.HasPrefix(*rebuilt[i].Stdin, "<system>\n")) {
			t.Fatalf("%s %d: the rebuilt standard input does not lead with the <system> section that carries the system text", e.Type, e.Seq)
		}
	}
}

// tools1Digests maps each attempt file's name to its digest.
func tools1Digests(files []tools1File) map[string]string {
	m := make(map[string]string, len(files))
	for _, f := range files {
		m[f.Name] = f.Digest
	}
	return m
}

// tools1FileHolds says what an attempt file of the content list carries.
func tools1FileHolds(name string) string {
	if name == tools1ToolsFile {
		return "tool declarations"
	}
	return "system text"
}

// tools1File is one attempt file of a client invocation: its name inside the
// attempt directory and the "sha256:<hex>" digest of its bytes.
type tools1File struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
}

// tools1ToolsFile is the attempt file in which both client routes declare the
// request's tools, as MCP entries (areas/llm.md DH-ADR-LLM-8).
const tools1ToolsFile = "tools.json"

// tools1SystemFile names, per route, the attempt file that carries the harness
// system text; "" when the route writes it as the leading <system> section of
// standard input instead (claude-code-cli.md and github-copilot-cli.md sec. 5).
var tools1SystemFile = map[string]string{claudeCodeRoute.ID: "system.txt", copilotCLIRoute.ID: ""}

// tools1SettledFiles are the attempt files the client invocation of settlement
// e records, nil when it records none.
func tools1SettledFiles(t *testing.T, e protocol.Event) []tools1File {
	t.Helper()
	var s struct {
		Client *struct {
			Files []tools1File `json:"files"`
		} `json:"client"`
	}
	decode(t, e, &s)
	if s.Client == nil {
		return nil
	}
	return s.Client.Files
}

// tools1RebuiltFiles reads the attempt files of each rebuilt invocation from a
// replay's result, a JSON array of invocations or an object holding them under
// "invocations" (rebuiltInvocations).
func tools1RebuiltFiles(t *testing.T, raw json.RawMessage) [][]tools1File {
	t.Helper()
	type entry struct {
		Files []tools1File `json:"files"`
	}
	var list []entry
	if err := json.Unmarshal(raw, &list); err != nil {
		var wrapped struct {
			Invocations []entry `json:"invocations"`
		}
		if err := json.Unmarshal(raw, &wrapped); err != nil || wrapped.Invocations == nil {
			t.Fatalf("the replay answered neither an invocation array nor {\"invocations\": [...]}: %s", raw)
		}
		list = wrapped.Invocations
	}
	out := make([][]tools1File, len(list))
	for i, e := range list {
		out[i] = e.Files
	}
	return out
}
