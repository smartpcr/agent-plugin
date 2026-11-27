package sandbox

// The service half of the seam (C37, DH-D-SANDBOX-3; sandbox-ssh.md sec. 6.1):
// the effects a policy promises, the one function that computes enforcement from
// evidence (DH-ADR-SANDBOX-3), the per-backend Status and the Service interface.
// The implementation for world local is world/sandbox/local.

import (
	"context"
	"slices"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
)

// The effects a confined policy can promise. The list is maintained by hand and
// reviewed when a backend or kernel changes (DH-ADR-SANDBOX-3).
const (
	// EffectFSWrite: writes, deletes, renames and truncation outside the write
	// roots are refused.
	EffectFSWrite Effect = "fs-write"
	// EffectNetwork: outbound network is refused; promised only by a policy
	// whose network setting is denied.
	EffectNetwork Effect = "network"
	// EffectHarnessHome: the harness home is not writable.
	EffectHarnessHome Effect = "harness-home"
	// EffectDelegatedExecution: no same-user service runs a program on the
	// command's behalf (a session bus, a task scheduler).
	EffectDelegatedExecution Effect = "delegated-execution"
)

// Effects lists every effect, in report order.
var Effects = []Effect{EffectFSWrite, EffectNetwork, EffectHarnessHome, EffectDelegatedExecution}

// Promised is what p promises to govern, in report order: nothing for the zero
// Policy and for danger-full-access; otherwise file writes, the harness home and
// delegated execution, and the network when p denies it.
func Promised(p Policy) []Effect {
	if p.IsZero() || p.Mode() == DangerFullAccess {
		return nil
	}
	out := []Effect{EffectFSWrite}
	if !p.Network() {
		out = append(out, EffectNetwork)
	}
	return append(out, EffectHarnessHome, EffectDelegatedExecution)
}

// Evidence is what the host knows about one start when it decides: the backend
// of the plan, whether it is a custom runner, the effects the start-up probe
// showed governed, the effects this start's ready names, the exposures the
// probe reported and the start's scratch directory.
type Evidence struct {
	Backend Backend
	// Custom marks an operator's runner: what it governs is never known well
	// enough to be Full.
	Custom bool
	// Probe and Start are the governed effects of the start-up probe and of this
	// start's ready. nil is no evidence: nothing governed.
	Probe, Start []Effect
	// Exposed are effects reachable though not promised, as probed.
	Exposed []Effect
	Scratch string
}

// Enforce is the one function that computes enforcement (DH-ADR-SANDBOX-3, INV-3).
// An effect counts as governed only when both the start-up probe and this start's
// ready name it (the weaker evidence wins; unknown is not governed). A confined
// policy is Full when every promised effect is governed and the backend is not a
// custom runner, Partial otherwise, naming each missing effect. danger-full-access
// under backend none is Unconfined with every effect missing. A policy no
// resolver issued, a confined policy under backend none, or danger-full-access
// under a confining backend gives the zero Confinement, which no start may run
// under. Delegated execution is governed only when the stage observed that no
// session bus or service manager is reachable; otherwise the start is Partial
// naming it (S13 default, I2 Q4: partial when delegated execution cannot be
// stopped).
func Enforce(p Policy, e Evidence) Confinement {
	if p.IsZero() || !e.Backend.Valid() {
		return Confinement{}
	}
	if p.Mode() == DangerFullAccess {
		if e.Backend != BackendNone {
			return Confinement{}
		}
		return Confinement{State: StateUnconfined, Backend: BackendNone, Missing: slices.Clone(Effects)}
	}
	if e.Backend == BackendNone {
		return Confinement{}
	}
	c := Confinement{State: StateFull, Backend: e.Backend, Scratch: e.Scratch}
	for _, want := range Promised(p) {
		if !slices.Contains(e.Probe, want) || !slices.Contains(e.Start, want) {
			c.Missing = append(c.Missing, want)
		}
	}
	for _, x := range e.Exposed {
		if !slices.Contains(c.Exposed, x) && !slices.Contains(c.Missing, x) {
			c.Exposed = append(c.Exposed, x)
		}
	}
	if len(c.Missing) > 0 || e.Custom {
		c.State = StatePartial
	}
	return c
}

// Expect is the confinement a workspace-write start with the network denied gets
// now, judged from the probe statuses alone (`dsh sandbox status`, C37): the first
// available backend in chain order is the one selected, and its probe is both the
// probe's and the start's evidence, so a real start can only be weaker. With no
// available backend a confined start is refused and only danger-full-access runs:
// Unconfined under backend none with every effect missing.
func Expect(statuses []Status) Confinement {
	for _, s := range statuses {
		if !s.Available {
			continue
		}
		p, err := Issue(mint.Spec{Mode: string(WorkspaceWrite)})
		if err != nil {
			return Confinement{}
		}
		return Enforce(p, Evidence{Backend: s.Backend, Custom: s.Custom, Probe: s.Governed, Start: s.Governed, Exposed: s.Exposed})
	}
	return Confinement{State: StateUnconfined, Backend: BackendNone, Missing: slices.Clone(Effects)}
}

// Status is one backend as the last probe found it (Describe, Reprobe).
type Status struct {
	Backend Backend
	// Custom marks an operator's runner.
	Custom bool
	// Available is true when the probe passed: no error and the canary refused.
	Available bool
	// Reason says why the backend is unavailable; empty when it is available.
	Reason string
	// Canary is the probe's canary result ("refused", "written", "not-tried"),
	// empty when the probe ended before one.
	Canary string
	// Governed and Exposed are the effects the probe showed governed and
	// reachable though not promised.
	Governed, Exposed []Effect
}

// Clone is a copy of s that shares no slice with it.
func (s Status) Clone() Status {
	s.Governed, s.Exposed = slices.Clone(s.Governed), slices.Clone(s.Exposed)
	return s
}

// Service is the sandbox seam (C37): it probes the backends at start-up, selects
// one and is the only maker of a plan for a Policy.
type Service interface {
	// Describe answers from the last probe, one Status per backend in chain order.
	Describe(ctx context.Context) []Status
	// Reprobe probes every backend again and returns the new statuses.
	Reprobe(ctx context.Context) []Status
	// Prepare fixes the plan for argv under p: confined under the selected
	// backend for read-only and workspace-write, unconfined for
	// danger-full-access. A confined policy with no backend that passed the probe
	// is refused with sandbox/unavailable and nothing starts.
	Prepare(ctx context.Context, p Policy, argv []string) (Launch, error)
}

// ServiceKey is the service the sandbox seam provides.
var ServiceKey = cordis.NewServiceKey[Service]("sandbox")
