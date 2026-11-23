package driver

import (
	"context"
	"fmt"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// stepResult is how one step ended.
type stepResult struct {
	// opened is false when the step never opened (no step/start): reason then
	// ends the turn. ended is true when point 5 already wrote the turn/end.
	opened, ended bool
	// calls is the number of tool calls the step's message made.
	calls int
	// cause is the step's cancel cause, "" when it was not cancelled.
	cause agent.Cause
	// reason ends the turn after the step closed (error, refusal); nil goes on.
	reason    *event.Reason
	maxTokens bool
}

// step runs points 4 to 13 of step n with batch, the step's input: claimed
// messages, which point 8 admits, and kept-open messages already in the log
// (their ids in logged). Once step/start is appended, step/end is appended on
// every path but a failed append, after every tool call of the step has its
// tool/result.
func (d *Driver) step(t *turnState, n int, batch []vocab.Message, logged map[string]bool) (res stepResult, err error) {
	var cp agent.Capture
	if err := d.write(t.ctx, func(ctx context.Context) error {
		var err error
		cp, err = d.o.Model.Capture(ctx, t.turn, n)
		return err
	}); err != nil {
		return stepResult{reason: errorReason("driver/model", err)}, nil
	}
	st, err := ResolveStep(t.ctx, d.o.Tools, d.o.Agent, cp.Selection)
	if err != nil {
		if t.ctx.Err() != nil {
			return stepResult{reason: d.aborted(t)}, nil
		}
		return stepResult{reason: errorReason("driver/tools", err)}, nil
	}
	defer st.Release()
	// Point 4: the step's prompt, assembled once from the step's view; the
	// step's retries reuse it. A failure opens no step.
	asm, system, err := d.assemble(t.ctx, st.View())
	if err != nil {
		if t.ctx.Err() != nil {
			return stepResult{reason: d.aborted(t)}, nil
		}
		return stepResult{reason: errorReason("driver/prompt", err)}, nil
	}

	// The step is cancellable from before point 4: a step cancel that lands
	// while PreStep runs closes this step.
	sctx, cancel := context.WithCancelCause(t.ctx)
	ss := &stepState{step: n, ctx: sctx, cancel: cancel, asm: asm, system: system}
	d.mu.Lock()
	t.step = ss
	d.mu.Unlock()
	defer func() {
		d.mu.Lock()
		t.step = nil
		d.mu.Unlock()
		cancel(nil)
	}()

	// Points 4 to 6: PreStep, then step/start with the batch and the capture.
	out, err := agent.ProposeStep(d.o.Context, d.o.Agent, d.log(), agent.PreStepRequest{
		Ctx: t.ctx, Turn: t.turn, Step: n, Messages: batch, Selection: cp.Selection, Previous: cp.Previous,
	}, cp.Record)
	if err != nil {
		return stepResult{}, err
	}
	if !out.Entered {
		return stepResult{ended: true}, nil
	}
	res.opened = true
	// Point 13 runs on every path from here.
	defer func() {
		if err != nil {
			return // a failed append: the session's next write-open closes the step
		}
		res.cause = stepCause(ss)
		var flush *event.Reason
		flush, err = d.closeStep(ss.ctx, t.turn, n, res.cause)
		// A failed step-end checkpoint ends the turn error unless the step
		// already ends it otherwise; a cancelled step stays aborted, and
		// point 16's flush settles what this one left.
		if flush != nil && res.reason == nil && res.cause == "" {
			res.reason = flush
		}
	}()

	msg, reason, err := d.call(t, ss, n, cp.Selection, admission{
		controls: cp.Selection.Controls, series: out.Enter.StartsSeries, entered: out.Enter.Messages, logged: logged,
	})
	if err != nil {
		return res, err
	}
	res.reason = reason
	if msg == nil {
		return res, nil
	}
	res.maxTokens = msg.maxTokens
	calls := toolCalls(msg.message)
	res.calls = len(calls)
	if len(calls) > 0 {
		// Point 12. A cancelled step still runs the batch: each call is logged
		// and answered tools/aborted-before-start without its body running.
		// A batch that a log write stopped ends the turn error once point 13
		// has answered the calls it left.
		if _, xerr := st.Execute(sctx, d.log(), calls); xerr != nil {
			if permanent(xerr) {
				return res, xerr
			}
			d.failed(t.turn, n, xerr)
			res.reason = errorReason("driver/tools", xerr)
		}
	}
	return res, nil
}

// closeStep is point 13: an error result for each call still unanswered (one
// whose batch failed), then step/end with the step's cancel cause, one group,
// a write made for the step's context parent. Then the step-end checkpoint
// (DH-D-STORE-4) through the policy mounted on the driver's context, a write
// like the closers: a cancel does not stop it, CleanupTimeout bounds it. A
// failed checkpoint is the reason that ends the turn (driver/flush), or the
// error when no retry can mend it.
func (d *Driver) closeStep(parent context.Context, turn, step int, cause agent.Cause) (*event.Reason, error) {
	s := d.o.Session
	b := s.Open()
	closer := surface.CauseFailed
	if cause != "" {
		closer = surface.Cause(cause)
	}
	ds, err := surface.Closers(surface.Brackets{Calls: b.Calls}, closer, s.Len())
	if err != nil {
		return nil, err
	}
	ds = append(ds, mustDraft(event.StepEndType.Draft(event.StepEnd{Turn: turn, Step: step, Cause: string(cause)})))
	if err := d.write(parent, func(ctx context.Context) error {
		_, err := s.AppendGroup(ctx, ds)
		return err
	}); err != nil {
		return nil, err
	}
	err = d.write(parent, func(ctx context.Context) error {
		return checkpoint.Before(ctx, d.checkpoints(), s, checkpoint.StepEnd)
	})
	switch {
	case err == nil:
		return nil, nil
	case permanent(err):
		return nil, err
	}
	return errorReason("driver/flush", err), nil
}

// checkpoints is the checkpoint policy mounted on the driver's context, the
// Default flush when none is.
func (d *Driver) checkpoints() checkpoint.Policy { return checkpoint.Mounted(d.o.Context) }

// settled is the message a step's attempt produced.
type settled struct {
	message   vocab.Message
	maxTokens bool
}

// call is points 7 to 11: the model attempts of one step, retried as point 11
// decides, each settled by exactly one event. adm is the step's admission: the
// first attempt admits it whole (point 8), a retry only a changed header. It
// returns the logged assistant message (nil for none) and the reason that ends
// the turn after the step.
func (d *Driver) call(t *turnState, ss *stepState, n int, sel agent.ModelSelection, adm admission) (*settled, *event.Reason, error) {
	s := d.o.Session
	maxAttempts := d.budget.attempts()
	started := d.o.Now()
	for attempt := 1; ; attempt++ {
		if ss.ctx.Err() != nil {
			return nil, nil, nil
		}
		p, err := d.o.Models.Prepare(ss.ctx, sel.Route, sel.Model, sel.Controls)
		if err != nil {
			if ss.ctx.Err() != nil {
				return nil, nil, nil
			}
			return nil, errorReason("driver/prepare", err), nil
		}
		adm.route, adm.first = p.Route(), attempt == 1
		if err := d.admitBatch(ss.ctx, ss, adm); err != nil {
			return nil, nil, err
		}
		// Point 9: checkpoint, then build the request at the committed cut, so
		// everything it reflects is durable before a byte leaves the machine.
		// The checkpoint is the policy mounted on the driver's context
		// (DH-D-STORE-4), the Default flush when none is. A failed checkpoint
		// means the attempt does not run. Like durable, it runs under the
		// step's context, bounded by CleanupTimeout, so a cancel stops a
		// stalled store at once and the turn ends aborted; point 16's flush
		// settles what this one left. A failure no retry can mend stops the
		// driver; any other ends the turn error.
		fctx, stop := context.WithTimeout(ss.ctx, d.o.CleanupTimeout)
		err = checkpoint.Before(fctx, d.checkpoints(), s, checkpoint.Attempt)
		stop()
		if err != nil {
			if ss.ctx.Err() != nil {
				return nil, nil, nil
			}
			if permanent(err) {
				return nil, nil, err
			}
			return nil, errorReason("driver/flush", err), nil
		}
		req, err := s.NextRequest()
		if err != nil {
			return nil, errorReason("driver/request", err), nil
		}
		var rec llm.Attempt
		var got bool
		for range p.Stream(ss.ctx, req, llm.AttemptSinkFunc(func(a llm.Attempt) error { rec, got = a, true; return nil })) {
		}
		if !got {
			return nil, nil, fmt.Errorf("driver: attempt %d of step %d settled no record", attempt, n)
		}
		if err := d.settle(ss.ctx, rec); err != nil {
			return nil, nil, err
		}
		if rec.Succeeded() {
			return &settled{message: *rec.Message, maxTokens: rec.Finish.Reason == vocab.FinishMaxTokens}, nil, nil
		}
		if ss.ctx.Err() != nil {
			if rec.Message != nil {
				return &settled{message: *rec.Message}, nil, nil
			}
			return nil, nil, nil
		}
		f := rec.Finish.Failure
		if f == nil {
			f, _ = vocab.NewFailure(vocab.CodeEmptyResponse, "the attempt produced no message")
		}
		dec, err := agent.AskAttemptFailed(d.o.Context, d.o.Agent.ScopeKey(), agent.AttemptFailure{
			Ctx: ss.ctx, Agent: d.o.Agent.Ref(), Turn: t.turn, Step: n, Attempt: attempt, Failure: f,
			Policy: retryPolicy(p.Retry(), maxAttempts), AttemptsLeft: maxAttempts - attempt, StepStarted: started,
			RoutePolicy: p.Retry(),
		})
		if err != nil {
			d.failed(t.turn, n, err)
			return nil, extensionReason(err), nil
		}
		r, retry := dec.(agent.Retry)
		if !retry || attempt >= maxAttempts {
			return nil, failureReason(f), nil
		}
		// The listener decided; the driver waits, on the session clock and the
		// step's context. A cancel during the wait ends the step with the cancel
		// cause (nil reason: the caller records it), not with f.
		if !d.retryWait(ss, t.turn, n, attempt, r.After) {
			return nil, nil, nil
		}
	}
}

// retryWait waits after on the session clock for failed attempt attempt of
// step n, or until the step is cancelled, and reports whether the wait ran out.
// Either way it then sends agent.RetryWaited with the outcome.
func (d *Driver) retryWait(ss *stepState, turn, n, attempt int, after time.Duration) bool {
	select {
	case <-d.o.After(after):
	case <-ss.ctx.Done():
	}
	// A cancel that raced the timer still wins: the loop would not attempt.
	ended := ss.ctx.Err() == nil
	agent.Notify(d.o.Context, agent.RetryWaited, d.o.Agent, agent.RetryWaitEnded{
		Agent: d.o.Agent.Ref(), Turn: turn, Step: n, Attempt: attempt, Cancelled: !ended,
	})
	return ended
}

// settle appends the attempt's one settlement event. An interrupted attempt
// that kept a prefix is the turn's assistant/message: the prefix is history,
// with no tool call.
func (d *Driver) settle(parent context.Context, a llm.Attempt) error {
	var (
		dr  event.Draft
		err error
	)
	if !a.Succeeded() && a.Message != nil && a.Finish.Failure != nil && a.Finish.Failure.Class == vocab.Cancelled && a.RequestDigest != "" {
		asOf, timing := a.RequestAsOf, a.Timing
		dr, err = event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: *a.Message, RequestAsOf: &asOf, RequestDigest: a.RequestDigest,
			Usage: a.Usage, Timing: &timing, ServedBy: a.ServedBy, Estimate: a.Estimate, Client: a.Client,
		})
	} else {
		dr, err = a.Draft()
	}
	if err != nil {
		return err
	}
	return d.write(parent, func(ctx context.Context) error {
		_, err := d.o.Session.Append(ctx, dr)
		return err
	})
}

