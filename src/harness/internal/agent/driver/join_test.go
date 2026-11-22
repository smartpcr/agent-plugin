package driver_test

import (
	"sync"
	"testing"
	"time"
)

// joinTimeout bounds the join of a released test goroutine.
const joinTimeout = 10 * time.Second

// goJoined runs fn on a new goroutine and registers its release with t.Cleanup
// right after the start (P-C, tools/harness-gate/README.md "Waiting and releasing
// in tests"). The cleanup calls unblock, which must make fn return (nil when fn
// returns by itself), then joins fn, failing the test if fn has not returned
// within joinTimeout. So a test that fails half-way still releases and joins every
// goroutine it started. The returned channel is closed when fn returns.
func goJoined(t testing.TB, unblock func(), fn func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() {
		if unblock != nil {
			unblock()
		}
		select {
		case <-done:
		case <-time.After(joinTimeout):
			t.Errorf("a released test goroutine did not return within %s", joinTimeout)
		}
	})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

// closeOnce returns a release that closes ch once, however often it is called,
// so the test body and a goJoined cleanup can both release the same gate.
func closeOnce(ch chan struct{}) func() {
	return sync.OnceFunc(func() { close(ch) })
}
