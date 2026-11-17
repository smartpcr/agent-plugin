// Package actions is the Go binding of the cordis behaviour suites: one handler per
// action in test/e2e/schema/actions.catalog.yaml, driving the probe host on top of
// the Go port (src/go).
package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	bdd "github.com/smartpcr/bdd-code-extension/runtimes/go/suite/actions"
	"github.com/smartpcr/bdd-code-extension/runtimes/go/suite/extension"
	"github.com/smartpcr/bdd-code-extension/runtimes/go/suite/model"
	"github.com/smartpcr/bdd-code-extension/runtimes/go/suite/value"
	"github.com/smartpcr/bdd-code-extension/runtimes/go/suite/walk"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/test/e2e/bindings/go/probe"
)

// Registrations is every catalog action this binding implements.
var Registrations = []bdd.Registration{
	{Name: "host.start", Handler: hostStart},
	{Name: "host.dispose", Handler: hostDispose},
	{Name: "host.trace", Handler: hostTrace},
	{Name: "host.settle", Handler: hostSettle},
	{Name: "host.waitFor", Handler: hostWaitFor},
	{Name: "gate.open", Handler: gateOpen},
	{Name: "component.use", Handler: componentUse},
	{Name: "component.wait", Handler: componentWait},
	{Name: "component.state", Handler: componentState},
	{Name: "component.update", Handler: componentUpdate},
	{Name: "component.restart", Handler: componentRestart},
	{Name: "component.dispose", Handler: componentDispose},
	{Name: "component.replace", Handler: componentReplace},
	{Name: "service.provide", Handler: serviceProvide},
	{Name: "service.withdraw", Handler: serviceWithdraw},
	{Name: "service.lookup", Handler: serviceLookup},
	{Name: "events.listen", Handler: eventsListen},
	{Name: "events.dispatch", Handler: eventsDispatch},
	{Name: "timer.schedule", Handler: timerSchedule},
	{Name: "loader.apply", Handler: loaderApply},
	{Name: "loader.entry", Handler: loaderEntry},
	{Name: "loader.setDisabled", Handler: loaderSetDisabled},
	{Name: "configFile.open", Handler: configFileOpen},
	{Name: "configFile.write", Handler: configFileWrite},
	{Name: "configFile.sync", Handler: configFileSync},
	{Name: "configFile.read", Handler: configFileRead},
}

// Extension is the binding as the suite runtime loads it.
var Extension = extension.Extension{
	Name:    "cordis-go",
	Actions: Registrations,
	Start: func(map[string]string) extension.RunState {
		return extension.RunState{State: &probe.Session{}}
	},
}

// ------------------------------------------------------------------ helpers

func session(request *bdd.Request) (*probe.Session, error) {
	s, ok := request.State.(*probe.Session)
	if !ok || s == nil {
		return nil, bdd.Failed("the cordis binding has no run state")
	}
	return s, nil
}

func host(request *bdd.Request) (*probe.Host, error) {
	s, err := session(request)
	if err != nil {
		return nil, err
	}
	h := s.Current()
	if h == nil {
		return nil, bdd.Failed(request.Action + ": no host; run host.start first")
	}
	return h, nil
}

func fail(request *bdd.Request, err error) error {
	return bdd.Failed(fmt.Sprintf("%s: %v", request.Action, err))
}

func text(request *bdd.Request, key string) string { return walk.Text(request.Step, key) }

func flag(request *bdd.Request, key string, fallback bool) bool {
	if held, ok := request.Step.Value(key).(bool); ok {
		return held
	}
	return fallback
}

func integer(request *bdd.Request, key string, fallback int64) int64 {
	if number, ok := value.AsNumber(request.Step.Value(key)); ok {
		return int64(number)
	}
	return fallback
}

func texts(request *bdd.Request, key string) []string {
	list, _ := walk.List(request.Step, key)
	out := make([]string, 0, len(list))
	for _, item := range list {
		out = append(out, value.Text(item))
	}
	return out
}

