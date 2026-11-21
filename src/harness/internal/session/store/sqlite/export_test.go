package sqlite

import (
	"context"
	"database/sql"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// SetAfterAppend installs the crash-injection hook: fn runs after each committed
// append, before Append returns, so a re-exec'd child can exit between an append
// and its flush. It returns the restore.
func SetAfterAppend(fn func(id string, b store.Batch)) func() {
	old := afterAppend
	afterAppend = fn
	return func() { afterAppend = old }
}

// StaleWriter is a writer of session id on s in the name of holder at epoch, as a
// displaced holder (another process's, dead or alive) would still carry it. It
// takes no lease: its appends meet the guard as the real holder's would.
func StaleWriter(ctx context.Context, s *Store, id, holder string, epoch uint64) (store.Writer, error) {
	var r row
	if err := s.read(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = liveByID(ctx, tx, id)
		return err
	}); err != nil {
		return nil, err
	}
	return &writer{reader: reader{s: s, id: id, uid: r.uid, header: r.sum.Header}, epoch: epoch, holder: holder}, nil
}

// LockPath is the liveness lock file of instance under s's run directory.
func LockPath(s *Store, instance string) string { return s.lockPath(instance) }

// Synchronous is PRAGMA synchronous on the write connection, the one every
// commit runs on (2: FULL).
func (s *Store) Synchronous(ctx context.Context) (int64, error) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	var v int64
	err := s.wconn.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&v)
	return v, err
}

// TornDown reports whether the write connection and pool are closed.
func (s *Store) TornDown() bool {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.wconn == nil && s.wdb == nil
}
