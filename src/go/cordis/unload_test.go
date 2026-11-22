package cordis_test

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

func isInactive(err error) bool { return errors.Is(err, cordis.ErrInactive) }

// DH-T-CORDIS-1: a cleanup cannot register anything on its unloading fiber, so the
// next load starts with an empty effect list.
//
//dsh:test DH-T-CORDIS-1
func TestCleanupCannotRegisterWhileItsFiberUnloads(t *testing.T) {
	root, _ := Quiet()
	ping := cordis.NewEvent[int]("ping")
	var mu sync.Mutex
	var refused []error
	var loads atomic.Int32
	fiber := Must(root.Apply("registrar", func(ctx *cordis.Context) error {
		loads.Add(1)
		_, err := ctx.Effect("registrar", func() (cordis.Disposer, error) {
			return func() error {
				_, effectErr := ctx.Effect("late", func() (cordis.Disposer, error) { return nil, nil })
				_, provideErr := ctx.Provide("late", 1)
				_, onErr := ping.On(ctx, func(*cordis.Context, int) error { return nil })
				_, useErr := ctx.Use(Echo(&Log{}), nil)
				mu.Lock()
				refused = append(refused, effectErr, provideErr, onErr, useErr)
				mu.Unlock()
				return nil
			}, nil
		})
		return err
	}))
	MustWait(t, fiber)
	Equal(t, 1, len(fiber.Effects()), "effects after the first load")

	Within(t, "restart", func() { NoError(t, fiber.Restart()) })
	Equal(t, 2, int(loads.Load()), "loads")
	Equal(t, 1, len(fiber.Effects()), "effects after the restart")

	Within(t, "dispose", fiber.Dispose)
	Equal(t, cordis.Disposed, fiber.State())
	mu.Lock()
	defer mu.Unlock()
	Equal(t, 8, len(refused), "registrations attempted by cleanups")
	for i, err := range refused {
		True(t, isInactive(err), "registration "+[]string{"Effect", "Provide", "On", "Use"}[i%4]+" was not refused: "+errString(err))
	}
}

// TC-02a: concurrent disposals both return only after every cleanup ran exactly once,
// and the disposed fiber refuses new effects.
func TestConcurrentDisposeWaitsForEveryCleanup(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root, _ := Quiet()
		release := make(chan struct{})
		unblock := closeOnce(release)
		var ran [3]atomic.Int32
		fiber := Must(root.Apply("blocking", func(ctx *cordis.Context) error {
			for i := range ran {
				if _, err := ctx.Effect("block", func() (cordis.Disposer, error) {
					return func() error {
						<-release
						ran[i].Add(1)
						return nil
					}, nil
				}); err != nil {
					return err
				}
			}
			return nil
		}))
		MustWait(t, fiber)

		allRanOnce := func() bool {
			for i := range ran {
				if ran[i].Load() != 1 {
					return false
				}
			}
			return true
		}
		results := make(chan bool, 2)
		for range 2 {
			goJoined(t, unblock, func() {
				fiber.Dispose()
				results <- allRanOnce()
			})
		}
		synctest.Wait()
		select {
		case <-results:
			t.Fatal("Dispose returned while cleanups were blocked")
		default:
		}
		unblock()
		for range 2 {
			Within(t, "dispose", func() { True(t, <-results, "Dispose returned before every cleanup ran once") })
		}
		Within(t, "wait", func() { NoError(t, fiber.Wait()) })
		True(t, allRanOnce(), "a cleanup ran more than once")
		_, err := fiber.Ctx().Effect("after", func() (cordis.Disposer, error) { return nil, nil })
		True(t, isInactive(err), "a disposed fiber accepted an effect: "+errString(err))
	})
}

