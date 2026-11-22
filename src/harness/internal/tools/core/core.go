// Package core is the tools.registry component (DH-D-TOOLS-1, DH-D-TOOLS-2): the
// registry of tool declarations, the step view, and the guarded pipeline that is
// the one way to a tool body. The batch scheduler (DH-D-TOOLS-3) joins them here,
// so that no composition can have one without the others. Only packages under
// internal/tools import it (DH-T-TOOLS-18); everything else uses the contract,
// tools.Service under tools.Key.
package core

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// Name is the component's catalogue name.
const Name = "tools.registry"

// Component is tools.registry: it injects the scope table, declares the registry
// layered (a component mounted under a scope writes its own layer) and provides a
// tools.Service under tools.Key.
var Component = cordis.Define(Name, func(ctx *cordis.Context, _ cordis.None) error {
	tab, err := scope.TableKey.Get(ctx)
	if err != nil {
		return err
	}
	tab.Declare(tools.Key.Name(), scope.Layered)
	_, err = tools.Key.Provide(ctx, New(ctx, tab))
	return err
}, cordis.WithInject(scope.TableKey.Name()))

// layer is one scope's (or the global) registrations.
type layer struct {
	mu       sync.RWMutex
	tools    map[string]*tools.Declaration
	filters  map[uint64]tools.Filter
	policies map[uint64]named[tools.Policy]
	guards   map[uint64]named[tools.Guard]
	shapers  map[uint64]named[tools.Shaper]
	spill    *named[tools.SpillShaper] // the layer's one spill seat
	next     uint64
}

func newLayer() *layer {
	return &layer{tools: map[string]*tools.Declaration{}, filters: map[uint64]tools.Filter{},
		policies: map[uint64]named[tools.Policy]{}, guards: map[uint64]named[tools.Guard]{}, shapers: map[uint64]named[tools.Shaper]{}}
}

// registry is the tools.Service.
type registry struct {
	ctx    *cordis.Context // notifies tools.Changed
	layers *scope.Layers[*layer]
	cfg    Config

	mu       sync.Mutex
	seq      uint64
	live     map[*tools.Declaration]*liveDecl // every registration, for Catalogue
	detached map[*detachedBody]struct{}       // bodies that outlived their grace
}

type liveDecl struct {
	refs  int
	first uint64 // registration order of the first live registration
}

// New is a registry over the scope table tab; ctx notifies tools.Changed.
func New(ctx *cordis.Context, tab scope.Table) tools.Service {
	return NewWith(ctx, tab, Config{})
}

// NewWith is New with the pipeline configured by cfg.
func NewWith(ctx *cordis.Context, tab scope.Table, cfg Config) tools.Service {
	if cfg.MaxArgumentBytes <= 0 {
		cfg.MaxArgumentBytes = DefaultMaxArgumentBytes
	}
	if cfg.MaxResultBytes <= 0 {
		cfg.MaxResultBytes = DefaultMaxResultBytes
	}
	if cfg.DefaultTimeout <= 0 {
		cfg.DefaultTimeout = DefaultTimeout
	}
	if cfg.Grace <= 0 {
		cfg.Grace = DefaultGrace
	}
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
	cfg.Timeouts = maps.Clone(cfg.Timeouts)
	return &registry{ctx: ctx, layers: scope.NewLayers(tab, newLayer), cfg: cfg, live: map[*tools.Declaration]*liveDecl{},
		detached: map[*detachedBody]struct{}{}}
}

func (r *registry) Register(ctx *cordis.Context, d *tools.Declaration) (*cordis.EffectHandle, error) {
	if d == nil {
		return nil, &tools.Error{Code: tools.CodeInvalidDeclaration, Msg: "a declaration is required"}
	}
	return r.RegisterSet(ctx, []*tools.Declaration{d})
}

