//go:build linux

package subprocess

import (
	"os/exec"
	"syscall"
)

// start puts the child in a process group of its own and asks the kernel to
// SIGKILL it when the host dies (PR_SET_PDEATHSIG), so a host killed without
// warning still takes its direct child with it (DH-T-NODEPROC-7, M1-G-BOOT-5).
// The rung is weak: a descendant that leaves the group, or that the child starts
// before it is killed, is not reached; exact ownership is the control group's.
//
// The death signal fires when the thread that started the child exits, not the
// process, so the start goes through startLocked's thread, which lives as long as
// the host. The control-group rung's fallback (cgroupOwner.fallBack) starts here
// too, so it keeps the signal.
func (o *groupOwner) start(build func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := build()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := startLocked(cmd); err != nil {
		return nil, err
	}
	o.pid = cmd.Process.Pid
	return cmd, nil
}
