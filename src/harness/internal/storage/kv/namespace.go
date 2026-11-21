package kv

import (
	"context"
	"database/sql"
	"encoding/json"
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// record is one stored row, kept as its bytes.
type record struct {
	version int
	value   string
}

// Namespace is one open namespace of records of type T. It is safe for
// concurrent use.
type Namespace[T any] struct {
	db   DB
	spec Spec[T]

	// umu serialises Update and Fresh, so snapshots are swapped and changes
	// raised in commit order.
	umu sync.Mutex

	mu    sync.RWMutex // guards rev, recs and stale
	rev   int64
	recs  map[string]record
	stale bool // a commit's outcome is unknown: Fresh reloads unconditionally

	lmu       sync.Mutex
	listeners map[int]func(Change)
	nextL     int
}

// Open registers spec's namespace in db, refusing one created with another
// authority, and loads its snapshot.
func Open[T any](ctx context.Context, db DB, spec Spec[T]) (*Namespace[T], error) {
	if db == nil || spec.Namespace == "" || spec.Version < 1 {
		return nil, herr.Newf(ReasonInvalid, "a namespace needs a database, a name and a version of at least 1")
	}
	if spec.Authority != Authoritative && spec.Authority != Derived {
		return nil, herr.Newf(ReasonInvalid, "namespace %q: authority %q", spec.Namespace, spec.Authority)
	}
	for v := range spec.Convert {
		if v < 1 || v >= spec.Version {
			return nil, herr.Newf(ReasonInvalid, "namespace %q: a converter from version %d (current %d)", spec.Namespace, v, spec.Version)
		}
	}
	if spec.Now == nil {
		spec.Now = time.Now
	}
	n := &Namespace[T]{db: db, spec: spec, listeners: map[int]func(Change){}}
	if err := db.WriteTx(ctx, func(tx *sql.Tx) error {
		return EnsureNamespace(ctx, tx, spec.Namespace, spec.Authority)
	}); err != nil {
		return nil, Classify(err)
	}
	var (
		rev  int64
		recs map[string]record
	)
	if err := db.ReadTx(ctx, func(tx *sql.Tx) error {
		var err error
		rev, recs, err = n.load(ctx, tx)
		return err
	}); err != nil {
		return nil, Classify(err)
	}
	n.rev, n.recs = rev, recs
	return n, nil
}

// Name is the namespace's name.
func (n *Namespace[T]) Name() string { return n.spec.Namespace }

// load reads the revision and every record in tx, checking each one: a newer
// version, or an older one with no converter, is kept (its read fails); an
// undecodable record fails an
// authoritative namespace and is dropped from a derived one.
func (n *Namespace[T]) load(ctx context.Context, tx *sql.Tx) (int64, map[string]record, error) {
	rev, err := Revision(ctx, tx, n.spec.Namespace)
	if err != nil {
		return 0, nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT key, version, value FROM kv WHERE namespace = ? ORDER BY key`, n.spec.Namespace)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	recs := map[string]record{}
	for rows.Next() {
		var (
			key string
			r   record
		)
		if err := rows.Scan(&key, &r.version, &r.value); err != nil {
			return 0, nil, err
		}
		if r.version <= n.spec.Version {
			if _, err := n.decode(key, r); herr.Has(err, ReasonUndecodable) {
				if n.spec.Authority == Authoritative {
					return 0, nil, err
				}
				if n.spec.OnDrop != nil {
					n.spec.OnDrop(key, err)
				}
				continue
			}
		}
		recs[key] = r
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	return rev, recs, nil
}

// decode is r as a fresh T: the current version decoded, an older one
// converted, a newer one refused.
func (n *Namespace[T]) decode(key string, r record) (T, error) {
	var v T
	switch {
	case r.version > n.spec.Version:
		return v, herr.Newf(ReasonRecordVersion, "namespace %q key %q: record version %d is newer than %d: upgrade dsh",
			n.spec.Namespace, key, r.version, n.spec.Version)
	case r.version < n.spec.Version:
		conv := n.spec.Convert[r.version]
		if conv == nil {
			return v, herr.Newf(ReasonRecordVersion, "namespace %q key %q: no converter from record version %d",
				n.spec.Namespace, key, r.version)
		}
		out, err := conv(json.RawMessage(r.value))
		if err != nil {
			return v, herr.Wrap(ReasonUndecodable, err, "namespace "+n.spec.Namespace+" key "+key)
		}
		return out, nil
	}
	if err := json.Unmarshal([]byte(r.value), &v); err != nil {
		return v, herr.Wrap(ReasonUndecodable, err, "namespace "+n.spec.Namespace+" key "+key)
	}
	return v, nil
}

// Get is a copy of key's record; ok is false when there is none. A record of a
// newer version, or of an older one with no converter, is storage/record-version.
func (n *Namespace[T]) Get(key string) (v T, ok bool, err error) {
	n.mu.RLock()
	r, ok := n.recs[key]
	n.mu.RUnlock()
	if !ok {
		return v, false, nil
	}
	v, err = n.decode(key, r)
	return v, err == nil, err
}

// Keys is every key, sorted.
func (n *Namespace[T]) Keys() []string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return slices.Sorted(maps.Keys(n.recs))
}

// Revision is the revision the snapshot reflects.
func (n *Namespace[T]) Revision() int64 {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.rev
}

// Subscribe adds fn as a listener of every Change, called after commit in
// commit order; the returned func removes it. fn must not call Update or Fresh.
func (n *Namespace[T]) Subscribe(fn func(Change)) (cancel func()) {
	n.lmu.Lock()
	id := n.nextL
	n.nextL++
	n.listeners[id] = fn
	n.lmu.Unlock()
	return func() {
		n.lmu.Lock()
		delete(n.listeners, id)
		n.lmu.Unlock()
	}
}

func (n *Namespace[T]) notify(cs []Change) {
	if len(cs) == 0 {
		return
	}
	n.lmu.Lock()
	ls := make([]func(Change), 0, len(n.listeners))
	for _, id := range slices.Sorted(maps.Keys(n.listeners)) {
		ls = append(ls, n.listeners[id])
	}
	n.lmu.Unlock()
	for _, c := range cs {
		for _, l := range ls {
			l(c)
		}
	}
}

// diff is the changes that take old to cur, in key order.
func (n *Namespace[T]) diff(old, cur map[string]record, rev int64, o Origin) []Change {
	var out []Change
	keys := slices.Sorted(maps.Keys(cur))
	for k := range old {
		if _, ok := cur[k]; !ok {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range slices.Compact(keys) {
		was, had := old[k]
		now, has := cur[k]
		switch {
		case has && (!had || was != now):
			out = append(out, Change{Namespace: n.spec.Namespace, Key: k, Op: OpPut, Revision: rev, Origin: o})
		case had && !has:
			out = append(out, Change{Namespace: n.spec.Namespace, Key: k, Op: OpDelete, Revision: rev, Origin: o})
		}
	}
	return out
}

// Fresh reloads the snapshot when the stored revision differs from the one it
// reflects (another instance wrote), raising a Foreign Change per changed key.
// A read that gates an action calls it first.
func (n *Namespace[T]) Fresh(ctx context.Context) error {
	n.umu.Lock()
	defer n.umu.Unlock()
	n.mu.RLock()
	have, stale, old := n.rev, n.stale, n.recs
	n.mu.RUnlock()
	var (
		rev     int64
		recs    map[string]record
		changed bool
	)
	if err := n.db.ReadTx(ctx, func(tx *sql.Tx) error {
		cur, err := Revision(ctx, tx, n.spec.Namespace)
		if err != nil || (cur == have && !stale) {
			return err
		}
		changed = true
		rev, recs, err = n.load(ctx, tx)
		return err
	}); err != nil {
		return Classify(err)
	}
	if !changed {
		return nil
	}
	n.mu.Lock()
	n.rev, n.recs, n.stale = rev, recs, false
	n.mu.Unlock()
	n.notify(n.diff(old, recs, rev, Foreign))
	return nil
}

// Run calls Fresh every interval until ctx ends, and returns ctx's error. A
// failed refresh is retried at the next tick.
func (n *Namespace[T]) Run(ctx context.Context, every time.Duration) error {
	if every <= 0 {
		every = time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			_ = n.Fresh(ctx)
		}
	}
}

// Txn is the view an Update callback reads and writes: the stored records with
// the callback's own writes applied.
type Txn[T any] struct {
	n      *Namespace[T]
	base   map[string]record
	staged map[string]*record // nil is a delete
	now    int64

	downgrade bool // Put and Delete may replace a newer-version record
}

// Get is key's record as this transaction sees it.
func (t *Txn[T]) Get(key string) (v T, ok bool, err error) {
	r, ok := t.lookup(key)
	if !ok {
		return v, false, nil
	}
	v, err = t.n.decode(key, r)
	return v, err == nil, err
}

func (t *Txn[T]) lookup(key string) (record, bool) {
	if s, staged := t.staged[key]; staged {
		if s == nil {
			return record{}, false
		}
		return *s, true
	}
	r, ok := t.base[key]
	return r, ok
}

// Keys is every key this transaction sees, sorted.
func (t *Txn[T]) Keys() []string {
	var out []string
	for k := range t.base {
		if s, staged := t.staged[k]; !staged || s != nil {
			out = append(out, k)
		}
	}
	for k, s := range t.staged {
		if _, inBase := t.base[k]; !inBase && s != nil {
			out = append(out, k)
		}
	}
	slices.Sort(out)
	return out
}

// AllowDowngrade lets this transaction's Put and Delete replace a record
// written by a newer build, discarding whatever that build stored.
func (t *Txn[T]) AllowDowngrade() { t.downgrade = true }

// guardNewer refuses to replace key's record when a newer build wrote it, so an
// older build sharing the database cannot silently drop fields it does not know.
func (t *Txn[T]) guardNewer(key string) error {
	if t.downgrade {
		return nil
	}
	if r, ok := t.lookup(key); ok && r.version > t.n.spec.Version {
		return herr.Newf(ReasonRecordVersion, "namespace %q key %q: record version %d is newer than %d: upgrade dsh",
			t.n.spec.Namespace, key, r.version, t.n.spec.Version)
	}
	return nil
}

// Put writes v under key at the current version. A record of a newer version is
// storage/record-version unless AllowDowngrade was called.
func (t *Txn[T]) Put(key string, v T) error {
	if key == "" {
		return herr.Newf(ReasonInvalid, "namespace %q: an empty key", t.n.spec.Namespace)
	}
	if err := t.guardNewer(key); err != nil {
		return err
	}
	b, err := json.Marshal(v)
	if err != nil {
		return herr.Wrap(ReasonInvalid, err, "namespace "+t.n.spec.Namespace+" key "+key)
	}
	t.staged[key] = &record{version: t.n.spec.Version, value: string(b)}
	return nil
}

// Delete removes key; deleting a missing key is no change. A record of a newer
// version is storage/record-version unless AllowDowngrade was called.
func (t *Txn[T]) Delete(key string) error {
	if err := t.guardNewer(key); err != nil {
		return err
	}
	if _, ok := t.lookup(key); ok {
		t.staged[key] = nil
	}
	return nil
}

// Update runs fn in one write transaction. When fn returns nil its writes, and
// the namespace's next revision, commit together; then the snapshot is swapped
// and a Local Change is raised per written key. Any error (fn's, a busy
// database, a failed commit) leaves memory as it was and raises nothing; when a
// foreign write is seen first, its Changes are raised with origin Foreign
// before the local ones.
func (n *Namespace[T]) Update(ctx context.Context, fn func(tx *Txn[T]) error) error {
	n.umu.Lock()
	defer n.umu.Unlock()
	n.mu.RLock()
	have, old := n.rev, n.recs
	n.mu.RUnlock()
	var (
		rev, seen int64
		base      map[string]record
		next      map[string]record
		wrote     bool
		began     bool
	)
	err := n.db.WriteTx(ctx, func(tx *sql.Tx) error {
		began = true
		cur, err := Revision(ctx, tx, n.spec.Namespace)
		if err != nil {
			return err
		}
		base, seen = old, cur
		if cur != have {
			if _, base, err = n.load(ctx, tx); err != nil {
				return err
			}
		}
		t := &Txn[T]{n: n, base: base, staged: map[string]*record{}, now: n.spec.Now().UnixNano()}
		if err := fn(t); err != nil {
			return err
		}
		next = maps.Clone(base)
		for _, k := range slices.Sorted(maps.Keys(t.staged)) {
			r := t.staged[k]
			if r == nil {
				if _, err := tx.ExecContext(ctx, `DELETE FROM kv WHERE namespace = ? AND key = ?`, n.spec.Namespace, k); err != nil {
					return err
				}
				delete(next, k)
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO kv (namespace, key, version, value, updated_at) VALUES (?, ?, ?, ?, ?)
				ON CONFLICT (namespace, key) DO UPDATE SET version = excluded.version, value = excluded.value, updated_at = excluded.updated_at`,
				n.spec.Namespace, k, r.version, r.value, t.now); err != nil {
				return err
			}
			next[k] = *r
		}
		rev = cur
		if len(t.staged) > 0 {
			wrote = true
			if rev, err = BumpRevision(ctx, tx, n.spec.Namespace); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if began && ctx.Err() == nil {
			// The commit may or may not have landed: reload before the next use.
			n.mu.Lock()
			n.stale = true
			n.mu.Unlock()
		}
		return Classify(err)
	}
	n.mu.Lock()
	n.rev, n.recs, n.stale = rev, next, false
	n.mu.Unlock()
	var cs []Change
	if seen != have {
		cs = n.diff(old, base, seen, Foreign)
	}
	if wrote {
		cs = append(cs, n.diff(base, next, rev, Local)...)
	}
	n.notify(cs)
	return nil
}
