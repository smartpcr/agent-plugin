package agentpreset

import (
	"cmp"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// Key is the registry's service key (bundle-preset.md §6.1, agentpreset.Key).
var Key = cordis.NewServiceKey[*Registry](RegistryName)

// Info describes a declaration's current revision. Revision counts the revisions
// built for the id, from 1; Broken is set when the revision cannot be used.
type Info struct {
	ID, Name, Description string
	Order                 int
	Revision              int
	Digest                string
	Broken                string
}

// RevisionStatus is one live revision in the registry's list: current or retired,
// with its lease count.
type RevisionStatus struct {
	ID       string
	Revision int
	Digest   string
	Leases   int
	Retired  bool
	Broken   string
}

// Lease is a counted reference to one revision. Release is idempotent; the lease is
// also released when its owner's fiber unloads.
type Lease interface {
	// Key is the revision's scope key: an agent scope that lists it as enclosing
	// sees what the revision's children registered.
	Key() scope.Key
	Info() Info
	Release()
}

// Options configure a Registry.
type Options struct {
	// Catalogue resolves the children's components and says what they provide;
	// required.
	Catalogue Catalogue
	// Scopes is the scope table each revision's scope is taken from; required.
	Scopes scope.Table
	// Evaluator computes the expressions in children's configs; nil rejects them,
	// as loader.Options does.
	Evaluator loader.Evaluator
	// Version is the harness version, part of every digest.
	Version string
	// Config is the registry config until a Load gives another.
	Config Config
}

// Registry keeps the declarations and the revision list (DH-D-BUNDLE-4).
type Registry struct {
	ctx  *cordis.Context
	opts Options

	// loadMu serialises every change of the declaration set and its rebuild.
	loadMu   sync.Mutex
	loaded   []Declaration // the set Load gave
	declared []*declared   // the agent.preset entries, in registration order

	// bindMu guards targets; a target's lock serialises the bindings of one agent.
	bindMu  sync.Mutex
	targets map[scope.Key]*targetLock

	mu       sync.Mutex
	closed   bool
	cfg      Config
	current  map[string]*revision // by declaration id
	order    []string             // declaration ids in roster order
	revs     []*revision          // every live revision, current and retired, by creation
	numbers  map[string]int       // the last revision number built per id
	bindings map[scope.Key]*lease // the lease each bound agent scope holds
}

// declared is one agent.preset entry's registration.
type declared struct{ decl Declaration }

type targetLock struct {
	mu    sync.Mutex
	users int
}

// revision is one mounted instance of a declaration, or a declaration that cannot
// mount (static: no scope, Broken names its issues).
type revision struct {
	info     Info
	children map[string]Child
	sc       *scope.Scope
	ctx      *cordis.Context
	nested   *loader.Service
	provides map[string]bool
	static   bool

	// guarded by Registry.mu
	leases  int
	retired bool
	closed  bool
}

// New is a registry owned by ctx: every revision's scope is closed when ctx's fiber
// unloads. It holds no declaration until Load or Declare.
func New(ctx *cordis.Context, o Options) (*Registry, error) {
	if ctx == nil || o.Catalogue == nil || o.Scopes == nil {
		return nil, errors.New("agentpreset: a registry needs its context, a catalogue and a scope table")
	}
	r := &Registry{ctx: ctx, opts: o, cfg: o.Config, current: map[string]*revision{}, numbers: map[string]int{},
		bindings: map[scope.Key]*lease{}, targets: map[scope.Key]*targetLock{}}
	if _, err := ctx.Effect("agentpreset/registry", func() (cordis.Disposer, error) {
		return func() error { r.unload(); return nil }, nil
	}); err != nil {
		return nil, err
	}
	return r, nil
}

// Load replaces the set of declarations given by value (those of agent.preset
// entries, Declare, stay) and the registry config. A set with a static issue
// (Validate) is refused whole with CodeInvalid and nothing changes. Otherwise each
// declaration whose resolved children changed (an edit of it or of a base it
// extends) gets a new revision, mounted eagerly by a loader of its own, and the
// revision it replaces, like that of a removed declaration, is retired: it closes at
// its last lease's release, at once when it has none. A revision whose child fails
// or never becomes active is listed broken; that is not an error of Load.
func (r *Registry) Load(cfg Config, decls []Declaration) error {
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	all := append(slices.Clone(decls), r.declaredLocked()...)
	res, issues := resolveAll(cfg, all, r.opts.Catalogue, r.opts.Version)
	if len(issues) > 0 {
		return invalid(issues)
	}
	if err := r.install(cfg, res, nil); err != nil {
		return err
	}
	r.loaded = slices.Clone(decls)
	return nil
}

// Declare adds the declaration of one agent.preset entry, owned by owner: when
// owner's fiber unloads the declaration is removed and its revision retired. An
// empty or duplicate id is refused, so that entry fails. The set is rebuilt
// leniently, as entries register one at a time while the tree loads: a declaration
// with a static issue, or whose Extends does not resolve yet, is listed broken
// until the set changes, and a default not declared yet is not an error.
func (r *Registry) Declare(owner *cordis.Context, d Declaration) error {
	if owner == nil {
		return errors.New("agentpreset: a declaration needs its owner context")
	}
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	if d.ID == "" || strings.Contains(d.ID, ":") {
		return invalid([]Issue{{Preset: d.ID, Path: "id", Message: "a declaration needs an id without \":\""}})
	}
	for _, x := range append(slices.Clone(r.loaded), r.declaredLocked()...) {
		if x.ID == d.ID {
			return invalid([]Issue{{Preset: d.ID, Path: "id", Message: fmt.Sprintf("duplicate id %q", d.ID)}})
		}
	}
	reg := &declared{decl: d}
	r.declared = append(r.declared, reg)
	if err := r.syncLocked(); err != nil {
		r.declared = slices.DeleteFunc(r.declared, func(x *declared) bool { return x == reg })
		return err
	}
	_, err := owner.Effect("agentpreset/declaration:"+d.ID, func() (cordis.Disposer, error) {
		return func() error {
			r.loadMu.Lock()
			defer r.loadMu.Unlock()
			r.declared = slices.DeleteFunc(r.declared, func(x *declared) bool { return x == reg })
			_ = r.syncLocked()
			return nil
		}, nil
	})
	if err != nil {
		r.declared = slices.DeleteFunc(r.declared, func(x *declared) bool { return x == reg })
		_ = r.syncLocked()
	}
	return err
}

func (r *Registry) declaredLocked() []Declaration {
	out := make([]Declaration, len(r.declared))
	for i, x := range r.declared {
		out[i] = x.decl
	}
	return out
}

// syncLocked rebuilds the whole set leniently (Declare); loadMu is held.
func (r *Registry) syncLocked() error {
	r.mu.Lock()
	cfg := r.cfg
	r.mu.Unlock()
	all := append(slices.Clone(r.loaded), r.declaredLocked()...)
	res, issues := resolveAll(cfg, all, r.opts.Catalogue, r.opts.Version)
	why := map[string][]string{}
	for _, is := range issues {
		if is.Preset != "" {
			why[is.Preset] = append(why[is.Preset], is.String())
		}
	}
	var ok []resolved
	got := map[string]bool{}
	for _, d := range res {
		if len(why[d.decl.ID]) == 0 {
			ok = append(ok, d)
			got[d.decl.ID] = true
		}
	}
	var static []Info
	for _, d := range all {
		if got[d.ID] {
			continue
		}
		msg := strings.Join(why[d.ID], "; ")
		if msg == "" {
			msg = fmt.Sprintf("preset %s: extends %q, which does not resolve", d.ID, d.Extends)
		}
		static = append(static, Info{ID: d.ID, Name: d.Name, Description: d.Description, Order: d.Order, Broken: msg})
	}
	return r.install(cfg, ok, static)
}

// install swaps in the resolved set: an unchanged declaration keeps its revision, a
// changed one is built anew outside the locks, and every replaced or removed
// revision is retired. static are declarations listed broken without a scope.
func (r *Registry) install(cfg Config, res []resolved, static []Info) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return &Error{Code: CodeClosed, Msg: "the registry has unloaded"}
	}
	was := maps.Clone(r.current)
	numbers := maps.Clone(r.numbers)
	r.mu.Unlock()

	next := map[string]*revision{}
	var built []*revision
	for _, d := range res {
		if old, ok := was[d.decl.ID]; ok && !old.static && old.info.Digest == d.digest {
			next[d.decl.ID] = old
			continue
		}
		numbers[d.decl.ID]++
		rev := r.build(d, numbers[d.decl.ID])
		next[d.decl.ID] = rev
		built = append(built, rev)
	}
	for _, in := range static {
		if old, ok := was[in.ID]; ok && old.static && old.info.Broken == in.Broken {
			next[in.ID] = old
			continue
		}
		numbers[in.ID]++
		in.Revision = numbers[in.ID]
		rev := &revision{info: in, static: true, provides: map[string]bool{}}
		next[in.ID] = rev
		built = append(built, rev)
	}

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		for _, rev := range built {
			rev.close()
		}
		return &Error{Code: CodeClosed, Msg: "the registry has unloaded"}
	}
	var drop []*revision
	for id, old := range was {
		if next[id] != old {
			old.retired = true
			if old.leases == 0 && !old.closed {
				old.closed = true
				drop = append(drop, old)
			}
		}
	}
	// A kept revision takes the declaration's new name, description and order.
	for _, d := range res {
		rev := next[d.decl.ID]
		rev.info.Name, rev.info.Description, rev.info.Order = d.decl.Name, d.decl.Description, d.decl.Order
	}
	r.current, r.numbers, r.cfg = next, numbers, cfg
	r.revs = append(r.revs, built...)
	r.revs = slices.DeleteFunc(r.revs, func(rev *revision) bool { return rev.closed })
	r.order = slices.Collect(maps.Keys(next))
	slices.SortFunc(r.order, func(a, b string) int {
		return cmp.Or(cmp.Compare(next[a].info.Order, next[b].info.Order), cmp.Compare(a, b))
	})
	r.mu.Unlock()

	for _, rev := range drop {
		rev.close()
	}
	return nil
}

