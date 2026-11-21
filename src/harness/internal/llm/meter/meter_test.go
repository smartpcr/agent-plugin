package meter_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/meter"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
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
// buckets, each attempt counted once; the context the last step occupied is
// projected from it; folding from a checkpoint equals folding from zero; the
// state does not grow with the message count; the estimate and calibration are
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
	if meter.Total(got.Billed) != 412 || meter.Total(got.Last) != 167 || got.LastSeq != 4 {
		t.Fatalf("total %d, last %d at %d", meter.Total(got.Billed), meter.Total(got.Last), got.LastSeq)
	}
	p := views.Refold(meter.PressureView(), session.Header{}, evs)
	if p.Projected() != 15+140+12 || p.Sample.Seq != 4 || p.Fraction(1000) != 0.167 {
		t.Fatalf("projected %d from %+v", p.Projected(), p.Sample)
	}

	// A stored row taken up by the log after it equals the fold from zero.
	row := must(json.Marshal(views.Refold(d, session.Header{}, evs[:2])))
	var restored meter.Usage
	must(0, json.Unmarshal(row, &restored))
	if from := views.FoldFrom(d, restored, evs[2:]); !reflect.DeepEqual(from, got) {
		t.Fatalf("from a checkpoint %+v, from zero %+v", from, got)
	}

	// An attempt that reported no usage counts as an attempt and bills nothing;
	// no fold's state grows with the log.
	more := append([]event.Event(nil), evs...)
	more = append(more, attempt(t, 5, nil))
	for i := 6; i < 206; i++ {
		more = append(more, user(t, i))
	}
	long := views.Refold(d, session.Header{}, more)
	if long.Attempts != 4 || long.Billed != want || long.Last != got.Last {
		t.Fatalf("after a usage-less attempt %+v", long)
	}
	if a, b := len(must(json.Marshal(got))), len(must(json.Marshal(long))); a != b {
		t.Fatalf("usage grew from %d to %d bytes over 200 messages", a, b)
	}
	// Pressure carries running totals (appended since the sample, the
	// surface's price) and the last change's seq: 20 and 200 messages later
	// its state differs by a digit each only.
	few := views.Refold(meter.PressureView(), session.Header{}, more[:26])
	lp := views.Refold(meter.PressureView(), session.Header{}, more)
	if a, b := len(must(json.Marshal(few))), len(must(json.Marshal(lp))); b > a+3 {
		t.Fatalf("pressure grew from %d to %d bytes over 180 messages", a, b)
	}

	// The three folds are registered once; a second registration is refused.
	reg := views.New()
	must(meter.New(reg, meter.DefaultConfig()))
	if _, err := meter.New(reg, meter.DefaultConfig()); err == nil {
		t.Fatal("the folds registered twice")
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

func cites(op event.Op, sources []event.Seq) []event.Seq {
	if op.Kind != event.OpReplace || sources != nil {
		return sources
	}
	var out []event.Seq
	for s := op.Start; s <= op.End; s++ {
		out = append(out, s)
	}
	return out
}

// system is a system prompt at seq; a replacement cites sources, or every seq
// of its range when sources is nil.
func system(t *testing.T, seq int, op event.Op, text string, sources ...event.Seq) event.Event {
	t.Helper()
	m := must(vocab.RestoreMessage(fmt.Sprintf("s%d", seq), vocab.RoleSystem, nil, vocab.TextBlock(text)))
	return stamp(t, seq, must(event.SystemMessage.Draft(op, event.MessagePayload{Message: m}, cites(op, sources)...)))
}

// summary is a user message replacing the nodes it cites: a compaction.
func summary(t *testing.T, seq int, op event.Op, sources ...event.Seq) event.Event {
	t.Helper()
	m := must(vocab.RestoreMessage(fmt.Sprintf("u%d", seq), vocab.RoleUser, nil, vocab.TextBlock("summary")))
	return stamp(t, seq, must(event.UserMessage.Draft(op, event.UserMessagePayload{Message: m}, cites(op, sources)...)))
}

// DH-T-LLM-15: a system-prompt replacement is repriced at once, before any new
// usage sample, and the next sample takes the change in; folding from a stored
// checkpoint between the replacement and the sample equals folding from zero.
func TestSystemPromptReplacementIsRepricedAtOnce(t *testing.T) {
	d := meter.PressureView()
	short := strings.Repeat("a", 40) // 10 tokens of text
	long := strings.Repeat("b", 400) // 100 tokens of text
	frame := meter.MessageTokens + meter.BlockTokens
	more := meter.EstimateMessage(must(vocab.RestoreMessage("u", vocab.RoleUser, nil, vocab.TextBlock("more"))))
	evs := []event.Event{
		system(t, 0, event.Append(), short),
		user(t, 1),
	}
	before := views.Refold(d, session.Header{}, evs)
	if before.Projected() != frame+10+more || before.System.SystemTotal != frame+10 || before.Sample != nil {
		t.Fatalf("before any sample: projected %d, system %d", before.Projected(), before.System.SystemTotal)
	}
	evs = append(evs, message(t, 2, &vocab.Usage{Input: 300, Output: 20}))
	sampled := views.Refold(d, session.Header{}, evs)
	if sampled.Projected() != 320 || sampled.AppendedSince != 0 {
		t.Fatalf("sampled projection %d", sampled.Projected())
	}

	// The replacement of the system prompt at 0 is priced in the same step.
	evs = append(evs, system(t, 3, event.Replace(0, 0), long))
	replaced := views.Refold(d, session.Header{}, evs)
	if want := 320 + 90; replaced.Projected() != want || len(replaced.System.System) != 1 || replaced.System.System[0].Seq != 3 {
		t.Fatalf("after the replacement: projected %d, system %+v, want %d", replaced.Projected(), replaced.System, want)
	}
	if !replaced.Rewritten || replaced.Compacted {
		t.Fatalf("a system replacement: rewritten %v, compacted %v", replaced.Rewritten, replaced.Compacted)
	}
	if len(sampled.System.System) != 1 || sampled.System.System[0].Seq != 0 {
		t.Fatalf("the replacement wrote into an earlier state: %+v", sampled.System)
	}

	// A checkpoint between the replacement and the next sample folds on equal.
	row := must(json.Marshal(replaced))
	var restored meter.Pressure
	must(0, json.Unmarshal(row, &restored))
	tail := []event.Event{message(t, 4, &vocab.Usage{Input: 400, Output: 10})}
	all := views.Refold(d, session.Header{}, append(append([]event.Event(nil), evs...), tail...))
	if from := views.FoldFrom(d, restored, tail); !reflect.DeepEqual(from, all) {
		t.Fatalf("from a checkpoint %+v, from zero %+v", from, all)
	}
	if all.Projected() != 410 || all.Rewritten {
		t.Fatalf("the next sample's projection %d, rewritten %v", all.Projected(), all.Rewritten)
	}

	// An appended second system prompt adds its price; neither drops the other.
	two := views.Refold(d, session.Header{}, append(append([]event.Event(nil), evs...), system(t, 4, event.Append(), short)))
	if two.System.SystemTotal != 2*frame+110 || two.Projected() != 320+90+frame+10 {
		t.Fatalf("two system prompts: system %d, projected %d", two.System.SystemTotal, two.Projected())
	}
	if p := (meter.Pressure{}).Fraction(0); p != 0 {
		t.Fatalf("pressure of no window %v", p)
	}
}

// surfaceSystems are the seqs of the system nodes the surface itself retains
// after evs: the reference the breakdown's prices must match.
func surfaceSystems(t *testing.T, evs []event.Event) []event.Seq {
	t.Helper()
	st := surface.NewState(event.Default())
	for _, ev := range evs {
		if err := st.Fold(ev.Record(), true); err != nil {
			t.Fatal(err)
		}
	}
	var out []event.Seq
	h := st.History()
	for i := range h.Len() {
		if n := h.At(i); n.Type() == event.SystemMessage.Name() {
			out = append(out, n.Seq())
		}
	}
	return out
}

func priced(b meter.Breakdown) []event.Seq {
	var out []event.Seq
	for _, p := range b.System {
		out = append(out, p.Seq)
	}
	return out
}

// DH-ADR-LLM-9: one price per retained system prompt. Every surface
// replacement that removes a system node removes its price: an empty system
// replacement (which leaves no node), a user compaction covering a later system
// prompt, and a system replacement over a range; each case is checked against
// the surface's own history.
func TestSystemPricesFollowTheSurface(t *testing.T) {
	short := strings.Repeat("a", 40)
	frame := meter.MessageTokens + meter.BlockTokens
	base := []event.Event{
		system(t, 0, event.Append(), short),
		user(t, 1),
		system(t, 2, event.Append(), short),
		user(t, 3),
	}
	cases := []struct {
		name  string
		tail  []event.Event
		total int
	}{
		{"an empty system replacement", []event.Event{system(t, 4, event.Replace(2, 2), "  \n")}, frame + 10},
		{"a compaction over a later system prompt", []event.Event{summary(t, 4, event.Replace(1, 3))}, frame + 10},
		{"a system replacement over a range", []event.Event{system(t, 4, event.Replace(1, 3), short)}, 2 * (frame + 10)},
		{"a compaction that leaves the system prompts", []event.Event{summary(t, 4, event.Replace(3, 3))}, 2 * (frame + 10)},
		// A citation outside the range is provenance: system 0 stays.
		{"a replacement citing a retained system prompt", []event.Event{system(t, 4, event.Replace(2, 2), short, 0, 2)}, 2 * (frame + 10)},
		{"a compaction citing a retained system prompt", []event.Event{summary(t, 4, event.Replace(3, 3), 0, 2, 3)}, 2 * (frame + 10)},
		// A replacement's node takes the position of the first node it
		// replaced: the range 4..5 runs from the summary at user 1's place to
		// user 5, so it covers system 2 although 2 < 4, and system 0 before it
		// is untouched.
		{"a range starting at a replacement's node", []event.Event{
			summary(t, 4, event.Replace(1, 1)),
			user(t, 5),
			system(t, 6, event.Replace(4, 5), short, 4, 2, 3, 5),
		}, frame + 10 + frame + 10},
		{"a range ending at a replacement's node", []event.Event{
			summary(t, 4, event.Replace(3, 3)),
			summary(t, 5, event.Replace(2, 4), 2, 4),
		}, frame + 10},
	}
	for _, c := range cases {
		evs := append(append([]event.Event(nil), base...), c.tail...)
		b := views.Refold(meter.BreakdownView(), session.Header{}, evs)
		if got, want := priced(b), surfaceSystems(t, evs); !slices.Equal(got, want) || b.SystemTotal != c.total {
			t.Errorf("%s: priced %v (total %d), the surface retains %v (want total %d)", c.name, got, b.SystemTotal, want, c.total)
		}
		p := views.Refold(meter.PressureView(), session.Header{}, evs)
		if !reflect.DeepEqual(p.System, b) {
			t.Errorf("%s: pressure's prices %+v, breakdown %+v", c.name, p.System, b)
		}
	}

	// A blank system prompt appended leaves no node and no price.
	evs := append(append([]event.Event(nil), base...), system(t, 4, event.Append(), " "))
	if b := views.Refold(meter.BreakdownView(), session.Header{}, evs); len(b.System) != 2 {
		t.Fatalf("a blank system prompt was priced: %+v", b)
	}
	// A compaction makes the fold's projection an upper bound.
	p := views.Refold(meter.PressureView(), session.Header{}, append(append([]event.Event(nil), base...), summary(t, 4, event.Replace(3, 3))))
	if !p.Compacted || !p.Rewritten {
		t.Fatalf("a compaction was not recorded: %+v", p)
	}
}

var bg = context.Background()

// newSession is a memstore-backed session with a system prompt, a header with
// one tool, and one user message.
func newSession(t *testing.T) (session.Handle, surface.RequestHeader, event.Seq) {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	svc := session.NewService(root, memstore.New(), session.Config{})
	h := must(svc.Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	tool := vocab.ToolSchema{Name: "read", Description: "reads a file", Parameters: json.RawMessage(`{"type":"object"}`)}
	sys := must(vocab.RestoreMessage("s", vocab.RoleSystem, nil, vocab.TextBlock(strings.Repeat("a", 40))))
	u := must(vocab.RestoreMessage("u", vocab.RoleUser, nil, vocab.TextBlock("hello there")))
	rs := must(h.AppendGroup(bg, []event.Draft{
		must(event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: sys})),
		must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "r/a", Tools: []vocab.ToolSchema{tool}})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: u})),
	}))
	hdr, ok := h.RequestHeader()
	if !ok {
		t.Fatal("no request header")
	}
	return h, hdr, rs[2].Seq
}

func answer(t *testing.T, h session.Handle, asOf event.Seq, u *vocab.Usage) event.Seq {
	t.Helper()
	return answerPriced(t, h, asOf, u, 0)
}

// answerPriced settles a step whose request the runtime priced at estimate.
func answerPriced(t *testing.T, h session.Handle, asOf event.Seq, u *vocab.Usage, estimate int) event.Seq {
	t.Helper()
	m := must(vocab.RestoreMessage("a", vocab.RoleAssistant, nil, vocab.TextBlock("the answer")))
	r := must(h.Append(bg, must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
		Message: m, RequestAsOf: &asOf, RequestDigest: "sha256:ab", Usage: u, Estimate: estimate}))))
	return r.Seq
}

// DH-T-LLM-15: the tokenMeter measures the session's current surface, read from
// the session, under a header; the calibration factor is the last sample's
// reported prompt over the heuristic's price of that same request (the nodes up
// to its requestAsOf under the header then in force). Once a compaction or a
// header change rewrites that envelope the new one is scaled by the last such
// factor, not measured raw (token-meter.4).
func TestMeterMeasuresTheSession(t *testing.T) {
	h, hdr, asOf := newSession(t)
	m := must(meter.New(views.New(), meter.DefaultConfig()))
	cold := must(m.Measure(bg, h, hdr))
	if cold.Calibrated || cold.Factor != 1 || cold.Tokens != cold.Estimate || cold.AsOf != h.Len() {
		t.Fatalf("before any sample: %+v", cold)
	}
	if cold.Estimate <= meter.EstimateHeader(hdr) || meter.EstimateHeader(hdr) == 0 {
		t.Fatalf("estimate %d, header %d", cold.Estimate, meter.EstimateHeader(hdr))
	}

	// The provider reports twice the heuristic's price of the request.
	reported := 2 * cold.Estimate
	at := answer(t, h, asOf, &vocab.Usage{Input: reported, Output: 7})
	got := must(m.Measure(bg, h, hdr))
	ans := m.EstimateMessage(must(vocab.RestoreMessage("a", vocab.RoleAssistant, nil, vocab.TextBlock("the answer"))))
	if !got.Calibrated || got.Factor != 2 || got.Estimate != cold.Estimate+ans || got.Tokens != 2*got.Estimate {
		t.Fatalf("after the sample: %+v (answer %d)", got, ans)
	}
	if r := must(m.Pressure(bg, h)); r.Measured || r.Tokens != reported+7 || r.Fraction(0) != 0 {
		t.Fatalf("pressure %+v", r)
	}
	if u := must(m.Usage(bg, h)); u.Attempts != 1 || u.Billed.Input != reported {
		t.Fatalf("usage %+v", u)
	}
	if b := must(m.Breakdown(bg, h)); len(b.System) != 1 || b.SystemTotal == 0 {
		t.Fatalf("breakdown %+v", b)
	}

	// With calibration off the factor is 1.
	off := must(meter.New(views.New(), meter.Config{}))
	if g := must(off.Measure(bg, h, hdr)); g.Calibrated || g.Factor != 1 {
		t.Fatalf("calibration off: %+v", g)
	}

	// A compaction rewrites the envelope: the factor taken on the intact one
	// still scales the new estimate, and pressure measures the surface rather
	// than projecting from the sample.
	sum := must(vocab.RestoreMessage("c", vocab.RoleUser, nil, vocab.TextBlock("summary")))
	must(h.Append(bg, must(event.UserMessage.Draft(event.Replace(asOf, at), event.UserMessagePayload{Message: sum}, asOf, at))))
	after := must(m.Measure(bg, h, hdr))
	if !after.Calibrated || after.Factor != 2 || after.Estimate >= got.Estimate || after.Tokens != meter.Calibrated(after.Estimate, 2) {
		t.Fatalf("after a compaction: %+v", after)
	}
	if r := must(m.Pressure(bg, h)); !r.Measured || r.Tokens != after.Tokens {
		t.Fatalf("pressure after a compaction %+v, measured %+v", r, after)
	}
	if g := must(off.Measure(bg, h, hdr)); g.Calibrated || g.Factor != 1 {
		t.Fatalf("calibration off after a compaction: %+v", g)
	}

	// A header change keeps the factor too; a sample taken on the rewritten
	// envelope is not one of its request, so it does not recalibrate.
	tool := vocab.ToolSchema{Name: "write", Description: "writes a file", Parameters: json.RawMessage(`{"type":"object"}`)}
	must(h.Append(bg, must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "r/a", Tools: append(slices.Clone(hdr.Tools), tool)}))))
	hdr2, _ := h.RequestHeader()
	if g := must(m.Measure(bg, h, hdr2)); !g.Calibrated || g.Factor != 2 || g.Tokens != meter.Calibrated(g.Estimate, 2) {
		t.Fatalf("after a header change: %+v", g)
	}
	answer(t, h, asOf, &vocab.Usage{Input: 1, Output: 1})
	if g := must(m.Measure(bg, h, hdr2)); !g.Calibrated || g.Factor != 2 {
		t.Fatalf("a stale sample recalibrated: %+v", g)
	}

	// A fresh request after the compaction, priced by the runtime when sent,
	// recalibrates to its own ratio; a second compaction then keeps that ratio,
	// not the first one, and so does a fold resumed from a checkpoint.
	fresh := answerPriced(t, h, h.Len()-1, &vocab.Usage{Input: 3 * 40, Output: 1}, 40)
	sum2 := must(vocab.RestoreMessage("c2", vocab.RoleUser, nil, vocab.TextBlock("summary again")))
	must(h.Append(bg, must(event.UserMessage.Draft(event.Replace(fresh, fresh), event.UserMessagePayload{Message: sum2}, fresh))))
	if g := must(m.Measure(bg, h, hdr2)); !g.Calibrated || g.Factor != 3 || g.Tokens != meter.Calibrated(g.Estimate, 3) {
		t.Fatalf("after a second compaction: %+v", g)
	}
	evs := must(h.Read(bg, 0, 0))
	d := meter.PressureView()
	whole := views.Refold(d, session.Header{}, evs)
	for cut := range evs {
		if from := views.FoldFrom(d, views.Refold(d, session.Header{}, evs[:cut]), evs[cut:]); !reflect.DeepEqual(from, whole) {
			t.Fatalf("resumed at %d: %+v, from zero %+v", cut, from, whole)
		}
	}
	if whole.Factor != 3 {
		t.Fatalf("the fold's factor %v", whole.Factor)
	}
}

