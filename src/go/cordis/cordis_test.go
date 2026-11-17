package cordis_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

var (
	tick      = cordis.NewEvent[int]("tick")
	resolve   = cordis.NewBail[string, string]("resolve")
	transform = cordis.NewWaterfall[string]("transform")
)

func cleanup(log *Log, line string) func() (cordis.Disposer, error) {
	return func() (cordis.Disposer, error) {
		return func() error {
			log.Add(line)
			return nil
		}, nil
	}
}

// ---------------------------------------------------------------- effects

func TestEffectsDisposeInReverseOrder(t *testing.T) {
	root := cordis.New()
	log := &Log{}
	fiber := Must(root.Apply("p", func(ctx *cordis.Context) error {
		for _, name := range []string{"a", "b"} {
			if _, err := ctx.Effect("", func() (cordis.Disposer, error) {
				log.Add(name)
				return func() error { log.Add("~" + name); return nil }, nil
			}); err != nil {
				return err
			}
		}
		return nil
	}))
	MustWait(t, fiber)
	Equal(t, "a,b", log.String())

	Within(t, "dispose", fiber.Dispose)
	Equal(t, "a,b,~b,~a", log.String())
}

func TestGeneratorEffectsNestAndUnwindTogether(t *testing.T) {
	root := cordis.New()
	log := &Log{}
	fiber := Must(root.Apply("p", func(ctx *cordis.Context) error {
		_, err := ctx.EffectIter("resources", func(yield func(cordis.Disposer) bool) error {
			log.Add("open:1")
			if !yield(func() error { log.Add("close:1"); return nil }) {
				return nil
			}
			log.Add("open:2")
			inner, err := ctx.Effect("", func() (cordis.Disposer, error) {
				log.Add("open:3")
				return func() error { log.Add("close:3"); return nil }, nil
			})
			if err != nil {
				return err
			}
			yield(inner.Disposer())
			return nil
		})
		return err
	}))
	MustWait(t, fiber)
	Equal(t, "open:1,open:2,open:3", log.String())

	Within(t, "dispose", fiber.Dispose)
	Equal(t, "open:1,open:2,open:3,close:3,close:1", log.String())
}

func TestGeneratorEffectStopsAdvancingOnceDisposed(t *testing.T) { // #268
	root := cordis.New()
	release := make(chan struct{})
	events := &Log{}
	var handle atomic.Pointer[cordis.EffectHandle]

	// the generator keeps the fiber loading until it finishes, so do not wait for it
	fiber := Must(root.Apply("p", func(ctx *cordis.Context) error {
		h, err := ctx.EffectIter("", func(yield func(cordis.Disposer) bool) error {
			defer events.Add("finally")
			if !yield(func() error { events.Add("dispose 1"); return nil }) {
				return nil
			}
			<-release
			if !yield(func() error { events.Add("dispose 2"); return nil }) {
				return nil
			}
			events.Add("outdated continuation")
			return nil
		})
		handle.Store(h)
		return err
	}))

	Eventually(t, func() bool { return handle.Load() != nil }, "effect never started")
	h := handle.Load()
	disposed := make(chan struct{})
	go func() {
		h.Dispose()
		close(disposed)
	}()
	Eventually(t, func() bool { return !h.Alive() }, "dispose never started")
	close(release)
	Within(t, "dispose", func() { <-disposed })
	Equal(t, "dispose 2,finally,dispose 1", events.String())
	Within(t, "fiber dispose", fiber.Dispose)
}

func TestNestedTreesUnloadFromTheLeavesUp(t *testing.T) {
	root := cordis.New()
	log := &Log{}
	var inner atomic.Pointer[cordis.Fiber]
	outer := Must(root.Apply("outer", func(ctx *cordis.Context) error {
		if _, err := ctx.Effect("", cleanup(log, "~outer")); err != nil {
			return err
		}
		fiber, err := ctx.Apply("inner", func(ctx *cordis.Context) error {
			_, err := ctx.Effect("", cleanup(log, "~inner"))
			return err
		})
		inner.Store(fiber)
		return err
	}))
	MustWait(t, outer)
	MustWait(t, inner.Load())

	Within(t, "dispose", outer.Dispose)
	Equal(t, "~inner,~outer", log.String())
}

// --------------------------------------------------------------- services

