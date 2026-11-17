// Package timer provides the "timer" service: timeouts, intervals, sleeps, throttles
// and debounces whose lifetime is owned by the calling fiber. When the component that
// scheduled a timer unloads, the timer stops; no scheduled call runs after its owner
// has unloaded.
package timer

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Key is the typed key of the timer service.
var Key = cordis.NewServiceKey[*Service]("timer")

// Component provides the timer service.
var Component = cordis.Construct("TimerService", func(ctx *cordis.Context, _ cordis.None) (*Service, error) {
	service := &Service{ctx: ctx}
	if _, err := Key.Provide(ctx, service); err != nil {
		return nil, err
	}
	return service, nil
})

// Service schedules work on behalf of the fiber that resolved it: Get returns a copy
// bound to the caller's context, so every timer is an effect of the caller.
type Service struct {
	ctx *cordis.Context
}

// ForContext returns the service bound to the calling context.
func (s *Service) ForContext(ctx *cordis.Context) any {
	if ctx == s.ctx {
		return s
	}
	return &Service{ctx: ctx}
}

// Timeout runs callback once after delay. Disposing the handle, or unloading the
// owner, cancels it.
func (s *Service) Timeout(callback func(), delay time.Duration) (*cordis.EffectHandle, error) {
	var handle atomic.Pointer[cordis.EffectHandle]
	h, err := s.ctx.Effect("ctx.Timeout()", func() (cordis.Disposer, error) {
		timer := time.AfterFunc(delay, func() {
			if h := handle.Load(); h != nil {
				// a tick already due when the owner unloaded must not run
				if !h.Alive() {
					return
				}
				h.Dispose()
			}
			callback()
		})
		return func() error {
			timer.Stop()
			return nil
		}, nil
	})
	handle.Store(h)
	return h, err
}

// Interval runs callback every period until the handle is disposed or the owner
// unloads.
func (s *Service) Interval(callback func(), period time.Duration) (*cordis.EffectHandle, error) {
	var handle atomic.Pointer[cordis.EffectHandle]
	h, err := s.ctx.Effect("ctx.Interval()", func() (cordis.Disposer, error) {
		ticker := time.NewTicker(period)
		stop := make(chan struct{})
		go func() {
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					// a tick already due when the owner unloaded must not run
					if h := handle.Load(); h != nil && !h.Alive() {
						return
					}
					callback()
				}
			}
		}()
		return func() error {
			ticker.Stop()
			close(stop)
			return nil
		}, nil
	})
	handle.Store(h)
	return h, err
}

// Sleep blocks for delay. It returns cordis.ErrInactive early if the owner unloads
// first.
func (s *Service) Sleep(delay time.Duration) error {
	cancelled := make(chan struct{})
	handle, err := s.ctx.Effect("ctx.Sleep()", func() (cordis.Disposer, error) {
		return func() error {
			close(cancelled)
			return nil
		}, nil
	})
	if err != nil {
		return err
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		handle.Dispose()
		return nil
	case <-cancelled:
		return cordis.ErrInactive
	}
}

// Debounce returns a function that runs callback once calls have stopped for delay.
// Each call restarts the wait. Pending calls are dropped when the owner unloads.
func (s *Service) Debounce(callback func(), delay time.Duration) (func(), error) {
	schedule, err := newSchedule(s.ctx, "ctx.Debounce()")
	if err != nil {
		return nil, err
	}
	return func() {
		schedule.invoke(func(time.Duration) (time.Duration, func(), bool) {
			return delay, callback, true
		})
	}, nil
}

// Throttle returns a function that runs callback at most once per period. The first
// call runs at once; later calls inside the window collapse into one trailing call
// at the end of the window, unless noTrailing is set, in which case they are dropped.
// Windows are measured on a monotonic clock.
func (s *Service) Throttle(callback func(), period time.Duration, noTrailing bool) (func(), error) {
	schedule, err := newSchedule(s.ctx, "ctx.Throttle()")
	if err != nil {
		return nil, err
	}
	var last time.Duration
	called := false
	var mu sync.Mutex
	execute := func() {
		mu.Lock()
		last, called = schedule.now(), true
		mu.Unlock()
		callback()
	}
	return func() {
		schedule.invoke(func(now time.Duration) (time.Duration, func(), bool) {
			mu.Lock()
			remaining := time.Duration(0)
			if called {
				remaining = period - (now - last)
			}
			mu.Unlock()
			if remaining <= 0 {
				return 0, execute, false
			}
			if noTrailing {
				return 0, nil, false
			}
			return remaining, execute, true
		})
	}, nil
}

// schedule holds at most one pending call, dropped on disposal.
type schedule struct {
	mu         sync.Mutex
	start      time.Time
	pending    *time.Timer
	generation uint64
	disposed   bool
}

func newSchedule(ctx *cordis.Context, label string) (*schedule, error) {
	s := &schedule{start: time.Now()}
	_, err := ctx.Effect(label, func() (cordis.Disposer, error) {
		return func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.disposed = true
			s.generation++
			if s.pending != nil {
				s.pending.Stop()
				s.pending = nil
			}
			return nil
		}, nil
	})
	if err != nil {
		return nil, err
	}
	return s, nil
}

// now is the time since the schedule was created, on the monotonic clock.
func (s *schedule) now() time.Duration { return time.Since(s.start) }

// invoke replaces the pending call with what trigger decides: run now, run after a
// delay (deferred), or nothing (run == nil).
func (s *schedule) invoke(trigger func(now time.Duration) (delay time.Duration, run func(), deferred bool)) {
	var runNow func()
	s.mu.Lock()
	if s.disposed {
		s.mu.Unlock()
		return
	}
	s.generation++
	if s.pending != nil {
		s.pending.Stop()
		s.pending = nil
	}
	delay, run, deferred := trigger(s.now())
	switch {
	case run == nil:
	case !deferred:
		runNow = run
	default:
		generation := s.generation
		s.pending = time.AfterFunc(delay, func() { s.fire(generation, run) })
	}
	s.mu.Unlock()

	// Outside the lock: a re-entering callback may schedule a trailing call, and that
	// call must survive this invocation returning (#224).
	if runNow != nil {
		runNow()
	}
}

func (s *schedule) fire(generation uint64, run func()) {
	s.mu.Lock()
	if s.disposed || generation != s.generation {
		s.mu.Unlock()
		return
	}
	s.pending = nil
	s.mu.Unlock()
	run()
}
