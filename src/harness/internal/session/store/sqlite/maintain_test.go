package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// stamped is n tool calls from seq 0 at one-second steps from at.
func stamped(t *testing.T, n int, at time.Time) []store.Record {
	t.Helper()
	recs := make([]store.Record, n)
	for i := range recs {
		d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", i), Name: "grep", Arguments: `{}`})
		if err != nil {
			t.Fatal(err)
		}
		if recs[i], err = d.Stamp(store.Seq(i), at.Add(time.Duration(i)*time.Second).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	return recs
}

// rowsOf counts what is left of session id: its sessions row, events, checkpoints
// and requests, read by a fresh connection.
func rowsOf(t *testing.T, path, id string) (sessions, events, checkpoints, requests int64) {
	t.Helper()
	db := fresh(t, path)
	sessions = countWhere(t, db, `SELECT count(*) FROM sessions WHERE id = ?`, id)
	events = countWhere(t, db, `SELECT count(*) FROM events WHERE session IN (SELECT uid FROM sessions WHERE id = ?)`, id)
	checkpoints = countWhere(t, db, `SELECT count(*) FROM checkpoints WHERE session IN (SELECT uid FROM sessions WHERE id = ?)`, id)
	requests = countWhere(t, db, `SELECT count(*) FROM requests WHERE session = ?`, id)
	return
}

func assertGone(t *testing.T, path, id string) {
	t.Helper()
	if s, e, c, r := rowsOf(t, path, id); s+e+c+r != 0 {
		t.Fatalf("session %q left %d session, %d event, %d checkpoint and %d request rows", id, s, e, c, r)
	}
}

func assertClean(t *testing.T, s *sqlite.Store) {
	t.Helper()
	rep, err := s.Check(bg)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Clean() || len(rep.Deleting) != 0 {
		t.Fatalf("Check = %+v; want clean, nothing being deleted", rep)
	}
}

// DH-T-STORE-11 / TC-07: a deletion killed after removing half the events leaves
// a tombstone: the session is invisible at once and its spill directory is gone.
// When the store is reopened the deletion completes: no row of the session or
// of its subagent child remains, the fork survives, and Check reports clean.
func TestInterruptedDeletionFinishesOnOpen(t *testing.T) {
	path := dbPath(t)
	spill := filepath.Join(filepath.Dir(path), "spill")
	at := time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(bg, store.Header{ID: "big"}, stamped(t, 1000, at)); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(bg, store.Header{ID: "kid", Origin: "subagent", Creator: "big"}, stamped(t, 5, at)); err != nil {
		t.Fatal(err)
	}
	fork, err := s.Create(bg, store.Header{ID: "fork"}, store.CreateOptions{ForkOf: &store.ForkOf{ID: "big", Through: 9}})
	if err != nil {
		t.Fatal(err)
	}
	if err := fork.Close(bg); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	mine := filepath.Join(spill, sqlite.SpillName("big"))
	theirs := filepath.Join(spill, sqlite.SpillName("fork"))
	for _, d := range []string{mine, theirs} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "0123-out.txt"), []byte("spilled"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	d := spawnMaint(t, path, "delete", "big", spill)
	line := d.expect("chunk")
	if n, _ := strconv.Atoi(field(t, line, "deleted")); n != 500 {
		t.Fatalf("child reported %q; want it stopped after 500 of 1000 events", line)
	}
	db := fresh(t, path)
	if n := countWhere(t, db, `SELECT count(*) FROM sessions WHERE id IN ('big', 'kid') AND state = 'deleting'`); n != 2 {
		t.Fatalf("%d tombstones; want the session and its subagent child marked", n)
	}
	if _, e, _, _ := rowsOf(t, path, "big"); e != 500 {
		t.Fatalf("%d events left mid-deletion; want 500", e)
	}
	if _, err := os.Stat(mine); !os.IsNotExist(err) {
		t.Fatalf("spill directory of the deleted session: %v; want it gone before the rows", err)
	}
	// Invisible at once, to another instance, while the deleter still runs.
	o, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"big", "kid"} {
		if _, err := o.Stat(bg, id); codeOf(err) != store.CodeNotFound {
			t.Fatalf("Stat(%s) mid-deletion = %v; want %s", id, err, store.CodeNotFound)
		}
	}
	if page, err := o.List(bg, store.Query{}); err != nil || len(page.Summaries) != 1 || page.Summaries[0].Header.ID != "fork" {
		t.Fatalf("List mid-deletion = %+v, %v; want the fork only", page, err)
	}
	if rep, err := o.Check(bg); err != nil || !rep.Clean() || !slices.Equal(rep.Deleting, []string{"big", "kid"}) {
		t.Fatalf("Check mid-deletion = %+v, %v; want clean with two tombstones", rep, err)
	}
	if err := o.Close(bg); err != nil {
		t.Fatal(err)
	}

	d.kill()
	r := open(t, path)
	assertGone(t, path, "big")
	assertGone(t, path, "kid")
	assertClean(t, r)
	rd, err := r.OpenRead(bg, "fork")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rd.Read(bg, 0, 0, store.ReadOptions{}); err != nil || len(got) != 10 {
		t.Fatalf("the fork after its parent's deletion: %d records, %v; want 10", len(got), err)
	}
	if _, err := os.Stat(filepath.Join(theirs, "0123-out.txt")); err != nil {
		t.Fatalf("the fork's spill file: %v", err)
	}
	// The id is free again.
	if _, err := r.Create(bg, store.Header{ID: "big"}, store.CreateOptions{}); err != nil {
		t.Fatalf("Create of the deleted id: %v", err)
	}
}

// DH-T-STORE-11: deletion is refused while another process holds the session, or
// any subagent child of it, and nothing changes; this instance's own writer
// blocks it too until closed. A deletion runs in chunks and removes checkpoints
// and requests with the row.
func TestDeleteRefusedWhileHeld(t *testing.T) {
	path := dbPath(t)
	a, inst := holdIn(t, path)
	s := open(t, path)
	err := s.Delete(bg, "s", store.DeleteOptions{})
	if codeOf(err) != store.CodeOwned || !strings.Contains(err.Error(), "s") {
		t.Fatalf("Delete while another process holds it = %v; want %s", err, store.CodeOwned)
	}
	if sum, err := s.Stat(bg, "s"); err != nil || sum.NextSeq != 3 || sum.Holder == nil || sum.Holder.Instance != inst {
		t.Fatalf("Stat after the refusal = %+v, %v; want 3 records held by %s", sum, err, inst)
	}
	if err := s.Seed(bg, store.Header{ID: "p"}, stamped(t, 2, time.Now())); err != nil {
		t.Fatal(err)
	}
	w, err := s.OpenWrite(bg, "p", store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(bg, store.Header{ID: "top"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh(t, path).Exec(`UPDATE sessions SET origin = 'subagent', creator = 'top' WHERE id = 'p'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(bg, "top", store.DeleteOptions{}); codeOf(err) != store.CodeOwned {
		t.Fatalf("Delete of a session whose subagent child is held = %v; want %s", err, store.CodeOwned)
	}
	if n := countWhere(t, fresh(t, path), `SELECT count(*) FROM sessions WHERE state = 'deleting'`); n != 0 {
		t.Fatalf("a refused deletion left %d tombstones", n)
	}
	if err := w.Append(bg, store.Batch{First: 2, Records: stamped(t, 3, time.Now())[2:],
		Checkpoints: []store.Checkpoint{{Key: "core/x", StateVersion: 1, Seq: 2, State: "{}"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh(t, path).Exec(`INSERT INTO requests (id, session, kind, drafts) VALUES ('r1', 'p', 'feedback', '[]')`); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(bg, "top", store.DeleteOptions{}); err != nil {
		t.Fatalf("Delete once released: %v", err)
	}
	assertGone(t, path, "top")
	assertGone(t, path, "p")
	a.kill()
	assertClean(t, s)
}

// DH-T-STORE-11: retention is off by default (age 0 deletes nothing), finishes
// tombstones, and deletes only sessions no live instance holds whose last event
// is past the age; it runs as one duty per home.
func TestRetentionRetiresOnlyUnheldOldSessions(t *testing.T) {
	path := dbPath(t)
	now := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	s, err := sqlite.Open(bg, path, sqlite.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(bg)
	old := now.Add(-30 * 24 * time.Hour)
	for id, at := range map[string]time.Time{"old": old, "new": now.Add(-time.Hour), "held": old} {
		if err := s.Seed(bg, store.Header{ID: id, CreatedAt: at}, stamped(t, 3, at)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Seed(bg, store.Header{ID: "empty-old", CreatedAt: old}, nil); err != nil {
		t.Fatal(err)
	}
	w, err := s.OpenWrite(bg, "held", store.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close(bg)
	if got, err := s.Retire(bg, 0); err != nil || len(got) != 0 {
		t.Fatalf("Retire with no age = %v, %v; want nothing deleted", got, err)
	}
	ran, err := s.RunRetention(bg, 7*24*time.Hour, time.Hour)
	if err != nil || !ran {
		t.Fatalf("RunRetention = %v, %v", ran, err)
	}
	for _, id := range []string{"old", "empty-old"} {
		assertGone(t, path, id)
	}
	for _, id := range []string{"new", "held"} {
		if _, err := s.Stat(bg, id); err != nil {
			t.Fatalf("retention removed %s: %v", id, err)
		}
	}
	if ran, err := s.RunRetention(bg, 7*24*time.Hour, time.Hour); ran || err != nil {
		t.Fatalf("RunRetention again within the period = %v, %v; want not run", ran, err)
	}
}

// DH-T-STORE-11: Retire's shortlist is not its verdict. A shortlisted session
// that gains a recent event before its deletion, or whose id is deleted and
// created again (another session), survives; an untouched one goes.
func TestRetireRevalidatesItsShortlist(t *testing.T) {
	path := dbPath(t)
	now := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
	s, err := sqlite.Open(bg, path, sqlite.WithClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(bg)
	old := now.Add(-30 * 24 * time.Hour)
	for _, id := range []string{"refreshed", "replaced", "stale"} {
		if err := s.Seed(bg, store.Header{ID: id, CreatedAt: old}, stamped(t, 3, old)); err != nil {
			t.Fatal(err)
		}
	}
	var shortlist []string
	defer sqlite.SetAfterRetireShortlist(func(ids []string) {
		shortlist = ids
		w, err := s.OpenWrite(bg, "refreshed", store.OpenOptions{})
		if err != nil {
			t.Error(err)
			return
		}
		if err := w.Append(bg, store.Batch{First: 3, Records: stamped(t, 4, now)[3:]}); err != nil {
			t.Error(err)
		}
		if err := w.Close(bg); err != nil {
			t.Error(err)
		}
		if err := s.Delete(bg, "replaced", store.DeleteOptions{}); err != nil {
			t.Error(err)
		}
		if err := s.Seed(bg, store.Header{ID: "replaced", CreatedAt: old}, stamped(t, 2, old)); err != nil {
			t.Error(err)
		}
	})()
	got, err := s.Retire(bg, 7*24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(shortlist, []string{"refreshed", "replaced", "stale"}) {
		t.Fatalf("shortlist = %v; want all three old sessions", shortlist)
	}
	if !slices.Equal(got, []string{"stale"}) {
		t.Fatalf("Retire deleted %v; want only the untouched session", got)
	}
	assertGone(t, path, "stale")
	if sum, err := s.Stat(bg, "refreshed"); err != nil || sum.NextSeq != 4 {
		t.Fatalf("the refreshed session: %+v, %v; want it kept with 4 records", sum, err)
	}
	if sum, err := s.Stat(bg, "replaced"); err != nil || sum.NextSeq != 2 {
		t.Fatalf("the replacement session: %+v, %v; want it kept with its 2 records", sum, err)
	}
}

// DH-T-STORE-11: an interrupted deletion Open cannot finish fails Open with the
// cause, releasing the instance (no hosts row, no liveness lock); the tombstone
// stays, and the next Open that can finish it does.
func TestOpenFailsWhenADeletionCannotFinish(t *testing.T) {
	path := dbPath(t)
	spill := filepath.Join(filepath.Dir(path), "spill")
	s, err := sqlite.Open(bg, path, sqlite.WithSpillDir(spill))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(bg, store.Header{ID: "doomed"}, stamped(t, 3, time.Now())); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := fresh(t, path).Exec(`UPDATE sessions SET state = 'deleting' WHERE id = 'doomed'`); err != nil {
		t.Fatal(err)
	}
	restore := sqlite.SetRemoveAll(func(string) error { return os.ErrPermission })
	if _, err := sqlite.Open(bg, path, sqlite.WithSpillDir(spill)); codeOf(err) != sqlite.CodeIO || !strings.Contains(err.Error(), "doomed") {
		restore()
		t.Fatalf("Open with an unfinishable deletion = %v; want %s naming the session", err, sqlite.CodeIO)
	}
	restore()
	db := fresh(t, path)
	if n := countWhere(t, db, `SELECT count(*) FROM hosts`); n != 0 {
		t.Fatalf("the failed Open left %d hosts rows", n)
	}
	if n := countWhere(t, db, `SELECT count(*) FROM sessions WHERE id = 'doomed' AND state = 'deleting'`); n != 1 {
		t.Fatalf("the failed Open dropped the tombstone (%d rows)", n)
	}

	// The same failure while another connection takes the write lock: the
	// failed Open has claimed the tombstone and cannot remove its hosts row, so
	// it keeps its liveness file, unlocked, for the next Open to find it dead.
	var (
		release  func()
		instance string
	)
	restore = sqlite.SetRemoveAll(func(string) error {
		instance = tombHolder(t, db)
		release = holdWriteLock(t, path)
		return os.ErrPermission
	})
	_, err = sqlite.Open(bg, path, sqlite.WithSpillDir(spill), sqlite.WithBusyTimeout(100*time.Millisecond))
	restore()
	if codeOf(err) != sqlite.CodeIO || !strings.Contains(err.Error(), "doomed") || release == nil {
		t.Fatalf("Open with an unfinishable deletion and a contended cleanup = %v; want %s naming the session", err, sqlite.CodeIO)
	}
	release()
	if n := countWhere(t, db, `SELECT count(*) FROM hosts WHERE instance = ?`, instance); n != 1 {
		t.Fatalf("%d hosts rows of the failed instance; want its row left by the contended cleanup", n)
	}
	if n := countWhere(t, db, `SELECT count(*) FROM sessions WHERE id = 'doomed' AND lease_holder = ?`, instance); n != 1 {
		t.Fatal("the tombstone is not held by the failed instance; the test does not exercise the stranding")
	}
	r, err := sqlite.Open(bg, path, sqlite.WithSpillDir(spill))
	if err != nil {
		t.Fatalf("Open once removal works: %v", err)
	}
	defer r.Close(bg)
	assertGone(t, path, "doomed")
	assertClean(t, r)
}

// tombHolder is the lease holder of the 'doomed' tombstone.
func tombHolder(t *testing.T, db *sql.DB) string {
	t.Helper()
	var h sql.NullString
	if err := db.QueryRow(`SELECT lease_holder FROM sessions WHERE id = 'doomed'`).Scan(&h); err != nil {
		t.Fatal(err)
	}
	return h.String
}

// DH-T-STORE-12: Check reports a session whose row count was damaged on purpose,
// a checkpoint past the end, and rows of no session.
func TestCheckReportsDamage(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	for _, id := range []string{"ok", "short"} {
		if err := s.Seed(bg, store.Header{ID: id}, stamped(t, 4, time.Now())); err != nil {
			t.Fatal(err)
		}
	}
	assertClean(t, s)
	db := fresh(t, path)
	if _, err := db.Exec(`UPDATE sessions SET next_seq = 5 WHERE id = 'short'`); err != nil {
		t.Fatal(err)
	}
	rep, err := s.Check(bg)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Clean() || rep.Sessions != 2 || len(rep.Problems) != 1 || !strings.Contains(rep.Problems[0], `"short"`) {
		t.Fatalf("Check after damaging next_seq = %+v; want one problem naming short", rep)
	}
	if _, err := db.Exec(`UPDATE sessions SET next_seq = 4 WHERE id = 'short'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO checkpoints (session, key, state_version, seq, state)
		SELECT uid, 'core/x', 1, 9, '{}' FROM sessions WHERE id = 'ok'`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO requests (id, session, kind, drafts) VALUES ('r', 'gone', 'feedback', '[]')`); err != nil {
		t.Fatal(err)
	}
	rep, err = s.Check(bg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Problems) != 2 || !strings.Contains(rep.Problems[0], "past the end") || !strings.Contains(rep.Problems[1], "requests of no session") {
		t.Fatalf("Check = %+v; want the checkpoint past the end and the orphan request", rep)
	}
}

// countFold is a refold standing in for the session core: one checkpoint,
// core/count v1, at the last seq, holding the number of events.
func countFold(ctx context.Context, r store.Reader) ([]store.Checkpoint, error) {
	recs, err := r.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil || len(recs) == 0 {
		return nil, err
	}
	return []store.Checkpoint{{Key: "core/count", StateVersion: 1, Seq: recs[len(recs)-1].Seq,
		State: strconv.Itoa(len(recs))}}, nil
}

// DH-T-STORE-12: after a rebuild the checkpoints equal the incrementally built
// ones; verify reports a row damaged on purpose and changes nothing; rebuild
// repairs it.
func TestRebuildAndVerify(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	for _, id := range []string{"a", "b"} {
		w, err := s.Create(bg, store.Header{ID: id}, store.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for seq := store.Seq(0); seq < 4; seq++ {
			if err := w.Append(bg, store.Batch{First: seq, Records: []store.Record{call(t, seq)},
				Checkpoints: []store.Checkpoint{{Key: "core/count", StateVersion: 1, Seq: seq, State: strconv.Itoa(int(seq) + 1)}}}); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(bg); err != nil {
			t.Fatal(err)
		}
	}
	verify := func() sqlite.RebuildReport {
		t.Helper()
		rep, err := s.Rebuild(bg, sqlite.RebuildOptions{Verify: true, Refold: countFold})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	if rep := verify(); rep.Sessions != 2 || len(rep.Differences) != 0 {
		t.Fatalf("verify of incrementally built checkpoints = %+v; want no difference", rep)
	}
	db := fresh(t, path)
	if _, err := db.Exec(`UPDATE checkpoints SET state = '99' WHERE session = (SELECT uid FROM sessions WHERE id = 'b')`); err != nil {
		t.Fatal(err)
	}
	stateOf := func(id string) string {
		var st string
		if err := db.QueryRow(`SELECT state FROM checkpoints WHERE session = (SELECT uid FROM sessions WHERE id = ?)`, id).Scan(&st); err != nil {
			t.Fatal(err)
		}
		return st
	}
	rep := verify()
	if len(rep.Differences) != 1 || !strings.Contains(rep.Differences[0], `"b"`) {
		t.Fatalf("verify after damage = %+v; want one difference naming b", rep)
	}
	if st := stateOf("b"); st != "99" {
		t.Fatalf("verify changed the damaged row to %q", st)
	}
	rep, err := s.Rebuild(bg, sqlite.RebuildOptions{Session: "b", Refold: countFold})
	if err != nil || rep.Sessions != 1 || len(rep.Differences) != 1 {
		t.Fatalf("Rebuild of b = %+v, %v", rep, err)
	}
	if st := stateOf("b"); st != "4" {
		t.Fatalf("checkpoint after the rebuild = %q; want the incremental value 4", st)
	}
	if rep := verify(); len(rep.Differences) != 0 {
		t.Fatalf("verify after the rebuild = %+v; want no difference", rep)
	}
	if _, err := s.Rebuild(bg, sqlite.RebuildOptions{Session: "missing", Refold: countFold}); codeOf(err) != store.CodeNotFound {
		t.Fatalf("Rebuild of a missing session = %v; want %s", err, store.CodeNotFound)
	}
}

// TestMaintenanceFailsCleanly: with a cancelled context, and after the store is
// closed, every maintenance call returns an error and changes nothing; a refold
// that fails stops a rebuild before anything is replaced; a small delete chunk
// still deletes every row.
func TestMaintenanceFailsCleanly(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "m"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: stamped(t, 3, time.Now())}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	_, events, _, _ := rowsOf(t, path, "m")
	var exp strings.Builder
	if err := s.Export(bg, &exp, "m", sqlite.ExportOptions{}); err != nil {
		t.Fatal(err)
	}
	failing := func(context.Context, store.Reader) ([]store.Checkpoint, error) {
		return nil, fmt.Errorf("refold refused")
	}
	if _, err := s.Rebuild(bg, sqlite.RebuildOptions{Refold: failing}); err == nil || !strings.Contains(err.Error(), "refold refused") {
		t.Fatalf("Rebuild with a failing refold = %v", err)
	}
	calls := func(ctx context.Context, s *sqlite.Store) map[string]error {
		var buf strings.Builder
		_, imp := s.Import(ctx, strings.NewReader(exp.String()), sqlite.ImportOptions{NewID: "m2"})
		_, fol := s.Follow(ctx, "m", 0)
		_, _, acq := s.AcquireDuty(ctx, "d")
		_, run := s.RunDuty(ctx, "d", time.Hour, func(context.Context) error { return nil })
		_, fin := s.FinishDeletions(ctx)
		_, ret := s.Retire(ctx, time.Nanosecond)
		_, chk := s.Check(ctx)
		_, reb := s.Rebuild(ctx, sqlite.RebuildOptions{Refold: countFold})
		return map[string]error{
			"Export": s.Export(ctx, &buf, "m", sqlite.ExportOptions{}), "Import": imp, "Follow": fol,
			"AcquireDuty": acq, "ReleaseDuty": s.ReleaseDuty(ctx, "d"), "RunDuty": run,
			"Delete": s.Delete(ctx, "m", store.DeleteOptions{}), "FinishDeletions": fin,
			"Retire": ret, "Check": chk, "Rebuild": reb,
		}
	}
	cancelled, cancel := context.WithCancel(bg)
	cancel()
	for name, err := range calls(cancelled, s) {
		if err == nil {
			t.Errorf("%s with a cancelled context succeeded", name)
		}
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	for name, err := range calls(bg, s) {
		if err == nil {
			t.Errorf("%s on a closed store succeeded", name)
		}
	}
	if sessions, after, _, _ := rowsOf(t, path, "m"); sessions != 1 || after != events {
		t.Fatalf("the failed calls changed the session: %d sessions, %d events (was %d)", sessions, after, events)
	}
	if sessions, _, _, _ := rowsOf(t, path, "m2"); sessions != 0 {
		t.Fatal("a failed import left the session m2")
	}
	if n := countWhere(t, fresh(t, path), `SELECT count(*) FROM duties`); n != 0 {
		t.Fatalf("a failed call left %d duty rows", n)
	}

	small, err := sqlite.Open(bg, path, sqlite.WithDeleteChunk(1))
	if err != nil {
		t.Fatal(err)
	}
	defer small.Close(bg)
	if err := small.Delete(bg, "m", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	assertGone(t, path, "m")
}
