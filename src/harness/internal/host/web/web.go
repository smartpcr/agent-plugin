// Package web is host.web, the route table of the UI (DH-D-API-1,
// docs/harness/areas/api.md §6.1).
//
// Every UI route is a declaration: a stable name, a method and a pattern, the
// effect the route has on state, the path parameters its handler may read, and the
// handler. Routes.Handle validates every declaration of a call, then, as one effect
// of the owner's fiber, adds them to a copy of the table and swaps the table
// atomically: a call registers all of its routes or none, and a refused call leaves
// the table as it was. The effect's cleanup removes the routes in one more swap,
// cancels the context of every handler still serving one of them and waits for
// those handlers to return. Dispatch is the standard library's pattern matching over
// the current table; a path no loaded component registered is 404.
package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Reasons Routes.Handle refuses a registration with.
const (
	// ReasonUndeclared is a route with no effect declaration.
	ReasonUndeclared herr.Reason = "web/undeclared-effect"
	// ReasonInvalid is a declaration that breaks one of the rules of Route.
	ReasonInvalid herr.Reason = "web/route-invalid"
	// ReasonTaken is a name, or a method and pattern, that is already registered.
	ReasonTaken herr.Reason = "web/route-taken"
)

// ErrUnloaded is the cause of a handler's context when its owner unloads.
var ErrUnloaded = errors.New("web: the route's owner unloaded")

// Effect is what a route does to state. A route without one is not registered.
type Effect string

// The closed set of effects.
const (
	// Read answers GET and never changes state.
	Read Effect = "read"
	// Change creates, replaces, removes or performs a named action: POST, PUT or
	// DELETE.
	Change Effect = "change"
	// Stream is a long-lived GET: the listener answers it 503 until boot is ready
	// and it must end when its context does.
	Stream Effect = "stream"
)

var effects = []Effect{Read, Change, Stream}

// methods is each effect's allowed methods.
var methods = map[Effect][]string{
	Read:   {http.MethodGet},
	Change: {http.MethodPost, http.MethodPut, http.MethodDelete},
	Stream: {http.MethodGet},
}

// Route is one UI route's declaration.
type Route struct {
	// Name is the stable name of the route: dot-separated lower-case words
	// ("session.prompt"). It is unique in the table.
	Name string
	// Method is GET, POST, PUT or DELETE, as Effect allows. A GET route answers
	// HEAD too.
	Method string
	// Pattern is a net/http path pattern without method or host
	// ("/sessions/{sid}/prompt"). It matches itself only: no trailing slash,
	// except "/", which is the root alone.
	Pattern string
	// Effect is the declared effect; required.
	Effect Effect
	// Params names every wildcard of Pattern, and nothing else: the parameters the
	// handler may read with PathValue.
	Params []string
	// Serve is the handler; required. Its request context ends with the client,
	// the listener, or the owner's effect.
	Serve http.Handler
}

// Info is the catalog view of one registered route.
type Info struct {
	Name    string
	Method  string
	Pattern string
	Effect  Effect
	Params  []string
}

var (
	namePattern  = regexp.MustCompile(`^[a-z][a-z0-9-]*(\.[a-z][a-z0-9-]*)*$`)
	wildcardExpr = regexp.MustCompile(`\{([^{}]*)\}`)
)

// check is the static validation of one declaration; it returns every issue.
func (r Route) check() (reason herr.Reason, issues []string) {
	reason = ReasonInvalid
	if r.Effect == "" {
		return ReasonUndeclared, []string{"the route declares no effect"}
	}
	if !slices.Contains(effects, r.Effect) {
		issues = append(issues, fmt.Sprintf("effect %q is not one of %v", r.Effect, effects))
	} else if !slices.Contains(methods[r.Effect], r.Method) {
		issues = append(issues, fmt.Sprintf("method %q is not one of %v, the methods of effect %q", r.Method, methods[r.Effect], r.Effect))
	}
	if !namePattern.MatchString(r.Name) {
		issues = append(issues, fmt.Sprintf("name %q is not dot-separated lower-case words", r.Name))
	}
	if r.Serve == nil {
		issues = append(issues, "the route has no handler")
	}
	if !strings.HasPrefix(r.Pattern, "/") || strings.ContainsAny(r.Pattern, " \t") {
		issues = append(issues, fmt.Sprintf("pattern %q is not a path starting with /", r.Pattern))
		return reason, issues
	}
	if r.Pattern != "/" && strings.HasSuffix(r.Pattern, "/") {
		issues = append(issues, fmt.Sprintf("pattern %q ends with /", r.Pattern))
	}
	var wild []string
	for _, m := range wildcardExpr.FindAllStringSubmatch(r.Pattern, -1) {
		if name := strings.TrimSuffix(m[1], "..."); name != "$" {
			wild = append(wild, name)
		}
	}
	for _, w := range wild {
		if !slices.Contains(r.Params, w) {
			issues = append(issues, fmt.Sprintf("path parameter %q is not declared in Params", w))
		}
	}
	seen := map[string]bool{}
	for _, p := range r.Params {
		if seen[p] {
			issues = append(issues, fmt.Sprintf("parameter %q is declared twice", p))
		}
		seen[p] = true
		if !slices.Contains(wild, p) {
			issues = append(issues, fmt.Sprintf("parameter %q is not in the pattern", p))
		}
	}
	return reason, issues
}