// A dependent's cleanup sees its provider's resources live; the provider's run after.
func TestDependentUnloadsBeforeItsProvider(t *testing.T) {
	root, _ := Quiet()
	log := &Log{}
	var closed atomic.Bool
	var sawLive atomic.Bool
	provider := Must(root.Apply("provider", func(ctx *cordis.Context) error {
		if _, err := ctx.Provide("svc", "value"); err != nil {
			return err
		}
		_, err := ctx.Effect("resource", func() (cordis.Disposer, error) {
			return func() error {
				closed.Store(true)
				log.Add("provider")
				return nil
			}, nil
		})
		return err
	}))
	consumer := Must(root.Apply("consumer", func(ctx *cordis.Context) error {
		_, err := ctx.Effect("use", func() (cordis.Disposer, error) {
			return func() error {
				sawLive.Store(!closed.Load())
				log.Add("consumer")
				return nil
			}, nil
		})
		return err
	}, "svc"))
	MustWait(t, provider)
	MustWait(t, consumer)
	Equal(t, cordis.Active, consumer.State())

	Within(t, "dispose provider", provider.Dispose)
	Equal(t, "consumer,provider", log.String())
	True(t, sawLive.Load(), "the dependent's cleanup saw its provider released")
}

// DH-T-CORDIS-2: a cleanup started by one goroutine holds back a second disposal of
// the same handle and the unload of the fiber's providers until it returns.
//
//dsh:test DH-T-CORDIS-2
func TestRunningCleanupHoldsBackSecondDisposeAndProvider(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root, _ := Quiet()
		log := &Log{}
		var closed atomic.Bool
		var sawLive atomic.Bool
		entered := make(chan struct{})
		release := make(chan struct{})
		unblock := closeOnce(release)
		provider := Must(root.Apply("provider", func(ctx *cordis.Context) error {
			if _, err := ctx.Provide("svc", "value"); err != nil {
				return err
			}
			_, err := ctx.Effect("resource", func() (cordis.Disposer, error) {
				return func() error {
					closed.Store(true)
					log.Add("provider")
					return nil
				}, nil
			})
			return err
		}))
		var handle atomic.Pointer[cordis.EffectHandle]
		consumer := Must(root.Apply("consumer", func(ctx *cordis.Context) error {
			h, err := ctx.Effect("blocking", func() (cordis.Disposer, error) {
				return func() error {
					close(entered)
					<-release
					sawLive.Store(!closed.Load())
					log.Add("consumer")
					return nil
				}, nil
			})
			handle.Store(h)
			return err
		}, "svc"))
		MustWait(t, provider)
		MustWait(t, consumer)
		h := handle.Load()

		aDone := goJoined(t, unblock, h.Dispose)
		Within(t, "cleanup start", func() { <-entered })

		bHandle := make(chan struct{})
		bDone := goJoined(t, unblock, func() {
			h.Dispose()
			close(bHandle)
			provider.Dispose()
		})
		providerDone := goJoined(t, unblock, provider.Dispose)

		synctest.Wait()
		select {
		case <-bHandle:
			t.Fatal("a second Dispose returned while the first caller's cleanup was running")
		case <-providerDone:
			t.Fatal("the provider unloaded while a dependent's cleanup was running")
		default:
		}
		True(t, !log.Contains("provider"), "the provider's cleanup ran before its dependent's: "+log.String())

		unblock()
		Within(t, "first dispose", func() { <-aDone })
		Within(t, "second dispose", func() { <-bDone })
		Within(t, "provider dispose", func() { <-providerDone })
		Equal(t, "consumer,provider", log.String())
		True(t, sawLive.Load(), "the dependent's cleanup saw its provider released")
	})
}

