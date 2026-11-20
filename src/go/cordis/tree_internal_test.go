package cordis

import (
	"testing"
	"time"
)

// A barrier is not held back by a notification posted after its target, even one that
// is still running once the target has returned.
func TestBarrierIgnoresNotificationsPastItsTarget(t *testing.T) {
	tr := New().t
	started, gate := make(chan struct{}), make(chan struct{})
	defer close(gate)
	target := tr.post(func() {})
	tr.post(func() { close(started); <-gate })
	tr.flush()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("the later notification did not start")
	}
	done := make(chan struct{})
	go func() { tr.barrier(target-1, target); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the barrier waited for a notification past its target")
	}
}
