package clock_test

import (
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
)

// TC-06a: a component of a kit tree waiting 30 s on the virtual clock completes
// as soon as the clock is advanced 30 s, in well under 100 ms of wall time; it
// does not complete a nanosecond early.
func TestVirtualClockDrivesDeadlinesWithoutSleeping(t *testing.T) {
	root := testkit.Tree(t)
	c := clock.NewManual(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	done := make(chan time.Time, 1)
	fiber, err := root.Apply("waiter", func(ctx *cordis.Context) error {
		deadline := c.After(30 * time.Second)
		go func() {
			select {
			case at := <-deadline:
				done <- at
			case <-ctx.Lifetime().Done():
			}
		}()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fiber.Dispose)
	if err := fiber.Wait(); err != nil {
		t.Fatal(err)
	}
	if !c.BlockUntil(1, time.Second) {
		t.Fatal("the component never waited on the clock")
	}

	start := time.Now()
	c.Advance(30*time.Second - time.Nanosecond)
	select {
	case <-done:
		t.Fatal("the wait completed before its deadline")
	default:
	}
	c.Advance(time.Nanosecond)
	select {
	case at := <-done:
		if want := time.Date(2026, 1, 1, 0, 0, 30, 0, time.UTC); !at.Equal(want) {
			t.Errorf("the wait completed at %v, want %v", at, want)
		}
	case <-time.After(time.Second):
		t.Fatal("advancing 30 s did not complete the wait")
	}
	if wall := time.Since(start); wall >= 100*time.Millisecond {
		t.Errorf("the 30 s wait took %v of wall time, want under 100 ms", wall)
	}
}
