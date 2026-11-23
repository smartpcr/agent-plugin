package views

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

var (
	_ session.Restorer      = (*Registry)(nil)
	_ session.ClosingFolder = (*Registry)(nil)
)

func (f *folded) clone() *folded {
	return &folded{next: f.next, values: maps.Clone(f.values), failed: maps.Clone(f.failed), written: maps.Clone(f.written)}
}

func (r *Registry) initial(h session.Header) *folded {
	f := &folded{values: map[string]any{}, failed: map[string]error{}, written: map[string]event.Seq{}}
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
// from a later checkpoint) skips it.
func (r *Registry) step(f *folded, ev event.Event, from map[string]event.Seq) {
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
		}
	}
	f.next = ev.Seq() + 1
}

// checkpoints is s's stored rows when s can list them and a fold is stored; nil
// otherwise, or when listing fails (stored state is derived: fold from the log).
func (r *Registry) checkpoints(ctx context.Context, s session.ReadSession) []store.Checkpoint {
	c, ok := s.(session.Checkpointed)
	if !ok || !r.anyStored() {
		return nil
	}
	cps, err := c.Checkpoints(ctx)
	if err != nil {
		return nil
	}
	return cps
}

// rebuild folds s's events [0, at), each stored fold starting from its newest
// usable checkpoint row when s can list them (a row of this fold's version
// strictly below at, so it reflects nothing past the cut). A limit above 0
// refuses, with view/behind, a catch-up of more than limit events.
func (r *Registry) rebuild(ctx context.Context, s session.ReadSession, at event.Seq, limit int) (*folded, error) {
	f, _, err := r.restore(ctx, s, at, r.checkpoints(ctx, s), limit)
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
// of another version, undecodable or behind at-1: those the caller rewrites. A
// limit above 0 refuses, with view/behind and before reading the log, a
// catch-up of more than limit events.
func (r *Registry) restore(ctx context.Context, s session.ReadSession, at event.Seq, cps []store.Checkpoint, limit int) (*folded, []string, error) {
	f := r.initial(s.Header())
	from := map[string]event.Seq{}
	var stale []string
	low := at
	for _, d := range r.defs {
		start := event.Seq(0)
		if d.stored {
			if v, cp, ok := decodeRow(cps, d, at); ok {
				f.values[d.key] = v
				delete(f.failed, d.key)
				start = cp.Seq + 1
				f.written[d.key] = start
			}
			if start != at {
				stale = append(stale, d.key)
			}
		}
		from[d.key] = start
		low = min(low, start)
	}
	if limit > 0 && at-low > event.Seq(limit) {
		return nil, nil, behind(s.ID(), low, at, limit)
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
			r.step(f, ev, from)
		}
	}
	f.next = at
	return f, stale, nil
}

