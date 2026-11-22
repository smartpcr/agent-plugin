// Package agent is the agent contract: the types every component uses to find,
// address and own agents, and the typed steering points of a turn. It holds no
// loop; the one driver (internal/agent/driver) fills the factory seat, and
// nothing else imports the driver.
package agent

import (
	"context"
	"fmt"
	"strconv"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// Ref names one live instance of an agent: its session and the incarnation the
// registry gave it. It is a plain value, so it crosses a worker, process or wire
// boundary and is resolved on arrival.
type Ref struct {
	Session     session.ID `json:"session"`
	Incarnation uint64     `json:"incarnation"`
}

// IsZero reports whether r names no agent.
func (r Ref) IsZero() bool { return r.Session == "" }

func (r Ref) String() string {
	return string(r.Session) + "#" + strconv.FormatUint(r.Incarnation, 10)
}

// Status is whether the agent's driver is running a turn.
type Status string

// The statuses.
const (
	StatusIdle    Status = "idle"
	StatusRunning Status = "running"
)

// State is the agent's lifecycle state.
type State string

// The states.
const (
	StateInitializing State = "initializing"
	StateLive         State = "live"
	StateClosing      State = "closing"
	StateDisposed     State = "disposed"
)

// Agent is the read and address face of one agent. It gives no way to dispose:
// only the Handle its creator holds does. The input, cancel, model and
// maintenance methods arrive with the driver (DH-D-AGENT-2, -3, -8).
type Agent interface {
	Ref() Ref
	// Session is read access; only the driver appends turn events.
	Session() session.ReadSession
	// Scope is the registration context of the agent's scope; it reaches no service.
	Scope() *cordis.Context
	// ScopeKey is the agent's scope key, for visibility only.
	ScopeKey() scope.Key
	Status() Status
	State() State
}

// Handle is what the creator holds: the agent and the right to dispose it.
type Handle interface {
	Agent() Agent
	// Dispose removes the agent from the registry and closes it. It is
	// idempotent; it also runs when the creator's fiber unloads.
	Dispose(ctx context.Context) error
}

// CreateOptions is what Create passes to the factory.
type CreateOptions struct {
	// Session is the agent's session id; required, and unique among live agents.
	Session session.ID
	// Preset is the preset id the agent is composed from, "" for none.
	Preset string
	// Parent is the agent that spawned this one, zero for a root agent.
	Parent Ref
	// Model is the agent's initial model selection, a blank Route for none; it
	// ranks below a recorded Select and the latest request header
	// (ModelOptions.Initial).
	Model ModelSelection
	// Setup runs at creation step 4 with the agent's scope context: what it
	// registers there (tools, prompt sections, listeners) lives exactly as
	// long as the agent. Its commit runs once Setup has returned. An error
	// from either rolls the creation back, undoing what Setup registered.
	Setup SetupFunc
	// Resume is set by Registry.Resume: the factory opens the stored session
	// (closing an interrupted turn) instead of creating it. Create clears it.
	Resume bool
}

// SetupFunc is CreateOptions.Setup. commit may be nil.
type SetupFunc func(scope *cordis.Context, a Agent) (commit func() error, err error)

// Instance is what a factory builds: the agent and its close.
type Instance interface {
	Agent
	Close(ctx context.Context) error
}

// StopCause is why an agent is asked to stop working on its session.
type StopCause string

// The stop causes.
const (
	// StopHidden: the agent's session was hidden (archived); a hidden session
	// never keeps working (DH-D-STORAGE-7).
	StopHidden StopCause = "hidden"
)

// Stopper is the optional stop of an Instance: it ends the agent's running turn
// and refuses new work, giving cause. The driver implements it (DH-D-AGENT-3);
// an instance without it has nothing to stop.
type Stopper interface {
	Stop(ctx context.Context, cause StopCause) error
}

// Stop asks a (an agent from the registry, or an instance) to stop for cause.
// An agent whose instance is no Stopper has no work to stop: nil.
func Stop(ctx context.Context, a Agent, cause StopCause) error {
	if v, ok := a.(view); ok {
		a = v.a
	}
	if s, ok := a.(Stopper); ok {
		return s.Stop(ctx, cause)
	}
	return nil
}

// Factory builds agents; the driver is the one shipped factory. ref is the
// reference the registry gives the new instance, and the instance must report it.
type Factory interface {
	Build(owner *cordis.Context, o CreateOptions, ref Ref) (Instance, error)
}

// FactoryFunc is a func Factory.
type FactoryFunc func(owner *cordis.Context, o CreateOptions, ref Ref) (Instance, error)

// Build calls f.
func (f FactoryFunc) Build(owner *cordis.Context, o CreateOptions, ref Ref) (Instance, error) {
	return f(owner, o, ref)
}

// Registry is the agent registry, the service behind Agents.
type Registry interface {
	// Create builds an agent through the factory seat, owned by owner: the
	// returned Handle alone disposes it, and it is disposed when owner's fiber
	// unloads.
	Create(owner *cordis.Context, o CreateOptions) (Handle, error)
	// Resume is Create for a stored session: the factory opens it, closing an
	// interrupted turn, and the agent starts idle with its queued input.
	Resume(owner *cordis.Context, o CreateOptions) (Handle, error)
	// Get is the live agent of a session.
	Get(id session.ID) (Agent, bool)
	// Resolve is the live agent r names; it fails for a disposed or replaced
	// incarnation.
	Resolve(r Ref) (Agent, error)
	// List is every live agent, ordered by session id.
	List() []Agent
	// SetFactory fills the factory seat as an effect of owner's fiber: the seat
	// empties when that fiber unloads. A second seat holder is refused.
	SetFactory(owner *cordis.Context, f Factory) (*cordis.EffectHandle, error)
}

// Agents is the registry's service key.
var Agents = cordis.NewServiceKey[Registry]("agent.agents")

// Code is a closed agent error code.
type Code string

// The agent error codes.
const (
	CodeInvalid         Code = "agent/invalid-options"
	CodeDuplicate       Code = "agent/duplicate"
	CodeNoFactory       Code = "agent/no-factory"
	CodeFactoryTaken    Code = "agent/factory-taken"
	CodeNotFound        Code = "agent/not-found"
	CodeStale           Code = "agent/stale-ref"
	CodeExtensionFailed Code = "extension/failed"
	// CodeClosing: the agent is being torn down and admits no input.
	CodeClosing Code = "agent/closing"
	// CodeSelfWait: WaitIdle or Dispose was called from inside the agent's
	// own Created dispatch, a wait that could never end.
	CodeSelfWait Code = "agent/self-wait"
	// CodeFlushFailed: teardown's final flush failed or timed out (FlushError).
	CodeFlushFailed Code = "session/flush-failed"
)

// Error is an agent error with a closed code.
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

// Is matches a sentinel *Error (one with only a code) by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Msg == "" && t.Err == nil
}

