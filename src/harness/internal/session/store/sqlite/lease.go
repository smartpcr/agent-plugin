package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// The liveness of an instance (session-store.md, the lease, rules 1 to 3;
// DH-D-STORE-3). Every instance holds an operating-system lock on
// run/<instance>.lock from Open to Close. The kernel drops it when the process
// dies, so a recorded holder whose lock file can be locked is dead. Nothing
// expires by time.
//
// The lock is never taken on the database file: a POSIX lock on that file is
// dropped by any open-and-close of it in the same process (DH-LIB-SQLITE-R11).
//
// A holder is probed only inside a write transaction (BEGIN IMMEDIATE), which
// every process takes in turn, so two processes cannot both conclude that a
// holder is dead and both take its lease; the prober that found it dead also
// removes its lock file and hosts row before the transaction ends.

// lockPath is the liveness lock file of instance, or "" when the name could not
// be an instance id of this store (and so names no file under run/).
func (s *Store) lockPath(instance string) string {
	if instance == "" || strings.ContainsAny(instance, `/\:.`) {
		return ""
	}
	return filepath.Join(s.run, instance+".lock")
}

// lockInstance creates run/ owner-only when missing and takes this instance's
// liveness lock.
func (s *Store) lockInstance() error {
	if err := os.MkdirAll(s.run, 0o700); err != nil {
		return &store.Error{Code: CodeIO, Msg: err.Error(), Err: err}
	}
	l, err := fsx.TryLockBeyondEnd(s.lockPath(s.instance))
	if err != nil {
		return &store.Error{Code: CodeIO, Msg: "liveness lock: " + err.Error(), Err: err}
	}
	s.live = l
	return nil
}

// unlockInstance releases this instance's liveness lock and removes its file.
// The file goes after the lock: Windows does not remove an open file, and no
// prober looks for this instance any more (its leases and hosts row are gone).
func (s *Store) unlockInstance() error {
	if s.live == nil {
		return nil
	}
	err := s.live.Unlock()
	s.live = nil
	if rerr := os.Remove(s.lockPath(s.instance)); rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		err = errors.Join(err, rerr)
	}
	if err != nil {
		return &store.Error{Code: CodeIO, Msg: "liveness lock: " + err.Error(), Err: err}
	}
	return nil
}

// alive reports whether the instance named holder runs. This instance does. For
// another one, its lock file is probed: held means alive; acquired means its
// process is gone, and its lock file and hosts row are removed in tx. A name
// that is not an instance id is alive: only Force takes such a lease. It runs
// inside a write transaction only.
func (s *Store) alive(ctx context.Context, tx *sql.Tx, holder string) (bool, error) {
	if holder == s.instance {
		return true, nil
	}
	path := s.lockPath(holder)
	if path == "" {
		return true, nil
	}
	l, err := fsx.TryLockBeyondEnd(path)
	if errors.Is(err, fsx.ErrLocked) {
		return true, nil
	}
	if err != nil {
		return false, &store.Error{Code: CodeIO, Msg: "probe of " + holder + ": " + err.Error(), Err: err}
	}
	if err := l.Unlock(); err != nil {
		return false, &store.Error{Code: CodeIO, Msg: "probe of " + holder + ": " + err.Error(), Err: err}
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, &store.Error{Code: CodeIO, Msg: "probe of " + holder + ": " + err.Error(), Err: err}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM hosts WHERE instance = ?`, holder)
	return false, err
}

// claimable refuses the lease of r when its holder is alive and o does not
// force the takeover (rules 2 to 4).
func (s *Store) claimable(ctx context.Context, tx *sql.Tx, id string, r row, o store.OpenOptions) error {
	if r.sum.Holder == nil || o.Takeover == store.Force {
		return nil
	}
	live, err := s.alive(ctx, tx, r.sum.Holder.Instance)
	if err != nil {
		return err
	}
	if live {
		return &store.OwnedError{ID: id, Holder: *r.sum.Holder}
	}
	return nil
}

// sweep removes the hosts rows of dead instances (and their lock files), in one
// write transaction at Open. The leases they recorded stay: the next OpenWrite
// of each session probes its holder again and finds it dead.
func (s *Store) sweep(ctx context.Context) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		rs, err := tx.QueryContext(ctx, `SELECT instance FROM hosts WHERE instance <> ?`, s.instance)
		if err != nil {
			return err
		}
		var others []string
		for rs.Next() {
			var in string
			if err := rs.Scan(&in); err != nil {
				rs.Close()
				return err
			}
			others = append(others, in)
		}
		if err := errors.Join(rs.Err(), rs.Close()); err != nil {
			return err
		}
		for _, in := range others {
			if s.lockPath(in) == "" {
				continue
			}
			if _, err := s.alive(ctx, tx, in); err != nil {
				return err
			}
		}
		return nil
	})
}
