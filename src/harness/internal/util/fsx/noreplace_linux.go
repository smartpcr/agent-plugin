//go:build linux

package fsx

import (
	"errors"

	"golang.org/x/sys/unix"
)

// renameNoReplace is renameat2(RENAME_NOREPLACE); a kernel or filesystem without it
// falls back to link-then-remove.
func renameNoReplace(stage, target string, _ bool) error {
	err := unix.Renameat2(unix.AT_FDCWD, stage, unix.AT_FDCWD, target, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return linkThenRemove(stage, target)
	}
	return err
}
