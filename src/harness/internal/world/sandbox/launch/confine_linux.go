//go:build linux

package launch

import (
	"errors"
	"fmt"
	"runtime"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"
	"golang.org/x/sys/unix"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

func init() { confiners[sandbox.BackendLandlock] = confineLandlock }

// LandlockABI is the running kernel's Landlock interface version, read with
// go-landlock's LandlockGetABIVersion; 0 when the kernel has no Landlock or it is
// disabled at boot.
func LandlockABI() (int, error) {
	v, err := llsyscall.LandlockGetABIVersion()
	switch {
	case errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EOPNOTSUPP):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("read the Landlock interface version: %w", err)
	}
	return v, nil
}

// confineLandlock restricts the stage itself, in the sequence of
// DH-ADR-NATIVESYSTEM-1 and with the refusals of DH-ADR-NATIVESYSTEM-2:
//
//  1. read the kernel's version and build the plan's rule set (LandlockRules
//     refuses a version below 1);
//  2. create the ruleset with go-landlock's Config.NewRulesetFD from a config
//     that is not best-effort and handles exactly the kinds of
//     min(kernel, reference); go-landlock opens every rule's path, so a missing
//     write root fails the stage before anything is restricted; no descriptor
//     is a refusal;
//  3. look at the session bus, before the restriction (Landlock at this
//     reference set does not scope sockets, so it shows what the command will
//     reach);
//  4. lock the goroutine to its thread, never unlocked; set no_new_privs; apply
//     the ruleset with LandlockRestrictSelf without the all-threads flag.
//
// Contract: steps 4's calls, the canary, the wait for go and the exec
// (subprocess.Become) all run on the one locked thread, so the command inherits
// its domain. Nothing here may hand off to another goroutine, and nothing may
// call go-landlock's Restrict functions (which reach psx).
func confineLandlock(p Plan) ([]sandbox.Effect, error) {
	abi, err := LandlockABI()
	if err != nil {
		return nil, err
	}
	rs, err := LandlockRules(abi, p)
	if err != nil {
		return nil, err
	}
	cfg, err := landlockConfig(rs)
	if err != nil {
		return nil, err
	}
	rules := make([]landlock.Rule, 0, len(rs.Rules))
	for _, r := range rs.Rules {
		rules = append(rules, landlock.PathAccess(landlock.AccessFSSet(r.Access), r.Path))
	}
	fd, flags, err := cfg.NewRulesetFD(rules...)
	if err != nil {
		return nil, fmt.Errorf("create the Landlock ruleset: %w", err)
	}
	if fd < 0 {
		return nil, errors.New("create the Landlock ruleset: go-landlock returned no ruleset, so nothing would be restricted")
	}
	defer func() { _ = unix.Close(fd) }()
	governed := delegated(rs.Governed)

	runtime.LockOSThread()
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return nil, fmt.Errorf("set no_new_privs: %w", err)
	}
	if err := llsyscall.LandlockRestrictSelf(fd, flags); err != nil {
		return nil, fmt.Errorf("restrict the stage: %w", err)
	}
	return governed, nil
}

// landlockConfig is the go-landlock config for rs. It is a struct literal (the
// deprecated NewConfig is not called), so it checks here what NewConfig checked:
// the handled set holds only kinds of the reference set, which go-landlock knows.
// NewRulesetFD checks the same again before it creates anything.
func landlockConfig(rs Ruleset) (landlock.Config, error) {
	if unknown := rs.Handled &^ LandlockHandled(LandlockReference); unknown != 0 {
		return landlock.Config{}, fmt.Errorf("the Landlock config for interface version %d: handled access %#x is outside the reference set", rs.Version, unknown)
	}
	return landlock.Config{HandledAccessFS: landlock.AccessFSSet(rs.Handled)}, nil
}
