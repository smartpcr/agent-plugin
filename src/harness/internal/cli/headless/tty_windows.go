//go:build windows

package headless

import "os"

// openTerminal opens the console's input for reading; an error means the
// process has no console.
func openTerminal() (*os.File, error) {
	f, err := os.OpenFile("CONIN$", os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	if !isTerminal(f) {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}
