package home

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// TC-06b / DH-D-STORAGE-2: an empty home is created private; loosening spill/ makes
// the next check fail naming spill/.
func TestHomeCreatedPrivate(t *testing.T) {
	l := New(t.TempDir())
	if err := Ensure(l); err != nil {
		t.Fatal(err)
	}
	for _, dir := range l.Dirs() {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			t.Fatalf("%s: %v", dir, err)
		}
		if err := CheckPrivate(dir); err != nil {
			t.Errorf("%s: %v", dir, err)
		}
	}
	if err := Check(l); err != nil {
		t.Fatal(err)
	}
	loosen(t, l.Spill())
	err := Check(l)
	if !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "spill/") {
		t.Fatalf("after loosening spill/: %v", err)
	}
	if err := Ensure(l); !herr.Has(err, ReasonNotPrivate) {
		t.Fatalf("Ensure repaired or accepted a loosened directory: %v", err)
	}
}

// A world-readable home that already holds data is reported, not repaired.
func TestEnsureRefusesLoosenedExistingHome(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "keep"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	loosen(t, root)
	if err := Ensure(New(root)); !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "the home") {
		t.Fatalf("loosened existing home: %v", err)
	}
}

func TestEnsureCreatesMissingParents(t *testing.T) {
	l := New(filepath.Join(t.TempDir(), "a", "home"))
	if err := Ensure(l); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(l); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
}

func TestCheckMissingAndNotDirectory(t *testing.T) {
	l := New(t.TempDir())
	if err := Ensure(l); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(l.Reports()); err != nil {
		t.Fatal(err)
	}
	if err := Check(l); !herr.Has(err, ReasonIO) || !strings.Contains(err.Error(), "reports/") {
		t.Fatalf("missing reports/: %v", err)
	}
	if err := os.WriteFile(l.Reports(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(l); !herr.Has(err, ReasonNotDirectory) || !strings.Contains(err.Error(), "reports/") {
		t.Fatalf("file as reports/: %v", err)
	}
}

// A database file that others can read fails the check, naming dsh.db.
func TestCheckDatabaseFile(t *testing.T) {
	l := New(t.TempDir())
	if err := Ensure(l); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.DB(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Check(l); err != nil {
		t.Fatalf("private dsh.db: %v", err)
	}
	loosen(t, l.DB())
	if err := Check(l); !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), DBName) {
		t.Fatalf("loosened dsh.db: %v", err)
	}
}

func TestLayoutPaths(t *testing.T) {
	root := filepath.FromSlash("/h/.dsh")
	l := New(root + string(filepath.Separator))
	for got, want := range map[string]string{
		l.Root(): root, l.DB(): filepath.Join(root, "dsh.db"), l.Run(): filepath.Join(root, "run"),
		l.Spill(): filepath.Join(root, "spill"), l.Blobs(): filepath.Join(root, "blobs"),
		l.Config(): filepath.Join(root, "config"), l.Reports(): filepath.Join(root, "reports"),
		l.Secrets(): filepath.Join(root, "secrets"),
	} {
		if got != want {
			t.Errorf("%s, want %s", got, want)
		}
	}
}

func TestPrivateDirAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatalf("an existing private directory: %v", err)
	}
	f, err := CreatePrivateFile(filepath.Join(dir, "f"))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(f); err != nil {
		t.Fatalf("a created private file: %v", err)
	}
	f.Close()
	if _, err := CreatePrivateFile(filepath.Join(dir, "f")); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("creating an existing file = %v", err)
	}
	if err := EnsurePrivateDir(filepath.Join(dir, "f")); !herr.Has(err, ReasonNotDirectory) {
		t.Fatalf("EnsurePrivateDir over a file = %v", err)
	}
}

func TestSafeSegment(t *testing.T) {
	for in, want := range map[string]string{
		"report.csv": "report.csv", "": "fb", "..": "fb", "a/b": "a_b", `a\b`: "a_b",
	} {
		if got := SafeSegment(in, 20, "fb"); got != want {
			t.Errorf("SafeSegment(%q) = %q, want %q", in, got, want)
		}
	}
	for _, in := range []string{"CON", "nul.txt", "com1", "LPT9.log", strings.Repeat("x", 50), "a\x00b", " . "} {
		got := SafeSegment(in, 20, "fb")
		if len(got) > 20 || strings.ContainsAny(got, "/\\\x00") || got == "." || got == ".." || isDeviceName(got) {
			t.Errorf("SafeSegment(%q) = %q", in, got)
		}
	}
}
