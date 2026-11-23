package workspace_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

var bg = context.Background()

func openDB(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(bg, path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	t.Cleanup(func() { _ = s.Close(bg) })
	return s
}

// TC-08: Machine state survives restart. A workspace registered in a temp home
// is read back with identical fields after the store is closed and reopened.
//
//dsh:test DH-T-STORAGE-1
func TestMachineStateSurvivesRestart(t *testing.T) {
	home := t.TempDir()
	dbPath := filepath.Join(home, "dsh.db")
	folder := filepath.Join(home, "project")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2025, 3, 4, 5, 6, 7, 891011121, time.FixedZone("x", 3600))
	db := openDB(t, dbPath)
	reg, err := workspace.Open(bg, db, workspace.WithClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	added, err := reg.Add(bg, folder)
	if err != nil {
		t.Fatal(err)
	}
	if added.Title != "project" || added.Position != 0 || added.RequestedPath != folder || !added.CreatedAt.Equal(clock) {
		t.Fatalf("added = %+v", added)
	}
	rev, err := reg.Revision(bg)
	if err != nil || rev != 1 {
		t.Fatalf("revision = %d %v", rev, err)
	}
	if err := db.Close(bg); err != nil {
		t.Fatal(err)
	}

	reg2, err := workspace.Open(bg, openDB(t, dbPath))
	if err != nil {
		t.Fatal(err)
	}
	got, err := reg2.Get(bg, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got != added {
		t.Fatalf("after restart\n got %+v\nwant %+v", got, added)
	}
	list, err := reg2.List(bg)
	if err != nil || len(list) != 1 || list[0] != added {
		t.Fatalf("list = %+v %v", list, err)
	}
	if rev, _ := reg2.Revision(bg); rev != 1 {
		t.Fatalf("revision after restart = %d", rev)
	}
}

func TestRegistryChanges(t *testing.T) {
	home := t.TempDir()
	reg, err := workspace.Open(bg, openDB(t, filepath.Join(home, "dsh.db")))
	if err != nil {
		t.Fatal(err)
	}
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(home, "f.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Add(bg, file); !errors.Is(err, workspace.ErrNotDirectory) {
		t.Fatalf("add a file = %v", err)
	}
	if _, err := reg.Add(bg, filepath.Join(home, "missing")); !errors.Is(err, workspace.ErrNotDirectory) {
		t.Fatalf("add a missing folder = %v", err)
	}
	wa, err := reg.Add(bg, a)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := reg.Add(bg, b)
	if err != nil || wb.Position != 1 {
		t.Fatalf("second add = %+v %v", wb, err)
	}
	if again, err := reg.Add(bg, filepath.Join(a, ".", "")); !errors.Is(err, workspace.ErrExists) || again.ID != wa.ID {
		t.Fatalf("re-add = %+v %v", again, err)
	}
	if got, err := reg.ByPath(bg, a); err != nil || got.ID != wa.ID {
		t.Fatalf("by path = %+v %v", got, err)
	}
	renamed, err := reg.Rename(bg, wa.ID, "  Alpha ")
	if err != nil || renamed.Title != "Alpha" {
		t.Fatalf("rename = %+v %v", renamed, err)
	}
	if err := reg.Remove(bg, wa.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(a); err != nil {
		t.Fatalf("remove touched the folder: %v", err)
	}
	if _, err := reg.Get(bg, wa.ID); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("get removed = %v", err)
	}
	if err := reg.Remove(bg, wa.ID); !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("remove twice = %v", err)
	}
	if rev, _ := reg.Revision(bg); rev != 4 {
		t.Fatalf("revision = %d, want 4 (add, add, rename, remove)", rev)
	}
	list, _ := reg.List(bg)
	if len(list) != 1 || list[0].ID != wb.ID {
		t.Fatalf("list = %+v", list)
	}
}
