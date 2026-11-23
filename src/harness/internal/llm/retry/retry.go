// Package retry is the llm.retry component (DH-D-LLM-3, DH-ADR-LLM-4): a
// listener on the driver's failed-attempt point (agent.AttemptFailed) that
// retries a Transient-class failure under the call's captured policy, on the
// same route, model and credential. A Permanent failure (llm/auth among them) is
// never retried and never passed to another route (SP-2). One cap on attempts
// per step binds every recovery listener.
//
// Every decision is a fact in the log, written through the component's Record
// hook: llm/retry is appended before the wait starts, llm/retry-started once it
// ends and the step re-runs, llm/retry-abandoned with the reason recovery
// stopped. The wait runs on the component's timer and the failed attempt's
// context, whichever ends first; a cancel during it is abandoned and makes no
// attempt.
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
	// Record appends d to the session of f.Agent; required. An error fails the
	// decision closed: the attempt is not retried.
	Record func(ctx context.Context, f agent.AttemptFailure, d event.Draft) error
	// Policy returns the failed call's captured route policy (llm.Prepared.Retry,
	// e.g. the DeepSeek route's DefaultRetry). agent.AttemptFailure does not carry
	// that policy, so the wiring that prepared the call (Phase 11) must inject it
	// here; the route's Mode, RetryAfterCap, Jitter and MaxElapsed apply only
	// through this hook.
	//
	// nil falls back to FromAgent(f.Policy): the point's own bounded policy,
	// which never retries persistently and has no Retry-After cap, jitter or
	// elapsed bound. It is a standalone default, not the route's policy.
	Policy func(f agent.AttemptFailure) llm.RetryPolicy
	// Now, After and Jitter are the clock, the timer and the jitter draw; nil is
	// time.Now, time.After and math/rand. A test passes a virtual clock.
	Now    func() time.Time
	After  func(time.Duration) <-chan time.Time
	Jitter func() float64
}

// FromAgent is the llm policy of the point's bounded policy: MaxAttempts counts
// the first attempt too. It carries only MaxAttempts, BaseDelay and MaxDelay; it
// is Config.Policy's fallback, not a substitute for the route's captured policy.
func FromAgent(p agent.RetryPolicy) llm.RetryPolicy {
	return llm.RetryPolicy{Mode: llm.RetryBounded, MaxRetries: max(p.MaxAttempts-1, 0), BaseDelay: p.BaseDelay, MaxDelay: p.MaxDelay}.Normalised()
}

// Component is one registered retry listener.
type Component struct {
	cfg    Config
	handle *cordis.EffectHandle

	mu    sync.Mutex
	steps map[agent.Ref]stepStart // one entry per agent: its current step's first failure
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
		// Bounded-only fallback; the route policy needs Config.Policy (see Config).
		cfg.Policy = func(f agent.AttemptFailure) llm.RetryPolicy { return FromAgent(f.Policy) }
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.After == nil {
		cfg.After = time.After
	}
	if cfg.Jitter == nil {
		cfg.Jitter = rand.Float64
	}
	c := &Component{cfg: cfg, steps: map[agent.Ref]stepStart{}}
	h, err := agent.AttemptFailed.On(scope, c.decide)
	if err != nil {
		return nil, err
	}
	c.handle = h
	return c, nil
}

// Close removes the listener.
func (c *Component) Close() { c.handle.Dispose() }

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
	d, err := ScheduledType.Draft(Scheduled{Turn: f.Turn, Step: f.Step, Attempt: f.Attempt, DelayMs: wait.Milliseconds(), Failure: f.Failure, Policy: policy})
	if err != nil {
		return nil, err
	}
	if err := c.cfg.Record(ctx, f, d); err != nil {
		return nil, err
	}
	timer := c.cfg.After(wait)
	select {
	case <-ctx.Done():
		if err := c.abandon(context.WithoutCancel(ctx), f, llm.AbandonCancelled); err != nil {
			return nil, err
		}
		return agent.GiveUp{}, nil
	case <-timer:
	}
	d, err = StartedType.Draft(Started{Turn: f.Turn, Step: f.Step, Attempt: f.Attempt + 1})
	if err != nil {
		return nil, err
	}
	if err := c.cfg.Record(ctx, f, d); err != nil {
		return nil, err
	}
	// The wait is done here; the driver re-runs the step at once.
	return agent.Retry{Reason: string(f.Failure.Code)}, nil
}
