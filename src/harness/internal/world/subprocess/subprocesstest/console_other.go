//go:build !windows

package subprocesstest

// consoleWindow is false: only Windows gives a process a console window.
func consoleWindow() bool { return false }
