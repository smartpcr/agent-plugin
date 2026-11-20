// Package vocab is the model vocabulary (DH-D-LLM-11, DH-ADR-LLM-1): messages,
// blocks and their sources, tool schemas, controls, the route id, the stream's
// chunks, failure classification, and the one canonical JSON encoding a request
// digest is computed over.
//
// It is a leaf: it imports no harness package but internal/schema, so the
// session package, which constructs every model request, can import it without
// a cycle, and package llm re-exports each type as an alias. It holds no request
// type: session.Request is the only one (RM-6, DH-ADR-SESSION-7), which a rule
// in internal/archtest enforces.
//
// Every set here is closed except a message's Source kind. A message is built
// only by its constructors, which mint its id and copy its content; the other
// types are plain values whose Validate method checks the closed sets.
package vocab
