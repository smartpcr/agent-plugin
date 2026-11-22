package core

import (
	"context"
	"errors"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// DefaultMaxParallel is the cap on overlapping bodies of one batch when
// Config.MaxParallel is zero (areas/tools.md §6.2, maxParallel).
const DefaultMaxParallel = 10

// The batch scheduler (DH-ADR-TOOLS-2, DH-ADR-AGENT-11). One coordinator, the
// caller's goroutine, admits the calls in the model's order and makes every
// append of the batch, so a log never sees two appends of one batch at once.
// Before the stages of a call it classifies the call and waits, as a barrier,
// until the call may overlap every running body: an exclusive call waits for
// all of them, a call with resource keys for those it conflicts with, and every
// call for a free slot under maxParallel. Calls therefore start in the model's
// order. Bodies run in their own goroutines; their results are settled (shaped
// and appended as tool/result) strictly in the model's order, so a body that
// finishes early waits for its predecessors to commit.

// slot is one call of the batch from its admission to its commit.
type slot struct {
	a    prepared
	done bool // the body returned (or there was none), so a.e is final
}

func (r *registry) schedule(ctx context.Context, b tools.Batch, ag agent.Ref, ls listeners) (tools.BatchResult, error) {
	limit := r.cfg.MaxParallel
	// Tools sees the step's declarations without the Lookup that resolve
	// performs, so classifying a call has no effect on the batch.
	decls := map[string]*tools.Declaration{}
	for _, d := range b.View.Tools() {
		decls[d.Name()] = d
	}
	out := tools.BatchResult{Results: make([]tools.Result, 0, len(b.Calls))}
	slots := make([]*slot, len(b.Calls))
	running := map[int]tools.Overlap{}
	finished := make(chan int, len(b.Calls))
	next := 0
	var failed error

	// commit settles, in the model's order, every call whose outcome is final
	// and whose predecessors have committed. A log failure stops the batch.
	commit := func() {
		for failed == nil && next < len(slots) && slots[next] != nil && slots[next].done {
			s := slots[next]
			res, err := r.settle(ctx, b, ls, s.a.call, s.a.e)
			out.Results = append(out.Results, res)
			if err := errors.Join(s.a.err, err); err != nil {
				failed = err
			}
			next++
		}
	}
	wait := func() {
		i := <-finished
		slots[i].done = true
		delete(running, i)
	}

	for i, c := range b.Calls {
		ov := r.overlap(decls, c)
		for !admissible(ov, running, limit) {
			wait()
			commit()
		}
		if failed != nil {
			break
		}
		a := r.prepare(ctx, b, ag, ls, c, ov)
		s := &slot{a: a}
		slots[i] = s
		if a.run == nil {
			s.done = true
		} else {
			running[i] = ov
			go func(i int, run func() ended) {
				s.a.e = run()
				finished <- i
			}(i, a.run)
		}
		commit()
		// A log that refused this call's tool/call stops admission at once, as
		// the serial pipeline did, even while earlier bodies still run: their
		// results and this call's still commit in order below, and no later call
		// is started.
		if failed != nil || a.err != nil {
			break
		}
	}
	// The bodies already started are waited for and committed in order until a
	// commit fails; after that no result is committed.
	for len(running) > 0 {
		wait()
		commit()
	}
	commit()
	return out, failed
}

// overlap is the classification of c in this batch: unknown, refused or
// forced-exclusive tools are exclusive, as is a call whose Overlap says so. An
// argument over the byte bound is exclusive without being decoded, so its
// Overlap function never sees it; prepare settles it arguments-too-large.
func (r *registry) overlap(decls map[string]*tools.Declaration, c tools.CallRequest) tools.Overlap {
	d, ok := decls[c.Name]
	if !ok || slices.Contains(r.cfg.ForceExclusive, c.Name) || len(c.Arguments) > r.cfg.MaxArgumentBytes {
		return tools.Exclusive
	}
	return d.Classify([]byte(c.Arguments))
}

// admissible reports whether a call classified ov may start next to running
// under the cap limit.
func admissible(ov tools.Overlap, running map[int]tools.Overlap, limit int) bool {
	if len(running) >= limit {
		return false
	}
	for _, o := range running {
		if ov.Conflicts(o) {
			return false
		}
	}
	return true
}
