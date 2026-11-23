//go:build bench

// DH-T-AGENT-28 (agent-loop.md §9.1, DH-ADR-TOOLCHAIN-14), measurement half:
// continuing a session, from a waking send on a resumed long, tool-heavy
// session to the first model request leaving the driver, over a ladder of
// session sizes with a heavy tail. Run it with
//
//	go test -tags bench -run '^$' -bench Continuation ./internal/agent/driver/
//
// and again under GOMEMLIMIT (4GiB, 8GiB) for the memory-limited rerun.
//
// Acceptance is staged (an owner decision for Stage 11.2): until the replay
// provider (DH-D-UTIL-7, impl.md step 16.7, held open by H-10) lands, the
// scripted model and memstore stand in for it. The ladder is the heavy-tailed
// corpus's (testkit/corpus.Ladder: the p50, p90, p99 and longest of the 1,650
// measured sessions); each rung's session is made of tool-heavy driver turns,
// as many as reach the rung's measured events (and at least its turns; the
// unbudgeted max rung is capped, see maxTemplateTurns). The
// 99th-percentile (nearest-rank) latency of the continuations timed at each
// rung up to the p99 rung must stay inside provisionalBudget, in the plain run
// and in the GOMEMLIMIT reruns; the max rung is measured, not budgeted. The final budget
// stays unpinned until the provider lands, when this check is re-run against
// it. Each timed continuation resumes a fresh store seeded
// with the same template session (records only: the template's checkpoints
// are not copied), whose size is checked outside the timer. The benchmark
// reports wall time per continuation, the p50 and p99 latency, peak heap,
// turns and records.

package driver_test

import (
	"context"
	"fmt"
	"iter"
	"runtime"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// recordsPerTurn is the records one tool-heavy driver turn logs (one step with
// two tool calls, then the answer); the template check below fails if it drifts.
// Since the one-batch admission (plan 17.1) an unchanged prompt adds no
// system/message and no request/header, so the session's system prompt and
// header are logged once, in its first turn (two records more in all).
const recordsPerTurn = 17

// sessionSize is one rung of the ladder in driver turns.
type sessionSize struct {
	name     string
	turns    int
	events   int
	budgeted bool
}

// maxTemplateTurns caps a template's driver turns. The longest measured session
// (84,467 events) would take 4,446 driver turns, and the template is built
// through the driver, whose scripted turns grow with the history: on the first
// run (Windows, i9-13900K) only 570 of them finished inside the template's
// 10-minute bound. The unbudgeted max rung is therefore capped at the previous
// synthetic ladder's longest session, 1,024 turns (19,456 records).
const maxTemplateTurns = 1024

// sessionSizes is the heavy-tailed ladder (corpus.Ladder) in driver turns: as
// many as reach the rung's measured events, and at least its measured turns,
// capped at maxTemplateTurns. Every rung but the longest is budgeted.
func sessionSizes() []sessionSize {
	var out []sessionSize
	for _, r := range corpus.Ladder() {
		turns := min(maxTemplateTurns, max(r.Turns, (r.Events+recordsPerTurn-1)/recordsPerTurn))
		out = append(out, sessionSize{name: r.Name, turns: turns, events: min(r.Events, turns*recordsPerTurn),
			budgeted: r.Name != "max"})
	}
	return out
}

// provisionalBudget is the staged DH-T-AGENT-28 p99 latency budget for the
// continuations of a session up to the p99 rung; not the final pin (see the
// file comment).
const provisionalBudget = 100 * time.Millisecond

// firstStream signals each request the driver hands to the model.
type firstStream struct {
	driver.Models
	ch chan struct{}
}

func (f *firstStream) Prepare(ctx context.Context, rt vocab.RouteID, model string, c vocab.Controls) (llm.Prepared, error) {
	p, err := f.Models.Prepare(ctx, rt, model, c)
	if err != nil {
		return nil, err
	}
	return signalled{Prepared: p, ch: f.ch}, nil
}

type signalled struct {
	llm.Prepared
	ch chan struct{}
}

func (p signalled) Stream(ctx context.Context, req session.Request, sink llm.AttemptSink) iter.Seq[vocab.Chunk] {
	select {
	case p.ch <- struct{}{}:
	default:
	}
	return p.Prepared.Stream(ctx, req, sink)
}

// continuation is one agent over a session in st: open (a resume when the
// session exists), its driver, and the scripted model.
type continuation struct {
	h   session.Handle
	tr  *fakemodel.Transport
	in  *agent.Inbox
	drv *driver.Driver
	ch  chan struct{}
}

func resume(b *testing.B, root *cordis.Context, svc tools.Service, st *memstore.Store, id string, create bool) *continuation {
	b.Helper()
	ss := session.NewService(root, st, session.Config{})
	var h session.Handle
	if create {
		h = must(ss.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}}))
	} else {
		var err error
		if h, _, err = ss.Open(bg, root, session.ID(id), session.OpenOptions{}); err != nil {
			b.Fatal(err)
		}
	}
	c := &continuation{h: h, ch: make(chan struct{}, 1), tr: fakemodel.NewTransport(b)}
	rt := llm.New()
	fakemodel.Register(b, rt, route, c.tr)
	c.in = must(agent.OpenInbox(bg, h))
	mod := must(agent.OpenModel(bg, h, agent.ModelOptions{Initial: agent.ModelSelection{Route: route}}))
	c.drv = must(driver.Start(driver.Options{
		Context: root, Agent: stubAgent{ref: agent.Ref{Session: session.ID(id), Incarnation: 1}},
		Session: h, Inbox: c.in, Model: mod, Models: &firstStream{Models: rt, ch: c.ch}, Tools: svc,
		Prompt: newTestPrompt(defaultPrompt),
	}))
	return c
}

