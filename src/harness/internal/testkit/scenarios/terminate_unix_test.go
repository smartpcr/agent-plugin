//go:build scenario && !windows

package scenarios

import (
	"syscall"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// hostCommand is bin and args as they are: on POSIX the binary is the tree's root
// and its pid is the host's.
func hostCommand(_ *testing.T, env []string, bin string, args ...string) ([]string, []string, func(*launcher.Host) int) {
	return append([]string{bin}, args...), env, func(h *launcher.Host) int { return h.Process.Pid() }
}

// terminate sends the host the termination signal, SIGTERM.
func terminate(t *testing.T, h *dshHost) error {
	t.Helper()
	return syscall.Kill(h.Process.Pid(), syscall.SIGTERM)
}
