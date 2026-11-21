package meter_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
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
	if from := views.FoldFrom(d, restored, evs[2:]); from != got {
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
