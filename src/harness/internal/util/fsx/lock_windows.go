//go:build windows

package fsx

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// beyondHigh is the high 32 bits of the offset of the byte a beyond-the-end lock
// takes: far past any file a lock file grows to, so no read of it overlaps.
const beyondHigh = 0x7fffffff

// region is the overlapped structure naming the locked byte.
func region(beyond bool) windows.Overlapped {
	var ol windows.Overlapped
	if beyond {
		ol.OffsetHigh = beyondHigh
	}
	return ol
}

func lockFile(f *os.File, beyond bool) error {
	ol := region(beyond)
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ol)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return &os.PathError{Op: "lock", Path: f.Name(), Err: ErrLocked}
	}
	return err
}

func unlockFile(f *os.File, beyond bool) error {
	ol := region(beyond)
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ol)
}
