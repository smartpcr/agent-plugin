//go:build windows

package subprocesstest

import (
	"os"

	"golang.org/x/sys/windows"
)

// dupFile is a second handle for f, owned by whoever closes it.
func dupFile(f *os.File) (uintptr, error) {
	self := windows.CurrentProcess()
	var h windows.Handle
	err := windows.DuplicateHandle(self, windows.Handle(f.Fd()), self, &h, 0, false, windows.DUPLICATE_SAME_ACCESS)
	return uintptr(h), err
}
