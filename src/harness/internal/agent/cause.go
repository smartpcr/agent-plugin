package agent

import (
	"context"
	"errors"
)

// Cause is why a turn or a step was cancelled. The set is closed
// (architecture.md §5.6): a cancel names one of these, and the first cause of a
// turn is what its turn/end records as aborted{cause} (DH-D-AGENT-5).
type Cause string

// The causes.
const (
	// CauseUser: a person asked to stop (a stop button, a carrier's cancel).
	CauseUser Cause = "user"
	// CauseParent: the agent that spawned this one cancelled it.
	CauseParent Cause = "parent"
	// CauseExtension: a policy or an extension stopped the turn.
	CauseExtension Cause = "extension"
	// CauseDisposed: the agent is being torn down.
	CauseDisposed Cause = "disposed"
	// CauseBudget: a turn budget was reached (the time limit cancels).
	CauseBudget Cause = "budget"
)

// Valid reports whether c is one of the closed causes.
func (c Cause) Valid() bool {
	switch c {
	case CauseUser, CauseParent, CauseExtension, CauseDisposed, CauseBudget:
		return true
	}
	return false
}

// CancelError is the typed cancel cause a turn or step context carries
// (context.Cause). It is immutable: no transport ever writes onto it.
type CancelError struct{ Cause Cause }

func (e *CancelError) Error() string { return "agent: cancelled (" + string(e.Cause) + ")" }

// Is matches context.Canceled, so a cancelled call reads as one.
func (e *CancelError) Is(target error) bool { return target == context.Canceled }

// CauseOf is the cancel cause err carries, if it is (or wraps) a *CancelError.
func CauseOf(err error) (Cause, bool) {
	var ce *CancelError
	if errors.As(err, &ce) {
		return ce.Cause, true
	}
	return "", false
}

// CancelScope is what a cancel stops: the whole turn or only the running step.
type CancelScope string

// The scopes.
const (
	// ScopeTurn cancels the turn: the step closes, the turn ends aborted{cause}.
	ScopeTurn CancelScope = "turn"
	// ScopeStep cancels only the running step: it closes with the cause and the
	// driver goes on to the continue-or-stop decision (point 14).
	ScopeStep CancelScope = "step"
)

// InboxChoice is what a cancel does with input still parked in the inbox.
type InboxChoice string

// The choices; there is no default.
const (
	// InboxKeep leaves parked input parked; it waits for the next wake.
	InboxKeep InboxChoice = "keep"
	// InboxDiscard removes parked input, logged as discarded with the cause: for
	// a turn cancel both lists, for a step cancel the next-step list.
	InboxDiscard InboxChoice = "discard"
)

// CancelOptions are a cancel's scope and inbox choice; both are required.
type CancelOptions struct {
	Scope CancelScope
	Inbox InboxChoice
}

// CodeInvalidCancel is a cancel with an unknown cause, scope or inbox choice.
const CodeInvalidCancel Code = "agent/invalid-cancel"

// ErrInvalidCancel matches a refused cancel with errors.Is.
var ErrInvalidCancel = &Error{Code: CodeInvalidCancel}

// CheckCancel validates a cancel's arguments: the cause is one of the closed
// set, and the scope and the inbox choice are given.
func CheckCancel(c Cause, o CancelOptions) error {
	switch {
	case !c.Valid():
		return errorf(CodeInvalidCancel, "cause %q is not user, parent, extension, disposed or budget", c)
	case o.Scope != ScopeTurn && o.Scope != ScopeStep:
		return errorf(CodeInvalidCancel, "scope %q is not turn or step", o.Scope)
	case o.Inbox != InboxKeep && o.Inbox != InboxDiscard:
		return errorf(CodeInvalidCancel, "the inbox choice is required: keep or discard, not %q", o.Inbox)
	}
	return nil
}

// Canceller is the cancel of a driven agent. It never waits for the turn to
// close; the log says, on step/end and turn/end, why it closed. It is an
// optional interface an agent's instance implements, not a method of Agent
// (an owner decision for Stage 11.2); Cancel below is the way to call it.
type Canceller interface {
	Cancel(cause Cause, o CancelOptions) error
}

// Cancel cancels a's turn or step for cause. An agent whose instance is no
// Canceller has nothing running to cancel, but its arguments are still checked.
func Cancel(a Agent, cause Cause, o CancelOptions) error {
	if err := CheckCancel(cause, o); err != nil {
		return err
	}
	if v, ok := a.(view); ok {
		a = v.a
	}
	if c, ok := a.(Canceller); ok {
		return c.Cancel(cause, o)
	}
	return nil
}

func (c Cause) String() string { return string(c) }