// key is the net/http pattern the route registers under.
func (r Route) key() string {
	if r.Pattern == "/" {
		return r.Method + " /{$}"
	}
	return r.Method + " " + r.Pattern
}

// Config is host.web's entry config. Zero fields take the defaults below.
type Config struct {
	// UnloadGrace bounds how long a route's cleanup waits for its cancelled
	// handlers to return; one still running then is logged and left behind.
	UnloadGrace time.Duration `json:"unloadGrace,omitempty"`
}

// DefaultUnloadGrace is the zero UnloadGrace.
const DefaultUnloadGrace = 5 * time.Second

// Routes is the route table. Its zero value is not usable; build one with New.
type Routes struct {
	grace time.Duration
	log   func(format string, args ...any)

	mu    sync.Mutex // serialises swaps
	table atomic.Pointer[table]
}

// New returns an empty table. log reports a handler that outlives its cleanup's
// grace; nil discards it.
func New(cfg Config, log func(format string, args ...any)) *Routes {
	if log == nil {
		log = func(string, ...any) {}
	}
	grace := cfg.UnloadGrace
	if grace <= 0 {
		grace = DefaultUnloadGrace
	}
	r := &Routes{grace: grace, log: log}
	empty, _ := build(nil)
	r.table.Store(empty)
	return r
}

// table is one immutable generation of the routes.
type table struct {
	mux     *http.ServeMux
	entries []*entry
	byKey   map[string]*entry
}

// entry is one registered route and the group of the Handle call that added it.
type entry struct {
	route Route
	key   string
	group *group
}

// build compiles entries into a new table. A pattern the standard library refuses,
// or one that conflicts with another, is an error and no table is built.
func build(entries []*entry) (*table, error) {
	t := &table{mux: http.NewServeMux(), entries: entries, byKey: make(map[string]*entry, len(entries))}
	for _, e := range entries {
		if err := register(t.mux, e); err != nil {
			return nil, err
		}
		t.byKey[e.key] = e
	}
	return t, nil
}

// register adds e to mux; ServeMux panics on a bad or conflicting pattern.
func register(mux *http.ServeMux, e *entry) (err error) {
	defer func() {
		if v := recover(); v != nil {
			err = herr.Newf(ReasonInvalid, "route %q: %v", e.route.Name, v)
		}
	}()
	mux.Handle(e.key, e)
	return nil
}

// Handle registers decls as one effect of owner's fiber and returns the effect.
// Every declaration is validated first: a call with any refused declaration, a name
// or a method and pattern already registered (by this call or an earlier one), or a
// pattern that conflicts with a registered one, returns an error and the table is
// unchanged. Disposing the effect, directly or by unloading owner, removes the
// routes, cancels their in-flight handlers and waits for them to return.
func (r *Routes) Handle(owner *cordis.Context, decls ...Route) (*cordis.EffectHandle, error) {
	if owner == nil {
		return nil, herr.New(ReasonInvalid, "routes are registered as the effect of an owner")
	}
	if len(decls) == 0 {
		return nil, herr.New(ReasonInvalid, "no route to register")
	}
	names, keys := map[string]bool{}, map[string]bool{}
	label := make([]string, 0, len(decls))
	for _, d := range decls {
		if reason, issues := d.check(); len(issues) > 0 {
			return nil, herr.Newf(reason, "route %q: %s", d.Name, strings.Join(issues, "; "))
		}
		if names[d.Name] {
			return nil, herr.Newf(ReasonTaken, "route name %q is declared twice", d.Name)
		}
		if keys[d.key()] {
			return nil, herr.Newf(ReasonTaken, "route %q: %s %s is declared twice", d.Name, d.Method, d.Pattern)
		}
		names[d.Name], keys[d.key()] = true, true
		label = append(label, d.Name)
	}
	return owner.Effect("web routes "+strings.Join(label, ","), func() (cordis.Disposer, error) {
		g := newGroup()
		if err := r.add(g, decls); err != nil {
			return nil, err
		}
		return func() error { r.remove(g); return nil }, nil
	})
}

