package cordis_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

// settle is how long a test waits to observe that something did not happen.
const settle = 50 * time.Millisecond

func isInactive(err error) bool { return errors.Is(err, cordis.ErrInactive) }

// DH-T-CORDIS-1: a cleanup cannot register anything on its unloading fiber, so the
// next load starts with an empty effect list.
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
	root, _ := Quiet()
	release := make(chan struct{})
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
		go func() {
			fiber.Dispose()
			results <- allRanOnce()
		}()
	}
	select {
	case <-results:
		t.Fatal("Dispose returned while cleanups were blocked")
	case <-time.After(settle):
	}
	close(release)
	for range 2 {
		Within(t, "dispose", func() { True(t, <-results, "Dispose returned before every cleanup ran once") })
	}
	Within(t, "wait", func() { NoError(t, fiber.Wait()) })
	True(t, allRanOnce(), "a cleanup ran more than once")
	_, err := fiber.Ctx().Effect("after", func() (cordis.Disposer, error) { return nil, nil })
	True(t, isInactive(err), "a disposed fiber accepted an effect: "+errString(err))
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
func TestRunningCleanupHoldsBackSecondDisposeAndProvider(t *testing.T) {
	root, _ := Quiet()
	log := &Log{}
	var closed atomic.Bool
	var sawLive atomic.Bool
	entered := make(chan struct{})
	release := make(chan struct{})
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

	aDone := make(chan struct{})
	go func() { h.Dispose(); close(aDone) }()
	Within(t, "cleanup start", func() { <-entered })

	bHandle := make(chan struct{})
	bDone := make(chan struct{})
	go func() {
		h.Dispose()
		close(bHandle)
		provider.Dispose()
		close(bDone)
	}()
	providerDone := make(chan struct{})
	go func() { provider.Dispose(); close(providerDone) }()

	select {
	case <-bHandle:
		t.Fatal("a second Dispose returned while the first caller's cleanup was running")
	case <-providerDone:
		t.Fatal("the provider unloaded while a dependent's cleanup was running")
	case <-time.After(settle):
	}
	True(t, !log.Contains("provider"), "the provider's cleanup ran before its dependent's: "+log.String())

	close(release)
	Within(t, "first dispose", func() { <-aDone })
	Within(t, "second dispose", func() { <-bDone })
	Within(t, "provider dispose", func() { <-providerDone })
	Equal(t, "consumer,provider", log.String())
	True(t, sawLive.Load(), "the dependent's cleanup saw its provider released")
}

// DH-T-CORDIS-3: a fiber that never loaded still releases its effects, and a creation
// handler can dispose the new fiber before its body runs.
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
		var ran, otherCalled atomic.Bool
		var disposeOnce sync.Once
		root.Internal().OnPlugin(func(*cordis.Fiber) { panic("handler failure") })
		root.Internal().OnPlugin(func(f *cordis.Fiber) {
			if f.Name() == "doomed" {
				disposeOnce.Do(f.Dispose)
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
