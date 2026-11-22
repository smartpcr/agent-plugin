package sqlite

import (
	"context"
	"database/sql"
	"runtime"
	"sync"
	"testing"

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

// WriteHeldSignal returns a channel closed the first time, from now on, that a
// write (or a lease change, or Close) takes s's writer mutex. Install it before
// starting the write it waits for, while nothing else uses s.
func WriteHeldSignal(s *Store) <-chan struct{} {
	ch := make(chan struct{})
	s.wmu.held = sync.OnceFunc(func() { close(ch) })
	return ch
}

// busyHandlerFunc is the driver's busy callback (modernc.org/sqlite busy.go): it
// runs while a statement waits, under busy_timeout, for another connection's lock,
// and reads the call's context there.
const busyHandlerFunc = "modernc.org/sqlite.busyHandler"

// BusyWaitSignal returns a context for a store call and a channel closed once that
// call has entered the driver's busy wait: the busy handler asks the context of the
// call in progress for Done before each of its waits, and the returned context
// notices that caller. The context is cancelled when the test ends.
func BusyWaitSignal(t testing.TB, parent context.Context) (context.Context, <-chan struct{}) {
	ctx, cancel := context.WithCancel(parent)
	t.Cleanup(cancel)
	ch := make(chan struct{})
	return busyCtx{Context: ctx, entered: sync.OnceFunc(func() { close(ch) })}, ch
}

type busyCtx struct {
	context.Context
	entered func()
}

func (c busyCtx) Done() <-chan struct{} {
	var pcs [8]uintptr
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs[:])])
	for {
		f, more := frames.Next()
		if f.Function == busyHandlerFunc {
			c.entered()
			break
		}
		if !more {
			break
		}
	}
	return c.Context.Done()
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
