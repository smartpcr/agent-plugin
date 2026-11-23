package retry_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/retry"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
)

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// log is the Record hook's side: the drafts in order, the timer waiters pending
// when each arrived, and whether its context was already done.
type log struct {
	mu      sync.Mutex
	clk     *clock.Manual
	types   []string
	raws    []string
	waiters []int
	done    []bool
}

func (l *log) record(ctx context.Context, _ agent.AttemptFailure, d event.Draft) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.types = append(l.types, d.Type())
	l.raws = append(l.raws, d.Raw())
	l.waiters = append(l.waiters, l.clk.Waiters())
	l.done = append(l.done, ctx.Err() != nil)
	return nil
}

func (l *log) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.types...)
}

type env struct {
	root *cordis.Context
	clk  *clock.Manual
	log  *log
	logs *cordis.MemoryExporter
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	logs := &cordis.MemoryExporter{}
	root.Loggers().AddExporter(logs)
	t.Cleanup(root.Dispose)
	clk := clock.NewManual(time.Unix(1_700_000_000, 0))
	return &env{root: root, clk: clk, log: &log{clk: clk}, logs: logs}
}

func (e *env) listen(t *testing.T, cfg retry.Config) *retry.Component {
	t.Helper()
	cfg.Record = e.log.record
	cfg.Now, cfg.Jitter = e.clk.Now, func() float64 { return 0.5 }
	c := must(retry.Listen(e.root, cfg))
	t.Cleanup(c.Close)
	return c
}

func giveUp(agent.AttemptFailure) (agent.AttemptDecision, error) { return agent.GiveUp{}, nil }

// ask dispatches f and plays the driver's part of a Retry (M1-D-PIPE-2 A): it
// waits the decision's After on the virtual clock, then sends the wait's
// outcome, agent.RetryWaited, not cancelled.
func (e *env) ask(t *testing.T, f agent.AttemptFailure) agent.AttemptDecision {
	t.Helper()
	d, err := agent.AttemptFailed.Around(e.root, f, giveUp)
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := d.(agent.Retry); ok {
		e.wait(f, r.After, false)
	}
	return d
}

// wait is the driver's wait for f's retry: after on the virtual clock, unless
// a cancel ends it, then the notice.
func (e *env) wait(f agent.AttemptFailure, after time.Duration, cancelled bool) {
	if !cancelled {
		timer := e.clk.After(after)
		e.clk.Advance(after)
		<-timer
	}
	agent.RetryWaited.Notify(e.root, scope.Key{}, agent.RetryWaitEnded{Agent: f.Agent, Turn: f.Turn, Step: f.Step, Attempt: f.Attempt, Cancelled: cancelled})
}

func failure(code vocab.Code, after time.Duration) *vocab.Failure {
	f := must(vocab.NewFailure(code, string(code)))
	f.RetryAfter = after
	return f
}

// attempt is failed attempt n of a step whose driver allows 3 attempts.
func attempt(n int, f *vocab.Failure) agent.AttemptFailure {
	return agent.AttemptFailure{Ctx: context.Background(), Agent: agent.Ref{Session: "s1", Incarnation: 1}, Turn: 1, Step: 1,
		Attempt: n, Failure: f, Policy: agent.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second, MaxDelay: 8 * time.Second},
		AttemptsLeft: 3 - n}
}

func equal(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("records %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("records %v, want %v", got, want)
		}
	}
}

