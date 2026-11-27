//go:build e2e && unix

package e2e

import (
	"os/exec"
	"syscall"
)

// obsStoppable prepares cmd for obsTerminate; on Unix there is nothing to do.
func obsStoppable(*exec.Cmd) {}

// obsTerminate sends cmd the termination signal, SIGTERM.
func obsTerminate(cmd *exec.Cmd) error {
	return cmd.Process.Signal(syscall.SIGTERM)
}
