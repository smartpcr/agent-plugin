package views

import (
	"context"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// The fold status (M1-G-STORE-17, session-views.md §7: "That key is failed for
// that session at its last good seq; Status and the local log name it"). The
// row counts come from the bound store (store.FoldRows), so they are the same
// in every process. Failed is what this process knows and a row cannot say:
//
//   - the fold panicked or produced a state that does not encode, in a session
//     this registry follows or folded for a rebuild;
//   - the store refused the session's row: a rebuild's UpsertDerived failed,
//     or the store sent a derived-dropped Degraded notice naming the key at its
//     version (the registry subscribes in Bind);
//   - a rebuild found a row of the current version that does not decode and is
//     not behind the head, which a forward-only write cannot replace.
//
// A refused row stops counting as failed once a later batch of the followed
// session commits a row of that key, or a rebuild writes it; a failed fold of a
// followed session counts until Forget.

// FoldStatus is how one registered fold stands across the bound store's
// sessions. Current, Behind and Pending count, by its row, every session with
// an event; a Failed session is also counted there by the row it has.
type FoldStatus struct {
	Key     string
	Version int
	// Stored is whether the fold keeps rows; a fold that does not counts only
	// Failed.
	Stored bool
	// Current is the sessions whose row of Version reaches the head.
	Current int
	// Behind is the sessions whose row of Version is older than the head.
	Behind int
	// Pending is the sessions with no row of Version (never written, or of
	// another version and not yet rebuilt): their readers see pending.
	Pending int
	// Failed is the sessions this process knows the fold failed for.
	Failed int
	// Err is why the rows could not be counted (no store bound, or the store
	// failed); the row counts are then zero.
	Err error
}

// failure is a fold failure of one session learned outside its followed
// state; upTo is the next seq of the session's fold when it was learned.
type failure struct {
	upTo event.Seq
	err  error
}

// Status is, per registered fold in registration order, its version and the
// sessions whose row is current, behind or pending, and those failed.
func (r *Registry) Status(ctx context.Context) []FoldStatus {
	r.mu.Lock()
	r.sealLocked()
	b := r.bound
	out := make([]FoldStatus, 0, len(r.defs))
	want := map[string]int{}
	for _, d := range r.defs {
		failed := map[session.ID]bool{}
		for id := range r.failures[d.key] {
			failed[id] = true
		}
		for id, t := range r.sessions {
			if t.committed != nil {
				if _, ok := t.committed.failed[d.key]; ok {
					failed[id] = true
				}
			}
		}
		out = append(out, FoldStatus{Key: d.key, Version: d.version, Stored: d.stored, Failed: len(failed)})
		if d.stored {
			want[d.key] = d.version
		}
	}
	r.mu.Unlock()
	if len(want) == 0 {
		return out
	}
	counts, err := countRows(ctx, b, want)
	byKey := map[string]store.FoldCount{}
	for _, c := range counts {
		byKey[c.Key] = c
	}
	for i := range out {
		if !out[i].Stored {
			continue
		}
		if err != nil {
			out[i].Err = err
			continue
		}
		c := byKey[out[i].Key]
		out[i].Current, out[i].Behind, out[i].Pending = c.Current, c.Behind, c.Pending
	}
	return out
}

// countRows is the bound store's row counts of want.
func countRows(ctx context.Context, b *binding, want map[string]int) ([]store.FoldCount, error) {
	if b == nil {
		return nil, errorf(CodeInvalid, "no store is bound to the registry (Bind)")
	}
	fr, ok := b.st.(store.FoldRows)
	if !ok {
		return nil, errorf(CodeInvalid, "the bound store cannot count derived rows")
	}
	return fr.FoldCounts(ctx, want)
}

// fail records that the fold of key failed for session id at upTo.
func (r *Registry) fail(key string, id session.ID, upTo event.Seq, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failLocked(key, id, upTo, err)
}

func (r *Registry) failLocked(key string, id session.ID, upTo event.Seq, err error) {
	if r.failures == nil {
		r.failures = map[string]map[session.ID]failure{}
	}
	m := r.failures[key]
	if m == nil {
		m = map[session.ID]failure{}
		r.failures[key] = m
	}
	m[id] = failure{upTo: upTo, err: err}
}

// clearFailure forgets a failure of key for id: its row was written.
func (r *Registry) clearFailure(key string, id session.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.failures[key], id)
}

// clearFailuresLocked forgets each refused-row failure of id that committed
// fold f outlives: f wrote a row of that key with its batch and ends past the
// point the failure was learned at. The caller holds r.mu.
func (r *Registry) clearFailuresLocked(id session.ID, f *folded) {
	for key, m := range r.failures {
		if fl, ok := m[id]; ok && fl.upTo < f.next && f.written[key] == f.next {
			delete(m, id)
		}
	}
}

// onDegraded is the store's Degraded subscriber: a derived-dropped notice marks
// each named fold of this registry, at its registered version, failed for the
// session, at the end of the batch this registry folded for it.
func (r *Registry) onDegraded(n store.Degraded) {
	if n.Reason != store.DegradedDerived || n.Session == "" {
		return
	}
	id := session.ID(n.Session)
	r.mu.Lock()
	defer r.mu.Unlock()
	var upTo event.Seq
	if t := r.sessions[id]; t != nil {
		switch {
		case t.pending != nil:
			upTo = t.pending.next
		case t.committed != nil:
			upTo = t.committed.next
		}
	}
	for _, k := range n.Keys {
		key, version, ok := parseDerivedKey(k)
		if d := r.byKey[key]; !ok || d == nil || d.version != version {
			continue
		}
		r.failLocked(key, id, upTo, errorf(CodeFailed, "session %q: the store dropped the row %s: %v", id, k, n.Err))
	}
}

// parseDerivedKey splits store.DerivedKey's "<key>@<state version>".
func parseDerivedKey(s string) (string, int, bool) {
	i := strings.LastIndexByte(s, '@')
	if i <= 0 {
		return "", 0, false
	}
	v, err := strconv.Atoi(s[i+1:])
	return s[:i], v, err == nil
}
