// Package scope is per-agent and per-preset registration with one owner and no
// borrowed authority (DH-D-SESSION-8, DH-ADR-SESSION-10, core-session.md §6.1).
//
// A scope is a fiber for lifetime and a key for visibility. Table.New mounts an
// empty child fiber under the scope component's own context, which injects
// nothing, tags it with an opaque Key through a context value, and registers an
// effect on the owner that closes it. Resolution of services walks up parent
// fibers, so the scope context reaches no service: a component mounted under it
// resolves exactly what it declares. Only this package can tag a context, so the
// contexts that carry a key are exactly the scope fiber's and its descendants', and
// they all unload when the scope closes.
//
// Visibility is by key: an entry registered through a scope context is visible in
// that scope and in scopes that list it as enclosing. The enclosing list is
// ordered, linearised nearest first, checked for cycles and changeable only by a
// holder of *Scope.
package scope

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Code is a "<domain>/<reason>" error code.
type Code string

// The codes this package raises.
const (
	CodeClosed         Code = "scope/closed"
	CodeCycle          Code = "scope/cycle"
	CodeSharedRegistry Code = "scope/shared-registry"
)

// Error carries a Code; errors.Is matches a sentinel of the same code.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string {
	if e.Msg == "" {
		return string(e.Code)
	}
	return string(e.Code) + ": " + e.Msg
}

// Is matches a sentinel of the same code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Msg == "" && t.Code == e.Code
}

// Sentinels for errors.Is.
var (
	ErrClosed         = &Error{Code: CodeClosed}
	ErrCycle          = &Error{Code: CodeCycle}
	ErrSharedRegistry = &Error{Code: CodeSharedRegistry}
)

// Key identifies a scope. It is opaque and comparable; the zero Key is global. A
// Key grants nothing: it cannot change an enclosing list or close a scope.
type Key struct {
	t  *table
	id uint64
}

// IsZero reports whether k is the global key.
func (k Key) IsZero() bool { return k.t == nil }

func (k Key) String() string {
	if k.IsZero() {
		return "scope(global)"
	}
	return fmt.Sprintf("scope(%d)", k.id)
}

// Visibility is a registry service's declaration: whether a component mounted
// under a scope may write to it.
type Visibility int

const (
	// Layered registries keep one layer per scope; a scoped write lands in the
	// scope's own layer.
	Layered Visibility = iota + 1
	// Shared registries are process-wide; a scoped component writing to one
	// would leak past its scope, so CheckMount reports it.
	Shared
)

// Diagnostic is one CheckMount finding.
type Diagnostic struct {
	Code    Code
	Service string
	Message string
}

// Table is the scope table. Its methods are the whole of what a holder of a Table
// can do; New returns the *Scope that alone can change or close a scope.
type Table interface {
	New(owner *cordis.Context, label string, enclosing ...Key) (*Scope, error)
	// Enclosing is k's enclosing scopes, linearised nearest first, without k,
	// without closed scopes; a fresh slice.
	Enclosing(k Key) []Key
	Declare(service string, v Visibility)
	// CheckMount names the Shared registries among injects.
	CheckMount(injects []string) []Diagnostic
	table() *table
}

// TableKey is the scope table's service key.
var TableKey = cordis.NewServiceKey[Table]("session.scope")

// Component is session.scope: it provides a Table. Its scopes hang under an empty
// holder fiber, a sibling of the one providing the table, so no fiber above a scope
// provides or injects a service. The component injects nothing, and must be a
// top-level entry so that no ancestor fiber does either.
var Component = cordis.Define("session.scope", func(ctx *cordis.Context, _ cordis.None) error {
	var base *cordis.Context
	holder, err := ctx.Apply("session.scope.holder", func(c *cordis.Context) error {
		base = c
		return nil
	})
	if err == nil {
		err = holder.Wait()
	}
	if err != nil {
		return err
	}
	t := NewTable(base)
	provider, err := ctx.Apply("session.scope.table", func(c *cordis.Context) error {
		_, err := TableKey.Provide(c, t)
		return err
	})
	if err != nil {
		return err
	}
	return provider.Wait()
})

