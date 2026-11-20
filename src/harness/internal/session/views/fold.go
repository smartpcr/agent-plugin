package views

import (
	"context"
	"fmt"
	"maps"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

func (f *folded) clone() *folded {
	return &folded{next: f.next, values: maps.Clone(f.values), failed: maps.Clone(f.failed)}
}

func (r *Registry) initial(h session.Header) *folded {
	f := &folded{values: map[string]any{}, failed: map[string]error{}}
	for _, d := range r.defs {
		v, err := safe(d, func() any { return d.init(h) })
		if err != nil {
			f.failed[d.key] = err
			continue
		}
		f.values[d.key] = v
	}
	return f
}

// safe runs fn, turning a panic into a view/failed error for d.
func safe(d *def, fn func() any) (v any, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = errorf(CodeFailed, "fold %q panicked: %v", d.key, p)
		}
	}()
	return fn(), nil
}

// step folds ev into f in place; a fold that panics is failed for the session
// from then on, and the others go on.
func (r *Registry) step(f *folded, ev event.Event) {
	for _, d := range r.foldsFor(ev.Type()) {
		if _, failed := f.failed[d.key]; failed {
			continue
		}
		cur := f.values[d.key]
		var changed bool
		next, err := safe(d, func() any {
			n, c := d.apply(cur, ev)
			changed = c
			return n
		})
		switch {
		case err != nil:
			f.failed[d.key] = fmt.Errorf("at seq %d: %w", ev.Seq(), err)
			delete(f.values, d.key)
		case changed:
			f.values[d.key] = next
		}
	}
	f.next = ev.Seq() + 1
}

// rebuild folds s's events [0, at) from its log.
func (r *Registry) rebuild(ctx context.Context, s session.ReadSession, at event.Seq) (*folded, error) {
	f := r.initial(s.Header())
	if at == 0 {
		return f, nil
	}
	evs, err := s.Read(ctx, 0, int(at))
	if err != nil {
		return nil, err
	}
	if len(evs) != int(at) {
		return nil, errorf(CodeFailed, "session %q: read %d events below %d", s.ID(), len(evs), at)
	}
	for _, ev := range evs {
		r.step(f, ev)
	}
	return f, nil
}

// Fold is the session's Folder hook, called under the append lock with each
// batch before storage commits it, so it does no I/O. The previous batch's
// result is promoted when this one starts where it ended (it committed) and
// discarded when this one starts where the committed state ends (its append
// failed). Any other start is a session this registry has not followed (opened
// before it, forgotten, or reopened after a restart): Fold folds nothing, and
// the next Read rebuilds from the log at its cut, outside the append lock, and
// seeds the base later batches fold from.
func (r *Registry) Fold(s session.ReadSession, batch []event.Event) []session.Derived {
	if len(batch) == 0 {
		return nil
	}
	first := batch[0].Seq()
	r.mu.Lock()
	r.sealLocked()
	base := r.baseLocked(s.ID(), first)
	r.mu.Unlock()
	rebuilt := base == nil
	if rebuilt {
		if first != 0 {
			return nil
		}
		base = r.initial(s.Header())
	}
	next := base.clone()
	for _, ev := range batch {
		r.step(next, ev)
	}
	r.mu.Lock()
	t := r.sessions[s.ID()]
	if t == nil {
		t = &tracked{}
		r.sessions[s.ID()] = t
	}
	if rebuilt {
		t.committed = base
	}
	t.pending = next
	r.mu.Unlock()
	return nil
}

// baseLocked is the committed fold of id ending at at, promoting a pending
// fold that ends there; nil when the registry has none. The caller holds r.mu.
func (r *Registry) baseLocked(id session.ID, at event.Seq) *folded {
	t := r.sessions[id]
	if t == nil {
		return nil
	}
	if t.pending != nil {
		if t.pending.next == at {
			t.committed = t.pending
		}
		t.pending = nil
	}
	if t.committed != nil && t.committed.next == at {
		return t.committed
	}
	return nil
}

// seed records f, rebuilt from a live session's log, as the committed fold the
// next batch starting at f.next builds on, unless the registry already holds a
// later one. A batch that committed meanwhile leaves f behind; the next Read
// then rebuilds and seeds again.
func (r *Registry) seed(id session.ID, f *folded) {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.sessions[id]
	if t == nil {
		t = &tracked{}
		r.sessions[id] = t
	}
	if t.committed == nil || t.committed.next < f.next {
		t.committed = f
	}
}

// Forget drops the registry's state for a session, after it is closed or
// deleted; a later read folds it from the log.
func (r *Registry) Forget(id session.ID) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

// asOf is a live session's one consistent position.
type asOf interface {
	AsOf(fn func(at session.Offset))
}

// Read returns the folds named by keys (every fold when keys is empty) of s,
// all at one cut: a live session's AsOf position, else its Len. An unregistered
// key is view/unknown-key.
func (r *Registry) Read(ctx context.Context, s session.ReadSession, keys ...string) (Snapshot, error) {
	r.mu.Lock()
	r.sealLocked()
	if len(keys) == 0 {
		for _, d := range r.defs {
			keys = append(keys, d.key)
		}
	}
	for _, k := range keys {
		if _, ok := r.byKey[k]; !ok {
			r.mu.Unlock()
			return Snapshot{}, errorf(CodeUnknownKey, "no fold %q is registered", k)
		}
	}
	r.mu.Unlock()

	var at event.Seq
	var hit *folded
	l, live := s.(asOf)
	if live {
		l.AsOf(func(n session.Offset) {
			at = n
			r.mu.Lock()
			hit = r.baseLocked(s.ID(), n)
			r.mu.Unlock()
		})
	} else {
		at = s.Len()
	}
	if hit == nil {
		var err error
		if hit, err = r.rebuild(ctx, s, at); err != nil {
			return Snapshot{}, err
		}
		if live {
			r.seed(s.ID(), hit)
		}
	}
	snap := Snapshot{AsOf: at, values: map[string]any{}, failed: map[string]error{}}
	for _, k := range keys {
		if err, failed := hit.failed[k]; failed {
			snap.failed[k] = err
			continue
		}
		snap.values[k] = hit.values[k]
	}
	return snap, nil
}
