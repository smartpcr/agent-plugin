package agent

import (
	"context"
	"errors"
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
	// live is set once Created has passed: Get, Resolve and List see the
	// agent from then on. Guarded by r.mu, as are the fields below it.
	live bool
	// creating is closed when the agent's Created dispatch has returned;
	// creatingG is the goroutine running it. nil until Created begins.
	creating  chan struct{}
	creatingG uint64
	effects   [2]*cordis.EffectHandle // on the owner's fiber, on the co-owner's
	swept     bool
	// closing is set when the teardown begins; a creation then never goes live.
	closing bool
	// built is closed once the build has settled: inst is set, or the build
	// failed and rolled itself back.
	built chan struct{}

	once      sync.Once
	leaveOnce sync.Once
	err       error
}

// The owner effects' indexes in entry.effects; noEffect is a disposer that
// runs from none of them.
const (
	ownerEffect = 0
	coEffect    = 1
	noEffect    = -1
)

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
	o.Resume = false
	return r.create(owner, o)
}

func (r *registry) Resume(owner *cordis.Context, o CreateOptions) (Handle, error) {
	o.Resume = true
	return r.create(owner, o)
}

// create is the registry's half of the creation transaction (agent-loop.md
// §6.3): reserve the id, build (the factory runs steps 1 to 4 and rolls its
// own part back), dispatch Created, tie the teardown to both owners, enter
// the registry and go live. A failure after the build runs the instance's
// teardown before the error is returned.
func (r *registry) create(owner *cordis.Context, o CreateOptions) (Handle, error) {
	if owner == nil {
		return nil, errorf(CodeInvalid, "Create needs an owner context")
	}
	if o.Session == "" || strings.TrimSpace(string(o.Session)) != string(o.Session) {
		return nil, errorf(CodeInvalid, "session id %q is blank or padded", o.Session)
	}
	if err := owner.Lifetime().Err(); err != nil {
		return nil, &Error{Code: CodeInvalid, Msg: "the owner is not active", Err: err}
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
	e := &entry{r: r, ref: Ref{Session: o.Session, Incarnation: r.inc[o.Session]}, built: make(chan struct{})}
	r.entries[o.Session] = e
	r.mu.Unlock()

	// The owner's effect is in place before anything is built, so an owner
	// that unloads while the build or Created still runs waits for them and
	// then tears down what was built: nothing outlives the owner's unload.
	if err := e.own(owner, ownerEffect); err != nil {
		r.release(e)
		close(e.built)
		return nil, &Error{Code: CodeInvalid, Msg: "the owner is not active", Err: err}
	}
	bg := context.Background()
	inst, err := build(f, owner, o, e.ref)
	if err == nil && inst.Ref() != e.ref {
		err = errorf(CodeInvalid, "the factory built %s, not %s", inst.Ref(), e.ref)
	}
	if err != nil {
		r.release(e)
		if inst != nil {
			err = errors.Join(err, closeInst(inst, bg))
		}
		close(e.built)
		e.sweep(noEffect)
		return nil, err
	}

	// An owner already unloading tears the agent down without Created.
	g, _ := goroutineID()
	began := owner.Lifetime().Err() == nil
	r.mu.Lock()
	e.inst = inst
	if began {
		e.creating, e.creatingG = make(chan struct{}), g
	}
	r.mu.Unlock()
	close(e.built)
	if !began {
		return nil, errors.Join(&Error{Code: CodeInvalid, Msg: "the owner unloaded during the creation", Err: owner.Lifetime().Err()},
			e.dispose(bg, noEffect))
	}
	if co, ok := inst.(CoOwned); ok && co.CoOwner() != nil && co.CoOwner() != owner {
		if err := e.own(co.CoOwner(), coEffect); err != nil {
			// Created never began: withdraw the mark so no Disposed is sent.
			r.mu.Lock()
			ch := e.creating
			e.creating = nil
			r.mu.Unlock()
			close(ch)
			return nil, errors.Join(err, e.dispose(bg, noEffect))
		}
	}
	err = emitCreated(r.root, inst, view{inst, e})
	close(e.creating)
	if err == nil {
		if cause := owner.Lifetime().Err(); cause != nil {
			err = &Error{Code: CodeInvalid, Msg: "the owner unloaded during the creation", Err: cause}
		}
	}
	if err != nil {
		return nil, errors.Join(err, e.dispose(bg, noEffect))
	}

	// Go live unless a teardown has begun meanwhile.
	r.mu.Lock()
	closing := e.closing
	if !closing {
		e.live = true
	}
	r.mu.Unlock()
	if closing {
		return nil, errors.Join(Closing(e.ref), e.dispose(bg, noEffect))
	}
	if act, ok := inst.(Activator); ok {
		if err := activate(act); err != nil {
			return nil, errors.Join(err, e.dispose(bg, noEffect))
		}
	}
	return handle{e}, nil
}

// own ties e's teardown to ctx's fiber as effect i. Its disposer waits for a
// build still running and tears down what it built; a build that failed has
// rolled itself back.
func (e *entry) own(ctx *cordis.Context, i int) error {
	eh, err := ctx.Effect("agent:"+string(e.ref.Session), func() (cordis.Disposer, error) {
		return func() error {
			<-e.built
			if e.inst == nil {
				return nil
			}
			return e.dispose(context.Background(), i)
		}, nil
	})
	if err != nil {
		return err
	}
	e.r.mu.Lock()
	swept := e.swept
	if !swept {
		e.effects[i] = eh
	}
	e.r.mu.Unlock()
	if swept {
		eh.Dispose()
	}
	return nil
}

func emitCreated(root *cordis.Context, inst Instance, a Agent) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("agent: a %s listener panicked: %v", Created.Name(), p)
		}
	}()
	return Created.Emit(root, inst.ScopeKey(), CreatedNotice{Agent: a})
}

