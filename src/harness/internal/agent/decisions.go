package agent

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// ModelSelection is the model a step is proposed for: the route, the model
// name ("" is the route's default) and the request controls.
type ModelSelection struct {
	Route    vocab.RouteID  `json:"route"`
	Model    string         `json:"model,omitempty"`
	Controls vocab.Controls `json:"controls,omitzero"`
}

// --------------------------------------------------------------- PreStep

// PreStepRequest is point 4's proposal. Messages are copies: a listener changes
// the batch only through Enter.
type PreStepRequest struct {
	// Ctx is the turn's context.
	Ctx        context.Context
	Agent      Ref
	Turn, Step int
	Messages   []vocab.Message
	Selection  ModelSelection
	// Previous is the selection of the last step, nil for the first.
	Previous *ModelSelection
}

// PreStepDecision is Enter or Reject.
type PreStepDecision interface{ preStep() }

// Enter opens the step with Messages as its batch; StartsSeries starts a new
// request series (it reaches the request header).
type Enter struct {
	Messages     []vocab.Message
	StartsSeries bool
}

// Reject ends the turn blocked, by By for Reason, and opens no step.
type Reject struct {
	By     string
	Reason string
}

func (Enter) preStep()  {}
func (Reject) preStep() {}

// PreStep is point 4: listeners wrap one another in registration order and may
// return Enter or Reject. Dispatch through AskPreStep.
var PreStep = cordis.NewAround[PreStepRequest, PreStepDecision]("agent/pre-step")

// --------------------------------------------------------------- AttemptFailed

// RetryPolicy is the bounded retry the step runs under.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// AttemptFailure is point 11's fact: an attempt settled with a failure.
type AttemptFailure struct {
	Ctx          context.Context
	Agent        Ref
	Turn, Step   int
	Attempt      int
	Failure      *vocab.Failure
	Policy       RetryPolicy
	AttemptsLeft int
	// StepStarted is when the step's first attempt started, so an elapsed bound
	// counts that attempt's own time; zero when the raiser does not know it.
	StepStarted time.Time
}

// AttemptDecision is Retry or GiveUp.
type AttemptDecision interface{ attempt() }

// Retry waits After and runs the attempt again in the same step.
type Retry struct {
	After  time.Duration
	Reason string
}

// GiveUp ends the turn with the failure.
type GiveUp struct{}

func (Retry) attempt()  {}
func (GiveUp) attempt() {}

// AttemptFailed is point 11. The terminal is GiveUp: with no listener nothing
// is retried on the model's behalf.
var AttemptFailed = cordis.NewAround[AttemptFailure, AttemptDecision]("agent/attempt-failed")

// --------------------------------------------------------------- TurnStopping

// StopRequest is point 14: the turn would stop for Reason (an event.Reason kind).
type StopRequest struct {
	Ctx    context.Context
	Agent  Ref
	Turn   int
	Steps  int
	Reason string
}

// StopDecision is AllowStop or KeepOpen.
type StopDecision interface{ stop() }

// AllowStop lets the turn stop.
type AllowStop struct{}

// KeepOpen keeps the turn open: Messages are inserted and the next step runs.
type KeepOpen struct {
	Messages []vocab.Message
	Reason   string
}

func (AllowStop) stop() {}
func (KeepOpen) stop()  {}

// turnStopping is only reachable through OnTurnStopping, which never hands a
// listener next: a listener can keep a turn open and never force it closed.
var turnStopping = cordis.NewAround[StopRequest, StopDecision]("agent/turn-stopping")

// TurnStoppingListener is a listener of the turn-stopping point.
type TurnStoppingListener func(ctx *cordis.Context, req StopRequest) (StopDecision, error)

// OnTurnStopping registers l as an effect of ctx's fiber. The helper always
// calls downstream too and keeps the turn open if either side says KeepOpen,
// joining the messages in registration order.
func OnTurnStopping(ctx *cordis.Context, l TurnStoppingListener, opts ...cordis.ListenOption) (*cordis.EffectHandle, error) {
	if l == nil {
		return nil, errors.New("agent: OnTurnStopping needs a listener")
	}
	return turnStopping.On(ctx, func(c *cordis.Context, req StopRequest, next func(StopRequest) (StopDecision, error)) (StopDecision, error) {
		mine, err := l(c, req)
		if err != nil {
			return nil, err
		}
		if mine == nil {
			return nil, errors.New("agent: a turn-stopping listener returned no decision")
		}
		down, err := next(req)
		if err != nil {
			return nil, err
		}
		return mergeStop(mine, down), nil
	}, opts...)
}

