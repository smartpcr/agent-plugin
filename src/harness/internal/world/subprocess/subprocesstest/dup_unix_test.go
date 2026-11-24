//go:build unix

package subprocesstest

import (
	"os"
	"syscall"
)

// dupFile is a second descriptor for f, owned by whoever closes it.
func dupFile(f *os.File) (uintptr, error) {
	fd, err := syscall.Dup(int(f.Fd()))
	return uintptr(fd), err
}
