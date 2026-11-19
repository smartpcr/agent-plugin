package timer_test

import (
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/go/timer"
)

// withTimer loads the timer service, then a fiber that injects it and runs body.
func withTimer(t *testing.T, body func(ctx *cordis.Context, service *timer.Service) error) (*cordis.Context, *cordis.Fiber) {
	t.Helper()
	root := cordis.New()
	MustWait(t, Must(root.Use(timer.Component, nil)))
	fiber := MustWait(t, Must(root.Inject([]string{timer.Key.Name()}, func(ctx *cordis.Context) error {
		service, err := timer.Key.Get(ctx)
		if err != nil {
			return err
		}
		return body(ctx, service)
	})))
	return root, fiber
}

func TestTimersAreOwnedByTheCallingFiber(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var ticks atomic.Int32
		_, fiber := withTimer(t, func(_ *cordis.Context, service *timer.Service) error {
			_, err := service.Interval(func() { ticks.Add(1) }, 10*time.Millisecond)
			return err
		})
		Eventually(t, func() bool { return ticks.Load() > 2 }, "interval never fired")
		Within(t, "dispose", fiber.Dispose)

		settled := ticks.Load()
		Advance(60 * time.Millisecond)
		Equal(t, settled, ticks.Load(), "timer kept firing after its owner unloaded")
	})
}

func TestTimeoutRunsOnceAndSleepIsCancelledByUnload(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var fired atomic.Int32
		sleep := make(chan error, 1)
		root, fiber := withTimer(t, func(ctx *cordis.Context, service *timer.Service) error {
			if _, err := service.Timeout(func() { fired.Add(1) }, 10*time.Millisecond); err != nil {
				return err
			}
			_, err := ctx.EffectAsync("sleeper", func() (cordis.Disposer, error) {
				go func() { sleep <- service.Sleep(time.Hour) }()
				return nil, nil
			})
			return err
		})
		// Unloading the tree cancels the sleeper even when the test stops early.
		t.Cleanup(func() { Within(t, "unload", root.Dispose) })
		Eventually(t, func() bool { return fired.Load() == 1 }, "timeout never fired")
		Within(t, "dispose", fiber.Dispose)
		Within(t, "sleep", func() { Equal(t, cordis.ErrInactive, <-sleep) })
		Equal(t, int32(1), fired.Load())
	})
}

func TestThrottleRunsTheFirstCallAndOneTrailingCallPerWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var throttled func()
		_, fiber := withTimer(t, func(_ *cordis.Context, service *timer.Service) (err error) {
			throttled, err = service.Throttle(func() { calls.Add(1) }, 80*time.Millisecond, false)
			return err
		})
		throttled()
		throttled()
		throttled()
		Equal(t, int32(1), calls.Load())
		Eventually(t, func() bool { return calls.Load() == 2 }, "trailing call never ran")
		Advance(120 * time.Millisecond)
		Equal(t, int32(2), calls.Load())
		Within(t, "dispose", fiber.Dispose)
	})
}

func TestThrottleWithNoTrailingDropsCallsInsideTheWindow(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var throttled func()
		withTimer(t, func(_ *cordis.Context, service *timer.Service) (err error) {
			throttled, err = service.Throttle(func() { calls.Add(1) }, 50*time.Millisecond, true)
			return err
		})
		throttled()
		throttled()
		Advance(100 * time.Millisecond)
		Equal(t, int32(1), calls.Load())
	})
}

func TestNoTrailingThrottleOrDebounceCallRunsAfterTheOwnerUnloads(t *testing.T) { // #161
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var throttled, debounced func()
		_, fiber := withTimer(t, func(_ *cordis.Context, service *timer.Service) (err error) {
			if throttled, err = service.Throttle(func() { calls.Add(1) }, 40*time.Millisecond, false); err != nil {
				return err
			}
			debounced, err = service.Debounce(func() { calls.Add(1) }, 40*time.Millisecond)
			return err
		})
		throttled()
		throttled() // schedules a trailing call
		debounced() // schedules a debounced call
		Within(t, "dispose", fiber.Dispose)
		Advance(100 * time.Millisecond)
		Equal(t, int32(1), calls.Load())
		throttled()
		Equal(t, int32(1), calls.Load())
	})
}

func TestDebounceRunsOnceCallsStop(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var debounced func()
		withTimer(t, func(_ *cordis.Context, service *timer.Service) (err error) {
			debounced, err = service.Debounce(func() { calls.Add(1) }, 30*time.Millisecond)
			return err
		})
		for range 5 {
			debounced()
			Advance(5 * time.Millisecond)
		}
		Equal(t, int32(0), calls.Load())
		Eventually(t, func() bool { return calls.Load() == 1 }, "debounced call never ran")
		Advance(60 * time.Millisecond)
		Equal(t, int32(1), calls.Load())
	})
}

func TestAReEnteringThrottledCallbackKeepsItsTrailingCall(t *testing.T) { // #224
	synctest.Test(t, func(t *testing.T) {
		var calls atomic.Int32
		var throttled func()
		withTimer(t, func(_ *cordis.Context, service *timer.Service) (err error) {
			throttled, err = service.Throttle(func() {
				if calls.Add(1) == 1 {
					throttled() // re-enter inside the window
				}
			}, 40*time.Millisecond, false)
			return err
		})
		throttled()
		Eventually(t, func() bool { return calls.Load() == 2 }, "re-entrant trailing call was lost")
	})
}