func TestUndeclaredServiceAccessIsRejected(t *testing.T) {
	root := cordis.New()
	Must(root.Provide("global", 1))
	MustWait(t, Must(root.Apply("provider", func(ctx *cordis.Context) error {
		_, err := ctx.Provide("secret", 42)
		return err
	})))

	var global any
	var captured error
	MustWait(t, Must(root.Apply("p", func(ctx *cordis.Context) error {
		// root provisions are reachable from every fiber, like upstream's locate()
		global, _ = ctx.Get("global")
		_, captured = ctx.Get("secret")
		return nil
	})))

	Equal(t, any(1), global)
	True(t, errors.Is(captured, cordis.ErrNotInjected), fmt.Sprintf("got %v", captured))
	secret := Must(root.Get("secret"))
	Equal(t, any(42), secret)
}

// Upstream's ctx.get(name): optional and unchecked. deepseek-harness and dsh-superagent
// probe for services they do not depend on this way (ctx.get('jobs')?.…).
func TestOptionalLookupIsUncheckedAndNeverFails(t *testing.T) {
	root := cordis.New()
	MustWait(t, Must(root.Apply("provider", func(ctx *cordis.Context) error {
		_, err := ctx.Provide("secret", 42)
		return err
	})))
	// a provider that has published its service but is still loading
	published := make(chan struct{})
	release := make(chan struct{})
	loading := Must(root.Apply("loading", func(ctx *cordis.Context) error {
		if _, err := ctx.Provide("idle-service", "x"); err != nil {
			return err
		}
		close(published)
		<-release
		return nil
	}))
	<-published

	var secret, missing, isolated, inactive any
	var foundSecret, foundMissing, foundIsolated, foundInactive bool
	var typed int
	var typedOK bool
	MustWait(t, Must(root.Apply("p", func(ctx *cordis.Context) error {
		secret, foundSecret = ctx.Lookup("secret") // not declared, still reachable
		missing, foundMissing = ctx.Lookup("jobs")
		isolated, foundIsolated = ctx.Isolate("secret").Lookup("secret")
		inactive, foundInactive = ctx.Lookup("idle-service")
		typed, typedOK = cordis.NewServiceKey[int]("secret").Lookup(ctx)
		return nil
	})))

	True(t, foundSecret, "an undeclared service was not found")
	Equal(t, any(42), secret)
	True(t, !foundMissing && missing == nil, "a missing service was found")
	True(t, !foundIsolated && isolated == nil, "the lookup crossed into another realm")
	True(t, !foundInactive && inactive == nil, "a provider that is not active was found")
	True(t, typedOK && typed == 42, "the typed lookup failed")

	close(release)
	MustWait(t, loading)
	value, found := root.Lookup("idle-service")
	True(t, found && value == "x", "the service was not found once its provider was active")
}

func TestDependentsLoadAndUnloadWithTheirProvider(t *testing.T) {
	root := cordis.New()
	var loads, unloads atomic.Int32
	consumer := Must(root.Inject([]string{"counter"}, func(ctx *cordis.Context) error {
		loads.Add(1)
		_, err := ctx.Effect("", func() (cordis.Disposer, error) {
			return func() error { unloads.Add(1); return nil }, nil
		})
		return err
	}))
	MustWait(t, consumer)
	Equal(t, cordis.Pending, consumer.State())
	Equal(t, int32(0), loads.Load())

	provider := MustWait(t, Must(root.Use(Counter, nil)))
	MustWait(t, consumer)
	Equal(t, cordis.Active, consumer.State())
	Equal(t, int32(1), loads.Load())

	Within(t, "dispose", provider.Dispose)
	MustWait(t, consumer)
	Equal(t, cordis.Pending, consumer.State())
	Equal(t, int32(1), unloads.Load())

	MustWait(t, Must(root.Use(Counter, nil)))
	MustWait(t, consumer)
	Equal(t, int32(2), loads.Load())
}

func TestProviderIdentityChangeReloadsDependents(t *testing.T) {
	root := cordis.New()
	var loads atomic.Int32
	handle := Must(root.Provide("thing", 1))
	consumer := Must(root.Inject([]string{"thing"}, func(*cordis.Context) error { loads.Add(1); return nil }))
	MustWait(t, consumer)
	Equal(t, int32(1), loads.Load())

	Within(t, "withdraw", handle.Dispose)
	Must(root.Provide("thing", 2))
	MustWait(t, consumer)
	Equal(t, int32(2), loads.Load())
}

type gatedService struct{ ready chan struct{} }

func (s *gatedService) Init() error {
	<-s.ready
	return nil
}