// DH-T-CORDIS-3: a fiber that never loaded still releases its effects, and a creation
// handler can dispose the new fiber before its body runs.
//
//dsh:test DH-T-CORDIS-3
func TestPendingFiberReleasesEffectsAndCreationHandlerCanDispose(t *testing.T) {
	t.Run("pending fiber", func(t *testing.T) {
		root, _ := Quiet()
		log := &Log{}
		fiber := Must(root.Use(NeedsCounter, nil))
		MustWait(t, fiber)
		Equal(t, cordis.Pending, fiber.State())
		Must(fiber.Ctx().Effect("attached", func() (cordis.Disposer, error) {
			return func() error { log.Add("released"); return nil }, nil
		}))
		Within(t, "dispose", fiber.Dispose)
		Equal(t, cordis.Disposed, fiber.State())
		Equal(t, "released", log.String())
	})

	t.Run("creation handler", func(t *testing.T) {
		root, logs := Quiet()
		var ran, otherCalled, disposing atomic.Bool
		root.Internal().OnPlugin(func(*cordis.Fiber) { panic("handler failure") })
		root.Internal().OnPlugin(func(f *cordis.Fiber) {
			// Dispose delivers its own notifications, so this handler runs again.
			if f.Name() == "doomed" && disposing.CompareAndSwap(false, true) {
				f.Dispose()
			}
		})
		root.Internal().OnPlugin(func(*cordis.Fiber) { otherCalled.Store(true) })
		fiber := Must(root.Apply("doomed", func(*cordis.Context) error {
			ran.Store(true)
			return nil
		}))
		Within(t, "wait", func() { NoError(t, fiber.Wait()) })
		Equal(t, cordis.Disposed, fiber.State())
		True(t, !ran.Load(), "the body of a fiber disposed on creation ran")
		True(t, otherCalled.Load(), "a panicking handler stopped the others")
		True(t, Count(logs, cordis.LevelError) > 0, "the panicking handler was not logged")
	})
}

// lifetimeWatcher records each load's lifetime and its cause once cancelled. Its
// cleanup waits for the watcher goroutine, so it deadlocks unless the lifetime is
// cancelled before cleanups run.
func lifetimeWatcher(lifetimes chan<- context.Context, causes chan<- error) func(ctx *cordis.Context) error {
	return func(ctx *cordis.Context) error {
		life := ctx.Lifetime()
		lifetimes <- life
		exited := make(chan struct{})
		go func() {
			<-life.Done()
			causes <- context.Cause(life)
			close(exited)
		}()
		_, err := ctx.Effect("watch", func() (cordis.Disposer, error) {
			return func() error { <-exited; return nil }, nil
		})
		return err
	}
}

func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(Timeout):
		t.Fatalf("no %s within %s", what, Timeout)
		panic("unreachable")
	}
}

// DH-T-CORDIS-4: each load gets a fresh lifetime, cancelled with the leave's cause
// before cleanups run.
//
//dsh:test DH-T-CORDIS-4
func TestFiberLifetimeIsCancelledByUnload(t *testing.T) {
	t.Run("dispose", func(t *testing.T) {
		root, _ := Quiet()
		lifetimes, causes := make(chan context.Context, 4), make(chan error, 4)
		fiber := MustWait(t, Must(root.Apply("watcher", lifetimeWatcher(lifetimes, causes))))
		life := receive(t, lifetimes, "lifetime")
		NoError(t, life.Err())
		Within(t, "dispose", fiber.Dispose)
		Equal(t, cordis.ErrInactive, receive(t, causes, "cause"))
		Equal(t, cordis.ErrInactive, context.Cause(fiber.Ctx().Lifetime()))
	})

	t.Run("restart", func(t *testing.T) {
		root, _ := Quiet()
		lifetimes, causes := make(chan context.Context, 4), make(chan error, 4)
		fiber := MustWait(t, Must(root.Apply("watcher", lifetimeWatcher(lifetimes, causes))))
		first := receive(t, lifetimes, "first lifetime")
		Within(t, "restart", func() { NoError(t, fiber.Restart()) })
		Equal(t, cordis.ErrUnloading, receive(t, causes, "cause"))
		second := receive(t, lifetimes, "second lifetime")
		True(t, first != second, "the reload reused the cancelled lifetime")
		NoError(t, second.Err())
		Equal(t, second, fiber.Lifetime())
		Within(t, "dispose", root.Dispose)
	})

	t.Run("provider withdrawn", func(t *testing.T) {
		root, _ := Quiet()
		lifetimes, causes := make(chan context.Context, 4), make(chan error, 4)
		provider := MustWait(t, Must(root.Use(Counter, nil)))
		fiber := MustWait(t, Must(root.Apply("watcher", lifetimeWatcher(lifetimes, causes), "counter")))
		Equal(t, cordis.Active, fiber.State())
		Within(t, "dispose provider", provider.Dispose)
		Equal(t, cordis.ErrUnloading, receive(t, causes, "cause"))
		MustWait(t, fiber)
		Equal(t, cordis.Pending, fiber.State())
		Equal(t, cordis.ErrUnloading, context.Cause(fiber.Lifetime()))
	})

	t.Run("blocking body", func(t *testing.T) {
		root, _ := Quiet()
		causes := make(chan error, 1)
		started := make(chan struct{})
		fiber := Must(root.Apply("blocker", func(ctx *cordis.Context) error {
			life := ctx.Lifetime()
			close(started)
			<-life.Done()
			causes <- context.Cause(life)
			return nil
		}))
		Within(t, "body start", func() { <-started })
		Equal(t, cordis.Loading, fiber.State())
		Within(t, "dispose", fiber.Dispose)
		Equal(t, cordis.ErrInactive, receive(t, causes, "cause"))
		Equal(t, cordis.Disposed, fiber.State())
	})

	t.Run("pending and root", func(t *testing.T) {
		root, _ := Quiet()
		fiber := MustWait(t, Must(root.Use(NeedsCounter, nil)))
		Equal(t, cordis.Pending, fiber.State())
		Equal(t, cordis.ErrUnloading, context.Cause(fiber.Lifetime()))

		lifetimes, causes := make(chan context.Context, 1), make(chan error, 1)
		MustWait(t, Must(root.Apply("watcher", lifetimeWatcher(lifetimes, causes))))
		NoError(t, root.Lifetime().Err())
		Within(t, "dispose root", root.Dispose)
		Equal(t, cordis.ErrInactive, receive(t, causes, "child cause"))
		Equal(t, cordis.ErrInactive, context.Cause(root.Lifetime()))
	})
}

