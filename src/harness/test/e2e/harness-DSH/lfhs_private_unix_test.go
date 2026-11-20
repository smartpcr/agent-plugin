//go:build e2e && !windows

package e2e

import (
	"fmt"
	"os"
)

// lfhsLoosen opens path to everyone, as TC-06b does on Unix.
func lfhsLoosen(path string) error { return os.Chmod(path, 0o777) }

// lfhsAssertPrivate independently requires mode bits 0700 with no group/other bits.
func lfhsAssertPrivate(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if perm := info.Mode().Perm(); perm != 0o700 || perm&0o077 != 0 {
		return fmt.Errorf("%s: mode %o, want 0700", path, perm)
	}
	return nil
}
