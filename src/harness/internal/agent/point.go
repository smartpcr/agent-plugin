package agent

import (
	"context"
	"errors"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Appender is the session write the driver holds.
type Appender interface {
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
}

// StepOutcome is how point 4 ended.
type StepOutcome struct {
	// Entered is true when step/start was appended (Seq); Enter is the batch.
	Entered bool
	Enter   Enter
	// Reason is the turn/end reason (Seq) when the step was not entered.
	Reason event.Reason
	Seq    event.Seq
	// Err is the steering listener's failure when Reason is error.
	Err error
}

// ProposeStep runs point 4 to 6 of a turn whose step is not yet open: it asks
// PreStep and then appends step/start (Enter), or turn/end blocked with who and
// why (Reject), or turn/end error naming extension/failed (a listener's error
// or panic); no step/start is appended unless the step is entered. Observers
// learn the outcome through StepDecided (and Failed), whose failures change
// nothing. The returned error is the append's only.
func ProposeStep(c *cordis.Context, a Agent, w Appender, req PreStepRequest) (StepOutcome, error) {
	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	req.Agent = a.Ref()
	var out StepOutcome
	d, err := AskPreStep(c, a.ScopeKey(), req)
	switch d := d.(type) {
	case Enter:
		r, aerr := w.Append(ctx, mustDraft(event.StepStartType.Draft(event.StepStart{Turn: req.Turn, Step: req.Step})))
		if aerr != nil {
			return out, aerr
		}
		out = StepOutcome{Entered: true, Enter: d, Seq: r.Seq}
	case Reject:
		out.Reason = event.Reason{Kind: event.ReasonBlocked, By: d.By, Detail: d.Reason}
	default:
		var ext *ExtensionError
		point := PreStep.Name()
		if errors.As(err, &ext) {
			point = ext.Point
		}
		out.Err = err
		out.Reason = event.Reason{Kind: event.ReasonError, By: point, Detail: err.Error()}
	}
	if !out.Entered {
		r, aerr := w.Append(ctx, mustDraft(event.TurnEndType.Draft(event.TurnEnd{Turn: req.Turn, Reason: out.Reason})))
		if aerr != nil {
			return out, aerr
		}
		out.Seq = r.Seq
	}
	if out.Err != nil {
		Notify(c, Failed, a, FailedNotice{Agent: a.Ref(), Turn: req.Turn, Step: req.Step, Err: out.Err})
	}
	Notify(c, StepDecided, a, StepNotice{Agent: a.Ref(), Turn: req.Turn, Step: req.Step, Entered: out.Entered, Reason: out.Reason, Seq: out.Seq})
	return out, nil
}

func mustDraft(d event.Draft, err error) event.Draft {
	if err != nil {
		panic(err) // the core payloads above always validate
	}
	return d
}
