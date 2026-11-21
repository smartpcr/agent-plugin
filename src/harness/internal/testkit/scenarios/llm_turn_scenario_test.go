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

// TestTurnWithReasoningAndToolCall: a turn with reasoning and a tool call on the
// vendor's own protocol (areas/llm.md DH-E-LLM-1; DH-D-LLM-1, DH-D-LLM-2,
// DH-D-LLM-6). Authored in DH-S-9; executed in DH-S-17, which requires the agent
// loop (DH-S-11) and the headless launcher and titles (DH-S-16).
//
// Given the built dsh, a fresh temporary home, a real API key set at run time,
// route deepseek-official on the vendor's Messages endpoint, a model that supports
// reasoning and one harmless test tool (a client tool the scenario answers), when
// (1) a prompt that requires the tool is sent, (2) the turn finishes and (3) a
// second prompt is sent and cancelled after its first text delta, then turn 1 has
// two steps, each attempt is exactly one assistant/message, the first carries
// reasoning and a tool call whose arguments are valid JSON and reports usage, the
// second request carries the reasoning back and is accepted; turn 2 ends aborted
// with one assistant/attempt and its cause; and the session's tokenUsage equals
// the sum of the provider-reported usage of all three attempts. Cleanup: the home
// is deleted by the launcher; on failure the exported log is kept (its path is
// logged) and the provider request ids are logged.
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key (the workflow's secret), handed to
// the program as DEEPSEEK_API_KEY; DSH_SCENARIO_LLM1_MODEL the reasoning model id
// on route deepseek-official; DSH_SCENARIO_LLM1_PROMPT a prompt that
// requires calling the tool scenario_echo once; DSH_SCENARIO_LLM1_PROMPT2 a prompt
// whose answer is long enough to cancel mid-stream; DSH_SCENARIO_LLM1_USAGE the
// session/query text of the command whose result carries the session's
// tokenUsage.
//
//dsh:scenario DH-E-LLM-1
func TestTurnWithReasoningAndToolCall(t *testing.T) {
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	model := need(t, "DSH_SCENARIO_LLM1_MODEL", "a reasoning model on deepseek-official")
	prompt1 := need(t, "DSH_SCENARIO_LLM1_PROMPT", "a prompt that requires scenario_echo")
	prompt2 := need(t, "DSH_SCENARIO_LLM1_PROMPT2", "a prompt with a long answer")
	usageQuery := need(t, "DSH_SCENARIO_LLM1_USAGE", "the session/query text that reports tokenUsage")

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
	h := startSDK(t, dshBinary(t), []string{"DEEPSEEK_API_KEY=" + key}, nil, echo)
	session := h.create(t, &protocol.ModelRoute{Provider: "deepseek-official", Model: model})
	h.keepExport(t, session)

	// (1)-(2) The tool turn.
	run1 := h.awaitRun(t, session, h.prompt(t, session, prompt1), 10*time.Minute)
	evs1 := inRun(h.history(t, session), run1.FirstSeq, run1.LastSeq)
	logRequestIDs(t, evs1)
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
			t.Fatalf("assistant/message %d reports no usage: the stream must carry usage before finish", m.Seq)
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
	logRequestIDs(t, evs2)
	end2 := turnEnd(t, evs2)
	if end2.Reason.Kind != "aborted" || end2.Reason.Cause == "" {
		t.Fatalf("turn 2 ended %+v, want aborted with its cause", end2.Reason)
	}
	attempts := ofType(evs2, "assistant/attempt", "assistant/message")
	if len(attempts) != 1 || attempts[0].Type != "assistant/attempt" {
		t.Fatalf("turn 2 settled %d attempts, want exactly one assistant/attempt", len(attempts))
	}
	var aborted wireSettlement
	decode(t, attempts[0], &aborted)
	if aborted.Failure == nil || aborted.Failure.Class != "cancelled" {
		t.Fatalf("turn 2's attempt failure %+v, want class cancelled (the connection closed by the cancel)", aborted.Failure)
	}
	if aborted.Usage != nil {
		total.add(*aborted.Usage)
	}

	// tokenUsage equals the sum of the provider-reported usage of all three attempts.
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

// assertStreamsEnded checks that every streamed attempt of turn ended, and that
// nothing of an attempt was streamed after its end.
func (h *sdkHost) assertStreamsEnded(t *testing.T, turn int64) {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	type key struct{ step, attempt int64 }
	ended := map[key]bool{}
	for _, s := range h.streams {
		if s.Turn != turn {
			continue
		}
		k := key{s.Step, s.Attempt}
		if ended[k] {
			t.Fatalf("turn %d step %d attempt %d streamed %q after its end", turn, s.Step, s.Attempt, s.Kind)
		}
		if s.Kind == "end" {
			ended[k] = true
		}
	}
	if len(ended) == 0 {
		t.Fatalf("turn %d streamed nothing", turn)
	}
}

// logRequestIDs logs the provider request ids of the settlements among evs, so a
// failed run can be traced at the provider.
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
