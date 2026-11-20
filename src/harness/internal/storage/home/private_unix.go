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

// checkPrivate requires the current user as owner and no group or other bits.
func checkPrivate(_ string, info fs.FileInfo) string {
	if st, ok := info.Sys().(*syscall.Stat_t); ok && int(st.Uid) != os.Geteuid() {
		return fmt.Sprintf("owned by uid %d, not the current user", st.Uid)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return fmt.Sprintf("mode %#o grants group or others access; want %#o", perm, perm&0o700)
	}
	return ""
}