// build mounts declaration d as revision n: a scope owned by the registry, a context
// private for every service the enabled children's catalogue items provide, and a
// nested loader over one insert of the children. A child that fails to load, or
// whose injections reach a Shared registry (scope.Table.CheckMount), closes the scope
// and leaves the revision broken.
func (r *Registry) build(d resolved, n int) *revision {
	rev := &revision{
		info: Info{ID: d.decl.ID, Name: d.decl.Name, Description: d.decl.Description, Order: d.decl.Order,
			Revision: n, Digest: d.digest},
		children: d.children,
		provides: map[string]bool{},
	}
	keys := slices.Sorted(maps.Keys(d.children))
	var entries []loader.EntryOptions
	var injects []string
	for _, k := range keys {
		c := d.children[k]
		entries = append(entries, loader.EntryOptions{ID: k, Name: c.Component, Config: c.Config, Disabled: c.Disabled})
		if c.Disabled {
			continue
		}
		if item, err := r.opts.Catalogue.Lookup(c.Component); err == nil {
			for _, p := range item.Provides {
				rev.provides[p] = true
			}
		}
		if comp, ok := r.opts.Catalogue.Resolve(c.Component); ok {
			for _, dep := range comp.Inject() {
				injects = append(injects, dep.Name)
			}
		}
	}
	if diags := r.opts.Scopes.CheckMount(injects); len(diags) > 0 {
		msgs := make([]string, len(diags))
		for i, dg := range diags {
			msgs[i] = dg.Message
		}
		rev.info.Broken = strings.Join(msgs, "; ")
		return rev
	}

	sc, err := r.opts.Scopes.New(r.ctx, "preset:"+d.decl.ID+"#"+strconv.Itoa(n))
	if err != nil {
		rev.info.Broken = err.Error()
		return rev
	}
	ctx := sc.Ctx()
	for _, p := range slices.Sorted(maps.Keys(rev.provides)) {
		ctx = ctx.Isolate(p)
	}
	rev.sc, rev.ctx = sc, ctx
	nested, err := loader.New(ctx, loader.Options{Resolver: r.opts.Catalogue, Evaluator: r.opts.Evaluator},
		loader.Layer{Label: "preset:" + d.decl.ID, Ops: []loader.Op{{Kind: loader.OpInsert, Entries: entries}}})
	if err != nil {
		rev.info.Broken = err.Error()
		rev.close()
		return rev
	}
	rev.nested = nested
	nested.WaitAll()
	var failed []string
	for _, k := range keys {
		if d.children[k].Disabled {
			continue
		}
		entry := nested.Find(k)
		if entry == nil {
			failed = append(failed, k+": not mounted")
			continue
		}
		if f := nested.Fiber(entry); f == nil {
			failed = append(failed, k+": not mounted")
		} else if err := f.Err(); err != nil {
			failed = append(failed, k+": "+err.Error())
		} else if st := f.State(); st != cordis.Active {
			// A fiber waiting for an injected service settles Pending with no error.
			failed = append(failed, k+": "+waiting(r.opts.Catalogue, d.children[k].Component, st))
		}
	}
	if len(failed) > 0 {
		rev.info.Broken = strings.Join(failed, "; ")
		rev.close()
	}
	return rev
}

