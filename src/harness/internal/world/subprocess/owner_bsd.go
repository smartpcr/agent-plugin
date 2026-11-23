//go:build unix && !linux

package subprocess

import (
	"os/exec"
	"syscall"
)

// No per-tree container exists here; the process group is the documented owner
// (DH-D-EXEC-2), not a fallback.
func choose() Owner { return Owner{Kind: ProcessGroup} }

func newOwner() (treeOwner, error) { return &groupOwner{}, nil }

// start puts the child in a process group of its own. These systems have no
// parent-death signal, so a killed host leaves the tree running: the documented
// survivors of DH-T-NODEPROC-7.
func (o *groupOwner) start(build func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := build()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	o.pid = cmd.Process.Pid
	return cmd, nil
}
