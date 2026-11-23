package views_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus"
)

// propertySeed is the seed of the property test's logs. It is fixed, so every
// run checks the same 500 logs, and logged, so a failure names the log to
// regenerate: log i is generated from rand.NewPCG(propertySeed, i).
const propertySeed = 0x1529d5a

// propertyLogs is how many seeded logs the property test checks.
const propertyLogs = 500

// logGen builds one seeded, valid log: turns of a prompt and steps of failed
// attempts, tool calls with their results (some errors) and answers (some
// blank), with rewrites of tool results, a compaction of the history's head,
// request-context records, a newer ignorable prompt now and then, and a last
// turn that may be left unsettled (mid-step or after its steps).
type logGen struct {
	r    *rand.Rand
	recs []event.Record
	now  int64
	// hist is the current model history's seqs, as the surface fold keeps it;
	// results are the tool results among them, which a rewrite may replace.
	hist, results []event.Seq
	calls         map[event.Seq]string // a tool result's call id
}

var reasons = []string{event.ReasonCompleted, event.ReasonCompleted, event.ReasonMaxTokens, event.ReasonError,
	event.ReasonAborted, event.ReasonInterrupted, event.ReasonStop, event.ReasonRefusal}

func (g *logGen) add(d event.Draft, newer bool) event.Seq {
	seq := event.Seq(len(g.recs))
	g.now += int64(g.r.IntN(3_000))
	rec := must(d.Stamp(seq, g.now))
	if newer {
		info, _ := event.Default().Lookup(rec.Type)
		rec.Version, rec.Ignorable = info.Version+1, true
	}
	g.recs = append(g.recs, rec)
	return seq
}

// node adds a surface draft that becomes a history node.
func (g *logGen) node(d event.Draft) event.Seq {
	seq := g.add(d, false)
	g.hist = append(g.hist, seq)
	return seq
}

func (g *logGen) timing() *event.Timing {
	if g.r.IntN(4) == 0 {
		return nil
	}
	start := g.now
	first := start + int64(g.r.IntN(500))
	return &event.Timing{Start: start, FirstToken: first, End: first + int64(g.r.IntN(2_000))}
}

func (g *logGen) usage() *vocab.Usage {
	if g.r.IntN(5) == 0 {
		return nil
	}
	in, out := g.r.IntN(2_000), g.r.IntN(300)
	return &vocab.Usage{Input: in, CacheRead: g.r.IntN(100), Output: out, Total: in + out}
}

func (g *logGen) maybeContext() {
	if g.r.IntN(6) == 0 {
		g.add(ctxDraft(fmt.Sprintf("v%d", len(g.recs))), false)
	}
}

// rewrite replaces a current tool result with a shorter one.
func (g *logGen) rewrite() {
	if len(g.results) == 0 {
		return
	}
	i := g.r.IntN(len(g.results))
	old := g.results[i]
	call := g.calls[old]
	seq := g.add(must(event.ToolResult.Draft(event.Replace(old, old), event.ToolResultPayload{CallID: call,
		Message: bmsg(fmt.Sprintf("rx%d", len(g.recs)), vocab.RoleTool, vocab.ToolResultBlock(call, false, vocab.TextPart("[elided]")))}, old)), false)
	at := indexOf(g.hist, old)
	g.hist[at] = seq
	g.results[i] = seq
	g.calls[seq] = call
}

// compact replaces the history's head through a tool result or a prompt with
// one summary prompt, so no tool use loses its result.
func (g *logGen) compact() {
	var ends []int
	for i, seq := range g.hist {
		if typ := g.recs[seq].Type; seq >= g.hist[0] && (typ == event.ToolResult.Name() || typ == event.UserMessage.Name()) {
			ends = append(ends, i)
		}
	}
	if len(ends) == 0 {
		return
	}
	end := ends[g.r.IntN(len(ends))]
	from, to := g.hist[0], g.hist[end]
	seq := g.add(must(event.UserMessage.Draft(event.Replace(from, to), event.UserMessagePayload{
		Message: bmsg(fmt.Sprintf("sum%d", len(g.recs)), vocab.RoleUser, vocab.TextBlock("summary of the conversation so far"))},
		slices.Clone(g.hist[:end+1])...)), false)
	gone := map[event.Seq]bool{}
	for _, s := range g.hist[:end+1] {
		gone[s] = true
	}
	g.hist = append([]event.Seq{seq}, g.hist[end+1:]...)
	var kept []event.Seq
	for _, s := range g.results {
		if !gone[s] {
			kept = append(kept, s)
		}
	}
	g.results = kept
}