// waiting says why a settled child is not active: the services it injects, which
// never came, for a pending one.
func waiting(cat Catalogue, component string, st cordis.FiberState) string {
	if comp, ok := cat.Resolve(component); ok && st == cordis.Pending {
		var names []string
		for _, dep := range comp.Inject() {
			names = append(names, dep.Name)
		}
		if len(names) > 0 {
			return "not active after the tree settled: waits for " + strings.Join(names, ", ")
		}
	}
	return fmt.Sprintf("not active after the tree settled (%v)", st)
}

// close unloads the revision's scope, and with it every child and the nested
// loader. It runs outside Registry.mu.
func (rev *revision) close() {
	if rev.sc != nil {
		_ = rev.sc.Close()
	}
}

// unload is the registry's own unload: every revision closes and nothing binds.
func (r *Registry) unload() {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.closed = true
	revs := r.revs
	for _, rev := range revs {
		rev.closed = true
	}
	r.revs, r.current, r.order, r.bindings = nil, map[string]*revision{}, nil, map[scope.Key]*lease{}
	r.mu.Unlock()
	for _, rev := range revs {
		rev.close()
	}
}

// List is every declaration's current revision in roster order (Order, then id).
func (r *Registry) List() []Info {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Info, 0, len(r.order))
	for _, id := range r.order {
		out = append(out, r.current[id].info)
	}
	return out
}