func TestInitGatesDependents(t *testing.T) {
	root := cordis.New()
	ready := make(chan struct{})
	gated := cordis.Construct("GatedService", func(ctx *cordis.Context, _ cordis.None) (*gatedService, error) {
		service := &gatedService{ready: ready}
		_, err := ctx.Provide("gated", service)
		return service, err
	})
	var loaded atomic.Bool

	provider := Must(root.Use(gated, nil))
	consumer := Must(root.Inject([]string{"gated"}, func(*cordis.Context) error { loaded.Store(true); return nil }))
	MustWait(t, consumer)
	True(t, !loaded.Load(), "dependent ran before init completed")
	Equal(t, cordis.Loading, provider.State())

	close(ready)
	MustWait(t, provider)
	MustWait(t, consumer)
	True(t, loaded.Load(), "dependent never ran")
}

func TestIsolateCreatesAPrivateRealm(t *testing.T) {
	root := cordis.New()
	Must(root.Provide("db", "shared"))
	isolated := root.Isolate("db")
	Must(isolated.Provide("db", "private"))

	var outer, inner any
	MustWait(t, Must(root.Inject([]string{"db"}, func(ctx *cordis.Context) error {
		outer, _ = ctx.Get("db")
		return nil
	})))
	MustWait(t, Must(isolated.Inject([]string{"db"}, func(ctx *cordis.Context) error {
		inner, _ = ctx.Get("db")
		return nil
	})))
	Equal(t, any("shared"), outer)
	Equal(t, any("private"), inner)
}

func TestLookupStopsAtAnIsolationBoundary(t *testing.T) { // #209
	root := cordis.New()
	Must(root.Provide("db", "shared"))
	var outer any
	var inner error
	MustWait(t, Must(root.Inject([]string{"db"}, func(ctx *cordis.Context) error {
		outer, _ = ctx.Get("db")
		_, inner = ctx.Isolate("db").Get("db")
		return nil
	})))
	Equal(t, any("shared"), outer)
	True(t, errors.Is(inner, cordis.ErrNotInjected), fmt.Sprintf("got %v", inner))
}

func TestAFiberInjectsAndReProvidesTheSameNameInAnotherRealm(t *testing.T) {
	root := cordis.New()
	Must(root.Provide("foo", "outer"))
	var seenOuter, seenInner any
	consumer := MustWait(t, Must(root.Inject([]string{"foo"}, func(ctx *cordis.Context) error {
		isolated := ctx.Isolate("foo")
		outer, err := ctx.Get("foo")
		if err != nil {
			return err
		}
		if _, err := isolated.Provide("foo", fmt.Sprintf("wraps %v", outer)); err != nil {
			return err
		}
		seenOuter, _ = ctx.Get("foo")
		seenInner, _ = isolated.Get("foo")
		return nil
	})))
	Equal(t, any("outer"), seenOuter)
	Equal(t, any("wraps outer"), seenInner)
	Within(t, "dispose", consumer.Dispose)
}

// --------------------------------------------------------- service disposal

func TestProviderEffectsOutliveADependentsAsyncCleanup(t *testing.T) { // #196
	root := cordis.New()
	gate := make(chan struct{})
	timeline := &Log{}
	provider := MustWait(t, Must(root.Apply("provider", func(ctx *cordis.Context) error {
		var closed atomic.Bool
		read := func() {
			state := "open"
			if closed.Load() {
				state = "closed"
			}
			timeline.Add("read: " + state)
		}
		if _, err := ctx.Provide("foo", read); err != nil {
			return err
		}
		_, err := ctx.Effect("", func() (cordis.Disposer, error) {
			return func() error {
				closed.Store(true)
				timeline.Add("provider closed")
				return nil
			}, nil
		})
		return err
	})))
	MustWait(t, Must(root.Inject([]string{"foo"}, func(ctx *cordis.Context) error {
		value, err := ctx.Get("foo")
		if err != nil {
			return err
		}
		read := value.(func())
		_, err = ctx.Effect("", func() (cordis.Disposer, error) {
			return func() error {
				<-gate
				read()
				return nil
			}, nil
		})
		return err
	})))

	disposed := make(chan struct{})
	go func() {
		provider.Dispose()
		close(disposed)
	}()
	time.Sleep(30 * time.Millisecond)
	closedDuringDrain := timeline.Contains("provider closed")
	close(gate)
	Within(t, "dispose", func() { <-disposed })
	True(t, !closedDuringDrain, "provider closed before its dependent finished")
	Equal(t, "read: open,provider closed", timeline.String())
}

