package sandbox

// The launch half of the seam (A10, areas/sandbox-ssh.md sec. 6.1), as far as the one
// process starter needs it before DH-S-13 (gap-closure plan step 19.3.6): the type
// Launch, which world/subprocess requires on every Spec; Trusted, the plan for the
// host's own children; and Untrusted, the reported unconfined plan whose tree is
// untrusted (step 19.4.4). DH-S-13 adds sandbox.Service.Prepare, which builds a
// confined Launch from a Policy, the plan's Wrap, Confirm and Release, and the
// starter's refusal of Trusted when an agent initiated the start (DH-T-EXEC-8).

import "strings"

// Launch is the plan the one process starter executes for one command. Only this
// package builds one; the zero Launch is no plan, and the starter refuses it, so a
// start request cannot leave the choice out by accident.
type Launch struct {
	trusted bool
	// unconfined marks Untrusted's plan: valid, run without confinement, and
	// its tree untrusted.
	unconfined bool
	reason     string
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

// Valid reports whether l is a plan the starter may execute.
func (l Launch) Valid() bool { return (l.trusted || l.unconfined) && l.reason != "" }

// IsTrusted reports whether l is a Trusted plan, whose tree is the only kind that
// is not an untrusted tree.
func (l Launch) IsTrusted() bool { return l.trusted }

// Reason is why a Trusted plan is trusted, or an Untrusted one unconfined; empty
// for the zero Launch.
func (l Launch) Reason() string { return l.reason }

// String names the plan for a report.
func (l Launch) String() string {
	switch {
	case !l.Valid():
		return "no launch plan"
	case l.unconfined:
		return "unconfined, untrusted (" + l.reason + ")"
	}
	return "trusted (" + l.reason + ")"
}
