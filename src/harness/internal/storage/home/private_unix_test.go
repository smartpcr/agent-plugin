//go:build !windows

package home

import (
	"os"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// A directory with no group or other bits but without full owner rwx is not
// mode 0700 and fails the check; Ensure does not repair it.
func TestCheckRequiresOwnerRWXOnDirectories(t *testing.T) {
	for _, mode := range []os.FileMode{0o500, 0o600, 0o300, 0o000} {
		l := New(t.TempDir())
		if err := Ensure(l); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(l.Spill(), mode); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(l.Spill(), 0o700) })
		err := Check(l)
		if !herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), "spill/") {
			t.Errorf("spill/ at %#o: %v", mode, err)
		}
		if err := Ensure(l); !herr.Has(err, ReasonNotPrivate) {
			t.Errorf("Ensure accepted spill/ at %#o: %v", mode, err)
		}
	}
}

// A private file needs owner read: 0600 and 0400 pass, 0200 and 0000 fail.
func TestCheckRequiresOwnerReadOnFiles(t *testing.T) {
	l := New(t.TempDir())
	if err := Ensure(l); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.DB(), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for mode, ok := range map[os.FileMode]bool{0o600: true, 0o400: true, 0o200: false, 0o000: false} {
		if err := os.Chmod(l.DB(), mode); err != nil {
			t.Fatal(err)
		}
		err := Check(l)
		if ok && err != nil {
			t.Errorf("dsh.db at %#o: %v", mode, err)
		}
		if !ok && (!herr.Has(err, ReasonNotPrivate) || !strings.Contains(err.Error(), DBName)) {
			t.Errorf("dsh.db at %#o: %v", mode, err)
		}
	}
}