func TestWithdrawingAServiceWaitsForADisposingConsumer(t *testing.T) {
	root := cordis.New()
	gate := make(chan struct{})
	timeline := &Log{}
	provider := MustWait(t, Must(root.Apply("provider", func(*cordis.Context) error { return nil })))
	withdraw := Must(provider.Ctx().Provide("foo", new(int)))
	consumer := MustWait(t, Must(root.Inject([]string{"foo"}, func(ctx *cordis.Context) error {
		_, err := ctx.Effect("", func() (cordis.Disposer, error) {
			return func() error {
				<-gate
				timeline.Add("consumer finished")
				return nil
			}, nil
		})
		return err
	})))

	disposing := make(chan struct{})
	withdrawing := make(chan struct{})
	go func() {
		consumer.Dispose()
		close(disposing)
	}()
	go func() {
		withdraw.Dispose()
		timeline.Add("withdrawn")
		close(withdrawing)
	}()
	time.Sleep(30 * time.Millisecond)
	early := timeline.Contains("withdrawn")
	close(gate)
	Within(t, "dispose", func() { <-disposing; <-withdrawing })
	True(t, !early, "withdrawn while the consumer was still cleaning up")
	Equal(t, "consumer finished,withdrawn", timeline.String())
}

type openCloseService struct {
	ctx      *cordis.Context
	timeline *Log
	closed   atomic.Bool
}

func (s *openCloseService) Init() error {
	_, err := s.ctx.Effect("connection", func() (cordis.Disposer, error) {
		return func() error {
			s.closed.Store(true)
			s.timeline.Add("closed")
			return nil
		}, nil
	})
	return err
}

func (s *openCloseService) Read() {
	state := "open"
	if s.closed.Load() {
		state = "closed"
	}
	s.timeline.Add("read: " + state)
}

func TestChildOfAProviderThatInjectsTheParentServiceIsDrainedFirst(t *testing.T) {
	root := cordis.New()
	gate := make(chan struct{})
	timeline := &Log{}
	key := cordis.NewServiceKey[*openCloseService]("open-close")
	component := cordis.Construct("OpenCloseService", func(ctx *cordis.Context, _ cordis.None) (*openCloseService, error) {
		service := &openCloseService{ctx: ctx, timeline: timeline}
		_, err := key.Provide(ctx, service)
		return service, err
	})
	provider := MustWait(t, Must(root.Use(component, nil)))
	child := Must(provider.Ctx().Inject([]string{key.Name()}, func(ctx *cordis.Context) error {
		service, err := key.Get(ctx)
		if err != nil {
			return err
		}
		_, err = ctx.Effect("", func() (cordis.Disposer, error) {
			return func() error {
				<-gate
				service.Read()
				return nil
			}, nil
		})
		return err
	}))
	MustWait(t, child)

	disposed := make(chan struct{})
	go func() {
		provider.Dispose()
		close(disposed)
	}()
	time.Sleep(30 * time.Millisecond)
	close(gate)
	Within(t, "dispose", func() { <-disposed })
	Equal(t, "read: open,closed", timeline.String())
}

// ------------------------------------------------------------- intercepts

func TestInterceptLayersConfigDownTheChain(t *testing.T) {
	root := cordis.New()
	scoped := root.Intercept(cordis.LoggerServiceName, cordis.LoggerConfig{Level: cordis.LevelDebug})
	deeper := scoped.Intercept(cordis.LoggerServiceName, cordis.LoggerConfig{Level: cordis.LevelSilent})

	_, ok := cordis.GetIntercept[cordis.LoggerConfig](root, cordis.LoggerServiceName)
	True(t, !ok, "root has an intercept")
	config, _ := cordis.GetIntercept[cordis.LoggerConfig](scoped, cordis.LoggerServiceName)
	Equal(t, cordis.LevelDebug, config.Level)
	config, _ = cordis.GetIntercept[cordis.LoggerConfig](deeper, cordis.LoggerServiceName)
	Equal(t, cordis.LevelSilent, config.Level)
	Equal(t, 2, len(deeper.InterceptChain(cordis.LoggerServiceName)))
}

func TestContextInterceptsTakePriorityOverInjectMetadata(t *testing.T) { // #206
	root := cordis.New()
	Must(root.Provide("labelled", new(int)))
	var seen atomic.Value
	metadata := cordis.Define("MetadataComponent", func(ctx *cordis.Context, _ cordis.None) error {
		value, _ := cordis.GetIntercept[string](ctx, "labelled")
		seen.Store(value)
		return nil
	}, cordis.WithInjectConfig("labelled", "from-inject"))

	MustWait(t, Must(root.Use(metadata, nil)))
	Equal(t, "from-inject", seen.Load().(string))
	MustWait(t, Must(root.Intercept("labelled", "from-intercept").Use(metadata, nil)))
	Equal(t, "from-intercept", seen.Load().(string))
}