// Revisions is the revision list: every revision not yet closed, current and
// retired, in creation order, with its lease count.
func (r *Registry) Revisions() []RevisionStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []RevisionStatus
	for _, rev := range r.revs {
		if rev.closed {
			continue
		}
		out = append(out, RevisionStatus{ID: rev.info.ID, Revision: rev.info.Revision, Digest: rev.info.Digest,
			Leases: rev.leases, Retired: rev.retired, Broken: rev.info.Broken})
	}
	return out
}

// Default is the saved choice while it names a declaration, else Config.Default.
func (r *Registry) Default() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.defaultLocked()
}

func (r *Registry) defaultLocked() string {
	if s := r.cfg.SelectedDefault; s != "" && r.current[s] != nil {
		return s
	}
	return r.cfg.Default
}

// Resolve is the current revision of id ("" is the default) without acquiring it.
func (r *Registry) Resolve(id string) (Info, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rev, err := r.lookupLocked(id)
	if err != nil {
		return Info{}, err
	}
	return rev.info, nil
}

func (r *Registry) lookupLocked(id string) (*revision, error) {
	if r.closed {
		return nil, &Error{Code: CodeClosed, Msg: "the registry has unloaded"}
	}
	if id == "" {
		id = r.defaultLocked()
	}
	rev := r.current[id]
	if rev == nil {
		return nil, notFound(id, slices.Clone(r.order))
	}
	return rev, nil
}

// Acquire leases the current revision of id ("" is the default) for owner. A broken
// revision is refused with CodeInvalid and its diagnostic, an unknown id with
// CodeNotFound and the available ids; neither leaves a lease.
func (r *Registry) Acquire(owner *cordis.Context, id string) (Lease, error) {
	r.mu.Lock()
	rev, err := r.lookupLocked(id)
	if err == nil && rev.info.Broken != "" {
		err = &Error{Code: CodeInvalid, Msg: fmt.Sprintf("preset %q revision %d is broken: %s", rev.info.ID, rev.info.Revision, rev.info.Broken)}
	}
	if err != nil {
		r.mu.Unlock()
		return nil, err
	}
	rev.leases++
	r.mu.Unlock()
	return r.hold(owner, rev)
}