// Root teardown is exclusive and final: a root cleanup cannot register anything that
// outlives it, and an overlapping Dispose returns only after the first one's cleanups.
func TestRootDisposeIsFinalAndExclusive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root, _ := Quiet()
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := closeOnce(release)
		var released atomic.Bool
		var refused []error
		Must(root.Effect("gate", func() (cordis.Disposer, error) {
			return func() error {
				close(entered)
				_, effectErr := root.Effect("late", func() (cordis.Disposer, error) { return nil, nil })
				_, useErr := root.Apply("late", func(*cordis.Context) error { return nil })
				_, provideErr := root.Provide("late", 1)
				refused = []error{effectErr, useErr, provideErr}
				<-release
				released.Store(true)
				return nil
			}, nil
		}))

		results := make(chan bool, 3)
		goJoined(t, unblock, func() { root.Dispose(); results <- released.Load() })
		Within(t, "first cleanup", func() { <-entered })
		Equal(t, cordis.Unloading, root.Fiber().State())
		goJoined(t, unblock, func() { root.Dispose(); results <- released.Load() })
		goJoined(t, unblock, func() { _ = root.Fiber().Wait(); results <- released.Load() })
		synctest.Wait()
		select {
		case <-results:
			t.Fatal("a root Dispose or Wait returned while its cleanup was blocked")
		default:
		}
		unblock()
		for range 3 {
			Within(t, "dispose", func() { True(t, <-results, "a root Dispose or Wait returned before the cleanup") })
		}
		Equal(t, cordis.Disposed, root.Fiber().State())
		for i, err := range refused {
			True(t, isInactive(err), "root cleanup registration "+[]string{"Effect", "Use", "Provide"}[i]+" was not refused: "+errString(err))
		}
		_, err := root.Effect("after", func() (cordis.Disposer, error) { return nil, nil })
		True(t, isInactive(err), "a disposed root accepted an effect: "+errString(err))
		Equal(t, 0, len(root.Fiber().Effects()), "effects left on the disposed root")
	})
}

// An acquisition in flight when its fiber unloads is waited for: the cleanup it
// collects runs before the unload ends and before the fiber's provider is released.
func TestUnloadWaitsForInFlightAcquisition(t *testing.T) {
	for _, mode := range []string{"sync", "async"} {
		t.Run(mode, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				root, _ := Quiet()
				var providerClosed, unloaded atomic.Bool
				provider := MustWait(t, Must(root.Apply("provider", func(ctx *cordis.Context) error {
					if _, err := ctx.Provide("svc", "value"); err != nil {
						return err
					}
					_, err := ctx.Effect("close", func() (cordis.Disposer, error) {
						return func() error { providerClosed.Store(true); return nil }, nil
					})
					return err
				})))
				owner := MustWait(t, Must(root.Apply("owner", func(*cordis.Context) error { return nil }, "svc")))
				Equal(t, cordis.Active, owner.State())

				entered, gate := make(chan struct{}), make(chan struct{})
				unblock := closeOnce(gate)
				type observed struct{ providerClosed, unloaded bool }
				cleanups := make(chan observed, 2)
				acquire := func() (cordis.Disposer, error) {
					close(entered)
					<-gate
					return func() error {
						cleanups <- observed{providerClosed.Load(), unloaded.Load()}
						return nil
					}, nil
				}
				register := owner.Ctx().Effect
				if mode == "async" {
					register = owner.Ctx().EffectAsync
				}
				goJoined(t, unblock, func() { _, _ = register("slow", acquire) })
				Within(t, "acquisition start", func() { <-entered })

				life := owner.Lifetime()
				disposed := goJoined(t, unblock, func() {
					provider.Dispose()
					unloaded.Store(true)
				})
				Within(t, "owner unload start", func() { <-life.Done() })
				synctest.Wait()
				select {
				case <-disposed:
					t.Fatal("the provider was disposed while its dependent was still acquiring")
				default:
				}
				unblock()
				Within(t, "dispose", func() { <-disposed })
				got := receive(t, cleanups, "cleanup")
				True(t, !got.unloaded, "the cleanup ran after the unload returned")
				True(t, !got.providerClosed, "the cleanup ran after the provider was released")
				Equal(t, 0, len(cleanups), "the cleanup ran twice")
			})
		})
	}
}

// A creation handler that disposes its fiber stops the load even when another
// goroutine is delivering notifications, so the handler is queued behind it.
func TestCreationHandlerRunsBeforeLoadUnderConcurrentDelivery(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		root, _ := Quiet()
		blocking, gate := make(chan struct{}), make(chan struct{})
		unblock := closeOnce(gate)
		var blockOnce sync.Once
		var disposing atomic.Bool
		root.Internal().OnStatus(func(f *cordis.Fiber, _, _ cordis.FiberState) {
			if f.Name() == "blocker" {
				blockOnce.Do(func() { close(blocking); <-gate })
			}
		})
		root.Internal().OnPlugin(func(f *cordis.Fiber) {
			if f.Name() == "doomed" && disposing.CompareAndSwap(false, true) {
				f.Dispose()
			}
		})
		goJoined(t, unblock, func() { _, _ = root.Apply("blocker", func(*cordis.Context) error { return nil }) })
		Within(t, "delivery blocked", func() { <-blocking })

		var ran atomic.Bool
		doomed := Must(root.Apply("doomed", func(*cordis.Context) error {
			ran.Store(true)
			return nil
		}))
		// Every goroutine of the bubble is blocked: the load could have run by now.
		synctest.Wait()
		True(t, !ran.Load(), "the fiber loaded before its creation handler ran")
		unblock()
		Within(t, "wait", func() { NoError(t, doomed.Wait()) })
		Equal(t, cordis.Disposed, doomed.State())
		True(t, !ran.Load(), "the body of a fiber disposed on creation ran")
	})
}

