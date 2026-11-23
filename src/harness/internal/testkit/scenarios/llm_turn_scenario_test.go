//go:build scenario

package scenarios

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestTurnWithReasoningAndToolCall: a turn with reasoning and a tool call on a
// signed-in client route (areas/llm.md DH-E-LLM-1; DH-D-LLM-1, DH-D-LLM-2,
// DH-D-LLM-6, on the route of DH-D-LLM-12). Authored in DH-S-9 and re-authored
// onto the client routes by gap-closure-plan 16.5.8 (M1-D-CLOSE-4 A); executed
// in DH-S-17, which requires the agent loop (DH-S-11) and the headless launcher
// and titles (DH-S-16).
//
// Given the built dsh, a fresh temporary home, no API key anywhere, the user's
// own installed official client signed in as themselves, its route registered
// by a configuration layer, a model and reasoning effort at which it reasons and
// one harmless test tool (a client tool the scenario answers), when, per route,
// (1) a prompt that requires the tool is sent, (2) the turn finishes and (3) a
// second prompt is sent and cancelled after its first text delta, then turn 1
// has two steps; each attempt is exactly one assistant/message recording its
// client invocation (the route's client, its version, the argument and
// standard-input digests and a client session id of its own); the first carries
// reasoning and a tool call whose arguments are valid JSON, which the client did
// not run and the harness ran once through its pipeline; every message reports
// usage and nothing of an attempt streams after its end; the second step is
// accepted. Turn 2 ends aborted with its cause and one settlement recording the
// stopped client's invocation: an interrupted assistant/message holding the
// streamed text with no tool call when a prefix was kept (the usual case, the
// cancel coming after a text delta), or an assistant/attempt of class
// cancelled when nothing was, as the driver persists them (DH-E-AGENT-2); and
// the session's tokenUsage equals the
// sum of the client-reported usage of all three attempts. A client route renders
// no reasoning back to the client (DH-T-LLM-18), so, unlike on a keyed Messages
// route, the second request is not checked for it. Cleanup: the home is deleted
// by the launcher; nothing of the user's sign-in or client settings is touched;
// on failure the exported log is kept (its path is logged) and the client
// invocations are logged.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_LLM1_LAYER is the layer text over {route} that registers
// that client route; DSH_SCENARIO_<route>_MODEL and DSH_SCENARIO_<route>_EFFORT
// (<route> is CLAUDE_CODE or COPILOT_CLI) the model and a reasoning effort at
// which it reasons; DSH_SCENARIO_LLM1_PROMPT a prompt that requires calling the
// tool scenario_echo once; DSH_SCENARIO_LLM1_PROMPT2 a prompt whose answer is
// long enough to cancel mid-stream; DSH_SCENARIO_LLM1_USAGE the session/query
// text of the command whose result carries the session's tokenUsage. The client
// is the one on the PATH, signed in beforehand by the user.
//
//dsh:scenario DH-E-LLM-1
func TestTurnWithReasoningAndToolCall(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_LLM1_LAYER", "layer text over {route} that registers that client route")
	prompt1 := need(t, "DSH_SCENARIO_LLM1_PROMPT", "a prompt that requires scenario_echo")
	prompt2 := need(t, "DSH_SCENARIO_LLM1_PROMPT2", "a prompt with a long answer")
	usageQuery := need(t, "DSH_SCENARIO_LLM1_USAGE", "the session/query text that reports tokenUsage")
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		turnWithReasoningAndToolCall(t, bin, r, expand(layerT, map[string]string{"route": r.ID}), prompt1, prompt2, usageQuery)
	})
}

