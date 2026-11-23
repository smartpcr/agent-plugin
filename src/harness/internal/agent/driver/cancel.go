package driver

import (
	"context"
	"errors"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// turnState is the running turn as Cancel sees it. Its fields after turn are
// guarded by the driver's mu.
type turnState struct {
	turn   int
	ctx    context.Context
	cancel context.CancelCauseFunc
	// cause is the turn's first cancel cause; the turn/end records it.
	cause agent.Cause
	// limit is set when the time budget, not a caller, cancelled the turn.
	limit string
	// keeping is set by the cancel that will abort the turn: next-turn input
	// parked then waits for a new wake. A step cancel's mark is dropped when the
	// turn goes on past it (resumed).
	keeping bool
	step    *stepState
}

// stepState is the step between point 4 and point 13; nil outside it.
type stepState struct {
	step   int
	ctx    context.Context
	cancel context.CancelCauseFunc
	cause  agent.Cause
	// asm and system are the step's prompt assembly and its rendered system
	// prompt, made once at point 4; the step's retries reuse them.
	asm    prompt.Assembly
	system string
}

// cancelTurn cancels the turn and, through its context, the step; the first
// cause wins. The caller holds mu.
func (t *turnState) cancelTurn(c agent.Cause) bool {
	if t.cause != "" {
		return false
	}
	t.cause = c
	t.cancel(&agent.CancelError{Cause: c})
	return true
}

// cancelStep cancels only the step; the first cause wins. The caller holds mu.
func (s *stepState) cancelStep(c agent.Cause) {
	if s.cause != "" {
		return
	}
	s.cause = c
	s.cancel(&agent.CancelError{Cause: c})
}

// Cancel implements agent.Canceller (DH-D-AGENT-5). It never waits for the turn
// to close.
//
// ScopeTurn cancels the turn context with cause: the stream's delivered text
// becomes an interrupted message with no tool call, no new tool call starts, the
// step closes with an error result for each unanswered call and step/end{cause},
// and the turn ends aborted{cause}. ScopeStep cancels only the step context: the
// step closes the same way, and at point 14 the turn goes on with pending
// next-step input or else ends aborted{cause}. Between steps a step cancel
// finds no step and stops nothing: the turn goes on as if it never came (an
// owner decision for Stage 11.2). With no turn running only the inbox choice
// applies. Of two cancels the first cause is the one recorded.
//
// Cancel signals at once and never waits on inbox storage: a claim or Sync
// stalled in a flush cannot hold it. InboxDiscard then removes parked input,
// each item logged discarded with the cause: both lists for a turn cancel, the
// next-step list for a step cancel; Cancel returns when those writes have
// settled, and no claim takes input until they have. InboxKeep leaves it
// parked, and next-turn input parked at the cancel waits for a new wake.
func (d *Driver) Cancel(c agent.Cause, o agent.CancelOptions) error {
	if err := agent.CheckCancel(c, o); err != nil {
		return err
	}
	d.mu.Lock()
	var gate chan struct{}
	if o.Inbox == agent.InboxDiscard {
		gate = make(chan struct{})
		d.gates = append(d.gates, gate)
	}
	if t := d.cur; t != nil {
		switch o.Scope {
		case agent.ScopeTurn:
			d.keep(t)
			t.cancelTurn(c)
		case agent.ScopeStep:
			if t.step != nil {
				if t.step.cause == "" {
					d.keep(t)
				}
				t.step.cancelStep(c)
			}
		}
	}
	d.mu.Unlock()
	if gate == nil {
		return nil
	}
	defer close(gate)
	lists := []agent.List{agent.ListNextStep}
	if o.Scope == agent.ScopeTurn {
		lists = append(lists, agent.ListNextTurn)
	}
	var errs error
	for _, l := range lists {
		for _, m := range d.o.Inbox.Pending(l) {
			err := d.write(d.life, func(ctx context.Context) error {
				_, err := d.o.Inbox.Discard(ctx, l, m.ID, string(c))
				return err
			})
			if err != nil {
				errs = errors.Join(errs, err)
			}
		}
	}
	return errs
}

// keep marks, at a cancel of t, that next-turn input parked now waits for a
// new wake: the wakes their sends left are spent here, without the inbox's
// lock, so a wake left after this is a send that arrived after the cancel.
// The first mark stands until resumed drops a step cancel's. The caller holds
// mu.
func (d *Driver) keep(t *turnState) {
	if t.keeping || t.cause != "" {
		return
	}
	t.keeping = true
	select {
	case <-d.o.Inbox.Wake():
	default:
	}
}

// discarded waits until every Discard a Cancel began before now has settled,
// so a claim never takes input a cancel is discarding. Only the driver
// goroutine calls it.
func (d *Driver) discarded() {
	d.mu.Lock()
	gs := d.gates
	d.mu.Unlock()
	for _, g := range gs {
		select {
		case <-g:
		case <-d.life.Done():
			return
		}
	}
	d.mu.Lock()
	d.gates = d.gates[len(gs):]
	d.mu.Unlock()
}

// resumed is called when point 14 goes on with claimed steering after a step:
// a step cancel the turn survived kept nothing, so its mark is dropped and the
// cancel that does abort the turn makes its own. A turn already cancelled
// keeps the mark its cancel made.
func (d *Driver) resumed(t *turnState) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if t.cause == "" {
		t.keeping = false
	}
}

// stepCause is the cause the step closes with, "" when it was not cancelled.
func stepCause(s *stepState) agent.Cause {
	if s.ctx.Err() == nil {
		return ""
	}
	if c, ok := agent.CauseOf(context.Cause(s.ctx)); ok {
		return c
	}
	return agent.CauseDisposed
}