// DH-T-LLM-7 (TC-09a): under virtual time a bounded policy retries a Transient
// failure up to its count; the decision is Retry{After} with a Retry-After
// exactly (and the backoff otherwise), for the driver to wait; llm/retry is
// appended before the decision returns, so before any timer starts, and
// llm/retry-started once the driver's wait has ended.
//
//dsh:test DH-T-LLM-7
func TestBoundedRetryOnTheVirtualClock(t *testing.T) {
	e := newEnv(t)
	e.listen(t, retry.Config{})
	start := e.clk.Now()

	// A Retry-After is honoured exactly.
	if d := e.ask(t, attempt(1, failure(vocab.CodeServer, 2*time.Second))); d != (agent.Retry{After: 2 * time.Second, Reason: string(vocab.CodeServer)}) {
		t.Fatalf("decision %#v", d)
	}
	equal(t, e.log.snapshot(), "llm/retry", "llm/retry-started")
	if e.log.waiters[0] != 0 {
		t.Fatal("a timer was running when llm/retry was appended")
	}
	var s retry.Scheduled
	must(0, json.Unmarshal([]byte(e.log.raws[0]), &s))
	if s.DelayMs != 2000 || s.Attempt != 1 || s.Failure.Code != vocab.CodeServer || s.Policy.MaxRetries != 2 {
		t.Fatalf("llm/retry %+v", s)
	}
	if got := e.clk.Now().Sub(start); got != 2*time.Second {
		t.Fatalf("waited %v, want the 2s Retry-After", got)
	}
	var st retry.Started
	must(0, json.Unmarshal([]byte(e.log.raws[1]), &st))
	if st.Attempt != 2 {
		t.Fatalf("llm/retry-started %+v", st)
	}

	// Without one the backoff doubles: 1s then 2s (jitter drawn at its midpoint).
	if d := e.ask(t, attempt(2, failure(vocab.CodeRateLimit, 0))); d != (agent.Retry{After: 2 * time.Second, Reason: string(vocab.CodeRateLimit)}) {
		t.Fatalf("second retry %#v", d)
	}
	must(0, json.Unmarshal([]byte(e.log.raws[2]), &s))
	if s.DelayMs != 2000 {
		t.Fatalf("second backoff %dms, want 2000", s.DelayMs)
	}

	// The third failure of MaxAttempts 3 is exhausted: abandoned, no wait.
	if d := e.ask(t, attempt(3, failure(vocab.CodeServer, 0))); d != (agent.GiveUp{}) {
		t.Fatalf("exhausted %#v", d)
	}
	got := e.log.snapshot()
	equal(t, got, "llm/retry", "llm/retry-started", "llm/retry", "llm/retry-started", "llm/retry-abandoned")
	var a retry.Abandoned
	must(0, json.Unmarshal([]byte(e.log.raws[4]), &a))
	if a.Reason != llm.AbandonExhausted || a.Attempt != 3 {
		t.Fatalf("abandoned %+v", a)
	}
}

// DH-T-LLM-7: with no Policy hook the failed call's captured route policy
// (AttemptFailure.RoutePolicy) applies: its Retry-After cap bounds the wait and
// is the policy llm/retry records.
//
//dsh:test DH-T-LLM-7
func TestRoutePolicyIsTheDefault(t *testing.T) {
	e := newEnv(t)
	e.listen(t, retry.Config{})
	route := llm.RetryPolicy{Mode: llm.RetryBounded, MaxRetries: 1, RetryAfterCap: 5 * time.Second}.Normalised()
	f := attempt(1, failure(vocab.CodeServer, 7*time.Second))
	f.RoutePolicy = route
	if d := e.ask(t, f); d != (agent.Retry{After: 5 * time.Second, Reason: string(vocab.CodeServer)}) {
		t.Fatalf("decision %#v, want the route's 5s cap", d)
	}
	var s retry.Scheduled
	must(0, json.Unmarshal([]byte(e.log.raws[0]), &s))
	if s.DelayMs != 5000 || s.Policy != route {
		t.Fatalf("llm/retry %+v, want 5000ms under %+v", s, route)
	}
	// The route allows one retry, though the point's own policy allows two.
	second := attempt(2, failure(vocab.CodeServer, 0))
	second.RoutePolicy = route
	if d := e.ask(t, second); d != (agent.GiveUp{}) {
		t.Fatalf("second failure %#v, want the route's bound", d)
	}
	if p := retry.RoutePolicy(attempt(1, nil)); p != retry.FromAgent(attempt(1, nil).Policy) {
		t.Fatalf("no route policy: %+v, want the point's own", p)
	}
}

// DH-T-LLM-7, SP-2: a Permanent failure is never retried in any mode: no
// llm/retry, an llm/retry-abandoned "not-retryable", and the turn given up. An
// inner recovery listener that would retry it (or take it elsewhere) is never
// asked: auth is terminal.
//
//dsh:test DH-T-LLM-7
func TestPermanentFailureIsNeverRetried(t *testing.T) {
	for _, mode := range []llm.RetryMode{llm.RetryBounded, llm.RetryPersistent} {
		t.Run(string(mode), func(t *testing.T) {
			e := newEnv(t)
			e.listen(t, retry.Config{Policy: func(agent.AttemptFailure) llm.RetryPolicy {
				return llm.RetryPolicy{Mode: mode, MaxRetries: 5}
			}})
			passed := 0
			must(agent.AttemptFailed.On(e.root, func(_ *cordis.Context, f agent.AttemptFailure, next func(agent.AttemptFailure) (agent.AttemptDecision, error)) (agent.AttemptDecision, error) {
				passed++
				return agent.Retry{Reason: "recover"}, nil
			}))
			if d := e.ask(t, attempt(1, failure(vocab.CodeAuth, time.Second))); d != (agent.GiveUp{}) {
				t.Fatalf("decision %#v", d)
			}
			equal(t, e.log.snapshot(), "llm/retry-abandoned")
			var a retry.Abandoned
			must(0, json.Unmarshal([]byte(e.log.raws[0]), &a))
			if a.Reason != llm.AbandonNotRetryable || a.Failure.Code != vocab.CodeAuth || passed != 0 {
				t.Fatalf("abandoned %+v, passed on %d", a, passed)
			}
		})
	}
}

