package sqlite

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"sync"
	"time"

	msqlite "modernc.org/sqlite"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

// The codes this store raises besides the contract's (DH-LIB-SQLITE-R7): every
// driver error is translated once, here, into one of them; no driver error type
// crosses the store interface.
const (
	// CodeForeign refuses a file that is not a dsh database: another
	// application's id, an unrelated schema, or not SQLite at all.
	CodeForeign store.Code = "store/foreign-database"
	// CodeNewerLayout refuses a database a newer build migrated.
	CodeNewerLayout store.Code = "store/newer-layout"
	// CodeNoWAL refuses a home where the write-ahead log cannot be entered
	// (a network filesystem, for one).
	CodeNoWAL store.Code = "store/no-wal"
	// CodeUnusable is every call after a failed rollback left the outcome of a
	// transaction unknown; the store must be reopened.
	CodeUnusable store.Code = "store/unusable"

	CodeBusy        store.Code = "store/busy"
	CodeConstraint  store.Code = "store/constraint"
	CodeCorrupt     store.Code = "store/corrupt"
	CodeFull        store.Code = "store/disk-full"
	CodeReadOnly    store.Code = "store/read-only"
	CodeInterrupted store.Code = "store/interrupted"
	CodeIO          store.Code = "store/io"
)

// Result codes of SQLite (primary codes, the low byte of an extended code).
const (
	rcInterrupt  = 9
	rcBusy       = 5
	rcLocked     = 6
	rcReadOnly   = 8
	rcIOErr      = 10
	rcCorrupt    = 11
	rcFull       = 13
	rcConstraint = 19
	rcNotADB     = 26
)

// DefaultBusyTimeout bounds how long a statement waits for another process's
// lock before it fails with CodeBusy (DH-LIB-SQLITE-R4, store.Config
// BusyTimeout); it also bounds the first switch into WAL, which needs the
// database to itself.
const DefaultBusyTimeout = 5 * time.Second

// syncFull is PRAGMA synchronous = FULL: every commit syncs the write-ahead log
// before it returns.
const syncFull = 2

// readPool bounds the read connections: each carries its own page cache.
const readPool = 4

// translate is the one mapping from a driver error to a harness error. A
// cancelled or expired context is returned as the context's error.
func translate(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var se *msqlite.Error
	if !errors.As(err, &se) {
		if harness(err) {
			return err
		}
		return &store.Error{Code: CodeIO, Msg: err.Error()}
	}
	code := CodeIO
	switch se.Code() & 0xff {
	case rcBusy, rcLocked:
		code = CodeBusy
	case rcConstraint:
		code = CodeConstraint
	case rcCorrupt:
		code = CodeCorrupt
	case rcNotADB:
		code = CodeForeign
	case rcFull:
		code = CodeFull
	case rcReadOnly:
		code = CodeReadOnly
	case rcInterrupt:
		code = CodeInterrupted
	case rcIOErr:
		code = CodeIO
	}
	// The message only: the driver's type stays inside this package.
	return &store.Error{Code: code, Msg: se.Error()}
}

// harness reports whether err is already one of the harness's own errors (the
// contract's, or an event registry's refusal), which pass through unchanged.
func harness(err error) bool {
	var (
		se      *store.Error
		owned   *store.OwnedError
		unknown *event.UnknownTypeError
		newer   *event.NewerPayloadError
	)
	return errors.As(err, &se) || errors.As(err, &owned) || errors.As(err, &unknown) || errors.As(err, &newer)
}

// Option configures a Store.
type Option func(*Store)

// WithRegistry judges appends and opens against r instead of event.Default().
func WithRegistry(r *event.Registry) Option { return func(s *Store) { s.reg = r } }

// WithClock sets the clock that stamps CreatedAt when a header has none,
// LastEventAt when a batch's records have no time, and lease acquisition.
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// WithKind names the kind of process this instance is (cli, server, ...), shown
// as a lease holder's Kind.
func WithKind(kind string) Option { return func(s *Store) { s.kind = kind } }

// WithBusyTimeout replaces DefaultBusyTimeout.
func WithBusyTimeout(d time.Duration) Option { return func(s *Store) { s.busy = d } }

