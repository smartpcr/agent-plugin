package home

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// EnsurePrivateDir needs its parent, and adopts an existing directory only when
// it is already private: a loosened one is reported, not repaired.
func TestEnsurePrivateDirRefusals(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent", "p")
	if err := EnsurePrivateDir(missing); !herr.Has(err, ReasonIO) || !strings.Contains(err.Error(), "create p") {
		t.Fatalf("EnsurePrivateDir below a missing parent = %v", err)
	}
	if _, err := os.Lstat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused EnsurePrivateDir left %s: %v", missing, err)
	}

	dir := filepath.Join(t.TempDir(), "p")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	loosen(t, dir)
	if err := EnsurePrivateDir(dir); !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "p is not private") {
		t.Fatalf("EnsurePrivateDir over a loosened directory = %v", err)
	}
	if err := CheckPrivate(dir); !herr.Has(err, ReasonNotPrivate) {
		t.Fatalf("EnsurePrivateDir repaired the loosened directory: %v", err)
	}
}

// CheckPrivate and CheckPrivateFile report a loosened file by its base name;
// CheckPrivateFile on a closed file is an I/O error.
func TestCheckPrivateOfALoosenedFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "f")
	f, err := CreatePrivateFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	loosen(t, path)
	if err := CheckPrivate(path); !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "home: f is not private") {
		t.Fatalf("CheckPrivate of a loosened file = %v", err)
	}
	if err := CheckPrivateFile(f); !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "home: f is not private") {
		t.Fatalf("CheckPrivateFile of a loosened file = %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivateFile(f); !herr.Has(err, ReasonIO) {
		t.Fatalf("CheckPrivateFile of a closed file = %v", err)
	}
}

// A symbolic link to a private file is not private itself: CheckPrivate never
// follows a link.
func TestCheckPrivateRefusesASymlink(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "p")
	if err := EnsurePrivateDir(dir); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "f")
	f, err := CreatePrivateFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("this host cannot create a symbolic link: %v", err)
	}
	if err := CheckPrivate(link); !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("CheckPrivate of a link = %v", err)
	}
}

// A home path that is a file is neither adopted nor replaced: Ensure fails and
// the file keeps its bytes.
func TestEnsureOverAFile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home")
	if err := os.WriteFile(root, []byte("not a home"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Ensure(New(root)); err == nil {
		t.Fatal("Ensure adopted a file as the home")
	}
	if data, err := os.ReadFile(root); err != nil || string(data) != "not a home" {
		t.Fatalf("the file after Ensure: %q, %v", data, err)
	}
}

// A device name made safe by a leading '_' is cut back to max, and a dot left at
// the cut is dropped.
func TestSafeSegmentCutsADeviceNameToMax(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"CON", 3, "_CO"},
		{"con.x", 5, "_con"},
		{"nul.txt", 4, "_nul"},
		{"LPT1", 20, "_LPT1"},
	} {
		if got := SafeSegment(c.in, c.max, "fb"); got != c.want {
			t.Errorf("SafeSegment(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
		}
	}
}
