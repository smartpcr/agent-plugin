package views

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// The background rebuild (M1-G-STORE-15, DH-D-VIEW-2, session-views.md "The
// host dies during a rebuild"). A stored fold whose Version changed has no
// usable row in any session until the session is rebuilt: on first touch (an
// open's Restore) or by the sweep here. Rebuild folds each session that lacks a
// row of the fold's version, read-only and without the session's lease, at
// most Config.RebuildConcurrency sessions at a time, and writes each result
// with the store's forward-only UpsertDerived. The rows are the progress
// record: a run that is killed leaves the sessions it wrote with a current-
// version row, so the next run lists only the rest. Until its row is written a
// session's readers see pending, never the old version's value (a row of
// another version is never usable).

// DutyRebuild is the background rebuild's duty: one instance of a home runs it.
const DutyRebuild = "views-rebuild"

// The rebuild's paging.
const (
	// rebuildPage is how many sessions lacking a row a rebuild lists at a time.
	rebuildPage = 256
	// rebuildReadChunk bounds the events one log read of a rebuild returns.
	rebuildReadChunk = 1024
)

// RebuildScope is which sessions a Rebuild folds.
type RebuildScope struct {
	// Sessions are the sessions to rebuild, each caught up from its usable row
	// (if any) to its durable head. Empty is every session of the bound store
	// that lacks a row of the fold's version.
	Sessions []session.ID
}

// Opener opens a session read-only, without its lease; session.Service is one.
type Opener interface {
	OpenRead(ctx context.Context, id session.ID) (session.ReadSession, func(), error)
}

// DutyRunner is a store that runs a duty on one instance of its home
// (sqlite.Store.RunDuty).
type DutyRunner interface {
	RunDuty(ctx context.Context, name string, every time.Duration, fn func(context.Context) error) (bool, error)
}

// binding is what Bind gave the registry.
type binding struct {
	st   store.Store
	open Opener
	stop func()
}

// Bind gives the registry the store its Rebuild and Status read and write, and
// the opener a rebuild reads sessions with (normally the session service over
// st). When st sends store.Degraded notices the registry subscribes, and a
// derived-dropped notice marks the named folds failed for that session (Status).
// A later Bind replaces this one; stop unsubscribes and unbinds.
func (r *Registry) Bind(st store.Store, open Opener) (stop func()) {
	b := &binding{st: st, open: open, stop: func() {}}
	if n, ok := st.(store.DegradedNotifier); ok {
		b.stop = n.OnDegraded(r.onDegraded)
	}
	r.mu.Lock()
	old := r.bound
	r.bound = b
	r.mu.Unlock()
	if old != nil {
		old.stop()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			if r.bound == b {
				r.bound = nil
			}
			r.mu.Unlock()
			b.stop()
		})
	}
}

// boundStore is the binding, or view/invalid when there is none.
func (r *Registry) boundStore() (*binding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sealLocked()
	if r.bound == nil {
		return nil, errorf(CodeInvalid, "no store is bound to the registry (Bind)")
	}
	return r.bound, nil
}

// Rebuild folds the sessions of scope for the stored fold key, read-only and at
// most Config.RebuildConcurrency at a time, and writes each one's row at its
// durable head with the store's UpsertDerived. A session whose fold fails (a
// panic or a state that does not encode) or whose row the store refuses is
// marked failed for Status and the others go on; the error joins those
// failures. A session deleted meanwhile is skipped. An unregistered key is
// view/unknown-key; a fold that is not Stored, or a store that cannot write
// derived rows (or list the sessions lacking one, for the whole-store scope),
// is view/invalid.
func (r *Registry) Rebuild(ctx context.Context, key string, scope RebuildScope) error {
	b, err := r.boundStore()
	if err != nil {
		return err
	}
	d := r.byKey[key]
	switch {
	case d == nil:
		return errorf(CodeUnknownKey, "no fold %q is registered", key)
	case !d.stored:
		return errorf(CodeInvalid, "fold %q is not Stored: it has no rows to rebuild", key)
	}
	dw, ok := b.st.(store.DerivedWriter)
	if !ok {
		return errorf(CodeInvalid, "the bound store cannot write derived rows without the lease")
	}
	if len(scope.Sessions) > 0 {
		return r.rebuildAll(ctx, b, dw, d, scope.Sessions)
	}
	fr, ok := b.st.(store.FoldRows)
	if !ok {
		return errorf(CodeInvalid, "the bound store cannot list the sessions lacking a row of %q", key)
	}
	var errs []error
	after := ""
	for {
		ids, err := fr.FoldPending(ctx, d.key, d.version, after, rebuildPage)
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		if len(ids) == 0 {
			return errors.Join(errs...)
		}
		page := make([]session.ID, len(ids))
		for i, id := range ids {
			page[i] = session.ID(id)
		}
		if err := r.rebuildAll(ctx, b, dw, d, page); err != nil {
			errs = append(errs, err)
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(errs, err)...)
		}
		after = ids[len(ids)-1]
	}
}

