//go:build scenario

package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestKilledDuringToolCallResumesIntoAcceptedHistory: a host killed during a tool
// call resumes into a history a real model accepts (areas/core-session.md
// DH-E-SESSION-2; DH-D-SESSION-5, DH-D-SESSION-2). Authored in DH-S-11 and
// retargeted onto the signed-in client routes of areas/llm.md DH-D-LLM-12 by
// gap-closure-plan 17.4.4 (M1-D-CLOSE-4 A); executed in DH-S-17, which requires
// the agent loop (DH-S-11), the shell tool (DH-S-13) and the headless launcher
// (DH-S-16).
//
// Given the built dsh, a fresh temporary home on the real store, no API key
// anywhere, the user's own installed official client signed in as themselves
// with its route registered by a layer, a workspace in workspace-write mode and
// the shell tool, when, per route, (1) the model is prompted to run a command
// that sleeps for two minutes, (2) the tool/call is durable (read back through
// session/history), (3) the session is exported and the host killed without a
// graceful stop, (4) a new host on the same home resumes the session, (5) it is
// exported again and (6) a prompt asks what happened, then the second export
// begins with the first, byte for byte, and then holds, in order, exactly one
// tool/result with code tools/outcome-unknown citing the call, step/end,
// turn/end with reason interrupted and session/end-seed; resuming a second time
// adds no closer; and the client's model answers step 6 with an
// assistant/message recording the invocation of the route's client, so the
// history rendered to the client (the outcome-unknown result included) has no
// unanswered call. The client ran only while an attempt ran: it returned the
// shell call unexecuted before the command started (M1-D-PIPE-6 A), so the kill
// meets the shell tool's child, not the client. Whether the sleeping process
// survived is the shell tool's assertion (A09), not this scenario's. Cleanup:
// the hosts' trees, the sleeping command included, are stopped by the launcher
// and the home lies under t.TempDir(); nothing of the user's sign-in is
// touched. On failure both exports are kept (their paths are logged).
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_<route>_MODEL (<route> is CLAUDE_CODE or COPILOT_CLI)
// the model; DSH_SCENARIO_SESSION2_LAYER a layer, a template over {route} and
// {workspace}, that registers that client route, selects the headless profile
// and the SQLite store and enables the shell tool; DSH_SCENARIO_SESSION2_PROMPT
// a prompt that makes the model run one shell command sleeping 120 seconds.
//
//dsh:scenario DH-E-SESSION-2
func TestKilledDuringToolCallResumesIntoAcceptedHistory(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_SESSION2_LAYER", "a headless layer over {route} with the SQLite store and the shell tool")
	prompt := need(t, "DSH_SCENARIO_SESSION2_PROMPT", "a prompt that runs a command sleeping 120 seconds")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		killedDuringToolCall(t, bin, r, layerT, prompt)
	})
}