// A notification raised while an effect is acquired is delivered by the tree's
// dispatcher, never on the acquisition's stack, so a handler can dispose the effect's fiber without waiting
// for itself, and an acquisition can still wait for notification-driven work.
func TestNotificationHandlerCanDisposeTheAcquiringFiber(t *testing.T) {
	t.Run("root provide", func(t *testing.T) {
		root, _ := Quiet()
		var claimed atomic.Bool
		handled := make(chan struct{})
		root.Internal().OnService(func(name string, value any) {
			if name == "svc" && value != nil && claimed.CompareAndSwap(false, true) {
				root.Dispose()
				close(handled)
			}
		})
		Within(t, "provide", func() { _, _ = root.Provide("svc", 1) })
		Within(t, "handler", func() { <-handled })
		Equal(t, cordis.Disposed, root.Fiber().State())
		True(t, !root.Has("svc"), "the service outlived the root")
	})

	t.Run("nested provide", func(t *testing.T) {
		root, _ := Quiet()
		var released atomic.Int32
		fiber := MustWait(t, Must(root.Apply("owner", func(*cordis.Context) error { return nil })))
		var claimed atomic.Bool
		handled := make(chan struct{})
		root.Internal().OnService(func(name string, value any) {
			if name == "inner" && value != nil && claimed.CompareAndSwap(false, true) {
				fiber.Dispose()
				close(handled)
			}
		})
		Within(t, "effect", func() {
			_, _ = fiber.Ctx().Effect("outer", func() (cordis.Disposer, error) {
				if _, err := fiber.Ctx().Provide("inner", 1); err != nil {
					return nil, err
				}
				return func() error { released.Add(1); return nil }, nil
			})
		})
		Within(t, "handler", func() { <-handled })
		Equal(t, cordis.Disposed, fiber.State())
		Equal(t, int32(1), released.Load(), "outer cleanup runs")
	})

	t.Run("use and wait inside an acquisition", func(t *testing.T) {
		root, _ := Quiet()
		var child *cordis.Fiber
		Within(t, "effect", func() {
			Must(root.Effect("spawner", func() (cordis.Disposer, error) {
				var err error
				if child, err = root.Apply("child", func(*cordis.Context) error { return nil }); err != nil {
					return nil, err
				}
				return nil, child.Wait()
			}))
		})
		Equal(t, cordis.Active, child.State())
		Within(t, "dispose", root.Dispose)
	})
}

// Root lifecycle handlers run on the dispatcher, not on the goroutine tearing the root
// down: an Unloading handler that waits for the teardown, and a Disposed handler that
// disposes the root again, neither block it.
func TestRootLifecycleHandlersCannotBlockTeardown(t *testing.T) {
	root, _ := Quiet()
	var cleaned atomic.Bool
	Must(root.Effect("cleanup", func() (cordis.Disposer, error) {
		return func() error { cleaned.Store(true); return nil }, nil
	}))
	unloading, disposed := make(chan bool, 1), make(chan struct{})
	root.Internal().OnStatus(func(f *cordis.Fiber, _, next cordis.FiberState) {
		if f != root.Fiber() {
			return
		}
		switch next {
		case cordis.Unloading:
			_ = f.Wait()
			unloading <- cleaned.Load()
		case cordis.Disposed:
			root.Dispose()
			close(disposed)
		}
	})
	Within(t, "dispose", root.Dispose)
	True(t, cleaned.Load(), "Dispose returned before the cleanup")
	Within(t, "unloading handler", func() { True(t, <-unloading, "Wait in the Unloading handler returned before the cleanup") })
	Within(t, "disposed handler", func() { <-disposed })
	Equal(t, cordis.Disposed, root.Fiber().State())
}

// A handler that waits for a fiber's load does not deadlock when that load creates and
// waits for a child: the child's creation notification is delivered by the waiting
// load itself, not behind the blocked handler.
func TestHandlerWaitingForALoadThatWaitsForAChild(t *testing.T) {
	root, _ := Quiet()
	entered, handled := make(chan struct{}), make(chan error, 1)
	var once sync.Once
	var created atomic.Bool
	root.Internal().OnPlugin(func(f *cordis.Fiber) {
		if f.Name() == "child" {
			created.Store(true)
		}
	})
	root.Internal().OnStatus(func(f *cordis.Fiber, _, next cordis.FiberState) {
		if f.Name() == "parent" && next == cordis.Loading {
			once.Do(func() {
				close(entered)
				handled <- f.Wait()
			})
		}
	})
	var child *cordis.Fiber
	parent := Must(root.Apply("parent", func(ctx *cordis.Context) error {
		<-entered
		var err error
		if child, err = ctx.Apply("child", func(*cordis.Context) error { return nil }); err != nil {
			return err
		}
		return child.Wait()
	}))
	Within(t, "handler", func() { NoError(t, <-handled) })
	Within(t, "wait", func() { NoError(t, parent.Wait()) })
	Equal(t, cordis.Active, parent.State())
	Equal(t, cordis.Active, child.State())
	True(t, created.Load(), "the child loaded before its creation handler ran")
}

