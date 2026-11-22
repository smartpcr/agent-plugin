package blob

import (
	"io/fs"
	"testing"
)

// SetSyncDir replaces the directory sync for one test.
func SetSyncDir(t testing.TB, f func(string) error) {
	old := syncDir
	syncDir = f
	t.Cleanup(func() { syncDir = old })
}

// SetAfterLockShared installs f, run each time a saver has taken gc.lock shared,
// for one test. Install it while no saver runs.
func SetAfterLockShared(t testing.TB, f func()) {
	old := afterLockShared
	afterLockShared = f
	t.Cleanup(func() { afterLockShared = old })
}

// SetChmod replaces the mode change for one test.
func SetChmod(t testing.TB, f func(string, fs.FileMode) error) {
	old := chmod
	chmod = f
	t.Cleanup(func() { chmod = old })
}
