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
// What this is not yet: the budget is unmeasured (agent-loop.md §9.1 leaves
// it open), so no reviewed constant is asserted; the scripted model stands in
// for the replay provider (DH-D-UTIL-7, impl.md step 16.7, held open by H-10)
// and memstore for the built binary's store, and the ladder stands in for the
// measured 1,650-session distribution, which is not in the repository. The
// benchmark reports wall time per continuation and peak heap; when the
// replay provider and the corpus land, the same loop gains the budget check.

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
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// sessionSizes is the heavy-tailed ladder: the turns of a median, a long, a
// very long and the longest session. Each turn is tool-heavy: one step with
// two tool calls, then the answer.
var sessionSizes = []struct {
	name  string
	turns int
}{
	{"p50", 8}, {"p90", 64}, {"p99", 512}, {"max", 1024},
}

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

func BenchmarkContinuation(b *testing.B) {
	for _, size := range sessionSizes {
		b.Run(size.name, func(b *testing.B) {
			root, svc := setup(b)
			must(svc.Register(root, tool("echo")))
			st := memstore.New()
			id := "bench-" + size.name

			// The long session: size.turns tool-heavy turns, then closed.
			c := resume(b, root, svc, st, id, true)
			c.tr.Script(tooled(size.turns)...)
			for i := range size.turns {
				must(c.in.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: fmt.Sprintf("turn %d", i)}))
			}
			c.idle(b, 10*time.Minute)
			c.close(b)

			var peak uint64
			var ms runtime.MemStats
			b.ResetTimer()
			b.StopTimer()
			for range b.N {
				c := resume(b, root, svc, st, id, false)
				c.tr.Script(tooled(1)...)
				b.StartTimer()
				must(c.in.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "continue"}))
				select {
				case <-c.ch:
				case <-time.After(time.Minute):
					b.Fatal("no model request left the driver")
				}
				b.StopTimer()
				runtime.ReadMemStats(&ms)
				peak = max(peak, ms.HeapAlloc)
				c.idle(b, time.Minute)
				c.close(b)
			}
			b.ReportMetric(float64(peak), "peak-heap-B")
			b.ReportMetric(float64(size.turns), "turns")
		})
	}
}
