package views_test

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

func bmsg(id string, role vocab.Role, blocks ...vocab.Block) vocab.Message {
	return must(vocab.RestoreMessage(id, role, nil, blocks...))
}

// turnDrafts is one deterministic turn n of a session: a prompt, a step with a
// tool call and its result (a failed attempt first on every third turn, and a
// rewrite of the result on every fifth), and a final answer step.
func turnDrafts(n int) []event.Draft {
	call := fmt.Sprintf("call-%d", n)
	at := int64(1_700_000_000_000 + n*10_000)
	ds := []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: n})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{
			Message: bmsg(fmt.Sprintf("u%d", n), vocab.RoleUser, vocab.TextBlock(fmt.Sprintf("  prompt   number %d: list the files and explain each of them in detail, please  ", n)))})),
		must(event.StepStartType.Draft(event.StepStart{Turn: n, Step: 1})),
	}
	if n%3 == 0 {
		ds = append(ds, must(event.AssistantAttempt.Draft(event.AssistantAttemptPayload{
			Usage:   &vocab.Usage{Input: 10},
			Timing:  &event.Timing{Start: at, End: at + 50},
			Failure: &vocab.Failure{Code: vocab.CodeServer, Class: vocab.Transient, Message: "overloaded"}})))
	}
	ds = append(ds,
		must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: bmsg(fmt.Sprintf("a%d-1", n), vocab.RoleAssistant, vocab.ToolUseBlock(call, "ls", json.RawMessage(`{"q":1}`))),
			Usage:   &vocab.Usage{Input: 100 + n, CacheRead: 5, Output: 7, Total: 112 + n},
			Timing:  &event.Timing{Start: at + 100, FirstToken: at + 150, End: at + 400}})),
		must(event.ToolCall.Draft(event.ToolCallPayload{CallID: call, Name: "ls", Arguments: `{"q":1}`})),
		must(event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: call, IsError: n%4 == 0,
			Message: bmsg(fmt.Sprintf("r%d", n), vocab.RoleTool, vocab.ToolResultBlock(call, n%4 == 0, vocab.TextPart("a.txt b.txt")))})),
		must(event.StepEndType.Draft(event.StepEnd{Turn: n, Step: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: n, Step: 2})),
		must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: bmsg(fmt.Sprintf("a%d-2", n), vocab.RoleAssistant, vocab.TextBlock(fmt.Sprintf("answer %d", n))),
			Usage:   &vocab.Usage{Input: 120, Output: 30, Total: 150},
			Timing:  &event.Timing{Start: at + 500, FirstToken: at + 520, End: at + 900}})),
		must(event.StepEndType.Draft(event.StepEnd{Turn: n, Step: 2})),
		must(event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: event.Reason{Kind: event.ReasonCompleted}})),
	)
	return ds
}

// rewriteDraft replaces the tool result at seq (of call) with shorter content.
func rewriteDraft(seq event.Seq, n int) event.Draft {
	call := fmt.Sprintf("call-%d", n)
	return must(event.ToolResult.Draft(event.Replace(seq, seq), event.ToolResultPayload{CallID: call,
		Message: bmsg(fmt.Sprintf("r%d-x", n), vocab.RoleTool, vocab.ToolResultBlock(call, false, vocab.TextPart("[elided]")))}, seq))
}

