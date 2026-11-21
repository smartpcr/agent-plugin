// Package tools is the tool contract (DH-D-TOOLS-1, areas/tools.md §6.1): the one
// package other areas import to declare a tool or to read the tools one agent may
// use in one step.
//
// A tool is declared once, as one Go value:
//
//	var Echo = tools.Define(tools.Spec[EchoIn, EchoOut]{
//	    Name: "echo", Description: "Repeat the text.",
//	    Run:  func(ctx context.Context, in EchoIn, run tools.Run) (EchoOut, error) { ... },
//	    Overlap: func(in EchoIn) tools.Overlap { return tools.Free },
//	})
//
// Define derives the model-facing input schema and the output schema through
// package schema, so the schema sent to the model, the argument check and the
// catalogue entry cannot disagree. A declaration whose input leaves the
// model-facing subset carries the error with every offending path, and
// registering it fails.
//
// Declarations are registered with a Service (provided under Key by the
// tools.registry component, internal/tools/core) through a cordis context: the
// global layer for an untagged context, or one scope's layer for a scope's
// context (DH-D-SESSION-8). A scoped tool shadows a global one of the same name; a
// second tool of the same name in one layer is refused and the first remains. Once
// per proposed step the driver calls Resolve, which returns an immutable StepView.
//
// The body of a tool is reachable only from packages under internal/tools: the
// pipeline is the one way to run it (areas/tools.md §6.1, "nothing else can reach a
// tool body").
package tools
