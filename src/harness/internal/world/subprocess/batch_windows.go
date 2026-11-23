//go:build windows

package subprocess

import (
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

// systemShell is cmd.exe in the system directory. It is not %ComSpec%: the
// environment can point that anywhere, and a checked batch run must be parsed by
// the cmd.exe whose rules checkBatchArgs encodes.
func systemShell() (string, error) {
	dir, err := windows.GetSystemDirectory()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "cmd.exe"), nil
}

// setCmdLine makes CreateProcess receive line as given instead of the arguments
// quoted by syscall.EscapeArg. The tree owner adds its creation flags to these
// attributes; it does not replace them.
func setCmdLine(cmd *exec.Cmd, line string) {
	if line == "" {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line}
}
