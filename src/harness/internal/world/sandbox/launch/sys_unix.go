//go:build unix

package launch

import (
	"errors"
	"os"
	"syscall"
)

// closeOnExec marks the channel ends close-on-exec, so the command the stage
// becomes inherits neither and the host reads end of file once it begins.
func closeOnExec(files ...*os.File) error {
	for _, f := range files {
		syscall.CloseOnExec(int(f.Fd()))
	}
	return nil
}

// readOnly reports a write refused because the file system is mounted read-only,
// as bubblewrap's read-only binds refuse it.
func readOnly(err error) bool { return errors.Is(err, syscall.EROFS) }
