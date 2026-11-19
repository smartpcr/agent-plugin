// Package onespawn is the fixture of the onespawn analyzer.
package onespawn

import "os/exec" // want `os/exec imported outside internal/world/subprocess`

// Run starts a program outside the one spawn path.
func Run() error { return exec.Command("true").Run() }
