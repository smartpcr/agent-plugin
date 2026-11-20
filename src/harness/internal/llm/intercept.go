package llm

import (
	"context"
	"errors"
	"iter"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// StreamRequest is what a StreamAround listener sees of an attempt: the
// prepared call's route, model and effective configuration, and the request.
// It is a copy; changing it changes nothing the runtime sends.
type StreamRequest struct {
	// Ctx is the attempt's context.
	Ctx     context.Context
	Route   vocab.RouteID
	Model   string
	Config  vocab.CallConfig
	Request session.Request
}

// StreamDecision is Proceed or Serve.
type StreamDecision interface{ streamDecision() }

// Proceed sends the attempt to the prepared call's route.
type Proceed struct{}

// Serve substitutes the provider: Stream is read in its place (a replay, say)
// and ServedBy, which must be set, is named in the attempt record. The stream is
// normalised and settled exactly as a provider's is: it may end in a failure, a
// panic is llm/malformed-response and a missing Finish llm/stream-closed.
type Serve struct {
	Stream   iter.Seq2[vocab.Chunk, error]
	ServedBy string
}

func (Proceed) streamDecision() {}
func (Serve) streamDecision()   {}

// StreamAround is the around key every attempt passes, after the request is
// checked against the prepared call and before any provider I/O. A listener may
// Proceed or Serve; it cannot change the route or the request: neither decision
// carries one, and a listener that hands next a changed request is refused.
var StreamAround = cordis.NewAround[StreamRequest, StreamDecision]("llm/stream")

// WithInterception dispatches StreamAround through host: the listeners visible
// from host see every attempt. Without it no attempt is intercepted.
func WithInterception(host *cordis.Context) Option { return func(rt *Runtime) { rt.host = host } }

// intercept asks StreamAround; it returns the substituted stream and who serves
// it, nil for the provider, or the failure that settles the attempt.
func (p *prepared) intercept(ctx context.Context, req session.Request) (iter.Seq2[vocab.Chunk, error], string, *vocab.Failure) {
	if p.rt.host == nil {
		return nil, "", nil
	}
	sreq := StreamRequest{Ctx: ctx, Route: p.route.ID, Model: p.model, Config: p.Config(), Request: req}
	d, err := StreamAround.Around(p.rt.host, sreq, func(r StreamRequest) (StreamDecision, error) {
		if r.Route != p.route.ID || r.Model != p.model || !sameControls(r.Config.Controls, p.cfg.Controls) ||
			r.Request.Digest() != req.Digest() || r.Request.AsOf() != req.AsOf() {
			return nil, errors.New("a listener changed the route, model, configuration or request")
		}
		return Proceed{}, nil
	})
	if err != nil {
		return nil, "", failure(vocab.CodeInvalidRequest, "a listener of %s failed: %v", StreamAround.Name(), err)
	}
	switch d := d.(type) {
	case Proceed:
		return nil, "", nil
	case Serve:
		switch {
		case d.Stream == nil:
			return nil, "", failure(vocab.CodeInvalidRequest, "a listener of %s served no stream", StreamAround.Name())
		case d.ServedBy == "":
			return nil, "", failure(vocab.CodeInvalidRequest, "a listener of %s served a stream without naming who serves it", StreamAround.Name())
		}
		return d.Stream, d.ServedBy, nil
	}
	return nil, "", failure(vocab.CodeInvalidRequest, "a listener of %s returned no decision (%T)", StreamAround.Name(), d)
}
