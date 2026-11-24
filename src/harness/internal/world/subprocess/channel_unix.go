//go:build unix

package subprocess

import (
	"os/exec"
	"syscall"
)

// prepareExtra has nothing to do on POSIX: os/exec duplicates each file into its
// descriptor in the child.
func prepareExtra([]Channel) error { return nil }

// addExtra makes the channels descriptors 3, 4, ... of the child.
func addExtra(cmd *exec.Cmd, extra []Channel) {
	for _, c := range extra {
		cmd.ExtraFiles = append(cmd.ExtraFiles, c.File)
	}
}

func childFD(_ []Channel, i int) uintptr { return uintptr(3 + i) }

// detachCmd starts the child in a session of its own: it leaves the host's
// process group and controlling terminal, so no signal to them reaches it.
func detachCmd(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}

// startDetached starts cmd; there is nothing to retry on POSIX.
func startDetached(build func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := build()
	detachCmd(cmd)
	return cmd, cmd.Start()
}
