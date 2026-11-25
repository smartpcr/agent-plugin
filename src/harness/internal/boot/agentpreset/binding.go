package agentpreset

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// Cause is why a session's binding was written (bundle-preset.md §6.2).
type Cause string

// The causes.
const (
	CauseCreate  Cause = "create"
	CauseSelect  Cause = "select"
	CauseResume  Cause = "resume"
	CauseInherit Cause = "inherit"
	CauseRebind  Cause = "rebind"
)

// Valid reports whether c is one of the causes.
func (c Cause) Valid() bool {
	switch c {
	case CauseCreate, CauseSelect, CauseResume, CauseInherit, CauseRebind:
		return true
	}
	return false
}

// Selected is agent-preset/selected: the session is bound to the revision of Preset
// whose resolved children hash to Digest.
type Selected struct {
	Preset string `json:"preset"`
	Digest string `json:"digest"`
	Cause  Cause  `json:"cause"`
}

// SelectedType is the durable, log-only agent-preset/selected event.
var SelectedType = event.MustDefine(event.Default(), event.Spec[Selected]{Name: "agent-preset/selected", Version: 1,
	Validate: func(s Selected) error {
		if s.Preset == "" || s.Digest == "" {
			return errors.New("agent-preset/selected: a binding names its preset and digest")
		}
		if !s.Cause.Valid() {
			return fmt.Errorf("agent-preset/selected: cause %q is not one of create, select, resume, inherit, rebind", s.Cause)
		}
		return nil
	}})

// FoldKey is the key of the session's preset fold.
const FoldKey = "agentPreset"

// Binding is the preset fold of one session: its last agent-preset/selected. A zero
// Binding means the session was never bound.
type Binding struct {
	Preset string `json:"preset,omitempty"`
	Digest string `json:"digest,omitempty"`
	Cause  Cause  `json:"cause,omitempty"`
}

// BindingView is the preset fold (DH-D-VIEW-1): the last agent-preset/selected.
func BindingView() views.Definition[Binding] {
	return views.Definition[Binding]{
		Key: FoldKey, Version: 1, Stored: true, Types: []string{SelectedType.Name()},
		Init: func(session.Header) Binding { return Binding{} },
		Apply: func(b Binding, ev event.Event) (Binding, bool) {
			if ev.Type() != SelectedType.Name() {
				return b, false
			}
			s, err := SelectedType.As(ev)
			if err != nil {
				return b, false
			}
			return Binding(s), true
		},
	}
}

// RegisterView registers the preset fold in r.
func RegisterView(r *views.Registry) (views.Handle[Binding], error) {
	return views.Register(r, BindingView())
}

// Target is the agent a binding is for: its session's log, its scope, the context
// whose unload releases the lease (the agent's own) and, for Select, its
// maintenance gate.
type Target struct {
	Session session.Session
	Scope   *scope.Scope
	Owner   *cordis.Context
	// Maintainer runs Select as an agent maintenance task (DH-ADR-BUNDLE-6), so no
	// turn starts inside it; the agent's driver is one. Select requires it.
	Maintainer Maintainer
}

// Maintainer runs fn while no turn of the agent can open (driver.Driver.Maintain).
type Maintainer interface {
	Maintain(ctx context.Context, fn func(context.Context) error) error
}

// BindingOf is the preset fold of s, refolded from its log.
func BindingOf(ctx context.Context, s session.ReadSession) (Binding, error) {
	evs, err := s.Read(ctx, 0, 0)
	if err != nil {
		return Binding{}, err
	}
	return views.Refold(BindingView(), s.Header(), evs), nil
}

// Bind binds t to the current revision of id ("" is the default): the lease is
// taken, the agent scope encloses the revision's scope, and agent-preset/selected is
// appended unless the session's fold already names that preset and digest. An
// append that fails gives the lease back and restores the enclosing list. The lease
// t held before, if any, is released. Bindings of one agent scope are serialised,
// so the last one wins in the registry, the scope and the log alike.
func (r *Registry) Bind(ctx context.Context, t Target, id string, cause Cause) (Info, error) {
	if t.Session == nil || t.Scope == nil || t.Owner == nil {
		return Info{}, errors.New("agentpreset: a binding needs its session, scope and owner")
	}
	defer r.lockTarget(t.Scope.Key())()
	return r.bindLocked(ctx, t, id, cause)
}

