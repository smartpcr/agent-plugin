package sandbox

import "strings"

// Launch is the plan the one process starter executes for one command. Only this
// package builds one; the zero Launch is no plan, and the starter refuses it, so a
// start request cannot leave the choice out by accident.
type Launch struct {
	trusted bool
	reason  string
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

// Valid reports whether l is a plan the starter may execute.
func (l Launch) Valid() bool { return l.trusted && l.reason != "" }

// IsTrusted reports whether l is a Trusted plan, whose tree is the only kind that
// is not an untrusted tree.
func (l Launch) IsTrusted() bool { return l.trusted }

// Reason is why a Trusted plan is trusted; empty for any other plan.
func (l Launch) Reason() string { return l.reason }

// String names the plan for a report.
func (l Launch) String() string {
	if !l.Valid() {
		return "no launch plan"
	}
	return "trusted (" + l.reason + ")"
}