// turn appends turn n; last turns may stop unsettled.
func (g *logGen) turn(n int, last bool) {
	agent := ""
	if g.r.IntN(4) == 0 {
		agent = "helper"
	}
	g.add(must(event.TurnStartType.Draft(event.TurnStart{Turn: n, Agent: agent})), false)
	g.maybeContext()
	if g.r.IntN(10) != 0 {
		prompt := must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{
			Message: bmsg(fmt.Sprintf("u%d", n), vocab.RoleUser, vocab.TextBlock(fmt.Sprintf("  prompt %d:   %s  ", n, words(g.r))))}))
		if g.r.IntN(15) == 0 {
			g.add(prompt, true) // a newer ignorable prompt: log-only to the core
		} else {
			g.node(prompt)
		}
	}
	steps := g.r.IntN(4)
	unsettled := last && g.r.IntN(2) == 0
	for s := 1; s <= steps; s++ {
		g.add(must(event.StepStartType.Draft(event.StepStart{Turn: n, Step: s})), false)
		if g.r.IntN(4) == 0 {
			g.add(must(event.AssistantAttempt.Draft(event.AssistantAttemptPayload{Usage: g.usage(), Timing: g.timing(),
				Failure: &vocab.Failure{Code: vocab.CodeServer, Class: vocab.Transient, Message: "overloaded"}})), false)
		}
		if unsettled && s == steps && g.r.IntN(2) == 0 {
			return // stopped mid-step
		}
		id := fmt.Sprintf("a%d-%d", n, s)
		if s < steps || g.r.IntN(3) == 0 {
			call := fmt.Sprintf("c%d-%d", n, s)
			g.node(must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
				Message: bmsg(id, vocab.RoleAssistant, vocab.ToolUseBlock(call, "ls", json.RawMessage(`{"q":1}`))),
				Usage:   g.usage(), Timing: g.timing()})))
			g.add(must(event.ToolCall.Draft(event.ToolCallPayload{CallID: call, Name: "ls", Arguments: `{"q":1}`})), false)
			isErr := g.r.IntN(4) == 0
			res := g.node(must(event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: call, IsError: isErr,
				Message: bmsg(fmt.Sprintf("r%d-%d", n, s), vocab.RoleTool, vocab.ToolResultBlock(call, isErr, vocab.TextPart(words(g.r))))})))
			g.results = append(g.results, res)
			g.calls[res] = call
		} else {
			text := "answer " + words(g.r)
			if g.r.IntN(5) == 0 {
				text = "   " // blank: no history node
			}
			d := must(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
				Message: bmsg(id, vocab.RoleAssistant, vocab.TextBlock(text)), Usage: g.usage(), Timing: g.timing()}))
			if text == "   " {
				g.add(d, false)
			} else {
				g.node(d)
			}
		}
		g.add(must(event.StepEndType.Draft(event.StepEnd{Turn: n, Step: s})), false)
		g.maybeContext()
		if g.r.IntN(5) == 0 {
			g.rewrite()
		}
	}
	if g.r.IntN(8) == 0 {
		g.compact()
	}
	if !unsettled {
		g.add(must(event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: event.Reason{Kind: reasons[g.r.IntN(len(reasons))]}})), false)
	}
}

func indexOf(s []event.Seq, v event.Seq) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	panic(fmt.Sprintf("seq %d is not in %v", v, s))
}

var vocabulary = []string{"list", "the", "files", "explain", "each", "of", "them", "read", "main.go", "tests", "fix", "build"}

