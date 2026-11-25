//go:build windows

package subprocess

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// prepareExtra makes each channel's handle inheritable, so the child receives it
// under the same value (ChildFD). Only listed handles are inherited
// (PROC_THREAD_ATTRIBUTE_HANDLE_LIST, in os/exec and in createInJob alike), so the
// flag lets no other child inherit it.
func prepareExtra(extra []Channel) error {
	for i, c := range extra {
		if err := windows.SetHandleInformation(windows.Handle(c.File.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
			return fmt.Errorf("channel %d: SetHandleInformation: %w", i, err)
		}
	}
	return nil
}

// addExtra lists the channels' handles for the child.
func addExtra(cmd *exec.Cmd, extra []Channel) {
	if len(extra) == 0 {
		return
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	for _, c := range extra {
		cmd.SysProcAttr.AdditionalInheritedHandles = append(cmd.SysProcAttr.AdditionalInheritedHandles, syscall.Handle(c.File.Fd()))
	}
}

func childFD(extra []Channel, i int) uintptr { return extra[i].File.Fd() }

// detachFlags put the child outside the host's console and process group, and
// outside the job the host itself may run in.
const detachFlags = windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP

// startDetached starts cmd outside every job of the host when the host's job
// allows it to break away; when it does not, CreateProcess refuses before
// anything starts, and the child is started again without breaking away and
// stays in that job (it is still outside every tree owner of this package).
func startDetached(build func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := build()
	setFlags(cmd, detachFlags|windows.CREATE_BREAKAWAY_FROM_JOB)
	err := cmd.Start()
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return cmd, err
	}
	cmd = build()
	setFlags(cmd, detachFlags)
	return cmd, cmd.Start()
}

func setFlags(cmd *exec.Cmd, flags uint32) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= flags
}
