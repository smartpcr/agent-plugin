//go:build scenario && !windows

package scenarios

import (
	"os/exec"
	"syscall"
	"testing"
)

// ownConsole prepares cmd so terminate can reach it; on POSIX there is nothing to do.
func ownConsole(*exec.Cmd) {}

// terminate sends cmd the termination signal, SIGTERM.
func terminate(t *testing.T, cmd *exec.Cmd) error {
	t.Helper()
	return cmd.Process.Signal(syscall.SIGTERM)
}
