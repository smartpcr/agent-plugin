package cordis_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

// ---------------------------------------------------------- introspection

func TestContextAndFiberIntrospection(t *testing.T) {
	root, _ := Quiet()
	True(t, root.Root() == root, "root is its own root")
	True(t, root.Parent() == nil, "root has no parent")
	Equal(t, 0, root.Fiber().ID())
	True(t, root.Fiber().Runtime() == nil, "root fiber has no runtime")
	Equal(t, "Context <root>", root.String())

	component := cordis.Define("probe", func(ctx *cordis.Context, _ cordis.None) error {
		_, err := ctx.Effect("probe-effect", func() (cordis.Disposer, error) { return nil, nil })
		return err
	}, cordis.WithInject("a"), cordis.WithInjectConfig("b", 7), cordis.WithInjectConfig("a", 1))
	Equal(t, "probe", component.Name())
	Equal(t, "Component <probe>", component.String())
	inject := component.Inject()
	Equal(t, 2, len(inject))
	Equal(t, "a", inject[0].Name)
	Equal(t, any(1), inject[0].Config, "a later declaration replaces the earlier one")

	MustWait(t, Must(root.Provide("a", "A")).Fiber())
	_ = Must(root.Provide("b", "B"))
	fiber := MustWait(t, Must(root.Use(component, nil, "a", "c")))
	_ = Must(root.Provide("c", "C"))
	MustWait(t, fiber)
	Equal(t, cordis.Active, fiber.State())

	True(t, fiber.ID() > 0, "a component fiber has a positive id")
	True(t, fiber.Parent() == root, "fiber parent is the context it was used from")
	True(t, fiber.Ctx().Root() == root, "fiber context shares the root")
	True(t, fiber.Ctx().Parent() == root, "fiber context derives from the use site")
	True(t, fiber.Ctx().Fiber() == fiber, "fiber context is owned by the fiber")
	Equal(t, "probe", fiber.Runtime().Name())
	True(t, fiber.Runtime().Component() == component, "runtime keeps the component")
	Equal(t, "Fiber <probe> (Active)", fiber.String())
	Equal(t, 3, len(fiber.Inject()), "use-site inject is appended once")

	var labels []string
	for _, effect := range fiber.Effects() {
		labels = append(labels, effect.Label())
		True(t, effect.Fiber() == fiber, "effect belongs to its fiber")
	}
	True(t, slices.Contains(labels, "probe-effect"), "effects list the body's effect: "+strings.Join(labels, ","))
	Equal(t, "Effect <probe-effect>", fiber.Effects()[0].String())
}

func TestRegistryTracksAndRemovesComponents(t *testing.T) {
	root, _ := Quiet()
	registry := root.Registry()
	base := registry.Len()
	log := &Log{}
	component := cordis.Define("tracked", func(ctx *cordis.Context, _ cordis.None) error {
		_, err := ctx.Effect("", cleanup(log, "unloaded"))
		return err
	})
	True(t, !registry.Has(component), "unused component is not registered")

	first := MustWait(t, Must(root.Use(component, nil)))
	second := MustWait(t, Must(root.Use(component, nil)))
	True(t, registry.Has(component), "used component is registered")
	Equal(t, base+1, registry.Len())
	runtimes := registry.Runtimes()
	Equal(t, "tracked", runtimes[len(runtimes)-1].Name())
	Equal(t, 2, len(registry.Get(component).Fibers()))

	Within(t, "remove", func() { registry.Remove(component) })
	Equal(t, "unloaded,unloaded", log.String())
	Equal(t, cordis.Disposed, first.State())
	Equal(t, cordis.Disposed, second.State())
	True(t, !registry.Has(component), "removed component is unregistered")
	Within(t, "remove again", func() { registry.Remove(component) })
	True(t, registry.Replace(nil, component) == nil, "replace of nil moves nothing")
}

func TestInternalReportsPluginAndReplace(t *testing.T) {
	root, _ := Quiet()
	var plugged, replaced atomic.Int32
	removePlugin := root.Internal().OnPlugin(func(*cordis.Fiber) { plugged.Add(1) })
	defer removePlugin()
	removeReplace := root.Internal().OnReplace(func(*cordis.Fiber) { replaced.Add(1) })
	defer removeReplace()

	old := cordis.Define("old", func(*cordis.Context, cordis.None) error { return nil })
	next := cordis.Define("next", func(*cordis.Context, cordis.None) error { return nil })
	fiber := MustWait(t, Must(root.Use(old, nil)))
	Eventually(t, func() bool { return plugged.Load() == 1 }, "plugin raised on use")

	Equal(t, 1, len(root.Registry().Replace(old, next)))
	MustWait(t, fiber)
	Equal(t, "next", fiber.Name())
	Eventually(t, func() bool { return replaced.Load() == 1 }, "replace raised")

	Within(t, "dispose", fiber.Dispose)
	Eventually(t, func() bool { return plugged.Load() == 2 }, "plugin raised on dispose")
}