func (r *registry) RegisterSet(ctx *cordis.Context, ds []*tools.Declaration) (*cordis.EffectHandle, error) {
	if len(ds) == 0 {
		return nil, &tools.Error{Code: tools.CodeInvalidDeclaration, Msg: "the set is empty"}
	}
	// The disposal closure keeps the set; a caller mutating its slice afterwards
	// must not change what disposal removes.
	ds = slices.Clone(ds)
	names := make([]string, 0, len(ds))
	for _, d := range ds {
		if d == nil {
			return nil, &tools.Error{Code: tools.CodeInvalidDeclaration, Msg: "a declaration is required"}
		}
		if err := d.Err(); err != nil {
			return nil, err
		}
		if slices.Contains(names, d.Name()) {
			return nil, &tools.Error{Code: tools.CodeDuplicate, Tool: d.Name(), Msg: "named twice in one set"}
		}
		names = append(names, d.Name())
	}
	k := scope.Of(ctx)
	var refused error
	h, err := r.layers.Add(ctx, "tools "+strings.Join(names, ","), func(l *layer) func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		for _, d := range ds {
			if _, taken := l.tools[d.Name()]; taken {
				refused = &tools.Error{Code: tools.CodeDuplicate, Tool: d.Name(), Msg: "already registered in " + k.String()}
				return nil
			}
		}
		for _, d := range ds {
			l.tools[d.Name()] = d
		}
		r.track(ds, +1)
		return func() {
			l.mu.Lock()
			for _, d := range ds {
				if l.tools[d.Name()] == d {
					delete(l.tools, d.Name())
				}
			}
			l.mu.Unlock()
			r.track(ds, -1)
			r.changed(k)
		}
	})
	if err != nil {
		return nil, err
	}
	if refused != nil {
		h.Dispose()
		return nil, refused
	}
	r.changed(k)
	return h, nil
}

func (r *registry) Restrict(ctx *cordis.Context, f tools.Filter) (*cordis.EffectHandle, error) {
	k := scope.Of(ctx)
	if k.IsZero() {
		return nil, &tools.Error{Code: tools.CodeUnscoped, Msg: "Restrict needs a scoped context"}
	}
	f = tools.Filter{Allow: cloneNil(f.Allow), Deny: slices.Clone(f.Deny)}
	h, err := r.layers.Add(ctx, "tools restriction", func(l *layer) func() {
		l.mu.Lock()
		l.next++
		id := l.next
		l.filters[id] = f
		l.mu.Unlock()
		return func() {
			l.mu.Lock()
			delete(l.filters, id)
			l.mu.Unlock()
			r.changed(k)
		}
	})
	if err != nil {
		return nil, err
	}
	r.changed(k)
	return h, nil
}

// cloneNil clones s and keeps nil as nil, so an Allow of nil still admits all.
func cloneNil(s []string) []string {
	if s == nil {
		return nil
	}
	return append([]string{}, s...)
}

func (r *registry) track(ds []*tools.Declaration, delta int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, d := range ds {
		ld := r.live[d]
		if ld == nil {
			r.seq++
			ld = &liveDecl{first: r.seq}
			r.live[d] = ld
		}
		ld.refs += delta
		if ld.refs <= 0 {
			delete(r.live, d)
		}
	}
}

func (r *registry) changed(k scope.Key) {
	if r.ctx != nil {
		tools.Changed.Notify(r.ctx, k, tools.ChangedArgs{Scope: k})
	}
}

// snapshot is one layer's tools and filters at one moment.
type snapshot struct {
	tools   []*tools.Declaration
	filters []tools.Filter
}

func (l *layer) snapshot() snapshot {
	l.mu.RLock()
	defer l.mu.RUnlock()
	s := snapshot{tools: make([]*tools.Declaration, 0, len(l.tools))}
	for _, d := range l.tools {
		s.tools = append(s.tools, d)
	}
	slices.SortFunc(s.tools, func(a, b *tools.Declaration) int { return strings.Compare(a.Name(), b.Name()) })
	ids := make([]uint64, 0, len(l.filters))
	for id := range l.filters {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		s.filters = append(s.filters, l.filters[id])
	}
	return s
}

