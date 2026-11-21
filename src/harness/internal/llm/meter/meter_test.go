package meter_test

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func stamp(t *testing.T, seq int, d event.Draft) event.Event {
	t.Helper()
	return event.Of(must(d.Stamp(event.Seq(seq), int64(1000+seq))))
}

func message(t *testing.T, seq int, u *vocab.Usage) event.Event {
	t.Helper()
	m := must(vocab.RestoreMessage(fmt.Sprintf("a%d", seq), vocab.RoleAssistant, nil, vocab.TextBlock("answer")))
	return stamp(t, seq, must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: m, Usage: u})))
}

func attempt(t *testing.T, seq int, u *vocab.Usage) event.Event {
	t.Helper()
	f := must(vocab.NewFailure(vocab.CodeServer, "overloaded"))
	return stamp(t, seq, must(event.AssistantAttempt.Draft(event.AssistantAttemptPayload{Usage: u, Failure: f})))
}

func user(t *testing.T, seq int) event.Event {
	t.Helper()
	m := must(vocab.RestoreMessage(fmt.Sprintf("u%d", seq), vocab.RoleUser, nil, vocab.TextBlock("more")))
	return stamp(t, seq, must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: m})))
}

// DH-T-LLM-15: three settlements with known token counts (a failed attempt, its
// retry, and the next step) fold to the expected billed totals, in disjoint
// buckets, each attempt counted once; occupancy is the last reporting attempt's
// context; folding from a checkpoint equals folding from zero; the state does
// not grow with the message count; the estimate and calibration are
// deterministic.
func TestUsageFoldedFromSettlements(t *testing.T) {
	d := meter.UsageView()
	evs := []event.Event{
		user(t, 0),
		attempt(t, 1, &vocab.Usage{Input: 100, Output: 5}),
		message(t, 2, &vocab.Usage{Input: 20, CacheRead: 80, CacheWrite: 10, Output: 30}),
		user(t, 3),
		message(t, 4, &vocab.Usage{Input: 15, CacheRead: 140, Output: 12}),
	}
	got := views.Refold(d, session.Header{}, evs)
	want := vocab.Usage{Input: 135, CacheRead: 220, CacheWrite: 10, Output: 47}
	if got.Billed != want || got.Attempts != 3 || got.Reported != 3 {
		t.Fatalf("billed %+v over %d/%d attempts, want %+v over 3", got.Billed, got.Attempts, got.Reported, want)
	}
	if meter.Total(got.Billed) != 412 {
		t.Fatalf("total %d, want 412", meter.Total(got.Billed))
	}
	if got.Occupancy() != 15+140+12 || got.LastSeq != 4 {
		t.Fatalf("occupancy %d at %d, want 167 at 4", got.Occupancy(), got.LastSeq)
	}
	if p := got.Pressure(1000); p != 0.167 {
		t.Fatalf("pressure %v", p)
	}

	// A stored row taken up by the log after it equals the fold from zero.
	row := must(json.Marshal(views.Refold(d, session.Header{}, evs[:2])))
	var restored meter.Usage
	must(0, json.Unmarshal(row, &restored))
	if from := views.FoldFrom(d, restored, evs[2:]); !reflect.DeepEqual(from, got) {
		t.Fatalf("from a checkpoint %+v, from zero %+v", from, got)
	}

	// An attempt that reported no usage counts as an attempt, bills nothing and
	// leaves the occupancy; the state's size does not grow with the log.
	more := append([]event.Event(nil), evs...)
	more = append(more, attempt(t, 5, nil))
	for i := 6; i < 206; i++ {
		more = append(more, user(t, i))
	}
	long := views.Refold(d, session.Header{}, more)
	if long.Attempts != 4 || long.Billed != want || long.Occupancy() != got.Occupancy() {
		t.Fatalf("after a usage-less attempt %+v", long)
	}
	if a, b := len(must(json.Marshal(got))), len(must(json.Marshal(long))); a != b {
		t.Fatalf("state grew from %d to %d bytes over 200 messages", a, b)
	}

	// Registered once in a registry, as a stored fold.
	reg := views.New()
	if h := must(meter.Register(reg)); h.Key() != meter.UsageKey {
		t.Fatalf("key %q", h.Key())
	}

	// The estimate is a deterministic heuristic; calibration is a ratio.
	m := must(vocab.RestoreMessage("x", vocab.RoleUser, nil, vocab.TextBlock("12345678"), vocab.TextBlock("")))
	if meter.EstimateText("12345678") != 2 || meter.EstimateText("123456789") != 3 || meter.EstimateText("") != 0 {
		t.Fatal("text estimate")
	}
	if n := meter.EstimateMessage(m); n != meter.MessageTokens+2*meter.BlockTokens+2 || n != meter.EstimateMessage(m) {
		t.Fatalf("message estimate %d", n)
	}
	if f := meter.Calibration(150, 100); f != 1.5 || meter.Calibrated(10, f) != 15 || meter.Calibration(0, 100) != 1 {
		t.Fatalf("calibration %v", f)
	}
}

func system(t *testing.T, seq int, op event.Op, text string) event.Event {
	t.Helper()
	m := must(vocab.RestoreMessage(fmt.Sprintf("s%d", seq), vocab.RoleSystem, nil, vocab.TextBlock(text)))
	var cites []event.Seq
	if op.Kind == event.OpReplace {
		for s := op.Start; s <= op.End; s++ {
			cites = append(cites, s)
		}
	}
	return stamp(t, seq, must(event.SystemMessage.Draft(op, event.MessagePayload{Message: m}, cites...)))
}

