package workspace_test

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

// Detach ends a membership and bumps the revision; detaching a session in no
// workspace is a no-op that changes nothing; an unknown session is
// workspace/no-session.
func TestDetach(t *testing.T) {
	home := t.TempDir()
	db := openDB(t, filepath.Join(home, "dsh.db"))
	reg := must(workspace.Open(bg, db))
	a := mkdir(t, filepath.Join(home, "a"))
	wa := must(reg.Add(bg, a))
	newSession(t, db, "s1", a)
	if err := reg.Attach(bg, wa.ID, "s1"); err != nil {
		t.Fatal(err)
	}
	before := must(reg.Revision(bg))
	if err := reg.Detach(bg, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := reg.WorkspaceOf(bg, "s1"); ok || err != nil {
		t.Fatalf("after Detach the session is still in a workspace: %v %v", ok, err)
	}
	if got := must(reg.Members(bg, wa.ID)); len(got) != 0 {
		t.Fatalf("members after Detach = %v", got)
	}
	after := must(reg.Revision(bg))
	if after != before+1 {
		t.Fatalf("Detach moved the revision %d -> %d, want one step", before, after)
	}
	if err := reg.Detach(bg, "s1"); err != nil {
		t.Fatalf("detaching an ungrouped session = %v", err)
	}
	if rev := must(reg.Revision(bg)); rev != after {
		t.Fatalf("a no-op Detach moved the revision to %d", rev)
	}
	if err := reg.Detach(bg, "nope"); !errors.Is(err, workspace.ErrNoSession) {
		t.Fatalf("Detach of an unknown session = %v", err)
	}
}

// Attach refuses an unknown workspace, a session that records no directory or
// one that no longer exists, and a session another workspace holds.
func TestAttachRefusals(t *testing.T) {
	home := t.TempDir()
	db := openDB(t, filepath.Join(home, "dsh.db"))
	reg := must(workspace.Open(bg, db))
	a := mkdir(t, filepath.Join(home, "a"))
	b := mkdir(t, filepath.Join(home, "b"))
	wa, wb := must(reg.Add(bg, a)), must(reg.Add(bg, b))
	newSession(t, db, "nowhere", "")
	newSession(t, db, "gone", filepath.Join(home, "removed"))
	newSession(t, db, "taken", a)

	if err := reg.Attach(bg, "ws_missing", "taken"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("Attach to an unknown workspace = %v", err)
	}
	if err := reg.Attach(bg, wa.ID, "nowhere"); !errors.Is(err, workspace.ErrOutside) || !strings.Contains(err.Error(), "no working directory") {
		t.Fatalf("Attach of a session with no directory = %v", err)
	}
	if err := reg.Attach(bg, wa.ID, "gone"); !errors.Is(err, workspace.ErrOutside) {
		t.Fatalf("Attach of a session whose directory is gone = %v", err)
	}
	// Another process attached "taken" to b while its directory was b's.
	if err := db.WriteTx(bg, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(bg, `INSERT INTO workspace_sessions (session, workspace, attached_at)
			SELECT uid, ?, 0 FROM sessions WHERE id = 'taken'`, wb.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	before := must(reg.Revision(bg))
	if err := reg.Attach(bg, wa.ID, "taken"); !errors.Is(err, workspace.ErrAttached) || !strings.Contains(err.Error(), wb.ID) {
		t.Fatalf("Attach of a session held elsewhere = %v", err)
	}
	if ws, _, _ := reg.WorkspaceOf(bg, "taken"); ws != wb.ID || must(reg.Revision(bg)) != before {
		t.Fatalf("a refused Attach moved the session to %q or the revision", ws)
	}
	if _, err := reg.Members(bg, "ws_missing"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("Members of an unknown workspace = %v", err)
	}
}

// The flag writers refuse an unknown archive state and an unknown session, and
// ClearPending with nothing pending changes nothing.
func TestFlagRefusals(t *testing.T) {
	home := t.TempDir()
	db := openDB(t, filepath.Join(home, "dsh.db"))
	reg := must(workspace.Open(bg, db))
	newSession(t, db, "s", home)
	if err := reg.SetArchive(bg, "s", "frozen"); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("SetArchive(frozen) = %v", err)
	}
	if _, err := reg.TransitionArchive(bg, "s", workspace.Transition{From: "frozen", To: workspace.ArchiveArchived}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("TransitionArchive from frozen = %v", err)
	}
	if _, err := reg.TransitionArchive(bg, "nope", workspace.Transition{To: workspace.ArchivePending, NewOwner: "o"}); !errors.Is(err, workspace.ErrNoSession) {
		t.Fatalf("TransitionArchive of an unknown session = %v", err)
	}
	if err := reg.Pin(bg, "nope", true); !errors.Is(err, workspace.ErrNoSession) {
		t.Fatalf("Pin of an unknown session = %v", err)
	}
	m := workspace.NewMemoryMarks()
	if err := m.SetArchive(bg, "s", "frozen"); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("MemoryMarks.SetArchive(frozen) = %v", err)
	}
	if _, err := m.TransitionArchive(bg, "s", workspace.Transition{To: "frozen"}); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("MemoryMarks.TransitionArchive to frozen = %v", err)
	}
	if f := must(reg.Flags(bg, "s")); f != (workspace.Flags{}) {
		t.Fatalf("refused writes left flags %+v", f)
	}
	before := must(reg.Revision(bg))
	if n, err := reg.ClearPending(bg); err != nil || n != 0 || must(reg.Revision(bg)) != before {
		t.Fatalf("ClearPending with nothing pending = %d, %v, revision %d -> %d", n, err, before, must(reg.Revision(bg)))
	}
}