// turnWithReasoningAndToolCall is DH-E-LLM-1 on route r.
func turnWithReasoningAndToolCall(t *testing.T, bin string, r clientRoute, layer, prompt1, prompt2, usageQuery string) {
	t.Helper()
	model, effort := r.model(t), r.effort(t)
	var (
		callsMu sync.Mutex
		calls   []protocol.ToolCallParams
	)
	echo := sdkTool{
		Decl: protocol.ToolDeclaration{
			Name:        "scenario_echo",
			Description: "Echoes its text argument back. Harmless.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		},
		Answer: func(p protocol.ToolCallParams) protocol.ToolCallResult {
			callsMu.Lock()
			calls = append(calls, p)
			callsMu.Unlock()
			var args struct {
				Text string `json:"text"`
			}
			if err := json.Unmarshal(p.Arguments, &args); err != nil {
				return protocol.ToolCallResult{IsError: true, Content: []protocol.ContentBlock{{Type: "text", Text: err.Error()}}}
			}
			return protocol.ToolCallResult{Content: []protocol.ContentBlock{{Type: "text", Text: args.Text}}}
		},
	}
	h := startSDK(t, bin, nil, []string{"--layer", writeLayer(t, layer)}, echo)
	session := h.create(t, &protocol.ModelRoute{Provider: r.ID, Model: model, ReasoningEffort: effort})
	h.keepExport(t, session)

	// (1)-(2) The tool turn.
	run1 := h.awaitRun(t, session, h.prompt(t, session, prompt1), 10*time.Minute)
	evs1 := inRun(h.history(t, session), run1.FirstSeq, run1.LastSeq)
	logInvocations(t, evs1)
	if end := turnEnd(t, evs1); end.Reason.Kind != "completed" {
		t.Fatalf("turn 1 ended %q, want completed", end.Reason.Kind)
	}
	if n := len(ofType(evs1, "step/start")); n != 2 {
		t.Fatalf("turn 1 has %d steps, want 2", n)
	}
	if n := len(ofType(evs1, "assistant/attempt")); n != 0 {
		t.Fatalf("turn 1 has %d failed attempts, want every attempt to be one assistant/message", n)
	}
	msgs := ofType(evs1, "assistant/message")
	if len(msgs) != 2 {
		t.Fatalf("turn 1 has %d assistant/message, want 2 (one per step)", len(msgs))
	}
	// One client process per attempt, each with a client session of its own.
	sessions := map[string]bool{}
	for _, m := range msgs {
		inv := requireInvocation(t, m, r)
		if inv.SessionID == "" || sessions[inv.SessionID] {
			t.Fatalf("assistant/message %d names client session %q, want a new one per attempt", m.Seq, inv.SessionID)
		}
		sessions[inv.SessionID] = true
	}
	var first wireSettlement
	decode(t, msgs[0], &first)
	var reasoning, toolUse bool
	for _, b := range first.Message.Blocks {
		switch b.Kind {
		case "reasoning":
			reasoning = reasoning || (b.Reasoning != nil && b.Reasoning.Text != "")
		case "tool_use":
			if b.ToolUse == nil || !json.Valid(b.ToolUse.Args) {
				t.Fatalf("tool call arguments are not valid JSON: %s", msgs[0].Data)
			}
			toolUse = toolUse || b.ToolUse.Name == "scenario_echo"
		}
	}
	if !reasoning || !toolUse {
		t.Fatalf("step 1 message has reasoning %v, tool call %v; want both:\n%s", reasoning, toolUse, msgs[0].Data)
	}
	// The client returned the call unexecuted and the harness ran it, once, by
	// asking this SDK client (M1-D-PIPE-6 A).
	callsMu.Lock()
	nCalls, validArgs := len(calls), len(calls) == 1 && json.Valid(calls[0].Arguments)
	callsMu.Unlock()
	if !validArgs {
		t.Fatalf("the client tool was called %d times (want 1 with valid JSON arguments)", nCalls)
	}
	if len(ofType(evs1, "tool/result")) != 1 {
		t.Fatal("turn 1 does not hold the tool call's result")
	}
	var total wireUsage
	for _, m := range msgs {
		var s wireSettlement
		decode(t, m, &s)
		if s.Usage == nil {
			t.Fatalf("assistant/message %d reports no usage: the client's result must carry usage before finish", m.Seq)
		}
		total.add(*s.Usage)
	}
	h.assertStreamsEnded(t, int64(run1.Turn))

	// (3) The cancelled turn.
	receipt2 := h.prompt(t, session, prompt2)
	h.until(t, 5*time.Minute, "first text delta of turn 2", func() bool {
		for _, s := range h.streams {
			if s.SessionID == session && s.Turn > int64(run1.Turn) && s.Kind == "chunk" && s.Text != "" {
				return true
			}
		}
		return false
	})
	var cancelled protocol.SessionCancelResult
	h.call(t, "session/cancel", protocol.SessionCancelParams{SessionID: session, Scope: "turn", Inbox: "keep"}, &cancelled)
	if !cancelled.Cancelled {
		t.Fatal("session/cancel cancelled nothing")
	}
	run2 := h.awaitRun(t, session, receipt2, 5*time.Minute)
	evs2 := inRun(h.history(t, session), run2.FirstSeq, run2.LastSeq)
	logInvocations(t, evs2)
	end2 := turnEnd(t, evs2)
	if end2.Reason.Kind != "aborted" || end2.Reason.Cause == "" {
		t.Fatalf("turn 2 ended %+v, want aborted with its cause", end2.Reason)
	}
	// The driver persists a cancelled attempt that kept a prefix as an
	// interrupted assistant/message (the prefix is history, with no tool call),
	// and one that kept nothing as an assistant/attempt of class cancelled
	// (areas/agent-loop.md DH-E-AGENT-2). The cancel comes after the first text
	// delta, so a prefix is the usual case; either records the stopped client's
	// invocation.
	attempts := ofType(evs2, "assistant/attempt", "assistant/message")
	if len(attempts) != 1 {
		t.Fatalf("turn 2 settled %d attempts, want exactly one", len(attempts))
	}
	var aborted wireSettlement
	decode(t, attempts[0], &aborted)
	switch attempts[0].Type {
	case "assistant/message":
		h.mu.Lock()
		shown := streamedText(h, session, int64(run2.Turn))
		h.mu.Unlock()
		var kept strings.Builder
		if aborted.Message != nil {
			for _, b := range aborted.Message.Blocks {
				if b.ToolUse != nil {
					t.Fatalf("turn 2's interrupted message carries a tool call:\n%s", attempts[0].Data)
				}
				if b.Text != nil {
					kept.WriteString(b.Text.Text)
				}
			}
		}
		if shown == "" || !strings.HasPrefix(kept.String(), shown) {
			t.Fatalf("turn 2's interrupted message holds %q, want the streamed text %q", kept.String(), shown)
		}
	default:
		if aborted.Failure == nil || aborted.Failure.Class != "cancelled" {
			t.Fatalf("turn 2's attempt failure %+v, want class cancelled (the client stopped by the cancel)", aborted.Failure)
		}
	}
	requireInvocation(t, attempts[0], r)
	if aborted.Usage != nil {
		total.add(*aborted.Usage)
	}

	// tokenUsage equals the sum of the client-reported usage of all three attempts.
	var q protocol.SessionQueryResult
	h.call(t, "session/query", protocol.SessionQueryParams{SessionID: session, Content: []protocol.ContentBlock{{Type: "text", Text: usageQuery}}}, &q)
	var reported struct {
		TokenUsage *wireUsage `json:"tokenUsage"`
	}
	if err := json.Unmarshal(q.Result, &reported); err != nil || reported.TokenUsage == nil {
		t.Fatalf("%q answered no tokenUsage: %v\n%s", usageQuery, err, q.Result)
	}
	if *reported.TokenUsage != total {
		t.Fatalf("tokenUsage %+v, want the sum of the three attempts %+v", *reported.TokenUsage, total)
	}
}

// assertStreamsEnded checks that every attempt streamed in turn, each one
// observed by any notification, ended exactly once, and that nothing of an
// attempt was streamed after its end.
func (h *sdkHost) assertStreamsEnded(t *testing.T, turn int64) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	type key struct{ step, attempt int64 }
	ends := map[key]int{}
	var seen []key
	for _, s := range h.streams {
		if s.Turn != turn {
			continue
		}
		k := key{s.Step, s.Attempt}
		n, ok := ends[k]
		if !ok {
			seen = append(seen, k)
		}
		if n > 0 {
			t.Fatalf("turn %d step %d attempt %d streamed %q after its end", turn, s.Step, s.Attempt, s.Kind)
		}
		if s.Kind == "end" {
			n++
		}
		ends[k] = n
	}
	if len(seen) == 0 {
		t.Fatalf("turn %d streamed nothing", turn)
	}
	for _, k := range seen {
		if ends[k] != 1 {
			t.Fatalf("turn %d step %d attempt %d streamed without an end", turn, k.step, k.attempt)
		}
	}
}

// logRequestIDs logs the provider request ids of the settlements among evs, so a
// failed run can be traced at the provider. The keyed-route scenarios use it; the
// client-route scenarios log the client invocations instead (logInvocations).
func logRequestIDs(t *testing.T, evs []protocol.Event) {
	t.Helper()
	var ids []string
	for _, e := range ofType(evs, "assistant/attempt") {
		var s wireSettlement
		if json.Unmarshal(e.Data, &s) == nil && s.Failure != nil && s.Failure.RequestID != "" {
			ids = append(ids, s.Failure.RequestID)
		}
	}
	if len(ids) > 0 {
		t.Logf("provider request ids: %s", strings.Join(ids, ", "))
	}
}
