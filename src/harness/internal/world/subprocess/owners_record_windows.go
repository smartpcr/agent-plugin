//go:build windows

package subprocess

import (
	"errors"
	"strconv"

	"golang.org/x/sys/windows"
)

// stillActive is STILL_ACTIVE, the exit code of a running process.
const stillActive = 259

// procIdentity reports whether pid runs and its creation time. A process this
// user may not query is reported running with no identity.
func procIdentity(pid int) (bool, string) {
	if pid <= 0 {
		return false, ""
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return errors.Is(err, windows.ERROR_ACCESS_DENIED), ""
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil || code != stillActive {
		return false, ""
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		return true, ""
	}
	return true, strconv.FormatInt(created.Nanoseconds(), 10)
}

// survivors is the direct child when it is still the same process. The job of a
// host that died closed with it and, with kill-on-close, ended the tree, so a
// survivor here is one that had not yet been ended.
func survivors(e ownerEntry) []int {
	if alive, start := procIdentity(e.Pid); alive && sameStart(e.PidStart, start) {
		return []int{e.Pid}
	}
	return nil
}
