//go:build unix

package cli

import (
	"os/exec"
	"syscall"
	"testing"
)

// stoppable prepares cmd for terminateProcess; on Unix there is nothing to do.
func stoppable(*exec.Cmd) {}

// terminateProcess sends cmd the termination signal, SIGTERM.
func terminateProcess(_ *testing.T, cmd *exec.Cmd) error {
	return cmd.Process.Signal(syscall.SIGTERM)
}
