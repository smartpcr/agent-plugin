// Package checkpoint is the durability checkpoint of docs/harness/areas/
// session-store.md DH-D-STORE-4 (component session.checkpoint, SP-8): no model
// request or tool side effect happens without a durable record of what caused
// it.
//
// The owners of three points call it, never as an optional listener: the driver
// before a provider attempt starts and at each step end, and the tool pipeline
// before a root call's body, after policy, guards and approval. A nested call
// adds no flush; it rides on its outer call's checkpoint. The Default policy
// flushes the session at every point; a composition may mount another schedule
// under Key, and with none mounted the callers still flush, so the default fails
// closed. A failed checkpoint is returned and the work does not run.
package checkpoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// Point is where a checkpoint is taken.
type Point string

// The three points.
const (
	// Attempt precedes the start of a provider attempt.
	Attempt Point = "attempt"
	// ToolBody precedes the body of a root tool call.
	ToolBody Point = "tool-body"
	// StepEnd follows the step/end of a step.
	StepEnd Point = "step-end"
)

// The codes of a failed checkpoint. At ToolBody they are the tool outcome codes
// of docs/harness/areas/tools.md: a failed flush is tools/not-started, a
// cancellation during it tools/aborted-before-start; both mean the body never
// ran and the call is safe to retry.
const (
	CodeFailed             event.Code = "session/checkpoint-failed"
	CodeNotStarted         event.Code = event.Code(surface.CodeToolNotStarted)
	CodeAbortedBeforeStart event.Code = "tools/aborted-before-start"
)

// Session is what a checkpoint flushes; session.Session satisfies it.
type Session interface {
	Flush(ctx context.Context) (event.Offset, error)
}

// Policy decides what a checkpoint does at a point. Before returning nil
// promises that the work after the point may run.
type Policy interface {
	Before(ctx context.Context, s Session, p Point) error
}

// Default flushes the session at every point.
type Default struct{}

// Before flushes s.
func (Default) Before(ctx context.Context, s Session, _ Point) error {
	_, err := s.Flush(ctx)
	return err
}

// Key is the service a composition mounts another Policy under.
var Key = cordis.NewServiceKey[Policy]("session.checkpoint")

// Mounted is the policy mounted on c, or Default when none is (or c is nil).
func Mounted(c *cordis.Context) Policy {
	if c != nil {
		if p, ok := Key.Lookup(c); ok && p != nil {
			return p
		}
	}
	return Default{}
}

// Error is a failed checkpoint: the work after Point did not run.
type Error struct {
	Point Point
	Code  event.Code
	Err   error
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: checkpoint before %s failed: %v", e.Code, e.Point, e.Err)
}

// Unwrap is the policy's error.
func (e *Error) Unwrap() error { return e.Err }

// Is matches the sentinel of the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*event.Error)
	return ok && t.Msg == "" && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrFailed             = &event.Error{Code: CodeFailed}
	ErrNotStarted         = &event.Error{Code: CodeNotStarted}
	ErrAbortedBeforeStart = &event.Error{Code: CodeAbortedBeforeStart}
)

// Before takes the checkpoint at p through policy (Default when nil). A failure
// is an *Error whose code says whether the work may be retried.
func Before(ctx context.Context, policy Policy, s Session, p Point) error {
	if policy == nil {
		policy = Default{}
	}
	err := ctx.Err()
	if err == nil {
		err = policy.Before(ctx, s, p)
	}
	// A cancellation that arrived during a successful flush still stops the work.
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		return nil
	}
	code := CodeFailed
	if p == ToolBody {
		code = CodeNotStarted
		if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = CodeAbortedBeforeStart
		}
	}
	return &Error{Point: p, Code: code, Err: err}
}

// Run takes the checkpoint at p and then runs body; when the checkpoint fails,
// body is never called and the checkpoint's error is returned.
func Run(ctx context.Context, policy Policy, s Session, p Point, body func(context.Context) error) error {
	if err := Before(ctx, policy, s, p); err != nil {
		return err
	}
	return body(ctx)
}

// RunTool runs a tool call's body behind the ToolBody checkpoint. A nested call
// (one a running body started) takes none: its outer call's checkpoint covers
// it.
func RunTool(ctx context.Context, policy Policy, s Session, nested bool, body func(context.Context) error) error {
	if nested {
		return body(ctx)
	}
	return Run(ctx, policy, s, ToolBody, body)
}