func TestLoggerThresholdFollowsIntercepts(t *testing.T) {
	root, logs := Quiet()
	root.Logger().Debugf("hidden")
	debug := root.Intercept(cordis.LoggerServiceName, cordis.LoggerConfig{Level: cordis.LevelDebug})
	MustWait(t, Must(debug.Apply("noisy", func(ctx *cordis.Context) error {
		ctx.Logger().Debugf("shown")
		return nil
	})))
	Equal(t, "shown", strings.Join(Texts(logs, cordis.LevelDebug), ","))
	Equal(t, "noisy", logs.Messages()[0].Name)
}

// ----------------------------------------------------------------- config

func TestConfigIsValidatedBeforeTheBodyRuns(t *testing.T) {
	root, _ := Quiet()
	log := &Log{}
	echo := Echo(log)

	bad := Must(root.Use(echo, EchoOptions{Times: 99, Text: "no"}))
	var configError *cordis.ConfigError
	True(t, errors.As(Wait(t, bad), &configError), "invalid config did not fail the load")
	Equal(t, cordis.Failed, bad.State())
	Equal(t, 0, len(log.Lines()))

	good := MustWait(t, Must(root.Use(echo, EchoOptions{Times: 2, Text: "hi"})))
	Equal(t, "+hi hi", log.String())

	if err := good.Update(EchoOptions{Times: 1, Text: "bye"}, false); err != nil {
		t.Fatal(err)
	}
	Equal(t, "+hi hi,-hi hi,+bye", log.String())
}

func TestJSONConfigIsDecodedOntoTheDefaults(t *testing.T) {
	root := cordis.New()
	log := &Log{}
	MustWait(t, Must(root.Use(Echo(log), json.RawMessage(`{"text":"json"}`))))
	Equal(t, "+json", log.String())
}

func TestUpdateValidatesALoadedFiberUpFrontAndCommitsTheOriginalConfig(t *testing.T) {
	root := cordis.New()
	var mu sync.Mutex
	var committed []any
	root.Internal().OnCommit(func(_ *cordis.Fiber, config any) {
		mu.Lock()
		defer mu.Unlock()
		committed = append(committed, config)
	})
	fiber := MustWait(t, Must(root.Use(Echo(&Log{}), EchoOptions{Times: 1, Text: "a"})))

	var configError *cordis.ConfigError
	True(t, errors.As(fiber.Update(EchoOptions{Times: 99}, false), &configError), "invalid update was accepted")
	Equal(t, 1, fiber.Config().(EchoOptions).Times)
	Equal(t, 0, len(committed))

	valid := &EchoOptions{Times: 2, Text: "a"}
	if err := fiber.Update(valid, false); err != nil {
		t.Fatal(err)
	}
	Equal(t, 1, len(committed))
	True(t, committed[0] == any(valid), "commit did not carry the original config")

	if err := fiber.Update(EchoOptions{Times: 3, Text: "a"}, true); err != nil {
		t.Fatal(err)
	}
	Equal(t, 1, len(committed))
}

type valueOptions struct{ Value int }

func TestUpdateHookAcceptsAConfigInPlaceAndDiesWithTheFiber(t *testing.T) { // #56
	root := cordis.New()
	var applied atomic.Int32
	var counting atomic.Bool
	component := cordis.Define("ValueComponent", func(*cordis.Context, valueOptions) error {
		if counting.Load() {
			applied.Add(1)
		}
		return nil
	})
	fiber := MustWait(t, Must(root.Use(component, valueOptions{Value: 1})))
	counting.Store(true)
	var received []int
	Must(cordis.AcceptUpdate(fiber.Ctx(), func(options valueOptions) { received = append(received, options.Value) }))

	if err := fiber.Update(valueOptions{Value: 2}, false); err != nil {
		t.Fatal(err)
	}
	Equal(t, int32(0), applied.Load())
	Equal(t, "[2]", fmt.Sprint(received))
	Equal(t, 2, fiber.Config().(valueOptions).Value)

	// the reload drops the hook, so the next update restarts
	if err := fiber.Restart(); err != nil {
		t.Fatal(err)
	}
	Equal(t, int32(1), applied.Load())
	if err := fiber.Update(valueOptions{Value: 3}, false); err != nil {
		t.Fatal(err)
	}
	Equal(t, int32(2), applied.Load())
	Equal(t, "[2]", fmt.Sprint(received))
}

// -------------------------------------------------------------- lifecycle

func TestFailedFiberDoesNotReloadWhenAProviderChanges(t *testing.T) { // #98
	root, _ := Quiet()
	var loads atomic.Int32
	fiber := Must(root.Inject([]string{"thing"}, func(*cordis.Context) error {
		loads.Add(1)
		return errors.New("boom")
	}))
	first := Must(root.Provide("thing", 1))
	True(t, Wait(t, fiber) != nil, "load did not fail")
	Equal(t, cordis.Failed, fiber.State())

	Within(t, "withdraw", first.Dispose)
	Must(root.Provide("thing", 2))
	time.Sleep(20 * time.Millisecond)
	Equal(t, int32(1), loads.Load())
	Equal(t, cordis.Failed, fiber.State())
}

