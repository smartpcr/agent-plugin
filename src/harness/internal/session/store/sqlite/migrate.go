package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// The layout upgrade (DH-ADR-STORE-5 (a), session-store.md §6.3 failure table;
// M1-G-STORE-1, -2). A database of an older layout is upgraded by Open, under
// run/open.lock, in four steps:
//
//  1. Under the exclusive run/migrate.lock, every hosts row's liveness lock is
//     probed. An instance still alive (an older dsh on the same home) refuses the
//     upgrade with store/migration-blocked naming its pid, kind and start time;
//     nothing changes.
//  2. The database is copied with VACUUM INTO to
//     <home>/backup/dsh-L<from>-<unix ns>.db, outside any transaction. The copy
//     is created owner-only, synced, and checked with PRAGMA integrity_check. Any
//     failure (a full disk included) removes the partial copy and refuses the
//     open; the database is untouched.
//  3. Every pending migration and the new user_version are applied in one
//     BEGIN IMMEDIATE transaction, which probes the hosts again first (an
//     instance that started after step 1 still blocks). A failure rolls the
//     whole transaction back and refuses with store/migration-failed naming the
//     backup: the database reads exactly as before at its older layout.
//  4. The backup is kept. Nothing in the upgrade deletes it; Check lists it.
//
// A new, empty database (layout 0) has no rows to lose and no hosts to probe: it
// is created in step 3's single transaction without a backup.

// migrateLockName is the lock file under run/ held for the whole upgrade.
const migrateLockName = "migrate.lock"

// BackupDir is the directory beside dsh.db that holds the layout backups.
const BackupDir = "backup"

// Test seams (export_migrate_test.go). Outside tests they are as declared.
var (
	// layoutTarget is the layout Open upgrades to; a test caps it to build a
	// home of an older layout.
	layoutTarget = func() int { return LayoutVersion }
	// journalPragma enters the write-ahead log; a test forces another mode.
	journalPragma = `PRAGMA journal_mode = WAL`
	// vacuumInto writes the backup copy; a test makes it fail as a full disk
	// would.
	vacuumInto = func(ctx context.Context, c *sql.Conn, path string) error {
		_, err := c.ExecContext(ctx, `VACUUM INTO ?`, path)
		return err
	}
	// beforeMigration runs inside the upgrade transaction before migration v
	// (layout v to v+1); a test fails one step.
	beforeMigration func(v int) error
)

// BinaryVersion is this build as recorded in its hosts row (binary_version):
// the module version and, when the build recorded it, the VCS revision, marked
// ".dirty" for a modified tree.
func BinaryVersion() string {
	info, ok := debug.ReadBuildInfo()
	return buildVersion(info, ok)
}

func buildVersion(info *debug.BuildInfo, ok bool) string {
	if !ok || info == nil {
		return "(unknown)"
	}
	v := info.Main.Version
	if v == "" {
		v = "(devel)"
	}
	var rev string
	dirty := false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev != "" {
		v += "+" + rev
		if dirty {
			v += ".dirty"
		}
	}
	return v
}

// BinaryVersion is the build this instance records in its hosts row.
func (s *Store) BinaryVersion() string { return s.version }

// backupDir is <home>/backup.
func (s *Store) backupDir() string { return filepath.Join(filepath.Dir(s.path), BackupDir) }