// ------------------------------------------------------------------ config

type limits struct {
	Max int `json:"max"`
}

func (l limits) Validate() error {
	if l.Max < 0 {
		return errors.New("max must not be negative")
	}
	return nil
}

func TestConfigAcceptsEveryInputShape(t *testing.T) {
	root, _ := Quiet()
	var seen atomic.Int64
	component := cordis.Define("limits", func(_ *cordis.Context, config limits) error {
		seen.Store(int64(config.Max))
		return nil
	}, cordis.WithDefaults(func() limits { return limits{Max: 5} }))

	for _, tc := range []struct {
		name  string
		input any
		want  int64
	}{
		{"nil", nil, 5},
		{"value", limits{Max: 1}, 1},
		{"pointer", &limits{Max: 2}, 2},
		{"nil pointer", (*limits)(nil), 5},
		{"raw json", json.RawMessage(`{"max":3}`), 3},
		{"bytes", []byte(`{"max":4}`), 4},
		{"json null", []byte(" null "), 5},
		{"map", map[string]any{"max": 6}, 6},
	} {
		fiber := MustWait(t, Must(root.Use(component, tc.input)))
		Equal(t, tc.want, seen.Load(), tc.name)
		Within(t, "dispose "+tc.name, fiber.Dispose)
	}

	for _, tc := range []struct {
		name  string
		input any
		want  string
	}{
		{"wrong type", 42, "expected cordis_test.limits, got int"},
		{"bad json", []byte(`{"max":`), "unexpected end of JSON input"},
		{"unmarshalable map", map[string]any{"max": func() {}}, "unsupported type"},
		{"validator", limits{Max: -1}, "max must not be negative"},
	} {
		fiber := Must(root.Use(component, tc.input))
		err := Wait(t, fiber)
		var configError *cordis.ConfigError
		True(t, errors.As(err, &configError), tc.name+": want a ConfigError, got "+errString(err))
		True(t, strings.Contains(err.Error(), tc.want), tc.name+": "+err.Error())
		Within(t, "dispose "+tc.name, fiber.Dispose)
	}
}

func TestCustomSchemaResolvesConfig(t *testing.T) {
	root, _ := Quiet()
	var seen atomic.Int64
	double := cordis.SchemaFunc(func(config any) (any, error) {
		value := config.(limits)
		switch {
		case value.Max == 13:
			return nil, &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Message: "unlucky", Path: "max"}}}
		case value.Max == 99:
			return "not limits", nil
		case value.Max < 0:
			return nil, errors.New("negative")
		}
		return limits{Max: value.Max * 2}, nil
	})
	component := cordis.Define("schema", func(_ *cordis.Context, config limits) error {
		seen.Store(int64(config.Max))
		return nil
	}, cordis.WithSchema(double))

	fiber := MustWait(t, Must(root.Use(component, limits{Max: 4})))
	Equal(t, int64(8), seen.Load())
	Within(t, "dispose", fiber.Dispose)

	for _, tc := range []struct {
		max  int
		want string
	}{
		{13, "invalid config:\n  - unlucky (at max)"},
		{99, "schema returned string, expected cordis_test.limits"},
		{-1, "invalid config:\n  - negative"},
	} {
		fiber := Must(root.Use(component, limits{Max: tc.max}))
		err := Wait(t, fiber)
		True(t, err != nil && strings.Contains(err.Error(), tc.want), "schema error: "+errString(err))
		Within(t, "dispose", fiber.Dispose)
	}
}

func TestPointerConfigDefaultsToAllocatedZero(t *testing.T) {
	root, _ := Quiet()
	var got atomic.Pointer[limits]
	component := cordis.Define("pointer", func(_ *cordis.Context, config *limits) error {
		got.Store(config)
		return nil
	})
	fiber := MustWait(t, Must(root.Use(component, nil)))
	True(t, got.Load() != nil, "a pointer config defaults to a new zero value")
	Within(t, "dispose", fiber.Dispose)
}

// ---------------------------------------------------------------- services