// add swaps in a table holding the current routes and decls, or leaves it as it is.
func (r *Routes) add(g *group, decls []Route) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur := r.table.Load()
	next := slices.Clone(cur.entries)
	for _, d := range decls {
		e := &entry{route: d, key: d.key(), group: g}
		e.route.Params = slices.Clone(d.Params)
		for _, have := range cur.entries {
			if have.route.Name == d.Name {
				return herr.Newf(ReasonTaken, "route name %q is already registered", d.Name)
			}
			if have.key == e.key {
				return herr.Newf(ReasonTaken, "route %q: %s %s is already registered by %q", d.Name, d.Method, d.Pattern, have.route.Name)
			}
		}
		next = append(next, e)
	}
	t, err := build(next)
	if err != nil {
		return err
	}
	r.table.Store(t)
	return nil
}

// remove takes g's routes out of the table, then cancels and waits for their
// handlers.
func (r *Routes) remove(g *group) {
	r.mu.Lock()
	cur := r.table.Load()
	kept := slices.DeleteFunc(slices.Clone(cur.entries), func(e *entry) bool { return e.group == g })
	t, err := build(kept)
	if err == nil {
		r.table.Store(t)
	}
	r.mu.Unlock()
	if err != nil {
		// Unreachable: a subset of a conflict-free table always builds.
		r.log("web: rebuilding the route table: %v", err)
	}
	if !g.close(r.grace) {
		r.log("web: handlers still running when the %s unload grace ended", r.grace)
	}
}

// List is every registered route, sorted by name.
func (r *Routes) List() []Info {
	t := r.table.Load()
	out := make([]Info, 0, len(t.entries))
	for _, e := range t.entries {
		out = append(out, Info{Name: e.route.Name, Method: e.route.Method, Pattern: e.route.Pattern,
			Effect: e.route.Effect, Params: slices.Clone(e.route.Params)})
	}
	slices.SortFunc(out, func(a, b Info) int { return strings.Compare(a.Name, b.Name) })
	return out
}

// ServeHTTP dispatches req over the current table.
func (r *Routes) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.table.Load().mux.ServeHTTP(w, req)
}

// Streams reports whether req matches a Stream route of the current table: the
// listener mount's Stream function.
func (r *Routes) Streams(req *http.Request) bool {
	t := r.table.Load()
	_, pattern := t.mux.Handler(req)
	e, ok := t.byKey[pattern]
	return ok && e.route.Effect == Stream
}

// ServeHTTP runs the route's handler with a context that also ends when the route's
// owner unloads. A request dispatched over a table its route has just left is 404.
func (e *entry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	g := e.group
	if !g.enter() {
		http.NotFound(w, req)
		return
	}
	defer g.leave()
	ctx, cancel := context.WithCancelCause(req.Context())
	defer cancel(nil)
	stop := context.AfterFunc(g.ctx, func() { cancel(context.Cause(g.ctx)) })
	defer stop()
	e.route.Serve.ServeHTTP(w, req.WithContext(ctx))
}

// group is the lifetime of the routes of one Handle call and their handlers.
type group struct {
	ctx    context.Context
	cancel context.CancelCauseFunc

	mu      sync.Mutex
	closed  bool
	open    int
	drained chan struct{}
}

func newGroup() *group {
	ctx, cancel := context.WithCancelCause(context.Background())
	return &group{ctx: ctx, cancel: cancel}
}

func (g *group) enter() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return false
	}
	g.open++
	return true
}

func (g *group) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.open--
	if g.open == 0 && g.drained != nil {
		close(g.drained)
		g.drained = nil
	}
}

// close refuses new handlers, cancels the running ones and waits up to grace for
// them to return; it reports whether they did.
func (g *group) close(grace time.Duration) bool {
	g.mu.Lock()
	g.closed = true
	done := make(chan struct{})
	if g.open == 0 {
		close(done)
	} else {
		g.drained = done
	}
	g.mu.Unlock()
	g.cancel(ErrUnloaded)
	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case <-done:
		return true
	case <-timer.C:
		return false
	}
}
