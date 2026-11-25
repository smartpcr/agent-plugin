//go:build linux

package launch

import (
	"fmt"
	"runtime"
	//lint:ignore typeassert the landlock_create_ruleset and landlock_add_rule system calls take their attribute structs by address (unsafe.Pointer) and size (unsafe.Sizeof); golang.org/x/sys/unix has no wrapper for them
	"unsafe"

	"golang.org/x/sys/unix"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

func init() { confiners[sandbox.BackendLandlock] = confineLandlock }

// LandlockABI is the running kernel's Landlock interface version, read with
// landlock_create_ruleset(NULL, 0, LANDLOCK_CREATE_RULESET_VERSION); 0 when the
// kernel has no Landlock or it is disabled at boot.
func LandlockABI() (int, error) {
	v, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	switch {
	case e == unix.ENOSYS || e == unix.EOPNOTSUPP:
		return 0, nil
	case e != 0:
		return 0, fmt.Errorf("read the Landlock interface version: %w", e)
	}
	return int(v), nil
}

// rulesetAttr is struct landlock_ruleset_attr up to handled_access_fs, the only
// member this reference set fills; the kernel takes the size it is given.
type rulesetAttr struct{ handledFS uint64 }

// confineLandlock restricts the stage itself (DH-ADR-NATIVESYSTEM-2): it reads
// the kernel's version, builds the plan's rule set, opens every rule's path
// (a missing write root fails the stage before anything is restricted), then
// creates the ruleset, adds the rules, sets no_new_privs and restricts itself.
//
// Landlock without TSYNC confines the calling thread and what it later starts,
// so the goroutine is locked to its thread and never unlocked: the canary, the
// wait for go and the exec (subprocess.Become) all run on the restricted thread,
// and the command inherits its domain. The bus is looked at before the
// restriction; Landlock at this reference set does not scope sockets, so it
// shows what the command will reach.
func confineLandlock(p Plan) ([]sandbox.Effect, error) {
	abi, err := LandlockABI()
	if err != nil {
		return nil, err
	}
	rs, err := LandlockRules(abi, p)
	if err != nil {
		return nil, err
	}
	fds := make([]int, 0, len(rs.Rules))
	defer func() {
		for _, fd := range fds {
			_ = unix.Close(fd)
		}
	}()
	for _, r := range rs.Rules {
		fd, err := unix.Open(r.Path, unix.O_PATH|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, fmt.Errorf("open %s for its Landlock rule: %w", r.Path, err)
		}
		fds = append(fds, fd)
	}
	governed := delegated(rs.Governed)

	runtime.LockOSThread()
	attr := rulesetAttr{handledFS: rs.Handled}
	rfd, _, e := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if e != 0 {
		return nil, fmt.Errorf("create the Landlock ruleset: %w", e)
	}
	defer func() { _ = unix.Close(int(rfd)) }()
	for i, r := range rs.Rules {
		pb := unix.LandlockPathBeneathAttr{Allowed_access: r.Access, Parent_fd: int32(fds[i])}
		if _, _, e := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, rfd, unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&pb)), 0, 0, 0); e != 0 {
			return nil, fmt.Errorf("add the Landlock rule for %s: %w", r.Path, e)
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return nil, fmt.Errorf("set no_new_privs: %w", err)
	}
	if _, _, e := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, rfd, 0, 0); e != 0 {
		return nil, fmt.Errorf("restrict the stage: %w", e)
	}
	return governed, nil
}