// Store is the SQLite session store: one database per home (DH-D-STORE-2). It
// owns one write connection, used under a mutex as the writer of the whole
// instance, and a bounded read pool.
//
// Every commit runs with synchronous=FULL, so a committed event is a durable
// one (session-store.md, "What durable means"): another process, which sees
// only committed rows, sees only durable events, and Summary.Durable is the
// committed next seq in every instance.
//
// Lock order: cmu, then wmu, then mu. Acquiring a lease, registering its writer and
// releasing it all happen under wmu, and Close takes its writer snapshot under
// wmu, so no lease is acquired that Close does not release.
type Store struct {
	path     string
	reg      *event.Registry
	now      func() time.Time
	kind     string
	busy     time.Duration
	instance string
	pid      int

	wdb   *sql.DB
	rdb   *sql.DB
	wmu   sync.Mutex // guards wconn and broken; held across every lease change
	wconn *sql.Conn
	// broken is set when a rollback failed: the outcome of that transaction is
	// unknown, so the store refuses further work until reopened.
	broken error

	// cmu serializes Close from its first check to its last teardown step: a
	// concurrent Close waits for it and then sees its outcome (done, or the
	// leases still held, which it retries).
	cmu sync.Mutex

	mu      sync.Mutex // guards writers, closed and done
	writers map[*writer]struct{}
	// closed refuses new handles once Close began; done is set once Close
	// released everything and closed the pools. A Close that failed to release
	// a lease leaves done unset, so it may be called again.
	closed bool
	done   bool
}

var _ store.Store = (*Store)(nil)

// OpenHome opens the database of the harness home l (<home>/dsh.db).
func OpenHome(ctx context.Context, l home.Layout, opts ...Option) (*Store, error) {
	return Open(ctx, l.DB(), opts...)
}