func (c *continuation) idle(b *testing.B, within time.Duration) {
	b.Helper()
	ctx, cancel := context.WithTimeout(bg, within)
	defer cancel()
	if err := c.drv.WaitIdle(ctx); err != nil {
		b.Fatal(err)
	}
}

func (c *continuation) close(b *testing.B) {
	b.Helper()
	ctx, cancel := context.WithTimeout(bg, time.Minute)
	defer cancel()
	if err := c.drv.Close(ctx); err != nil {
		b.Fatal(err)
	}
	if err := c.h.Close(ctx); err != nil {
		b.Fatal(err)
	}
}

func tooled(n int) []fakemodel.Turn {
	out := make([]fakemodel.Turn, 0, 2*n)
	for i := range n {
		out = append(out, fakemodel.Calls(echo(fmt.Sprintf("a%d", i)), echo(fmt.Sprintf("b%d", i))), fakemodel.Reply("ok"))
	}
	return out
}

// BenchmarkContinuation times each rung's continuations (see the file comment).
//
//dsh:test DH-T-AGENT-28
func BenchmarkContinuation(b *testing.B) {
	for _, size := range sessionSizes() {
		b.Run(size.name, func(b *testing.B) {
			root, svc := setup(b)
			must(svc.Register(root, tool("echo")))
			st := memstore.New()
			id := "bench-" + size.name

			// The long session: size.turns tool-heavy turns, then closed. It
			// is the fixed template every timed continuation starts from.
			c := resume(b, root, svc, st, id, true)
			c.tr.Script(tooled(size.turns)...)
			for i := range size.turns {
				must(c.in.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: fmt.Sprintf("turn %d", i)}))
			}
			c.idle(b, 10*time.Minute)
			c.close(b)
			head, recs := snapshot(b, st, id)
			if n := turnsIn(recs); n != size.turns || len(recs) < size.events {
				b.Fatalf("the template holds %d turns and %d records, want %d and at least %d", n, len(recs), size.turns, size.events)
			}

			var (
				peak  uint64
				times []time.Duration
			)
			var ms runtime.MemStats
			b.ResetTimer()
			b.StopTimer()
			for range b.N {
				// Each continuation starts from an equal copy of the template,
				// checked before the timer runs, so b.N never lengthens it.
				fresh := memstore.New()
				if err := fresh.Seed(bg, head, recs); err != nil {
					b.Fatal(err)
				}
				if _, got := snapshot(b, fresh, id); len(got) != len(recs) || turnsIn(got) != size.turns {
					b.Fatalf("the copy holds %d records and %d turns, want %d and %d", len(got), turnsIn(got), len(recs), size.turns)
				}
				c := resume(b, root, svc, fresh, id, false)
				c.tr.Script(tooled(1)...)
				runtime.GC()
				b.StartTimer()
				began := time.Now()
				must(c.in.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "continue"}))
				select {
				case <-c.ch:
				case <-time.After(time.Minute):
					b.Fatal("no model request left the driver")
				}
				b.StopTimer()
				times = append(times, time.Since(began))
				runtime.ReadMemStats(&ms)
				peak = max(peak, ms.HeapAlloc)
				c.idle(b, time.Minute)
				c.close(b)
			}
			b.ReportMetric(float64(peak), "peak-heap-B")
			b.ReportMetric(float64(size.turns), "turns")
			b.ReportMetric(float64(len(recs)), "records")
			p99, over := overBudget(times, provisionalBudget)
			b.ReportMetric(float64(percentile(times, 50).Nanoseconds()), "p50-ns")
			b.ReportMetric(float64(p99.Nanoseconds()), "p99-ns")
			if size.budgeted && over {
				b.Errorf("p99 continuation latency of a %d-turn session is %v over %d samples, over the provisional budget %v", size.turns, p99, len(times), provisionalBudget)
			}
		})
	}
}

// snapshot is the header and every record of session id in st.
func snapshot(b *testing.B, st *memstore.Store, id string) (store.Header, []store.Record) {
	b.Helper()
	r, err := st.OpenRead(bg, id)
	if err != nil {
		b.Fatal(err)
	}
	defer r.Close(bg)
	recs, err := r.Read(bg, 0, 0, store.ReadOptions{WithAux: true})
	if err != nil {
		b.Fatal(err)
	}
	return r.Header(), recs
}

func turnsIn(recs []store.Record) int {
	n := 0
	for _, r := range recs {
		if r.Type == "turn/end" {
			n++
		}
	}
	return n
}
