package views

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

var _ session.Restorer = (*Registry)(nil)

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

// must is v, panicking with err so safe reports it as the fold's failure.
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// step folds ev into f in place; a fold that panics is failed for the session
// from then on, and the others go on. A fold whose from is past ev (restored
// from a later checkpoint) skips it; changed, when not nil, collects the keys
// ev changed.
func (r *Registry) step(f *folded, ev event.Event, from map[string]event.Seq, changed map[string]bool) {
	for _, d := range r.foldsFor(ev.Type()) {
		if _, failed := f.failed[d.key]; failed || ev.Seq() < from[d.key] {
			continue
		}
		cur := f.values[d.key]
		var changedOne bool
		next, err := safe(d, func() any {
			n, c := d.apply(cur, ev)
			changedOne = c
			return n
		})
		switch {
		case err != nil:
			f.failed[d.key] = fmt.Errorf("at seq %d: %w", ev.Seq(), err)
			delete(f.values, d.key)
		case changedOne:
			f.values[d.key] = next
			if changed != nil {
				changed[d.key] = true
			}
		}
	}
	f.next = ev.Seq() + 1
}

// rebuild folds s's events [0, at), each stored fold starting from its newest
// usable checkpoint row when s can list them (a row of this fold's version
// strictly below at, so it reflects nothing past the cut).
func (r *Registry) rebuild(ctx context.Context, s session.ReadSession, at event.Seq) (*folded, error) {
	var cps []store.Checkpoint
	if c, ok := s.(session.Checkpointed); ok && r.anyStored() {
		var err error
		if cps, err = c.Checkpoints(ctx); err != nil {
			cps = nil // stored state is derived: fold from the log
		}
	}
	f, _, err := r.restore(ctx, s, at, cps)
	return f, err
}

// anyStored reports whether a registered fold is Stored.
func (r *Registry) anyStored() bool {
	for _, d := range r.defs {
		if d.stored {
			return true
		}
	}
	return false
}

// restore folds s's events [0, at), taking each stored fold up from its row in
// cps when one is usable, and reports the stored folds whose rows are missing,
// of another version, undecodable or behind at-1: those the caller rewrites.
func (r *Registry) restore(ctx context.Context, s session.ReadSession, at event.Seq, cps []store.Checkpoint) (*folded, []string, error) {
	f := r.initial(s.Header())
	from := map[string]event.Seq{}
	var stale []string
	low := at
	for _, d := range r.defs {
		start := event.Seq(0)
		if d.stored {
			if cp, ok := usable(cps, d, at); ok {
				// A row that does not decode (or panics decoding) is unusable:
				// the fold is rebuilt from the log and the row rewritten.
				if v, err := safe(d, func() any { return must(d.decode(cp.State)) }); err == nil {
					f.values[d.key] = v
					delete(f.failed, d.key)
					start = cp.Seq + 1
				}
			}
			if start != at {
				stale = append(stale, d.key)
			}
		}
		from[d.key] = start
		low = min(low, start)
	}
	if low < at {
		evs, err := s.Read(ctx, low, int(at-low))
		if err != nil {
			return nil, nil, err
		}
		if len(evs) != int(at-low) || (len(evs) > 0 && evs[0].Seq() != low) {
			return nil, nil, errorf(CodeFailed, "session %q: read %d events in [%d, %d)", s.ID(), len(evs), low, at)
		}
		for _, ev := range evs {
			r.step(f, ev, from, nil)
		}
	}
	f.next = at
	return f, stale, nil
}

// usable is d's newest checkpoint row of d's version below at.
func usable(cps []store.Checkpoint, d *def, at event.Seq) (store.Checkpoint, bool) {
	var best store.Checkpoint
	found := false
	for _, c := range cps {
		if c.Key == d.key && c.StateVersion == d.version && c.Seq < at && (!found || c.Seq > best.Seq) {
			best, found = c, true
		}
	}
	return best, found
}

// rows is the checkpoint rows of the stored folds among keys in f, skipping a
// failed fold. A state that does not encode (an error or a panic) is invalid
// state: that key is failed in f from then on and the other folds go on
// (session-views.md §7). The caller calls rows before it publishes f.
func (r *Registry) rows(f *folded, keys func(*def) bool) []session.Derived {
	var out []session.Derived
	for _, d := range r.defs {
		if !d.stored || !keys(d) {
			continue
		}
		if _, failed := f.failed[d.key]; failed {
			continue
		}
		state, err := safe(d, func() any { return must(d.encode(f.values[d.key])) })
		if err != nil {
			f.failed[d.key] = fmt.Errorf("storing its state at seq %d: %w", f.next, err)
			delete(f.values, d.key)
			continue
		}
		//lint:ignore typeassert d.encode returns a string, and safe passes its value through
		out = append(out, session.Derived{Key: d.key, StateVersion: d.version, State: state.(string)})
	}
	return out
}

// Restore is the session's Restorer hook, called once when a session opens:
// each stored fold is taken up from its checkpoint row and the log after it,
// the others are folded from the log, and the result is the base the next
// batch folds on. The rows returned are the stored folds whose row was missing,
// of another version or behind the log; the session commits them at its head.
// A registry with no stored fold does nothing here and folds on the first read.
func (r *Registry) Restore(ctx context.Context, s session.ReadSession, cps []store.Checkpoint) ([]session.Derived, error) {
	r.mu.Lock()
	r.sealLocked()
	r.mu.Unlock()
	if !r.anyStored() {
		return nil, nil
	}
	f, stale, err := r.restore(ctx, s, s.Len(), cps)
	if err != nil {
		return nil, err
	}
	rows := r.rows(f, func(d *def) bool { return slices.Contains(stale, d.key) })
	r.seed(s.ID(), f)
	return rows, nil
}

// Fold is the session's Folder hook, called under the append lock with each
// batch before storage commits it, so it does no I/O. The previous batch's
// result is promoted when this one starts where it ended (it committed) and
// discarded when this one starts where the committed state ends (its append
// failed). Any other start is a session this registry has not followed (opened
// before it, forgotten, or reopened after a restart): Fold folds nothing, and
// the next Read rebuilds from the log at its cut, outside the append lock, and
// seeds the base later batches fold from. The rows returned are the stored
// folds the batch changed (every stored fold for a batch folded from 0), which
// the session commits with the batch at its last seq.
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
	changed := map[string]bool{}
	for _, ev := range batch {
		r.step(next, ev, nil, changed)
	}
	rows := r.rows(next, func(d *def) bool { return rebuilt || changed[d.key] })
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
	return rows
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