func words(r *rand.Rand) string {
	n := 1 + r.IntN(30)
	out := make([]byte, 0, n*6)
	for i := range n {
		if i > 0 {
			out = append(out, ' ')
		}
		out = append(out, vocabulary[r.IntN(len(vocabulary))]...)
	}
	return string(out)
}

// seededLog is log i of the property test: one in five a corpus log of a small
// random shape (testkit/corpus.Log), the others a logGen log.
func seededLog(i int) []event.Record {
	r := rand.New(rand.NewPCG(propertySeed, uint64(i)))
	if i%5 == 0 {
		sh := corpus.Shape{Anchor: -1, Turns: 1 + r.IntN(4), Events: r.IntN(60)}
		return must(corpus.Log(sh, 1_700_000_000_000))
	}
	g := &logGen{r: r, now: 1_700_000_000_000, calls: map[event.Seq]string{}}
	turns := r.IntN(6) // 0: an empty log
	for n := 1; n <= turns; n++ {
		g.turn(n, n == turns)
	}
	return g.recs
}

// rowSession is a session the registry has not followed (no AsOf), holding a
// log and checkpoint rows: only the methods the registry calls are defined.
type rowSession struct {
	session.ReadSession
	h   session.Header
	evs []event.Event
	cps []store.Checkpoint
}

func (s *rowSession) ID() session.ID         { return session.ID(s.h.ID) }
func (s *rowSession) Header() session.Header { return s.h }
func (s *rowSession) Len() session.Offset    { return session.Offset(len(s.evs)) }

func (s *rowSession) Read(_ context.Context, from session.Offset, limit int, _ ...session.ReadOption) ([]event.Event, error) {
	end := len(s.evs)
	if limit > 0 {
		end = min(end, int(from)+limit)
	}
	return s.evs[min(int(from), end):end], nil
}

func (s *rowSession) Checkpoints(context.Context) ([]store.Checkpoint, error) { return s.cps, nil }

// cutRow is d's checkpoint row at cut: its state over evs[:cut], encoded as the
// registry stores it, at seq cut-1; false at cut 0 (no row).
func cutRow[S any](d views.Definition[S], h session.Header, evs []event.Event, cut int) (store.Checkpoint, bool) {
	if cut == 0 {
		return store.Checkpoint{}, false
	}
	state := must(json.Marshal(views.Refold(d, h, evs[:cut])))
	return store.Checkpoint{Key: d.Key, StateVersion: d.Version, Seq: event.Seq(cut - 1), State: string(state)}, true
}

// resumeEqualsFold is the property for one view: d's row at cut, decoded and
// folded over evs[cut:], equals the full fold, as JSON. It returns the full
// fold's JSON.
func resumeEqualsFold[S any](t *testing.T, what string, d views.Definition[S], h session.Header, evs []event.Event, cut int) string {
	t.Helper()
	full := string(must(json.Marshal(views.Refold(d, h, evs))))
	from := d.Init(h)
	if cp, ok := cutRow(d, h, evs, cut); ok {
		var s S
		if err := json.Unmarshal([]byte(cp.State), &s); err != nil {
			t.Fatalf("%s: %s: decoding its row at cut %d: %v", what, d.Key, cut, err)
		}
		from = s
	}
	if got := string(must(json.Marshal(views.FoldFrom(d, from, evs[cut:])))); got != full {
		t.Fatalf("%s: %s: the row at cut %d plus the suffix differs from the full fold:\nresumed %s\nfull    %s", what, d.Key, cut, got, full)
	}
	return full
}

// registryEquals reads h's handle from snap and compares it, as JSON, with want.
func registryEquals[S any](t *testing.T, what string, h views.Handle[S], snap views.Snapshot, want string) {
	t.Helper()
	got := string(must(json.Marshal(must(h.Get(snap)))))
	if got != want {
		t.Fatalf("%s: %s: the registry's fold from its row differs from the full fold:\nregistry %s\nfull     %s", what, h.Key(), got, want)
	}
	if v, _ := snap.Value(h.Key()); v.State != views.Current {
		t.Fatalf("%s: %s: an Exact read is %v", what, h.Key(), v.State)
	}
}