func TestRestartWhileLoadingIsNotLost(t *testing.T) { // #189
	root := cordis.New()
	started := make(chan struct{})
	release := make(chan struct{})
	var applied atomic.Int32
	fiber := Must(root.Apply("p", func(*cordis.Context) error {
		if applied.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	}))
	<-started
	restarted := make(chan error, 1)
	go func() { restarted <- fiber.Restart() }()
	time.Sleep(10 * time.Millisecond) // let the restart bump the revision
	close(release)
	Within(t, "restart", func() {
		if err := <-restarted; err != nil {
			t.Error(err)
		}
	})
	Equal(t, int32(2), applied.Load())
	Equal(t, cordis.Active, fiber.State())
}

type gatedValue struct {
	value    int
	started  chan struct{}
	release  chan struct{}
	disposed *Log
}

func (g *gatedValue) Init() error {
	if g.value == 1 {
		close(g.started)
		<-g.release
	}
	return nil
}

func (g *gatedValue) Close() error {
	g.disposed.Add(fmt.Sprint(g.value))
	return nil
}

func TestConcurrentUpdatesSettleOnTheLatestConfig(t *testing.T) {
	root := cordis.New()
	started := make(chan struct{})
	release := make(chan struct{})
	applied, disposed := &Log{}, &Log{}
	component := cordis.Construct("GatedValue", func(_ *cordis.Context, options valueOptions) (*gatedValue, error) {
		applied.Add(fmt.Sprint(options.Value))
		return &gatedValue{value: options.Value, started: started, release: release, disposed: disposed}, nil
	})
	fiber := MustWait(t, Must(root.Use(component, valueOptions{Value: 0})))

	var wg sync.WaitGroup
	update := func(value int) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fiber.Update(valueOptions{Value: value}, false); err != nil {
				t.Error(err)
			}
		}()
		Eventually(t, func() bool { return fiber.Config().(valueOptions).Value == value }, "update did not apply")
	}
	update(1)
	Within(t, "first load", func() { <-started })
	update(2)
	update(3)
	close(release)
	Within(t, "updates", wg.Wait)

	Equal(t, "0,1,3", applied.String())
	Equal(t, "0,1", disposed.String())
	Equal(t, 3, fiber.Config().(valueOptions).Value)
	Equal(t, cordis.Active, fiber.State())
	Within(t, "dispose", root.Dispose)
	Equal(t, "0,1,3", disposed.String())
}

func TestSupersededLoadFailureDoesNotFailTheLatestUpdate(t *testing.T) {
	root, logs := Quiet()
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	fiber := Must(root.Apply("p", func(*cordis.Context) error {
		if calls.Add(1) != 1 {
			return nil
		}
		close(started)
		<-release
		return errors.New("superseded load")
	}))
	<-started
	updated := make(chan error, 1)
	go func() { updated <- fiber.Update(nil, false) }()
	time.Sleep(10 * time.Millisecond) // let the update bump the revision
	close(release)
	Within(t, "update", func() {
		if err := <-updated; err != nil {
			t.Error(err)
		}
	})
	Equal(t, cordis.Active, fiber.State())
	Equal(t, 1, Count(logs, cordis.LevelError))
}

func TestDisposedFiberSettlesToDisposedAndReportsIt(t *testing.T) { // #133
	root, _ := Quiet()
	fiber := Must(root.Apply("p", func(*cordis.Context) error { return errors.New("boom") }))
	_ = Wait(t, fiber)
	var mu sync.Mutex
	var states []cordis.FiberState
	root.Internal().OnStatus(func(changed *cordis.Fiber, _, next cordis.FiberState) {
		if changed == fiber {
			mu.Lock()
			states = append(states, next)
			mu.Unlock()
		}
	})

	Within(t, "dispose", fiber.Dispose)
	Equal(t, cordis.Disposed, fiber.State())
	mu.Lock()
	defer mu.Unlock()
	True(t, len(states) > 0 && states[len(states)-1] == cordis.Disposed, fmt.Sprint(states))
}

func TestAPanickingBodyFailsTheLoad(t *testing.T) {
	root, logs := Quiet()
	fiber := Must(root.Apply("p", func(*cordis.Context) error { panic("kaboom") }))
	var panicError *cordis.PanicError
	True(t, errors.As(Wait(t, fiber), &panicError), "panic was not reported as a PanicError")
	Equal(t, cordis.Failed, fiber.State())
	Equal(t, "panic: kaboom", Texts(logs, cordis.LevelError)[0])
}