// An external Dispose returns only once its notifications are delivered, in order
// behind an unrelated handler that blocks the dispatcher outside cordis.
func TestDisposeDeliversItsNotificationsInOrderBehindABlockedHandler(t *testing.T) {
	root, _ := Quiet()
	fiber := MustWait(t, Must(root.Apply("disposed", func(*cordis.Context) error { return nil })))
	var mu sync.Mutex
	var states []cordis.FiberState
	blocking, gate := make(chan struct{}), make(chan struct{})
	unblock := closeOnce(gate)
	var claimed atomic.Bool
	root.Internal().OnStatus(func(f *cordis.Fiber, _, next cordis.FiberState) {
		switch f {
		case fiber:
			mu.Lock()
			states = append(states, next)
			mu.Unlock()
		default:
			if f.Name() == "blocker" && claimed.CompareAndSwap(false, true) {
				close(blocking)
				<-gate
			}
		}
	})
	goJoined(t, unblock, func() { _, _ = root.Apply("blocker", func(*cordis.Context) error { return nil }) })
	Within(t, "dispatcher blocked", func() { <-blocking })

	disposed := goJoined(t, unblock, fiber.Dispose)
	Within(t, "unload", func() {
		for fiber.State() != cordis.Disposed {
			runtime.Gosched()
		}
	})
	select {
	case <-disposed:
		t.Fatal("dispose returned before its notifications were delivered")
	default:
	}
	mu.Lock()
	Equal(t, 0, len(states))
	mu.Unlock()
	unblock()
	Within(t, "dispose", func() { <-disposed })
	mu.Lock()
	defer mu.Unlock()
	Equal(t, fmt.Sprint([]cordis.FiberState{cordis.Unloading, cordis.Disposed}), fmt.Sprint(states))
}

// A fiber does not load while one of its creation handlers is blocked, even when an
// unrelated Wait delivers the queued notifications behind that handler.
func TestBlockedCreationHandlerHoldsTheLoadAgainstOtherDelivery(t *testing.T) {
	root, _ := Quiet()
	blocking, gate, handled := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var claimed atomic.Bool
	root.Internal().OnPlugin(func(f *cordis.Fiber) {
		if f.Name() == "doomed" && claimed.CompareAndSwap(false, true) {
			close(blocking)
			<-gate
			f.Dispose()
			close(handled)
		}
	})
	var ran atomic.Bool
	doomed := Must(root.Apply("doomed", func(*cordis.Context) error {
		ran.Store(true)
		return nil
	}))
	Within(t, "handler blocked", func() { <-blocking })
	Within(t, "unrelated wait", func() { NoError(t, root.Fiber().Wait()) })
	True(t, !ran.Load(), "the fiber loaded while its creation handler was blocked")
	close(gate)
	Within(t, "handler", func() { <-handled })
	Within(t, "wait", func() { NoError(t, doomed.Wait()) })
	Equal(t, cordis.Disposed, doomed.State())
	True(t, !ran.Load(), "the body of a fiber disposed on creation ran")
}

// A barrier inside an effect acquisition never runs a handler on the acquisition's
// stack: a handler it lets through can dispose the root, which waits for that
// acquisition, even while an unrelated handler blocks the dispatcher.
func TestAcquisitionBarrierDoesNotRunHandlersOnItsStack(t *testing.T) {
	root, _ := Quiet()
	sibling := MustWait(t, Must(root.Apply("sibling", func(*cordis.Context) error { return nil })))
	blocking, gate := make(chan struct{}), make(chan struct{})
	unblock := closeOnce(gate)
	var blocked, claimed atomic.Bool
	root.Internal().OnStatus(func(f *cordis.Fiber, _, _ cordis.FiberState) {
		if f.Name() == "blocker" && blocked.CompareAndSwap(false, true) {
			close(blocking)
			<-gate
		}
	})
	handled := make(chan struct{})
	root.Internal().OnService(func(name string, value any) {
		if name == "svc" && value != nil && claimed.CompareAndSwap(false, true) {
			root.Dispose()
			close(handled)
		}
	})
	goJoined(t, unblock, func() { _, _ = root.Apply("blocker", func(*cordis.Context) error { return nil }) })
	Within(t, "dispatcher blocked", func() { <-blocking })

	var cleaned atomic.Bool
	Within(t, "effect", func() {
		_, _ = root.Effect("acquire", func() (cordis.Disposer, error) {
			if _, err := root.Provide("svc", 1); err != nil {
				return nil, err
			}
			if err := sibling.Wait(); err != nil {
				return nil, err
			}
			return func() error { cleaned.Store(true); return nil }, nil
		})
	})
	unblock()
	Within(t, "handler", func() { <-handled })
	Equal(t, cordis.Disposed, root.Fiber().State())
	True(t, cleaned.Load(), "the acquisition's cleanup did not run")
}