func labels(request *bdd.Request, key string) map[string]string {
	held, ok := walk.AsMap(request.Step.Value(key))
	if !ok {
		return nil
	}
	out := map[string]string{}
	for _, name := range held.Keys() {
		out[name] = value.Text(held.Value(name))
	}
	return out
}

// decode converts a step value (ordered maps, lists, scalars) into target via JSON.
func decode(held any, target any) error {
	encoded, err := json.Marshal(model.Plain(held))
	if err != nil {
		return err
	}
	return json.Unmarshal(encoded, target)
}

func entries(request *bdd.Request) ([]loader.EntryOptions, error) {
	list, ok := walk.List(request.Step, "entries")
	if !ok {
		return nil, fmt.Errorf("entries is required")
	}
	var out []loader.EntryOptions
	if err := decode(list, &out); err != nil {
		return nil, fmt.Errorf("entries: %w", err)
	}
	return out, nil
}

func capture(pairs ...any) *model.Map {
	out := model.NewMap()
	for i := 0; i+1 < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1])
	}
	return out
}

func ok() *model.Map { return capture("ok", true) }

func errorText(err error) any {
	if err == nil {
		return nil
	}
	return err.Error()
}

func fiberOf(request *bdd.Request, h *probe.Host, id string) (*cordis.Fiber, error) {
	fiber := h.Fiber(id)
	if fiber == nil {
		return nil, bdd.Failed(fmt.Sprintf("%s: no fiber %q", request.Action, id))
	}
	return fiber, nil
}

// fiberState is the capture shared by component.wait, state, restart and dispose.
func fiberState(h *probe.Host, id string, fiber *cordis.Fiber) *model.Map {
	return capture(
		"id", id,
		"state", fiber.State().String(),
		"loads", h.Loads(id),
		"value", probe.SpecOf(fiber.Config()).Value,
		"implementation", fiber.Name(),
		"error", errorText(fiber.Err()),
	)
}

// startAndWatch runs fn in the background and returns once changed reports true, or
// after a short bound: an action told not to wait still starts its work first.
func startAndWatch(ctx context.Context, fn func(), changed func() bool) {
	go fn()
	deadline := time.Now().Add(time.Second)
	for !changed() && time.Now().Before(deadline) && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(5 * time.Millisecond)
}

// --------------------------------------------------------------------- host

func hostStart(request *bdd.Request) (any, error) {
	s, err := session(request)
	if err != nil {
		return nil, err
	}
	if _, err := s.Start(request.Context, flag(request, "loader", true), flag(request, "timer", true)); err != nil {
		return nil, fail(request, err)
	}
	return ok(), nil
}

func hostDispose(request *bdd.Request) (any, error) {
	s, err := session(request)
	if err != nil {
		return nil, err
	}
	if h := s.Current(); h != nil {
		if err := h.Dispose(request.Context); err != nil {
			return nil, fail(request, err)
		}
	}
	return ok(), nil
}

func hostTrace(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	events := h.Trace(text(request, "prefix"), text(request, "contains"))
	list := make([]any, len(events))
	for i, event := range events {
		list[i] = event
	}
	return capture("events", list, "joined", join(events), "count", int64(len(events))), nil
}

func join(events []string) string { return strings.Join(events, ",") }

func hostSettle(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	if err := h.Settle(request.Context); err != nil {
		return nil, fail(request, err)
	}
	return ok(), nil
}

func hostWaitFor(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	if err := h.WaitFor(request.Context, text(request, "marker")); err != nil {
		return nil, fail(request, err)
	}
	return capture("found", true, "joined", join(h.Trace("", ""))), nil
}

func gateOpen(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	name := text(request, "name")
	h.OpenGate(name)
	return capture("name", name), nil
}

// --------------------------------------------------------------- components

