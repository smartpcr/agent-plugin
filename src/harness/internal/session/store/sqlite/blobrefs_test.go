package sqlite_test

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
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
	for _, q := range []string{`DROP TABLE blob_refs`, `DROP TABLE lineage`, `PRAGMA user_version = 2`} {
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

// forkedSpill is a home whose session "p" spilled two files, cited at seqs 1 and
// 2, and whose fork "f" inherited the prefix through seq 1: it cites the first
// file only. It returns the home, the open store, the spill store reading with
// the store's lineage, the two ids and the cited file's bytes.
func forkedSpill(t *testing.T) (home.Layout, *sqlite.Store, *local.Store, string, string, []byte) {
	t.Helper()
	l := temphome.New(t)
	s, err := sqlite.OpenHome(bg, l)
	if err != nil {
		t.Fatal(err)
	}
	sp, err := local.New(l, local.DefaultConfig, local.WithLineage(local.ForkLineage(s)))
	if err != nil {
		t.Fatal(err)
	}
	full := bytes.Repeat([]byte("0123456789abcdef"), 1<<16)
	_, early, err := spill.Save(bg, sp, "p", "early.log", full, 64)
	if err != nil {
		t.Fatal(err)
	}
	_, late, err := spill.Save(bg, sp, "p", "late.log", []byte(string(full[:4096])), 64)
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(bg, store.Header{ID: "p"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0), spilled(t, 1, early.Path), spilled(t, 2, late.Path)}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
	f, err := s.Create(bg, store.Header{ID: "f"}, store.CreateOptions{ForkOf: &store.ForkOf{ID: "p", Through: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(bg); err != nil {
		t.Fatal(err)
	}
	return l, s, sp, early.Path, late.Path, full
}

// readSpill is the bytes of spill file id as caller reads them.
func readSpill(t *testing.T, sp *local.Store, caller, id string) []byte {
	t.Helper()
	r, err := sp.Open(bg, caller, id)
	if err != nil {
		t.Fatalf("fork read of %s: %v", id, err)
	}
	defer r.Close()
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func spillGone(t *testing.T, l home.Layout, id string) bool {
	t.Helper()
	_, err := os.Lstat(filepath.Join(l.Spill(), filepath.FromSlash(id)))
	return errors.Is(err, os.ErrNotExist)
}

// storage.md §6.1 / DH-D-STORAGE-5: deleting a session keeps the spill files a
// fork cites (through its inherited blob_refs rows) and removes the rest; the
// fork reads the kept file byte-identical. Deleting the fork then removes the
// file and the emptied directory, as nothing active cites or owns them.
func TestDeletionKeepsSpillFilesAForkCites(t *testing.T) {
	l, s, sp, early, late, full := forkedSpill(t)
	defer s.Close(bg)
	if err := s.Delete(bg, "p", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if got := readSpill(t, sp, "f", early); !bytes.Equal(got, full) {
		t.Fatalf("the fork read %d bytes, not the %d its parent spilled", len(got), len(full))
	}
	if !spillGone(t, l, late) {
		t.Fatal("the deleted parent's uncited spill file is still there")
	}
	if err := s.Delete(bg, "f", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if !spillGone(t, l, early) || !spillGone(t, l, sqlite.SpillName("p")) {
		t.Fatal("deleting the last citing fork left the file or its directory")
	}
}

// The same, when the parent's deletion is interrupted after its spill step: the
// next OpenHome finishes the tombstone and still keeps the fork's file.
func TestInterruptedDeletionKeepsSpillFilesAForkCites(t *testing.T) {
	l, s, _, early, late, full := forkedSpill(t)
	restore := sqlite.SetRemoveAll(func(path string) error {
		_ = os.RemoveAll(path)
		return os.ErrPermission // the crash, after the removal took effect
	})
	err := s.Delete(bg, "p", store.DeleteOptions{})
	restore()
	if err == nil {
		t.Fatal("the interrupted deletion reported success")
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if n := countWhere(t, fresh(t, l.DB()), `SELECT count(*) FROM sessions WHERE id = 'p' AND state = 'deleting'`); n != 1 {
		t.Fatalf("%d tombstones of the parent; the test does not exercise the resumed deletion", n)
	}
	r, err := sqlite.OpenHome(bg, l)
	if err != nil {
		t.Fatalf("OpenHome finishing the deletion: %v", err)
	}
	defer r.Close(bg)
	assertGone(t, l.DB(), "p")
	sp2, err := local.New(l, local.DefaultConfig, local.WithLineage(local.ForkLineage(r)))
	if err != nil {
		t.Fatal(err)
	}
	if got := readSpill(t, sp2, "f", early); !bytes.Equal(got, full) {
		t.Fatalf("after the resumed deletion the fork read %d bytes, want %d", len(got), len(full))
	}
	if !spillGone(t, l, late) {
		t.Fatal("the resumed deletion left the parent's uncited spill file")
	}
}

// The spill store's Delete, with the session store as its Citer, keeps what a
// fork cites, as a session deletion does.
func TestSessionStoreIsTheSpillCiter(t *testing.T) {
	l, s, _, early, late, full := forkedSpill(t)
	defer s.Close(bg)
	sp, err := local.New(l, local.DefaultConfig, local.WithLineage(local.ForkLineage(s)), local.WithCiter(s))
	if err != nil {
		t.Fatal(err)
	}
	if err := sp.Delete(bg, "p"); err != nil {
		t.Fatal(err)
	}
	if got := readSpill(t, sp, "f", early); !bytes.Equal(got, full) {
		t.Fatal("the spill Delete removed a file the fork cites")
	}
	if !spillGone(t, l, late) {
		t.Fatal("the spill Delete kept an uncited file")
	}
}

// storage.md §6.1, spill.go (forks read their owner's files "transitively"): for
// p -> f -> g, where g inherits p's spilled prefix through f, deleting p and
// then f keeps the file, and g still reads it byte-identical, also through a
// reopened home, because g's ancestry was recorded when it was forked. An
// unrelated session is still refused.
func TestNestedForkReadsInheritedSpillAfterAncestorsAreDeleted(t *testing.T) {
	l, s, _, early, _, full := forkedSpill(t)
	g, err := s.Create(bg, store.Header{ID: "g"}, store.CreateOptions{ForkOf: &store.ForkOf{ID: "f", Through: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Close(bg); err != nil {
		t.Fatal(err)
	}
	if err := s.Seed(bg, store.Header{ID: "stranger"}, []store.Record{call(t, 0)}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"p", "f"} {
		if err := s.Delete(bg, id, store.DeleteOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	r, err := sqlite.OpenHome(bg, l)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close(bg)
	if got, err := r.Ancestors(bg, "g"); err != nil || len(got) != 2 || got[0] != "f" || got[1] != "p" {
		t.Fatalf("Ancestors(g) = %v, %v; want [f p] after both were deleted", got, err)
	}
	sp, err := local.New(l, local.DefaultConfig, local.WithLineage(local.ForkLineage(r)))
	if err != nil {
		t.Fatal(err)
	}
	if got := readSpill(t, sp, "g", early); !bytes.Equal(got, full) {
		t.Fatalf("the grandchild read %d bytes, want the %d its grandparent spilled", len(got), len(full))
	}
	if _, err := sp.Open(bg, "stranger", early); !herr.Has(err, spill.ReasonNotOwner) {
		t.Fatalf("an unrelated session's read = %v; want %s", err, spill.ReasonNotOwner)
	}
	if err := r.Delete(bg, "g", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if !spillGone(t, l, early) {
		t.Fatal("deleting the last citing descendant left the file")
	}
}

// link makes a directory link at name to target: a symlink, or on Windows,
// where that needs a privilege, a junction. It skips the test when neither works.
func link(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err == nil {
		return
	}
	if runtime.GOOS == "windows" {
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", name, target).CombinedOutput(); err == nil {
			return
		} else {
			t.Skipf("no directory link: %v %s", err, out)
		}
	}
	t.Skip("no directory link")
}

// The orphan cleanup of a deletion stays inside the spill directory: an
// inherited reference naming a directory that is a link planted in the spill
// directory to an outside one leaves the outside file alone.
func TestDeletionNeverFollowsAPlantedSpillLink(t *testing.T) {
	l := temphome.New(t)
	s, err := sqlite.OpenHome(bg, l)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(bg)
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	planted := sqlite.SpillName("gone")
	link(t, outside, filepath.Join(l.Spill(), planted))
	if err := s.Seed(bg, store.Header{ID: "x"}, []store.Record{call(t, 0), spilled(t, 1, planted+"/sentinel")}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(bg, "x", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(sentinel); err != nil || string(b) != "keep" {
		t.Fatalf("the file outside the spill directory: %q, %v; want it untouched", b, err)
	}
}
