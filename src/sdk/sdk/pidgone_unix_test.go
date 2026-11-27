//go:build !windows

package sdk

import (
	"errors"
	"syscall"
)

func pidGone(pid int) bool { return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) }