// Property: a checkpoint plus the suffix equals the full fold (DH-D-VIEW-2). For
// each of 500 seeded logs and every built-in view, the view's row taken at a
// random cut (none at cut 0), decoded and folded over the rest of the log,
// equals the view folded over the whole log; and the registry's Exact read of a
// session holding those rows, which takes each view up from its row, equals it
// too. Every log is a valid one: the surface fold replays it.
//
//dsh:test DH-T-VIEW-1
func TestCheckpointPlusSuffixEqualsFullFold(t *testing.T) {
	t.Logf("seed %#x: log i is rand.NewPCG(%#x, i), %d logs", propertySeed, propertySeed, propertyLogs)
	defs := struct {
		sum   views.Definition[views.ListSummary]
		turns views.Definition[views.TurnIndex]
		stats views.Definition[views.Stats]
		idx   views.Definition[views.EventIndex]
	}{views.ListSummaryView(), views.TurnIndexView(), views.StatsView(), views.EventIndexView()}
	reg := views.New()
	b := must(views.RegisterBuiltins(reg))
	var events, rows, shadowed int
	for i := range propertyLogs {
		recs := seededLog(i)
		what := fmt.Sprintf("seed %#x log %d (%d events)", propertySeed, i, len(recs))
		if _, err := surface.Replay(nil, recs); err != nil {
			t.Fatalf("%s is not a valid log: %v", what, err)
		}
		evs := make([]event.Event, len(recs))
		for j, rec := range recs {
			evs[j] = event.Of(rec)
		}
		h := session.Header{ID: fmt.Sprintf("p-%03d", i)}
		r := rand.New(rand.NewPCG(propertySeed^0xc07, uint64(i)))
		cut := func() int { return r.IntN(len(evs) + 1) }
		cs := [4]int{cut(), cut(), cut(), cut()}
		sum := resumeEqualsFold(t, what, defs.sum, h, evs, cs[0])
		turns := resumeEqualsFold(t, what, defs.turns, h, evs, cs[1])
		stats := resumeEqualsFold(t, what, defs.stats, h, evs, cs[2])
		idx := resumeEqualsFold(t, what, defs.idx, h, evs, cs[3])

		s := &rowSession{h: h, evs: evs}
		for _, row := range []func() (store.Checkpoint, bool){
			func() (store.Checkpoint, bool) { return cutRow(defs.sum, h, evs, cs[0]) },
			func() (store.Checkpoint, bool) { return cutRow(defs.turns, h, evs, cs[1]) },
			func() (store.Checkpoint, bool) { return cutRow(defs.stats, h, evs, cs[2]) },
			func() (store.Checkpoint, bool) { return cutRow(defs.idx, h, evs, cs[3]) },
		} {
			if cp, ok := row(); ok {
				s.cps = append(s.cps, cp)
			}
		}
		snap, err := reg.Snapshot(bg, s, nil, views.Exact)
		if err != nil {
			t.Fatalf("%s: Exact snapshot from rows at %v: %v", what, cs, err)
		}
		if snap.AsOf != event.Seq(len(evs)) {
			t.Fatalf("%s: Exact snapshot at %d, want %d", what, snap.AsOf, len(evs))
		}
		registryEquals(t, what, b.ListSummary, snap, sum)
		registryEquals(t, what, b.TurnIndex, snap, turns)
		registryEquals(t, what, b.Stats, snap, stats)
		registryEquals(t, what, b.EventIndex, snap, idx)

		events += len(evs)
		rows += len(s.cps)
		shadowed += strings.Count(idx, `"replaced":true`)
	}
	// The logs exercise what the property is about: long enough, with rows taken
	// mid-log and replacements that rewrite earlier entries.
	if events < 10*propertyLogs || rows < 3*propertyLogs || shadowed < propertyLogs/4 {
		t.Fatalf("the logs are too plain: %d events, %d rows, %d replaced entries", events, rows, shadowed)
	}
	t.Logf("%d events, %d rows, %d replaced entries", events, rows, shadowed)
}
