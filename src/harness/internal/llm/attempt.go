package llm

import (
	"errors"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// Attempt is the one record of one attempt. The runtime fills it; the caller
// that owns the session appends it as exactly one settlement event (Draft).
type Attempt struct {
	Route vocab.RouteID
	Model string
	// Purpose is the auxiliary purpose of the request, "" for a loop step.
	Purpose string
	// RequestAsOf and RequestDigest attribute the attempt to the request it sent.
	RequestAsOf   event.Seq
	RequestDigest string
	// Message is the assembled assistant message: the whole answer on success,
	// the kept prefix of an interrupted attempt, nil when nothing was produced.
	Message *vocab.Message
	// Finish is the one Finish the stream ended with.
	Finish vocab.Finish
	Usage  *vocab.Usage
	Timing event.Timing
	// ServedBy names the llm.StreamAround listener that served the stream in
	// place of the provider; "" when the route's adapter served it.
	ServedBy string
	// Estimate is the token meter's heuristic price of the request sent
	// (meter.EstimateRequest), the denominator of the session's calibration.
	Estimate int
	// Client is the client invocation of a signed-in client route, from the
	// call's InvocationReporter; nil on every other route and for a served stream.
	Client *event.ClientInvocation
}

// InvocationReporter is a Call that runs a client program for its attempt (a
// signed-in client route, DH-D-LLM-12). After the attempt the runtime copies
// Invocation into the attempt record, so the settlement logs the
// client-invocation boundary (M1-D-PIPE-4 A). It is read once, after the
// attempt ended or was abandoned, and must return a copy that is safe to read
// while the attempt still unwinds; nil when nothing was started.
type InvocationReporter interface {
	Invocation() *event.ClientInvocation
}

// Succeeded is true when the attempt finished without a failure and produced a
// message: the attempt settles as assistant/message.
func (a Attempt) Succeeded() bool {
	if a.Finish.Failure != nil || a.Message == nil {
		return false
	}
	switch a.Finish.Reason {
	case vocab.FinishStop, vocab.FinishToolCalls, vocab.FinishMaxTokens:
		return true
	}
	return false
}

// Draft is the attempt's one settlement event: assistant/message when it
// succeeded, assistant/attempt (log-only, with the failure) otherwise. Both carry
// requestAsOf and requestDigest.
func (a Attempt) Draft() (event.Draft, error) {
	if a.RequestDigest == "" {
		return event.Draft{}, errors.New("llm: the attempt has no request digest")
	}
	asOf := a.RequestAsOf
	timing := a.Timing
	if a.Succeeded() {
		return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: *a.Message, RequestAsOf: &asOf, RequestDigest: a.RequestDigest,
			Usage: a.Usage, Timing: &timing, ServedBy: a.ServedBy, Estimate: a.Estimate, Client: a.Client,
		})
	}
	return event.AssistantAttempt.Draft(event.AssistantAttemptPayload{
		Message: a.Message, RequestAsOf: &asOf, RequestDigest: a.RequestDigest,
		Usage: a.Usage, Timing: &timing, Failure: a.Finish.Failure, ServedBy: a.ServedBy, Estimate: a.Estimate,
		Client: a.Client,
	})
}