// killedDuringToolCall is DH-E-SESSION-2 on route r.
func killedDuringToolCall(t *testing.T, bin string, r clientRoute, layerT, prompt string) {
	t.Helper()
	ws := t.TempDir()
	vars := map[string]string{"workspace": filepath.ToSlash(ws), "route": r.ID}
	args := []string{"--home", filepath.Join(t.TempDir(), "home"), "--layer", writeLayer(t, expand(layerT, vars))}
	route := &protocol.ModelRoute{Provider: r.ID, Model: r.model(t)}
	// No entry: the launcher's scrubbed environment holds no credential, and the
	// client finds the user's own sign-in in its configuration home.
	var env []string

	// (1)-(3) Run until the call is durable, export, then kill.
	h1 := startSDK(t, bin, env, args)
	var created protocol.SessionCreateResult
	h1.call(t, "session/create", protocol.SessionCreateParams{Cwd: ws, Model: route, PermissionMode: "workspace-write"}, &created)
	session := created.SessionID
	h1.prompt(t, session, prompt)
	var call protocol.Event
	waitUntil(t, 5*time.Minute, "a durable tool/call", func() bool {
		calls := ofType(h1.history(t, session), "tool/call")
		if len(calls) > 0 {
			call = calls[0]
		}
		return len(calls) > 0
	})
	first := h1.history(t, session)
	keepEvents(t, "first export", first)
	kill(t, h1)

	// (4)-(5) Resume on the same home and export again.
	h2 := startSDK(t, bin, env, args)
	resume(t, h2, session, route, true)
	second := h2.history(t, session)
	keepEvents(t, "second export", second)
	if len(second) < len(first) {
		t.Fatalf("the second export has %d events, the first %d", len(second), len(first))
	}
	for i := range first {
		if a, b := mustJSON(t, first[i]), mustJSON(t, second[i]); !bytes.Equal(a, b) {
			t.Fatalf("event %d changed across the restart:\nbefore: %s\nafter:  %s", i, a, b)
		}
	}
	var callID struct {
		CallID string `json:"call_id"`
	}
	decode(t, call, &callID)
	closers := second[len(first):]
	if got := eventTypes(closers); !slices.Equal(got, []string{"tool/result", "step/end", "turn/end", "session/end-seed"}) {
		t.Fatalf("resume appended %v, want exactly tool/result, step/end, turn/end, session/end-seed", got)
	}
	if !bytes.Contains(closers[0].Data, []byte("tools/outcome-unknown")) || !bytes.Contains(closers[0].Data, []byte(callID.CallID)) {
		t.Fatalf("the closing tool/result is not outcome-unknown for call %s: %s", callID.CallID, closers[0].Data)
	}
	var end wireTurnEnd
	decode(t, closers[2], &end)
	if end.Reason.Kind != "interrupted" {
		t.Fatalf("the closing turn/end has reason %q, want interrupted", end.Reason.Kind)
	}

	// A second resume adds no closer.
	kill(t, h2)
	h3 := startSDK(t, bin, env, args)
	resume(t, h3, session, route, false)
	third := h3.history(t, session)
	for _, e := range third[len(second):] {
		if slices.Contains([]string{"tool/result", "step/end", "turn/end"}, e.Type) {
			t.Fatalf("the second resume appended the closer %s: %s", e.Type, e.Data)
		}
	}

	// (6) The model accepts the history: no call is left unanswered.
	run := h3.awaitRun(t, session, h3.prompt(t, session, "What happened to the command you ran?"), 10*time.Minute)
	evs := inRun(h3.history(t, session), run.FirstSeq, run.LastSeq)
	logInvocations(t, evs)
	msgs := ofType(evs, "assistant/message")
	if end := turnEnd(t, evs); end.Reason.Kind != "completed" || len(msgs) == 0 {
		t.Fatalf("the model did not answer after the resume: turn ended %+v", end.Reason)
	}
	for _, m := range msgs {
		requireInvocation(t, m, r)
	}
}

// resume takes the write lease of session on h and checks whether it reported
// closing an interrupted turn.
func resume(t *testing.T, h *sdkHost, session string, route *protocol.ModelRoute, closed bool) {
	t.Helper()
	var r protocol.SessionResumeResult
	h.call(t, "session/resume", protocol.SessionResumeParams{SessionID: session, Model: route}, &r)
	if r.ClosedInterruptedTurn != closed {
		t.Fatalf("session/resume closedInterruptedTurn = %v, want %v", r.ClosedInterruptedTurn, closed)
	}
}

// kill stops h's whole tree with no grace, as a crash would.
func kill(t *testing.T, h *sdkHost) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := h.Process.StopTree(ctx, 0); err != nil {
		t.Fatalf("killing the host: %v", err)
	}
}

// keepEvents writes evs to a file kept past the run when the scenario fails, and
// logs its path; what names the export in the log.
func keepEvents(t *testing.T, what string, evs []protocol.Event) {
	t.Helper()
	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		f, err := os.CreateTemp("", "dsh-scenario-export-*.json")
		if err != nil {
			t.Logf("%s: %v", what, err)
			return
		}
		defer f.Close()
		_ = json.NewEncoder(f).Encode(evs)
		t.Logf("kept the %s at %s", what, f.Name())
	})
}

// eventTypes lists the types of evs in order.
func eventTypes(evs []protocol.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
