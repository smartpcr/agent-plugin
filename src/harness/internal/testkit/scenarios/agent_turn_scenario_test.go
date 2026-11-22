//go:build scenario

package scenarios

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestRealMultiStepTurnIsBracketed: a real multi-step turn is bracketed and
// reconstructable (areas/agent-loop.md DH-E-AGENT-1; DH-D-AGENT-3). Authored in
// DH-S-11; executed in DH-S-17, which requires the agent loop (DH-S-11) and the
// headless launcher (DH-S-16).
//
// Given an empty temporary home, profile headless, one real provider and a small
// model, a temporary workspace holding one text file and a recording proxy between
// dsh and the provider, when one prompt that requires reading the file is sent and
// the session goes idle, then the exported log holds one turn/start and one
// turn/end{completed}, at least two steps each closed by its step/end in order,
// exactly one settlement per attempt (each step's attempts are settled, the last
// by assistant/message), a tool/result for every tool/call, and every request
// rebuilt from the log equals the body the proxy captured. Cleanup: the launcher
// stops dsh and deletes the home; the workspace lies under t.TempDir(). On failure
// the export (its path is logged) and the proxy capture (in the failure message)
// are kept.
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key, handed to the program as
// DEEPSEEK_API_KEY; DSH_SCENARIO_AGENT1_OFFICIAL the base URL of DeepSeek's
// Messages endpoint, fronted by the proxy as {official}; DSH_SCENARIO_AGENT1_MODEL
// a small model on route deepseek-official; DSH_SCENARIO_AGENT1_LAYER a headless
// layer, a template over {official} and {workspace}, with no title route;
// DSH_SCENARIO_AGENT1_PROMPT a prompt that requires reading notes.txt with
// scenario_read; DSH_SCENARIO_AGENT1_REPLAY the session/query text whose result
// is the request bodies rebuilt from the log.
//
//dsh:scenario DH-E-AGENT-1
func TestRealMultiStepTurnIsBracketed(t *testing.T) {
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	officialURL := need(t, "DSH_SCENARIO_AGENT1_OFFICIAL", "the base URL of DeepSeek's Messages endpoint")
	model := need(t, "DSH_SCENARIO_AGENT1_MODEL", "a small model on deepseek-official")
	layerT := need(t, "DSH_SCENARIO_AGENT1_LAYER", "a headless layer routing the model through {official}")
	prompt := need(t, "DSH_SCENARIO_AGENT1_PROMPT", "a prompt that requires reading notes.txt")
	replay := need(t, "DSH_SCENARIO_AGENT1_REPLAY", "the session/query text that rebuilds the requests")

	ws := t.TempDir()
	writeFiles(t, ws, map[string]string{"notes.txt": "The meeting moved to Thursday.\n"})
	proxy := startProxy(t, map[string]string{"official": officialURL})
	vars := map[string]string{"workspace": filepath.ToSlash(ws), "official": proxy.base("official")}
	h := startSDK(t, dshBinary(t), []string{"DEEPSEEK_API_KEY=" + key},
		[]string{"--layer", writeLayer(t, expand(layerT, vars))}, readTool(ws, nil))
	session := h.create(t, &protocol.ModelRoute{Provider: "deepseek-official", Model: model})
	h.keepExport(t, session)

	run := h.awaitRun(t, session, h.prompt(t, session, expand(prompt, vars)), 10*time.Minute)
	all := h.history(t, session)
	logRequestIDs(t, all)
	if n := len(ofType(all, "turn/start")); n != 1 {
		t.Fatalf("%d turn/start, want 1", n)
	}
	if end := turnEnd(t, all); end.Reason.Kind != "completed" {
		t.Fatalf("the turn ended %+v, want completed", end.Reason)
	}
	evs := inRun(all, run.FirstSeq, run.LastSeq)
	steps := assertBracketedSteps(t, evs)
	if steps < 2 {
		t.Fatalf("the turn has %d steps, want at least two (a tool call, then the answer)", steps)
	}
	assertCallsPaired(t, evs)
	assertRebuiltEqualSent(t, h, session, replay, sentBodies(proxy, "official"))
}

// assertBracketedSteps checks that evs holds steps opened and closed in order,
// never nested, each settled at least once and ending on an assistant/message, a
// tool/call only inside a step; it returns the number of steps.
func assertBracketedSteps(t *testing.T, evs []protocol.Event) int {
	t.Helper()
	open, steps := false, 0
	var last string
	settled := 0
	for _, e := range evs {
		switch e.Type {
		case "step/start":
			if open {
				t.Fatalf("step/start at %d inside an open step", e.Seq)
			}
			open, settled, last = true, 0, ""
			steps++
		case "assistant/message", "assistant/attempt":
			if !open {
				t.Fatalf("%s at %d outside a step", e.Type, e.Seq)
			}
			settled++
			last = e.Type
		case "tool/call", "tool/result":
			if !open {
				t.Fatalf("%s at %d outside a step", e.Type, e.Seq)
			}
		case "step/end":
			if !open {
				t.Fatalf("step/end at %d with no open step", e.Seq)
			}
			if settled == 0 || last != "assistant/message" {
				t.Fatalf("the step closed at %d has %d settlements, the last %q; want each attempt settled, the last by assistant/message", e.Seq, settled, last)
			}
			open = false
		}
	}
	if open {
		t.Fatal("a step is left open")
	}
	return steps
}

// assertCallsPaired checks that every tool/call among evs has exactly one
// tool/result after it.
func assertCallsPaired(t *testing.T, evs []protocol.Event) {
	t.Helper()
	calls := map[string]int64{}
	results := map[string]int{}
	for _, e := range ofType(evs, "tool/call", "tool/result") {
		var p struct {
			CallID string `json:"call_id"`
		}
		if err := json.Unmarshal(e.Data, &p); err != nil {
			t.Fatalf("event %d: %v", e.Seq, err)
		}
		if e.Type == "tool/call" {
			calls[p.CallID] = e.Seq
			continue
		}
		if _, ok := calls[p.CallID]; !ok {
			t.Fatalf("tool/result %d for call %s before its tool/call", e.Seq, p.CallID)
		}
		results[p.CallID]++
	}
	for id := range calls {
		if results[id] != 1 {
			t.Fatalf("call %s has %d tool/result, want 1", id, results[id])
		}
	}
}
