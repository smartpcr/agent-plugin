package cordis_test

import (
	"sync"
	"testing"

	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

// goJoined runs fn on a new goroutine and registers its release with t.Cleanup right
// after the start (the P-C idiom of tools/harness-gate/README.md). The cleanup first
// calls unblock, which must make fn return (it closes the gate fn waits on), then
// joins fn, failing the test if fn has not returned within Timeout. So a test that
// fails half-way still releases and joins every goroutine it started. unblock must be
// safe to call twice: the test body usually calls it too (see closeOnce). The
// returned channel is closed when fn returns.
func goJoined(t *testing.T, unblock func(), fn func()) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() {
		unblock()
		Within(t, "joining a released test goroutine", func() { <-done })
	})
	go func() {
		defer close(done)
		fn()
	}()
	return done
}

// closeOnce returns a release that closes ch once, however often it is called, so the
// test body and a goJoined cleanup can both release the same gate.
func closeOnce(ch chan struct{}) func() {
	return sync.OnceFunc(func() { close(ch) })
}