func componentUse(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	var spec probe.Spec
	if err := decode(request.Step, &spec); err != nil {
		return nil, fail(request, err)
	}
	if spec.ID == "" {
		return nil, fail(request, fmt.Errorf("id is required"))
	}
	implementation := text(request, "implementation")
	fiber, err := h.Use(spec, text(request, "parent"), implementation, labels(request, "isolate"))
	if err != nil {
		return nil, fail(request, err)
	}
	return capture("id", spec.ID, "state", fiber.State().String()), nil
}

func componentWait(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	id := text(request, "id")
	fiber, err := fiberOf(request, h, id)
	if err != nil {
		return nil, err
	}
	if err := probe.WaitFiber(request.Context, fiber); err != nil {
		return nil, fail(request, err)
	}
	return fiberState(h, id, fiber), nil
}

func componentState(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	id := text(request, "id")
	fiber, err := fiberOf(request, h, id)
	if err != nil {
		return nil, err
	}
	return fiberState(h, id, fiber), nil
}

func componentUpdate(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	id := text(request, "id")
	fiber, err := fiberOf(request, h, id)
	if err != nil {
		return nil, err
	}
	spec := probe.SpecOf(fiber.Config())
	spec.Value = integer(request, "value", 0)

	if !flag(request, "wait", true) {
		// Update applies the config before it reloads; return once it has, so the
		// next update starts after this one.
		startAndWatch(request.Context,
			func() { _ = fiber.Update(spec, false) },
			func() bool { return probe.SpecOf(fiber.Config()).Value == spec.Value })
		return capture("accepted", true, "error", nil, "state", fiber.State().String(), "loads", h.Loads(id)), nil
	}
	var updateErr error
	if err := run(request.Context, func() { updateErr = fiber.Update(spec, false) }); err != nil {
		return nil, fail(request, err)
	}
	return capture(
		"accepted", updateErr == nil,
		"error", errorText(updateErr),
		"state", fiber.State().String(),
		"loads", h.Loads(id),
	), nil
}

func componentRestart(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	id := text(request, "id")
	fiber, err := fiberOf(request, h, id)
	if err != nil {
		return nil, err
	}
	if !flag(request, "wait", true) {
		before := fiber.State()
		startAndWatch(request.Context,
			func() { _ = fiber.Restart() },
			func() bool { return fiber.State() != before })
		return fiberState(h, id, fiber), nil
	}
	if err := run(request.Context, func() { _ = fiber.Restart() }); err != nil {
		return nil, fail(request, err)
	}
	return fiberState(h, id, fiber), nil
}

func componentDispose(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	id := text(request, "id")
	fiber, err := fiberOf(request, h, id)
	if err != nil {
		return nil, err
	}
	if !flag(request, "wait", true) {
		go fiber.Dispose()
		return fiberState(h, id, fiber), nil
	}
	if err := run(request.Context, fiber.Dispose); err != nil {
		return nil, fail(request, err)
	}
	return fiberState(h, id, fiber), nil
}

func componentReplace(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	moved := h.Replace(text(request, "from"), text(request, "to"), texts(request, "inject"))
	return capture("moved", int64(moved)), nil
}

// ----------------------------------------------------------------- services

func serviceProvide(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	name := text(request, "name")
	provided := name
	if request.Step.Has("value") {
		provided = text(request, "value")
	}
	if err := h.Provide(name, provided, text(request, "realm")); err != nil {
		return nil, fail(request, err)
	}
	return capture("name", name), nil
}

func serviceWithdraw(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	if err := h.Withdraw(request.Context, text(request, "name"), flag(request, "wait", true)); err != nil {
		return nil, fail(request, err)
	}
	return capture("done", true), nil
}

func serviceLookup(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	found, isFound, failure, err := h.Lookup(text(request, "from"), text(request, "name"), flag(request, "optional", false))
	if err != nil {
		return nil, fail(request, err)
	}
	var shown, reason any
	if isFound {
		shown = probe.Text(found)
	}
	if failure != "" {
		reason = failure
	}
	return capture("found", isFound, "value", shown, "error", reason), nil
}

// ------------------------------------------------------------------- events

