//go:build unix

package subprocesstest

import (
	"fmt"
	"syscall"

	"golang.org/x/sys/unix"
)

// detachAttr starts a process as the leader of a new session, the way a daemon
// leaves its parent's session and process group.
func detachAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// checkDetached fails unless pid leads a session that is not this process's.
func checkDetached(pid int) error {
	own, err := unix.Getsid(0)
	if err != nil {
		return fmt.Errorf("getsid(self): %w", err)
	}
	sid, err := unix.Getsid(pid)
	if err != nil {
		return fmt.Errorf("getsid(%d): %w", pid, err)
	}
	if sid != pid || sid == own {
		return fmt.Errorf("pid %d is in session %d (this process: %d), not a session of its own", pid, sid, own)
	}
	return nil
}
