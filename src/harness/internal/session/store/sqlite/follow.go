package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// DefaultFollowInterval is how often a follower polls the change counter.
const DefaultFollowInterval = 250 * time.Millisecond

// followPage is the number of events a follower reads per query.
const followPage = 256

// WithFollowInterval replaces DefaultFollowInterval.
func WithFollowInterval(d time.Duration) Option { return func(s *Store) { s.follow = d } }

// Subscription is one Follow: the events committed to a session, by any process,
// from a position on, delivered once and in seq order.
type Subscription struct {
	events chan store.Record
	cancel context.CancelFunc
	done   chan struct{}
	err    error // set before events is closed
}

// Events delivers the records in seq order. It is closed when the subscription
// ends; Err then says why.
func (sub *Subscription) Events() <-chan store.Record { return sub.events }

// Err is why Events was closed: nil after Close or when the store closed, the
// context's error, session/deleted, or a store error.
func (sub *Subscription) Err() error {
	select {
	case <-sub.done:
		return sub.err
	default:
		return nil
	}
}

// Close ends the subscription and waits for its poller to stop.
func (sub *Subscription) Close() {
	sub.cancel()
	<-sub.done
}

// Follow delivers the events of session id from seq from on, including those
// another process commits (DH-ADR-STORE-6, DH-D-STORE-8). While the subscription
// lives it holds one connection of its own (not one of the read pool) and polls
// PRAGMA data_version on it every follow interval; when the counter moved it reads the rows past the last
// delivered seq in short read transactions. No read transaction stays open between
// polls, so a follower never holds back a checkpoint of the write-ahead log. A
// follower lags a commit by at most about one interval and sees only durable
// events (every commit is synced). The subscription ends with ctx, Close, the
// store's Close, or the session's deletion (session/deleted).
func (s *Store) Follow(ctx context.Context, id string, from store.Seq) (*Subscription, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var r row
	if err := s.read(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = liveByID(ctx, tx, id)
		return err
	}); err != nil {
		return nil, err
	}
	// A follower's connection is its own, outside the bounded read pool, so any
	// number of followers never starve Stat, List, OpenRead or Export.
	fdb, err := sql.Open("sqlite", dsn(s.path, false, s.busy))
	if err != nil {
		return nil, translate(ctx, err)
	}
	fdb.SetMaxOpenConns(1)
	conn, err := fdb.Conn(ctx)
	if err != nil {
		_ = fdb.Close()
		return nil, translate(ctx, err)
	}
	release := func() {
		_ = conn.Close()
		_ = fdb.Close()
	}
	fctx, cancel := context.WithCancel(ctx)
	sub := &Subscription{events: make(chan store.Record), cancel: cancel, done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		cancel()
		release()
		return nil, store.Errorf(store.CodeClosed, "the store is closed")
	}
	s.subs[sub] = struct{}{}
	s.mu.Unlock()
	go func() {
		err := s.poll(fctx, conn, sub, r.uid, id, from)
		release()
		s.mu.Lock()
		delete(s.subs, sub)
		s.mu.Unlock()
		if errors.Is(err, context.Canceled) && ctx.Err() == nil {
			err = nil // closed by Close, not by the caller's context
		}
		sub.err = err
		close(sub.events)
		close(sub.done)
	}()
	return sub, nil
}

