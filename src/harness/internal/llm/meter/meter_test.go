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
	// Pressure carries a running total of what was appended since the sample:
	// 20 and 200 messages later its state differs by one digit only.
	few := views.Refold(meter.PressureView(), session.Header{}, more[:26])
	lp := views.Refold(meter.PressureView(), session.Header{}, more)
	if a, b := len(must(json.Marshal(few))), len(must(json.Marshal(lp))); b > a+1 {
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
		last  event.Event
		total int
	}{
		{"an empty system replacement", system(t, 4, event.Replace(2, 2), "  \n"), frame + 10},
		{"a compaction over a later system prompt", summary(t, 4, event.Replace(1, 3)), frame + 10},
		{"a system replacement over a range", system(t, 4, event.Replace(1, 3), short), 2 * (frame + 10)},
		{"a compaction that leaves the system prompts", summary(t, 4, event.Replace(3, 3)), 2 * (frame + 10)},
	}
	for _, c := range cases {
		evs := append(append([]event.Event(nil), base...), c.last)
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
	m := must(vocab.RestoreMessage("a", vocab.RoleAssistant, nil, vocab.TextBlock("the answer")))
	r := must(h.Append(bg, must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
		Message: m, RequestAsOf: &asOf, RequestDigest: "sha256:ab", Usage: u}))))
	return r.Seq
}

// DH-T-LLM-15: the tokenMeter measures the session's current surface, read from
// the session, under a header; the calibration factor is the last sample's
// reported prompt over the heuristic's price of that same request (the nodes up
// to its requestAsOf under the header then in force), and it is dropped, not
// carried, once a replacement rewrites that envelope.
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

	// A compaction rewrites the envelope: the factor is dropped and pressure
	// measures the surface rather than projecting from the sample.
	sum := must(vocab.RestoreMessage("c", vocab.RoleUser, nil, vocab.TextBlock("summary")))
	must(h.Append(bg, must(event.UserMessage.Draft(event.Replace(asOf, at), event.UserMessagePayload{Message: sum}, asOf, at))))
	after := must(m.Measure(bg, h, hdr))
	if after.Calibrated || after.Factor != 1 || after.Estimate >= got.Estimate {
		t.Fatalf("after a compaction: %+v", after)
	}
	if r := must(m.Pressure(bg, h)); !r.Measured || r.Tokens != after.Tokens {
		t.Fatalf("pressure after a compaction %+v, measured %+v", r, after)
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
