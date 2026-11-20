//go:build windows

package subprocess

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func choose() Owner { return Owner{Kind: JobObject, Exact: true} }

// jobOwner is a Job Object with kill-on-close whose only handle the host holds.
// The child is created suspended, assigned to the job, then resumed, so it cannot
// start a grandchild outside the job. When the host dies the handle closes and the
// job ends the tree.
type jobOwner struct {
	job windows.Handle
}

func newOwner() (treeOwner, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("CreateJobObject: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return nil, fmt.Errorf("SetInformationJobObject: %w", err)
	}
	return &jobOwner{job: job}, nil
}

func (o *jobOwner) report() Owner { return TreeOwner() }

func (o *jobOwner) start(build func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := build()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// A pipe child gets no window (Windows rule 6).
		CreationFlags: windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW,
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	pid := uint32(cmd.Process.Pid)
	if err := o.adopt(pid); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, err
	}
	return cmd, nil
}

// adopt assigns the suspended process to the job and resumes its threads.
func (o *jobOwner) adopt(pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return fmt.Errorf("OpenProcess: %w", err)
	}
	defer windows.CloseHandle(h)
	if err := windows.AssignProcessToJobObject(o.job, h); err != nil {
		return fmt.Errorf("AssignProcessToJobObject: %w", err)
	}
	return resumeThreads(pid)
}

func resumeThreads(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return fmt.Errorf("CreateToolhelp32Snapshot: %w", err)
	}
	defer windows.CloseHandle(snap)
	var te windows.ThreadEntry32
	te.Size = uint32(unsafe.Sizeof(te))
	resumed := 0
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		th, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if err != nil {
			return fmt.Errorf("OpenThread: %w", err)
		}
		_, rerr := windows.ResumeThread(th)
		windows.CloseHandle(th)
		if rerr != nil {
			return fmt.Errorf("ResumeThread: %w", rerr)
		}
		resumed++
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return fmt.Errorf("Thread32Next: %w", err)
	}
	if resumed == 0 {
		return errors.New("no thread of the suspended child was found")
	}
	return nil
}

// polite does nothing: a pipe child has no polite stop on Windows; StopTree waits
// the grace period for it to finish on its own.
func (o *jobOwner) polite() error { return nil }

func (o *jobOwner) kill() error { return windows.TerminateJobObject(o.job, 1) }

// basicAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type basicAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

func (o *jobOwner) active() (uint32, error) {
	var info basicAccounting
	err := windows.QueryInformationJobObject(o.job, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)), nil)
	return info.ActiveProcesses, err
}

func (o *jobOwner) empty() (bool, error) {
	n, err := o.active()
	return err == nil && n == 0, err
}

func (o *jobOwner) describe() string {
	n, err := o.active()
	if err != nil {
		return "job state unknown: " + err.Error()
	}
	return fmt.Sprintf("%d processes left in the job", n)
}

func (o *jobOwner) release() {
	if o.job != 0 {
		windows.CloseHandle(o.job)
		o.job = 0
	}
}

// windowsLineLength is the length, in UTF-16 units, of the command line
// CreateProcess receives for argv, quoted by syscall.EscapeArg.
func windowsLineLength(argv []string) int {
	n := 0
	for i, a := range argv {
		if i > 0 {
			n++
		}
		n += len(windows.StringToUTF16(syscall.EscapeArg(a))) - 1
	}
	return n
}
