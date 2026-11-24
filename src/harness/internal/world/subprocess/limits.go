package subprocess

import (
	"strings"
)

// Limits are optional caps on a tree (Spec.Limits, step 19.4.5), carried by its
// owner: the job object's memory and active-process limits on Windows, the
// control group's memory.max and pids.max on Linux where those controllers are
// delegated, nothing on the process-group rung. A zero field is no cap.
type Limits struct {
	// MemoryBytes caps the memory the whole tree commits (Windows) or charges
	// (Linux memory.max).
	MemoryBytes int64
	// Processes caps the members alive at once. On Linux pids.max counts
	// threads, so a multithreaded program needs room for its threads.
	Processes int
}

// IsZero reports whether no cap is asked for.
func (l Limits) IsZero() bool { return l.MemoryBytes <= 0 && l.Processes <= 0 }

// LimitsState says what a tree owner did with the Limits.
type LimitsState string

// The limits outcomes.
const (
	// LimitsNone: no cap was asked for.
	LimitsNone LimitsState = "none"
	// LimitsEnforced: every cap asked for is applied.
	LimitsEnforced LimitsState = "enforced"
	// LimitsPartial: some caps are applied, Detail names the others.
	LimitsPartial LimitsState = "partial"
	// LimitsUnavailable: no cap could be applied; Detail says why.
	LimitsUnavailable LimitsState = "unavailable"
)

// LimitsReport is the outcome of a tree's Limits.
type LimitsReport struct {
	State LimitsState
	// Detail names what was applied and why anything was not.
	Detail string
}

func (r LimitsReport) String() string {
	if r.Detail == "" {
		return string(r.State)
	}
	return string(r.State) + " (" + r.Detail + ")"
}

// Limits reports what the tree's owner did with Spec.Limits.
func (p *Process) Limits() LimitsReport { return p.limits }

// limitsOf sums up the caps applied and the ones that were not.
func limitsOf(l Limits, applied, missing []string) LimitsReport {
	switch {
	case l.IsZero():
		return LimitsReport{State: LimitsNone}
	case len(missing) == 0:
		return LimitsReport{State: LimitsEnforced, Detail: strings.Join(applied, ", ")}
	case len(applied) == 0:
		return LimitsReport{State: LimitsUnavailable, Detail: strings.Join(missing, "; ")}
	}
	return LimitsReport{State: LimitsPartial, Detail: strings.Join(applied, ", ") + "; " + strings.Join(missing, "; ")}
}

// groupLimits is the process-group rung's report: it holds no limits.
func groupLimits(l Limits) LimitsReport {
	if l.IsZero() {
		return LimitsReport{State: LimitsNone}
	}
	return LimitsReport{State: LimitsUnavailable, Detail: "the process-group rung applies no limits"}
}
