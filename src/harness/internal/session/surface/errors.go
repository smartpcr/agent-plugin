// Package surface is the pure code behind a session's core folds
// (docs/harness/areas/core-session.md §6.1, DH-D-SESSION-3, -4, -5): planning and
// applying surface operations, derived model history with stable node identity,
// the request-header and tool-history folds, bracket tracking and the closers of
// an interrupted turn, the canonical request content with its digest, and Rebuild
// over any reader of a log prefix.
//
// Every function here is a function of the events it is given. A State is folded
// by Plan then Apply: Plan judges a candidate record against the current folds and
// changes nothing; Apply, called only after the record was committed, advances
// them. The live session (package session) and a reader of storage use the same
// two calls, so the history a live session holds and the history recomputed from
// the log cannot disagree (DH-T-SESSION-9).
package surface

import (
	"fmt"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// The codes this package raises; invalid-event and invalid-surface are event's.
const (
	CodeUnbalanced  event.Code = "session/unbalanced"
	CodeNoHeader    event.Code = "session/no-header"
	CodeNoBuilder   event.Code = "session/no-builder"
	CodeBrokenStore event.Code = "session/broken-store"
	CodeCheckpoint  event.Code = "session/checkpoint-version"
)

// Sentinels for errors.Is.
var (
	ErrUnbalanced  = &event.Error{Code: CodeUnbalanced}
	ErrNoHeader    = &event.Error{Code: CodeNoHeader}
	ErrNoBuilder   = &event.Error{Code: CodeNoBuilder}
	ErrBrokenStore = &event.Error{Code: CodeBrokenStore}
	ErrCheckpoint  = &event.Error{Code: CodeCheckpoint}
)

func errorf(code event.Code, format string, args ...any) *event.Error {
	return &event.Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// The outcome codes of a tool call a closer answers. Area A03 owns them
// (tools.md §6.2); a test there checks they are equal to these.
const (
	CodeToolNotStarted     = "tools/not-started"
	CodeToolOutcomeUnknown = "tools/outcome-unknown"
	// CodeApprovalUnavailable closes a recorded call whose approval was asked
	// and not given: its body never ran. It is stage 4's code for an approval
	// nobody could answer (tools.md §6.2, interaction-credentials.md §6.3).
	CodeApprovalUnavailable = "approval/unavailable"
)
