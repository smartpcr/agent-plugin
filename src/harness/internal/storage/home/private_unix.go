//go:build !windows

package home

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// makePrivate sets mode 0700, whatever the process mask.
func makePrivate(path string) error { return os.Chmod(path, 0o700) }

// checkPrivate requires the current user as owner and no group or other bits; a
// directory needs owner rwx (mode 0700 exactly), a file owner read.
func checkPrivate(_ string, info fs.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Sprintf("owned by uid %d, not the current user", st.Uid)
	}
	perm := info.Mode().Perm()
	if perm&0o077 != 0 {
		return fmt.Sprintf("mode %#o grants group or others access; want %#o", perm, perm&0o700)
	}
	if info.IsDir() && perm != 0o700 {
		return fmt.Sprintf("mode %#o lacks owner read, write or search; want 0700", perm)
	}
	if !info.IsDir() && perm&0o400 == 0 {
		return fmt.Sprintf("mode %#o lacks owner read; want 0600", perm)
	}
	return ""
}

// checkPrivateFile checks an open file by its Fstat.
func checkPrivateFile(f *os.File, info fs.FileInfo) string { return checkPrivate(f.Name(), info) }
