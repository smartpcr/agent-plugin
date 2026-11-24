// Package mint holds the one input from which a sandbox.Policy is issued. It is
// internal to world/sandbox, so only the resolver (world/sandbox/policy) and the
// fake resolver (world/sandbox/sandboxtest) can name Spec and so call
// sandbox.Issue: a Policy is obtained from a resolver, never built by its user
// (DH-D-SANDBOX-1, architecture.md sec. 4.2).
package mint

// Spec is what a resolver decided for one call. Strings keep this package free
// of world/sandbox's types; sandbox.Issue validates every field.
type Spec struct {
	// Mode is "read-only", "workspace-write" or "danger-full-access".
	Mode string
	// Roots are the write roots; only workspace-write may have any.
	Roots []Root
	// Network reports whether outbound network is an allowed effect.
	Network bool
	// Require is "full" or "any"; empty means "any".
	Require string
	// TextVersion is the version of the model-visible policy text, if any.
	TextVersion string
}

// Root is one write root and why it is writable.
type Root struct {
	Path   string
	Source string
}
