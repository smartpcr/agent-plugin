package clock

import (
	"testing"
	"testing/synctest"
	"time"
)

// Run runs fn in a testing/synctest bubble (DH-D-UTIL-6): inside it the time
// package reads a fake clock that starts at midnight UTC 2000-01-01 and moves
// only when every goroutine of the bubble is durably blocked, so a component's
// own timers, tickers and context deadlines pass in no wall time. No component
// takes a clock: the bubble is the virtual time (util-test-support.md §10 item 2).
//
// Run returns once every goroutine started in the bubble has ended; one still
// blocked when fn returns is a deadlock, and the test fails. That is the kit's
// goroutine-leak rule. fn's t.Cleanup functions run inside the bubble before Run
// returns, so a release registered there may still wait on bubble channels. fn's
// t must not call Run, Parallel or Deadline (synctest.Test), and Run must not be
// called inside another bubble.
func Run(t *testing.T, fn func(t *testing.T)) {
	t.Helper()
	synctest.Test(t, fn)
}

// Advance lets d of virtual time pass and then waits until every other goroutine
// of the bubble is durably blocked again, so each timer due by then has fired and
// its receiver has run to its next wait. It is the kit's one sleep helper: it is
// called only inside Run, where it takes no wall time. A d of zero or less only
// waits for the bubble to settle.
func Advance(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
	synctest.Wait()
}

// Until advances the bubble's clock to date, as Advance(time.Until(date)): a date
// already past only waits for the bubble to settle.
func Until(date time.Time) { Advance(time.Until(date)) }

// Settle waits until every other goroutine of the bubble is durably blocked,
// without moving the clock (synctest.Wait). Call it only inside Run.
func Settle() { synctest.Wait() }
