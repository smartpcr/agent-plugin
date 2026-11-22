// This file is the lifecycle half of the contract (DH-D-AGENT-6): the optional
// interfaces through which the registry runs an instance's one teardown order
// (agent-loop.md §6.3, T1 to T8), and the reads and sends a live agent takes.

package agent

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strconv"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// TeardownHooks are the registry's parts of an instance's teardown.
type TeardownHooks struct {
	// JoinCreation (T2) returns once no Created dispatch of the agent runs.
	JoinCreation func()
	// Leave (T7) removes the agent from the registry and sends Disposed when
	// Created had begun. It is idempotent.
	Leave func()
}

// Teardowner is the optional teardown of an Instance: the one memoised order
// T1 to T8, calling the hooks at T2 and T7. The registry uses it in place of
// Close; whoever calls it first runs it and every other caller waits for it.
type Teardowner interface {
	Teardown(ctx context.Context, h TeardownHooks) error
}

// CoOwned is an Instance with a second owner: the registry ties its teardown
// to CoOwner's fiber as well as to the creator's, so whichever unloads first
// runs it. The driver co-owns every agent it built.
type CoOwned interface {
	CoOwner() *cordis.Context
}

// Activator is an Instance that goes live after Created (creation step 7):
// Activate starts its loop and releases the wakes held since step 3. An error
// tears the agent down.
type Activator interface {
	Activate() error
}

// Idler is an Instance whose loop can be waited on.
type Idler interface {
	WaitIdle(ctx context.Context) error
}

// Sender is an Instance that admits input.
type Sender interface {
	Send(ctx context.Context, list List, m InboxMessage) (Receipt, error)
}

// Runner is an Instance that reports when its loop has stopped.
type Runner interface {
	// Done is closed when the loop goroutine has exited; it is nil before
	// the loop started.
	Done() <-chan struct{}
}

func unwrap(a Agent) (Agent, *entry) {
	if v, ok := a.(view); ok {
		return v.a, v.e
	}
	return a, nil
}

// WaitIdle waits until a's loop is parked with nothing to run. An agent with
// no loop is idle. Called from inside a's own Created dispatch it fails at
// once with agent/self-wait.
func WaitIdle(ctx context.Context, a Agent) error {
	a, e := unwrap(a)
	if e != nil && e.selfWait() {
		return errorf(CodeSelfWait, "WaitIdle on %s from inside its own Created dispatch", e.ref)
	}
	if i, ok := a.(Idler); ok {
		return i.WaitIdle(ctx)
	}
	return nil
}

// Send queues m on a's list. A closing or disposed agent refuses with
// agent/closing; an agent that takes no input refuses with agent/invalid-options.
func Send(ctx context.Context, a Agent, list List, m InboxMessage) (Receipt, error) {
	a, _ = unwrap(a)
	if s, ok := a.(Sender); ok {
		return s.Send(ctx, list, m)
	}
	return Receipt{}, errorf(CodeInvalid, "%s takes no input", a.Ref())
}

// Done is closed when a's loop has stopped; nil for an agent with no loop or
// one not yet live.
func Done(a Agent) <-chan struct{} {
	a, _ = unwrap(a)
	if r, ok := a.(Runner); ok {
		return r.Done()
	}
	return nil
}

// FlushError is teardown's final flush failing or timing out (T6): events
// after Durable may be lost, and resume closes the turn. The lease is still
// released.
type FlushError struct {
	Durable session.Offset
	Err     error
}

func (e *FlushError) Error() string {
	return fmt.Sprintf("%s: the final flush failed; durable through %d: %v", CodeFlushFailed, e.Durable, e.Err)
}

func (e *FlushError) Unwrap() error { return e.Err }

// Is matches ErrFlushFailed.
func (e *FlushError) Is(target error) bool { return target == ErrFlushFailed }

// Closing is the agent/closing error for ref.
func Closing(ref Ref) error { return errorf(CodeClosing, "%s is closing", ref) }

// goroutineID is the calling goroutine's id, read from its stack header
// "goroutine N [...". ok is false when the header has another form.
func goroutineID() (uint64, bool) {
	var buf [64]byte
	rest, ok := bytes.CutPrefix(buf[:runtime.Stack(buf[:], false)], []byte("goroutine "))
	if !ok {
		return 0, false
	}
	digits, _, ok := bytes.Cut(rest, []byte(" ["))
	if !ok {
		return 0, false
	}
	id, err := strconv.ParseUint(string(digits), 10, 64)
	return id, err == nil && id != 0
}
