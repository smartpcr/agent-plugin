package agent

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// registry is the Registry: a map guarded by a mutex, with an incarnation
// counter per session id for Ref.
type registry struct {
	root *cordis.Context // dispatches the lifecycle notices

	mu      sync.Mutex
	factory Factory
	seat    uint64 // the generation of the seat holder
	entries map[session.ID]*entry
	inc     map[session.ID]uint64
}

// entry is one agent, reserved while its factory builds it.
type entry struct {
	r    *registry
	ref  Ref
	inst Instance // nil while reserved

	once   sync.Once
	err    error
	effect *cordis.EffectHandle
}

// NewRegistry is an empty registry; root dispatches its lifecycle notices.
func NewRegistry(root *cordis.Context) Registry {
	return &registry{root: root, entries: map[session.ID]*entry{}, inc: map[session.ID]uint64{}}
}

func (r *registry) SetFactory(owner *cordis.Context, f Factory) (*cordis.EffectHandle, error) {
	if owner == nil || f == nil {
		return nil, errorf(CodeInvalid, "SetFactory needs an owner and a factory")
	}
	return owner.Effect("agent.factory", func() (cordis.Disposer, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if r.factory != nil {
			return nil, errorf(CodeFactoryTaken, "the factory seat is already filled")
		}
		r.seat++
		gen := r.seat
		r.factory = f
		return func() error {
			r.mu.Lock()
			if r.seat == gen {
				r.factory = nil
			}
			r.mu.Unlock()
			return nil
		}, nil
	})
}

func (r *registry) Create(owner *cordis.Context, o CreateOptions) (Handle, error) {
	if owner == nil {
		return nil, errorf(CodeInvalid, "Create needs an owner context")
	}
	if o.Session == "" || strings.TrimSpace(string(o.Session)) != string(o.Session) {
		return nil, errorf(CodeInvalid, "session id %q is blank or padded", o.Session)
	}
	r.mu.Lock()
	f := r.factory
	if f == nil {
		r.mu.Unlock()
		return nil, errorf(CodeNoFactory, "no factory fills the seat")
	}
	if _, live := r.entries[o.Session]; live {
		r.mu.Unlock()
		return nil, errorf(CodeDuplicate, "session %q already has a live agent", o.Session)
	}
	r.inc[o.Session]++
	e := &entry{r: r, ref: Ref{Session: o.Session, Incarnation: r.inc[o.Session]}}
	r.entries[o.Session] = e
	r.mu.Unlock()

	inst, err := build(f, owner, o, e.ref)
	if err == nil && inst.Ref() != e.ref {
		err = errorf(CodeInvalid, "the factory built %s, not %s", inst.Ref(), e.ref)
	}
	if err == nil {
		err = Created.Emit(r.root, inst.ScopeKey(), CreatedNotice{Agent: view{inst}})
	}
	if err != nil {
		r.release(e)
		if inst != nil {
			_ = closeInst(inst, context.Background())
		}
		return nil, err
	}
	r.mu.Lock()
	e.inst = inst
	r.mu.Unlock()
	eh, err := owner.Effect("agent:"+string(o.Session), func() (cordis.Disposer, error) {
		return func() error { return e.dispose(context.Background()) }, nil
	})
	if err != nil {
		_ = e.dispose(context.Background())
		return nil, err
	}
	e.effect = eh
	return handle{e}, nil
}

func build(f Factory, owner *cordis.Context, o CreateOptions, ref Ref) (inst Instance, err error) {
	defer func() {
		if p := recover(); p != nil {
			inst, err = nil, fmt.Errorf("agent: the factory panicked: %v", p)
		}
	}()
	inst, err = f.Build(owner, o, ref)
	if err == nil && inst == nil {
		err = errorf(CodeInvalid, "the factory built no agent")
	}
	return inst, err
}

func closeInst(inst Instance, ctx context.Context) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("agent: close panicked: %v", p)
		}
	}()
	return inst.Close(ctx)
}

// release drops e if it is still the entry of its session.
func (r *registry) release(e *entry) {
	r.mu.Lock()
	if r.entries[e.ref.Session] == e {
		delete(r.entries, e.ref.Session)
	}
	r.mu.Unlock()
}

// dispose removes the entry, closes the instance and then notifies Disposed.
func (e *entry) dispose(ctx context.Context) error {
	e.once.Do(func() {
		e.r.release(e)
		e.err = closeInst(e.inst, ctx)
		Disposed.Notify(e.r.root, e.inst.ScopeKey(), DisposedNotice{Ref: e.ref})
	})
	return e.err
}

func (r *registry) live(id session.ID) (Instance, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || e.inst == nil {
		return nil, false
	}
	return e.inst, true
}

func (r *registry) Get(id session.ID) (Agent, bool) {
	inst, ok := r.live(id)
	if !ok {
		return nil, false
	}
	return view{inst}, true
}

func (r *registry) Resolve(ref Ref) (Agent, error) {
	if ref.IsZero() {
		return nil, errorf(CodeNotFound, "the zero reference names no agent")
	}
	if inst, ok := r.live(ref.Session); ok && inst.Ref() == ref {
		return view{inst}, nil
	}
	r.mu.Lock()
	known := ref.Incarnation > 0 && ref.Incarnation <= r.inc[ref.Session]
	r.mu.Unlock()
	if known {
		return nil, errorf(CodeStale, "%s was disposed or replaced", ref)
	}
	return nil, errorf(CodeNotFound, "no agent %s", ref)
}

func (r *registry) List() []Agent {
	r.mu.Lock()
	out := make([]Agent, 0, len(r.entries))
	for _, e := range r.entries {
		if e.inst != nil {
			out = append(out, view{e.inst})
		}
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b Agent) int { return strings.Compare(string(a.Ref().Session), string(b.Ref().Session)) })
	return out
}

// view is the Agent every reader gets: the instance without its Close.
type view struct{ a Instance }

func (v view) Ref() Ref                     { return v.a.Ref() }
func (v view) Session() session.ReadSession { return v.a.Session() }
func (v view) Scope() *cordis.Context       { return v.a.Scope() }
func (v view) ScopeKey() scope.Key          { return v.a.ScopeKey() }
func (v view) Status() Status               { return v.a.Status() }
func (v view) State() State                 { return v.a.State() }

// handle is the creator's Handle.
type handle struct{ e *entry }

func (h handle) Agent() Agent { return view{h.e.inst} }

func (h handle) Dispose(ctx context.Context) error {
	err := h.e.dispose(ctx)
	if h.e.effect != nil {
		h.e.effect.Dispose()
	}
	return err
}
