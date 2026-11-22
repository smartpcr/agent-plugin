package procwait_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
)

// The poll helper is tested on virtual time: inside a synctest bubble its ticker and
// every deadline are virtual, so the tests neither sleep nor depend on the host's
// speed (M1-G-GATE-24, M1-D-GATE-24; DH-T-TOOLCHAIN-8).

// counter is a fact that holds from its n-th check on.
type counter struct{ calls, holdsFrom int }

func (c *counter) fact() bool { c.calls++; return c.calls >= c.holdsFrom }

func TestPollReturnsAtOnceWhenTheFactHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &counter{holdsFrom: 1}
		start := time.Now()
		if err := procwait.Poll(t.Context(), c.fact); err != nil {
			t.Fatalf("Poll = %v", err)
		}
		if c.calls != 1 || time.Since(start) != 0 {
			t.Fatalf("checked %d times over %v, want once and no wait", c.calls, time.Since(start))
		}
	})
}

func TestPollChecksEveryIntervalUntilTheFactHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &counter{holdsFrom: 4}
		start := time.Now()
		if err := procwait.Poll(t.Context(), c.fact); err != nil {
			t.Fatalf("Poll = %v", err)
		}
		if c.calls != 4 {
			t.Fatalf("checked %d times, want 4", c.calls)
		}
		if got, want := time.Since(start), 3*procwait.Interval; got != want {
			t.Fatalf("waited %v, want three intervals (%v)", got, want)
		}
	})
}

func TestPollStopsAtTheContextDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 25*procwait.Interval)
		defer cancel()
		c := &counter{holdsFrom: math.MaxInt}
		start := time.Now()
		err := procwait.Poll(ctx, c.fact)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Poll = %v, want context.DeadlineExceeded", err)
		}
		if got := time.Since(start); got != 25*procwait.Interval {
			t.Fatalf("gave up after %v, want the deadline %v", got, 25*procwait.Interval)
		}
		calls := c.calls
		synctest.Wait()
		if c.calls != calls {
			t.Fatal("the fact was checked after Poll returned")
		}
	})
}

func TestPollWithoutDeadlineIsBoundedByTheDefault(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		err := procwait.Poll(context.Background(), func() bool { return false })
		if err == nil || !strings.Contains(err.Error(), "default") {
			t.Fatalf("Poll = %v, want the default-timeout cause", err)
		}
		if got := time.Since(start); got != procwait.DefaultTimeout {
			t.Fatalf("gave up after %v, want DefaultTimeout (%v)", got, procwait.DefaultTimeout)
		}
	})
}

func TestPollReportsTheCancelCause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gone := errors.New("the child exited before writing its pid file")
		ctx, cancel := context.WithCancelCause(t.Context())
		checks := 0
		err := procwait.Poll(ctx, func() bool {
			if checks++; checks == 2 {
				cancel(gone)
			}
			return false
		})
		if !errors.Is(err, gone) {
			t.Fatalf("Poll = %v, want the cause %v", err, gone)
		}
	})
}

// fakeTB records Fatal instead of stopping the goroutine, so the test can observe
// how Until fails its caller.
type fakeTB struct {
	testing.TB
	fatal []string
}

func (f *fakeTB) Helper()                      {}
func (f *fakeTB) Fatal(args ...any)            { f.fatal = append(f.fatal, fmt.Sprint(args...)) }
func (f *fakeTB) Fatalf(s string, args ...any) { f.fatal = append(f.fatal, fmt.Sprintf(s, args...)) }

func TestUntilFailsTheTestOnlyWhenTheFactNeverHolds(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ok := &fakeTB{TB: t}
		procwait.Until(t.Context(), ok, (&counter{holdsFrom: 3}).fact)
		if len(ok.fatal) != 0 {
			t.Fatalf("a fact that holds failed the test: %v", ok.fatal)
		}

		ctx, cancel := context.WithTimeout(t.Context(), procwait.Interval)
		defer cancel()
		never := &fakeTB{TB: t}
		procwait.Until(ctx, never, func() bool { return false })
		if len(never.fatal) != 1 || !strings.Contains(never.fatal[0], "did not hold") {
			t.Fatalf("Until reported %q, want one \"did not hold\" failure", never.fatal)
		}
	})
}