// DH-T-LLM-7: a Retry-After longer than the cap waits the cap; persistent mode
// stops once the next wait would pass MaxElapsed.
//
//dsh:test DH-T-LLM-7
func TestRetryAfterCapAndPersistentElapsed(t *testing.T) {
	e := newEnv(t)
	e.listen(t, retry.Config{Policy: func(agent.AttemptFailure) llm.RetryPolicy {
		return llm.RetryPolicy{Mode: llm.RetryPersistent, MaxElapsed: 10 * time.Second, RetryAfterCap: 4 * time.Second}
	}})
	first := attempt(1, failure(vocab.CodeRateLimit, time.Hour))
	first.StepStarted = e.clk.Now()
	if d := e.ask(t, first); d != (agent.Retry{After: 4 * time.Second, Reason: string(vocab.CodeRateLimit)}) {
		t.Fatalf("decision %#v, want the 4s cap", d)
	}
	var s retry.Scheduled
	must(0, json.Unmarshal([]byte(e.log.raws[0]), &s))
	if s.DelayMs != 4000 {
		t.Fatalf("capped wait %dms, want 4000", s.DelayMs)
	}
	second := attempt(2, failure(vocab.CodeRateLimit, time.Hour))
	second.StepStarted = first.StepStarted
	e.ask(t, second) // elapsed 4s + 4s <= 10s
	third := attempt(3, failure(vocab.CodeRateLimit, time.Hour))
	third.StepStarted = first.StepStarted
	if d := e.ask(t, third); d != (agent.GiveUp{}) {
		t.Fatalf("past MaxElapsed: %#v", d) // elapsed 8s + 4s > 10s
	}
	var a retry.Abandoned
	must(0, json.Unmarshal([]byte(e.log.raws[len(e.log.raws)-1]), &a))
	if a.Reason != llm.AbandonElapsed {
		t.Fatalf("abandoned %+v", a)
	}
}

// DH-T-LLM-7: a cancel during the driver's wait appends llm/retry-abandoned
// "cancelled", with the failure, through the cancelled step's context, and no
// llm/retry-started: no attempt follows.
//
//dsh:test DH-T-LLM-7
func TestCancelDuringTheWaitIsAbandoned(t *testing.T) {
	e := newEnv(t)
	e.listen(t, retry.Config{})
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	f := attempt(1, failure(vocab.CodeServer, 5*time.Second))
	f.Ctx = ctx
	d := must(agent.AttemptFailed.Around(e.root, f, giveUp))
	if d != (agent.Retry{After: 5 * time.Second, Reason: string(vocab.CodeServer)}) {
		t.Fatalf("decision %#v", d)
	}
	cancel()
	e.wait(f, 0, true)
	equal(t, e.log.snapshot(), "llm/retry", "llm/retry-abandoned")
	var a retry.Abandoned
	must(0, json.Unmarshal([]byte(e.log.raws[1]), &a))
	if a.Reason != llm.AbandonCancelled || a.Attempt != 1 || a.Failure == nil || a.Failure.Code != vocab.CodeServer {
		t.Fatalf("abandoned %+v", a)
	}
	if e.log.done[1] {
		t.Fatal("llm/retry-abandoned was recorded through a cancelled context")
	}
	// The wait is settled: a repeated notice records nothing more.
	e.wait(f, 0, true)
	equal(t, e.log.snapshot(), "llm/retry", "llm/retry-abandoned")
}

// DH-T-LLM-7: only the wait the component scheduled is its to record: a notice
// for another listener's Retry, or for another attempt, step or agent, records
// nothing.
//
//dsh:test DH-T-LLM-7
func TestWaitOfAnotherRetryIsNotRecorded(t *testing.T) {
	e := newEnv(t)
	e.listen(t, retry.Config{})
	f := attempt(1, failure(vocab.CodeServer, time.Second))
	e.wait(f, 0, false) // nothing scheduled
	must(agent.AttemptFailed.Around(e.root, f, giveUp))
	other := f
	other.Attempt = 2
	e.wait(other, 0, false)
	other = f
	other.Agent.Incarnation = 2
	e.wait(other, 0, true)
	equal(t, e.log.snapshot(), "llm/retry")
	e.wait(f, time.Second, false)
	equal(t, e.log.snapshot(), "llm/retry", "llm/retry-started")
}