// Sentinels for errors.Is.
var (
	ErrInvalid         = &Error{Code: CodeInvalid}
	ErrDuplicate       = &Error{Code: CodeDuplicate}
	ErrNoFactory       = &Error{Code: CodeNoFactory}
	ErrFactoryTaken    = &Error{Code: CodeFactoryTaken}
	ErrNotFound        = &Error{Code: CodeNotFound}
	ErrStale           = &Error{Code: CodeStale}
	ErrExtensionFailed = &Error{Code: CodeExtensionFailed}
	ErrClosing         = &Error{Code: CodeClosing}
	ErrSelfWait        = &Error{Code: CodeSelfWait}
	ErrFlushFailed     = &Error{Code: CodeFlushFailed}
)

func errorf(c Code, format string, args ...any) error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, args...)}
}

type initiatorKey struct{}

// WithInitiator is ctx carrying r as the agent on whose behalf code runs. It
// proves neither liveness nor permission: Resolve it.
func WithInitiator(ctx context.Context, r Ref) context.Context {
	return context.WithValue(ctx, initiatorKey{}, r)
}

// Initiator is the agent ctx runs on behalf of.
func Initiator(ctx context.Context) (Ref, bool) {
	r, ok := ctx.Value(initiatorKey{}).(Ref)
	return r, ok && !r.IsZero()
}
