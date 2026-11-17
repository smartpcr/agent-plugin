// Package probe is the probe host the behaviour suites drive: one cordis tree per
// case, and configurable probe components that record what happens to them in an
// ordered trace. test/e2e/schema/actions.catalog.yaml documents the probe's keys and
// every trace marker; the C# binding records the same traces.
package probe

import (
	"encoding/json"
	"fmt"
	"strconv"
	"sync/atomic"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Spec is a probe's config: the keys at the top of the action catalog. It arrives
// as a Spec from component.use and children, or as JSON from a loader entry.
type Spec struct {
	// ID is the fiber id that names the probe in the trace. A loader entry's probe
	// has none in its config; its id is the entry's composite id.
	ID                  string   `json:"id,omitempty"`
	Provides            []string `json:"provides,omitempty"`
	Inject              []string `json:"inject,omitempty"`
	Optional            []string `json:"optional,omitempty"`
	Effects             []string `json:"effects,omitempty"`
	InitGate            string   `json:"initGate,omitempty"`
	CleanupGate         string   `json:"cleanupGate,omitempty"`
	ReadOnCleanup       string   `json:"readOnCleanup,omitempty"`
	Fail                string   `json:"fail,omitempty"`
	FailWhileValueBelow *int64   `json:"failWhileValueBelow,omitempty"`
	AcceptUpdates       bool     `json:"acceptUpdates,omitempty"`
	MaxValue            *int64   `json:"maxValue,omitempty"`
	Value               int64    `json:"value,omitempty"`
	Children            []Spec   `json:"children,omitempty"`
}

// Validate is the probe's schema: a value above maxValue is rejected.
func (s Spec) Validate() error {
	if s.MaxValue != nil && s.Value > *s.MaxValue {
		return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{
			Message: fmt.Sprintf("value %d exceeds maxValue %d", s.Value, *s.MaxValue),
			Path:    "value",
		}}}
	}
	return nil
}

// failsNow reports whether a configured failure applies to this value.
func (s Spec) failsNow() bool {
	return s.FailWhileValueBelow == nil || s.Value < *s.FailWhileValueBelow
}

// SpecOf reads a fiber's config, as handed in, back into a Spec.
func SpecOf(config any) Spec {
	switch held := config.(type) {
	case Spec:
		return held
	case *Spec:
		if held != nil {
			return *held
		}
	case json.RawMessage:
		var spec Spec
		if len(held) > 0 {
			_ = json.Unmarshal(held, &spec)
		}
		return spec
	case []byte:
		var spec Spec
		_ = json.Unmarshal(held, &spec)
		return spec
	}
	return Spec{}
}

// Handle is the value a probe provides for a service: a lookup reports its
// provider, and a read during a dependent's cleanup reports whether the resource
// behind it is still open.
type Handle struct {
	Provider string
	closed   atomic.Bool
}

// Closed reports whether the provider released the resource behind the service.
func (h *Handle) Closed() bool { return h.closed.Load() }

// Text is a looked-up service value as the suites compare it: the provider id for
// a probe's handle, the value itself for service.provide.
func Text(value any) string {
	switch held := value.(type) {
	case *Handle:
		return held.Provider
	case string:
		return held
	case nil:
		return ""
	default:
		return fmt.Sprint(held)
	}
}

