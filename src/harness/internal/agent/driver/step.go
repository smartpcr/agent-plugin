package driver

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
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

// step runs points 4 to 13 of step n with batch, the messages not yet in the
// log. Once step/start is appended, step/end is appended on every path but a
// failed append, after every tool call of the step has its tool/result.
func (d *Driver) step(t *turnState, n int, batch []vocab.Message) (res stepResult, err error) {
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

	// The step is cancellable from before point 4: a step cancel that lands
	// while PreStep runs closes this step.
	sctx, cancel := context.WithCancelCause(t.ctx)
	ss := &stepState{step: n, ctx: sctx, cancel: cancel}
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
		err = d.closeStep(ss.ctx, t.turn, n, res.cause)
	}()

	if err := d.admit(ss.ctx, batch, out.Enter.Messages); err != nil {
		return res, err
	}
	msg, reason, err := d.call(t, ss, n, cp.Selection, st, out.Enter.StartsSeries)
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
// a write made for the step's context parent.
func (d *Driver) closeStep(parent context.Context, turn, step int, cause agent.Cause) error {
	s := d.o.Session
	b := s.Open()
	closer := surface.CauseFailed
	if cause != "" {
		closer = surface.Cause(cause)
	}
	ds, err := surface.Closers(surface.Brackets{Calls: b.Calls}, closer, s.Len())
	if err != nil {
		return err
	}
	ds = append(ds, mustDraft(event.StepEndType.Draft(event.StepEnd{Turn: turn, Step: step, Cause: string(cause)})))
	return d.write(parent, func(ctx context.Context) error {
		_, err := s.AppendGroup(ctx, ds)
		return err
	})
}

// admit appends the messages of the entered batch that are not yet in the log:
// the claims logged batch, so only what a PreStep listener added is new.
func (d *Driver) admit(parent context.Context, batch, entered []vocab.Message) error {
	logged := make(map[string]bool, len(batch))
	for _, m := range batch {
		logged[m.ID()] = true
	}
	var fresh []vocab.Message
	for _, m := range entered {
		if !logged[m.ID()] {
			fresh = append(fresh, m)
		}
	}
	return d.insert(parent, fresh)
}

// settled is the message a step's attempt produced.
type settled struct {
	message   vocab.Message
	maxTokens bool
}

// call is points 7 to 11: the model attempts of one step, retried as point 11
// decides, each settled by exactly one event. series is the step's
// Enter.StartsSeries: the step's first request starts a new request series, so
// its header is appended even when unchanged. It returns the logged assistant
// message (nil for none) and the reason that ends the turn after the step.
func (d *Driver) call(t *turnState, ss *stepState, n int, sel agent.ModelSelection, st *StepTools, series bool) (*settled, *event.Reason, error) {
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
		if err := d.header(ss.ctx, p.Route(), st.Schemas(), sel.Controls, series && attempt == 1); err != nil {
			return nil, nil, err
		}
		// Point 9: checkpoint, then build the request at the committed cut, so
		// everything it reflects is durable before a byte leaves the machine.
		// A failed flush means the attempt does not run.
		if err := d.write(ss.ctx, func(ctx context.Context) error {
			_, err := s.Flush(ctx)
			return err
		}); err != nil {
			return nil, nil, err
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
		})
		if err != nil {
			d.failed(t.turn, n, err)
			return nil, extensionReason(err), nil
		}
		r, retry := dec.(agent.Retry)
		if !retry || attempt >= maxAttempts {
			return nil, failureReason(f), nil
		}
		select {
		case <-d.o.After(r.After):
		case <-ss.ctx.Done():
			return nil, nil, nil
		}
	}
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
			Usage: a.Usage, Timing: &timing, ServedBy: a.ServedBy, Estimate: a.Estimate,
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

// header appends a request/header when the step's route, tools or controls
// differ from the one in force, or when force starts a new request series.
func (d *Driver) header(parent context.Context, route vocab.RouteID, schemas []vocab.ToolSchema, c vocab.Controls, force bool) error {
	cur, ok := d.o.Session.RequestHeader()
	if !force && ok && cur.Route == route && sameTools(cur.Tools, schemas) && reflect.DeepEqual(cur.Controls, c) {
		return nil
	}
	p := event.RequestHeaderPayload{Route: route, Tools: schemas}
	if !reflect.DeepEqual(c, vocab.Controls{}) {
		ctrl := c.Clone()
		p.Controls = &ctrl
	}
	dr, err := event.RequestHeader.Draft(p)
	if err != nil {
		return err
	}
	return d.write(parent, func(ctx context.Context) error {
		_, err := d.o.Session.Append(ctx, dr)
		return err
	})
}

func sameTools(a, b []vocab.ToolSchema) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
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
