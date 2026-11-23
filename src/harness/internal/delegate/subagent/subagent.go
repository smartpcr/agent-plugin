package subagent

import (
	"context"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// SourceSubagent is the inbox source of a delegated task.
const SourceSubagent = "subagent"

// Spec is one delegation.
type Spec struct {
	// Name labels the child among its owner's children; "" is the session id.
	Name string
	// Session is the child's session id; "" mints one.
	Session session.ID
	// Preset is the preset the child is composed from, "" for none.
	Preset string
	// Creator is the agent delegating, zero for a component that is no agent.
	Creator agent.Ref
	// Prompt is the task, the child's first next-turn input; required.
	Prompt string
	// Model is the child's initial model selection, nil for the defaults.
	Model *agent.ModelSelection
	// Setup runs in the child's scope at creation (agent.CreateOptions.Setup).
	Setup agent.SetupFunc
	// Resume reopens the stored child session Session, which is then
	// required, instead of creating it (A4): the child starts with its queued
	// input, and Prompt is its next task. A start that meets a run of the
	// same child still releasing waits for that run to settle.
	Resume bool
}

// StopReason is how a run ended.
type StopReason string

// The stop reasons.
const (
	StopCompleted   StopReason = "completed"
	StopAborted     StopReason = "aborted"
	StopError       StopReason = "error"
	StopStartFailed StopReason = "start-failed"
)

// CleanupState is whether releasing the child succeeded.
type CleanupState string

// The cleanup states.
const (
	CleanupOK     CleanupState = "ok"
	CleanupFailed CleanupState = "failed"
)

// Result is a settled run; it exists only once the child was released.
type Result struct {
	Stop StopReason
	// Output is the last non-empty assistant text after the run's start.
	Output string
	// Diagnostic explains an error, an abort's cause or a failed cleanup.
	Diagnostic string
	Cleanup    CleanupState
}

// Activation is one child run.
type Activation interface {
	// Child is the child's session id; Name its label among the owner's children.
	Child() session.ID
	Name() string
	// Agent is the child agent; it gives no way to dispose it.
	Agent() agent.Agent
	// Result blocks until the run has settled, never before the child was
	// released.
	Result(ctx context.Context) (Result, error)
	// Dispose stops (cause parent) and releases exactly this run and returns
	// once it has settled.
	Dispose(ctx context.Context) error
}

// Runtime is the one delegation seam, the service behind Key.
type Runtime interface {
	// Start delegates s to a new child owned by owner and returns once the
	// child is published and its task accepted.
	Start(owner *cordis.Context, s Spec) (Activation, error)
	// OwnerOf is the one owner of an unsettled child.
	OwnerOf(child session.ID) (*cordis.Context, bool)
	// Children are owner's unsettled children, ordered by session id.
	Children(owner *cordis.Context) []session.ID
	// WaitQuiet returns once none of owner's children is working.
	WaitQuiet(ctx context.Context, owner *cordis.Context) error
}

// Key is the delegation runtime's service key.
var Key = cordis.NewServiceKey[Runtime]("subagent.manager")

// Code is a closed subagent error code.
type Code string

// The subagent error codes.
const (
	CodeInvalid   Code = "subagent/invalid-spec"
	CodeNameTaken Code = "subagent/name-taken"
	CodeInactive  Code = "subagent/owner-inactive"
)

// Error is a subagent error with a closed code.
type Error struct {
	Code Code
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return string(e.Code) + ": " + e.Msg + ": " + e.Err.Error()
	}
	return string(e.Code) + ": " + e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// Is matches a sentinel *Error by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Msg == "" && t.Err == nil
}

// Sentinels for errors.Is.
var (
	ErrInvalid   = &Error{Code: CodeInvalid}
	ErrNameTaken = &Error{Code: CodeNameTaken}
	ErrInactive  = &Error{Code: CodeInactive}
)

func errorf(c Code, err error, format string, args ...any) error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, args...), Err: err}
}
