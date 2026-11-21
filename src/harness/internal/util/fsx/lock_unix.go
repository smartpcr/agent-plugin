//go:build unix

package fsx

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func lockFile(f *os.File, _, shared bool) error {
	how := unix.LOCK_EX
	if shared {
		how = unix.LOCK_SH
	}
	err := unix.Flock(int(f.Fd()), how|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) {
		return &os.PathError{Op: "lock", Path: f.Name(), Err: ErrLocked}
	}
	return err
}

func unlockFile(f *os.File, _ bool) error {
	return unix.Flock(int(f.Fd()), unix.LOCK_UN)
}
