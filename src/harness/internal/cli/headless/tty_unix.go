//go:build !windows

package headless

import "os"

// openTerminal opens the controlling terminal for reading; an error means the
// process has none.
func openTerminal() (*os.File, error) { return os.Open("/dev/tty") }
