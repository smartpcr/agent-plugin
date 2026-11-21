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
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	msqlite "modernc.org/sqlite"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
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

// DefaultShutdownFlushTimeout bounds Close: the final barrier and the release of
// every lease this instance holds (store.Config ShutdownFlushTimeout;
// DH-ADR-STORE-3).
const DefaultShutdownFlushTimeout = 10 * time.Second

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

// WithRunDir places the liveness lock files in dir instead of the run/ directory
// beside the database (home.Layout.Run). Every process sharing the database must
// use the same directory.
func WithRunDir(dir string) Option { return func(s *Store) { s.run = dir } }

// WithShutdownFlushTimeout replaces DefaultShutdownFlushTimeout.
func WithShutdownFlushTimeout(d time.Duration) Option { return func(s *Store) { s.shutdown = d } }

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
	run      string        // the directory of the liveness lock files
	shutdown time.Duration // the bound of Close
	follow   time.Duration // a follower's poll interval
	spill    string        // <home>/spill, or "" when deletions remove rows only
	chunk    int           // events per deletion transaction
	// live is this instance's liveness lock, held from Open to Close.
	live *fsx.FileLock

	wdb   *sql.DB
	rdb   *sql.DB
	wmu   ctxMutex // guards wconn and broken; held across every lease change
	wconn *sql.Conn
	// broken is set when a rollback failed: the outcome of that transaction is
	// unknown, so the store refuses further work until reopened.
	broken error

	// cmu serializes Close from its first check to its last teardown step: a
	// concurrent Close waits for it and then sees its outcome (done, or the
	// leases still held, which it retries).
	cmu sync.Mutex

	mu      sync.Mutex // guards writers, subs, running, closed and done
	writers map[*writer]struct{}
	subs    map[*Subscription]struct{}
	running map[string]chan struct{} // a run lock per duty name
	// closed refuses new handles once Close began; done is set once Close
	// released everything and closed the pools. A Close that failed to release
	// a lease leaves done unset, so it may be called again.
	closed bool
	done   bool
}

var _ store.Store = (*Store)(nil)

// ctxMutex is a mutex whose acquisition can also give up when a context ends, so
// Close can bound its wait for a write in flight (one that may itself wait up to
// the busy timeout on another process's lock). Its zero value is unlocked.
type ctxMutex struct {
	once sync.Once
	ch   chan struct{}
}

func (m *ctxMutex) sem() chan struct{} {
	m.once.Do(func() { m.ch = make(chan struct{}, 1) })
	return m.ch
}

// Lock acquires m.
func (m *ctxMutex) Lock() { m.sem() <- struct{}{} }

// Unlock releases m.
func (m *ctxMutex) Unlock() { <-m.sem() }

// LockContext acquires m, or returns ctx's error when ctx ends first.
func (m *ctxMutex) LockContext(ctx context.Context) error {
	select {
	case m.sem() <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// OpenHome opens the database of the harness home l (<home>/dsh.db), with the
// liveness locks in <home>/run and the spill files a deletion removes in
// <home>/spill.
func OpenHome(ctx context.Context, l home.Layout, opts ...Option) (*Store, error) {
	return Open(ctx, l.DB(), append([]Option{WithRunDir(l.Run()), WithSpillDir(l.Spill())}, opts...)...)
}

// Open runs the open sequence on the database at path (DH-LIB-SQLITE-R12):
// create it owner-only when missing; check its identity and layout version
// before anything that writes, refusing a foreign or newer database unchanged;
// enter WAL; apply the numbered migrations; open the read pool; register the
// instance under its liveness lock; sweep the hosts rows of dead instances.
func Open(ctx context.Context, path string, opts ...Option) (*Store, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := &Store{path: path, reg: event.Default(), now: time.Now, kind: "dsh", busy: DefaultBusyTimeout,
		shutdown: DefaultShutdownFlushTimeout, follow: DefaultFollowInterval, chunk: DefaultDeleteChunk,
		pid: os.Getpid(), writers: map[*writer]struct{}{}, subs: map[*Subscription]struct{}{}}
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
	if s.run == "" {
		s.run = filepath.Join(filepath.Dir(path), "run")
	}
	if err := createPrivate(path); err != nil {
		return nil, err
	}
	// The lock is held before this instance is named anywhere (its hosts row,
	// a lease), so a prober never finds a running instance unlocked.
	if err := s.lockInstance(); err != nil {
		return nil, err
	}
	if err := s.openWriter(ctx); err != nil {
		s.abandon()
		return nil, err
	}
	rdb, err := sql.Open("sqlite", dsn(path, false, s.busy))
	if err != nil {
		s.abandon()
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
		s.abandon()
		return nil, err
	}
	// Housekeeping only: OpenWrite probes a recorded holder whether or not its
	// hosts row is gone, so a failed sweep (a busy database) does not fail Open.
	_ = s.sweep(ctx)
	// A deletion a dead instance left half done completes here (DH-ADR-STORE-7),
	// before Open returns: a store that cannot finish it (a spill directory it
	// cannot remove, a failing database) fails to open with that error rather
	// than run on with a deletion silently half done. The tombstone stays, so the
	// next Open retries it.
	if _, err := s.FinishDeletions(ctx); err != nil {
		// Recovery may already hold a tombstone's lease under this instance. When
		// the hosts row cannot be removed (another connection holds the write
		// lock), the liveness file must stay, unlocked: a missing file with a
		// hosts row reads as alive and would strand that tombstone for good.
		herr := s.dropHost()
		s.abandonKeeping(herr != nil)
		return nil, errors.Join(err, herr)
	}
	return s, nil
}

// dropHost removes this instance's hosts row, so an Open that fails after
// registering leaves no row behind.
func (s *Store) dropHost() error {
	return s.write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), `DELETE FROM hosts WHERE instance = ?`, s.instance)
		return err
	})
}

