// Package retry is the llm.retry component (DH-D-LLM-3, DH-ADR-LLM-4): a
// listener on the driver's failed-attempt point (agent.AttemptFailed) that
// retries a Transient-class failure under the call's captured policy, on the
// same route, model and credential. A Permanent failure (llm/auth among them) is
// never retried and never passed to another route (SP-2). One cap on attempts
// per step binds every recovery listener.
//
// The component decides and logs; the driver waits (M1-D-PIPE-2 A). Every
// decision is a fact in the log, written through the component's Record hook:
// llm/retry is appended and agent.Retry{After} returned, so the record exists
// before the driver's timer starts; when the driver's wait ends it sends
// agent.RetryWaited, from which the component appends llm/retry-started (the
// step re-runs) or llm/retry-abandoned "cancelled" (a cancel ended the wait and
// no attempt runs). Any other stop is llm/retry-abandoned with its reason at the
// decision. There is one clock, the driver's session clock, and one place a
// cancel lands.
package retry

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// DefaultStepAttemptCap is retry.Config.StepAttemptCap's default (llm.md sec. 6.2).
const DefaultStepAttemptCap = 12

// Scheduled is llm/retry: a retry was decided and its wait is about to start.
type Scheduled struct {
	Turn int `json:"turn"`
	Step int `json:"step"`
	// Attempt is the failed attempt's number in the step (1 for the first).
	Attempt int             `json:"attempt"`
	DelayMs int64           `json:"delay_ms"`
	Failure *vocab.Failure  `json:"failure"`
	Policy  llm.RetryPolicy `json:"policy"`
}

// Started is llm/retry-started: the wait ended and attempt Attempt runs.
type Started struct {
	Turn    int `json:"turn"`
	Step    int `json:"step"`
	Attempt int `json:"attempt"`
}

// Abandoned is llm/retry-abandoned: why recovery of the failed attempt stopped.
type Abandoned struct {
	Turn    int            `json:"turn"`
	Step    int            `json:"step"`
	Attempt int            `json:"attempt"`
	Reason  llm.Abandon    `json:"reason"`
	Failure *vocab.Failure `json:"failure,omitempty"`
}

func validReason(a Abandoned) error {
	switch a.Reason {
	case llm.AbandonNotRetryable, llm.AbandonExhausted, llm.AbandonElapsed, llm.AbandonStepCap, llm.AbandonCancelled:
		return nil
	}
	return fmt.Errorf("retry: abandon reason %q is not in the closed set", a.Reason)
}

// The durable, log-only events of the component. Version 2 of llm/retry and
// llm/retry-abandoned writes every duration of the failure and the policy as its
// text ("2s", "500ms") instead of integer nanoseconds (plan 14.4.1); upgrade 1
// rewrites a stored number to its text.
var (
	ScheduledType = event.MustDefine(event.Default(), event.Spec[Scheduled]{Name: "llm/retry", Version: 2,
		Upgrades: map[int]event.Upgrade{1: event.DurationsAsText("failure.retry_after",
			"policy.base_delay", "policy.max_delay", "policy.max_elapsed", "policy.retry_after_cap")}})
	StartedType   = event.MustDefine(event.Default(), event.Spec[Started]{Name: "llm/retry-started", Version: 1})
	AbandonedType = event.MustDefine(event.Default(), event.Spec[Abandoned]{Name: "llm/retry-abandoned", Version: 2,
		Upgrades: map[int]event.Upgrade{1: event.DurationsAsText("failure.retry_after")},
		Validate: validReason})
)

// Config configures the component.
type Config struct {
	// StepAttemptCap bounds the attempts of one step across every recovery
	// listener; zero is DefaultStepAttemptCap.
	StepAttemptCap int
	// Record appends d to the session of f.Agent; required. An error at the
	// decision fails it closed: the attempt is not retried. The wait's outcome
	// (llm/retry-started, llm/retry-abandoned "cancelled") is written from an
	// observe-only notice, so an error there is logged on the component's logger
	// and the step goes on as the driver decided.
	Record func(ctx context.Context, f agent.AttemptFailure, d event.Draft) error
	// Policy overrides the policy a failure is decided under. nil is RoutePolicy:
	// the failed call's route policy as captured at Prepare, which the driver
	// carries in AttemptFailure.RoutePolicy, so the route's Mode, RetryAfterCap,
	// Jitter and MaxElapsed apply.
	Policy func(f agent.AttemptFailure) llm.RetryPolicy
	// Now and Jitter are the clock elapsed time is read on (the driver's session
	// clock, so MaxElapsed and StepStarted agree) and the jitter draw; nil is
	// time.Now and math/rand. The component starts no timer: the driver waits.
	Now    func() time.Time
	Jitter func() float64
}