func TestHandlersMayCallBackIntoTheContext(t *testing.T) {
	root := cordis.New()
	var seen atomic.Int32
	root.Internal().OnStatus(func(fiber *cordis.Fiber, _, _ cordis.FiberState) {
		_ = fiber.State() // takes the tree lock: notifications run outside it
		_ = root.Has("counter")
		seen.Add(1)
	})
	root.Internal().OnService(func(string, any) { _, _ = root.Get(cordis.LoggerServiceName) })
	MustWait(t, Must(root.Use(Counter, nil)))
	consumer := MustWait(t, Must(root.Use(NeedsCounter, nil)))
	Equal(t, cordis.Active, consumer.State())
	True(t, seen.Load() > 0, "no status notification")
}

// -------------------------------------------------------------- replace

type replaceOptions struct{ Text string }

type replaceV2 struct{ text string }

func TestRegistryReplaceSwapsTheImplementationInPlace(t *testing.T) { // #266
	root := cordis.New()
	var v1Loads, v2Loads atomic.Int32
	v1 := cordis.Construct("ReplaceV1", func(_ *cordis.Context, options replaceOptions) (*replaceOptions, error) {
		v1Loads.Add(1)
		return &options, nil
	})
	v2 := cordis.Construct("ReplaceV2", func(_ *cordis.Context, options replaceOptions) (*replaceV2, error) {
		v2Loads.Add(1)
		return &replaceV2{text: options.Text}, nil
	})
	fiber := MustWait(t, Must(root.Use(v1, replaceOptions{Text: "kept"})))

	moved := root.Registry().Replace(v1, v2)
	Equal(t, 1, len(moved))
	MustWait(t, fiber)
	instance, ok := fiber.Instance().(*replaceV2)
	True(t, ok, fmt.Sprintf("instance is %T", fiber.Instance()))
	Equal(t, "kept", instance.text)
	Equal(t, int32(1), v1Loads.Load())
	Equal(t, int32(1), v2Loads.Load())

	// the old component is forwarded
	forwarded := MustWait(t, Must(root.Use(v1, nil)))
	_, ok = forwarded.Instance().(*replaceV2)
	True(t, ok, "old component was not forwarded")
	True(t, root.Registry().Get(v1).Component() == v2, "registry lookup is not forwarded")
}

func TestRegistryReplaceTakesTheNewDependenciesAndRestartsAFailedFiber(t *testing.T) {
	root, _ := Quiet()
	fiber := Must(root.Use(Failing, nil))
	_ = Wait(t, fiber)
	Equal(t, cordis.Failed, fiber.State())

	root.Registry().Replace(Failing, NeedsCounter)
	MustWait(t, fiber)
	Equal(t, cordis.Pending, fiber.State())

	MustWait(t, Must(root.Use(Counter, nil)))
	MustWait(t, fiber)
	Equal(t, cordis.Active, fiber.State())
}

// ----------------------------------------------------------------- events

func TestListenersUnregisterWhenTheirFiberUnloads(t *testing.T) {
	root := cordis.New()
	var seen atomic.Int32
	fiber := MustWait(t, Must(root.Apply("p", func(ctx *cordis.Context) error {
		_, err := tick.On(ctx, func(_ *cordis.Context, n int) error { seen.Add(int32(n)); return nil })
		return err
	})))
	NoError(t, tick.Emit(root, 2))
	Equal(t, int32(2), seen.Load())

	Within(t, "dispose", fiber.Dispose)
	NoError(t, tick.Emit(root, 2))
	Equal(t, int32(2), seen.Load())
}

func TestOnceRunsAListenerOnce(t *testing.T) {
	root := cordis.New()
	var seen atomic.Int32
	Must(tick.Once(root, func(*cordis.Context, int) error { seen.Add(1); return nil }))
	NoError(t, tick.Emit(root, 1))
	NoError(t, tick.Emit(root, 1))
	Equal(t, int32(1), seen.Load())
}

func TestBailReturnsTheFirstAnswer(t *testing.T) {
	root := cordis.New()
	answers := []func(string) (string, bool){
		func(string) (string, bool) { return "", false },
		func(input string) (string, bool) { return strings.ToUpper(input), true },
		func(string) (string, bool) { return "never", true },
	}
	for i, answer := range answers {
		MustWait(t, Must(root.Apply(fmt.Sprint(i), func(ctx *cordis.Context) error {
			_, err := resolve.On(ctx, func(_ *cordis.Context, input string) (string, bool, error) {
				result, ok := answer(input)
				return result, ok, nil
			})
			return err
		})))
	}
	result, ok, err := resolve.Bail(root, "hi")
	True(t, ok && err == nil, "no answer")
	Equal(t, "HI", result)
}