func eventsListen(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	behaviour := text(request, "behaviour")
	if behaviour == "" {
		behaviour = "record"
	}
	label := text(request, "label")
	err = h.Listen(probe.Listener{
		Owner:     text(request, "owner"),
		Event:     text(request, "event"),
		Mode:      text(request, "mode"),
		Label:     label,
		Behaviour: behaviour,
		Answer:    text(request, "answer"),
		Delay:     time.Duration(integer(request, "delayMs", 100)) * time.Millisecond,
		Prepend:   flag(request, "prepend", false),
	})
	if err != nil {
		return nil, fail(request, err)
	}
	return capture("label", label), nil
}

func eventsDispatch(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	var result any
	var failure, dispatchErr error
	var count int64
	if err := run(request.Context, func() {
		result, failure, count, dispatchErr = h.Dispatch(text(request, "event"), text(request, "mode"), text(request, "from"), text(request, "value"))
	}); err != nil {
		return nil, fail(request, err)
	}
	if dispatchErr != nil {
		return nil, fail(request, dispatchErr)
	}
	return capture("result", result, "error", errorText(failure), "errors", count), nil
}

// ------------------------------------------------------------------- timers

func timerSchedule(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	label := text(request, "label")
	err = h.Schedule(text(request, "owner"), label, text(request, "kind"),
		time.Duration(integer(request, "delayMs", 0))*time.Millisecond, int(integer(request, "calls", 1)))
	if err != nil {
		return nil, fail(request, err)
	}
	return capture("label", label), nil
}

// ------------------------------------------------------------------- loader

func loaderApply(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	list, err := entries(request)
	if err != nil {
		return nil, fail(request, err)
	}
	if err := h.Apply(request.Context, list); err != nil {
		return nil, fail(request, err)
	}
	return capture("entries", int64(len(h.Loader.Entries()))), nil
}

func loaderEntry(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	if h.Loader == nil {
		return nil, fail(request, fmt.Errorf("the host was started without the loader"))
	}
	id := text(request, "id")
	entry := h.Loader.Find(id)
	if entry == nil {
		return capture("exists", false, "hasFiber", false, "state", nil, "loads", h.Loads(id), "value", int64(0)), nil
	}
	fiber := h.Loader.Fiber(entry)
	live := fiber != nil && fiber.State() != cordis.Disposed
	var state any
	var config any = h.Loader.Options(entry).Config
	if fiber != nil {
		state = fiber.State().String()
		config = fiber.Config()
	}
	return capture(
		"exists", true,
		"hasFiber", live,
		"state", state,
		"loads", h.Loads(id),
		"value", probe.SpecOf(config).Value,
	), nil
}

func loaderSetDisabled(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	if err := h.SetDisabled(request.Context, text(request, "id"), flag(request, "disabled", false)); err != nil {
		return nil, fail(request, err)
	}
	return ok(), nil
}

func configFileOpen(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	list, err := entries(request)
	if err != nil {
		return nil, fail(request, err)
	}
	path, err := h.OpenConfigFile(request.Context, list, flag(request, "watch", false))
	if err != nil {
		return nil, fail(request, err)
	}
	return capture("path", path), nil
}

func configFileWrite(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	content := text(request, "text")
	if !request.Step.Has("text") {
		list, err := entries(request)
		if err != nil {
			return nil, fail(request, err)
		}
		if content, err = probe.EntriesText(list); err != nil {
			return nil, fail(request, err)
		}
	}
	if err := h.WriteConfigFile(request.Context, content, flag(request, "byRename", false)); err != nil {
		return nil, fail(request, err)
	}
	return ok(), nil
}

func configFileSync(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	if err := h.SyncConfigFile(request.Context); err != nil {
		return nil, fail(request, err)
	}
	return ok(), nil
}

func configFileRead(request *bdd.Request) (any, error) {
	h, err := host(request)
	if err != nil {
		return nil, err
	}
	content, err := h.ReadConfigFile()
	if err != nil {
		return nil, fail(request, err)
	}
	return capture("text", content), nil
}

// run runs fn, giving up when ctx ends first.
func run(ctx context.Context, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