// Open runs the open sequence on the database at path (DH-LIB-SQLITE-R12):
// create it owner-only when missing; check its identity and layout version
// before anything that writes, refusing a foreign or newer database unchanged;
// enter WAL; apply the numbered migrations; open the read pool; register the
// instance.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &Store{path: path, reg: event.Default(), now: time.Now, kind: "dsh", busy: DefaultBusyTimeout,
		pid: os.Getpid(), writers: map[*writer]struct{}{}}
	for _, o := range opts {
		o(s)
	}
	var id [6]byte
	if _, err := rand.Read(id[:]); err != nil {
		return nil, fmt.Errorf("sqlite: instance id: %w", err)
	}
	s.instance = fmt.Sprintf("%d-%s", s.pid, hex.EncodeToString(id[:]))
	if strings.ContainsAny(path, "?#") {
		return nil, store.Errorf(CodeIO, "database path %q holds a URI delimiter", path)
	}
	if err := createPrivate(path); err != nil {
		return nil, err
	}
	if err := s.openWriter(ctx); err != nil {
		s.closeDBs()
		return nil, err
	}
	rdb, err := sql.Open("sqlite", dsn(path, false, s.busy))
	if err != nil {
		s.closeDBs()
		return nil, translate(ctx, err)
	}
	rdb.SetMaxOpenConns(readPool)
	rdb.SetMaxIdleConns(readPool)
	s.rdb = rdb
	if err := s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO hosts (instance, pid, kind, started_at) VALUES (?, ?, ?, ?)`,
			s.instance, s.pid, s.kind, s.now().UnixNano())
		return err
	}); err != nil {
		s.closeDBs()
		return nil, err
	}
	return s, nil
}

// createPrivate creates path owner-only when it does not exist. The file is
// never opened through os again while the store is open (DH-LIB-SQLITE-R11).
func createPrivate(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return &store.Error{Code: CodeIO, Msg: err.Error(), Err: err}
	}
	if err := f.Close(); err != nil {
		return &store.Error{Code: CodeIO, Msg: err.Error(), Err: err}
	}
	return nil
}

// dsn is the connection string of the write connection or of a read
// connection: the one place the driver's keys appear. synchronous=FULL makes
// every commit sync the write-ahead log before it returns, so nothing another
// process can read is less than durable.
func dsn(path string, write bool, busy time.Duration) string {
	q := fmt.Sprintf("?_pragma=busy_timeout(%d)&_pragma=foreign_keys(1)", busy.Milliseconds())
	if write {
		return path + q + "&_pragma=synchronous(FULL)&_txlock=immediate"
	}
	return path + q + "&_pragma=query_only(1)"
}

// openWriter opens the one write connection, checks identity and layout, enters
// WAL and migrates.
func (s *Store) openWriter(ctx context.Context) error {
	wdb, err := sql.Open("sqlite", dsn(s.path, true, s.busy))
	if err != nil {
		return translate(ctx, err)
	}
	wdb.SetMaxOpenConns(1)
	wdb.SetMaxIdleConns(1)
	wdb.SetConnMaxLifetime(0)
	wdb.SetConnMaxIdleTime(0)
	s.wdb = wdb
	conn, err := wdb.Conn(ctx)
	if err != nil {
		return s.openErr(ctx, err)
	}
	s.wconn = conn
	version, err := s.identify(ctx)
	if err != nil {
		return err
	}
	var mode string
	if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&mode); err != nil {
		return s.openErr(ctx, err)
	}
	if !strings.EqualFold(mode, "wal") {
		return store.Errorf(CodeNoWAL, "%s: the write-ahead log cannot be entered (journal mode stays %q)", s.path, mode)
	}
	var sync int64
	if err := conn.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&sync); err != nil {
		return s.openErr(ctx, err)
	}
	if sync != syncFull {
		return store.Errorf(CodeIO, "%s: the write connection runs synchronous=%d, not FULL", s.path, sync)
	}
	for v := version; v < LayoutVersion; v++ {
		if err := s.migrate(ctx, v); err != nil {
			return err
		}
	}
	return nil
}

// openErr translates an open failure, naming the file.
func (s *Store) openErr(ctx context.Context, err error) error {
	err = translate(ctx, err)
	var se *store.Error
	if errors.As(err, &se) {
		se.Msg = s.path + ": " + se.Msg
	}
	return err
}

// identify reads the identity and layout version without writing: a foreign
// or newer database is refused before any pragma that writes.
func (s *Store) identify(ctx context.Context) (int, error) {
	var app, version, objects int64
	if err := s.wconn.QueryRowContext(ctx, `PRAGMA application_id`).Scan(&app); err != nil {
		return 0, s.openErr(ctx, err)
	}
	if err := s.wconn.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return 0, s.openErr(ctx, err)
	}
	if err := s.wconn.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema`).Scan(&objects); err != nil {
		return 0, s.openErr(ctx, err)
	}
	switch {
	case app == 0 && version == 0 && objects == 0:
		return 0, nil // a new, empty database
	case app != ApplicationID:
		return 0, store.Errorf(CodeForeign, "%s is not a dsh database (application id %#x)", s.path, app)
	case version > int64(LayoutVersion):
		return 0, store.Errorf(CodeNewerLayout, "%s has layout %d; this build knows layouts up to %d: upgrade dsh", s.path, version, LayoutVersion)
	}
	return int(version), nil
}

