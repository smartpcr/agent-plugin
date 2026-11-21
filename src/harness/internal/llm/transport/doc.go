// Package transport is the HTTP transport every model driver shares
// (docs/harness/areas/llm.md DH-D-LLM-2).
//
// A Transport takes its client from util/egress under a permit of kind
// "model-request" (Kind): the client refuses redirects, adds only
// `User-Agent: dsh`, and opens no connection the permit does not name. Post sends
// one request built by a driver and returns the response of a 2xx status with its
// body wrapped in an idle timer; any other status becomes a *vocab.Failure built
// from the status, the headers (retry-after, request id) and the bounded error body,
// classified by the caller's Classifier. A transport failure (no permit, a dial
// error, a stalled body) is a *vocab.Failure too, so the runtime settles it as one.
//
// Events reads a text/event-stream body as the HTML standard defines it. The bytes
// come from a remote server, so the reader is bounded and never panics.
//
//dsh:untrusted-parser event streams arrive from model vendors and gateways
package transport
