// Package watchtest provides a fake watch.Notifier for tests of the watch service
// and of its consumers: events and errors are sent by hand, Add can be made to
// fail, and a silent notifier delivers nothing.
package watchtest

import (
	"slices"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// Fake is a scriptable Notifier.
type Fake struct {
	// AddErr, when set, is returned by Add for the directories it is called with.
	AddErr func(dir string) error

	mu     sync.Mutex
	dirs   map[string]int
	events chan string
	errs   chan error
	closed bool
}

// New is an empty Fake.
func New() *Fake {
	return &Fake{dirs: map[string]int{}, events: make(chan string, 64), errs: make(chan error, 8)}
}

// Factory is f as an Options.NewNotifier.
func (f *Fake) Factory() func() (watch.Notifier, error) {
	return func() (watch.Notifier, error) { return f, nil }
}

// Add records dir unless AddErr refuses it.
func (f *Fake) Add(dir string) error {
	if f.AddErr != nil {
		if err := f.AddErr(dir); err != nil {
			return err
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dirs[dir]++
	return nil
}

// Remove forgets dir.
func (f *Fake) Remove(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.dirs[dir]--; f.dirs[dir] <= 0 {
		delete(f.dirs, dir)
	}
	return nil
}

// Events is the event channel Send feeds.
func (f *Fake) Events() <-chan string { return f.events }

// Errors is the error channel Fail feeds.
func (f *Fake) Errors() <-chan error { return f.errs }

// Close marks the fake closed.
func (f *Fake) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	return nil
}

// Send delivers an event for path.
func (f *Fake) Send(path string) { f.events <- path }

// Fail delivers err (watch.ErrOverflow for an overflow).
func (f *Fake) Fail(err error) { f.errs <- err }

// Watched lists the directories currently added, sorted.
func (f *Fake) Watched() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.dirs))
	for d := range f.dirs {
		out = append(out, d)
	}
	slices.Sort(out)
	return out
}

// Closed reports whether Close was called.
func (f *Fake) Closed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}
