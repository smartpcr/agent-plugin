package agent_test

import (
	"testing"
	"time"
)

// goJoined runs fn on a new goroutine and registers its release with t.Cleanup
// right after the start (P-C, tools/harness-gate/README.md "Waiting and releasing
// in tests"): the cleanup calls unblock, which must make fn return (nil when fn
// returns by itself), then joins fn within 10 s. The returned channel is closed
// when fn returns.
func goJoined(t *testing.T, unblock func(), fn func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() {
		if unblock != nil {
			unblock()
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("a released test goroutine did not return within 10s")
		}
	})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}
