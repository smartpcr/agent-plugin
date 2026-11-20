package llm

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Option configures a Runtime.
type Option func(*Runtime)

// WithClock sets the clock attempt timings are read from, in Unix milliseconds.
func WithClock(clock func() int64) Option { return func(rt *Runtime) { rt.clock = clock } }

// New is a runtime with no routes.
func New(opts ...Option) *Runtime {
	rt := &Runtime{routes: map[vocab.RouteID]*entry{}, clock: func() int64 { return time.Now().UnixMilli() }}
	for _, o := range opts {
		o(rt)
	}
	return rt
}

// AttemptSink receives the one attempt record of an attempt. The caller that
// owns the sink appends it as one settlement event (Attempt.Draft); the runtime
// never writes a session.
type AttemptSink interface {
	Settle(a Attempt) error
}

// AttemptSinkFunc is a func AttemptSink.
type AttemptSinkFunc func(a Attempt) error

// Settle calls f.
func (f AttemptSinkFunc) Settle(a Attempt) error { return f(a) }

// Prepared is a captured call: what the driver holds. The adapter's Call is not
// reachable through it.
type Prepared interface {
	Route() vocab.RouteID
	Model() string
	// Config is the effective controls, the adapter's defaults materialised.
	Config() vocab.CallConfig
	// Stream runs one attempt. It always ends with exactly one Finish chunk
	// (unless the consumer stops reading first) and hands exactly one Attempt
	// record to sink after it; an adapter error, panic, missing finish or a
	// cancellation becomes that Finish and record. A sink error is the sink's own:
	// it is not an llm failure and does not change the record.
	Stream(ctx context.Context, req session.Request, sink AttemptSink) iter.Seq[vocab.Chunk]
}

type prepared struct {
	rt    *Runtime
	route Route // captured at Prepare; a later Replace does not reach it
	slots chan struct{}
	model string
	cfg   vocab.CallConfig
	call  Call
}

func (p *prepared) Route() vocab.RouteID { return p.route.ID }
func (p *prepared) Model() string        { return p.model }
func (p *prepared) Config() vocab.CallConfig {
	return vocab.CallConfig{Controls: p.cfg.Controls.Clone(), Defaulted: slices.Clone(p.cfg.Defaulted)}
}

func failure(code vocab.Code, format string, args ...any) *vocab.Failure {
	f, err := vocab.NewFailure(code, fmt.Sprintf(format, args...))
	if err != nil {
		panic(err) // the codes below are all in the closed table
	}
	return f
}

// Prepare resolves route r and model (the route's default model when ""),
// materialises the route's defaults into the controls and validates them; it
// does no I/O. An unknown route is llm/no-route, an unknown model
// llm/unknown-model, an effort the model does not list
// llm/unsupported-reasoning-effort and an out-of-range control
// llm/unsupported-option, each a *vocab.Failure.
func (rt *Runtime) Prepare(ctx context.Context, r vocab.RouteID, model string, c vocab.Controls) (Prepared, error) {
	e, ok := rt.lookup(r)
	if !ok {
		return nil, failure(vocab.CodeNoRoute, "no route %q is registered", r)
	}
	route := e.route
	m, ok := route.model(model)
	if !ok {
		if model == "" {
			return nil, failure(vocab.CodeUnknownModel, "route %q has no default model; name one", r)
		}
		return nil, failure(vocab.CodeUnknownModel, "route %q has no model %q", r, model)
	}
	cfg := materialise(c.Clone(), route.Defaults)
	if err := cfg.Validate(); err != nil {
		return nil, failure(vocab.CodeUnsupportedOption, "%v", err)
	}
	if eff := cfg.Controls.Effort; eff != "" && len(route.Models) > 0 && !slices.Contains(m.Efforts, eff) {
		return nil, failure(vocab.CodeUnsupportedReasoningEffort, "model %q of route %q does not accept effort %q", m.Name, r, eff)
	}
	call, err := callPrepare(ctx, route, m.Name, cfg)
	if err != nil {
		var f *vocab.Failure
		if errors.As(err, &f) {
			return nil, f
		}
		return nil, failure(vocab.CodeInvalidRequest, "route %q could not prepare the call: %v", r, err)
	}
	if call == nil {
		return nil, failure(vocab.CodeInvalidRequest, "route %q prepared no call", r)
	}
	return &prepared{rt: rt, route: route, slots: e.slots, model: m.Name, cfg: cfg, call: call}, nil
}

