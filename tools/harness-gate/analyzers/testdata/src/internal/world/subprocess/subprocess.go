// Package subprocess is the one spawn path; it may import os/exec.
package subprocess

import "os/exec"

// Command builds a command from an argument list.
func Command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }
