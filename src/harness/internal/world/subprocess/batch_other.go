//go:build !windows

package subprocess

import (
	"errors"
	"os/exec"
)

// systemShell has no counterpart off Windows, where there are no batch files;
// check never reaches it there.
func systemShell() (string, error) {
	return "", errors.New("batch files run only on Windows")
}

// setCmdLine does nothing off Windows: a command line is a Windows notion, and
// check never builds one elsewhere.
func setCmdLine(*exec.Cmd, string) {}
