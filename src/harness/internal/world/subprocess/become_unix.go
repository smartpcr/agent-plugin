//go:build unix

package subprocess

import (
	"syscall"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// become replaces the process; it returns only when execve fails. The kept
// channels are inherited descriptors without close-on-exec, so they stay open.
func become(argv, env []string, _ []uintptr, _ func()) (int, error) {
	err := syscall.Exec(argv[0], argv, env)
	return 0, herr.Wrap(ReasonStartFailed, err, "exec "+argv[0])
}