// RoutePolicy is Config.Policy's default: f.RoutePolicy, or FromAgent(f.Policy)
// for a raiser that carried no route policy (the point's own bounded policy,
// with no Retry-After cap, jitter or elapsed bound of the route's).
func RoutePolicy(f agent.AttemptFailure) llm.RetryPolicy {
	if f.RoutePolicy != (llm.RetryPolicy{}) {
		return f.RoutePolicy
	}
	return FromAgent(f.Policy)
}

// FromAgent is the llm policy of the point's bounded policy: MaxAttempts counts
// the first attempt too. It carries only MaxAttempts, BaseDelay and MaxDelay; it
// is RoutePolicy's fallback, not a substitute for the route's captured policy.
func FromAgent(p agent.RetryPolicy) llm.RetryPolicy {
	return llm.RetryPolicy{Mode: llm.RetryBounded, MaxRetries: max(p.MaxAttempts-1, 0), BaseDelay: p.BaseDelay, MaxDelay: p.MaxDelay}.Normalised()
}

// Component is one registered retry listener.
type Component struct {
	cfg     Config
	handles []*cordis.EffectHandle

	mu      sync.Mutex
	steps   map[agent.Ref]stepStart            // one entry per agent: its current step's first failure
	waiting map[agent.Ref]agent.AttemptFailure // one entry per agent: the failure whose retry wait is pending
}

type stepStart struct {
	turn, step int
	at         time.Time
}

// Listen registers the component on agent.AttemptFailed in scope.
func Listen(scope *cordis.Context, cfg Config) (*Component, error) {
	if cfg.Record == nil {
		return nil, errors.New("retry: Listen needs a Record hook")
	}
	if cfg.StepAttemptCap < 0 {
		return nil, fmt.Errorf("retry: negative step attempt cap %d", cfg.StepAttemptCap)
	}
	if cfg.StepAttemptCap == 0 {
		cfg.StepAttemptCap = DefaultStepAttemptCap
	}
	if cfg.Policy == nil {
		cfg.Policy = RoutePolicy
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Jitter == nil {
		cfg.Jitter = rand.Float64
	}
	c := &Component{cfg: cfg, steps: map[agent.Ref]stepStart{}, waiting: map[agent.Ref]agent.AttemptFailure{}}
	h, err := agent.AttemptFailed.On(scope, c.decide)
	if err != nil {
		return nil, err
	}
	w, err := agent.RetryWaited.On(scope, c.waited)
	if err != nil {
		h.Dispose()
		return nil, err
	}
	c.handles = []*cordis.EffectHandle{h, w}
	return c, nil
}

// Close removes the listeners.
func (c *Component) Close() {
	for _, h := range c.handles {
		h.Dispose()
	}
}

// elapsed is the time since f's step began: its first attempt's start
// (f.StepStarted) when the raiser supplies it, else the step's first failure,
// which it records when f is that failure. Only a bounded policy reaches the
// fallback, and it does not read elapsed time: decide refuses a persistent
// policy with no StepStarted.
func (c *Component) elapsed(f agent.AttemptFailure) time.Duration {
	now := c.cfg.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.steps[f.Agent]
	if !ok || s.turn != f.Turn || s.step != f.Step || f.Attempt <= 1 {
		s = stepStart{turn: f.Turn, step: f.Step, at: now}
		if !f.StepStarted.IsZero() {
			s.at = f.StepStarted
		}
		c.steps[f.Agent] = s
	}
	return max(now.Sub(s.at), 0)
}

func (c *Component) forget(f agent.AttemptFailure) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s, ok := c.steps[f.Agent]; ok && s.turn == f.Turn && s.step == f.Step {
		delete(c.steps, f.Agent)
	}
}

func (c *Component) abandon(ctx context.Context, f agent.AttemptFailure, why llm.Abandon) error {
	c.forget(f)
	d, err := AbandonedType.Draft(Abandoned{Turn: f.Turn, Step: f.Step, Attempt: f.Attempt, Reason: why, Failure: f.Failure})
	if err != nil {
		return err
	}
	return c.cfg.Record(ctx, f, d)
}

