package llm

import (
	"fmt"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
)

// RetryMode is how a route's retry policy is bounded (DH-ADR-LLM-4).
type RetryMode string

// The retry modes.
const (
	// RetryBounded retries up to MaxRetries times.
	RetryBounded RetryMode = "bounded"
	// RetryPersistent retries until MaxElapsed has passed since the first
	// attempt started.
	RetryPersistent RetryMode = "persistent"
)

// The policy defaults (llm.md sec. 6.2, retry.Config).
const (
	DefaultMaxRetries    = 5
	DefaultBaseDelay     = 500 * time.Millisecond
	DefaultMaxDelay      = 10 * time.Second
	DefaultJitter        = 0.1
	DefaultMaxElapsed    = 30 * time.Minute
	DefaultRetryAfterCap = time.Minute
)

// NoJitter is the Jitter that turns jitter off. A zero Jitter takes
// DefaultJitter, like every other zero field, so 0 cannot mean "off".
const NoJitter = -1.0

// RetryPolicy is a route's retry policy, captured with the call at Prepare. Only
// a Transient-class failure is ever retried; a Permanent one (llm/auth among
// them) never is, in any mode, and a retry repeats the same route, model and
// credential reference: there is no fallback to another route (SP-2). The zero
// policy never retries (a stub route's); a provider route carries DefaultRetry.
// A zero delay, jitter, elapsed bound or cap takes its default; set Jitter to
// NoJitter to disable jitter.
type RetryPolicy struct {
	Mode RetryMode `json:"mode,omitempty"`
	// MaxRetries bounds the retries after the first attempt (bounded mode); 0
	// is none.
	MaxRetries int `json:"max_retries,omitempty"`
	// BaseDelay doubles per retry up to MaxDelay; Jitter spreads it by up to
	// that fraction either way; NoJitter is none.
	BaseDelay time.Duration `json:"base_delay,omitempty"`
	MaxDelay  time.Duration `json:"max_delay,omitempty"`
	Jitter    float64       `json:"jitter,omitempty"`
	// MaxElapsed bounds the time from the first attempt's start to the next
	// attempt's (persistent mode).
	MaxElapsed time.Duration `json:"max_elapsed,omitempty"`
	// RetryAfterCap is the longest provider Retry-After honoured: a failure that
	// asks for longer waits the cap.
	RetryAfterCap time.Duration `json:"retry_after_cap,omitempty"`
}

// retryPolicyJSON is RetryPolicy without its methods, so its encoding is the
// fields'.
type retryPolicyJSON RetryPolicy

// MarshalJSON writes each delay, bound and cap as its text, "500ms"
// (schema.Marshal), so the bytes encoding/json writes match the derived shape.
func (p RetryPolicy) MarshalJSON() ([]byte, error) { return schema.Marshal(retryPolicyJSON(p)) }

// UnmarshalJSON reads each duration from its text; a number there is refused.
func (p *RetryPolicy) UnmarshalJSON(b []byte) error {
	return schema.Unmarshal(b, (*retryPolicyJSON)(p))
}

// DefaultRetry is the bounded default a provider route carries: five retries,
// 500 ms doubling to 10 s, jitter 0.1.
func DefaultRetry() RetryPolicy {
	return RetryPolicy{Mode: RetryBounded, MaxRetries: DefaultMaxRetries}.Normalised()
}

// PersistentRetry is the persistent mode: retries until MaxElapsed (30 min).
func PersistentRetry() RetryPolicy { return RetryPolicy{Mode: RetryPersistent}.Normalised() }

// Normalised is p with every zero delay, jitter, bound and cap at its default;
// MaxRetries is kept as it is, and so is a NoJitter.
func (p RetryPolicy) Normalised() RetryPolicy {
	if p.Mode == "" {
		p.Mode = RetryBounded
	}
	if p.BaseDelay == 0 {
		p.BaseDelay = DefaultBaseDelay
	}
	if p.MaxDelay == 0 {
		p.MaxDelay = DefaultMaxDelay
	}
	if p.Jitter == 0 {
		p.Jitter = DefaultJitter
	}
	if p.MaxElapsed == 0 {
		p.MaxElapsed = DefaultMaxElapsed
	}
	if p.RetryAfterCap == 0 {
		p.RetryAfterCap = DefaultRetryAfterCap
	}
	return p
}

// Validate refuses an unknown mode or a negative bound.
func (p RetryPolicy) Validate() error {
	if p.Mode != "" && p.Mode != RetryBounded && p.Mode != RetryPersistent {
		return fmt.Errorf("llm: retry mode %q is neither %q nor %q", p.Mode, RetryBounded, RetryPersistent)
	}
	if p.MaxRetries < 0 || p.BaseDelay < 0 || p.MaxDelay < 0 || p.MaxElapsed < 0 || p.RetryAfterCap < 0 {
		return fmt.Errorf("llm: retry policy %+v has a negative bound", p)
	}
	if p.Jitter != NoJitter && (p.Jitter < 0 || p.Jitter >= 1) {
		return fmt.Errorf("llm: retry jitter %v is neither NoJitter nor in [0, 1)", p.Jitter)
	}
	if p.BaseDelay > 0 && p.MaxDelay > 0 && p.BaseDelay > p.MaxDelay {
		return fmt.Errorf("llm: retry base delay %v exceeds the max delay %v", p.BaseDelay, p.MaxDelay)
	}
	return nil
}

// Abandon is why recovery stopped; the set is closed (llm/retry-abandoned).
type Abandon string

// The abandon reasons.
const (
	AbandonNotRetryable Abandon = "not-retryable"
	AbandonExhausted    Abandon = "exhausted"
	AbandonElapsed      Abandon = "elapsed"
	AbandonStepCap      Abandon = "step-cap"
	AbandonCancelled    Abandon = "cancelled"
)

// Next decides what follows failed attempt number attempt (1 for the first),
// elapsed after the first attempt started: the wait before the next attempt, or
// why there is none. u in [0, 1) draws the jitter; a provider Retry-After is
// honoured exactly, up to RetryAfterCap, and takes no jitter. Next is pure.
func (p RetryPolicy) Next(f *vocab.Failure, attempt int, elapsed time.Duration, u float64) (time.Duration, Abandon) {
	p = p.Normalised()
	switch {
	case f == nil || f.Class == vocab.Cancelled:
		return 0, AbandonCancelled
	case f.Class != vocab.Transient:
		return 0, AbandonNotRetryable
	case p.Mode == RetryBounded && attempt > p.MaxRetries:
		return 0, AbandonExhausted
	}
	wait := p.backoff(attempt, u)
	if f.RetryAfter > 0 {
		wait = min(f.RetryAfter, p.RetryAfterCap)
	}
	if p.Mode == RetryPersistent && elapsed+wait > p.MaxElapsed {
		return 0, AbandonElapsed
	}
	return wait, ""
}

// backoff is BaseDelay doubled per earlier retry, capped at MaxDelay, spread by
// the jitter and capped again.
func (p RetryPolicy) backoff(attempt int, u float64) time.Duration {
	d := p.BaseDelay
	for i := 1; i < attempt && d < p.MaxDelay; i++ {
		d *= 2
	}
	d = min(d, p.MaxDelay)
	u = min(max(u, 0), 1)
	if p.Jitter > 0 {
		d = time.Duration(float64(d) * (1 + p.Jitter*(2*u-1)))
	}
	return min(max(d, 0), p.MaxDelay)
}
