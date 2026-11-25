package sandbox

// The launch half of the seam (A10, areas/sandbox-ssh.md sec. 6.1): the type
// Launch, which world/subprocess requires on every Spec (DH-D-EXEC-4); Trusted,
// the plan for the host's own children; Untrusted, the reported unconfined plan
// whose tree is untrusted (gap-closure step 19.4.4); Helper, the unconfined plan
// of a helper whose Policy ForHelper issued; and Staged, the plan that runs a
// launch stage, whose Stage the starter drives: Wrap, start, Confirm, Release, in
// that order (C38). The Confinement a confirmed plan reports travels on the
// handle. DH-S-13.3 adds sandbox.Service.Prepare, the one maker of a staged plan
// for a Policy, and the starter's refusal of Trusted when an agent initiated the
// start (DH-T-EXEC-8).

import (
	"context"
	"os"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// State is how far a command is confined; the zero State is not a state.
type State string

// The three states.
const (
	// StateFull: every effect the policy promises is governed.
	StateFull State = "full"
	// StatePartial: confined, but some effects are not governed (Missing).
	StatePartial State = "partial"
	// StateUnconfined: nothing is confined (danger-full-access, or a host child).
	StateUnconfined State = "unconfined"
)

// Confinement is what the host computed for one start when it confirmed the plan,
// never what the stage asserted (architecture.md sec. 4.2).
type Confinement struct {
	State   State
	Backend Backend
	// Missing are the promised effects the start does not govern.
	Missing []Effect
	// Exposed are effects reachable though not promised (delegated-execution).
	Exposed []Effect
	// Scratch is the start's scratch directory, if it has one.
	Scratch string
}

// Valid reports whether c is a confinement a start may run under: a known state
// and backend, and backend none exactly when unconfined.
func (c Confinement) Valid() bool {
	switch c.State {
	case StateFull, StatePartial:
		return c.Backend.Valid() && c.Backend != BackendNone
	case StateUnconfined:
		return c.Backend == BackendNone
	}
	return false
}

// Clone is a copy of c that shares no slice with it.
func (c Confinement) Clone() Confinement {
	c.Missing, c.Exposed = slices.Clone(c.Missing), slices.Clone(c.Exposed)
	return c
}

// unconfinedConfinement is what a plan without a stage reports.
var unconfinedConfinement = Confinement{State: StateUnconfined, Backend: BackendNone}

// Wrapped is the command the starter starts in place of a staged plan's command:
// the stage's argument list (Argv[0] absolute) and the environment entries the
// plan adds to the caller's.
type Wrapped struct {
	Argv []string
	Env  []string
}

// Pass hands f to the child beside its standard streams and returns the number
// the child sees it under (subprocess.ChildFD).
type Pass func(f *os.File) uintptr

// Stage is the part of a staged plan the one starter drives (sandbox-ssh.md sec.
// 6.1, "How a command starts"). The starter calls each method once, in order:
// Wrap; then, when what Wrap returned has started, Confirm; and Release in every
// case, also when Wrap or the start failed. An error from Confirm means the
// command never ran; the starter then stops the stage's tree.
type Stage interface {
	// Wrap returns what to start for argv, the command: the stage and its plan.
	// pass hands the stage the child's ends of its channel.
	Wrap(argv []string, pass Pass) (Wrapped, error)
	// Confirm reads the stage's ready, takes the host's decision on it and sends
	// go or closes the channel. It returns once the command has started under the
	// returned Confinement, or with the error that kept it from running.
	Confirm(ctx context.Context) (Confinement, error)
	// Release frees what the plan holds on the host for this start.
	Release(ctx context.Context) error
}

// Launch is the plan the one process starter executes for one command. Only this
// package builds one; the zero Launch is no plan, and the starter refuses it, so a
// start request cannot leave the choice out by accident.
type Launch struct {
	trusted bool
	// unconfined marks Untrusted's and Helper's plans: valid, run without
	// confinement, and the tree untrusted.
	unconfined bool
	reason     string
	// stage is a staged plan's stage; policy the Policy a Helper or staged plan
	// was made for.
	stage  Stage
	policy Policy
}

// Trusted is the plan for the host's own child: a program that reads nothing the
// model can write (the ssh client, the browser opener, the keychain helper, a
// test fixture). reason says why the child is trusted and is reported with it; an
// empty reason gives the zero Launch, which the starter refuses.
func Trusted(reason string) Launch {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Launch{}
	}
	return Launch{trusted: true, reason: reason}
}