// callPrepare runs the adapter's Prepare with its panic contained.
func callPrepare(ctx context.Context, route Route, model string, cfg vocab.CallConfig) (call Call, err error) {
	defer func() {
		if r := recover(); r != nil {
			call, err = nil, fmt.Errorf("the adapter panicked: %v", r)
		}
	}()
	return route.Adapter.Prepare(ctx, route.ID, model, vocab.CallConfig{Controls: cfg.Controls.Clone(), Defaulted: slices.Clone(cfg.Defaulted)})
}

// materialise fills every unset control of c from d and names what it filled.
func materialise(c, d vocab.Controls) vocab.CallConfig {
	var defaulted []string
	if c.Effort == "" && d.Effort != "" {
		c.Effort, defaulted = d.Effort, append(defaulted, "effort")
	}
	if c.Temperature == nil && d.Temperature != nil {
		v := *d.Temperature
		c.Temperature, defaulted = &v, append(defaulted, "temperature")
	}
	if c.TopP == nil && d.TopP != nil {
		v := *d.TopP
		c.TopP, defaulted = &v, append(defaulted, "top_p")
	}
	if c.MaxOutputTokens == 0 && d.MaxOutputTokens != 0 {
		c.MaxOutputTokens, defaulted = d.MaxOutputTokens, append(defaulted, "max_output_tokens")
	}
	if c.Stop == nil && d.Stop != nil {
		c.Stop, defaulted = slices.Clone(d.Stop), append(defaulted, "stop")
	}
	if c.ToolChoice == (vocab.ToolChoice{}) && d.ToolChoice != (vocab.ToolChoice{}) {
		c.ToolChoice, defaulted = d.ToolChoice, append(defaulted, "tool_choice")
	}
	return vocab.CallConfig{Controls: c, Defaulted: defaulted}
}

// Complete is the buffered auxiliary call: one attempt on the request's route
// and its default model, with the request's controls. req must be an auxiliary
// request (from Session.RequestFrom); sink receives the one attempt record so the
// caller logs it, attributed by requestAsOf, requestDigest and the purpose in
// req.Meta(). The message is returned only when the attempt succeeded; a failed
// attempt returns its *vocab.Failure, and a sink error is returned as itself,
// never as an llm failure. Bounded retry around it is DH-D-LLM-3.
func (rt *Runtime) Complete(ctx context.Context, req session.Request, sink AttemptSink) (vocab.Message, error) {
	if sink == nil {
		return vocab.Message{}, errors.New("llm: Complete needs an attempt sink")
	}
	if req.IsZero() {
		return vocab.Message{}, failure(vocab.CodeInvalidRequest, "the request was never built")
	}
	if !req.Meta().Auxiliary() {
		return vocab.Message{}, failure(vocab.CodeInvalidRequest, "Complete takes an auxiliary request; a loop step streams")
	}
	p, err := rt.Prepare(ctx, req.Route(), "", req.Controls())
	if err != nil {
		return vocab.Message{}, err
	}
	var (
		rec     Attempt
		settled bool
		sinkErr error
	)
	capture := AttemptSinkFunc(func(a Attempt) error {
		rec, settled = a, true
		sinkErr = sink.Settle(a)
		return sinkErr
	})
	for range p.Stream(ctx, req, capture) {
	}
	switch {
	case !settled:
		return vocab.Message{}, errors.New("llm: the attempt was not settled")
	case sinkErr != nil:
		return vocab.Message{}, fmt.Errorf("llm: the attempt sink: %w", sinkErr)
	case !rec.Succeeded():
		return vocab.Message{}, rec.Finish.Failure
	}
	return *rec.Message, nil
}
