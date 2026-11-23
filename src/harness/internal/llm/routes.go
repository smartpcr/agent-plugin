package llm

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Adapter is a provider's side of a route: a pure function of the request and
// the call it captured. Retry is not an adapter concern.
type Adapter interface {
	// Prepare captures one call to model with the effective config and fails
	// with an *vocab.Failure for anything it cannot encode. It sends no request:
	// a keyed route's Prepare does no I/O, and a signed-in client route
	// (DH-D-LLM-12) only probes the installed client's version, bounded by a
	// deadline, failing with llm/client-upgrade-required before any request
	// text is written.
	Prepare(ctx context.Context, route vocab.RouteID, model string, cfg vocab.CallConfig) (Call, error)
}

// Call is one captured call. The runtime calls Attempt once per attempt and never
// lets the caller reach it: only Prepared.Stream does.
type Call interface {
	// Attempt is one provider attempt. An error ends it; a failure the provider
	// classified is a *vocab.Failure.
	Attempt(ctx context.Context, req session.Request) iter.Seq2[vocab.Chunk, error]
}

// Model is one catalogue entry of a route.
type Model struct {
	Name string
	// Efforts are the reasoning-effort levels the model accepts; none means the
	// model takes no effort control.
	Efforts []string
}

// Route is one registered route.
type Route struct {
	ID vocab.RouteID
	// Models is the route's catalogue; the first is the default model, used by
	// Complete. Empty admits any model name and has no default.
	Models []Model
	// Defaults are the adapter's controls for every field the caller leaves unset.
	Defaults vocab.Controls
	// MaxConcurrent caps the route's attempts in flight; 0 is no cap.
	MaxConcurrent int
	// Retry is the route's retry policy; zero never retries.
	Retry   RetryPolicy
	Adapter Adapter
}

func (r Route) clone() Route {
	r.Models = slices.Clone(r.Models)
	for i := range r.Models {
		r.Models[i].Efforts = slices.Clone(r.Models[i].Efforts)
	}
	r.Defaults = r.Defaults.Clone()
	return r
}

func (r Route) validate() error {
	if err := r.ID.Validate(); err != nil {
		return err
	}
	if r.Adapter == nil {
		return fmt.Errorf("llm: route %q has no adapter", r.ID)
	}
	if r.MaxConcurrent < 0 {
		return fmt.Errorf("llm: route %q has a negative concurrency cap", r.ID)
	}
	if err := r.Retry.Validate(); err != nil {
		return fmt.Errorf("llm: route %q: %w", r.ID, err)
	}
	seen := map[string]bool{}
	for _, m := range r.Models {
		if m.Name == "" || seen[m.Name] {
			return fmt.Errorf("llm: route %q lists model %q blank or twice", r.ID, m.Name)
		}
		seen[m.Name] = true
	}
	return r.Defaults.Validate()
}

// model is the catalogue entry of name, or the default model for "".
func (r Route) model(name string) (Model, bool) {
	if len(r.Models) == 0 {
		return Model{Name: name}, name != ""
	}
	if name == "" {
		return r.Models[0], true
	}
	for _, m := range r.Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// entry is a registered route with its concurrency slots.
type entry struct {
	route Route
	reg   *Registration
	slots chan struct{} // nil when uncapped
}

// Registration is one provider's set of routes. Replace swaps the whole set at
// once; Close removes it.
type Registration struct {
	rt     *Runtime
	owner  string
	closed bool // under rt.mu
	ids    []vocab.RouteID
}

// ErrDuplicateRoute is returned when a route id is registered twice.
var ErrDuplicateRoute = errors.New("llm/duplicate-route")

// Register adds owner's routes. A route id already served by any registration
// (or listed twice) fails the whole call and nothing changes.
func (rt *Runtime) Register(owner string, routes ...Route) (*Registration, error) {
	reg := &Registration{rt: rt, owner: owner}
	if err := reg.Replace(routes...); err != nil {
		return nil, err
	}
	return reg, nil
}

// Replace atomically replaces the registration's route set: a call prepared
// before keeps the route it captured, and on any error the old set keeps serving.
func (g *Registration) Replace(routes ...Route) error {
	rt := g.rt
	next := make(map[vocab.RouteID]*entry, len(routes))
	for _, r := range routes {
		if err := r.validate(); err != nil {
			return err
		}
		if _, dup := next[r.ID]; dup {
			return fmt.Errorf("%w: route %q is listed twice by %q", ErrDuplicateRoute, r.ID, g.owner)
		}
		next[r.ID] = &entry{route: r.clone(), reg: g}
	}
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if g.closed {
		return fmt.Errorf("llm: the registration of %q is closed", g.owner)
	}
	for id := range next {
		if cur, ok := rt.routes[id]; ok && cur.reg != g {
			return fmt.Errorf("%w: route %q is already served by %q", ErrDuplicateRoute, id, cur.reg.owner)
		}
	}
	// A route this registration keeps with the same cap keeps its slots, so
	// attempts in flight across the Replace still count against the cap.
	for id, e := range next {
		n := e.route.MaxConcurrent
		if n == 0 {
			continue
		}
		if cur, ok := rt.routes[id]; ok && cur.slots != nil && cap(cur.slots) == n {
			e.slots = cur.slots
		} else {
			e.slots = make(chan struct{}, n)
		}
	}
	for _, id := range g.ids {
		delete(rt.routes, id)
	}
	g.ids = g.ids[:0]
	for id, e := range next {
		rt.routes[id] = e
		g.ids = append(g.ids, id)
	}
	slices.Sort(g.ids)
	return nil
}

// Close removes the registration's routes; calls already prepared keep theirs.
func (g *Registration) Close() {
	rt := g.rt
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if g.closed {
		return
	}
	g.closed = true
	for _, id := range g.ids {
		if e, ok := rt.routes[id]; ok && e.reg == g {
			delete(rt.routes, id)
		}
	}
	g.ids = nil
}

// Routes is the ids currently served, sorted.
func (rt *Runtime) Routes() []vocab.RouteID {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	out := make([]vocab.RouteID, 0, len(rt.routes))
	for id := range rt.routes {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

func (rt *Runtime) lookup(id vocab.RouteID) (*entry, bool) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	e, ok := rt.routes[id]
	return e, ok
}

// Runtime is the model runtime. The zero value is not usable; call New.
type Runtime struct {
	mu     sync.RWMutex
	routes map[vocab.RouteID]*entry
	clock  func() int64
	after  func(time.Duration) <-chan time.Time // the retry waits of Complete
	jitter func() float64                       // draws a retry's jitter, in [0, 1)
	host   *cordis.Context                      // StreamAround is dispatched through it; nil: no interception
}
