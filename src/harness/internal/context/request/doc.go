// Package request is DH-D-CONTEXT-1 (component C30, context-compaction-skill.md
// §6.1, where the package is called context/inject): context reaches the model
// through one logged, ordered pipeline.
//
// A Pipeline is the one agent.PreStep listener of the context area. It calls
// downstream first; when the step is entered it runs the compaction check
// (Options.Compact, a Considerer: nil at M1 by decision M1-D-LOOP-4), then
// its contributors against a read-only view of the agent's current surface
// read after that check, so a baseline compaction shadowed is entered again
// in the same step. Contributors run in the fixed order of one table (Order),
// whatever order they registered in, and their outputs merge by that order
// into Enter.Messages: a contributor's After messages directly after the
// claimed message they follow, its Reword in place of a claimed message's
// text, its Messages after the claimed batch (see Output). The driver logs every
// entered message as user/message before it builds the step's request, so
// every contribution is in the log with its observation (the message source)
// and a request rebuilt from the log at its requestAsOf equals the one sent.
//
// Contributors are layered by scope (session/scope): one registered in a
// preset's scope serves only the agents that scope encloses, the nearest
// scope's contributor of a kind wins, and a global one serves every agent
// (DH-ADR-CONTEXT-1). Options.Scopes names each agent's scope.
//
// A contributor observes the world once and returns user-role messages whose
// source kind is its name; it never appends. A failing or panicking optional
// contributor is skipped for the step and recorded as the ignorable,
// log-only context/skipped; a failing required one rejects the step, so the
// turn ends blocked with the contributor's name. A downstream Reject enters
// nothing and runs no contributor.
//
// The directory internal/context holds no Go package, so nothing shadows the
// standard context package (RM-8); the archtest rules enforce it, and that no
// package under internal/context imports the agent driver or a model provider.
package request
