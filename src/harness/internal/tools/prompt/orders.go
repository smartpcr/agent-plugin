package prompt

// The named first-party section orders, in one file (DH-D-TOOLS-5): sections are
// joined by Order, then by name, so a contributor picks its place from this table
// instead of inventing a number.
const (
	// OrderIdentity is the harness identity line.
	OrderIdentity = 0
	// OrderPersonaPrefix is the operator's persona text before everything else.
	OrderPersonaPrefix = 100
	// OrderHarness is first-party guidance on how the harness works.
	OrderHarness = 200
	// OrderTools is guidance on using the tools of the step.
	OrderTools = 400
	// OrderEnvironment is facts about the workspace that do not change per step.
	OrderEnvironment = 600
	// OrderProject is project instructions (an AGENTS.md and the like).
	OrderProject = 800
	// OrderPersonaSuffix is the operator's persona text after everything else.
	OrderPersonaSuffix = 1000
)

// The built-in sections the prompt component contributes from its config.
const (
	SectionIdentity      = "identity"
	SectionPersonaPrefix = "persona-prefix"
	SectionPersonaSuffix = "persona-suffix"
)

// IdentityLine is the fixed harness identity line (includeHarnessIdentity).
const IdentityLine = "You are dsh, the DeepSeek harness: an agent that works in the user's workspace through the tools listed for this step."
