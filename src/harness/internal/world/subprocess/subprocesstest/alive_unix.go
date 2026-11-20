//go:build unix

package subprocesstest

import (
	"errors"
	"os/signal"
	"syscall"
)

func ignoreStop() { signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP) }

// Alive reports whether a process with pid exists; an unreaped zombie counts.
func Alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