func TestSetReplacesOwnedServiceOnly(t *testing.T) {
	root, _ := Quiet()
	key := cordis.NewServiceKey[int]("count")
	Equal(t, "count", key.Name())
	True(t, strings.Contains(errString(key.Set(root, 1)), "without Provide"), "set before provide fails")

	_ = Must(key.Provide(root, 1))
	NoError(t, key.Set(root, 2))
	value, ok := key.Lookup(root)
	True(t, ok, "lookup finds the service")
	Equal(t, 2, value)

	other := MustWait(t, Must(root.Apply("other", func(ctx *cordis.Context) error { return nil })))
	err := other.Ctx().Set("count", 3)
	True(t, strings.Contains(errString(err), "owned by <root>"), "set by a non-owner fails: "+errString(err))

	_ = Must(root.Provide("name", "text"))
	_, err = cordis.NewServiceKey[int]("name").Get(root)
	True(t, strings.Contains(errString(err), `service "name" is string, not int`), "typed get checks the type")
	_, ok = cordis.NewServiceKey[int]("name").Lookup(root)
	True(t, !ok, "typed lookup rejects another type")
	_, ok = cordis.NewServiceKey[int]("missing").Lookup(root)
	True(t, !ok, "typed lookup of a missing service")
	Within(t, "dispose", other.Dispose)
}

type flaky struct{ ok bool }

func (f flaky) Available() bool {
	if !f.ok {
		panic("checker exploded")
	}
	return true
}

func TestPanickingCheckerMakesServiceUnavailable(t *testing.T) {
	root, logs := Quiet()
	_ = Must(root.Provide("svc", flaky{}))
	fiber := MustWait(t, Must(root.Apply("dependent", func(*cordis.Context) error { return nil }, "svc")))
	Equal(t, cordis.Pending, fiber.State())
	Eventually(t, func() bool { return Count(logs, cordis.LevelError) > 0 }, "checker panic is logged")

	NoError(t, root.Set("svc", flaky{ok: true}))
	MustWait(t, fiber)
	Equal(t, cordis.Active, fiber.State())
	Within(t, "dispose", fiber.Dispose)
}

func TestServiceErrorsDescribeTheirCause(t *testing.T) {
	root, _ := Quiet()
	fiber := MustWait(t, Must(root.Apply("probe", func(*cordis.Context) error { return nil })))
	_, err := fiber.Ctx().Get("undeclared")
	True(t, errors.Is(err, cordis.ErrNotInjected), "undeclared service: "+errString(err))
	True(t, strings.Contains(err.Error(), "without inject"), err.Error())
	Within(t, "dispose", fiber.Dispose)

	unavailable := &cordis.ServiceError{Name: "x", Err: cordis.ErrUnavailable}
	True(t, strings.Contains(unavailable.Error(), "inactive context"), unavailable.Error())

	inner := errors.New("boom")
	True(t, errors.Is(&cordis.PanicError{Value: inner}, inner), "panic error unwraps an error value")
	True(t, (&cordis.PanicError{Value: "text"}).Unwrap() == nil, "panic error with a non-error value")
	Equal(t, "IsolateKey(realm)", cordis.NewIsolateKey("realm").String())
}

// ------------------------------------------------------------------ events

func TestEventKeysPrependAndName(t *testing.T) {
	root, _ := Quiet()
	log := &Log{}
	Equal(t, "tick", tick.Name())
	Equal(t, "resolve", resolve.Name())
	Equal(t, "transform", transform.Name())

	_ = Must(tick.On(root, func(_ *cordis.Context, n int) error { log.Add("on"); return nil }))
	_ = Must(tick.Prepend(root, func(_ *cordis.Context, n int) error { log.Add("first"); return nil }))
	NoError(t, tick.Emit(root, 1))
	Equal(t, "first,on", log.String())

	_ = Must(resolve.On(root, func(_ *cordis.Context, q string) (string, bool, error) { return "late", true, nil }))
	_ = Must(resolve.Prepend(root, func(_ *cordis.Context, q string) (string, bool, error) { return "early", true, nil }))
	answer, ok, err := resolve.Bail(root, "q")
	NoError(t, err)
	True(t, ok, "bail answered")
	Equal(t, "early", answer)

	_ = Must(transform.On(root, func(_ *cordis.Context, v string, next func(string) (string, error)) (string, error) {
		return next(v + "b")
	}))
	_ = Must(transform.Prepend(root, func(_ *cordis.Context, v string, next func(string) (string, error)) (string, error) {
		return next(v + "a")
	}))
	out, err := transform.Waterfall(root, "", func(v string) (string, error) { return v, nil })
	NoError(t, err)
	Equal(t, "ab", out)
}

// ------------------------------------------------------------------ logger