// DH-T-LLM-7: the step cap binds every recovery listener: at the cap the inner
// repair listener is never asked, though it would retry anything.
//
//dsh:test DH-T-LLM-7
func TestStepCapBindsEveryListener(t *testing.T) {
	e := newEnv(t)
	e.listen(t, retry.Config{StepAttemptCap: 3})
	repairs := 0
	must(agent.AttemptFailed.On(e.root, func(*cordis.Context, agent.AttemptFailure, func(agent.AttemptFailure) (agent.AttemptDecision, error)) (agent.AttemptDecision, error) {
		repairs++
		return agent.Retry{Reason: "repaired"}, nil
	}))
	// Below the cap a failure this component does not retry reaches the repairer.
	if d := e.ask(t, attempt(2, failure(vocab.CodeContextWindowExceeded, 0))); d != (agent.Retry{Reason: "repaired"}) || repairs != 1 {
		t.Fatalf("below the cap: %#v, repairs %d", d, repairs)
	}
	if d := e.ask(t, attempt(3, failure(vocab.CodeContextWindowExceeded, 0))); d != (agent.GiveUp{}) || repairs != 1 {
		t.Fatalf("at the cap: %#v, repairs %d", d, repairs)
	}
	var a retry.Abandoned
	must(0, json.Unmarshal([]byte(e.log.raws[len(e.log.raws)-1]), &a))
	if a.Reason != llm.AbandonStepCap {
		t.Fatalf("abandoned %+v", a)
	}
}

// DH-T-LLM-7: the driver's own attempt budget is a step cap too: a failure the
// policy would retry, with no attempt left, is abandoned "step-cap" and no
// llm/retry is written that no wait would follow.
//
//dsh:test DH-T-LLM-7
func TestDriverAttemptBudgetIsAStepCap(t *testing.T) {
	e := newEnv(t)
	e.listen(t, retry.Config{Policy: func(agent.AttemptFailure) llm.RetryPolicy { return llm.DefaultRetry() }})
	f := attempt(2, failure(vocab.CodeServer, time.Second))
	f.AttemptsLeft = 0
	if d := e.ask(t, f); d != (agent.GiveUp{}) {
		t.Fatalf("decision %#v", d)
	}
	equal(t, e.log.snapshot(), "llm/retry-abandoned")
	var a retry.Abandoned
	must(0, json.Unmarshal([]byte(e.log.raws[0]), &a))
	if a.Reason != llm.AbandonStepCap {
		t.Fatalf("abandoned %+v", a)
	}
}

// DH-T-LLM-7: persistent elapsed time counts from the step's first attempt
// start (StepStarted), not its first failure: a first attempt that used the
// whole MaxElapsed is not retried.
//
//dsh:test DH-T-LLM-7
func TestPersistentElapsedCountsTheFirstAttempt(t *testing.T) {
	e := newEnv(t)
	e.listen(t, retry.Config{Policy: func(agent.AttemptFailure) llm.RetryPolicy {
		return llm.RetryPolicy{Mode: llm.RetryPersistent, MaxElapsed: 10 * time.Second}
	}})
	f := attempt(1, failure(vocab.CodeServer, time.Second))
	f.StepStarted = e.clk.Now()
	e.clk.Advance(10 * time.Second) // the first provider request took the whole bound
	if d := e.ask(t, f); d != (agent.GiveUp{}) {
		t.Fatalf("decision %#v, want no retry past MaxElapsed", d)
	}
	equal(t, e.log.snapshot(), "llm/retry-abandoned")
	var a retry.Abandoned
	must(0, json.Unmarshal([]byte(e.log.raws[0]), &a))
	if a.Reason != llm.AbandonElapsed {
		t.Fatalf("abandoned %+v", a)
	}

	// The start is kept for the step: a later failure measures from it too.
	f2 := attempt(1, failure(vocab.CodeServer, time.Second))
	f2.Step, f2.StepStarted = 2, e.clk.Now()
	e.ask(t, f2) // waits 1s
	e.clk.Advance(9 * time.Second)
	f3 := f2
	f3.Attempt = 2
	if d := e.ask(t, f3); d != (agent.GiveUp{}) { // 1s wait + 9s request + 1s wait > 10s
		t.Fatalf("second attempt %#v", d)
	}
}

