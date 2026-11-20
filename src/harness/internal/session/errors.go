// Package session is the session contract of docs/harness/areas/core-session.md
// §6.1 and the live sessions behind it (DH-D-SESSION-2, -4, -5): a Service that
// creates, opens and lists sessions over A05's store, the ReadSession, Session and
// Handle a caller works through, the opaque Request only a session can build, and
// the three scoped live events.
//
// A live session holds no events. It holds the core folds of package surface,
// guarded by its append lock, and a deliverer goroutine that reads committed
// records forward from storage and hands each to the Committed listeners once, in
// order, outside the lock (ADR-9).
//
// The live session sits in this package rather than in a session/core package so
// that Request can have no exported constructor (ADR-7, SP-1): only code in this
// package can wrap a surface.Built in a Request.
package session

import (
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// The codes this package raises, besides event's, surface's and store's.
const (
	CodeClosed       event.Code = "session/closed"
	CodeTooLarge     event.Code = "session/too-large"
	CodeNotCommitted event.Code = "session/not-committed"
	CodeEmptyGroup   event.Code = "session/empty-group"
	CodeUnbalanced              = surface.CodeUnbalanced
	CodeBrokenStore             = surface.CodeBrokenStore
	CodeNoHeader                = surface.CodeNoHeader
	CodeNoBuilder               = surface.CodeNoBuilder
)

// Sentinels for errors.Is. ErrClosed also matches store.ErrClosed's code.
var (
	ErrClosed       = &event.Error{Code: CodeClosed}
	ErrTooLarge     = &event.Error{Code: CodeTooLarge}
	ErrNotCommitted = &event.Error{Code: CodeNotCommitted}
	ErrEmptyGroup   = &event.Error{Code: CodeEmptyGroup}
	ErrUnbalanced   = surface.ErrUnbalanced
	ErrBrokenStore  = surface.ErrBrokenStore
	ErrNoHeader     = surface.ErrNoHeader
	ErrNoBuilder    = surface.ErrNoBuilder
)

func errorf(code event.Code, format string, args ...any) *event.Error {
	return &event.Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// ClosedError is the error of an append to a closed session; Cause is why the
// session closed (nil for an ordinary Close).
type ClosedError struct {
	ID    ID
	Cause error
}

func (e *ClosedError) Error() string {
	if e.Cause == nil {
		return fmt.Sprintf("%s: session %q is closed", CodeClosed, e.ID)
	}
	return fmt.Sprintf("%s: session %q is closed: %v", CodeClosed, e.ID, e.Cause)
}

// Is matches ErrClosed and store.ErrClosed.
func (e *ClosedError) Is(target error) bool { return target == ErrClosed || target == store.ErrClosed }

// Unwrap is the cause.
func (e *ClosedError) Unwrap() error { return e.Cause }

// brokenStore classifies a storage clause the core relies on that did not hold
// (DH-T-SESSION-19).
func brokenStore(id ID, format string, args ...any) error {
	return &event.Error{Code: CodeBrokenStore, Msg: fmt.Sprintf("session %q: ", id) + fmt.Sprintf(format, args...)}
}

func isLeaseLost(err error) bool { return errors.Is(err, store.ErrLeaseLost) }
