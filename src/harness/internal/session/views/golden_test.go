package views_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

// goldenLog is the fixed log the built-in views' goldens fold: three turns, each
// model attempt timed, with a failed attempt, a tool error, a rewrite of a tool
// result and a compaction of the first turn's history, and three outcomes
// (completed, error, aborted). Record i is stamped at the logical time
// 1000*(i+1) ms, no wall clock. unsettled is the length of the log before the
// second turn's turn/end: its last turn open.
func goldenLog(t *testing.T) (evs []event.Event, unsettled int) {
	t.Helper()
	var recs []event.Record
	add := func(d event.Draft) event.Seq {
		seq := event.Seq(len(recs))
		recs = append(recs, must(d.Stamp(seq, int64(1000*(len(recs)+1)))))
		return seq
	}
	timing := func(start, first, end int64) *event.Timing {
		return &event.Timing{Start: start, FirstToken: first, End: end}
	}
	prompt := func(id, text string) event.Draft {
		return must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: bmsg(id, vocab.RoleUser, vocab.TextBlock(text))}))
	}
	answer := func(id, text string, tm *event.Timing) event.Draft {
		return must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: bmsg(id, vocab.RoleAssistant, vocab.TextBlock(text)),
			Usage:   &vocab.Usage{Input: 200, Output: 40, Total: 240}, Timing: tm}))
	}
	toolStep := func(turn, step int, isErr bool, tm *event.Timing) event.Seq {
		call := fmt.Sprintf("call-%d-%d", turn, step)
		add(must(event.StepStartType.Draft(event.StepStart{Turn: turn, Step: step})))
		add(must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: bmsg(fmt.Sprintf("a%d-%d", turn, step), vocab.RoleAssistant, vocab.ToolUseBlock(call, "ls", json.RawMessage(`{"dir":"."}`))),
			Usage:   &vocab.Usage{Input: 100, CacheRead: 10, Output: 5, Total: 115}, Timing: tm})))
		add(must(event.ToolCall.Draft(event.ToolCallPayload{CallID: call, Name: "ls", Arguments: `{"dir":"."}`})))
		res := add(must(event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: call, IsError: isErr,
			Message: bmsg(fmt.Sprintf("r%d-%d", turn, step), vocab.RoleTool, vocab.ToolResultBlock(call, isErr, vocab.TextPart("a.txt b.txt c.txt")))})))
		add(must(event.StepEndType.Draft(event.StepEnd{Turn: turn, Step: step})))
		return res
	}
	end := func(turn int, kind string) {
		add(must(event.TurnEndType.Draft(event.TurnEnd{Turn: turn, Reason: event.Reason{Kind: kind}})))
	}

	// Turn 1: a failed attempt, then a tool step whose result is an error, then
	// the answer; completed.
	add(must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})))
	p1 := add(prompt("u1", "  List   the files\nin this   directory, please.  "))
	add(must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})))
	add(must(event.AssistantAttempt.Draft(event.AssistantAttemptPayload{
		Usage: &vocab.Usage{Input: 100}, Timing: timing(10, 0, 60),
		Failure: &vocab.Failure{Code: vocab.CodeServer, Class: vocab.Transient, Message: "overloaded"}})))
	add(must(event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: 1})))
	toolStep(1, 2, true, timing(100, 130, 300))
	add(must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 3})))
	a1 := add(answer("a1-3", "The directory holds a.txt, b.txt and c.txt.", timing(400, 420, 700)))
	add(must(event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: 3})))
	end(1, event.ReasonCompleted)

	// Turn 2, by a helper agent: a tool step whose result is rewritten, a
	// compaction of turn 1's history into one summary prompt, then the answer;
	// it ends with an error.
	add(must(event.TurnStartType.Draft(event.TurnStart{Turn: 2, Agent: "helper"})))
	add(prompt("u2", "Now count the lines of each file."))
	res := toolStep(2, 1, false, timing(1000, 1100, 1500))
	add(must(event.ToolResult.Draft(event.Replace(res, res), event.ToolResultPayload{CallID: "call-2-1",
		Message: bmsg("r2-1x", vocab.RoleTool, vocab.ToolResultBlock("call-2-1", false, vocab.TextPart("[elided]")))}, res)))
	var turn1 []event.Seq
	for s := p1; s <= a1; s++ {
		if typ := recs[s].Type; typ == event.UserMessage.Name() || typ == event.AssistantMessage.Name() || typ == event.ToolResult.Name() {
			turn1 = append(turn1, s)
		}
	}
	add(must(event.UserMessage.Draft(event.Replace(p1, a1), event.UserMessagePayload{
		Message: bmsg("sum1", vocab.RoleUser, vocab.TextBlock("Summary: the directory holds three text files."))}, turn1...)))
	add(must(event.StepStartType.Draft(event.StepStart{Turn: 2, Step: 2})))
	add(answer("a2-2", "a.txt has 3 lines; b.txt and c.txt have 1.", timing(1600, 1650, 2000)))
	add(must(event.StepEndType.Draft(event.StepEnd{Turn: 2, Step: 2})))
	unsettled = len(recs)
	end(2, event.ReasonError)

	// Turn 3: one answer, aborted.
	add(must(event.TurnStartType.Draft(event.TurnStart{Turn: 3})))
	add(prompt("u3", "Thanks."))
	add(must(event.StepStartType.Draft(event.StepStart{Turn: 3, Step: 1})))
	add(answer("a3-1", "You are welcome.", timing(3000, 3010, 3100)))
	add(must(event.StepEndType.Draft(event.StepEnd{Turn: 3, Step: 1})))
	end(3, event.ReasonAborted)

	if _, err := surface.Replay(nil, recs); err != nil {
		t.Fatalf("the golden log is not a valid log: %v", err)
	}
	for _, r := range recs {
		evs = append(evs, event.Of(r))
	}
	return evs, unsettled
}