type ctxKey struct{}

type node struct {
	enclosing []Key
	closed    bool
	onClose   []func()
}

type table struct {
	base *cordis.Context

	mu     sync.RWMutex
	next   uint64
	nodes  map[uint64]*node
	vis    map[string]Visibility
	closed bool
}

// NewTable is a table whose scopes hang under base. base should be a context
// whose fiber and ancestors inject nothing.
func NewTable(base *cordis.Context) Table {
	return &table{base: base, nodes: map[uint64]*node{}, vis: map[string]Visibility{}}
}

func (t *table) table() *table { return t }

// Scope is one scope; only its holder can change its enclosing list or close it.
type Scope struct {
	key   Key
	label string
	ctx   *cordis.Context
	fiber *cordis.Fiber

	once      sync.Once
	ownerStop *cordis.EffectHandle
	fromOwner bool
	mu        sync.Mutex
}

func (t *table) New(owner *cordis.Context, label string, enclosing ...Key) (*Scope, error) {
	if owner == nil {
		return nil, errors.New("scope: an owner context is required")
	}
	t.mu.Lock()
	for _, e := range enclosing {
		if err := t.checkLive(e); err != nil {
			t.mu.Unlock()
			return nil, err
		}
	}
	t.next++
	k := Key{t: t, id: t.next}
	t.nodes[k.id] = &node{enclosing: slices.Clone(enclosing)}
	t.mu.Unlock()

	s := &Scope{key: k, label: label}
	fiber, err := t.base.WithValue(ctxKey{}, k).Apply("scope:"+label, func(c *cordis.Context) error {
		s.ctx = c
		return nil
	})
	if err == nil {
		err = fiber.Wait()
	}
	if err != nil {
		if fiber != nil {
			fiber.Dispose()
		}
		t.finish(k)
		return nil, err
	}
	s.fiber = fiber
	h, err := owner.Effect("scope:"+label, func() (cordis.Disposer, error) {
		return func() error {
			s.mu.Lock()
			s.fromOwner = true
			s.mu.Unlock()
			return s.Close()
		}, nil
	})
	if err != nil {
		fiber.Dispose()
		t.finish(k)
		return nil, err
	}
	s.mu.Lock()
	s.ownerStop = h
	s.mu.Unlock()
	return s, nil
}

// checkLive requires e to be a live key of t (or global); t.mu held.
func (t *table) checkLive(e Key) error {
	if e.IsZero() {
		return nil
	}
	if e.t != t {
		return fmt.Errorf("scope: %s belongs to another table", e)
	}
	n, ok := t.nodes[e.id]
	if !ok || n.closed {
		return &Error{Code: CodeClosed, Msg: e.String() + " is closed"}
	}
	return nil
}

// finish marks k closed, forgets it and runs its close hooks.
func (t *table) finish(k Key) {
	t.mu.Lock()
	n, ok := t.nodes[k.id]
	if !ok {
		t.mu.Unlock()
		return
	}
	n.closed = true
	hooks := n.onClose
	delete(t.nodes, k.id)
	t.mu.Unlock()
	for _, h := range hooks {
		h()
	}
}

// onClose runs fn when k closes, at once if it already has; false for global.
func (t *table) onClose(k Key, fn func()) bool {
	if k.IsZero() {
		return false
	}
	t.mu.Lock()
	n, ok := t.nodes[k.id]
	if ok && !n.closed {
		n.onClose = append(n.onClose, fn)
		t.mu.Unlock()
		return true
	}
	t.mu.Unlock()
	fn()
	return true
}