// DH-T-LLM-7: a persistent policy's point without StepStarted fails explicitly,
// with nothing recorded and no timer, rather than measuring MaxElapsed from the
// first failure; a Permanent failure under it is still given up (SP-2); a
// bounded policy needs no start.
//
//dsh:test DH-T-LLM-7
func TestPersistentPolicyRequiresTheStepStart(t *testing.T) {
	e := newEnv(t)
	mode := llm.RetryPersistent
	e.listen(t, retry.Config{Policy: func(agent.AttemptFailure) llm.RetryPolicy {
		return llm.RetryPolicy{Mode: mode, MaxRetries: 3, MaxElapsed: 10 * time.Second}
	}})
	d, err := agent.AttemptFailed.Around(e.root, attempt(1, failure(vocab.CodeServer, time.Second)), giveUp)
	if !errors.Is(err, retry.ErrNoStepStart) || d != nil {
		t.Fatalf("decision %#v, error %v", d, err)
	}
	equal(t, e.log.snapshot())
	if d := e.ask(t, attempt(1, failure(vocab.CodeAuth, 0))); d != (agent.GiveUp{}) {
		t.Fatalf("auth without a start: %#v", d)
	}
	equal(t, e.log.snapshot(), "llm/retry-abandoned")
	mode = llm.RetryBounded
	if d := e.ask(t, attempt(1, failure(vocab.CodeServer, time.Second))); d != (agent.Retry{After: time.Second, Reason: string(vocab.CodeServer)}) {
		t.Fatalf("bounded without a start: %#v", d)
	}
}

// A failed Record hook at the decision fails it closed (llm/retry and an
// abandon); one writing the wait's outcome from the observe-only notice is
// logged and changes nothing, since the driver has already decided.
func TestRecordFailureFailsClosed(t *testing.T) {
	for i, name := range []string{"scheduled", "started", "abandoned"} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			n := 0
			c := must(retry.Listen(e.root, retry.Config{
				Record: func(context.Context, agent.AttemptFailure, event.Draft) error {
					n++
					if (i < 2 && n == i+1) || i == 2 {
						return errors.New("disk full")
					}
					return nil
				},
				Now: e.clk.Now, Jitter: func() float64 { return 0.5 },
			}))
			t.Cleanup(c.Close)
			f := attempt(1, failure(vocab.CodeServer, time.Second))
			if i == 2 {
				f = attempt(1, failure(vocab.CodeAuth, 0))
			}
			d, err := agent.AttemptFailed.Around(e.root, f, giveUp)
			if i != 1 {
				if err == nil {
					t.Fatalf("a failed record was not an error: %#v", d)
				}
				return
			}
			if err != nil || d != (agent.Retry{After: time.Second, Reason: string(vocab.CodeServer)}) {
				t.Fatalf("decision %#v, error %v", d, err)
			}
			e.wait(f, time.Second, false)
			if n != 2 {
				t.Fatalf("%d records, want llm/retry and the attempted llm/retry-started", n)
			}
			logged := false
			for _, m := range e.logs.Messages() {
				logged = logged || (m.Err != nil && strings.Contains(m.Err.Error(), "disk full"))
			}
			if !logged {
				t.Fatal("the failed llm/retry-started record was not logged")
			}
		})
	}
}

// Listen refuses a missing Record hook and a negative cap; a point without a
// failure is an error, not a decision.
func TestListenAndPointValidation(t *testing.T) {
	e := newEnv(t)
	if _, err := retry.Listen(e.root, retry.Config{}); err == nil {
		t.Fatal("Listen without Record")
	}
	if _, err := retry.Listen(e.root, retry.Config{Record: e.log.record, StepAttemptCap: -1}); err == nil {
		t.Fatal("negative cap")
	}
	c := must(retry.Listen(e.root, retry.Config{Record: e.log.record}))
	t.Cleanup(c.Close)
	if _, err := agent.AttemptFailed.Around(e.root, agent.AttemptFailure{Attempt: 1}, giveUp); err == nil {
		t.Fatal("a point with no failure decided")
	}
	if _, err := retry.AbandonedType.Draft(retry.Abandoned{Reason: "bored"}); err == nil {
		t.Fatal("an abandon reason outside the closed set drafted")
	}
	if p := retry.FromAgent(agent.RetryPolicy{MaxAttempts: 3, BaseDelay: time.Second}); p.MaxRetries != 2 || p.Mode != llm.RetryBounded {
		t.Fatalf("FromAgent %+v", p)
	}
}
