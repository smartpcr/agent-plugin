package answerer_test

import (
	"fmt"
	"runtime"
	"sync"
	"testing"
)

// recorder is a testing.TB that records failures instead of failing the real
// test, and holds its cleanups until cleanup is called, so a test can check what
// a kit helper reports on a path that fails its caller. Everything else (TempDir,
// Name, Log) is the real test's.
type recorder struct {
	testing.TB
	mu       sync.Mutex
	failures []string
	fatal    bool
	cleanups []func()
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	r.mu.Lock()
	r.fatal = true
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recorder) Error(args ...any) { r.Errorf("%s", fmt.Sprint(args...)) }

func (r *recorder) Fatal(args ...any) { r.Fatalf("%s", fmt.Sprint(args...)) }

func (r *recorder) Cleanup(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups = append(r.cleanups, f)
}

// run calls fn on its own goroutine, so a Fatalf ends fn alone, and waits for it.
// It reports whether fn was ended by a Fatalf.
func (r *recorder) run(fn func()) (fatal bool) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatal
}

// cleanup runs the recorded cleanups, last first.
func (r *recorder) cleanup() {
	r.mu.Lock()
	cs := r.cleanups
	r.cleanups = nil
	r.mu.Unlock()
	for i := len(cs) - 1; i >= 0; i-- {
		cs[i]()
	}
}

func (r *recorder) failed() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.failures...)
}
