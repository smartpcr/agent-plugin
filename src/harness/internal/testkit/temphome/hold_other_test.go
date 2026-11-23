//go:build !windows

package temphome_test

import (
	"os"
	"testing"
)

// holdOpen makes dir read-only, so the file in it cannot be removed, and returns
// the release, which makes it writable again.
func holdOpen(t *testing.T, dir string, _ string) (release func()) {
	t.Helper()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	return func() { _ = os.Chmod(dir, 0o700) }
}
