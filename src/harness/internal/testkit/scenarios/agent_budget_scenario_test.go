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
// (areas/agent-loop.md DH-E-AGENT-4; DH-D-AGENT-3). Authored in DH-S-11; executed
// in DH-S-17, which requires the agent loop (DH-S-11) and the headless launcher
// (DH-S-16).
//
// Given an empty temporary home, profile headless with budget.maxSteps 2, one real
// provider and a small model, its key supplied at run time, a temporary workspace
// holding four small files and a prompt that needs at least four tool calls in
// sequence (read each file in turn), when the prompt is sent, the session goes
// idle and "continue" is sent, then the first turn ends budget{limit: steps,
// value: 2} with every call paired with its result, and the second turn starts
// from that history and makes progress: it reads a file the first turn did not.
// Cleanup: the launcher stops dsh and deletes the home; the workspace lies under
// t.TempDir(). On failure the export is kept (its path is logged).
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key, handed to the program as
// DEEPSEEK_API_KEY; DSH_SCENARIO_AGENT4_MODEL a small model on route
// deepseek-official; DSH_SCENARIO_AGENT4_LAYER a layer, a template over
// {workspace}, selecting the headless profile with budget.maxSteps: 2 and no title
// route; DSH_SCENARIO_AGENT4_PROMPT a prompt that reads 1.txt, 2.txt, 3.txt and
// 4.txt with scenario_read one at a time, in order.
//
//dsh:scenario DH-E-AGENT-4
func TestBudgetStopsARealToolLoop(t *testing.T) {
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	model := need(t, "DSH_SCENARIO_AGENT4_MODEL", "a small model on deepseek-official")
	layerT := need(t, "DSH_SCENARIO_AGENT4_LAYER", "a headless layer with budget.maxSteps: 2")
	prompt := need(t, "DSH_SCENARIO_AGENT4_PROMPT", "a prompt that reads four files in turn")

	ws := t.TempDir()
	writeFiles(t, ws, map[string]string{"1.txt": "one\n", "2.txt": "two\n", "3.txt": "three\n", "4.txt": "four\n"})
	vars := map[string]string{"workspace": filepath.ToSlash(ws)}
	var (
		mu    sync.Mutex
		reads []string
	)
	read := readTool(ws, func(name string) {
		mu.Lock()
		reads = append(reads, name)
		mu.Unlock()
	})
	h := startSDK(t, dshBinary(t), []string{"DEEPSEEK_API_KEY=" + key}, []string{"--layer", writeLayer(t, expand(layerT, vars))}, read)
	session := h.create(t, &protocol.ModelRoute{Provider: "deepseek-official", Model: model})
	h.keepExport(t, session)

	run1 := h.awaitRun(t, session, h.prompt(t, session, expand(prompt, vars)), 10*time.Minute)
	evs1 := inRun(h.history(t, session), run1.FirstSeq, run1.LastSeq)
	logRequestIDs(t, evs1)
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
	mu.Lock()
	first := map[string]bool{}
	for _, name := range reads {
		first[name] = true
	}
	n1 := len(reads)
	mu.Unlock()

	run2 := h.awaitRun(t, session, h.prompt(t, session, "continue"), 10*time.Minute)
	evs2 := inRun(h.history(t, session), run2.FirstSeq, run2.LastSeq)
	logRequestIDs(t, evs2)
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
		t.Fatalf("turn 2 read %v after turn 1 read %v: no progress from the history", later, reads[:n1])
	}
}