// fillSession appends whole turns to h, then request-context events, up to
// exactly total events.
func fillSession(t *testing.T, h session.Session, total int) {
	t.Helper()
	for n := 1; ; n++ {
		ds := turnDrafts(n)
		if int(h.Len())+len(ds)+1 > total {
			break
		}
		recs, err := h.AppendGroup(bg, ds)
		if err != nil {
			t.Fatalf("turn %d: %v", n, err)
		}
		if n%5 == 0 {
			var result event.Seq
			for i, d := range ds {
				if d.Type() == event.ToolResult.Name() {
					result = recs[i].Seq
				}
			}
			if _, err := h.Append(bg, rewriteDraft(result, n)); err != nil {
				t.Fatalf("rewrite in turn %d: %v", n, err)
			}
		}
	}
	for i := 0; int(h.Len()) < total; i++ {
		if _, err := h.Append(bg, ctxDraft(fmt.Sprintf("fill-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if int(h.Len()) != total {
		t.Fatalf("the session has %d events, want %d", h.Len(), total)
	}
}

// refoldEqualsStored decodes the stored row of d and compares it, as JSON, with d
// refolded from the log; it returns the refolded state.
func refoldEqualsStored[S any](t *testing.T, d views.Definition[S], h session.Header, evs []event.Event, cps map[string]store.Checkpoint) S {
	t.Helper()
	cp, ok := cps[d.Key]
	if !ok {
		t.Fatalf("%s: no stored row", d.Key)
	}
	if cp.StateVersion != d.Version || int(cp.Seq) >= len(evs) {
		t.Fatalf("%s: row at version %d seq %d over %d events", d.Key, cp.StateVersion, cp.Seq, len(evs))
	}
	var stored S
	if err := json.Unmarshal([]byte(cp.State), &stored); err != nil {
		t.Fatalf("%s: decoding the stored row: %v", d.Key, err)
	}
	refold := views.Refold(d, h, evs)
	through := views.Refold(d, h, evs[:cp.Seq+1])
	got, want, at := must(json.Marshal(stored)), must(json.Marshal(refold)), must(json.Marshal(through))
	if string(got) != string(want) || string(at) != string(want) {
		t.Fatalf("%s: stored state differs from the refold:\nstored %s\nrefold %s\nthrough row seq %s", d.Key, got, want, at)
	}
	return refold
}

// Scenario: Refold equals stored for every built-in view (DH-S-8 exit evidence,
// TC-08). Given a fixed 200-event session in a temp home, when each built-in
// view is refolded from the log and compared with its stored state, then all
// four are equal; the event index's classes equal the surface fold's.
func TestRefoldEqualsStoredForEveryBuiltinView(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	reg := views.New()
	b := must(views.RegisterBuiltins(reg))
	st, svc, stop := boot(t, path, reg)
	h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "s-200"}}))
	fillSession(t, h, 200)
	snap := must(reg.Read(bg, h))
	live := must(b.Stats.Get(snap))
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}

	r := must(st.OpenRead(bg, "s-200"))
	recs := must(r.Read(bg, 0, 0, store.ReadOptions{}))
	header := r.Header()
	cpList := must(r.Checkpoints(bg))
	_ = r.Close(bg)
	if len(recs) != 200 {
		t.Fatalf("%d records, want 200", len(recs))
	}
	evs := make([]event.Event, len(recs))
	for i, rec := range recs {
		evs[i] = event.Of(rec)
	}
	cps := map[string]store.Checkpoint{}
	for _, c := range cpList {
		cps[c.Key] = c
	}

	sum := refoldEqualsStored(t, views.ListSummaryView(), header, evs, cps)
	turns := refoldEqualsStored(t, views.TurnIndexView(), header, evs, cps)
	stats := refoldEqualsStored(t, views.StatsView(), header, evs, cps)
	idx := refoldEqualsStored(t, views.EventIndexView(), header, evs, cps)
	stop()

	if sum.Blank || sum.Prompts != len(turns.Turns) || sum.Turns != len(turns.Turns) || sum.Preview == "" || len([]rune(sum.Preview)) > views.PreviewRunes {
		t.Fatalf("list summary = %+v over %d turns", sum, len(turns.Turns))
	}
	for _, e := range turns.Turns {
		if !e.Settled || e.Reason != event.ReasonCompleted || e.Steps != 2 || !e.HasPrompt || e.End <= e.Start {
			t.Fatalf("turn entry = %+v", e)
		}
	}
	if stats.Events != 200 || stats.Attempts != stats.AssistantMessages+stats.FailedAttempts || stats.Timed != stats.Attempts ||
		stats.Outcomes[event.ReasonCompleted] != len(turns.Turns) || stats.ToolErrors == 0 || stats.Replacements == 0 {
		t.Fatalf("stats = %+v", stats)
	}
	if fmt.Sprint(live) != fmt.Sprint(stats) {
		t.Fatalf("live stats %+v differ from the refold %+v", live, stats)
	}

	surf, err := surface.Replay(nil, recs)
	if err != nil {
		t.Fatal(err)
	}
	shadowed := 0
	for _, rec := range recs {
		e, ok := idx.Entry(rec.Seq)
		if !ok || e.Class != surf.Classify(rec).String() {
			t.Fatalf("seq %d (%s): index %+v, surface class %s", rec.Seq, rec.Type, e, surf.Classify(rec))
		}
		if e.Class == views.ClassShadowed {
			shadowed++
			if !e.Replaced {
				t.Fatalf("seq %d is shadowed without a replacement: %+v", rec.Seq, e)
			}
		}
	}
	if shadowed == 0 || len(idx.History) != surf.History().Len() {
		t.Fatalf("%d shadowed, history %d vs surface %d", shadowed, len(idx.History), surf.History().Len())
	}
	for i, seq := range surf.History().Seqs() {
		if idx.History[i] != seq {
			t.Fatalf("history[%d] = %d, surface %d", i, idx.History[i], seq)
		}
	}
	if got := idx.ByCall("call-1"); len(got) != 2 {
		t.Fatalf("events of call-1 = %v", got)
	}
}
