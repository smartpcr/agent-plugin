package driver

import (
	"context"
	"errors"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
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
	// kept is the next-turn input parked at the turn's first cancel; nil
	// until a cancel lands.
	kept map[string]bool
	step *stepState
}

// stepState is the step between point 4 and point 13; nil outside it.
type stepState struct {
	step   int
	ctx    context.Context
	cancel context.CancelCauseFunc
	cause  agent.Cause
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
// finds no step and stops nothing. With no turn running only the inbox choice
// applies. Of two cancels the first cause is the one recorded.
//
// InboxDiscard removes parked input first, each item logged discarded with the
// cause: both lists for a turn cancel, the next-step list for a step cancel.
// InboxKeep leaves it parked, and parked next-turn input waits for a new wake.
func (d *Driver) Cancel(c agent.Cause, o agent.CancelOptions) error {
	if err := agent.CheckCancel(c, o); err != nil {
		return err
	}
	var errs error
	if o.Inbox == agent.InboxDiscard {
		lists := []agent.List{agent.ListNextStep}
		if o.Scope == agent.ScopeTurn {
			lists = append(lists, agent.ListNextTurn)
		}
		for _, l := range lists {
			for _, m := range d.o.Inbox.Pending(l) {
				err := d.write(nil, func(ctx context.Context) error {
					_, err := d.o.Inbox.Discard(ctx, l, m.ID, string(c))
					return err
				})
				if err != nil {
					errs = errors.Join(errs, err)
				}
			}
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if t := d.cur; t != nil {
		switch o.Scope {
		case agent.ScopeTurn:
			d.keepPending(t)
			t.cancelTurn(c)
		case agent.ScopeStep:
			if t.step != nil {
				d.keepPending(t)
				t.step.cancelStep(c)
			}
		}
	}
	return errs
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
