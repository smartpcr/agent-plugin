// Package procwait is the test kit's one poll helper (M1-D-GATE-24, option A). A
// test waits on in-process work through an explicit signal (a channel, a test hook,
// a process's done channel) or on virtual time in a testing/synctest bubble; it never
// sleeps (the nosleep analyzer, QG-4). Some facts have no in-process signal: that a
// real child process has written its pid file, that another process's log holds a
// line, that a port another process owns is accepting. For those, and only those, a
// test polls the fact with Until or Poll.
//
// Every wait is bounded: by the context's deadline, or by DefaultTimeout when the
// context has none.
package procwait

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// Interval is the poll period: how long Until waits between two checks of the fact.
const Interval = 10 * time.Millisecond

// DefaultTimeout bounds a wait whose context has no deadline.
const DefaultTimeout = 30 * time.Second

// Poll checks fact at once and then every Interval until it returns true, and
// returns nil. It returns an error naming the context's cause when ctx ends first,
// or when DefaultTimeout passes and ctx has no deadline; fact is not called again
// after that.
func Poll(ctx context.Context, fact func() bool) error {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeoutCause(ctx, DefaultTimeout,
			fmt.Errorf("procwait: no deadline, stopped after the default %s", DefaultTimeout))
		defer cancel()
	}
	tick := time.NewTicker(Interval)
	defer tick.Stop()
	for {
		if fact() {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("procwait: the fact did not hold: %w", context.Cause(ctx))
		case <-tick.C:
		}
	}
}

// Until is Poll that fails t (t.Fatal, from the calling test goroutine) when the
// fact does not hold before ctx ends. Use it only for a fact about a real child
// process or another process's log; in-process waits use a signal or synctest.
func Until(ctx context.Context, t testing.TB, fact func() bool) {
	t.Helper()
	if err := Poll(ctx, fact); err != nil {
		t.Fatal(err)
	}
}