// Untrusted is the reported unconfined plan of a child the model can reach: a
// helper that reads what the model can write, or an operator hook with
// "confine: none" (sandbox-ssh.md). It runs without confinement and its tree is
// an untrusted tree, never a Trusted one. reason says why it is unconfined; an
// empty reason gives the zero Launch. It is the smallest untrusted plan until
// DH-S-13's Prepare builds confined ones (gap-closure step 19.4.4).
func Untrusted(reason string) Launch {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return Launch{}
	}
	return Launch{unconfined: true, reason: reason}
}

// Helper is the explicit unconfined plan of a helper child (a signed-in model
// client, a converter) whose Policy ForHelper issued for an unconfined helper
// kind: p must be a danger-full-access policy. A confined helper policy needs a
// confining plan, which only the seam's Prepare makes, and is refused with
// sandbox/unavailable; a policy that requires full enforcement is refused with
// sandbox/enforcement-insufficient, since an unconfined plan never gives it. The
// plan's tree is untrusted and it reports Unconfined.
func Helper(p Policy, kind string) (Launch, error) {
	if err := p.Validate(); err != nil {
		return Launch{}, err
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return Launch{}, herr.New(CodeUnavailable, "a helper plan must name its kind")
	}
	if p.Mode() != DangerFullAccess {
		return Launch{}, herr.Newf(CodeUnavailable, "helper %s: a %s policy needs a confining plan, which only the sandbox seam prepares", kind, p.Mode())
	}
	if p.Require() == RequireFull {
		return Launch{}, herr.Newf(CodeEnforcementInsufficient, "helper %s: the policy requires full enforcement and an unconfined plan gives none", kind)
	}
	return Launch{unconfined: true, reason: "helper " + kind + ", unconfined by its policy", policy: p}, nil
}

// Staged is the plan that runs the launch stage s for the command, made for
// policy p (world/sandbox/launch.NewLaunch; DH-S-13.3's Prepare). reason names
// the plan in a report. A nil s, an empty reason or a policy no resolver issued
// gives the zero Launch, which the starter refuses.
func Staged(p Policy, s Stage, reason string) Launch {
	reason = strings.TrimSpace(reason)
	if s == nil || reason == "" || p.IsZero() {
		return Launch{}
	}
	return Launch{stage: s, reason: reason, policy: p}
}

// Valid reports whether l is a plan the starter may execute.
func (l Launch) Valid() bool {
	return (l.trusted || l.unconfined || l.stage != nil) && l.reason != ""
}

// IsTrusted reports whether l is a Trusted plan, whose tree is the only kind that
// is not an untrusted tree.
func (l Launch) IsTrusted() bool { return l.trusted }

// Stage is a staged plan's stage, which the starter drives; nil for a plan that
// starts the command itself (Trusted, Untrusted, Helper).
func (l Launch) Stage() Stage { return l.stage }

// Policy is the Policy a Helper or staged plan was made for; the zero Policy for
// Trusted and Untrusted plans.
func (l Launch) Policy() Policy { return l.policy }

// Confinement is what a plan that starts the command itself reports: unconfined,
// backend none. A staged plan's Confinement is the one its Confirm returned, on
// the handle; for it, and for no plan, this is the zero Confinement.
func (l Launch) Confinement() Confinement {
	if !l.Valid() || l.stage != nil {
		return Confinement{}
	}
	return unconfinedConfinement
}

// Reason is why a Trusted plan is trusted, an Untrusted or Helper one
// unconfined, or what a staged one is; empty for the zero Launch.
func (l Launch) Reason() string { return l.reason }

// String names the plan for a report.
func (l Launch) String() string {
	switch {
	case !l.Valid():
		return "no launch plan"
	case l.stage != nil:
		return "staged (" + l.reason + ")"
	case l.unconfined:
		return "unconfined, untrusted (" + l.reason + ")"
	}
	return "trusted (" + l.reason + ")"
}