func (r *Registry) bindLocked(ctx context.Context, t Target, id string, cause Cause) (Info, error) {
	l, err := r.Acquire(t.Owner, id)
	if err != nil {
		return Info{}, err
	}
	held, err := r.own(l)
	if err != nil {
		return Info{}, err
	}
	return r.attach(ctx, t, held, cause)
}

// own is l as this registry's lease, which attach records as a binding. Acquire
// and AcquireFrom only hand out *lease; any other Lease is given back and refused,
// so no count is leaked and no foreign lease is published.
func (r *Registry) own(l Lease) (*lease, error) {
	own, ok := l.(*lease)
	if !ok || own.r != r {
		if l != nil {
			l.Release()
		}
		return nil, fmt.Errorf("agentpreset: %T is not a lease of this registry", l)
	}
	return own, nil
}

// BindFrom binds t to the exact revision holder is bound to (cause inherit).
func (r *Registry) BindFrom(ctx context.Context, t Target, holder scope.Key) (Info, error) {
	if t.Session == nil || t.Scope == nil || t.Owner == nil {
		return Info{}, errors.New("agentpreset: a binding needs its session, scope and owner")
	}
	defer r.lockTarget(t.Scope.Key())()
	l, err := r.AcquireFrom(t.Owner, holder)
	if err != nil {
		return Info{}, err
	}
	held, err := r.own(l)
	if err != nil {
		return Info{}, err
	}
	return r.attach(ctx, t, held, CauseInherit)
}

// attach publishes l as t's binding; the target's lock is held.
func (r *Registry) attach(ctx context.Context, t Target, l *lease, cause Cause) (Info, error) {
	info := l.Info()
	agent := t.Scope.Key()
	r.mu.Lock()
	prev := r.bindings[agent]
	r.mu.Unlock()

	was := r.opts.Scopes.Enclosing(agent)
	next := slices.DeleteFunc(slices.Clone(was), func(k scope.Key) bool { return prev != nil && k == prev.Key() })
	if !slices.Contains(next, l.Key()) {
		next = append(next, l.Key())
	}
	if err := t.Scope.SetEnclosing(next...); err != nil {
		l.Release()
		return Info{}, fmt.Errorf("agentpreset: enclose %s: %w", agent, err)
	}
	cur, err := BindingOf(ctx, t.Session)
	if err == nil && (cur.Preset != info.ID || cur.Digest != info.Digest) {
		var d event.Draft
		d, err = SelectedType.Draft(Selected{Preset: info.ID, Digest: info.Digest, Cause: cause})
		if err == nil {
			_, err = t.Session.Append(ctx, d)
		}
	}
	if err != nil {
		_ = t.Scope.SetEnclosing(was...)
		l.Release()
		return Info{}, fmt.Errorf("agentpreset: bind %s to %q: %w", agent, info.ID, err)
	}
	r.mu.Lock()
	r.bindings[agent] = l
	r.mu.Unlock()
	if prev != nil && prev != l {
		prev.Release()
	}
	return info, nil
}

// Select is the user's choice of preset for a blank session (DH-ADR-BUNDLE-5). It
// runs as an agent maintenance task (Target.Maintainer, DH-ADR-BUNDLE-6), so the
// check that the session is blank and the binding it makes are one step no turn can
// start inside: a session that has opened a turn is refused with CodeLocked, and an
// agent already on the current revision of id is left as it is.
func (r *Registry) Select(ctx context.Context, t Target, id string) (Info, error) {
	if t.Session == nil || t.Scope == nil || t.Owner == nil || t.Maintainer == nil {
		return Info{}, errors.New("agentpreset: a selection needs its session, scope, owner and maintenance gate")
	}
	var info Info
	err := t.Maintainer.Maintain(ctx, func(ctx context.Context) error {
		defer r.lockTarget(t.Scope.Key())()
		if b := t.Session.Open(); b.TurnOpen || b.LastTurn > 0 {
			return &Error{Code: CodeLocked, Msg: fmt.Sprintf("session %s has started; its preset is fixed", t.Session.ID())}
		}
		want, err := r.Resolve(id)
		if err != nil {
			return err
		}
		if have, ok := r.Of(t.Scope.Key()); ok && have.ID == want.ID && have.Revision == want.Revision {
			info = have
			return nil
		}
		info, err = r.bindLocked(ctx, t, want.ID, CauseSelect)
		return err
	})
	if err != nil {
		return Info{}, err
	}
	return info, nil
}
