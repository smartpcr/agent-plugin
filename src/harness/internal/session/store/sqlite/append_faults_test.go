package sqlite_test

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// appendOutcome is what a fresh connection reads of one session after a
// faulted append: its events and next seq, its blob references and the seq of
// each of its checkpoints by key.
type appendOutcome struct {
	events, next, refs int64
	checkpoints        map[string]int64
}

func outcomeOf(t *testing.T, path, id string) appendOutcome {
	t.Helper()
	db := fresh(t, path)
	o := appendOutcome{checkpoints: map[string]int64{}}
	o.events, o.next, _ = countRows(t, db, id)
	o.refs = countWhere(t, db, `SELECT count(*) FROM blob_refs b JOIN sessions s ON s.uid = b.session WHERE s.id = ?`, id)
	rs, err := db.Query(`SELECT c.key, c.seq FROM checkpoints c JOIN sessions s ON s.uid = c.session WHERE s.id = ?`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	for rs.Next() {
		var (
			key string
			seq int64
		)
		if err := rs.Scan(&key, &seq); err != nil {
			t.Fatal(err)
		}
		o.checkpoints[key] = seq
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return o
}

// DH-T-STORE-3 / DH-T-VIEW-5 / DH-D-VIEW-2: a fault injected at each step of
// the append transaction (taking the write lock, each event row, a blob
// reference, the session row, each derived row, the commit) leaves the events
// and their blob references all present or all absent, and the derived rows
// present with their events or absent as a group, never ahead of them. A fault
// in the derived rows costs no event: the events commit, every derived row of
// the batch keeps its older seq, and the drop is counted and reported. After an
// event-side fault the writer appends the same batch whole.
//
//dsh:test DH-T-STORE-3, DH-T-VIEW-5
func TestAppendFaultAtEachStep(t *testing.T) {
	path := dbPath(t)
	s, err := sqlite.Open(bg, path, sqlite.WithBusyTimeout(50*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(bg); err != nil {
			t.Errorf("Store.Close: %v", err)
		}
	})
	var notices []store.Degraded
	defer s.OnDegraded(func(d store.Degraded) { notices = append(notices, d) })()
	db := fresh(t, path)
	const refusal = `BEGIN SELECT RAISE(ABORT, 'test: refused'); END`
	type fault struct {
		name string
		// inject installs the fault and returns its removal.
		inject func(t *testing.T) func()
		// derived is true when the fault is in the derived rows: the events
		// commit and the derived rows are dropped.
		derived bool
		code    store.Code
	}
	trigger := func(def string) func(t *testing.T) func() {
		return func(t *testing.T) func() {
			t.Helper()
			if _, err := db.Exec(`CREATE TRIGGER fault_test ` + def + ` ` + refusal); err != nil {
				t.Fatal(err)
			}
			return func() {
				if _, err := db.Exec(`DROP TRIGGER fault_test`); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	faults := []fault{
		{name: "write lock held by another process", code: sqlite.CodeBusy,
			inject: func(t *testing.T) func() { return holdWriteLock(t, path) }},
		{name: "first event", code: sqlite.CodeConstraint, inject: trigger(`BEFORE INSERT ON events WHEN NEW.seq = 2`)},
		{name: "middle event", code: sqlite.CodeConstraint, inject: trigger(`BEFORE INSERT ON events WHEN NEW.seq = 3`)},
		{name: "last event", code: sqlite.CodeConstraint, inject: trigger(`BEFORE INSERT ON events WHEN NEW.seq = 4`)},
		{name: "blob reference", code: sqlite.CodeConstraint, inject: trigger(`BEFORE INSERT ON blob_refs WHEN NEW.seq = 3`)},
		{name: "session row", code: sqlite.CodeConstraint,
			inject: trigger(`BEFORE UPDATE OF next_seq ON sessions WHEN NEW.next_seq <> OLD.next_seq`)},
		{name: "commit", code: sqlite.CodeIO, inject: func(t *testing.T) func() {
			var once atomic.Bool
			return sqlite.SetCommitTx(func(tx *sql.Tx) error {
				if once.CompareAndSwap(false, true) {
					_ = tx.Rollback()
					return errors.New("test: the commit failed")
				}
				return tx.Commit()
			})
		}},
		{name: "first derived row", derived: true, inject: trigger(`BEFORE UPDATE ON checkpoints WHEN NEW.key = 'view/x'`)},
		{name: "second derived row", derived: true, inject: trigger(`BEFORE UPDATE ON checkpoints WHEN NEW.key = 'core/c'`)},
		{name: "new derived row", derived: true, inject: trigger(`BEFORE INSERT ON checkpoints WHEN NEW.key = 'view/new'`)},
	}
	for i, f := range faults {
		t.Run(f.name, func(t *testing.T) {
			id := fmt.Sprintf("s%d", i)
			w, err := s.Create(bg, store.Header{ID: id}, store.CreateOptions{})
			if err != nil {
				t.Fatal(err)
			}
			defer w.Close(bg)
			if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0), spilled(t, 1, id+"-a")},
				Checkpoints: []store.Checkpoint{
					{Key: "view/x", StateVersion: 1, Seq: 1, State: `{"n":1}`},
					{Key: "core/c", StateVersion: 1, Seq: 1, State: `{"n":1}`},
				}}); err != nil {
				t.Fatal(err)
			}
			batch := store.Batch{First: 2, Records: []store.Record{call(t, 2), spilled(t, 3, id+"-b"), call(t, 4)},
				Checkpoints: []store.Checkpoint{
					{Key: "view/x", StateVersion: 1, Seq: 4, State: `{"n":4}`},
					{Key: "core/c", StateVersion: 1, Seq: 4, State: `{"n":4}`},
					{Key: "view/new", StateVersion: 1, Seq: 3, State: `{"n":3}`},
				}}
			before := len(notices)
			remove := f.inject(t)
			err = w.Append(bg, batch)
			remove()
			got := outcomeOf(t, path, id)
			// Never ahead: every derived row is below the committed next seq.
			for key, seq := range got.checkpoints {
				if seq >= got.next {
					t.Fatalf("derived row %s at seq %d is ahead of next seq %d", key, seq, got.next)
				}
			}
			if got.events != got.next {
				t.Fatalf("%d events under next seq %d", got.events, got.next)
			}
			older := map[string]int64{"view/x": 1, "core/c": 1}
			switch {
			case f.derived:
				if err != nil {
					t.Fatalf("Append with a refused derived row = %v; want its events committed", err)
				}
				if got.events != 5 || got.refs != 2 || !equalMaps(got.checkpoints, older) {
					t.Fatalf("after the refused derived row: %+v; want 5 events, 2 references and the older derived rows %v", got, older)
				}
				if n := w.(store.DerivedCounter).DerivedDropped(); n != 3 {
					t.Fatalf("DerivedDropped = %d, want 3", n)
				}
				if len(notices) != before+1 || notices[before].Session != id || len(notices[before].Keys) != 3 {
					t.Fatalf("Degraded notices %+v; want one naming the 3 rows of %s", notices[before:], id)
				}
			default:
				if !errors.Is(err, &store.Error{Code: f.code}) {
					t.Fatalf("Append = %v, want %s", err, f.code)
				}
				if got.events != 2 || got.refs != 1 || !equalMaps(got.checkpoints, older) {
					t.Fatalf("after the failed append: %+v; want none of the batch: 2 events, 1 reference, derived rows %v", got, older)
				}
				if len(notices) != before {
					t.Fatalf("a failed append sent a Degraded notice: %+v", notices[before:])
				}
				if err := w.Append(bg, batch); err != nil {
					t.Fatalf("the same batch after the fault = %v", err)
				}
				want := map[string]int64{"view/x": 4, "core/c": 4, "view/new": 3}
				if got := outcomeOf(t, path, id); got.events != 5 || got.refs != 2 || !equalMaps(got.checkpoints, want) {
					t.Fatalf("after the retried append: %+v; want 5 events, 2 references, derived rows %v", got, want)
				}
			}
		})
	}
}

func equalMaps(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// blobRefRows is every blob_refs row of session id, read by a fresh connection.
func blobRefRows(t *testing.T, path, id string) []string {
	t.Helper()
	rs, err := fresh(t, path).Query(`SELECT b.seq, b.ref FROM blob_refs b JOIN sessions s ON s.uid = b.session
		WHERE s.id = ? ORDER BY b.seq, b.ref`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var (
			seq int64
			ref string
		)
		if err := rs.Scan(&seq, &ref); err != nil {
			t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d|%s", seq, ref))
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// DH-T-STORE-3: a fork's rows equal its parent's prefix row for row, every
// column of every event and every blob reference, and the parent's rows past
// the fork point are not copied; the fork's own appends leave the parent's rows
// as they were.
//
//dsh:test DH-T-STORE-3
func TestForkEqualsParentPrefixRowForRow(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	_, recs := exportFixture(t)
	p, err := s.Create(bg, store.Header{ID: "p"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ms = max(ms, recs[len(recs)-1].Time)
	recs = append(recs, spilled(t, store.Seq(len(recs)), "early"), call(t, store.Seq(len(recs)+1)), spilled(t, store.Seq(len(recs)+2), "late"))
	if err := p.Append(bg, store.Batch{First: 0, Records: recs}); err != nil {
		t.Fatal(err)
	}
	through := store.Seq(len(recs) - 2) // the "early" reference is inherited, "late" is not
	parentRows, parentRefs := eventRows(t, path, "p"), blobRefRows(t, path, "p")
	f, err := s.Create(bg, store.Header{ID: "f"}, store.CreateOptions{ForkOf: &store.ForkOf{ID: "p", Through: through}})
	if err != nil {
		t.Fatal(err)
	}
	forkRows, forkRefs := eventRows(t, path, "f"), blobRefRows(t, path, "f")
	if !slices.Equal(forkRows, parentRows[:through+1]) {
		t.Fatalf("fork rows differ from the parent's prefix through %d:\n%s\nwant\n%s", through,
			strings.Join(forkRows, "\n"), strings.Join(parentRows[:through+1], "\n"))
	}
	var wantRefs []string
	for _, r := range parentRefs {
		var seq int64
		if _, err := fmt.Sscanf(r, "%d|", &seq); err != nil {
			t.Fatal(err)
		}
		if store.Seq(seq) <= through {
			wantRefs = append(wantRefs, r)
		}
	}
	if len(wantRefs) == 0 || !slices.Equal(forkRefs, wantRefs) {
		t.Fatalf("fork references %v, want the parent's through %d: %v", forkRefs, through, wantRefs)
	}
	if err := f.Append(bg, store.Batch{First: through + 1, Records: []store.Record{call(t, through+1)}}); err != nil {
		t.Fatal(err)
	}
	if got := eventRows(t, path, "p"); !slices.Equal(got, parentRows) {
		t.Fatal("the fork's append changed the parent's rows")
	}
	if got := eventRows(t, path, "f"); len(got) != int(through)+2 || !slices.Equal(got[:through+1], parentRows[:through+1]) {
		t.Fatalf("the fork after its append has %d rows; want its inherited prefix and one more", len(got))
	}
	for _, w := range []store.Writer{p, f} {
		if err := w.Close(bg); err != nil {
			t.Fatal(err)
		}
	}
}

// DH-T-STORE-4: an injected failed commit keeps the batch: nothing of it is
// stored, the store stays usable, the next Flush succeeds and the same batch,
// handed again, commits whole.
//
//dsh:test DH-T-STORE-4
func TestFailedCommitKeepsTheBatch(t *testing.T) {
	path := dbPath(t)
	var fail atomic.Bool
	t.Cleanup(sqlite.SetCommitTx(func(tx *sql.Tx) error {
		if fail.CompareAndSwap(true, false) {
			_ = tx.Rollback() // the failed commit leaves nothing, as SQLite's rollback of an I/O error does
			return errors.New("test: the commit failed (disk I/O)")
		}
		return tx.Commit()
	}))
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)}}); err != nil {
		t.Fatal(err)
	}
	batch := store.Batch{First: 1, Records: []store.Record{call(t, 1), call(t, 2)},
		Checkpoints: []store.Checkpoint{{Key: "core/x", StateVersion: 1, Seq: 2, State: `{"n":2}`}}}
	fail.Store(true)
	err = w.Append(bg, batch)
	if !errors.Is(err, &store.Error{Code: sqlite.CodeIO}) || !strings.Contains(err.Error(), "test: the commit failed") {
		t.Fatalf("Append under a failing commit = %v, want %s", err, sqlite.CodeIO)
	}
	if events, next, cks := countRows(t, fresh(t, path), "s"); events != 1 || next != 1 || cks != 0 {
		t.Fatalf("after the failed commit a fresh connection reads %d events, next seq %d, %d checkpoints; want 1, 1, 0", events, next, cks)
	}
	if err := w.Flush(bg); err != nil {
		t.Fatalf("the Flush after the failed commit = %v", err)
	}
	if err := s.Flush(bg); err != nil {
		t.Fatalf("Store.Flush after the failed commit = %v", err)
	}
	if err := w.Append(bg, batch); err != nil {
		t.Fatalf("the kept batch, appended again = %v", err)
	}
	if err := w.Flush(bg); err != nil {
		t.Fatal(err)
	}
	if events, next, cks := countRows(t, fresh(t, path), "s"); events != 3 || next != 3 || cks != 1 {
		t.Fatalf("after the retried batch a fresh connection reads %d events, next seq %d, %d checkpoints; want 3, 3, 1", events, next, cks)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
}

// DH-T-STORE-4: when a transaction fails and its rollback fails too, the outcome
// of that transaction is unknown, so the store is unusable: the failing append
// returns its own error, and every later write, flush, open for write, deletion,
// derived write and close of a writer returns store/unusable, even once the
// rollback would work again. The database itself holds none of the batch.
//
//dsh:test DH-T-STORE-4
func TestFailedRollbackMakesTheStoreUnusable(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)}}); err != nil {
		t.Fatal(err)
	}
	u, err := s.Create(bg, store.Header{ID: "u"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	db := fresh(t, path)
	if _, err := db.Exec(`CREATE TRIGGER fault_test BEFORE INSERT ON events WHEN NEW.seq = 2
		BEGIN SELECT RAISE(ABORT, 'test: the second row is refused'); END`); err != nil {
		t.Fatal(err)
	}
	restore := sqlite.SetRollbackTx(func(tx *sql.Tx) error {
		_ = tx.Rollback()
		return errors.New("test: the rollback failed")
	})
	batch := store.Batch{First: 1, Records: []store.Record{call(t, 1), call(t, 2)}}
	err = w.Append(bg, batch)
	restore()
	if !errors.Is(err, &store.Error{Code: sqlite.CodeConstraint}) {
		t.Fatalf("the append whose rollback failed = %v, want its own %s", err, sqlite.CodeConstraint)
	}
	if _, err := db.Exec(`DROP TRIGGER fault_test`); err != nil {
		t.Fatal(err)
	}
	unusable := &store.Error{Code: sqlite.CodeUnusable}
	for name, fn := range map[string]func() error{
		"Append":       func() error { return w.Append(bg, batch) },
		"other Append": func() error { return u.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)}}) },
		"Flush":        func() error { return w.Flush(bg) },
		"Store.Flush":  func() error { return s.Flush(bg) },
		"Create": func() error {
			_, err := s.Create(bg, store.Header{ID: "n"}, store.CreateOptions{})
			return err
		},
		"OpenWrite": func() error {
			_, err := s.OpenWrite(bg, "s", store.OpenOptions{Takeover: store.Force})
			return err
		},
		"Delete": func() error { return s.Delete(bg, "u", store.DeleteOptions{}) },
		"UpsertDerived": func() error {
			_, err := s.UpsertDerived(bg, "s", []store.Checkpoint{{Key: "view/x", Seq: 0, State: `{}`}})
			return err
		},
		"WriteTx":      func() error { return s.WriteTx(bg, func(*sql.Tx) error { return nil }) },
		"Writer.Close": func() error { return w.Close(bg) },
	} {
		// Twice: the store stays unusable.
		for range 2 {
			if err := fn(); !errors.Is(err, unusable) {
				t.Errorf("%s after the failed rollback = %v, want %s", name, err, sqlite.CodeUnusable)
			}
		}
	}
	if events, next, cks := countRows(t, fresh(t, path), "s"); events != 1 || next != 1 || cks != 0 {
		t.Fatalf("a fresh connection reads %d events, next seq %d, %d checkpoints; want 1, 1, 0", events, next, cks)
	}
}