// rebuildAll rebuilds ids, at most Config.RebuildConcurrency at once.
func (r *Registry) rebuildAll(ctx context.Context, b *binding, dw store.DerivedWriter, d *def, ids []session.ID) error {
	sem := make(chan struct{}, r.cfg.RebuildConcurrency)
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for _, id := range ids {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			wg.Wait()
			return errors.Join(append(errs, ctx.Err())...)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := r.rebuildOne(ctx, b, dw, d, id); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// rebuildOne folds d over session id up to its durable head and writes the row.
func (r *Registry) rebuildOne(ctx context.Context, b *binding, dw store.DerivedWriter, d *def, id session.ID) error {
	rs, release, err := b.open.OpenRead(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil // deleted since it was listed
	}
	if err != nil {
		return fmt.Errorf("rebuilding %q of session %q: %w", d.key, id, err)
	}
	defer release()
	// UpsertDerived writes only below the durable head, which is what another
	// process reads; a read-open's Len may also count closers folded in memory.
	at := min(rs.Durable(), rs.Len())
	if at == 0 {
		return nil
	}
	cps := r.checkpoints(ctx, rs)
	v, start, err := r.foldOne(ctx, rs, d, at, cps)
	if err != nil {
		if errors.Is(err, ErrFailed) {
			r.fail(d.key, id, at, err)
		}
		return fmt.Errorf("rebuilding %q of session %q: %w", d.key, id, err)
	}
	if start == at {
		r.clearFailure(d.key, id)
		return nil // its row already reaches the head
	}
	state, err := safe(d, func() any { return must(d.encode(v)) })
	if err != nil {
		err = fmt.Errorf("storing its state at seq %d: %w", at, err)
		r.fail(d.key, id, at, err)
		return fmt.Errorf("rebuilding %q of session %q: %w", d.key, id, err)
	}
	//lint:ignore typeassert d.encode returns a string, and safe passes its value through
	row := store.Checkpoint{Key: d.key, StateVersion: d.version, Seq: at - 1, State: state.(string)}
	n, err := dw.UpsertDerived(ctx, string(id), []store.Checkpoint{row})
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil
	case err != nil && ctx.Err() == nil:
		r.fail(d.key, id, at, fmt.Errorf("the store refused its row: %w", err))
		return fmt.Errorf("rebuilding %q of session %q: %w", d.key, id, err)
	case err != nil:
		return err
	case n == 0 && hasRowAtOrPast(cps, d, at-1):
		// A row of this version at the head that does not decode: a forward-only
		// write cannot replace it.
		err := errorf(CodeFailed, "session %q: the row of %q at version %d does not decode and is not behind the head", id, d.key, d.version)
		r.fail(d.key, id, at, err)
		return err
	}
	r.clearFailure(d.key, id)
	return nil
}

// hasRowAtOrPast reports whether cps holds a row of d's version at seq or later.
func hasRowAtOrPast(cps []store.Checkpoint, d *def, seq event.Seq) bool {
	return slices.ContainsFunc(cps, func(c store.Checkpoint) bool {
		return c.Key == d.key && c.StateVersion == d.version && c.Seq >= seq
	})
}

// foldOne is d's state over s's events [0, at), taken up from d's usable row in
// cps when there is one, and the seq its fold started at (at when the row
// already reaches the head). A fold that panics is view/failed.
func (r *Registry) foldOne(ctx context.Context, s session.ReadSession, d *def, at event.Seq, cps []store.Checkpoint) (any, event.Seq, error) {
	v, cp, ok := decodeRow(cps, d, at)
	start := event.Seq(0)
	if ok {
		start = cp.Seq + 1
	} else {
		var err error
		if v, err = safe(d, func() any { return d.init(s.Header()) }); err != nil {
			return nil, 0, err
		}
	}
	for from := start; from < at; {
		n := min(rebuildReadChunk, int(at-from))
		evs, err := s.Read(ctx, from, n)
		if err != nil {
			return nil, 0, err
		}
		if len(evs) != n || evs[0].Seq() != from {
			return nil, 0, fmt.Errorf("session %q: read %d events from %d, want %d", s.ID(), len(evs), from, n)
		}
		for _, ev := range evs {
			if !d.all && !slices.Contains(r.byType[ev.Type()], d) {
				continue
			}
			cur := v
			var changed bool
			next, err := safe(d, func() any {
				n, c := d.apply(cur, ev)
				changed = c
				return n
			})
			if err != nil {
				return nil, 0, fmt.Errorf("at seq %d: %w", ev.Seq(), err)
			}
			if changed {
				v = next
			}
		}
		from += event.Seq(n)
	}
	return v, start, nil
}

// RunRebuild runs the background rebuild as the views-rebuild duty of the bound
// store (DutyRunner): on one instance of the home, at most once per every, it
// rebuilds every stored fold over the sessions lacking a row of its version. It
// reports whether the duty ran. A run killed part-way is not recorded, so the
// next call runs again and folds only the sessions still lacking a row.
func (r *Registry) RunRebuild(ctx context.Context, every time.Duration) (bool, error) {
	b, err := r.boundStore()
	if err != nil {
		return false, err
	}
	dr, ok := b.st.(DutyRunner)
	if !ok {
		return false, errorf(CodeInvalid, "the bound store cannot run a duty")
	}
	return dr.RunDuty(ctx, DutyRebuild, every, func(ctx context.Context) error {
		var errs []error
		for _, d := range r.defs {
			if !d.stored {
				continue
			}
			if err := r.Rebuild(ctx, d.key, RebuildScope{}); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	})
}