// The registry refuses an empty or missing path, an empty title and an unknown
// workspace, and Open needs a database.
func TestRegistryRefusals(t *testing.T) {
	home := t.TempDir()
	reg := must(workspace.Open(bg, openDB(t, filepath.Join(home, "dsh.db"))))
	if _, err := workspace.Open(bg, nil); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("Open(nil) = %v", err)
	}
	if _, err := workspace.Resolve("  "); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("Resolve of a blank path = %v", err)
	}
	missing := filepath.Join(home, "missing")
	if _, _, err := reg.EnsureDefault(bg, missing); !errors.Is(err, workspace.ErrNotDirectory) {
		t.Fatalf("EnsureDefault of a missing folder = %v", err)
	}
	if _, err := reg.ByPath(bg, missing); !errors.Is(err, workspace.ErrNotDirectory) {
		t.Fatalf("ByPath of a missing folder = %v", err)
	}
	w := must(reg.Add(bg, mkdir(t, filepath.Join(home, "a"))))
	if _, err := reg.Rename(bg, w.ID, " \t "); !errors.Is(err, workspace.ErrInvalid) {
		t.Fatalf("Rename to a blank title = %v", err)
	}
	if _, err := reg.Rename(bg, "ws_missing", "x"); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("Rename of an unknown workspace = %v", err)
	}
	if got := must(reg.Get(bg, w.ID)); got.Title != "a" {
		t.Fatalf("a refused rename changed the title to %q", got.Title)
	}
}

// busyDB is a database locked past its busy bound.
type busyDB struct{}

func (busyDB) WriteTx(context.Context, func(tx *sql.Tx) error) error {
	return &store.Error{Code: sqlite.CodeBusy, Msg: "locked"}
}

func (busyDB) ReadTx(context.Context, func(tx *sql.Tx) error) error {
	return &store.Error{Code: sqlite.CodeBusy, Msg: "locked"}
}

// A database locked past its busy bound fails every registry call with
// storage/busy.
//
//dsh:test DH-T-STORAGE-2
func TestRegistryOverABusyDatabase(t *testing.T) {
	if _, err := workspace.Open(bg, busyDB{}); !errors.Is(err, kv.ErrBusy) {
		t.Fatalf("Open over a busy database = %v", err)
	}
	// A registry opened while the database was free, used once it is locked.
	home := t.TempDir()
	db := &lockable{Store: openDB(t, filepath.Join(home, "dsh.db"))}
	reg := must(workspace.Open(bg, db))
	w := must(reg.Add(bg, mkdir(t, filepath.Join(home, "a"))))
	db.locked = true
	if _, err := reg.Add(bg, mkdir(t, filepath.Join(home, "b"))); !errors.Is(err, kv.ErrBusy) {
		t.Fatalf("Add = %v", err)
	}
	if _, err := reg.Rename(bg, w.ID, "x"); !errors.Is(err, kv.ErrBusy) {
		t.Fatalf("Rename = %v", err)
	}
	if _, err := reg.Get(bg, w.ID); !errors.Is(err, kv.ErrBusy) {
		t.Fatalf("Get = %v", err)
	}
	if _, err := reg.List(bg); !errors.Is(err, kv.ErrBusy) {
		t.Fatalf("List = %v", err)
	}
	if _, err := reg.Flags(bg, "s"); !errors.Is(err, kv.ErrBusy) {
		t.Fatalf("Flags = %v", err)
	}
	db.locked = false
	if got := must(reg.List(bg)); len(got) != 1 || got[0].ID != w.ID || got[0].Title != "a" {
		t.Fatalf("after the lock: %+v", got)
	}
}

// lockable is a store whose transactions fail as busy while locked is set.
type lockable struct {
	*sqlite.Store
	locked bool
}

func (l *lockable) WriteTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if l.locked {
		return busyDB{}.WriteTx(ctx, fn)
	}
	return l.Store.WriteTx(ctx, fn)
}

func (l *lockable) ReadTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	if l.locked {
		return busyDB{}.ReadTx(ctx, fn)
	}
	return l.Store.ReadTx(ctx, fn)
}
