//go:build unix

package subprocesstest

import (
	"errors"
	"os/signal"
	"syscall"
)

func ignoreStop() { signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP) }

// Exited reports whether the process with pid has exited: it is gone, or it is a
// zombie its parent has not reaped yet. A tree owner promises that every member
// exited; an orphan is reaped by its reaper (init, or a subreaper such as a
// systemd scope's manager) on its own time, so a test of the owner checks Exited,
// not Alive.
func Exited(pid int) bool { return !Alive(pid) || zombie(pid) }

// Alive reports whether a process with pid exists; an unreaped zombie counts.
func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
