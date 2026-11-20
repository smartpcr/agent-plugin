// Package llm is the model runtime of docs/harness/areas/llm.md (DH-D-LLM-1): one
// runtime for every model call, which routes, attempts and settles.
//
// A provider registers routes, each with an Adapter. Runtime.Prepare resolves a
// route, materialises the call's controls and validates them before any I/O;
// Prepared.Stream runs one provider attempt per call and normalises whatever the
// adapter does (an error, a panic, a stream that never finishes, chunks after the
// finish, a cancellation) into a stream that always ends with exactly one
// vocab.Finish and exactly one Attempt record. Runtime.Complete is the buffered
// form used by auxiliary calls.
//
// Every attempt passes the around key StreamAround (when the runtime was built
// WithInterception) after its request is checked and before any provider I/O. A
// listener answers Proceed or Serve{Stream, ServedBy}: a served stream replaces
// the provider (replay), is normalised and settled exactly like a provider's,
// takes no route slot, and its record names ServedBy. No decision carries a
// route, and a listener that hands next a changed route, model, configuration or
// request is refused, so interception cannot reroute (DH-T-LLM-4).
//
// The runtime holds no session writer (SP-1, SP-2). The Attempt record goes to
// the caller's AttemptSink, and the caller (the driver for a loop step, the
// auxiliary caller otherwise) appends it as exactly one settlement event,
// assistant/message or assistant/attempt, carrying requestAsOf and requestDigest;
// Attempt.Draft builds that event without writing it.
//
// Package llm imports session for the one request type, session.Request; session
// imports no llm package but llm/vocab.
package llm
