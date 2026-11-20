package llm

import (
	"context"
	"errors"
	"iter"
	"slices"

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

// StreamListener is a listener of StreamAround. req is the listener's own copy;
// next runs the inner listeners and the terminal on the original request, so no
// listener can change what another listener, or the provider, sees.
type StreamListener func(ctx *cordis.Context, req StreamRequest, next func() (StreamDecision, error)) (StreamDecision, error)

// streamCall is the value the underlying around key carries. Its type is
// unexported, so only StreamAround's wrappers can listen on that key, and each
// hands its listener a fresh copy of the one original request.
type streamCall struct{ req StreamRequest }

func (c *streamCall) view() StreamRequest {
	r := c.req
	r.Config = vocab.CallConfig{Controls: c.req.Config.Controls.Clone(), Defaulted: slices.Clone(c.req.Config.Defaulted)}
	return r
}

// StreamAroundKey is the type of StreamAround.
type StreamAroundKey struct {
	key *cordis.AroundKey[*streamCall, StreamDecision]
}

// StreamAround is the around key every attempt passes, after the request is
// checked against the prepared call and before any provider I/O. A listener may
// Proceed or Serve; it cannot change the route or the request: neither decision
// carries one, next takes no request, and every listener sees the original.
var StreamAround = StreamAroundKey{key: cordis.NewAround[*streamCall, StreamDecision]("llm/stream")}

// Name is the key's event name.
func (k StreamAroundKey) Name() string { return k.key.Name() }

// On registers l as an effect of ctx's fiber; the first registered is outermost.
func (k StreamAroundKey) On(ctx *cordis.Context, l StreamListener, opts ...cordis.ListenOption) (*cordis.EffectHandle, error) {
	if l == nil {
		return nil, errors.New("llm: StreamAround.On needs a listener")
	}
	return k.key.On(ctx, wrap(l), opts...)
}

// Prepend registers l outside the listeners already registered.
func (k StreamAroundKey) Prepend(ctx *cordis.Context, l StreamListener, opts ...cordis.ListenOption) (*cordis.EffectHandle, error) {
	if l == nil {
		return nil, errors.New("llm: StreamAround.Prepend needs a listener")
	}
	return k.key.Prepend(ctx, wrap(l), opts...)
}

func wrap(l StreamListener) func(*cordis.Context, *streamCall, func(*streamCall) (StreamDecision, error)) (StreamDecision, error) {
	return func(c *cordis.Context, call *streamCall, next func(*streamCall) (StreamDecision, error)) (StreamDecision, error) {
		return l(c, call.view(), func() (StreamDecision, error) { return next(call) })
	}
}

// WithInterception dispatches StreamAround through host: the listeners visible
// from host see every attempt. Without it no attempt is intercepted.
func WithInterception(host *cordis.Context) Option { return func(rt *Runtime) { rt.host = host } }

// intercept asks StreamAround with ctx, the attempt's own context (cancelled when
// the attempt settles, so a served stream reading StreamRequest.Ctx ends with it); it returns the substituted stream and who serves
// it, nil for the provider, or the failure that settles the attempt.
func (p *prepared) intercept(ctx context.Context, req session.Request) (iter.Seq2[vocab.Chunk, error], string, *vocab.Failure) {
	if p.rt.host == nil {
		return nil, "", nil
	}
	call := &streamCall{req: StreamRequest{Ctx: ctx, Route: p.route.ID, Model: p.model, Config: p.Config(), Request: req}}
	d, err := StreamAround.key.Around(p.rt.host, call, func(got *streamCall) (StreamDecision, error) {
		if got != call { // unreachable through StreamAround's wrappers
			return nil, errors.New("a listener changed the request")
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
