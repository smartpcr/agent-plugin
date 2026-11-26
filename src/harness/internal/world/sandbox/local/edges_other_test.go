//go:build !windows

package local_test

import (
	"os"
	"path/filepath"
	"testing"
)

// unresolvable is an absolute, clean path that cannot be resolved for a reason
// other than not existing: a path through a regular file (ENOTDIR).
func unresolvable(t *testing.T) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(file, "sub")
}
