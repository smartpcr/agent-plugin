//go:build e2e && linux

package e2e

// Linux-only pieces of the Stage 8.5 confinement suite
// (dh_s_18_acceptance_on_real_operating_systems_linux_confinement_and_sandbox_status_test.go).

import (
	"fmt"
	"os"
	"runtime"
	"syscall"
	"unsafe"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
)

// lcssLandlockABI is the running kernel's Landlock version, as the Landlock
// backend reads it (0 when Landlock is absent or disabled).
func lcssLandlockABI() (int, error) { return launch.LandlockABI() }

// The Landlock system calls (landlock_create_ruleset, landlock_add_rule,
// landlock_restrict_self): the same numbers on every Linux architecture.
var lcssLandlockSyscalls = []uint32{444, 445, 446}

// lcssDisableLandlock makes this process an unconfined machine and re-executes
// it: a seccomp filter, kept across exec and inherited by every child, makes the
// Landlock system calls fail with EOPNOTSUPP, which is what a kernel booted with
// Landlock disabled answers. Only the calling thread carries the filter, so the
// goroutine is locked to it and the process is replaced from it at once.
func lcssDisableLandlock(env []string) error {
	runtime.LockOSThread()
	const (
		prSetNoNewPrivs   = 38
		prSetSeccomp      = 22
		seccompModeFilter = 2
		retAllow          = 0x7fff0000
		retErrno          = 0x00050000
		eopnotsupp        = 95
	)
	n := uint8(len(lcssLandlockSyscalls))
	filter := []syscall.SockFilter{{Code: syscall.BPF_LD | syscall.BPF_W | syscall.BPF_ABS, K: 0}}
	for i, nr := range lcssLandlockSyscalls {
		filter = append(filter, syscall.SockFilter{Code: syscall.BPF_JMP | syscall.BPF_JEQ | syscall.BPF_K, Jt: n - uint8(i), K: nr})
	}
	filter = append(filter,
		syscall.SockFilter{Code: syscall.BPF_RET | syscall.BPF_K, K: retAllow},
		syscall.SockFilter{Code: syscall.BPF_RET | syscall.BPF_K, K: retErrno | eopnotsupp})
	prog := syscall.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if _, _, e := syscall.RawSyscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0); e != 0 {
		return fmt.Errorf("prctl(PR_SET_NO_NEW_PRIVS): %v", e)
	}
	if _, _, e := syscall.RawSyscall(syscall.SYS_PRCTL, prSetSeccomp, seccompModeFilter, uintptr(unsafe.Pointer(&prog))); e != 0 {
		return fmt.Errorf("prctl(PR_SET_SECCOMP): %v", e)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, os.Args, env)
}
