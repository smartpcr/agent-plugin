//go:build unix

package local_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
)

// Scenario: Spill files are private (DH-D-STORAGE-5, TC-08), Unix: the saved
// file is mode 0600 inside 0700 directories, whatever the umask.
func TestSpillFilesArePrivate(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)

	l := newHome(t)
	sp := newStore(t, l)
	_, ref, err := spill.Save(bg, sp, "s1", "out.txt", []byte("0123456789"), 2)
	if err != nil {
		t.Fatal(err)
	}
	file, ok := sp.HostPath(ref.Path)
	if !ok {
		t.Fatal("no host path")
	}
	want := map[string]os.FileMode{file: 0o600, filepath.Dir(file): 0o700, l.Spill(): 0o700}
	for path, mode := range want {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != mode {
			t.Fatalf("%s: mode %#o, want %#o", path, got, mode)
		}
	}
}
