package views_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// storedLog is id's committed log, header and checkpoint rows by key, read
// through the store.
func storedLog(t *testing.T, st store.Store, id string) ([]event.Event, session.Header, map[string]store.Checkpoint) {
	t.Helper()
	r := must(st.OpenRead(bg, id))
	defer r.Close(bg)
	recs := must(r.Read(bg, 0, 0, store.ReadOptions{}))
	evs := make([]event.Event, len(recs))
	for i, rec := range recs {
		evs[i] = event.Of(rec)
	}
	cps := map[string]store.Checkpoint{}
	for _, c := range must(r.Checkpoints(bg)) {
		cps[c.Key] = c
	}
	return evs, r.Header(), cps
}

// everyBuiltinEqualsStored checks, for every built-in view, that its stored row
// equals its fold of evs (refoldEqualsStored) and so does its value in snap.
func everyBuiltinEqualsStored(t *testing.T, b views.Builtins, snap views.Snapshot, h session.Header, evs []event.Event, cps map[string]store.Checkpoint) {
	t.Helper()
	same := func(key string, live, refold any) {
		t.Helper()
		if l, r := must(json.Marshal(live)), must(json.Marshal(refold)); string(l) != string(r) {
			t.Fatalf("%s: the live value differs from the fold of the log:\nlive   %s\nrefold %s", key, l, r)
		}
	}
	same(views.ListSummaryKey, must(b.ListSummary.Get(snap)), refoldEqualsStored(t, views.ListSummaryView(), h, evs, cps))
	same(views.TurnIndexKey, must(b.TurnIndex.Get(snap)), refoldEqualsStored(t, views.TurnIndexView(), h, evs, cps))
	same(views.StatsKey, must(b.Stats.Get(snap)), refoldEqualsStored(t, views.StatsView(), h, evs, cps))
	same(views.EventIndexKey, must(b.EventIndex.Get(snap)), refoldEqualsStored(t, views.EventIndexView(), h, evs, cps))
}

// Scenario: a fork's state equals a fold of its logical log (DH-D-VIEW-2). A
// parent of three turns is forked in the store through a seq inside its third
// turn (Service.Fork, DH-D-SESSION-7, is not yet a member, so the fork is the
// store's ForkOf copy of the prefix). Opened with the built-in views, the fork
// closes the interrupted turn and takes one turn more; then every built-in
// view, live and as stored, equals the fold of the fork's logical log: the
// inherited prefix, the closers and its own turn. The fork inherits through+1
// events (its header's InheritedCount), which its statistics count with its own.
//
//dsh:test DH-T-VIEW-8
func TestForkStateEqualsFoldOfItsLogicalLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	reg := views.New()
	b := must(views.RegisterBuiltins(reg))
	st, svc, stop := boot(t, path, reg)
	defer stop()

	parent := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "parent"}}))
	for n := 1; n <= 3; n++ {
		must(parent.AppendGroup(bg, turnDrafts(n)))
	}
	pevs, _, _ := storedLog(t, st, "parent")
	if err := parent.Close(bg); err != nil {
		t.Fatal(err)
	}
	// Through the third turn's first tool result: its step and turn are open.
	var through event.Seq
	turns := 0
	for _, ev := range pevs {
		if ev.Type() == event.TurnStartType.Name() {
			turns++
		}
		if turns == 3 && ev.Type() == event.ToolResult.Name() {
			through = ev.Seq()
			break
		}
	}
	w := must(st.Create(bg, store.Header{ID: "fork"}, store.CreateOptions{ForkOf: &store.ForkOf{ID: "parent", Through: through}}))
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}

	fork, rep, err := svc.Open(bg, nil, "fork", session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Interrupted || len(rep.Closers) == 0 {
		t.Fatalf("the fork through an open turn opened with %+v", rep)
	}
	must(fork.AppendGroup(bg, turnDrafts(4)))
	snap := must(reg.Read(bg, fork))
	evs, h, cps := storedLog(t, st, "fork")
	if err := fork.Close(bg); err != nil {
		t.Fatal(err)
	}
	if h.ForkSource != "parent" || h.ForkThrough != through || h.InheritedCount != int(through)+1 {
		t.Fatalf("fork header = %+v, want parent through %d inheriting %d", h, through, through+1)
	}
	for i := range h.InheritedCount {
		if evs[i].Type() != pevs[i].Type() || evs[i].Raw() != pevs[i].Raw() {
			t.Fatalf("the fork's seq %d (%s) is not the parent's (%s)", i, evs[i].Type(), pevs[i].Type())
		}
	}
	if snap.AsOf != event.Seq(len(evs)) {
		t.Fatalf("the fork's read is at %d, its log ends at %d", snap.AsOf, len(evs))
	}
	everyBuiltinEqualsStored(t, b, snap, h, evs, cps)

	stats := must(b.Stats.Get(snap))
	own := len(evs) - h.InheritedCount
	if stats.Events != h.InheritedCount+own || own <= len(rep.Closers) || stats.Turns != 4 {
		t.Fatalf("the fork's statistics count %d events and %d turns; it inherits %d and adds %d", stats.Events, stats.Turns, h.InheritedCount, own)
	}
	idx := must(b.TurnIndex.Get(snap))
	if len(idx.Turns) != 4 {
		t.Fatalf("the fork's turn index = %+v, want four turns", idx)
	}
	for _, e := range idx.Turns {
		if !e.Settled {
			t.Fatalf("turn %d of the fork is unsettled after its open closed it: %+v", e.Turn, e)
		}
	}
}