// upgrade takes the database from layout from to s.target as the comment at the
// top of this file describes. It runs under the open lock with the write
// connection open and in WAL.
func (s *Store) upgrade(ctx context.Context, from int) error {
	return s.underRunLock(ctx, migrateLockName, "migrate lock", func(ctx context.Context) error {
		var backup string
		if from > 0 {
			live, _, err := s.liveHosts(ctx, s.wconn)
			if err != nil {
				return s.openErr(ctx, err)
			}
			if len(live) > 0 {
				return s.blocked(from, live)
			}
			if backup, err = s.backup(ctx, from); err != nil {
				return err
			}
		}
		var (
			dead []string
			step int
		)
		err := s.write(ctx, func(tx *sql.Tx) error {
			var now int64
			if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&now); err != nil {
				return err
			}
			if now != int64(from) {
				return store.Errorf(CodeIO, "the layout changed from %d to %d under the open lock", from, now)
			}
			if from > 0 {
				live, gone, err := s.liveHosts(ctx, tx)
				if err != nil {
					return err
				}
				if len(live) > 0 {
					return s.blocked(from, live)
				}
				for _, in := range gone {
					if _, err := tx.ExecContext(ctx, `DELETE FROM hosts WHERE instance = ?`, in); err != nil {
						return err
					}
				}
				dead = gone
			}
			for v := from; v < s.target; v++ {
				if beforeMigration != nil {
					if err := beforeMigration(v); err != nil {
						step = v + 1
						return err
					}
				}
				if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
					step = v + 1
					return err
				}
			}
			if from == 0 {
				if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA application_id = %d`, ApplicationID)); err != nil {
					return err
				}
			}
			_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, s.target))
			return err
		})
		switch {
		case err == nil:
		case errors.Is(err, &store.Error{Code: CodeMigrationBlocked}), from == 0:
			return err
		default:
			at := ""
			if step > 0 {
				at = fmt.Sprintf(" at layout step %d", step)
			}
			return &store.Error{Code: CodeMigrationFailed, Err: err, Msg: fmt.Sprintf(
				"%s: the upgrade from layout %d to %d failed%s and was rolled back; the database keeps layout %d, and the backup %s is kept: %v",
				s.path, from, s.target, at, from, backup, err)}
		}
		// The rows of the dead instances are gone with the upgrade; their lock
		// files go after it, so a rolled-back upgrade never leaves a row whose file
		// is missing (which would read as alive).
		for _, in := range dead {
			_ = os.Remove(s.lockPath(in))
		}
		return nil
	})
}

// hostRow is one hosts row of another instance.
type hostRow struct {
	instance string
	pid      int64
	kind     string
	started  int64
	version  string
}

func (h hostRow) String() string {
	return fmt.Sprintf("pid %d (kind %s, started %s)", h.pid, h.kind, time.Unix(0, h.started).UTC().Format(time.RFC3339))
}

// hostRows reads every hosts row but this instance's; binary_version only on a
// layout that has it.
func (s *Store) hostRows(ctx context.Context, q querier, withVersion bool) ([]hostRow, error) {
	cols := `instance, pid, kind, started_at, ''`
	if withVersion {
		cols = `instance, pid, kind, started_at, binary_version`
	}
	rs, err := q.QueryContext(ctx, `SELECT `+cols+` FROM hosts WHERE instance <> ? ORDER BY started_at, instance`, s.instance)
	if err != nil {
		return nil, err
	}
	var out []hostRow
	for rs.Next() {
		var h hostRow
		if err := rs.Scan(&h.instance, &h.pid, &h.kind, &h.started, &h.version); err != nil {
			rs.Close()
			return nil, err
		}
		if s.lockPath(h.instance) != "" {
			out = append(out, h)
		}
	}
	return out, errors.Join(rs.Err(), rs.Close())
}

// liveHosts probes the liveness lock of every other instance with a hosts row
// (probeLock, which changes nothing) and returns the live ones and the instances
// proven dead. A row whose lock file is missing counts as live, as it does for
// a lease (alive).
func (s *Store) liveHosts(ctx context.Context, q querier) (live []hostRow, dead []string, err error) {
	rows, err := s.hostRows(ctx, q, false)
	if err != nil {
		return nil, nil, err
	}
	for _, h := range rows {
		st, err := s.probeLock(h.instance)
		if err != nil {
			return nil, nil, err
		}
		if st == lockFree {
			dead = append(dead, h.instance)
		} else {
			live = append(live, h)
		}
	}
	return live, dead, nil
}

// blocked is the refusal of an upgrade while the instances live run.
func (s *Store) blocked(from int, live []hostRow) error {
	names := make([]string, len(live))
	for i, h := range live {
		names[i] = h.String()
	}
	return store.Errorf(CodeMigrationBlocked,
		"%s: layout %d must be upgraded to %d, but %d other dsh instance(s) on this home still run: %s; close them and open again",
		s.path, from, s.target, len(live), strings.Join(names, "; "))
}

// backup copies the database at layout from into a new owner-only file under
// <home>/backup and verifies the copy. On any failure the partial copy is
// removed and the open is refused; the database itself is never written.
func (s *Store) backup(ctx context.Context, from int) (string, error) {
	dir := s.backupDir()
	if err := home.EnsurePrivateDir(dir); err != nil {
		return "", s.backupErr(dir, &store.Error{Code: CodeIO, Msg: err.Error(), Err: err})
	}
	name := filepath.Join(dir, fmt.Sprintf("dsh-L%d-%d.db", from, time.Now().UnixNano()))
	f, err := home.CreatePrivateFile(name)
	if err != nil {
		return "", s.backupErr(name, &store.Error{Code: CodeIO, Msg: err.Error(), Err: err})
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", s.backupErr(name, &store.Error{Code: CodeIO, Msg: err.Error(), Err: err})
	}
	if err := s.fillBackup(ctx, name, from); err != nil {
		_ = os.Remove(name)
		return "", s.backupErr(name, err)
	}
	return name, nil
}

// fillBackup writes the copy into the empty file name, syncs it and its
// directory, and checks it.
func (s *Store) fillBackup(ctx context.Context, name string, from int) error {
	if err := vacuumInto(ctx, s.wconn, name); err != nil {
		return translate(ctx, err)
	}
	// VACUUM INTO does not sync what it wrote.
	f, err := os.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return &store.Error{Code: CodeIO, Msg: err.Error(), Err: err}
	}
	if err := errors.Join(f.Sync(), f.Close()); err != nil {
		return &store.Error{Code: CodeIO, Msg: err.Error(), Err: err}
	}
	if err := fsx.SyncDir(filepath.Dir(name)); err != nil {
		return &store.Error{Code: CodeIO, Msg: err.Error(), Err: err}
	}
	return verifyBackup(ctx, name, from)
}

// verifyBackup opens the copy read-only and requires integrity_check to say ok
// and the copy to carry this database's identity at layout from.
func verifyBackup(ctx context.Context, name string, from int) error {
	db, err := sql.Open("sqlite", name+"?_pragma=query_only(1)")
	if err != nil {
		return translate(ctx, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	var found []string
	rs, err := db.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return translate(ctx, err)
	}
	for rs.Next() {
		var v string
		if err := rs.Scan(&v); err != nil {
			rs.Close()
			return translate(ctx, err)
		}
		found = append(found, v)
	}
	if err := errors.Join(rs.Err(), rs.Close()); err != nil {
		return translate(ctx, err)
	}
	if len(found) != 1 || found[0] != "ok" {
		return store.Errorf(CodeCorrupt, "the copy fails integrity_check: %s", strings.Join(found, "; "))
	}
	var app, version int64
	if err := db.QueryRowContext(ctx, `SELECT (SELECT application_id FROM pragma_application_id),
		(SELECT user_version FROM pragma_user_version)`).Scan(&app, &version); err != nil {
		return translate(ctx, err)
	}
	if app != ApplicationID || version != int64(from) {
		return store.Errorf(CodeCorrupt, "the copy has application id %#x and layout %d, not %#x and %d", app, version, ApplicationID, from)
	}
	return nil
}

// backupErr is the refusal of an open whose backup failed, keeping the cause's
// code (store/disk-full for a full disk).
func (s *Store) backupErr(path string, err error) error {
	code := CodeIO
	var se *store.Error
	if errors.As(err, &se) {
		code = se.Code
	}
	return &store.Error{Code: code, Err: err, Msg: fmt.Sprintf(
		"%s: the backup before the layout upgrade failed, so the upgrade was not attempted and nothing changed (%s): %v",
		s.path, path, err)}
}

// Backups lists the layout backups under <home>/backup, oldest name first. A
// missing directory has none.
func (s *Store) Backups() ([]string, error) {
	entries, err := os.ReadDir(s.backupDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &store.Error{Code: CodeIO, Msg: err.Error(), Err: err}
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "dsh-L") && strings.HasSuffix(e.Name(), ".db") {
			out = append(out, filepath.Join(s.backupDir(), e.Name()))
		}
	}
	return out, nil
}