// DH-T-LLM-15: a system-prompt replacement is repriced at once, before any new
// usage sample, and the next sample takes the change in; folding from a stored
// checkpoint between the replacement and the sample equals folding from zero.
func TestSystemPromptReplacementIsRepricedAtOnce(t *testing.T) {
	d := meter.UsageView()
	short := strings.Repeat("a", 40) // 10 tokens of text
	long := strings.Repeat("b", 400) // 100 tokens of text
	frame := meter.MessageTokens + meter.BlockTokens
	evs := []event.Event{
		system(t, 0, event.Append(), short),
		user(t, 1),
	}
	before := views.Refold(d, session.Header{}, evs)
	if before.Occupancy() != frame+10 || before.SystemTokens() != frame+10 {
		t.Fatalf("before any sample: occupancy %d, system %d, want %d", before.Occupancy(), before.SystemTokens(), frame+10)
	}
	evs = append(evs, message(t, 2, &vocab.Usage{Input: 300, Output: 20}))
	sampled := views.Refold(d, session.Header{}, evs)
	if sampled.Occupancy() != 320 {
		t.Fatalf("sampled occupancy %d, want 320", sampled.Occupancy())
	}

	// The replacement of the system prompt at 0 is priced in the same step.
	evs = append(evs, system(t, 3, event.Replace(0, 0), long))
	replaced := views.Refold(d, session.Header{}, evs)
	if want := 320 + 90; replaced.Occupancy() != want || len(replaced.System) != 1 || replaced.System[0].Seq != 3 {
		t.Fatalf("after the replacement: occupancy %d, system %+v, want %d", replaced.Occupancy(), replaced.System, want)
	}
	if len(sampled.System) != 1 || sampled.System[0].Seq != 0 {
		t.Fatalf("the replacement wrote into an earlier state: %+v", sampled.System)
	}

	// A checkpoint between the replacement and the next sample folds on equal.
	row := must(json.Marshal(replaced))
	var restored meter.Usage
	must(0, json.Unmarshal(row, &restored))
	tail := []event.Event{message(t, 4, &vocab.Usage{Input: 400, Output: 10})}
	all := views.Refold(d, session.Header{}, append(append([]event.Event(nil), evs...), tail...))
	if from := views.FoldFrom(d, restored, tail); !reflect.DeepEqual(from, all) {
		t.Fatalf("from a checkpoint %+v, from zero %+v", from, all)
	}
	if all.Occupancy() != 410 {
		t.Fatalf("the next sample's occupancy %d, want 410", all.Occupancy())
	}

	// An appended second system prompt adds its price; neither drops the other.
	two := views.Refold(d, session.Header{}, append(append([]event.Event(nil), evs...), system(t, 4, event.Append(), short)))
	if two.SystemTokens() != 2*frame+110 || two.Occupancy() != 320+90+frame+10 {
		t.Fatalf("two system prompts: system %d, occupancy %d", two.SystemTokens(), two.Occupancy())
	}
	if p := (meter.Usage{}).Pressure(0); p != 0 {
		t.Fatalf("pressure of no window %v", p)
	}
}

// Measure prices a current surface alone: every node's message, calibrated;
// a replaced node is priced by its replacement only.
func TestMeasureTheCurrentSurface(t *testing.T) {
	st := surface.NewState(event.Default())
	for _, ev := range []event.Event{
		system(t, 0, event.Append(), strings.Repeat("a", 40)),
		user(t, 1),
		system(t, 2, event.Replace(0, 0), strings.Repeat("b", 80)),
	} {
		if err := st.Fold(ev.Record(), true); err != nil {
			t.Fatal(err)
		}
	}
	frame := meter.MessageTokens + meter.BlockTokens
	want := frame + 20 + frame + meter.EstimateText("more")
	if n := must(meter.Measure(st.History(), 1)); n != want {
		t.Fatalf("measured %d, want %d", n, want)
	}
	if n := must(meter.Measure(st.History(), 2)); n != 2*want {
		t.Fatalf("calibrated %d, want %d", n, 2*want)
	}
	if n := must(meter.Measure(surface.History{}, 1)); n != 0 {
		t.Fatalf("an empty surface measured %d", n)
	}
}

// The estimate prices every block kind: reasoning and its signature, a tool
// call's id, name and arguments, a tool result's text and image parts, and an
// image; a size not yet read is priced by bytes.
func TestEstimateEveryBlockKind(t *testing.T) {
	cases := []struct {
		name string
		role vocab.Role
		b    vocab.Block
		want int
	}{
		{"reasoning", vocab.RoleAssistant, vocab.ReasoningBlock("12345678", "1234", false), 3},
		{"tool use", vocab.RoleAssistant, vocab.ToolUseBlock("1234", "read", []byte(`{"p":"x"}`)), 1 + 1 + 3},
		{"tool result", vocab.RoleTool, vocab.ToolResultBlock("1234", false, vocab.TextPart("12345678"), vocab.ImagePart("image/png", "att1")), 1 + 2 + meter.ImageTokens},
		{"image", vocab.RoleUser, vocab.ImageBlock("image/png", "att1"), meter.ImageTokens},
	}
	for _, c := range cases {
		m := must(vocab.RestoreMessage("x", c.role, nil, c.b))
		if n := meter.EstimateMessage(m); n != meter.MessageTokens+meter.BlockTokens+c.want {
			t.Errorf("%s: %d, want %d", c.name, n, meter.MessageTokens+meter.BlockTokens+c.want)
		}
	}
	if meter.EstimateBytes(0) != 0 || meter.EstimateBytes(9) != 3 {
		t.Fatal("bytes estimate")
	}
	if meter.Calibrated(10, 0) != 10 || meter.Calibrated(-1, 2) != 0 {
		t.Fatal("calibrated edge")
	}
}
