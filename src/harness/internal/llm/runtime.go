package llm

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"math/rand/v2"
	"slices"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Option configures a Runtime.
type Option func(*Runtime)

// WithClock sets the clock attempt timings are read from, in Unix milliseconds.
// Complete also measures a persistent retry policy's elapsed time on it.
func WithClock(clock func() int64) Option { return func(rt *Runtime) { rt.clock = clock } }

// WithTimer sets the timer Complete waits on between attempts (time.After by
// default); a test passes a virtual clock's After.
func WithTimer(after func(time.Duration) <-chan time.Time) Option {
	return func(rt *Runtime) { rt.after = after }
}

// WithJitter sets the source of a retry's jitter draw, in [0, 1); the default is
// math/rand. A constant 0.5 makes the backoff exact.
func WithJitter(draw func() float64) Option { return func(rt *Runtime) { rt.jitter = draw } }

// New is a runtime with no routes.
func New(opts ...Option) *Runtime {
	rt := &Runtime{routes: map[vocab.RouteID]*entry{}, clock: func() int64 { return time.Now().UnixMilli() },
		after: time.After, jitter: rand.Float64}
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
	// Retry is the route's retry policy captured with the call, its defaults
	// filled; a later Replace does not reach it.
	Retry() RetryPolicy
	// Stream runs the call's one attempt; a prepared call is single-use, so a
	// second Stream (sequential or concurrent) is refused with
	// llm/invalid-request before any provider I/O, as is a request whose
	// controls, with the route's defaults materialised, differ from Config. It
	// always ends with exactly one Finish chunk
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
	used  atomic.Bool // set by the first Stream: a prepared call runs once
}

func (p *prepared) Route() vocab.RouteID { return p.route.ID }
func (p *prepared) Model() string        { return p.model }
func (p *prepared) Retry() RetryPolicy   { return p.route.Retry.Normalised() }
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
	p, err := rt.prepareOn(ctx, e.route, e.slots, model, c)
	if err != nil {
		return nil, err
	}
	return p, nil
}

// prepareOn is Prepare on route, already resolved, and its slots.
func (rt *Runtime) prepareOn(ctx context.Context, route Route, slots chan struct{}, model string, c vocab.Controls) (*prepared, error) {
	r := route.ID
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
	return &prepared{rt: rt, route: route, slots: slots, model: m.Name, cfg: cfg, call: call}, nil
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

// sameControls reports whether a and b set the same controls to the same values.
func sameControls(a, b vocab.Controls) bool {
	samePtr := func(x, y *float64) bool { return x == nil && y == nil || x != nil && y != nil && *x == *y }
	return a.Effort == b.Effort && samePtr(a.Temperature, b.Temperature) && samePtr(a.TopP, b.TopP) &&
		a.MaxOutputTokens == b.MaxOutputTokens && slices.Equal(a.Stop, b.Stop) && a.ToolChoice == b.ToolChoice
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

// Complete is the buffered auxiliary call on the request's route and its default
// model, with the request's controls, under the route's retry policy
// (DH-D-LLM-3): a Transient failure is tried again on the same route after the
// policy's wait, a Retry-After honoured up to its cap; any other failure, an
// exhausted policy or a cancelled wait ends the call. Nothing else is tried: no
// other route, model or credential. Each attempt is its own prepared call on the
// route, model and policy the first attempt captured, and
// sink receives each attempt's one record so the caller logs it, attributed by
// requestAsOf, requestDigest and the purpose in req.Meta(); no chunk of any
// attempt reaches the caller. req must be an auxiliary request (from
// Session.RequestFrom). The message is returned only when an attempt succeeded;
// otherwise the last attempt's *vocab.Failure (llm/aborted when the context
// ended during a wait), and a sink error is returned as itself, never as an llm
// failure.
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
	start := rt.clock()
	// The first attempt captures the route (adapter, credential reference, models
	// and retry policy), its slots and the model; every later attempt of the call
	// is prepared on that capture, so a Replace meanwhile reaches none of them.
	first, err := rt.Prepare(ctx, req.Route(), "", req.Controls())
	if err != nil {
		return vocab.Message{}, err
	}
	captured := first.(*prepared)
	policy := captured.Retry()
	for attempt := 1; ; attempt++ {
		p := captured
		if attempt > 1 {
			if p, err = rt.prepareOn(ctx, captured.route, captured.slots, captured.model, req.Controls()); err != nil {
				return vocab.Message{}, err
			}
		}
		rec, err := rt.completeOnce(ctx, p, req, sink)
		if err != nil {
			return vocab.Message{}, err
		}
		if rec.Succeeded() {
			return *rec.Message, nil
		}
		elapsed := time.Duration(rt.clock()-start) * time.Millisecond
		wait, stop := policy.Next(rec.Finish.Failure, attempt, elapsed, rt.jitter())
		if stop != "" {
			return vocab.Message{}, rec.Finish.Failure
		}
		if ctx.Err() != nil {
			return vocab.Message{}, failure(vocab.CodeAborted, "cancelled before retry %d: %v", attempt, ctx.Err())
		}
		select {
		case <-ctx.Done():
			return vocab.Message{}, failure(vocab.CodeAborted, "cancelled during the wait before retry %d: %v", attempt, ctx.Err())
		case <-rt.after(wait):
		}
	}
}

// completeOnce runs p's one attempt buffered and returns its record once sink
// has it.
func (rt *Runtime) completeOnce(ctx context.Context, p Prepared, req session.Request, sink AttemptSink) (Attempt, error) {
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
		return Attempt{}, errors.New("llm: the attempt was not settled")
	case sinkErr != nil:
		return Attempt{}, fmt.Errorf("llm: the attempt sink: %w", sinkErr)
	}
	return rec, nil
}