func TestLoggerLevelsAndExporters(t *testing.T) {
	root, logs := Quiet()
	logger := root.Logger()
	Equal(t, "root", logger.Name())

	logger.Errorf("e%d", 1)
	logger.Warnf("w%d", 2)
	logger.Infof("i%d", 3)
	logger.Debugf("d%d", 4)
	Equal(t, "e1", strings.Join(Texts(logs, cordis.LevelError), ","))
	Equal(t, "w2", strings.Join(Texts(logs, cordis.LevelWarn), ","))
	Equal(t, "i3", strings.Join(Texts(logs, cordis.LevelInfo), ","))
	Equal(t, 0, Count(logs, cordis.LevelDebug), "debug is below the default threshold")

	root.Loggers().SetLevel(cordis.LevelDebug)
	Equal(t, cordis.LevelDebug, root.Loggers().Level())
	logger.Debugf("d%d", 5)
	Equal(t, 1, Count(logs, cordis.LevelDebug))

	quiet := root.Intercept(cordis.LoggerServiceName, &cordis.LoggerConfig{Level: cordis.LevelSilent})
	quiet.Logger().Errorf("dropped")
	Equal(t, 1, Count(logs, cordis.LevelError), "a silent intercept drops errors")

	var stdout, stderr bytes.Buffer
	removeConsole := root.Loggers().AddExporter(cordis.NewConsoleExporter(&stdout, &stderr))
	var slogOut bytes.Buffer
	removeSlog := root.Loggers().AddExporter(cordis.SlogExporter{Handler: slog.NewTextHandler(&slogOut, &slog.HandlerOptions{Level: slog.LevelWarn})})
	logger.Infof("hello")
	logger.Warnf("careful")
	logger.Error(&cordis.PanicError{Value: "bad", Stack: []byte("stack-trace")})
	logger.Debugf("hidden")
	removeConsole()
	removeSlog()
	logger.Errorf("after removal")

	True(t, strings.Contains(stdout.String(), "[I] root hello"), "info goes to out: "+stdout.String())
	True(t, strings.Contains(stderr.String(), "[W] root careful"), "warn goes to err: "+stderr.String())
	True(t, strings.Contains(stderr.String(), "stack-trace"), "panic stack is printed")
	True(t, !strings.Contains(stderr.String(), "after removal"), "removed exporter receives nothing")
	True(t, strings.Contains(slogOut.String(), "msg=careful") && strings.Contains(slogOut.String(), "logger=root"), "slog: "+slogOut.String())
	True(t, strings.Contains(slogOut.String(), "level=ERROR"), "slog maps errors")
	True(t, !strings.Contains(slogOut.String(), "hello"), "slog handler filters below its level")

	for level, want := range map[cordis.LogLevel]string{
		cordis.LevelSilent: "Silent", cordis.LevelError: "Error", cordis.LevelWarn: "Warn",
		cordis.LevelInfo: "Info", cordis.LevelDebug: "Debug", cordis.LogLevel(9): "LogLevel(9)",
	} {
		Equal(t, want, level.String())
	}
	for state, want := range map[cordis.FiberState]string{
		cordis.Pending: "Pending", cordis.Loading: "Loading", cordis.Active: "Active", cordis.Failed: "Failed",
		cordis.Disposed: "Disposed", cordis.Unloading: "Unloading", cordis.FiberState(42): "FiberState(?)",
	} {
		Equal(t, want, state.String())
	}
}

// ----------------------------------------------------------------- effects

func TestEffectAsyncKeepsFiberLoadingUntilAcquired(t *testing.T) {
	root, _ := Quiet()
	log := &Log{}
	release := make(chan struct{})
	fiber := Must(root.Apply("async", func(ctx *cordis.Context) error {
		_, err := ctx.EffectAsync("slow", func() (cordis.Disposer, error) {
			<-release
			log.Add("acquired")
			return func() error { log.Add("released"); return nil }, nil
		})
		return err
	}))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		Within(t, "dispose", fiber.Dispose)
	})
	Eventually(t, func() bool { return fiber.State() == cordis.Loading }, "fiber loads")
	close(release)
	MustWait(t, fiber)
	Equal(t, cordis.Active, fiber.State())
	Within(t, "dispose", fiber.Dispose)
	Equal(t, "acquired,released", log.String())

	failing := Must(root.Apply("async-fail", func(ctx *cordis.Context) error {
		_, err := ctx.EffectAsync("", func() (cordis.Disposer, error) { return nil, errors.New("no resource") })
		return err
	}))
	t.Cleanup(func() { Within(t, "dispose failing", failing.Dispose) })
	err := Wait(t, failing)
	True(t, err != nil && strings.Contains(err.Error(), "no resource"), "async failure fails the load: "+errString(err))
	Equal(t, cordis.Failed, failing.State())
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}