// Resolve computes the view at t.Scope from the layer chain, nearest first: the
// scope's own tools, then each enclosing layer's and the global layer's under the
// restrictions of every nearer layer. The nearest binding of a name decides it.
func (r *registry) Resolve(ctx context.Context, t tools.Target) (tools.StepView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// The view keeps its own target; each When gets a further copy, so neither
	// the caller nor a condition can change what the view publishes.
	t = t.Clone()
	chain := r.layers.Chain(t.Scope)
	snaps := make([]snapshot, len(chain))
	for i, l := range chain {
		snaps[i] = l.snapshot()
	}
	seen := map[string]bool{}
	var visible []*tools.Declaration
	for i, s := range snaps {
		for _, d := range s.tools {
			if seen[d.Name()] {
				continue
			}
			seen[d.Name()] = true
			if admitted(snaps[:i], d.Name()) {
				visible = append(visible, d)
			}
		}
	}
	slices.SortFunc(visible, func(a, b *tools.Declaration) int { return strings.Compare(a.Name(), b.Name()) })
	active := make([]*tools.Declaration, 0, len(visible))
	for _, d := range visible {
		ok, err := d.Active(t.Clone())
		if err != nil {
			return nil, &tools.Error{Code: tools.CodeActivationFailed, Tool: d.Name(), Msg: err.Error()}
		}
		if ok {
			active = append(active, d)
		}
	}
	return newView(t, active), nil
}

// admitted reports whether every restriction of the nearer layers lets name in.
func admitted(nearer []snapshot, name string) bool {
	for _, s := range nearer {
		for _, f := range s.filters {
			if !f.Admits(name) {
				return false
			}
		}
	}
	return true
}

func (r *registry) Catalogue() []tools.Entry {
	r.mu.Lock()
	type cand struct {
		d     *tools.Declaration
		first uint64
	}
	byName := map[string]cand{}
	for d, ld := range r.live {
		if c, ok := byName[d.Name()]; !ok || ld.first < c.first {
			byName[d.Name()] = cand{d, ld.first}
		}
	}
	r.mu.Unlock()
	out := make([]tools.Entry, 0, len(byName))
	for _, c := range byName {
		d := c.d
		out = append(out, tools.Entry{
			Name: d.Name(), Description: d.Description(), Origin: d.Origin(),
			Reach: d.Reach(), Disclosure: d.Disclosure(), Effects: d.Effects(),
			Fingerprint: d.Input().Fingerprint(), Parameters: d.Schema().Parameters,
		})
	}
	slices.SortFunc(out, func(a, b tools.Entry) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// view is the immutable StepView.
type view struct {
	target tools.Target
	list   []*tools.Declaration
	index  map[string]*tools.Declaration

	mu       sync.RWMutex
	released bool
}

func newView(t tools.Target, list []*tools.Declaration) *view {
	v := &view{target: t.Clone(), list: list, index: make(map[string]*tools.Declaration, len(list))}
	for _, d := range list {
		v.index[d.Name()] = d
	}
	return v
}

func (v *view) Target() tools.Target { return v.target.Clone() }

func (v *view) Tools() []*tools.Declaration { return slices.Clone(v.list) }

func (v *view) Lookup(name string) (*tools.Declaration, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	if v.released {
		return nil, false
	}
	d, ok := v.index[name]
	return d, ok
}

func (v *view) Release() {
	v.mu.Lock()
	v.released = true
	v.mu.Unlock()
}

func (v *view) String() string {
	names := make([]string, len(v.list))
	for i, d := range v.list {
		names[i] = d.Name()
	}
	return fmt.Sprintf("step view %v at %s", names, v.target.Scope)
}