// derivedRows is the count of checkpoint rows of the session row uid, read in
// one transaction of the store.
func derivedRows(t *testing.T, st *sqlite.Store, uid int64) int {
	t.Helper()
	var n int
	if err := st.ReadTx(bg, func(tx *sql.Tx) error {
		return tx.QueryRowContext(bg, `SELECT count(*) FROM checkpoints WHERE session = ?`, uid).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	return n
}

// sessionUID is the session row of id.
func sessionUID(t *testing.T, st *sqlite.Store, id string) int64 {
	t.Helper()
	var uid int64
	if err := st.ReadTx(bg, func(tx *sql.Tx) error {
		return tx.QueryRowContext(bg, `SELECT uid FROM sessions WHERE id = ?`, id).Scan(&uid)
	}); err != nil {
		t.Fatal(err)
	}
	return uid
}

// Scenario: a deleted session leaves no derived row (DH-D-VIEW-2). Two sessions
// with the built-in views' rows; one is deleted through the store: its
// checkpoint rows (every built-in view's and the core's) are gone, it is not
// found, the store's check finds no row of no session, and the other session's
// rows are untouched and still equal its fold.
//
//dsh:test DH-T-VIEW-8
func TestDeletedSessionLeavesNoDerivedRow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dsh.db")
	reg := views.New()
	b := must(views.RegisterBuiltins(reg))
	st, svc, stop := boot(t, path, reg)
	defer stop()
	for _, id := range []string{"s-del", "s-keep"} {
		h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}}))
		fillSession(t, h, 60)
		if err := h.Close(bg); err != nil {
			t.Fatal(err)
		}
	}
	_, _, cps := storedLog(t, st, "s-del")
	for _, key := range []string{views.ListSummaryKey, views.TurnIndexKey, views.StatsKey, views.EventIndexKey} {
		if _, ok := cps[key]; !ok {
			t.Fatalf("s-del has no %s row before the delete: %v", key, cps)
		}
	}
	uid := sessionUID(t, st, "s-del")
	if n := derivedRows(t, st, uid); n < 4 {
		t.Fatalf("s-del holds %d checkpoint rows before the delete", n)
	}

	if err := st.Delete(bg, "s-del", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	reg.Forget("s-del")
	if n := derivedRows(t, st, uid); n != 0 {
		t.Fatalf("the deleted session left %d checkpoint rows", n)
	}
	if _, err := st.OpenRead(bg, "s-del"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("OpenRead of the deleted session = %v, want not found", err)
	}
	rep := must(st.Check(bg))
	if len(rep.Problems) != 0 || len(rep.Deleting) != 0 {
		t.Fatalf("the store's check after the delete: %+v", rep)
	}

	evs, h, kept := storedLog(t, st, "s-keep")
	r, release, err := svc.OpenRead(bg, "s-keep")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	everyBuiltinEqualsStored(t, b, must(reg.Read(bg, r)), h, evs, kept)
}
