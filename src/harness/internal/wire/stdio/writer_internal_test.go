package stdio

import (
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// gateWriter blocks every write until release is closed and counts the writes
// that started.
type gateWriter struct {
	mu      sync.Mutex
	calls   int
	release chan struct{}
}

func (g *gateWriter) Write(p []byte) (int, error) {
	g.mu.Lock()
	g.calls++
	g.mu.Unlock()
	<-g.release
	return len(p), nil
}

// A write that passed the first end check before the writer stalled, and reaches
// the turn only after the stalled write returned and freed it, finds the turn and
// the end both ready; it must be refused every time and never reach the output.
//
//dsh:test DH-T-SDK-22
func TestNoWriteStartsAfterTheEnd(t *testing.T) {
	for i := range 32 {
		synctest.Test(t, func(t *testing.T) {
			out := &gateWriter{release: make(chan struct{})}
			release := sync.OnceFunc(func() { close(out.release) })
			t.Cleanup(release) // a failed run must not leave the output blocked
			stops := 0
			w := NewWriter(out, time.Second, func(launch.Reason, int) { stops++ })
			paused, resumed := make(chan struct{}), make(chan struct{})
			resume := sync.OnceFunc(func() { close(resumed) })
			t.Cleanup(resume)
			var admitted atomic.Int32
			w.admit = func() {
				if admitted.Add(1) == 2 {
					close(paused)
					<-resumed
				}
			}
			first := make(chan error, 1)
			go func() {
				_, err := w.Write([]byte("a\n"))
				first <- err
			}()
			synctest.Wait()
			second := make(chan error, 1)
			go func() {
				_, err := w.Write([]byte("b\n"))
				second <- err
			}()
			<-paused
			if err := <-first; !herr.Has(err, ReasonClientStalled) {
				t.Fatalf("run %d: first write: %v, want %s", i, err, ReasonClientStalled)
			}
			// The stalled write returns and frees the turn; then the paused write resumes.
			release()
			synctest.Wait()
			resume()
			if err := <-second; !herr.Has(err, ReasonClientStalled) {
				t.Fatalf("run %d: second write: %v, want %s", i, err, ReasonClientStalled)
			}
			synctest.Wait()
			out.mu.Lock()
			defer out.mu.Unlock()
			if out.calls != 1 {
				t.Fatalf("run %d: %d writes reached the output, want 1: a write started after the end", i, out.calls)
			}
			if stops != 1 {
				t.Fatalf("run %d: stop asked %d times, want 1", i, stops)
			}
		})
	}
}