// A creation handler can dispose its fiber even when a cleanup of that fiber creates a
// sibling: the disposal waits for the sibling's creation notification, which is
// delivered while the handler waits.
func TestCreationHandlerDisposalCanCreateASibling(t *testing.T) {
	root, _ := Quiet()
	handled := make(chan struct{})
	var claimed atomic.Bool
	var sibling atomic.Pointer[cordis.Fiber]
	root.Internal().OnPlugin(func(f *cordis.Fiber) {
		if f.Name() != "doomed" || !claimed.CompareAndSwap(false, true) {
			return
		}
		_, err := f.Ctx().Effect("spawn", func() (cordis.Disposer, error) {
			return func() error {
				b, err := root.Apply("sibling", func(*cordis.Context) error { return nil })
				sibling.Store(b)
				return err
			}, nil
		})
		NoError(t, err)
		f.Dispose()
		close(handled)
	})
	doomed := Must(root.Apply("doomed", func(*cordis.Context) error { return nil }))
	Within(t, "handler", func() { <-handled })
	Within(t, "wait", func() { NoError(t, doomed.Wait()) })
	Equal(t, cordis.Disposed, doomed.State())
	b := sibling.Load()
	True(t, b != nil, "the cleanup did not create the sibling")
	Within(t, "sibling", func() { NoError(t, b.Wait()) })
	Equal(t, cordis.Active, b.State())
}

// Notifications queued behind a blocked handler are delivered in the order they were
// posted, even when a Dispose waits for some of them.
func TestQueuedNotificationsKeepTheirOrderBehindABlockedHandler(t *testing.T) {
	root, _ := Quiet()
	fiber := MustWait(t, Must(root.Apply("disposed", func(*cordis.Context) error { return nil })))
	var mu sync.Mutex
	var seen []string
	record := func(s string) { mu.Lock(); seen = append(seen, s); mu.Unlock() }
	blocking, gate := make(chan struct{}), make(chan struct{})
	unblock := closeOnce(gate)
	var claimed atomic.Bool
	root.Internal().OnStatus(func(f *cordis.Fiber, _, next cordis.FiberState) {
		switch {
		case f == fiber:
			record("status:" + next.String())
		case f.Name() == "blocker" && claimed.CompareAndSwap(false, true):
			close(blocking)
			<-gate
		}
	})
	const services = 16
	var want []string
	root.Internal().OnService(func(name string, value any) {
		if value != nil && strings.HasPrefix(name, "svc") {
			record("service:" + name)
		}
	})
	goJoined(t, unblock, func() { _, _ = root.Apply("blocker", func(*cordis.Context) error { return nil }) })
	Within(t, "dispatcher blocked", func() { <-blocking })

	for i := range services {
		name := fmt.Sprint("svc", i)
		_, err := root.Provide(name, i)
		NoError(t, err)
		want = append(want, "service:"+name)
	}
	disposed := goJoined(t, unblock, fiber.Dispose)
	Within(t, "unload", func() {
		for fiber.State() != cordis.Disposed {
			runtime.Gosched()
		}
	})
	want = append(want, "status:"+cordis.Unloading.String(), "status:"+cordis.Disposed.String())
	unblock()
	Within(t, "dispose", func() { <-disposed })
	mu.Lock()
	defer mu.Unlock()
	Equal(t, fmt.Sprint(want), fmt.Sprint(seen))
}
