//go:build windows

package sdk

import "syscall"

// pidGone reports whether pid names no live process: it cannot be opened, or
// it has an exit code.
func pidGone(pid int) bool {
	const queryLimited = 0x1000 // PROCESS_QUERY_LIMITED_INFORMATION
	const stillActive = 259
	h, err := syscall.OpenProcess(queryLimited, false, uint32(pid))
	if err != nil {
		return true
	}
	defer syscall.CloseHandle(h)
	var code uint32
	if syscall.GetExitCodeProcess(h, &code) != nil {
		return true
	}
	return code != stillActive
}