func retryPolicy(p llm.RetryPolicy, maxAttempts int) agent.RetryPolicy {
	return agent.RetryPolicy{MaxAttempts: maxAttempts, BaseDelay: p.BaseDelay, MaxDelay: p.MaxDelay}
}

// toolCalls are the tool-use blocks of m as the batch's calls.
func toolCalls(m vocab.Message) []tools.CallRequest {
	var out []tools.CallRequest
	for _, b := range m.Blocks() {
		if b.Kind == vocab.KindToolUse && b.ToolUse != nil {
			args := string(b.ToolUse.Args)
			if args == "" {
				args = "{}"
			}
			out = append(out, tools.CallRequest{ID: b.ToolUse.ID, Name: b.ToolUse.Name, Arguments: args})
		}
	}
	return out
}

func failureReason(f *vocab.Failure) *event.Reason {
	if f.Code == vocab.CodeContentRefused {
		return &event.Reason{Kind: event.ReasonRefusal, Failure: f}
	}
	return &event.Reason{Kind: event.ReasonError, Failure: f}
}

func errorReason(by string, err error) *event.Reason {
	return &event.Reason{Kind: event.ReasonError, By: by, Detail: err.Error()}
}

// noCancelLog writes through a cancelled context: a cancelled step's tool/call
// and its error result, and the step's own brackets, are still logged. Each
// write keeps ctx's values but not its cancel, and is bounded by bound
// (Options.CleanupTimeout), so a stalled store cannot hold the step.
type noCancelLog struct {
	s interface {
		Append(ctx context.Context, d event.Draft) (event.Receipt, error)
		AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error)
		Flush(ctx context.Context) (event.Offset, error)
	}
	bound time.Duration
}

// log is the session as the step's pipeline writes it.
func (d *Driver) log() noCancelLog { return noCancelLog{s: d.o.Session, bound: d.o.CleanupTimeout} }

func (l noCancelLog) ctx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), l.bound)
}

func (l noCancelLog) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	ctx, cancel := l.ctx(ctx)
	defer cancel()
	return l.s.Append(ctx, d)
}

func (l noCancelLog) AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error) {
	ctx, cancel := l.ctx(ctx)
	defer cancel()
	return l.s.AppendGroup(ctx, ds)
}

func (l noCancelLog) Flush(ctx context.Context) (event.Offset, error) {
	ctx, cancel := l.ctx(ctx)
	defer cancel()
	return l.s.Flush(ctx)
}
