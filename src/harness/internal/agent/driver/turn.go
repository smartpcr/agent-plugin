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
// false when nothing opened. kept is non-nil when the turn ended aborted: the
// next-turn input that was parked when the cancel landed, which waits for a
// new wake. A returned error is a failed append or flush: the driver stops and
// the turn may stay open in the log.
func (d *Driver) turn() (ran bool, kept map[string]bool, err error) {
	if len(d.o.Inbox.Pending(agent.ListNextTurn)) == 0 {
		return false, nil, nil
	}
	d.setStatus(agent.StatusRunning, false)
	s := d.o.Session
	// Point 2: a turn a failed close left open is closed first.
	if s.Open().IsOpen() {
		if err := d.write(nil, func(ctx context.Context) error {
			_, err := s.CloseOpen(ctx, surface.CauseFailed)
			return err
		}); err != nil {
			return true, nil, err
		}
	}
	n := s.Open().LastTurn + 1
	var (
		msg agent.InboxMessage
		ok  bool
	)
	err = d.write(nil, func(ctx context.Context) error {
		var err error
		msg, ok, err = d.o.Inbox.ClaimTurn(ctx, n)
		return err
	})
	if err != nil {
		if !ok {
			return true, nil, err
		}
		// The item is taken but not durable: make it durable, then run it.
		if err := d.durable(err); err != nil {
			return true, nil, fmt.Errorf("driver: claim turn %d: %w", n, err)
		}
	}
	if !ok {
		return false, nil, nil
	}
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
	first, err := msg.Message()
	if err != nil {
		return true, nil, err
	}
	batch := []vocab.Message{first}
	more, err := d.claimStep(n)
	if err != nil {
		return true, nil, err
	}
	batch = append(batch, more...)

	reason, err := d.steps(t, batch)
	if err != nil {
		return true, nil, err
	}
	// Point 15: one turn/end, unless point 5 already wrote it.
	if reason != nil {
		if err := d.write(t.ctx, func(ctx context.Context) error {
			_, err := s.Append(ctx, mustDraft(event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: *reason})))
			return err
		}); err != nil {
			return true, nil, err
		}
		if reason.Kind == event.ReasonAborted {
			d.mu.Lock()
			kept = t.kept
			d.mu.Unlock()
			if kept == nil {
				kept = map[string]bool{}
			}
		}
	}
	// Point 16: the next turn opens only after this flush has settled, within
	// FinalFlushTimeout.
	fctx, stop := context.WithTimeout(context.WithoutCancel(t.ctx), d.o.FinalFlushTimeout)
	defer stop()
	if _, err := s.Flush(fctx); err != nil {
		return true, kept, fmt.Errorf("driver: flush turn %d: %w", n, err)
	}
	return true, kept, nil
}

// durable makes a claim whose append succeeded but whose flush failed (first)
// durable: its items have left the queue and are handed out never again, so
// they must run, not be lost. It retries Inbox.Sync with a backoff until it
// succeeds, the failure is permanent or the driver stops.
func (d *Driver) durable(first error) error {
	err, delay := first, 10*time.Millisecond
	for {
		if permanent(err) {
			return err
		}
		select {
		case <-d.life.Done():
			return err
		case <-d.o.After(delay):
		}
		if err = d.write(nil, d.o.Inbox.Sync); err == nil {
			return nil
		}
		delay = min(2*delay, time.Second)
	}
}

// timeLimit cancels t with cause budget when its time is up.
func (d *Driver) timeLimit(t *turnState, lim time.Duration) {
	select {
	case <-t.ctx.Done():
	case <-d.o.After(lim):
		d.mu.Lock()
		d.keepPending(t)
		if t.cancelTurn(agent.CauseBudget) {
			t.limit = LimitDuration
		}
		d.mu.Unlock()
	}
}

// claimStep claims every next-step item into turn n, as messages. Items whose
// claim was appended but not flushed are made durable and run.
func (d *Driver) claimStep(n int) ([]vocab.Message, error) {
	var items []agent.InboxMessage
	err := d.write(nil, func(ctx context.Context) error {
		var err error
		items, err = d.o.Inbox.ClaimStep(ctx, n)
		return err
	})
	if err != nil {
		if items == nil {
			return nil, err
		}
		if err := d.durable(err); err != nil {
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
	for {
		// Point 3.
		if t.ctx.Err() != nil {
			return d.aborted(t), nil
		}
		if !d.budget.stepsLeft(used) {
			return d.stepLimit(), nil
		}
		used++
		res, err := d.step(t, used, batch)
		if err != nil {
			return nil, err
		}
		if res.ended {
			return nil, nil
		}
		if !res.opened {
			return res.reason, nil
		}
		batch = nil
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
			if batch, err = d.claimStep(t.turn); err != nil {
				return nil, err
			}
			if len(batch) > 0 {
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
		batch = keep.Messages
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
		var (
			dr  event.Draft
			err error
		)
		switch m.Role() {
		case vocab.RoleUser:
			dr, err = event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: m})
		case vocab.RoleDeveloper:
			dr, err = event.DeveloperMessage.Draft(event.Append(), event.MessagePayload{Message: m})
		case vocab.RoleSystem:
			dr, err = event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: m})
		default:
			err = fmt.Errorf("driver: a kept-open turn cannot insert a %s message", m.Role())
		}
		if err != nil {
			return err
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