// ErrNoStepStart is returned for a persistent policy's failure whose point did
// not carry AttemptFailure.StepStarted.
var ErrNoStepStart = errors.New("retry: a persistent policy measures MaxElapsed from the step's first attempt start, and the point carried no StepStarted")

func (c *Component) decide(_ *cordis.Context, f agent.AttemptFailure, next func(agent.AttemptFailure) (agent.AttemptDecision, error)) (agent.AttemptDecision, error) {
	ctx := f.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if f.Failure == nil {
		return nil, errors.New("retry: the point carried no failure")
	}
	// The cap binds every recovery listener: no inner listener is asked.
	if f.Attempt >= c.cfg.StepAttemptCap {
		if err := c.abandon(ctx, f, llm.AbandonStepCap); err != nil {
			return nil, err
		}
		return agent.GiveUp{}, nil
	}
	policy := c.cfg.Policy(f).Normalised()
	// A persistent policy's MaxElapsed runs from the step's first attempt start;
	// measuring it from the first failure would leave that request's duration
	// out, so a point without the start fails explicitly instead. A Permanent
	// failure is still given up below: it needs no elapsed time.
	if policy.Mode == llm.RetryPersistent && f.StepStarted.IsZero() && f.Failure.Class != vocab.Permanent {
		c.forget(f)
		return nil, ErrNoStepStart
	}
	wait, why := policy.Next(f.Failure, f.Attempt, c.elapsed(f), c.cfg.Jitter())
	if why != "" {
		if err := c.abandon(ctx, f, why); err != nil {
			return nil, err
		}
		// Only a failure this component does not retry (not-retryable, and not
		// Permanent) is passed on: a repair listener may still own it. A bound
		// this component enforces (exhausted, elapsed, cancelled) is final, so no
		// inner listener can retry past it; a Permanent failure (llm/auth) is
		// terminal, so none can retry it or take it to another route (SP-2).
		if why == llm.AbandonNotRetryable && f.Failure.Class != vocab.Permanent {
			return next(f)
		}
		return agent.GiveUp{}, nil
	}
	// The raiser's own attempt budget (the driver's MaxAttemptsPerStep) is a
	// step cap too: with no attempt left the driver would not retry, so a
	// scheduled llm/retry would never be followed by its outcome.
	if f.Policy.MaxAttempts > 0 && f.AttemptsLeft <= 0 {
		if err := c.abandon(ctx, f, llm.AbandonStepCap); err != nil {
			return nil, err
		}
		return agent.GiveUp{}, nil
	}
	d, err := ScheduledType.Draft(Scheduled{Turn: f.Turn, Step: f.Step, Attempt: f.Attempt, DelayMs: wait.Milliseconds(), Failure: f.Failure, Policy: policy})
	if err != nil {
		return nil, err
	}
	if err := c.cfg.Record(ctx, f, d); err != nil {
		return nil, err
	}
	c.mu.Lock()
	c.waiting[f.Agent] = f
	c.mu.Unlock()
	// The driver waits After on the session clock and sends agent.RetryWaited.
	return agent.Retry{After: wait, Reason: string(f.Failure.Code)}, nil
}

// waited writes the outcome of the driver's wait for the retry this component
// scheduled: llm/retry-started when the wait ran out, llm/retry-abandoned
// "cancelled" when a cancel ended it. A notice for a wait the component did not
// schedule (another listener's Retry) is not its to record.
func (c *Component) waited(_ *cordis.Context, n agent.RetryWaitEnded) error {
	c.mu.Lock()
	f, ok := c.waiting[n.Agent]
	if ok && f.Turn == n.Turn && f.Step == n.Step && f.Attempt == n.Attempt {
		delete(c.waiting, n.Agent)
	} else {
		ok = false
	}
	c.mu.Unlock()
	if !ok {
		return nil
	}
	ctx := f.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	if n.Cancelled {
		// The step's context is cancelled: the record is still written.
		return c.abandon(context.WithoutCancel(ctx), f, llm.AbandonCancelled)
	}
	d, err := StartedType.Draft(Started{Turn: f.Turn, Step: f.Step, Attempt: f.Attempt + 1})
	if err != nil {
		return err
	}
	return c.cfg.Record(ctx, f, d)
}