// decodeRow is d's state from its newest usable row in cps below at. A row that
// does not decode (or panics decoding) is unusable: the fold is then rebuilt
// from the log, or pending at Stored freshness.
func decodeRow(cps []store.Checkpoint, d *def, at event.Seq) (any, store.Checkpoint, bool) {
	cp, ok := usable(cps, d, at)
	if !ok {
		return nil, cp, false
	}
	v, err := safe(d, func() any { return must(d.decode(cp.State)) })
	if err != nil {
		return nil, cp, false
	}
	return v, cp, true
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

// due reports whether stored fold d writes its row with the batch that took f
// to f.next: an EveryCommit fold always; a Periodic fold when no row of it is
// known, when the batch holds a turn end, or once its row is
// CheckpointEveryEvents events behind.
func (r *Registry) due(f *folded, d *def, turnEnd bool) bool {
	if d.cadence == EveryCommit {
		return true
	}
	w, ok := f.written[d.key]
	return !ok || turnEnd || f.next-w >= event.Seq(r.cfg.CheckpointEveryEvents)
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
	at := s.Len()
	f, stale, err := r.restore(ctx, s, at, cps, 0)
	if err != nil {
		return nil, err
	}
	rows := r.rows(f, func(d *def) bool { return slices.Contains(stale, d.key) })
	for _, row := range rows {
		f.written[row.Key] = at
	}
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
// folds due by their cadence (every stored fold for a batch folded from 0),
// which the session commits with the batch at its last seq.
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
	turnEnd := false
	for _, ev := range batch {
		r.step(next, ev, nil)
		turnEnd = turnEnd || ev.Type() == event.TurnEndType.Name()
	}
	rows := r.rows(next, func(d *def) bool { return rebuilt || r.due(next, d, turnEnd) })
	for _, row := range rows {
		next.written[row.Key] = next.next
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
	return rows
}

// Closing is the session's close hook, called under the append lock as the
// session closes: the rows of the stored folds whose newest row is behind the
// log head (the Periodic folds not due since), which the session commits at its
// head. A session this registry has not followed gets none.
func (r *Registry) Closing(s session.ReadSession) []session.Derived {
	n := s.Len()
	r.mu.Lock()
	base := r.baseLocked(s.ID(), n)
	r.mu.Unlock()
	if base == nil || n == 0 {
		return nil
	}
	f := base.clone()
	return r.rows(f, func(d *def) bool {
		w, ok := f.written[d.key]
		return !ok || w < n
	})
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
			r.clearFailuresLocked(id, t.committed)
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
// all at one cut: a live session's AsOf position, else its Len. It is the
// in-lease Exact read: every value is current (or failed), and the catch-up is
// not bounded. An unregistered key is view/unknown-key.
func (r *Registry) Read(ctx context.Context, s session.ReadSession, keys ...string) (Snapshot, error) {
	return r.snapshot(ctx, s, keys, Exact, 0)
}

// Snapshot returns the folds named by keys (every fold when keys is empty) of
// s at freshness f, all at one cut: a live session's AsOf position, else its
// Len, which is the snapshot's HeadSeq. A session this registry follows is read
// from memory, every value current, at either freshness. Otherwise Exact folds
// the log after the stored rows, at most Config.MaxCatchUpEvents events, and
// fails with view/behind (both positions set) when more are missing; Stored
// never folds and never blocks: each value is its stored row, current when the
// row reaches the head, behind (with the row's AsOfSeq) when it does not, and
// pending when there is no usable row (none, another version, undecodable, or a
// fold that is not Stored). An unregistered key is view/unknown-key.
func (r *Registry) Snapshot(ctx context.Context, s session.ReadSession, keys []string, f Freshness) (Snapshot, error) {
	if f != Stored && f != Exact {
		return Snapshot{}, errorf(CodeInvalid, "freshness %d is neither Stored nor Exact", f)
	}
	return r.snapshot(ctx, s, keys, f, r.cfg.MaxCatchUpEvents)
}

// snapshot is Read and Snapshot; limit bounds an Exact catch-up (0: none).
func (r *Registry) snapshot(ctx context.Context, s session.ReadSession, keys []string, f Freshness, limit int) (Snapshot, error) {
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
	if hit == nil && f == Stored {
		return r.stored(ctx, s, keys, at)
	}
	if hit == nil {
		var err error
		if hit, err = r.rebuild(ctx, s, at, limit); err != nil {
			return Snapshot{}, err
		}
		if live {
			r.seed(s.ID(), hit)
		}
	}
	snap := Snapshot{AsOf: at, HeadSeq: at, values: map[string]any{}, failed: map[string]error{}, states: map[string]Value{}}
	for _, k := range keys {
		if err, failed := hit.failed[k]; failed {
			snap.failed[k] = err
			snap.states[k] = Value{State: Failed}
			continue
		}
		snap.values[k] = hit.values[k]
		snap.states[k] = Value{State: Current, AsOfSeq: at}
	}
	return snap, nil
}

// stored is the Stored snapshot of keys of s at head at, from its rows only.
func (r *Registry) stored(ctx context.Context, s session.ReadSession, keys []string, at event.Seq) (Snapshot, error) {
	snap := Snapshot{HeadSeq: at, values: map[string]any{}, failed: map[string]error{}, states: map[string]Value{}}
	var cps []store.Checkpoint
	if c, ok := s.(session.Checkpointed); ok && at > 0 && r.anyStored() {
		var err error
		if cps, err = c.Checkpoints(ctx); err != nil {
			return Snapshot{}, err
		}
	}
	low := at
	for _, k := range keys {
		d := r.byKey[k]
		if at == 0 {
			// An empty log: the initial state is exact without folding.
			v, err := safe(d, func() any { return d.init(s.Header()) })
			if err != nil {
				snap.failed[k] = err
				snap.states[k] = Value{State: Failed}
				continue
			}
			snap.values[k] = v
			snap.states[k] = Value{State: Current}
			continue
		}
		v, cp, ok := decodeRow(cps, d, at)
		if !d.stored || !ok {
			snap.states[k] = Value{State: Pending}
			continue
		}
		val := Value{State: Current, AsOfSeq: cp.Seq + 1}
		if val.AsOfSeq < at {
			val.State = Behind
		}
		snap.values[k] = v
		snap.states[k] = val
		low = min(low, val.AsOfSeq)
	}
	snap.AsOf = low
	return snap, nil
}

// StoredState is d's state from its row in cps at Stored freshness against a
// log whose next seq is head: no fold, no log read. It is current when the row
// reaches the head, behind (with the row's AsOfSeq) when it does not, and
// pending, with the zero state, when there is no usable row (none, another
// version, a row at or past head, or one that does not decode). An empty log
// is d's initial state, current. A reader that holds the store's rows and not a
// session (the query service) reads its list summary so.
func StoredState[S any](d Definition[S], h session.Header, cps []store.Checkpoint, head event.Seq) (S, Value) {
	var zero S
	if head == 0 {
		return d.Init(h), Value{State: Current}
	}
	var best store.Checkpoint
	found := false
	for _, c := range cps {
		if c.Key == d.Key && c.StateVersion == d.Version && c.Seq < head && (!found || c.Seq > best.Seq) {
			best, found = c, true
		}
	}
	if !d.Stored || !found {
		return zero, Value{State: Pending}
	}
	var s S
	if err := decodeState(best.State, &s); err != nil {
		return zero, Value{State: Pending}
	}
	v := Value{State: Current, AsOfSeq: best.Seq + 1}
	if v.AsOfSeq < head {
		v.State = Behind
	}
	return s, v
}

// decodeState decodes a stored row into v; a decoder that panics is an error.
func decodeState(raw string, v any) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("decoding a stored row panicked: %v", p)
		}
	}()
	return json.Unmarshal([]byte(raw), v)
}
