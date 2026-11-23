//go:build scenario

package scenarios

import (
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestBudgetStopsARealToolLoop: the budget stops a real tool loop
// (areas/agent-loop.md DH-E-AGENT-4; DH-D-AGENT-3). Authored in DH-S-11 and
// retargeted onto the signed-in client routes of areas/llm.md DH-D-LLM-12 by
// gap-closure-plan 17.4.4 (M1-D-CLOSE-4 A); executed in DH-S-17, which requires
// the agent loop (DH-S-11) and the headless launcher (DH-S-16).
//
// Given an empty temporary home, profile headless with budget.maxSteps 2, no API
// key anywhere, the user's own installed official client signed in as
// themselves with its route registered by a layer and a small model, a temporary
// workspace holding four small files and a prompt that needs at least four tool
// calls in sequence (read each file in turn), when, per route, the prompt is
// sent, the session goes idle and "continue" is sent, then the first turn ends
// budget{limit: steps, value: 2} with every call paired with its result, each
// call returned unexecuted by the client and run by the harness (the scenario's
// tool was asked for it), every settlement recording the invocation of the
// route's client; and the second turn starts from that history and makes
// progress: it reads a file the first turn did not. Cleanup: the launcher stops
// dsh and deletes the home; the workspace lies under t.TempDir(); nothing of the
// user's sign-in is touched. On failure the export is kept (its path is logged)
// and the client invocations are logged.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_<route>_MODEL (<route> is CLAUDE_CODE or COPILOT_CLI) a
// small model; DSH_SCENARIO_AGENT4_LAYER a layer, a template over {route} and
// {workspace}, registering that client route and selecting the headless profile
// with budget.maxSteps: 2 and no title route; DSH_SCENARIO_AGENT4_PROMPT a
// prompt that reads 1.txt, 2.txt, 3.txt and 4.txt with scenario_read one at a
// time, in order.
//
//dsh:scenario DH-E-AGENT-4
func TestBudgetStopsARealToolLoop(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_AGENT4_LAYER", "a headless layer over {route} with budget.maxSteps: 2")
	prompt := need(t, "DSH_SCENARIO_AGENT4_PROMPT", "a prompt that reads four files in turn")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		budgetStopsARealToolLoop(t, bin, r, layerT, prompt)
	})
}

// budgetStopsARealToolLoop is DH-E-AGENT-4 on route r.
func budgetStopsARealToolLoop(t *testing.T, bin string, r clientRoute, layerT, prompt string) {
	t.Helper()
	model := r.model(t)
	ws := t.TempDir()
	writeFiles(t, ws, map[string]string{"1.txt": "one\n", "2.txt": "two\n", "3.txt": "three\n", "4.txt": "four\n"})
	vars := map[string]string{"workspace": filepath.ToSlash(ws), "route": r.ID}
	var (
		mu    sync.Mutex
		reads []string
	)
	read := readTool(ws, func(name string) {
		mu.Lock()
		reads = append(reads, name)
		mu.Unlock()
	})
	h := startSDK(t, bin, nil, []string{"--layer", writeLayer(t, expand(layerT, vars))}, read)
	session := h.create(t, &protocol.ModelRoute{Provider: r.ID, Model: model})
	h.keepExport(t, session)

	run1 := h.awaitRun(t, session, h.prompt(t, session, expand(prompt, vars)), 10*time.Minute)
	evs1 := inRun(h.history(t, session), run1.FirstSeq, run1.LastSeq)
	logInvocations(t, evs1)
	ends := ofType(evs1, "turn/end")
	if len(ends) != 1 {
		t.Fatalf("%d turn/end in turn 1, want 1", len(ends))
	}
	var end struct {
		Reason struct {
			Kind  string `json:"kind"`
			Limit string `json:"limit"`
			Value int    `json:"value"`
		} `json:"reason"`
	}
	decode(t, ends[0], &end)
	if end.Reason.Kind != "budget" || end.Reason.Limit != "steps" || end.Reason.Value != 2 {
		t.Fatalf("turn 1 ended %+v, want budget{limit: steps, value: 2}", end.Reason)
	}
	if n := len(ofType(evs1, "step/start")); n != 2 {
		t.Fatalf("turn 1 ran %d steps under maxSteps 2", n)
	}
	assertCallsPaired(t, evs1)
	for _, e := range ofType(evs1, "assistant/message", "assistant/attempt") {
		requireInvocation(t, e, r)
	}
	mu.Lock()
	first := map[string]bool{}
	for _, name := range reads {
		first[name] = true
	}
	n1 := len(reads)
	mu.Unlock()
	// The client returned each call unexecuted and the harness ran it by asking
	// this SDK client (M1-D-PIPE-6 A): one read per tool/result.
	if results := len(ofType(evs1, "tool/result")); n1 != results {
		t.Fatalf("turn 1 holds %d tool/result, the scenario's tool was asked %d times", results, n1)
	}

	run2 := h.awaitRun(t, session, h.prompt(t, session, "continue"), 10*time.Minute)
	evs2 := inRun(h.history(t, session), run2.FirstSeq, run2.LastSeq)
	logInvocations(t, evs2)
	if len(ofType(evs2, "turn/start")) != 1 {
		t.Fatal("\"continue\" did not start a turn")
	}
	assertCallsPaired(t, evs2)
	mu.Lock()
	later := append([]string(nil), reads[n1:]...)
	mu.Unlock()
	progressed := false
	for _, name := range later {
		progressed = progressed || !first[name]
	}
	if !progressed {
		mu.Lock()
		got := append([]string(nil), reads[:n1]...)
		mu.Unlock()
		t.Fatalf("turn 2 read %v after turn 1 read %v: no progress from the history", later, got)
	}
}
