//go:build !windows

package home

import (
	"os"
	"testing"
)

// loosen grants everybody access, as TC-06b does on Unix.
func loosen(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o777); err != nil {
		t.Fatal(err)
	}
}
