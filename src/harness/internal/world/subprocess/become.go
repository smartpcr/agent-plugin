package subprocess

import (
	"path/filepath"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Become turns the calling process into the program argv names, with env as its
// whole environment. It is the last step of the sandbox launch stage
// (world/sandbox/launch, DH-D-SANDBOX-2), after the host has answered "go", and
// no other code calls it: the stage's command was planned and started through
// Start, so Become is the same start, completed.
//
// On POSIX it replaces the process (execve) and returns only on failure; the
// descriptors marked close-on-exec close as the program begins, and started is
// not called; keep, the caller's own channels, are open descriptors already and
// stay so. On Windows, which has no exec, it starts the program as the only
// child of the process with the same standard streams and the handles keep
// lists (under the same values), calls started once the
// program exists (the stage closes its channel there), waits for it and returns
// its exit code. A program that cannot be started is an error and started is not
// called.
func Become(argv, env []string, keep []uintptr, started func()) (int, error) {
	if len(argv) == 0 || argv[0] == "" {
		return 0, herr.New(ReasonEmptyArgv, "no program to become")
	}
	if !filepath.IsAbs(argv[0]) {
		return 0, herr.Newf(ReasonRelativeProgram, "program %q is not an absolute path", filepath.Base(argv[0]))
	}
	if started == nil {
		started = func() {}
	}
	return become(argv, env, keep, started)
}
