package sqlite_test

import (
	"context"
	"database/sql"
	"strconv"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// followInterval is the poll interval of the follow tests, and followBound the
// most a commit may take to reach the follower: one interval plus scheduling slack
// for a loaded runner.
const (
	followInterval = 50 * time.Millisecond
	followBound    = 2 * time.Second
)

// DH-T-STORE-14 / TC-07: events committed by a second process reach a follower
// once and in order, each within the bounded latency, and no read transaction is
// open between polls.
func TestFollowerSeesAnotherProcessesCommits(t *testing.T) {
	path := dbPath(t)
	a := spawnMaint(t, path, "append", "", "")
	a.expect("ready")
	s, err := sqlite.Open(bg, path, sqlite.WithFollowInterval(followInterval))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(bg)
	sub, err := s.Follow(bg, "f", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	commits := a.lines("committed")
	a.send("go")
	committed := map[store.Seq]time.Time{}
	for want := store.Seq(0); want < 10; want++ {
		select {
		case rec, ok := <-sub.Events():
			if !ok {
				t.Fatalf("the subscription ended at seq %d: %v", want, sub.Err())
			}
			got := time.Now()
			if rec.Seq != want || rec.Type != "tool/call" {
				t.Fatalf("follower received seq %d (%s); want seq %d, in order", rec.Seq, rec.Type, want)
			}
			for committed[want].IsZero() {
				line := <-commits
				n, _ := strconv.Atoi(field(t, line, "seq"))
				ns, _ := strconv.ParseInt(field(t, line, "at"), 10, 64)
				committed[store.Seq(n)] = time.Unix(0, ns)
			}
			// The child reports its commit after Append returned, so the
			// follower may even see the row first.
			if lag := got.Sub(committed[want]); lag > followBound {
				t.Fatalf("seq %d reached the follower %s after its commit; want within %s", want, lag, followBound)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the follower never received seq %d", want)
		}
	}
	select {
	case rec := <-sub.Events():
		t.Fatalf("follower received seq %d again or past the end", rec.Seq)
	case <-time.After(5 * followInterval):
	}
	assertNoReader(t, path)
	a.kill()
	sub.Close()
	if err := sub.Err(); err != nil {
		t.Fatalf("Err after Close = %v; want nil", err)
	}
	if _, ok := <-sub.Events(); ok {
		t.Fatal("Events is open after Close")
	}
}

// assertNoReader checks that no connection holds a read transaction on path: a
// TRUNCATE checkpoint from a fresh connection, waiting for readers up to its busy
// timeout, completes (busy = 0) and leaves an empty write-ahead log.
func assertNoReader(t *testing.T, path string) {
	t.Helper()
	db := fresh(t, path)
	conn, err := db.Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(bg, `PRAGMA busy_timeout = 3000`); err != nil {
		t.Fatal(err)
	}
	var busy, log, done int64
	if err := conn.QueryRowContext(bg, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &log, &done); err != nil {
		t.Fatal(err)
	}
	if busy != 0 || log != 0 {
		t.Fatalf("wal_checkpoint(TRUNCATE) = busy %d, log %d; want 0 and 0: a follower holds a read transaction between polls", busy, log)
	}
}

// A follower in the writing process sees its own commits, a subscription from a
// later position starts there, and deleting the session ends it with
// session/deleted; the store's Close ends the rest.
func TestFollowInProcess(t *testing.T) {
	path := dbPath(t)
	s, err := sqlite.Open(bg, path, sqlite.WithFollowInterval(followInterval))
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(bg, store.Header{ID: "f"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for seq := store.Seq(0); seq < 3; seq++ {
		if err := w.Append(bg, store.Batch{First: seq, Records: []store.Record{call(t, seq)}}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Follow(bg, "missing", 0); codeOf(err) != store.CodeNotFound {
		t.Fatalf("Follow(missing) = %v; want %s", err, store.CodeNotFound)
	}
	late, err := s.Follow(bg, "f", 2)
	if err != nil {
		t.Fatal(err)
	}
	if rec := next(t, late); rec.Seq != 2 {
		t.Fatalf("Follow from 2 delivered seq %d first", rec.Seq)
	}
	if err := w.Append(bg, store.Batch{First: 3, Records: []store.Record{call(t, 3)}}); err != nil {
		t.Fatal(err)
	}
	if rec := next(t, late); rec.Seq != 3 {
		t.Fatalf("delivered seq %d; want 3", rec.Seq)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(bg, "f", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	waitEnd(t, late)
	if codeOf(late.Err()) != store.CodeDeleted {
		t.Fatalf("Err after the deletion = %v; want %s", late.Err(), store.CodeDeleted)
	}

	if _, err := s.Create(bg, store.Header{ID: "g"}, store.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(bg)
	byCtx, err := s.Follow(ctx, "g", 0)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitEnd(t, byCtx)
	if byCtx.Err() != context.Canceled {
		t.Fatalf("Err after the context ended = %v; want context.Canceled", byCtx.Err())
	}
	held, err := s.Follow(bg, "g", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	waitEnd(t, held)
	if held.Err() != nil {
		t.Fatalf("Err after Store.Close = %v; want nil", held.Err())
	}
	if _, err := s.Follow(bg, "g", 0); codeOf(err) != store.CodeClosed {
		t.Fatalf("Follow after Close = %v; want %s", err, store.CodeClosed)
	}
}

func next(t *testing.T, sub *sqlite.Subscription) store.Record {
	t.Helper()
	select {
	case rec, ok := <-sub.Events():
		if !ok {
			t.Fatalf("the subscription ended: %v", sub.Err())
		}
		return rec
	case <-time.After(10 * time.Second):
		t.Fatal("no event within 10s")
	}
	return store.Record{}
}

func waitEnd(t *testing.T, sub *sqlite.Subscription) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case _, ok := <-sub.Events():
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the subscription did not end within 10s")
		}
	}
}

// DH-T-STORE-14: a duty has one holder across processes, is not run again within
// its period, and moves to another instance once its holder is killed; Close
// releases the duties an instance holds.
func TestDutyHasOneHolderAndMovesAfterAKill(t *testing.T) {
	path := dbPath(t)
	c1, c2 := spawnMaint(t, path, "duty", "", ""), spawnMaint(t, path, "duty", "", "")
	r1, r2 := c1.expect("duty"), c2.expect("duty")
	holder, other := c1, c2
	if field(t, r2, "ran") == "true" {
		holder, other, r1, r2 = c2, c1, r2, r1
	}
	if field(t, r1, "ran") != "true" || field(t, r2, "ran") != "false" {
		t.Fatalf("two processes ran the duty: %q and %q; want exactly one run", r1, r2)
	}
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(bg)
	ran := 0
	run := func(context.Context) error { ran++; return nil }
	if ok, err := s.RunDuty(bg, "d", 0, run); ok || err != nil || ran != 0 {
		t.Fatalf("RunDuty while a live process holds it = %v, %v (ran %d); want not run", ok, err, ran)
	}
	if _, ok, err := s.AcquireDuty(bg, "d"); ok || err != nil {
		t.Fatalf("AcquireDuty while held = %v, %v", ok, err)
	}
	db := fresh(t, path)
	dutyRow := func() (holder sql.NullString, epoch, last int64) {
		t.Helper()
		if err := db.QueryRow(`SELECT holder, epoch, last_run_at FROM duties WHERE name = 'd'`).Scan(&holder, &epoch, &last); err != nil {
			t.Fatal(err)
		}
		return
	}
	if h, e, last := dutyRow(); h.String != field(t, r1, "instance") || e != 1 || last == 0 {
		t.Fatalf("duty row = %v epoch %d last %d; want the runner at epoch 1 with its run recorded", h, e, last)
	}

	holder.kill()
	// The run is recent, so the new holder takes the duty but does not run it
	// within the period; with no period it runs.
	if ok, err := s.RunDuty(bg, "d", time.Hour, run); ok || err != nil || ran != 0 {
		t.Fatalf("RunDuty within the period after the kill = %v, %v; want not due", ok, err)
	}
	if h, e, _ := dutyRow(); h.String != s.Instance() || e != 2 {
		t.Fatalf("duty row after the kill = %v epoch %d; want this instance at epoch 2", h, e)
	}
	if ok, err := s.RunDuty(bg, "d", 0, run); !ok || err != nil || ran != 1 {
		t.Fatalf("RunDuty as the holder = %v, %v (ran %d); want one run", ok, err, ran)
	}
	other.kill()

	if err := s.ReleaseDuty(bg, "d"); err != nil {
		t.Fatal(err)
	}
	if h, _, _ := dutyRow(); h.Valid {
		t.Fatalf("holder after ReleaseDuty = %v; want none", h)
	}
	if _, ok, err := s.AcquireDuty(bg, "d"); !ok || err != nil {
		t.Fatalf("AcquireDuty of a released duty = %v, %v", ok, err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if h, e, _ := dutyRow(); h.Valid || e != 3 {
		t.Fatalf("duty row after Close = %v epoch %d; want released at epoch 3", h, e)
	}
}
