//go:build windows

package subprocesstest

import "golang.org/x/sys/windows"

// stillActive is STILL_ACTIVE, the exit code of a running process.
const stillActive = 259

// ignoreStop does nothing: a pipe child has no polite stop signal on Windows.
func ignoreStop() {}

// Exited reports whether the process with pid has exited. Windows keeps no
// zombie: a process that exited is not Alive.
func Exited(pid int) bool { return !Alive(pid) }

// Alive reports whether a process with pid is still running.
func Alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	return code == stillActive
}
