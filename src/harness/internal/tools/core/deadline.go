package core

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/internal/body"
)

// Enforced deadlines with grace (DH-D-TOOLS-3, DH-ADR-TOOLS-5, DH-ADR-TOOLS-4):
// the body runs on its own goroutine under a context the pipeline cancels with a
// typed cause when the call's budget ends or the turn is cancelled. The
// pipeline then waits grace; a body still running after it is detached, listed
// by Detached, and the call settles tools/outcome-unknown, so a body that
// ignores its context never holds the turn.

// detachedBody is one body that outlived its grace.
type detachedBody struct {
	call  string
	tool  string
	agent agent.Ref
	since time.Time
	// overlap is the call's batch classification, kept while the body runs so
	// a later call on a conflicting resource key does not overlap it.
	overlap tools.Overlap
}

// budget is the call budget of d: the operator's override, else the declared
// Timeout, else the default; 0 is none (tools.NoDeadline). Opting out must be
// explicit (DH-ADR-TOOLS-5): an override that is negative but not
// tools.NoDeadline is misconfigured and ignored, as the declaration path
// refuses it, so it never silently disables the budget.
func (r *registry) budget(d *tools.Declaration) time.Duration {
	t, ok := r.cfg.Timeouts[d.Name()]
	if !ok || t == 0 || (t < 0 && t != tools.NoDeadline) {
		t = d.Timeout()
	}
	switch {
	case t == tools.NoDeadline:
		return 0
	case t == 0:
		return r.cfg.DefaultTimeout
	}
	return t
}

// grace is the registry's grace, or d's when that is longer.
func (r *registry) grace(d *tools.Declaration) time.Duration {
	return max(r.cfg.Grace, d.Grace())
}

type bodyOut struct {
	v   any
	err error
	at  time.Time // when the body returned, on the registry's clock
}

// execute runs the body of d under its budget and the turn's context, and
// settles it as stage 7 ends: the body's own value or failure when it returned
// in time, tools/timeout or tools/aborted when it returned within grace after
// its context was cancelled, tools/outcome-unknown when it did not.
//
// Times are absolute: the budget expires at start+budget whenever its timer is
// noticed, a body that returned at or after that instant has timed out even
// when its return is seen first, and grace after a deadline ends at
// expiry+grace, not grace after the timer was noticed.
func (r *registry) execute(ctx context.Context, b tools.Batch, d *tools.Declaration, bd body.Body, in any, run tools.Run, ov tools.Overlap) ended {
	budget, grace := r.budget(d), r.grace(d)
	clk := r.cfg.Clock
	bctx, cancel := context.WithCancelCause(ctx)
	// Armed before the body starts, so a body that has started has its timer.
	var deadline <-chan time.Time
	var expiry time.Time
	if budget > 0 {
		expiry = clk.Now().Add(budget)
		deadline = clk.After(budget)
	}
	expired := func(at time.Time) bool { return budget > 0 && !at.Before(expiry) }
	done := make(chan bodyOut, 1)
	go func() {
		v, err := runBody(bctx, bd, in, run)
		done <- bodyOut{v, err, clk.Now()}
	}()
	// cause is how the body's context was cancelled, set once, before any grace;
	// "" while it is not.
	var cause tools.Cause
	var graceEnd time.Time
	// returned classifies a body's return. A cancellation the pipeline already
	// acted on keeps its cause: a body whose context the turn cancelled before
	// its budget ended is answered as cancelled even if it returns after the
	// expiry, within grace. Otherwise the return's time, not the order a select
	// picks ready channels in, decides between a timeout and its own value.
	returned := func(o bodyOut) ended {
		switch {
		case cause == tools.CauseCancelled:
			return cancelledEnded(tools.CauseCancelled, budget, o)
		case expired(o.at):
			// At or after its expiry: a timeout, even when its return won the
			// race with the timer.
			return cancelledEnded(tools.CauseDeadline, budget, o)
		case ctx.Err() != nil:
			// The turn was cancelled as the body returned: the same as a body
			// that returned within grace.
			return cancelledEnded(tools.CauseCancelled, budget, o)
		}
		// Before its expiry: its own value, even when the deadline was seen first.
		return bodyEnded(o)
	}
	select {
	case o := <-done:
		cancel(nil)
		return returned(o)
	case <-deadline:
		cause, graceEnd = tools.CauseDeadline, expiry.Add(grace)
		cancel(fmt.Errorf("%w: the budget of %s ended", tools.ErrTimeout, budget))
	case <-ctx.Done():
		cause, graceEnd = tools.CauseCancelled, clk.Now().Add(grace)
		if expired(clk.Now()) {
			// The budget had already ended when the turn's cancellation was seen.
			cause, graceEnd = tools.CauseDeadline, expiry.Add(grace)
			cancel(fmt.Errorf("%w: the budget of %s ended", tools.ErrTimeout, budget))
		} else {
			cancel(context.Cause(ctx))
		}
	}
	select {
	case o := <-done:
		return returned(o)
	case <-clk.After(graceEnd.Sub(clk.Now())):
		// A return that is ready as grace ends is still a return.
		select {
		case o := <-done:
			return returned(o)
		default:
		}
	}
	r.detach(b, d, run, ov, done)
	what := "the turn was cancelled"
	if cause == tools.CauseDeadline {
		what = fmt.Sprintf("timed out after %s", budget)
	}
	e := stop(tools.StageBody, tools.CodeOutcomeUnknown, "",
		fmt.Sprintf("%s and the body did not return within the %s grace; it may still be running, and its effects are unknown", what, grace))
	e.cause = cause
	return e
}