// abandon undoes a failed Open: the pools close and the liveness lock goes.
func (s *Store) abandon() { s.abandonKeeping(false) }

// abandonKeeping is abandon; with keep, the liveness file stays, unlocked, so the
// next prober finds this instance dead even though its hosts row remains.
func (s *Store) abandonKeeping(keep bool) {
	_ = s.closeDBs()
	_ = s.releaseLive(keep)
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
// and truncates the write-ahead log, closes both pools and releases its liveness
// lock. New handles are refused from its start; writers still open fail with
// session/closed afterwards. The wait for a write in flight, the final barrier
// and the releases are bounded by the shutdown flush timeout (DH-ADR-STORE-3),
// not by ctx, so a cancelled caller still drains; a wait or release that does
// not finish in that bound is reported with the sessions still held, and nothing
// is torn down under the write in flight. When a release fails (a busy database, say) nothing
// is closed and the error is returned: Close may be called again and retries
// the leases still held. Once it succeeded it is idempotent. Concurrent calls
// run one at a time; a call that waited returns nil when the one before it
// succeeded.
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
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.shutdown)
	defer cancel()
	var errs []error
	retry := false
	// The wait for a write in flight is bounded too. When it outlasts the bound
	// nothing is torn down under that write: the leases stay held and Close may
	// be called again.
	if err := s.wmu.LockContext(ctx); err != nil {
		return errors.Join(s.unreleased(), store.Errorf(CodeBusy,
			"shutdown: a write in flight did not finish within %s: %v", s.shutdown, err))
	}
	if s.wconn != nil && s.broken == nil {
		for _, w := range s.snapshot() {
			if err := w.closeLocked(ctx); err != nil {
				errs = append(errs, err)
			}
		}
		if len(errs) > 0 {
			errs = append([]error{s.unreleased()}, errs...)
		}
		if len(errs) == 0 {
			if err := s.writeLocked(ctx, func(tx *sql.Tx) error {
				if _, err := tx.ExecContext(ctx, `UPDATE duties SET holder = NULL WHERE holder = ?`, s.instance); err != nil {
					return err
				}
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
			// Followers end first: one inside a read transaction would pin the
			// log, and a TRUNCATE checkpoint reports that as busy, not as an error.
			s.closeSubs()
			var busy, log, done int64
			if err := s.wconn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &done); err != nil {
				errs = append(errs, translate(ctx, err))
			} else if busy != 0 {
				errs = append(errs, store.Errorf(CodeBusy,
					"shutdown: the write-ahead log was not truncated: a reader is still active (log %d, checkpointed %d)", log, done))
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
	s.closeSubs()
	if err := s.closeDBs(); err != nil {
		errs = append(errs, err)
	}
	if err := s.unlockInstance(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// unreleased names the sessions whose writers Close could not release.
func (s *Store) unreleased() error {
	var ids []string
	for _, w := range s.snapshot() {
		ids = append(ids, w.id)
	}
	slices.Sort(ids)
	return store.Errorf(CodeBusy, "shutdown: %d session(s) still held: %s", len(ids), strings.Join(ids, ", "))
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
