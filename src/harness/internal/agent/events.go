package agent

import (
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// Every event about one agent is dispatched through that agent's scope filter: a
// listener registered through the agent's scope (or one enclosing it) sees only
// that agent's events; one registered untagged or with cordis.Global() sees all.

// CreatedNotice is sent while an agent is created; the first listener error fails
// the creation and rolls it back.
type CreatedNotice struct{ Agent Agent }

// DisposedNotice is sent after the registry entry is removed.
type DisposedNotice struct{ Ref Ref }

// StatusNotice is sent when an agent's status changes.
type StatusNotice struct {
	Agent  Ref
	Status Status
}

// FailedNotice reports a failure of a turn, also one that has no place in the log.
type FailedNotice struct {
	Agent      Ref
	Turn, Step int
	Err        error
}

// StepNotice is the observe-only outcome of point 4: the step was entered (Seq is
// its step/start) or the turn ended without it (Reason, Seq its turn/end).
type StepNotice struct {
	Agent      Ref
	Turn, Step int
	Entered    bool
	Reason     event.Reason
	Seq        event.Seq
}

// The lifecycle events. Created is ordered (Emit); the rest are observe-only
// (Notify): an observer's error or panic is logged and never changes an outcome.
var (
	Created       = scope.NewEvent[CreatedNotice]("agent/created")
	Disposed      = scope.NewEvent[DisposedNotice]("agent/disposed")
	StatusChanged = scope.NewEvent[StatusNotice]("agent/status")
	Failed        = scope.NewEvent[FailedNotice]("agent/error")
	StepDecided   = scope.NewEvent[StepNotice]("agent/step-decided")
)

// Notify sends an observe-only notice about a through its scope filter. The
// observers' failures are contained and logged on their own loggers.
func Notify[A any](ctx *cordis.Context, ev *scope.Event[A], a Agent, args A) {
	ev.Notify(ctx, a.ScopeKey(), args)
}
