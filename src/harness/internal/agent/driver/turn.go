package driver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// turn runs one turn when next-turn input is pending (points 2 to 16). ran is
// false when nothing opened. aborted reports a turn a cancel ended, aborted
// or budget{duration}: the next-turn input parked when its cancel landed waits
// for a new wake. A
// returned error is a failed append or flush: the driver stops and
// the turn may stay open in the log.
func (d *Driver) turn() (ran, aborted bool, err error) {
	if len(d.o.Inbox.Pending(agent.ListNextTurn)) == 0 {
		return false, false, nil
	}
	d.setStatus(agent.StatusRunning, false)
	s := d.o.Session
	// Point 2: a turn a failed close left open is closed first.
	if s.Open().IsOpen() {
		if err := d.write(d.life, func(ctx context.Context) error {
			_, err := s.CloseOpen(ctx, surface.CauseFailed)
			return err
		}); err != nil {
			return true, false, err
		}
	}
	n := s.Open().LastTurn + 1
	// The turn is Cancel's from before its claim: a cancel that lands while a
	// claim is being made durable stops this turn, not a later one.
	tctx, cancel := context.WithCancelCause(d.life)
	t := &turnState{turn: n, ctx: tctx, cancel: cancel}
	d.mu.Lock()
	d.cur = t
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		d.cur = nil
		d.mu.Unlock()
		cancel(nil)
	}()
	if lim := d.budget.MaxDuration; lim > 0 {
		go d.timeLimit(t, lim)
	}
	var (
		msg agent.InboxMessage
		ok  bool
	)
	d.discarded()
	err = d.write(d.life, func(ctx context.Context) error {
		var err error
		msg, ok, err = d.o.Inbox.ClaimTurn(ctx, n)
		return err
	})
	if err != nil {
		if !ok {
			return true, false, err
		}
		// The item is taken but not durable: make it durable, then run it. A
		// cancel stops the wait; the item is in the turn it aborts.
		if err := d.durable(t.ctx, err); err != nil {
			return true, false, fmt.Errorf("driver: claim turn %d: %w", n, err)
		}
	}
	if !ok {
		return false, false, nil
	}
	first, err := msg.Message()
	if err != nil {
		return true, false, err
	}
	batch := []vocab.Message{first}
	// A turn cancelled while its claim was recovering leaves parked steering
	// parked: point 3 ends it before any step.
	if t.ctx.Err() == nil {
		more, err := d.claimStep(t)
		if err != nil {
			return true, false, err
		}
		batch = append(batch, more...)
	}

	reason, err := d.steps(t, batch)
	if err != nil {
		return true, false, err
	}
	// Point 15: one turn/end, unless point 5 already wrote it.
	if reason != nil {
		if err := d.write(t.ctx, func(ctx context.Context) error {
			_, err := s.Append(ctx, mustDraft(event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: *reason})))
			return err
		}); err != nil {
			return true, false, err
		}
		// The time limit is a cancel too (timeLimit calls keep): its
		// budget{duration} end holds parked next-turn input like aborted.
		aborted = reason.Kind == event.ReasonAborted ||
			(reason.Kind == event.ReasonBudget && reason.Limit == LimitDuration)
	}
	// Point 16: the next turn opens only after this flush has settled, within
	// FinalFlushTimeout.
	fctx, stop := context.WithTimeout(context.WithoutCancel(t.ctx), d.o.FinalFlushTimeout)
	defer stop()
	if _, err := s.Flush(fctx); err != nil {
		return true, aborted, fmt.Errorf("driver: flush turn %d: %w", n, err)
	}
	// A claim whose recovery a cancel cut short is durable with this flush;
	// Sync clears the inbox's record of it and pays its owed wake.
	if !d.o.Inbox.Durable() {
		if err := d.o.Inbox.Sync(fctx); err != nil {
			return true, aborted, fmt.Errorf("driver: flush turn %d: %w", n, err)
		}
	}
	return true, aborted, nil
}

// durable makes a claim whose append succeeded but whose flush failed (first)
// durable: its items have left the queue and are handed out never again, so
// they must run, not be lost. It retries Inbox.Sync with a backoff until it
// succeeds or the failure is permanent. A cancel of the turn (ctx), Close
// included, stops the wait and a Sync in flight at once (each Sync runs under
// ctx, bounded by CleanupTimeout) and returns nil: the items are in the log,
// the turn they joined ends aborted, and point 16's flush makes them durable.
func (d *Driver) durable(ctx context.Context, first error) error {
	err, delay := first, 10*time.Millisecond
	for {
		if permanent(err) {
			return err
		}
		select {
		case <-ctx.Done():
			return nil
		case <-d.o.After(delay):
		}
		sctx, cancel := context.WithTimeout(ctx, d.o.CleanupTimeout)
		err = d.o.Inbox.Sync(sctx)
		cancel()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return nil
		}
		delay = min(2*delay, time.Second)
	}
}

// timeLimit cancels t with cause budget when its time is up. Like any turn
// cancel, next-turn input parked then waits for a new wake (keep).
func (d *Driver) timeLimit(t *turnState, lim time.Duration) {
	select {
	case <-t.ctx.Done():
	case <-d.o.After(lim):
		d.mu.Lock()
		d.keep(t)
		if t.cancelTurn(agent.CauseBudget) {
			t.limit = LimitDuration
		}
		d.mu.Unlock()
	}
}

