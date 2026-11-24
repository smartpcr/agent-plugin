//go:build unix

package subprocess

import (
	"errors"
	"fmt"
	"syscall"
)

// groupOwner is the child's own process group: the child calls setpgid at birth,
// so every descendant that does not leave the group is reached by -pid. Its start
// is per system: owner_group_linux.go also ties the child to the host's life.
type groupOwner struct {
	pid      int
	fallback string
	lim      Limits
}

func (o *groupOwner) report() Owner {
	return Owner{Kind: ProcessGroup, Exact: false, Fallback: o.fallback}
}

func (o *groupOwner) signal(sig syscall.Signal) error {
	if o.pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-o.pid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func (o *groupOwner) polite() error { return o.signal(syscall.SIGTERM) }
func (o *groupOwner) kill() error   { return o.signal(syscall.SIGKILL) }

func (o *groupOwner) empty() (bool, error) {
	if o.pid <= 0 {
		return true, nil
	}
	err := syscall.Kill(-o.pid, 0)
	switch {
	case errors.Is(err, syscall.ESRCH):
		return true, nil
	case err == nil, errors.Is(err, syscall.EPERM):
		return false, nil
	default:
		return false, err
	}
}

func (o *groupOwner) describe() string {
	return fmt.Sprintf("process group %d still has members", o.pid)
}

func (o *groupOwner) release() {}

func (o *groupOwner) limitsReport() LimitsReport { return groupLimits(o.lim) }

// member is best effort: pid is in the group, or descends from its leader.
func (o *groupOwner) member(pid int) bool { return o.pid > 0 && groupMember(o.pid, pid) }

// windowsLineLength is only meaningful on Windows.
func windowsLineLength([]string) int { return 0 }
