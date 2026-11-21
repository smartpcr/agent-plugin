package sqlite

import (
	"context"
	"database/sql"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// reader is a handle on one session, bound to its uid: a session deleted and
// re-created under the same id is another uid, so the handle reads
// session/deleted.
type reader struct {
	s      *Store
	id     string
	uid    int64
	header store.Header

	mu     sync.Mutex
	closed bool
}

func (r *reader) Header() store.Header { return r.header }

func (r *reader) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// open is the guard of every read: the handle is open and its session lives.
func (r *reader) open(ctx context.Context, tx *sql.Tx) (row, error) {
	if r.isClosed() {
		return row{}, store.Errorf(store.CodeClosed, "reader of %q is closed", r.id)
	}
	return liveByUID(ctx, tx, r.uid, r.id)
}

func (r *reader) Read(ctx context.Context, from store.Seq, limit int, o store.ReadOptions) ([]store.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, store.Errorf(store.CodeInvalidBoundary, "read limit %d is negative", limit)
	}
	var out []store.Record
	err := r.s.read(ctx, func(tx *sql.Tx) error {
		if _, err := r.open(ctx, tx); err != nil {
			return err
		}
		if from > maxSeq {
			return nil
		}
		n := int64(limit)
		if n == 0 {
			n = -1
		}
		var err error
		out, err = selectRecords(ctx, tx, r.uid, from, n, o.WithAux)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *reader) ReadSeqs(ctx context.Context, seqs []store.Seq, o store.ReadOptions) ([]store.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]store.Record, 0, len(seqs))
	err := r.s.read(ctx, func(tx *sql.Tx) error {
		ss, err := r.open(ctx, tx)
		if err != nil {
			return err
		}
		for _, q := range seqs {
			if q >= ss.sum.NextSeq {
				return store.Errorf(store.CodeInvalidBoundary, "session %q has no seq %d", r.id, q)
			}
			recs, err := selectRecords(ctx, tx, r.uid, q, 1, o.WithAux)
			if err != nil {
				return err
			}
			if len(recs) != 1 || recs[0].Seq != q {
				return store.Errorf(CodeCorrupt, "session %q: seq %d is missing below next seq %d", r.id, q, ss.sum.NextSeq)
			}
			out = append(out, recs[0])
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *reader) Checkpoints(ctx context.Context) ([]store.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var out []store.Checkpoint
	err := r.s.read(ctx, func(tx *sql.Tx) error {
		if _, err := r.open(ctx, tx); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT key, state_version, seq, state FROM checkpoints
			WHERE session = ? ORDER BY key, state_version`, r.uid)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var (
				c   store.Checkpoint
				seq int64
			)
			if err := rows.Scan(&c.Key, &c.StateVersion, &seq, &c.State); err != nil {
				return err
			}
			c.Seq = store.Seq(seq)
			out = append(out, c)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (r *reader) Close(context.Context) error {
	r.mu.Lock()
	r.closed = true
	r.mu.Unlock()
	return nil
}

// writer holds the lease of one session at one epoch, in the name of holder
// (this instance's id).
type writer struct {
	reader
	epoch  uint64
	holder string
}

// afterAppend, when set, runs after each committed append, before Append
// returns. It is nil in production; the crash tests set it (export_test.go) to
// end a re-exec'd child between an append and its flush.
var afterAppend func(id string, b store.Batch)

var _ store.Writer = (*writer)(nil)

func (w *writer) Epoch() uint64 { return w.epoch }

// check is the guard of an append or flush: the writer is open, its session
// lives and this instance still holds the lease at the writer's epoch.
func (w *writer) check(ctx context.Context, q querier) (row, error) {
	if w.isClosed() {
		return row{}, store.Errorf(store.CodeClosed, "writer of %q is closed", w.id)
	}
	r, err := liveByUID(ctx, q, w.uid, w.id)
	if err != nil {
		return row{}, err
	}
	if r.sum.Holder == nil || r.sum.Holder.Instance != w.holder || r.sum.LeaseEpoch != w.epoch {
		return row{}, store.Errorf(store.CodeLeaseLost, "session %q: epoch %d was taken over (now %d)", w.id, w.epoch, r.sum.LeaseEpoch)
	}
	return r, nil
}

// Append writes the batch in one transaction: every record and checkpoint, or
// none of them. The commit is synced before Append returns (synchronous=FULL),
// so another process never reads a record that is not durable.
func (w *writer) Append(ctx context.Context, b store.Batch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := w.s
	s.wmu.Lock()
	defer s.wmu.Unlock()
	err := s.writeLocked(ctx, func(tx *sql.Tx) error {
		ss, err := w.check(ctx, tx)
		if err != nil {
			return err
		}
		next := ss.sum.NextSeq
		if b.First != next {
			return store.Errorf(store.CodeSeqConflict, "session %q: batch starts at %d, next seq is %d", w.id, b.First, next)
		}
		var last int64
		if len(b.Records) > 0 && next > 0 {
			if err := tx.QueryRowContext(ctx, `SELECT time FROM events WHERE session = ? AND seq = ?`,
				w.uid, int64(next-1)).Scan(&last); err != nil {
				return err
			}
		}
		for i, rec := range b.Records {
			if rec.Seq != b.First+store.Seq(i) {
				return store.Errorf(store.CodeSeqConflict, "session %q: record %d of the batch has seq %d, want %d", w.id, i, rec.Seq, b.First+store.Seq(i))
			}
			if rec.Type == "" {
				return store.Errorf(store.CodeInvalidBatch, "session %q: record at seq %d has no type", w.id, rec.Seq)
			}
			if err := store.Admit(s.reg, rec); err != nil {
				return err
			}
			if rec.Time < last {
				return store.Errorf(store.CodeInvalidBatch, "session %q: record at seq %d goes back in time", w.id, rec.Seq)
			}
			last = rec.Time
		}
		total := next + store.Seq(len(b.Records))
		for _, c := range b.Checkpoints {
			if c.Key == "" || c.Seq >= total {
				return store.Errorf(store.CodeInvalidBatch, "session %q: checkpoint %q at seq %d is outside the log", w.id, c.Key, c.Seq)
			}
		}
		if len(b.Records) == 0 && len(b.Checkpoints) == 0 {
			return nil
		}
		size, err := insertRecords(ctx, tx, s.reg, w.uid, b.Records)
		if err != nil {
			return err
		}
		for _, c := range b.Checkpoints {
			if _, err := tx.ExecContext(ctx, `INSERT INTO checkpoints (session, key, state_version, seq, state)
				VALUES (?, ?, ?, ?, ?) ON CONFLICT (session, key, state_version)
				DO UPDATE SET seq = excluded.seq, state = excluded.state`,
				w.uid, c.Key, c.StateVersion, int64(c.Seq), c.State); err != nil {
				return err
			}
		}
		lastAt := ss.last
		if n := len(b.Records); n > 0 {
			lastAt = s.eventNanos(b.Records[n-1].Time)
		}
		res, err := tx.ExecContext(ctx, `UPDATE sessions SET next_seq = ?, last_event_at = ?, last_prompt_at = ?, bytes = bytes + ?
			WHERE uid = ? AND next_seq = ? AND lease_holder = ? AND lease_epoch = ? AND state = 'active'`,
			int64(total), lastAt, promptNanos(ss.prompt, b.Records), size, w.uid, int64(next), w.holder, int64(w.epoch))
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return store.Errorf(store.CodeLeaseLost, "session %q changed under the append", w.id)
		}
		return nil
	})
	if err == nil && afterAppend != nil {
		afterAppend(w.id, b)
	}
	return err
}

// Flush returns once every commit of this instance, this writer's among them,
// is durable.
func (w *writer) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s := w.s
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.wconn == nil {
		return store.Errorf(store.CodeClosed, "the store is closed")
	}
	if _, err := w.check(ctx, s.wconn); err != nil {
		return translate(ctx, err)
	}
	return s.barrierLocked(ctx)
}

// Close releases the lease and keeps the epoch. A writer that lost its lease
// closes without error and changes nothing. When the release fails (a busy
// database, say) the writer stays open and registered and the error is
// returned: Close may be called again, and Store.Close still releases it.
func (w *writer) Close(ctx context.Context) error {
	s := w.s
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return w.closeLocked(context.WithoutCancel(ctx))
}

// closeLocked is Close with s.wmu held. The writer is marked closed and
// unregistered only once the release committed.
func (w *writer) closeLocked(ctx context.Context) error {
	if w.isClosed() {
		return nil
	}
	s := w.s
	if s.wconn != nil {
		if err := s.barrierLocked(ctx); err != nil {
			return err
		}
		if err := s.writeLocked(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE sessions SET lease_holder = NULL, lease_pid = 0, lease_kind = '',
				lease_since = 0 WHERE uid = ? AND lease_holder = ? AND lease_epoch = ?`, w.uid, w.holder, int64(w.epoch))
			return err
		}); err != nil {
			return err
		}
	}
	w.mu.Lock()
	w.closed = true
	w.mu.Unlock()
	s.untrack(w)
	return nil
}
