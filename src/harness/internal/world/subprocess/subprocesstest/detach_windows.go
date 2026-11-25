//go:build windows

package subprocesstest

import "syscall"

// detachAttr starts a process in a new process group, the way a program leaves
// its parent's console group.
func detachAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// checkDetached has nothing to check: Windows keeps no queryable process group,
// so the creation flag is the detachment.
func checkDetached(int) error { return nil }
