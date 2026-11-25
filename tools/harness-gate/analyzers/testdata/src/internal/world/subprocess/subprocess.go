// Package subprocess is the one spawn path; it may import os/exec.
package subprocess

import "os/exec"

// Command builds a command from an argument list.
func Command(name string, args ...string) *exec.Cmd { return exec.Command(name, args...) }

// Launch stands in for the launch plan.
type Launch struct{ Reason string }

// Spec is a start: its argv and its launch plan.
type Spec struct {
	Argv   []string
	Launch Launch
}

// Restart is a Spec built inside the package; it too carries its plan.
func Restart(argv []string) Spec { return Spec{Argv: argv, Launch: Launch{Reason: "restart"}} }

// Bare forgets the plan inside the spawn package itself.
func Bare(argv []string) Spec {
	return Spec{Argv: argv} // want `subprocess.Spec without Launch`
}
