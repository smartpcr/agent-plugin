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

// SetAfterDeleteChunk installs the crash-injection hook of a deletion: fn runs
// after each committed chunk with the events removed so far, so a re-exec'd child
// can exit halfway through. It returns the restore.
func SetAfterDeleteChunk(fn func(id string, deleted int64)) func() {
	old := afterDeleteChunk
	afterDeleteChunk = fn
	return func() { afterDeleteChunk = old }
}

// SetRemoveAll replaces the spill-directory removal of a deletion, so a test can
// make it fail. It returns the restore.
func SetRemoveAll(fn func(path string) error) func() {
	old := removeAll
	removeAll = fn
	return func() { removeAll = old }
}

// SetAfterRetireShortlist installs fn between Retire's shortlist and its
// deletions. It returns the restore.
func SetAfterRetireShortlist(fn func(ids []string)) func() {
	old := afterRetireShortlist
	afterRetireShortlist = fn
	return func() { afterRetireShortlist = old }
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

// WriteHeld reports whether a write (or a lease change) holds s's writer mutex.
func WriteHeld(s *Store) bool {
	select {
	case s.wmu.sem() <- struct{}{}:
		s.wmu.Unlock()
		return false
	default:
		return true
	}
}

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