// AcquireFrom leases the exact revision the agent scope holder is bound to, retired
// or not (DH-ADR-BUNDLE-7): a child agent that names no preset joins its creator's.
func (r *Registry) AcquireFrom(owner *cordis.Context, holder scope.Key) (Lease, error) {
	r.mu.Lock()
	l := r.bindings[holder]
	if l == nil || r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("agentpreset: %s is bound to no preset", holder)
	}
	l.rev.leases++
	rev := l.rev
	r.mu.Unlock()
	return r.hold(owner, rev)
}

// hold wraps one counted lease on rev and ties it to owner's fiber; a failure gives
// the count back.
func (r *Registry) hold(owner *cordis.Context, rev *revision) (Lease, error) {
	l := &lease{r: r, rev: rev}
	if owner == nil {
		r.release(l)
		return nil, errors.New("agentpreset: a lease needs an owner context")
	}
	h, err := owner.Effect("agentpreset/lease:"+rev.info.ID+"#"+strconv.Itoa(rev.info.Revision), func() (cordis.Disposer, error) {
		return func() error {
			l.mu.Lock()
			l.fromOwner = true
			l.mu.Unlock()
			l.Release()
			return nil
		}, nil
	})
	if err != nil {
		r.release(l)
		return nil, err
	}
	l.mu.Lock()
	l.stop = h
	l.mu.Unlock()
	return l, nil
}

// release gives back l's count; the last release of a retired revision closes it.
func (r *Registry) release(l *lease) {
	r.mu.Lock()
	rev := l.rev
	rev.leases--
	for k, b := range r.bindings {
		if b == l {
			delete(r.bindings, k)
		}
	}
	closeIt := rev.retired && rev.leases == 0 && !rev.closed
	if closeIt {
		rev.closed = true
		r.revs = slices.DeleteFunc(r.revs, func(x *revision) bool { return x == rev })
	}
	r.mu.Unlock()
	if closeIt {
		rev.close()
	}
}

// Of is the revision the agent scope is bound to.
func (r *Registry) Of(agent scope.Key) (Info, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l := r.bindings[agent]; l != nil {
		return l.rev.info, true
	}
	return Info{}, false
}

// Lookup is a service a child of the agent's revision provides privately, as the
// agent sees it; false for a service the revision does not provide.
func (r *Registry) Lookup(agent scope.Key, service string) (any, bool) {
	r.mu.Lock()
	l := r.bindings[agent]
	var rev *revision
	if l != nil {
		rev = l.rev
	}
	r.mu.Unlock()
	if rev == nil || rev.ctx == nil || !rev.provides[service] {
		return nil, false
	}
	return rev.ctx.Lookup(service)
}

type lease struct {
	r   *Registry
	rev *revision

	once      sync.Once
	mu        sync.Mutex
	stop      *cordis.EffectHandle
	fromOwner bool
}

func (l *lease) Key() scope.Key {
	if l.rev.sc == nil {
		return scope.Key{}
	}
	return l.rev.sc.Key()
}

// Info is the revision's description, read under the registry's lock: a reload
// that keeps the revision refreshes its name, description and order.
func (l *lease) Info() Info {
	l.r.mu.Lock()
	defer l.r.mu.Unlock()
	return l.rev.info
}

// lockTarget serialises the bindings of one agent scope; the returned func unlocks.
func (r *Registry) lockTarget(k scope.Key) func() {
	r.bindMu.Lock()
	tl := r.targets[k]
	if tl == nil {
		tl = &targetLock{}
		r.targets[k] = tl
	}
	tl.users++
	r.bindMu.Unlock()
	tl.mu.Lock()
	return func() {
		tl.mu.Unlock()
		r.bindMu.Lock()
		if tl.users--; tl.users == 0 {
			delete(r.targets, k)
		}
		r.bindMu.Unlock()
	}
}

func (l *lease) Release() {
	l.once.Do(func() { l.r.release(l) })
	l.mu.Lock()
	h, fromOwner := l.stop, l.fromOwner
	l.stop = nil
	l.mu.Unlock()
	if h != nil && !fromOwner {
		h.Dispose()
	}
}