func (t *table) live(k Key) bool {
	if k.IsZero() {
		return true
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	n, ok := t.nodes[k.id]
	return ok && !n.closed
}

func (t *table) Enclosing(k Key) []Key {
	if k.IsZero() || k.t != t {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []Key
	seen := map[uint64]bool{k.id: true}
	var walk func(id uint64)
	walk = func(id uint64) {
		n, ok := t.nodes[id]
		if !ok {
			return
		}
		for _, e := range n.enclosing {
			if e.IsZero() || seen[e.id] {
				continue
			}
			seen[e.id] = true
			if en, ok := t.nodes[e.id]; ok && !en.closed {
				out = append(out, e)
				walk(e.id)
			}
		}
	}
	walk(k.id)
	return out
}

// reaches reports whether from reaches to through enclosing lists; t.mu held.
func (t *table) reaches(from, to uint64) bool {
	seen := map[uint64]bool{}
	stack := []uint64{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == to {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if n, ok := t.nodes[id]; ok {
			for _, e := range n.enclosing {
				if !e.IsZero() {
					stack = append(stack, e.id)
				}
			}
		}
	}
	return false
}

func (t *table) Declare(service string, v Visibility) {
	t.mu.Lock()
	t.vis[service] = v
	t.mu.Unlock()
}

func (t *table) CheckMount(injects []string) []Diagnostic {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var out []Diagnostic
	for _, name := range injects {
		if t.vis[name] == Shared {
			out = append(out, Diagnostic{Code: CodeSharedRegistry, Service: name,
				Message: fmt.Sprintf("%q is a Shared registry: a component mounted under a scope would write past it", name)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// Key is the scope's key.
func (s *Scope) Key() Key { return s.key }

// Label is the label it was created with.
func (s *Scope) Label() string { return s.label }

// Ctx is the scope's registration context. It carries the key and reaches no
// service; its Lifetime ends when the scope closes.
func (s *Scope) Ctx() *cordis.Context { return s.ctx }

// SetEnclosing replaces the enclosing list. A key that is closed, of another table,
// or that would make the scope enclose itself is refused, and the list is left as
// it was.
func (s *Scope) SetEnclosing(keys ...Key) error {
	t := s.key.t
	t.mu.Lock()
	defer t.mu.Unlock()
	n, ok := t.nodes[s.key.id]
	if !ok || n.closed {
		return &Error{Code: CodeClosed, Msg: s.key.String() + " is closed"}
	}
	for _, e := range keys {
		if err := t.checkLive(e); err != nil {
			return err
		}
		if !e.IsZero() && t.reaches(e.id, s.key.id) {
			return &Error{Code: CodeCycle, Msg: fmt.Sprintf("%s encloses %s", e, s.key)}
		}
	}
	n.enclosing = slices.Clone(keys)
	return nil
}

// Close unloads the scope's fiber (and everything registered through it) and
// reclaims its layers. It is idempotent; concurrent callers wait for the same
// completion.
func (s *Scope) Close() error {
	s.once.Do(func() {
		s.fiber.Dispose()
		s.key.t.finish(s.key)
	})
	s.mu.Lock()
	h, fromOwner := s.ownerStop, s.fromOwner
	s.ownerStop = nil
	s.mu.Unlock()
	if h != nil && !fromOwner {
		h.Dispose()
	}
	return nil
}

// Closed reports whether Close has run.
func (s *Scope) Closed() bool { return !s.key.t.live(s.key) }

// Of is the key ctx carries, or the zero Key.
func Of(ctx *cordis.Context) Key {
	if ctx == nil {
		return Key{}
	}
	k, _ := ctx.Value(ctxKey{}).(Key)
	return k
}

// visible reports whether an entry registered under from is visible in at: from
// is global, at itself, or one of at's enclosing scopes.
func visible(from, at Key) bool {
	if from.IsZero() || from == at {
		return true
	}
	if at.IsZero() {
		return false
	}
	return slices.Contains(at.t.Enclosing(at), from)
}
