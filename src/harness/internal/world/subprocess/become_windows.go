//go:build windows

package subprocess

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// become starts the program with the process's standard streams and the kept
// channels, inside the job the host put this process in, and waits for it. Only
// those handles are inherited (os/exec lists them), so the stage's channel never
// reaches it. A batch file is refused: cmd.exe would re-parse its arguments.
func become(argv, env []string, keep []uintptr, started func()) (int, error) {
	switch strings.ToLower(filepath.Ext(argv[0])) {
	case ".bat", ".cmd":
		return 0, herr.Newf(ReasonBatchRefused, "%s is a batch file", filepath.Base(argv[0]))
	}
	if _, err := os.Stat(argv[0]); err != nil {
		return 0, herr.Wrap(ReasonStartFailed, err, "start "+argv[0])
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	// The planned program, exactly as named; a missing one has already reached the
	// stage as fs.ErrNotExist above.
	cmd.Path, cmd.Err = argv[0], nil
	cmd.Args = argv
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if len(keep) > 0 {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
		for _, k := range keep {
			if err := windows.SetHandleInformation(windows.Handle(k), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
				return 0, herr.Wrap(ReasonStartFailed, err, fmt.Sprintf("pass channel %d to %s", k, filepath.Base(argv[0])))
			}
			cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, syscall.Handle(k))
		}
	}
	if err := cmd.Start(); err != nil {
		return 0, herr.Wrap(ReasonStartFailed, err, "start "+argv[0])
	}
	started()
	err := cmd.Wait()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		return 0, herr.Wrap(ReasonStartFailed, err, "wait "+argv[0])
	}
	return cmd.ProcessState.ExitCode(), nil
}