// M1-G-STORE-14: UpsertDerived writes a derived row without the session's
// lease while another instance holds it, and only moves the row forward and
// below the next seq. The holder keeps its lease and epoch and appends on; the
// other instance still cannot take the lease.
func TestUpsertDerivedNeedsNoLease(t *testing.T) {
	path := dbPath(t)
	holder := open(t, path)
	other := open(t, path)
	w, err := holder.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(bg)
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0), call(t, 1), call(t, 2)},
		Checkpoints: []store.Checkpoint{{Key: "view/x", StateVersion: 2, Seq: 0, State: `{"n":0}`}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := other.OpenWrite(bg, "s", store.OpenOptions{}); !errors.Is(err, store.ErrOwned) {
		t.Fatalf("OpenWrite by the other instance = %v, want %v", err, store.ErrOwned)
	}
	upsert := func(what string, want int, seq store.Seq) {
		t.Helper()
		n, err := other.UpsertDerived(bg, "s", []store.Checkpoint{{Key: "view/x", StateVersion: 2, Seq: seq, State: fmt.Sprintf(`{"n":%d}`, seq)}})
		if err != nil || n != want {
			t.Fatalf("UpsertDerived %s (seq %d) = %d, %v; want %d", what, seq, n, err, want)
		}
	}
	upsert("forward while held", 1, 2)
	upsert("equal", 0, 2)
	upsert("backward", 0, 1)
	upsert("at the next seq", 0, 3)
	upsert("past the largest seq", 0, store.Seq(1)<<63)
	cps, err := w.Checkpoints(bg)
	if err != nil || len(cps) != 1 || cps[0].Seq != 2 || cps[0].State != `{"n":2}` {
		t.Fatalf("the holder reads %+v, %v; want the row the other instance moved to seq 2", cps, err)
	}
	sum, err := other.Stat(bg, "s")
	if err != nil || sum.Holder == nil || sum.Holder.Instance != holder.Instance() || sum.LeaseEpoch != 1 {
		t.Fatalf("Stat after the lease-free write = %+v, %v; want the holder's lease at epoch 1", sum, err)
	}
	if err := w.Append(bg, store.Batch{First: 3, Records: []store.Record{call(t, 3)}}); err != nil {
		t.Fatalf("the holder's append after the lease-free write = %v", err)
	}
	upsert("below the new next seq", 1, 3)
	if _, err := other.UpsertDerived(bg, "s", []store.Checkpoint{{Seq: 1}}); !errors.Is(err, store.ErrInvalidBatch) {
		t.Fatalf("UpsertDerived of a row with no key = %v", err)
	}
	if err := other.Close(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := other.UpsertDerived(bg, "s", []store.Checkpoint{{Key: "view/x", Seq: 0}}); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("UpsertDerived on a closed store = %v", err)
	}
}