// component returns the probe component for an implementation name, creating it
// with inject on first use. Distinct names are distinct components.
func (h *Host) component(name string, inject []string) *cordis.Component {
	if name == "" {
		name = "probe"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if c, ok := h.components[name]; ok {
		return c
	}
	c := cordis.Define(name, h.body, cordis.WithInject(inject...))
	h.components[name] = c
	return c
}

// body is the probe component, in the order the catalog fixes.
func (h *Host) body(ctx *cordis.Context, spec Spec) error {
	id := spec.ID
	if id == "" {
		id = h.idOf(ctx.Fiber())
	}

	// 1. the load
	h.Record(id + ":load")
	h.addLoad(id)

	// 2. registered first, so released last
	if _, err := ctx.Effect("probe/unload", func() (cordis.Disposer, error) {
		return func() error { h.Record(id + ":unload"); return nil }, nil
	}); err != nil {
		return err
	}

	// 3. an asynchronous cleanup that may read a service it injects
	if spec.CleanupGate != "" {
		if _, err := ctx.Effect("probe/cleanup", func() (cordis.Disposer, error) {
			return func() error {
				h.Record(id + ":cleanup-start")
				h.waitGate(spec.CleanupGate)
				if spec.ReadOnCleanup != "" {
					h.Record(fmt.Sprintf("%s:read %s=%s", id, spec.ReadOnCleanup, readState(ctx, spec.ReadOnCleanup)))
				}
				h.Record(id + ":cleanup-done")
				return nil
			}, nil
		}); err != nil {
			return err
		}
	}

	// 4. provided services, each with the resource behind it
	for _, svc := range spec.Provides {
		handle := &Handle{Provider: id}
		if _, err := ctx.Provide(svc, handle); err != nil {
			return err
		}
		if _, err := ctx.Effect("probe/resource", func() (cordis.Disposer, error) {
			h.Record(id + ":+" + svc + ".resource")
			return func() error {
				handle.closed.Store(true)
				h.Record(id + ":-" + svc + ".resource")
				return nil
			}, nil
		}); err != nil {
			return err
		}
	}

	// 5. effects, released in reverse
	for _, name := range spec.Effects {
		if _, err := ctx.Effect("probe/effect", func() (cordis.Disposer, error) {
			h.Record(id + ":+" + name)
			return func() error { h.Record(id + ":-" + name); return nil }, nil
		}); err != nil {
			return err
		}
	}

	// 6. a failing body
	if spec.Fail == "body" && spec.failsNow() {
		return fmt.Errorf("probe %s: the body failed", id)
	}

	// 7. updates accepted in place
	if spec.AcceptUpdates {
		if _, err := cordis.AcceptUpdate(ctx, func(next Spec) {
			h.Record(id + ":accepted " + strconv.FormatInt(next.Value, 10))
		}); err != nil {
			return err
		}
	}

	// 8. optional scopes that follow their service
	for _, svc := range spec.Optional {
		if _, err := ctx.Inject([]string{svc}, func(scope *cordis.Context) error {
			_, err := scope.Effect("probe/optional", func() (cordis.Disposer, error) {
				h.Record(id + ":optional+" + svc)
				return func() error { h.Record(id + ":optional-" + svc); return nil }, nil
			})
			return err
		}); err != nil {
			return err
		}
	}

	// 9. children, with the same implementation
	if len(spec.Children) > 0 {
		component := ctx.Fiber().Runtime().Component()
		for _, child := range spec.Children {
			fiber, err := ctx.Use(component, child, child.Inject...)
			if err != nil {
				return err
			}
			h.register(child.ID, fiber)
		}
	}

	// 10. an asynchronous init: the fiber stays Loading, its services invisible
	failInit := spec.Fail == "init" && spec.failsNow()
	if spec.InitGate != "" || spec.Fail == "init" {
		h.Record(id + ":init-start")
		if spec.InitGate != "" && !h.waitGate(spec.InitGate) {
			return fmt.Errorf("probe %s: init cancelled", id)
		}
		if failInit {
			return fmt.Errorf("probe %s: init failed", id)
		}
		h.Record(id + ":init-done")
	}
	return nil
}

// readState reads svc through ctx as a dependent's cleanup would.
func readState(ctx *cordis.Context, svc string) string {
	value, err := ctx.Get(svc)
	if err != nil {
		return "missing"
	}
	if handle, ok := value.(*Handle); ok && handle.Closed() {
		return "closed"
	}
	return "open"
}
