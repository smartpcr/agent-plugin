//go:build windows

package fsx

import (
	"path/filepath"

	"golang.org/x/sys/windows"
)

// IsRemote reports whether path lies on a network drive, where change
// notification is unreliable and a watcher should poll.
func IsRemote(path string) (bool, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false, err
	}
	p, err := windows.UTF16PtrFromString(abs)
	if err != nil {
		return false, err
	}
	buf := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &buf[0], uint32(len(buf))); err != nil {
		return false, err
	}
	return windows.GetDriveType(&buf[0]) == windows.DRIVE_REMOTE, nil
}