// poll is the follower's loop. The first read runs at once; after that a read
// runs only when data_version moved.
func (s *Store) poll(ctx context.Context, conn *sql.Conn, sub *Subscription, uid int64, id string, from store.Seq) error {
	interval := s.follow
	if interval <= 0 {
		interval = DefaultFollowInterval
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	var version int64 = -1
	for {
		v, err := dataVersion(ctx, conn)
		if err != nil {
			return err
		}
		if v != version {
			version = v
			for {
				recs, err := followRead(ctx, conn, uid, id, from)
				if err != nil {
					return err
				}
				for _, rec := range recs {
					select {
					case sub.events <- rec:
					case <-ctx.Done():
						return ctx.Err()
					}
					from = rec.Seq + 1
				}
				if len(recs) < followPage {
					break
				}
			}
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func dataVersion(ctx context.Context, conn *sql.Conn) (int64, error) {
	var v int64
	if err := conn.QueryRowContext(ctx, `PRAGMA data_version`).Scan(&v); err != nil {
		return 0, translate(ctx, err)
	}
	return v, nil
}

// followRead reads one page past from in one short read transaction, which ends
// before it returns.
func followRead(ctx context.Context, conn *sql.Conn, uid int64, id string, from store.Seq) ([]store.Record, error) {
	if from > maxSeq {
		return nil, nil
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, translate(ctx, err)
	}
	defer tx.Rollback() //nolint:errcheck // a read transaction has nothing to keep
	if _, err := liveByUID(ctx, tx, uid, id); err != nil {
		return nil, translate(ctx, err)
	}
	recs, err := selectRecords(ctx, tx, uid, from, followPage, true)
	if err != nil {
		return nil, translate(ctx, err)
	}
	return recs, nil
}

// closeSubs ends every subscription; Close runs it before the pools close.
func (s *Store) closeSubs() {
	s.mu.Lock()
	subs := make([]*Subscription, 0, len(s.subs))
	for sub := range s.subs {
		subs = append(subs, sub)
	}
	s.mu.Unlock()
	for _, sub := range subs {
		sub.Close()
	}
}

// Duties (DH-ADR-STORE-6): a named job that must run once per home takes the
// row of its name in the duties table under the liveness rule of a session lease.
// The holder is probed only inside a write transaction, so two instances never
// both take a duty; a holder that died loses it to the next instance that asks,
// with the epoch raised. An instance keeps a duty it took until ReleaseDuty or
// Close.

// AcquireDuty takes duty name for this instance, or reports that a live
// instance holds it. A duty this instance already holds is acquired again at the
// same epoch.
func (s *Store) AcquireDuty(ctx context.Context, name string) (epoch uint64, ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return 0, false, err
	}
	if name == "" {
		return 0, false, store.Errorf(store.CodeInvalidBatch, "a duty needs a name")
	}
	err = s.write(ctx, func(tx *sql.Tx) error {
		var e uint64
		e, ok, _, err = s.acquireDuty(ctx, tx, name)
		epoch = e
		return err
	})
	return epoch, ok, err
}

// acquireDuty is AcquireDuty inside tx; it also returns the duty's last run
// (Unix nanoseconds).
func (s *Store) acquireDuty(ctx context.Context, tx *sql.Tx, name string) (uint64, bool, int64, error) {
	var (
		holder sql.NullString
		epoch  int64
		last   int64
	)
	err := tx.QueryRowContext(ctx, `SELECT holder, epoch, last_run_at FROM duties WHERE name = ?`, name).Scan(&holder, &epoch, &last)
	if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.ExecContext(ctx, `INSERT INTO duties (name, holder, epoch) VALUES (?, ?, 1)`, name, s.instance)
		return 1, err == nil, 0, err
	}
	if err != nil {
		return 0, false, 0, err
	}
	if holder.Valid && holder.String == s.instance {
		return uint64(epoch), true, last, nil
	}
	if holder.Valid {
		live, err := s.alive(ctx, tx, holder.String)
		if err != nil || live {
			return 0, false, last, err
		}
	}
	epoch++
	_, err = tx.ExecContext(ctx, `UPDATE duties SET holder = ?, epoch = ? WHERE name = ?`, s.instance, epoch, name)
	return uint64(epoch), err == nil, last, err
}

// ReleaseDuty gives up duty name when this instance holds it.
func (s *Store) ReleaseDuty(ctx context.Context, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE duties SET holder = NULL WHERE name = ? AND holder = ?`, name, s.instance)
		return err
	})
}

// RunDuty runs fn as duty name when this instance holds it (taking it when it is
// free or its holder is dead) and the last run of the duty, by any instance, is
// at least every ago (every 0: whenever it is held). It reports whether fn ran.
// The run is recorded only while this instance still holds the duty at the epoch
// it took it; otherwise RunDuty returns session/lease-lost.
func (s *Store) RunDuty(ctx context.Context, name string, every time.Duration, fn func(context.Context) error) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if name == "" {
		return false, store.Errorf(store.CodeInvalidBatch, "a duty needs a name")
	}
	// Runs of one duty in this instance are serialized from the due check to the
	// recorded run: the instance holds the duty at one epoch, so the lease alone
	// cannot tell two of its own calls apart.
	unlock, err := s.lockDuty(ctx, name)
	if err != nil {
		return false, err
	}
	defer unlock()
	var (
		epoch uint64
		due   bool
	)
	if err := s.write(ctx, func(tx *sql.Tx) error {
		e, ok, last, err := s.acquireDuty(ctx, tx, name)
		if err != nil || !ok {
			return err
		}
		epoch = e
		due = every <= 0 || last == 0 || s.now().UnixNano()-last >= every.Nanoseconds()
		return nil
	}); err != nil || !due {
		return false, err
	}
	if err := fn(ctx); err != nil {
		return true, err
	}
	var marked int64
	if err := s.write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE duties SET last_run_at = ? WHERE name = ? AND holder = ? AND epoch = ?`,
			s.now().UnixNano(), name, s.instance, int64(epoch))
		if err != nil {
			return err
		}
		marked, err = res.RowsAffected()
		return err
	}); err != nil {
		return true, err
	}
	if marked == 0 {
		return true, store.Errorf(store.CodeLeaseLost, "duty %q was taken by another instance during its run", name)
	}
	return true, nil
}

// lockDuty takes this instance's run lock of duty name, or returns ctx's error
// when ctx ends first; the returned function releases it.
func (s *Store) lockDuty(ctx context.Context, name string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.running == nil {
		s.running = map[string]chan struct{}{}
	}
	ch, ok := s.running[name]
	if !ok {
		ch = make(chan struct{}, 1)
		s.running[name] = ch
	}
	s.mu.Unlock()
	select {
	case ch <- struct{}{}:
		return func() { <-ch }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
