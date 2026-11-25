//go:build scenario && windows

package scenarios

import (
	"fmt"
	"os"
	"syscall"

	//lint:ignore typeassert LockFileEx and UnlockFileEx take the OVERLAPPED by pointer; package syscall has no wrapper for them
	"unsafe"
)

var (
	fs3Kernel32      = syscall.NewLazyDLL("kernel32.dll")
	fs3LockFileEx    = fs3Kernel32.NewProc("LockFileEx")
	fs3UnlockFileEx  = fs3Kernel32.NewProc("UnlockFileEx")
	fs3Ntdll         = syscall.NewLazyDLL("ntdll.dll")
	fs3NtSuspendProc = fs3Ntdll.NewProc("NtSuspendProcess")
	fs3NtResumeProc  = fs3Ntdll.NewProc("NtResumeProcess")
)

const (
	fs3ProcessSuspendResume = 0x0800
	fs3LockfileFailNow      = 0x1
	fs3LockfileExclusive    = 0x2
	fs3ErrorLockViolation   = syscall.Errno(33)
)

// fs3Freeze suspends every thread of process pid (NtSuspendProcess, which
// returns once they are suspended); resume resumes them.
func fs3Freeze(pid int) (resume func() error, err error) {
	h, err := syscall.OpenProcess(fs3ProcessSuspendResume, false, uint32(pid))
	if err != nil {
		return nil, err
	}
	if st, _, _ := fs3NtSuspendProc.Call(uintptr(h)); st != 0 {
		_ = syscall.CloseHandle(h)
		return nil, fmt.Errorf("NtSuspendProcess: status %#x", st)
	}
	return func() error {
		defer syscall.CloseHandle(h)
		if st, _, _ := fs3NtResumeProc.Call(uintptr(h)); st != 0 {
			return fmt.Errorf("NtResumeProcess: status %#x", st)
		}
		return nil
	}, nil
}

// fs3LockHeld reports whether another process holds the lock of path, taking and
// releasing it at once when it is free (fsx.Lock on Windows is an exclusive
// LockFileEx of byte 0).
func fs3LockHeld(path string) (bool, error) {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	var ol syscall.Overlapped
	r, _, e := fs3LockFileEx.Call(f.Fd(), fs3LockfileFailNow|fs3LockfileExclusive, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		if e == fs3ErrorLockViolation || e == syscall.ERROR_IO_PENDING {
			return true, nil
		}
		return false, e
	}
	if r, _, e := fs3UnlockFileEx.Call(f.Fd(), 0, 1, 0, uintptr(unsafe.Pointer(&ol))); r == 0 {
		return false, e
	}
	return false, nil
}
