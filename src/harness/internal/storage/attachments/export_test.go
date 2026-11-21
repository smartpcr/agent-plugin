package attachments

import "testing"

// SetSyncDir replaces the directory sync for one test.
func SetSyncDir(t testing.TB, f func(string) error) {
	old := syncDir
	syncDir = f
	t.Cleanup(func() { syncDir = old })
}
