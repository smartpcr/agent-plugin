package sqlite

import (
	"context"
	"database/sql"
	"errors"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// commitTx and rollbackTx end a write transaction of writeLocked. They are test
// seams (append_faults_test.go replaces them to fail a commit or a rollback);
// production never changes them.
var (
	commitTx   = (*sql.Tx).Commit
	rollbackTx = (*sql.Tx).Rollback
)

var (
	_ store.DerivedWriter    = (*Store)(nil)
	_ store.DegradedNotifier = (*Store)(nil)
	_ store.DerivedCounter   = (*writer)(nil)
)

// upsertInLease is an append's derived row: the lease holder's fold is the
// session's newest, so its row replaces the stored one.
const upsertInLease = `INSERT INTO checkpoints (session, key, state_version, seq, state)
	VALUES (?, ?, ?, ?, ?) ON CONFLICT (session, key, state_version)
	DO UPDATE SET seq = excluded.seq, state = excluded.state`

// upsertForward is UpsertDerived's row: written only below the session's next
// seq, and over a stored row only when it moves that row forward.
const upsertForward = `INSERT INTO checkpoints (session, key, state_version, seq, state)
	SELECT uid, ?, ?, ?, ? FROM sessions WHERE uid = ? AND state = 'active' AND ? < next_seq
	ON CONFLICT (session, key, state_version) DO UPDATE SET seq = excluded.seq, state = excluded.state
	WHERE excluded.seq > checkpoints.seq
	AND excluded.seq < (SELECT next_seq FROM sessions WHERE uid = checkpoints.session)`

// derivedInSavepoint writes an append's derived rows inside SAVEPOINT derived
// (DH-D-VIEW-2). A row the database refuses rolls the savepoint back, so none of
// rows is kept and the rest of the transaction (the records, the session row)
// is untouched; the refusal is returned as dropped. err is a failure the
// transaction cannot survive (the savepoint itself, or the context ending), and
// fails the append.
func derivedInSavepoint(ctx context.Context, tx *sql.Tx, uid int64, rows []store.Checkpoint) (dropped, err error) {
	if len(rows) == 0 {
		return nil, nil
	}
	if _, err := tx.ExecContext(ctx, `SAVEPOINT derived`); err != nil {
		return nil, err
	}
	for _, c := range rows {
		_, err := tx.ExecContext(ctx, upsertInLease, uid, c.Key, c.StateVersion, int64(c.Seq), c.State)
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return nil, err
		}
		if _, rerr := tx.ExecContext(ctx, `ROLLBACK TO derived`); rerr != nil {
			return nil, errors.Join(err, rerr)
		}
		if _, rerr := tx.ExecContext(ctx, `RELEASE derived`); rerr != nil {
			return nil, errors.Join(err, rerr)
		}
		return translate(ctx, err), nil
	}
	_, err = tx.ExecContext(ctx, `RELEASE derived`)
	return nil, err
}

// DerivedDropped implements store.DerivedCounter.
func (w *writer) DerivedDropped() uint64 { return w.dropped.Load() }

// OnDegraded implements store.DegradedNotifier.
func (s *Store) OnDegraded(fn func(store.Degraded)) (stop func()) { return s.notices.Subscribe(fn) }

// dropDerived counts and reports the derived rows of b an append of session id
// committed without; it runs once the append returned its lock.
func (w *writer) dropDerived(b store.Batch, cause error) {
	w.dropped.Add(uint64(len(b.Checkpoints)))
	keys := make([]string, len(b.Checkpoints))
	for i, c := range b.Checkpoints {
		keys[i] = store.DerivedKey(c)
	}
	w.s.notices.Send(store.Degraded{Session: w.id, Reason: store.DegradedDerived, Keys: keys, Err: cause})
}

// UpsertDerived implements store.DerivedWriter: in one write transaction of this
// instance, without the session's lease, each row moves its stored row forward
// or is skipped. It never touches events or the session row, so a holder's
// appends and epoch are unaffected.
func (s *Store) UpsertDerived(ctx context.Context, id string, rows []store.Checkpoint) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	for _, c := range rows {
		if c.Key == "" {
			return 0, store.Errorf(store.CodeInvalidBatch, "session %q: a derived row has no key", id)
		}
	}
	if err := s.admit(); err != nil {
		return 0, err
	}
	var n int
	err := s.write(ctx, func(tx *sql.Tx) error {
		n = 0
		r, err := liveByID(ctx, tx, id)
		if err != nil {
			return err
		}
		for _, c := range rows {
			if c.Seq > maxSeq {
				continue // past any next seq the store can hold
			}
			res, err := tx.ExecContext(ctx, upsertForward, c.Key, c.StateVersion, int64(c.Seq), c.State, r.uid, int64(c.Seq))
			if err != nil {
				return err
			}
			k, err := res.RowsAffected()
			if err != nil {
				return err
			}
			n += int(k)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}
