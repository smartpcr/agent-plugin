package sqlite_test

import (
	"fmt"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// spilled is a tool result at seq whose full output went to spill file id.
func spilled(t *testing.T, seq store.Seq, id string) store.Record {
	t.Helper()
	msg, err := vocab.RestoreMessage(fmt.Sprintf("r%d", seq), vocab.RoleTool, nil, vocab.ToolResultBlock("c0", false, vocab.TextPart("head")))
	if err != nil {
		t.Fatal(err)
	}
	d, err := event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: "c0", Message: msg, Spill: &event.Spill{Path: id, Bytes: 5 << 20}})
	if err != nil {
		t.Fatal(err)
	}
	ms++
	r, err := d.Stamp(seq, ms)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func cites(t *testing.T, s *sqlite.Store, id, ref string) bool {
	t.Helper()
	ok, err := s.Cites(bg, id, ref)
	if err != nil {
		t.Fatalf("Cites(%s, %s): %v", id, ref, err)
	}
	return ok
}

// DH-D-STORAGE-3 / DH-D-STORE-2: an appended event that cites a spill file gets
// its blob_refs row in the append; a fork inherits the rows of its prefix and
// keeps them when its parent is deleted, and the deleted session's rows go.
func TestBlobRefsFollowAppendForkAndDelete(t *testing.T) {
	path := dbPath(t)
	s := open(t, path)
	w, err := s.Create(bg, store.Header{ID: "p"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0), spilled(t, 1, "early"), spilled(t, 2, "late")}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	if !cites(t, s, "p", "spill:early") || !cites(t, s, "p", "spill:late") {
		t.Fatal("the appended tool results are not cited")
	}
	if cites(t, s, "p", "spill:other") || cites(t, s, "nobody", "spill:early") {
		t.Fatal("a reference nobody logged is cited")
	}

	f, err := s.Create(bg, store.Header{ID: "f"}, store.CreateOptions{ForkOf: &store.ForkOf{ID: "p", Through: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(bg); err != nil {
		t.Fatal(err)
	}
	if !cites(t, s, "f", "spill:early") || cites(t, s, "f", "spill:late") {
		t.Fatal("the fork does not cite exactly its inherited prefix")
	}

	if err := s.Delete(bg, "p", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if cites(t, s, "p", "spill:early") {
		t.Fatal("a deleted session still cites")
	}
	if !cites(t, s, "f", "spill:early") {
		t.Fatal("the fork lost its reference with its parent")
	}
	if n := countWhere(t, fresh(t, path), `SELECT count(*) FROM blob_refs WHERE ref = 'spill:late'`); n != 0 {
		t.Fatalf("%d blob_refs rows of the deleted session left", n)
	}
}

// A database written before blob_refs existed (layout 2) gets the rows of its
// logged spill references when it is opened by this build.
func TestBlobRefsBackfilledByTheMigration(t *testing.T) {
	path := dbPath(t)
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(bg, store.Header{ID: "old"}, []store.Record{call(t, 0), spilled(t, 1, "kept")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	db := fresh(t, path)
	for _, q := range []string{`DROP TABLE blob_refs`, `PRAGMA user_version = 2`} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if !cites(t, open(t, path), "old", "spill:kept") {
		t.Fatal("the migration did not backfill the logged spill reference")
	}
}
