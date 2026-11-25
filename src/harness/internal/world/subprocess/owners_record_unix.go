//go:build unix && !linux

package subprocess

import (
	"errors"
	"syscall"
)

// procIdentity reports whether pid runs; these systems give no start identity
// without parsing ps, so a reused pid is taken for the recorded one.
func procIdentity(pid int) (bool, string) {
	if pid <= 0 {
		return false, ""
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM), ""
}

// survivors is the direct child while it runs; the other members of its process
// group cannot be listed here.
func survivors(e ownerEntry) []int {
	if alive, _ := procIdentity(e.Pid); alive {
		return []int{e.Pid}
	}
	return nil
}