// migrate applies migration v (layout v to v+1) in one write transaction,
// unless another process applied it first.
func (s *Store) migrate(ctx context.Context, v int) error {
	return s.write(ctx, func(tx *sql.Tx) error {
		var now int64
		if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&now); err != nil {
			return err
		}
		if now > int64(v) {
			return nil
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			return err
		}
		if v == 0 {
			if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA application_id = %d`, ApplicationID)); err != nil {
				return err
			}
		}
		_, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, v+1))
		return err
	})
}

// write runs fn in one write transaction (BEGIN IMMEDIATE) on the write
// connection and commits it. Any error rolls the transaction back; a failed
// rollback makes the store unusable.
func (s *Store) write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.writeLocked(ctx, fn)
}

// writeLocked is write with s.wmu held.
func (s *Store) writeLocked(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if s.broken != nil {
		return &store.Error{Code: CodeUnusable, Msg: s.broken.Error()}
	}
	if s.wconn == nil {
		return store.Errorf(store.CodeClosed, "the store is closed")
	}
	tx, err := s.wconn.BeginTx(ctx, nil)
	if err != nil {
		return translate(ctx, err)
	}
	if err := fn(tx); err != nil {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			s.broken = fmt.Errorf("rollback failed after %v: %v", err, rerr)
		}
		return translate(ctx, err)
	}
	if err := tx.Commit(); err != nil {
		return translate(ctx, err)
	}
	return nil
}

// read runs fn in one read transaction on the read pool, so it sees one
// consistent snapshot (DH-LIB-SQLITE-R3).
func (s *Store) read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed || s.rdb == nil {
		return store.Errorf(store.CodeClosed, "the store is closed")
	}
	tx, err := s.rdb.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return translate(ctx, err)
	}
	defer tx.Rollback() //nolint:errcheck // a read transaction has nothing to keep
	if err := fn(tx); err != nil {
		return translate(ctx, err)
	}
	return nil
}

// barrier returns once every commit of this instance is durable. Each commit
// synced the write-ahead log before it returned (synchronous=FULL), so holding
// wmu, which orders the barrier after any commit in flight, is enough
// (DH-LIB-SQLITE-R2).
func (s *Store) barrier(ctx context.Context) error {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.barrierLocked(ctx)
}

func (s *Store) barrierLocked(ctx context.Context) error {
	if s.broken != nil {
		return &store.Error{Code: CodeUnusable, Msg: s.broken.Error()}
	}
	if s.wconn == nil {
		return store.Errorf(store.CodeClosed, "the store is closed")
	}
	return ctx.Err()
}

// admit refuses a new writer once Close began. It runs with wmu held, and the
// writer is registered before wmu is released, so Close, which snapshots the
// writers under wmu, always sees the lease it must release.
func (s *Store) admit() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return store.Errorf(store.CodeClosed, "the store is closed")
	}
	return nil
}

// Path is the database file.
func (s *Store) Path() string { return s.path }

// Instance is this store's instance id, the lease holder name of its writers.
func (s *Store) Instance() string { return s.instance }

// Close releases the leases this instance holds, unregisters it, checkpoints
// and truncates the write-ahead log and closes both pools. New handles are
// refused from its start; writers still open fail with session/closed
// afterwards. When a release fails (a busy database, say) nothing is closed
// and the error is returned: Close may be called again and retries the leases
// still held. Once it succeeded it is idempotent. Concurrent calls run one at
// a time; a call that waited returns nil when the one before it succeeded.
func (s *Store) Close(ctx context.Context) error {
	s.cmu.Lock()
	defer s.cmu.Unlock()
	s.mu.Lock()
	if s.done {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	// A release must not be abandoned half-way because the caller's context
	// ended; the busy timeout bounds each statement.
	ctx = context.WithoutCancel(ctx)
	var errs []error
	retry := false
	s.wmu.Lock()
	if s.wconn != nil && s.broken == nil {
		for _, w := range s.snapshot() {
			if err := w.closeLocked(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) == 0 {
			if err := s.writeLocked(ctx, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `DELETE FROM hosts WHERE instance = ?`, s.instance)
				return err
			}); err != nil {
				errs = append(errs, err)
			}
		}
		// A failed rollback on the way makes the store unusable: nothing more
		// can be released through it, so it closes.
		retry = len(errs) > 0 && s.broken == nil
		if !retry {
			if _, err := s.wconn.ExecContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
				errs = append(errs, translate(ctx, err))
			}
		}
	}
	s.wmu.Unlock()
	if retry {
		return errors.Join(errs...)
	}
	s.mu.Lock()
	s.done = true
	s.mu.Unlock()
	if err := s.closeDBs(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// snapshot is the writers registered now.
func (s *Store) snapshot() []*writer {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := make([]*writer, 0, len(s.writers))
	for w := range s.writers {
		ws = append(ws, w)
	}
	return ws
}

// closeDBs closes the read pool, then the write connection and pool. The read
// pool's pointer stays set: a read racing Close fails on the closed pool.
func (s *Store) closeDBs() error {
	var errs []error
	if s.rdb != nil {
		errs = append(errs, s.rdb.Close())
	}
	s.wmu.Lock()
	if s.wconn != nil {
		errs = append(errs, s.wconn.Close())
		s.wconn = nil
	}
	if s.wdb != nil {
		errs = append(errs, s.wdb.Close())
		s.wdb = nil
	}
	s.wmu.Unlock()
	for i, err := range errs {
		if err != nil {
			errs[i] = translate(nil, err)
		}
	}
	return errors.Join(errs...)
}
