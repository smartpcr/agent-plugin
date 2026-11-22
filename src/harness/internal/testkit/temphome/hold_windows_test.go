package temphome_test

import (
	"os"
	"testing"
)

// holdOpen keeps file open, which makes its directory unremovable on Windows,
// and returns the release.
func holdOpen(t *testing.T, _ string, file string) (release func()) {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	return func() { _ = f.Close() }
}
