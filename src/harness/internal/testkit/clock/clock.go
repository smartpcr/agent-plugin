// Package clock is the test kit's virtual time. Run runs a test body in a
// testing/synctest bubble, where the time package itself is fake and Advance and
// Until move it; that is the form DH-D-UTIL-6 specifies, and a component under
// test needs no clock parameter. Manual is the older explicit clock, for a
// component that takes a boot.Clock: time moves only when a test advances it, so
// a bound such as the 20 s shutdown budget passes in no wall time.
package clock

import (
	"sync"
	"time"
)

// Manual is a clock that only moves on Advance. It satisfies boot.Clock.
type Manual struct {
	mu      sync.Mutex
	now     time.Time
	waiters []waiter
	changed chan struct{}
}

type waiter struct {
	at time.Time
	c  chan time.Time
}

// NewManual returns a manual clock reading start.
func NewManual(start time.Time) *Manual {
	return &Manual{now: start, changed: make(chan struct{})}
}

// Now returns the clock's time.
func (m *Manual) Now() time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.now
}

// After returns a channel that receives the time once the clock has advanced by d.
func (m *Manual) After(d time.Duration) <-chan time.Time {
	m.mu.Lock()
	defer m.mu.Unlock()
	c := make(chan time.Time, 1)
	if d <= 0 {
		c <- m.now
		return c
	}
	m.waiters = append(m.waiters, waiter{at: m.now.Add(d), c: c})
	close(m.changed)
	m.changed = make(chan struct{})
	return c
}

// Advance moves the clock by d and fires every timer that is due.
func (m *Manual) Advance(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.now = m.now.Add(d)
	kept := m.waiters[:0]
	for _, w := range m.waiters {
		if w.at.After(m.now) {
			kept = append(kept, w)
			continue
		}
		w.c <- m.now
	}
	m.waiters = kept
}

// Waiters returns how many timers are pending.
func (m *Manual) Waiters() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.waiters)
}

// BlockUntil waits until at least n timers are pending, or the timeout of wall time
// passes; it reports whether they are.
func (m *Manual) BlockUntil(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		m.mu.Lock()
		count, changed := len(m.waiters), m.changed
		m.mu.Unlock()
		if count >= n {
			return true
		}
		select {
		case <-changed:
		case <-deadline:
			return false
		}
	}
}