func TestWaterfallComposesMiddlewareAroundATerminal(t *testing.T) {
	root := cordis.New()
	MustWait(t, Must(root.Apply("outer", func(ctx *cordis.Context) error {
		_, err := transform.On(ctx, func(_ *cordis.Context, value string, next func(string) (string, error)) (string, error) {
			inner, err := next(value)
			return "[" + inner + "]", err
		})
		return err
	})))
	MustWait(t, Must(root.Apply("inner", func(ctx *cordis.Context) error {
		_, err := transform.On(ctx, func(_ *cordis.Context, value string, next func(string) (string, error)) (string, error) {
			return next(value + "!")
		})
		return err
	})))

	result := Must(transform.Waterfall(root, "x", func(value string) (string, error) { return strings.ToUpper(value), nil }))
	Equal(t, "[X!]", result)
}

func TestPrependedBailAndWaterfallListenersRunFirst(t *testing.T) {
	root := cordis.New()
	MustWait(t, Must(root.Apply("first", func(ctx *cordis.Context) error {
		if _, err := transform.On(ctx, func(_ *cordis.Context, value string, next func(string) (string, error)) (string, error) {
			return next(value + "b")
		}); err != nil {
			return err
		}
		_, err := resolve.On(ctx, func(*cordis.Context, string) (string, bool, error) { return "first", true, nil })
		return err
	})))
	MustWait(t, Must(root.Apply("later", func(ctx *cordis.Context) error {
		if _, err := transform.Prepend(ctx, func(_ *cordis.Context, value string, next func(string) (string, error)) (string, error) {
			return next(value + "a")
		}); err != nil {
			return err
		}
		_, err := resolve.Prepend(ctx, func(*cordis.Context, string) (string, bool, error) { return "later", true, nil })
		return err
	})))

	result := Must(transform.Waterfall(root, "x", func(value string) (string, error) { return value, nil }))
	Equal(t, "xab", result)
	answer, ok, err := resolve.Bail(root, "q")
	True(t, ok && err == nil, "no answer")
	Equal(t, "later", answer)
}

func TestWaterfallRejectsCallingNextTwice(t *testing.T) { // #44
	root := cordis.New()
	MustWait(t, Must(root.Apply("twice", func(ctx *cordis.Context) error {
		_, err := transform.On(ctx, func(_ *cordis.Context, value string, next func(string) (string, error)) (string, error) {
			if _, err := next(value); err != nil {
				return "", err
			}
			return next(value)
		})
		return err
	})))
	_, err := transform.Waterfall(root, "x", func(value string) (string, error) { return value, nil })
	True(t, err != nil && err.Error() == "next() called multiple times", fmt.Sprint(err))
}

func TestAListenerRemovedMidDispatchIsSkipped(t *testing.T) { // #242
	root := cordis.New()
	seen := &Log{}
	var second atomic.Pointer[cordis.EffectHandle]
	MustWait(t, Must(root.Apply("a", func(ctx *cordis.Context) error {
		_, err := tick.On(ctx, func(*cordis.Context, int) error {
			seen.Add("a")
			second.Load().Dispose()
			return nil
		})
		return err
	})))
	MustWait(t, Must(root.Apply("b", func(ctx *cordis.Context) error {
		h, err := tick.On(ctx, func(*cordis.Context, int) error { seen.Add("b"); return nil })
		second.Store(h)
		return err
	})))

	NoError(t, tick.Emit(root, 0))
	Equal(t, "a", seen.String())
}

func TestParallelFailuresNameTheEventAndFlattenInTheLogger(t *testing.T) { // #205, #222
	root, logs := Quiet()
	for _, message := range []string{"first", "second"} {
		MustWait(t, Must(root.Apply(message, func(ctx *cordis.Context) error {
			_, err := tick.On(ctx, func(*cordis.Context, int) error { return errors.New(message) })
			return err
		})))
	}

	err := tick.Parallel(root, 1)
	var aggregate *cordis.AggregateError
	True(t, errors.As(err, &aggregate), fmt.Sprintf("got %v", err))
	True(t, strings.HasPrefix(aggregate.Error(), `one or more listeners of event "tick" failed`), aggregate.Error())
	Equal(t, 2, len(aggregate.Errors))

	root.Logger().Error(err)
	root.Logger().Error(&cordis.AggregateError{Message: "empty"})
	Equal(t, "first,second,empty", strings.Join(Texts(logs, cordis.LevelError), ","))
}