// goldenJSON is v as indented JSON with a final newline.
func goldenJSON(v any) string {
	return string(must(json.MarshalIndent(v, "", "  "))) + "\n"
}

// Golden logs of the built-in views (DH-D-VIEW-3): the list summary, the turn
// index first with its last turn unsettled and then settled, the statistics
// (every attempt timed, counts by outcome) and the event index after a
// replacement (a rewritten tool result and a compacted turn, shadowed with the
// seq that replaced them), each folded from goldenLog and compared with
// testdata/builtin/*.golden (re-record with -update).
//
//dsh:test DH-T-VIEW-9
func TestBuiltinViewGoldens(t *testing.T) {
	evs, unsettled := goldenLog(t)
	h := session.Header{ID: "golden"}

	open := views.Refold(views.TurnIndexView(), h, evs[:unsettled])
	if e, ok := open.Open(); !ok || e.Turn != 2 {
		t.Fatalf("before turn 2 ends, the open turn = %+v, %v", e, ok)
	}
	settled := views.Refold(views.TurnIndexView(), h, evs)
	if _, ok := settled.Open(); ok {
		t.Fatalf("every turn of the log is settled, yet one is open: %+v", settled)
	}
	stats := views.Refold(views.StatsView(), h, evs)
	if stats.Timed != stats.Attempts || stats.Attempts != stats.AssistantMessages+stats.FailedAttempts {
		t.Fatalf("not every attempt is timed: %+v", stats)
	}
	idx := views.Refold(views.EventIndexView(), h, evs)
	replaced := 0
	for _, e := range idx.Events {
		if e.Replaced {
			replaced++
		}
	}
	if replaced == 0 {
		t.Fatalf("the event index shows no replacement: %+v", idx)
	}

	golden.Assert(t, "builtin/list_summary", goldenJSON(views.Refold(views.ListSummaryView(), h, evs)))
	golden.Assert(t, "builtin/turn_index_unsettled", goldenJSON(open))
	golden.Assert(t, "builtin/turn_index_settled", goldenJSON(settled))
	golden.Assert(t, "builtin/stats", goldenJSON(stats))
	golden.Assert(t, "builtin/event_index_replaced", goldenJSON(idx))
}