// claimStep claims every next-step item into turn t, as messages. Items whose
// claim was appended but not flushed are made durable and run; a cancel of t
// during that recovery returns them as well, and point 3 then ends the turn.
func (d *Driver) claimStep(t *turnState) ([]vocab.Message, error) {
	n := t.turn
	d.discarded()
	var items []agent.InboxMessage
	err := d.write(d.life, func(ctx context.Context) error {
		var err error
		items, err = d.o.Inbox.ClaimStep(ctx, n)
		return err
	})
	if err != nil {
		if items == nil {
			return nil, err
		}
		if err := d.durable(t.ctx, err); err != nil {
			return nil, fmt.Errorf("driver: claim step input of turn %d: %w", n, err)
		}
	}
	out := make([]vocab.Message, 0, len(items))
	for _, m := range items {
		msg, err := m.Message()
		if err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, nil
}

// aborted is the turn/end reason of a cancelled turn: budget{duration} when the
// time limit cancelled it, aborted{cause} otherwise.
func (d *Driver) aborted(t *turnState) *event.Reason {
	d.mu.Lock()
	cause, limit := t.cause, t.limit
	d.mu.Unlock()
	if limit == LimitDuration {
		return &event.Reason{Kind: event.ReasonBudget, Limit: LimitDuration, Value: int(d.budget.MaxDuration.Milliseconds())}
	}
	if cause == "" {
		cause = agent.CauseDisposed
		if c, ok := agent.CauseOf(context.Cause(t.ctx)); ok {
			cause = c
		}
	}
	return &event.Reason{Kind: event.ReasonAborted, Cause: string(cause)}
}

// steps runs points 3 to 14 until the turn stops, returning its turn/end
// reason; nil when point 5 already closed the turn.
func (d *Driver) steps(t *turnState, batch []vocab.Message) (*event.Reason, error) {
	used, maxTokens := 0, false
	// logged holds the ids of the batch's messages already in the log: a
	// kept-open turn's insert. Claimed input is not; point 8 admits it.
	var logged map[string]bool
	for {
		// Point 3.
		if t.ctx.Err() != nil {
			return d.aborted(t), nil
		}
		if !d.budget.stepsLeft(used) {
			return d.stepLimit(), nil
		}
		used++
		res, err := d.step(t, used, batch, logged)
		if err != nil {
			return nil, err
		}
		if res.ended {
			return nil, nil
		}
		if !res.opened {
			return res.reason, nil
		}
		batch, logged = nil, nil
		maxTokens = maxTokens || res.maxTokens

		// Point 14.
		if h := d.o.Hooks.ContinueOrStop; h != nil {
			h(t.turn, used)
		}
		if t.ctx.Err() != nil {
			return d.aborted(t), nil
		}
		if res.reason != nil {
			return res.reason, nil
		}
		pending := len(d.o.Inbox.Pending(agent.ListNextStep)) > 0
		if (pending || (res.calls > 0 && res.cause == "")) && !d.budget.stepsLeft(used) {
			return d.stepLimit(), nil // parked input stays parked
		}
		if pending {
			if batch, err = d.claimStep(t); err != nil {
				return nil, err
			}
			if len(batch) > 0 {
				d.resumed(t)
				continue
			}
		}
		if res.cause != "" {
			return &event.Reason{Kind: event.ReasonAborted, Cause: string(res.cause)}, nil
		}
		if res.calls > 0 {
			continue // the tools owe the model a request
		}
		kind := event.ReasonCompleted
		if maxTokens {
			kind = event.ReasonMaxTokens
		}
		dec, err := agent.AskTurnStopping(d.o.Context, d.o.Agent.ScopeKey(), agent.StopRequest{
			Ctx: t.ctx, Agent: d.o.Agent.Ref(), Turn: t.turn, Steps: used, Reason: kind})
		if err != nil {
			d.failed(t.turn, used, err)
			return extensionReason(err), nil
		}
		keep, ok := dec.(agent.KeepOpen)
		if !ok {
			return &event.Reason{Kind: kind}, nil
		}
		if err := d.insert(t.ctx, keep.Messages); err != nil {
			return nil, err
		}
		batch, logged = keep.Messages, map[string]bool{}
		for _, m := range keep.Messages {
			logged[m.ID()] = true
		}
	}
}

func (d *Driver) stepLimit() *event.Reason {
	return &event.Reason{Kind: event.ReasonBudget, Limit: LimitSteps, Value: d.budget.MaxSteps}
}

// insert appends msgs, each as the event of its role, in one group, a write
// made for parent.
func (d *Driver) insert(parent context.Context, msgs []vocab.Message) error {
	var ds []event.Draft
	for _, m := range msgs {
		dr, err := messageDraft(m)
		if err != nil {
			return fmt.Errorf("driver: a kept-open turn: %w", err)
		}
		ds = append(ds, dr)
	}
	if len(ds) == 0 {
		return nil
	}
	return d.write(parent, func(ctx context.Context) error {
		_, err := d.o.Session.AppendGroup(ctx, ds)
		return err
	})
}

// failed reports a steering listener's failure through agent.Failed.
func (d *Driver) failed(turn, step int, err error) {
	agent.Notify(d.o.Context, agent.Failed, d.o.Agent, agent.FailedNotice{Agent: d.o.Agent.Ref(), Turn: turn, Step: step, Err: err})
}

func extensionReason(err error) *event.Reason {
	by := ""
	var ext *agent.ExtensionError
	if errors.As(err, &ext) {
		by = ext.Point
	}
	return &event.Reason{Kind: event.ReasonError, By: by, Detail: err.Error()}
}

func mustDraft(d event.Draft, err error) event.Draft {
	if err != nil {
		panic(err) // the core payloads above always validate
	}
	return d
}