// DH-T-LLM-15: the breakdown's state is bounded. Rewriting every message one by
// one keeps at most MaxAnchors anchors, with or without system prompts; past
// that the fold says it is approximate and the Meter reads the system prompts
// and the pressure from the session's surface, which then still match it.
func TestBreakdownStaysBoundedUnderRewrites(t *testing.T) {
	short := strings.Repeat("a", 40)
	rewrites := func(n int, sys bool) []event.Event {
		var evs []event.Event
		if sys {
			evs = append(evs, system(t, 0, event.Append(), short))
		} else {
			evs = append(evs, user(t, 0))
		}
		for i := 1; i <= n; i++ {
			evs = append(evs, user(t, i))
		}
		if sys {
			evs = append(evs, system(t, n+1, event.Append(), short))
		} else {
			evs = append(evs, user(t, n+1))
		}
		for i := 1; i <= n; i++ {
			evs = append(evs, summary(t, n+1+i, event.Replace(event.Seq(i), event.Seq(i))))
		}
		return evs
	}
	for _, sys := range []bool{false, true} {
		few := views.Refold(meter.BreakdownView(), session.Header{}, rewrites(meter.MaxAnchors, sys))
		many := views.Refold(meter.BreakdownView(), session.Header{}, rewrites(200, sys))
		if few.Approximate || len(few.Anchors) != meter.MaxAnchors || !reflect.DeepEqual(many, meter.Breakdown{Approximate: true}) {
			t.Fatalf("system prompts %v: %+v (approximate %v), then %+v", sys, few.Anchors, few.Approximate, many)
		}
		if got, want := priced(few), surfaceSystems(t, rewrites(meter.MaxAnchors, sys)); !slices.Equal(got, want) {
			t.Fatalf("system prompts %v: priced %v, the surface retains %v", sys, got, want)
		}
		pf := views.Refold(meter.PressureView(), session.Header{}, rewrites(50, sys))
		pm := views.Refold(meter.PressureView(), session.Header{}, rewrites(400, sys))
		// No entries at all; only the counters' digits grow.
		if a, b := len(must(json.Marshal(pf))), len(must(json.Marshal(pm))); !reflect.DeepEqual(pm.System, meter.Breakdown{Approximate: true}) || b > a+12 {
			t.Fatalf("system prompts %v: pressure grew from %d to %d bytes (%+v) over 350 rewrites", sys, a, b, pm.System)
		}
	}

	// A range whose ends are rewritten nodes past the anchor bound: rewrite
	// user b and the 17 users after system b+1, append a tail, then replace
	// from b's rewrite to the tail with a system prompt citing every node it
	// covers, which removes system b+1. The fold must not keep b+1's price by
	// a guessed order, cycle after cycle.
	cycle := func(evs []event.Event, b int) []event.Event {
		evs = append(evs, user(t, b), system(t, b+1, event.Append(), short))
		for i := 2; i <= 18; i++ {
			evs = append(evs, user(t, b+i))
		}
		evs = append(evs, summary(t, b+19, event.Replace(event.Seq(b), event.Seq(b))))
		for i := 2; i <= 18; i++ {
			evs = append(evs, summary(t, b+18+i, event.Replace(event.Seq(b+i), event.Seq(b+i))))
		}
		evs = append(evs, user(t, b+37))
		cited := []event.Seq{event.Seq(b + 19), event.Seq(b + 1)}
		for i := 20; i <= 37; i++ {
			cited = append(cited, event.Seq(b+i))
		}
		return append(evs, system(t, b+38, event.Replace(event.Seq(b+19), event.Seq(b+37)), short, cited...))
	}
	evs := []event.Event{system(t, 0, event.Append(), short)}
	var one, five []byte
	for c := range 5 {
		evs = cycle(evs, 1+39*c)
		if c == 0 {
			if want := []event.Seq{0, 39}; !slices.Equal(surfaceSystems(t, evs), want) {
				t.Fatalf("the surface retains %v, want %v", surfaceSystems(t, evs), want)
			}
			one = must(json.Marshal(views.Refold(meter.PressureView(), session.Header{}, evs)))
		}
	}
	five = must(json.Marshal(views.Refold(meter.PressureView(), session.Header{}, evs)))
	if b := views.Refold(meter.BreakdownView(), session.Header{}, evs); !reflect.DeepEqual(b, meter.Breakdown{Approximate: true}) {
		t.Fatalf("evicted-end ranges kept %+v (the surface retains %v)", b, surfaceSystems(t, evs))
	}
	if len(five) > len(one)+12 {
		t.Fatalf("pressure grew from %d to %d bytes over four more cycles: %s", len(one), len(five), five)
	}

	// On a session, an approximate fold defers to the surface.
	h, hdr, _ := newSession(t)
	m := must(meter.New(views.New(), meter.DefaultConfig()))
	var us []event.Seq
	for i := range meter.MaxAnchors + 2 {
		u := must(vocab.RestoreMessage(fmt.Sprintf("m%d", i), vocab.RoleUser, nil, vocab.TextBlock("more")))
		us = append(us, must(h.Append(bg, must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: u})))).Seq)
	}
	for _, q := range us {
		c := must(vocab.RestoreMessage("c", vocab.RoleUser, nil, vocab.TextBlock("summary")))
		must(h.Append(bg, must(event.UserMessage.Draft(event.Replace(q, q), event.UserMessagePayload{Message: c}, q))))
	}
	b := must(m.Breakdown(bg, h))
	if !b.Approximate || len(b.System) != 1 || b.System[0].Seq != h.History().At(0).Seq() || b.SystemTotal != b.System[0].Tokens {
		t.Fatalf("breakdown from the surface %+v", b)
	}
	r := must(m.Pressure(bg, h))
	if got := must(m.Measure(bg, h, hdr)); !r.Measured || r.Tokens != got.Tokens {
		t.Fatalf("pressure %+v, measured %+v", r, got)
	}

	// The evicted-end cycle on the session: the Meter prices exactly the
	// system prompts the session's surface retains.
	put := func(d event.Draft) event.Seq { return must(h.Append(bg, d)).Seq }
	text := func(role vocab.Role, s string) vocab.Message {
		return must(vocab.RestoreMessage(s, role, nil, vocab.TextBlock(s)))
	}
	for range 3 {
		first := put(must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: text(vocab.RoleUser, "first")})))
		sys := put(must(event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: text(vocab.RoleSystem, short)})))
		var rest []event.Seq
		for range 17 {
			rest = append(rest, put(must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: text(vocab.RoleUser, "more")}))))
		}
		start := put(must(event.UserMessage.Draft(event.Replace(first, first), event.UserMessagePayload{Message: text(vocab.RoleUser, "summary")}, first)))
		cited := []event.Seq{start, sys}
		for _, q := range rest {
			cited = append(cited, put(must(event.UserMessage.Draft(event.Replace(q, q), event.UserMessagePayload{Message: text(vocab.RoleUser, "summary")}, q))))
		}
		tail := put(must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: text(vocab.RoleUser, "tail")})))
		cited = append(cited, tail)
		put(must(event.SystemMessage.Draft(event.Replace(start, tail), event.MessagePayload{Message: text(vocab.RoleSystem, short)}, cited...)))
	}
	var want []event.Seq
	for i := range h.History().Len() {
		if n := h.History().At(i); n.Type() == event.SystemMessage.Name() {
			want = append(want, n.Seq())
		}
	}
	if b := must(m.Breakdown(bg, h)); !b.Approximate || !slices.Equal(priced(b), want) {
		t.Fatalf("breakdown priced %v, the session's surface retains %v", priced(b), want)
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