// bodyEnded is the outcome of a body that returned before its context was
// cancelled.
func bodyEnded(o bodyOut) ended {
	if o.err != nil {
		code := tools.CodeFailed
		var te *tools.Error
		if errors.As(o.err, &te) && te.Code != "" {
			code = te.Code
		}
		return stop(tools.StageBody, code, "", trimCode(o.err.Error(), code))
	}
	return ended{stage: tools.StageShape, value: o.v}
}

// cancelledEnded is the outcome of a body that returned within grace after its
// context was cancelled: a timeout is tools/timeout whatever the body returned;
// a cancellation replaces a value with tools/aborted and keeps a specific
// failure.
func cancelledEnded(cause tools.Cause, budget time.Duration, o bodyOut) ended {
	var e ended
	if cause == tools.CauseDeadline {
		e = stop(tools.StageBody, tools.CodeTimeout, "", fmt.Sprintf("the call exceeded its budget of %s", budget))
	} else {
		e = stop(tools.StageBody, tools.CodeAborted, "", "the turn was cancelled while the body ran")
		var te *tools.Error
		if o.err != nil && errors.As(o.err, &te) && te.Code != "" && te.Code != tools.CodeFailed &&
			te.Code != tools.CodeTimeout && te.Code != tools.CodeAborted {
			e = stop(tools.StageBody, te.Code, "", trimCode(o.err.Error(), te.Code))
		}
	}
	e.cause = cause
	return e
}

// detach lists the body and waits for it on its own goroutine: its return
// removes it from the list and appends tool/late-return to the call's session.
// Its value is never shown to the model.
func (r *registry) detach(b tools.Batch, d *tools.Declaration, run tools.Run, ov tools.Overlap, done <-chan bodyOut) {
	db := &detachedBody{call: run.CallID, tool: d.Name(), agent: run.Agent, since: r.cfg.Clock.Now(), overlap: ov}
	r.mu.Lock()
	r.detached[db] = struct{}{}
	r.mu.Unlock()
	go func() {
		o := <-done
		r.mu.Lock()
		delete(r.detached, db)
		r.mu.Unlock()
		if dr, err := tools.LateReturnType.Draft(tools.LateReturn{CallID: db.call, Tool: db.tool, OK: o.err == nil}); err == nil {
			// The turn that made the call may be long over; the audit is best effort.
			_, _ = b.Log.Append(context.Background(), dr)
		}
	}()
}

// busy is why a call of tool classified ov, by ag, may not start, "" when it
// may: a detached body of the same tool for ag is still running, or one whose
// resource keys conflict with ov's. Only keyed classifications are compared:
// exclusivity orders the calls of one batch, and a detached exclusive body
// would otherwise refuse every later call of the agent. It refuses at once and
// never waits, since a detached body may never return.
func (r *registry) busy(ag agent.Ref, tool string, ov tools.Overlap) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	keyed := func(o tools.Overlap) bool { return !o.IsExclusive() && !o.IsFree() }
	for db := range r.detached {
		if db.agent != ag {
			continue
		}
		if db.tool == tool {
			return fmt.Sprintf("an earlier call of %q by this agent did not return within grace and is still running", tool)
		}
		if keyed(ov) && keyed(db.overlap) && ov.Conflicts(db.overlap) {
			return fmt.Sprintf("call %s of %q by this agent did not return within grace, is still running and holds a resource key this call conflicts with", db.call, db.tool)
		}
	}
	return ""
}

func (r *registry) Detached() []tools.DetachedCall {
	r.mu.Lock()
	out := make([]tools.DetachedCall, 0, len(r.detached))
	for db := range r.detached {
		out = append(out, tools.DetachedCall{CallID: db.call, Tool: db.tool, Agent: db.agent, Since: db.since})
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b tools.DetachedCall) int {
		if c := a.Since.Compare(b.Since); c != 0 {
			return c
		}
		if a.CallID < b.CallID {
			return -1
		}
		if a.CallID > b.CallID {
			return 1
		}
		return 0
	})
	return out
}

func (r *registry) Limits() tools.Limits {
	return tools.Limits{MaxResultBytes: r.cfg.MaxResultBytes, MaxArgumentBytes: r.cfg.MaxArgumentBytes,
		DefaultTimeout: r.cfg.DefaultTimeout, Grace: r.cfg.Grace, MaxParallel: r.cfg.MaxParallel}
}

func (r *registry) SetSpill(ctx *cordis.Context, id string, s tools.SpillShaper) (*cordis.EffectHandle, error) {
	if s == nil || id == "" {
		return nil, &tools.Error{Code: tools.CodeInvalidDeclaration, Msg: "a spill seat needs its id and function"}
	}
	k := scope.Of(ctx)
	seat := &named[tools.SpillShaper]{id, s}
	var refused error
	h, err := r.layers.Add(ctx, "tools spill "+id, func(l *layer) func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if l.spill != nil {
			refused = &tools.Error{Code: tools.CodeDuplicate, Msg: "the spill seat of " + k.String() + " is taken by " + l.spill.id}
			return nil
		}
		l.spill = seat
		return func() {
			l.mu.Lock()
			if l.spill == seat {
				l.spill = nil
			}
			l.mu.Unlock()
		}
	})
	if err != nil {
		return nil, err
	}
	if refused != nil {
		h.Dispose()
		return nil, refused
	}
	return h, nil
}
