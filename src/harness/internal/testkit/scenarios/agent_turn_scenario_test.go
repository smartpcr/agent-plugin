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
// reconstructable (areas/agent-loop.md DH-E-AGENT-1; DH-D-AGENT-3, on the
// signed-in client route of areas/llm.md DH-D-LLM-12). Authored in DH-S-11 and
// retargeted onto the signed-in client routes by gap-closure-plan 17.4.4
// (M1-D-CLOSE-4 A); executed in DH-S-17, which requires the agent loop (DH-S-11)
// and the headless launcher (DH-S-16).
//
// Given an empty temporary home, profile headless, no API key anywhere, the
// user's own installed official client signed in as themselves with its route
// registered by a layer and a small model, and a temporary workspace holding one
// text file, when, per route, one prompt that requires reading the file is sent
// and the session goes idle, then the exported log holds one turn/start and one
// turn/end{completed}, at least two steps each closed by its step/end in order,
// exactly one settlement per attempt (each step's attempts are settled, the last
// by assistant/message), a tool/result for every tool/call, and the client
// invocation rebuilt from the log for every settlement equals the one it
// recorded: the argument list and its digest, and the standard input dsh wrote,
// by digest (the client-invocation boundary of M1-D-PIPE-4 A, where a keyed
// route had a recording proxy; see TestRequestsReceivedEqualRequestsRebuilt).
// Cleanup: the launcher stops dsh and deletes the home; the workspace lies under
// t.TempDir(); nothing of the user's sign-in is touched. On failure the export
// (its path is logged) and the client invocations are kept.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_AGENT1_LAYER a headless layer, a template over {route}
// and {workspace}, that registers that client route, with no title route;
// DSH_SCENARIO_<route>_MODEL (<route> is CLAUDE_CODE or COPILOT_CLI) a small
// model; DSH_SCENARIO_AGENT1_PROMPT a prompt that requires reading notes.txt
// with scenario_read; DSH_SCENARIO_AGENT1_REPLAY the session/query text whose
// result is the client invocations rebuilt from the log (rebuiltInvocations).
//
//dsh:scenario DH-E-AGENT-1
func TestRealMultiStepTurnIsBracketed(t *testing.T) {
	selfCheckInvocationOracle(t)
	layerT := need(t, "DSH_SCENARIO_AGENT1_LAYER", "a headless layer over {route} and {workspace} that registers that client route")
	prompt := need(t, "DSH_SCENARIO_AGENT1_PROMPT", "a prompt that requires reading notes.txt")
	replay := need(t, "DSH_SCENARIO_AGENT1_REPLAY", "the session/query text that rebuilds the client invocations")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		model := r.model(t)
		ws := t.TempDir()
		writeFiles(t, ws, map[string]string{"notes.txt": "The meeting moved to Thursday.\n"})
		vars := map[string]string{"workspace": filepath.ToSlash(ws), "route": r.ID}
		h := startSDK(t, bin, nil, []string{"--layer", writeLayer(t, expand(layerT, vars))}, readTool(ws, nil))
		session := h.create(t, &protocol.ModelRoute{Provider: r.ID, Model: model})
		h.keepExport(t, session)

		run := h.awaitRun(t, session, h.prompt(t, session, expand(prompt, vars)), 10*time.Minute)
		all := h.history(t, session)
		logInvocations(t, all)
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
		assertInvocationsRebuilt(t, h, session, replay, ofType(all, "assistant/message", "assistant/attempt"), r)
	})
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
