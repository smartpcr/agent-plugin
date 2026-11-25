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

// homeReadOnly reports whether home is mounted read-only for this process: the
// access check for writing fails with EROFS, as bubblewrap's read-only bind of
// the home makes it. Any other answer (writable, missing, refused by permission)
// is not shown read-only.
func homeReadOnly(home string) bool {
	const wOK = 0x2
	return errors.Is(syscall.Access(home, wOK), syscall.EROFS)
}
