package tools

import (
	"strings"
)

// Code is a "<domain>/<reason>" error code.
type Code string

// The codes this package and its registry raise at registration and step
// resolution, and the pipeline's outcome codes (areas/tools.md §6.2).
const (
	// CodeInvalidDeclaration: a declaration that cannot be registered (a bad name,
	// no body, a schema that cannot be derived or leaves the model-facing subset).
	CodeInvalidDeclaration Code = "tools/invalid-declaration"
	// CodeDuplicate: a second tool of one name in one layer.
	CodeDuplicate Code = "tools/duplicate"
	// CodeUnscoped: Restrict through a context that carries no scope.
	CodeUnscoped Code = "tools/unscoped"
	// CodeActivationFailed: a When condition failed, so the step view was not built.
	CodeActivationFailed Code = "tools/activation-failed"
	// CodeInvalidArguments: arguments that do not decode into the input type.
	CodeInvalidArguments Code = "tools/invalid-arguments"
	// CodeInvalidOutput: a body's value that is not of its declared output shape.
	CodeInvalidOutput Code = "tools/invalid-output"
	// CodeInvalidBatch: a batch without its view or log, or a call without its id.
	CodeInvalidBatch Code = "tools/invalid-batch"

	// CodeNotStarted: the call never started. Its name is not a tool of the
	// step's view (unknown, not admitted, or not reachable from the origin), or
	// the checkpoint before the body failed.
	CodeNotStarted Code = "tools/not-started"
	// CodeArgumentsTooLarge: argument bytes over the registry's bound.
	CodeArgumentsTooLarge Code = "tools/arguments-too-large"
	// CodeDenied: a policy listener or a guard refused the call.
	CodeDenied Code = "tools/denied"
	// CodeAbortedBeforeStart: a policy cancelled the call, or its context ended
	// before the body started.
	CodeAbortedBeforeStart Code = "tools/aborted-before-start"
	// CodeFailed: the body failed without a code of its own, or panicked; or a
	// listener of the call panicked.
	CodeFailed Code = "tools/failed"
	// CodeBlocked: a shaper blocked a successful result.
	CodeBlocked Code = "tools/blocked"
	// CodeTimeout: the call's budget ended and its body returned within grace.
	CodeTimeout Code = "tools/timeout"
	// CodeAborted: the turn was cancelled while the body ran, and the body
	// returned within grace.
	CodeAborted Code = "tools/aborted"
	// CodeOutcomeUnknown: the body did not return within grace after its deadline
	// or a cancellation; it is detached and may still be running.
	CodeOutcomeUnknown Code = "tools/outcome-unknown"
	// CodeBusy: a detached body of this tool, for this agent, is still running.
	CodeBusy Code = "tools/busy"
	// CodeApprovalRejected: a person rejected the call, or the session's
	// approval policy is never.
	CodeApprovalRejected Code = "approval/rejected"
	// CodeApprovalCancelled: the approval's context ended before it settled.
	CodeApprovalCancelled Code = "approval/cancelled"
	// CodeApprovalUnavailable: nobody could answer (no binding, no answerer, a
	// failed answerer, a set timeout, a stopping host) or the approval could not
	// be audited; no means no (SP-7).
	CodeApprovalUnavailable Code = "approval/unavailable"
)

// Error carries a Code, the tool it concerns and, for schema failures, every
// offending path; errors.Is matches a sentinel of the same code.
type Error struct {
	Code Code
	Tool string
	Msg  string
	// Issues lists every violation, each prefixed with its path.
	Issues []string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(string(e.Code))
	if e.Tool != "" {
		b.WriteString(": tool ")
		b.WriteString(e.Tool)
	}
	if e.Msg != "" {
		b.WriteString(": ")
		b.WriteString(e.Msg)
	}
	if len(e.Issues) > 0 {
		b.WriteString(": ")
		b.WriteString(strings.Join(e.Issues, "; "))
	}
	return b.String()
}

// Is matches a sentinel of the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Tool == "" && t.Msg == "" && len(t.Issues) == 0 && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrInvalidDeclaration = &Error{Code: CodeInvalidDeclaration}
	ErrDuplicate          = &Error{Code: CodeDuplicate}
	ErrUnscoped           = &Error{Code: CodeUnscoped}
	ErrActivationFailed   = &Error{Code: CodeActivationFailed}
	ErrInvalidArguments   = &Error{Code: CodeInvalidArguments}
	ErrInvalidOutput      = &Error{Code: CodeInvalidOutput}
	ErrInvalidBatch       = &Error{Code: CodeInvalidBatch}
	// ErrTimeout is matched by the cause of a body's context cancelled because
	// the call's budget ended (context.Cause).
	ErrTimeout = &Error{Code: CodeTimeout}
)