func mergeStop(a, b StopDecision) StopDecision {
	ka, aKeep := a.(KeepOpen)
	kb, bKeep := b.(KeepOpen)
	switch {
	case aKeep && bKeep:
		reason := ka.Reason
		if reason == "" {
			reason = kb.Reason
		}
		return KeepOpen{Messages: append(slices.Clone(ka.Messages), kb.Messages...), Reason: reason}
	case aKeep:
		return ka
	case bKeep:
		return kb
	}
	return AllowStop{}
}

// --------------------------------------------------------------- dispatch

// ExtensionError is a steering listener's error or panic: it ends the turn as
// error with code extension/failed and the point's name.
type ExtensionError struct {
	Point string
	Err   error
}

func (e *ExtensionError) Error() string {
	return fmt.Sprintf("%s: listener of %s failed: %v", CodeExtensionFailed, e.Point, e.Err)
}

func (e *ExtensionError) Unwrap() error { return e.Err }

// Is matches ErrExtensionFailed.
func (e *ExtensionError) Is(target error) bool { return target == ErrExtensionFailed }

func extFailed(point string, err error) error { return &ExtensionError{Point: point, Err: err} }

// AskPreStep dispatches PreStep to the listeners visible at the agent's scope at.
// With no listener the proposal is entered as it is.
func AskPreStep(ctx *cordis.Context, at scope.Key, req PreStepRequest) (PreStepDecision, error) {
	req.Messages = slices.Clone(req.Messages)
	d, err := PreStep.Around(ctx.WithFilter(scope.Filter(at)), req, func(r PreStepRequest) (PreStepDecision, error) {
		return Enter{Messages: slices.Clone(r.Messages)}, nil
	})
	if err != nil {
		return nil, extFailed(PreStep.Name(), err)
	}
	switch d := d.(type) {
	case Enter:
		return d, nil
	case Reject:
		if d.By == "" {
			d.By = PreStep.Name()
		}
		return d, nil
	}
	return nil, extFailed(PreStep.Name(), fmt.Errorf("returned no decision (%T)", d))
}

// AskAttemptFailed dispatches AttemptFailed. A cancelled attempt is not asked
// about (the turn ends aborted): GiveUp. A Retry with a negative wait is an
// extension failure. A Permanent failure (llm/auth among them) is never retried
// (SP-2): a Retry any listener returns for it is GiveUp, and the turn ends with
// the failure itself.
func AskAttemptFailed(ctx *cordis.Context, at scope.Key, f AttemptFailure) (AttemptDecision, error) {
	if f.Failure == nil {
		return nil, errors.New("agent: AttemptFailed needs the failure")
	}
	if f.Failure.Class == vocab.Cancelled {
		return GiveUp{}, nil
	}
	d, err := AttemptFailed.Around(ctx.WithFilter(scope.Filter(at)), f, func(AttemptFailure) (AttemptDecision, error) {
		return GiveUp{}, nil
	})
	if err != nil {
		return nil, extFailed(AttemptFailed.Name(), err)
	}
	switch d := d.(type) {
	case GiveUp:
		return d, nil
	case Retry:
		if d.After < 0 {
			return nil, extFailed(AttemptFailed.Name(), fmt.Errorf("retry after %v", d.After))
		}
		if f.Failure.Class == vocab.Permanent {
			return GiveUp{}, nil
		}
		return d, nil
	}
	return nil, extFailed(AttemptFailed.Name(), fmt.Errorf("returned no decision (%T)", d))
}

// AskTurnStopping dispatches the turn-stopping point. It is not asked after an
// abort, an error or a budget stop: those always stop.
func AskTurnStopping(ctx *cordis.Context, at scope.Key, req StopRequest) (StopDecision, error) {
	switch req.Reason {
	case event.ReasonAborted, event.ReasonError, event.ReasonBudget, event.ReasonInterrupted:
		return AllowStop{}, nil
	}
	d, err := turnStopping.Around(ctx.WithFilter(scope.Filter(at)), req, func(StopRequest) (StopDecision, error) {
		return AllowStop{}, nil
	})
	if err != nil {
		return nil, extFailed(turnStopping.Name(), err)
	}
	if d == nil {
		return nil, extFailed(turnStopping.Name(), errors.New("returned no decision"))
	}
	return d, nil
}