func activate(a Activator) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("agent: activate panicked: %v", p)
		}
	}()
	return a.Activate()
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

// dispose runs the agent's teardown once; every other caller waits for it and
// gets its result. from is the owner effect whose disposer calls, which the
// sweep skips. Called from inside the agent's own Created dispatch it fails
// at once: that wait could never end.
func (e *entry) dispose(ctx context.Context, from int) error {
	if e.selfWait() {
		return errorf(CodeSelfWait, "Dispose of %s from inside its own Created dispatch", e.ref)
	}
	e.once.Do(func() {
		e.r.mu.Lock()
		e.closing = true
		e.r.mu.Unlock()
		if td, ok := e.inst.(Teardowner); ok {
			e.err = teardown(td, ctx, TeardownHooks{JoinCreation: e.join, Leave: e.leave})
		} else {
			e.r.release(e)
			e.err = closeInst(e.inst, ctx)
		}
		e.leave()
	})
	e.sweep(from)
	return e.err
}

func teardown(td Teardowner, ctx context.Context, h TeardownHooks) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("agent: teardown panicked: %v", p)
		}
	}()
	return td.Teardown(ctx, h)
}

// leave is T7: out of the registry, then Disposed if Created had begun.
func (e *entry) leave() {
	e.leaveOnce.Do(func() {
		e.r.release(e)
		e.r.mu.Lock()
		began := e.creating != nil
		e.r.mu.Unlock()
		if began {
			Disposed.Notify(e.r.root, e.inst.ScopeKey(), DisposedNotice{Ref: e.ref})
		}
	})
}

// join is T2: wait for a Created dispatch still running.
func (e *entry) join() {
	e.r.mu.Lock()
	ch := e.creating
	e.r.mu.Unlock()
	if ch != nil {
		<-ch
	}
}

// selfWait reports whether the caller is the goroutine running e's Created
// dispatch, which has not returned.
func (e *entry) selfWait() bool {
	e.r.mu.Lock()
	ch, g := e.creating, e.creatingG
	e.r.mu.Unlock()
	if ch == nil || g == 0 {
		return false
	}
	select {
	case <-ch:
		return false
	default:
	}
	id, ok := goroutineID()
	return ok && id == g
}

// sweep disposes the owner effects but from, once: an effect left on a
// long-lived owner would outlive its agent.
func (e *entry) sweep(from int) {
	e.r.mu.Lock()
	if e.swept {
		e.r.mu.Unlock()
		return
	}
	e.swept = true
	hs := e.effects
	e.r.mu.Unlock()
	for i, h := range hs {
		if i != from && h != nil {
			h.Dispose()
		}
	}
}

func (r *registry) live(id session.ID) (Instance, *entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.entries[id]
	if !ok || !e.live {
		return nil, nil, false
	}
	return e.inst, e, true
}

func (r *registry) Get(id session.ID) (Agent, bool) {
	inst, e, ok := r.live(id)
	if !ok {
		return nil, false
	}
	return view{inst, e}, true
}

func (r *registry) Resolve(ref Ref) (Agent, error) {
	if ref.IsZero() {
		return nil, errorf(CodeNotFound, "the zero reference names no agent")
	}
	r.mu.Lock()
	e, ok := r.entries[ref.Session]
	var inst Instance
	if ok && e.live {
		inst = e.inst
	}
	// A reserved entry with this ref is still being created: not yet live, not stale.
	creating := ok && !e.live && e.ref == ref
	known := ref.Incarnation > 0 && ref.Incarnation <= r.inc[ref.Session]
	r.mu.Unlock()
	if inst != nil && inst.Ref() == ref {
		return view{inst, e}, nil
	}
	if creating {
		return nil, errorf(CodeNotFound, "%s is still being created", ref)
	}
	if known {
		return nil, errorf(CodeStale, "%s was disposed or replaced", ref)
	}
	return nil, errorf(CodeNotFound, "no agent %s", ref)
}

func (r *registry) List() []Agent {
	r.mu.Lock()
	out := make([]Agent, 0, len(r.entries))
	for _, e := range r.entries {
		if e.live {
			out = append(out, view{e.inst, e})
		}
	}
	r.mu.Unlock()
	slices.SortFunc(out, func(a, b Agent) int { return strings.Compare(string(a.Ref().Session), string(b.Ref().Session)) })
	return out
}

// view is the Agent every reader gets: the instance without its Close.
type view struct {
	a Instance
	e *entry
}

func (v view) Ref() Ref                     { return v.a.Ref() }
func (v view) Session() session.ReadSession { return v.a.Session() }
func (v view) Scope() *cordis.Context       { return v.a.Scope() }
func (v view) ScopeKey() scope.Key          { return v.a.ScopeKey() }
func (v view) Status() Status               { return v.a.Status() }
func (v view) State() State                 { return v.a.State() }

// handle is the creator's Handle.
type handle struct{ e *entry }

func (h handle) Agent() Agent { return view{h.e.inst, h.e} }

func (h handle) Dispose(ctx context.Context) error { return h.e.dispose(ctx, noEffect) }
