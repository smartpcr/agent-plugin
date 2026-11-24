//go:build e2e && windows

package e2e

import (
	"fmt"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// sftAccounting is JOBOBJECT_BASIC_ACCOUNTING_INFORMATION.
type sftAccounting struct {
	TotalUserTime             int64
	TotalKernelTime           int64
	ThisPeriodTotalUserTime   int64
	ThisPeriodTotalKernelTime int64
	TotalPageFaultCount       uint32
	TotalProcesses            uint32
	ActiveProcesses           uint32
	TotalTerminatedProcesses  uint32
}

var (
	sftJobOnce sync.Once
	sftJob     windows.Handle
	sftJobErr  error
)

// sftSpawnRecorder is the subprocess fake on Windows: the test process joins a
// job object (no limits; breakaway allowed so nested-job owners still work), and
// every process created from it, by any path, raises the job's TotalProcesses.
type sftSpawnRecorder struct{}

func sftNewSpawnRecorder() (*sftSpawnRecorder, error) {
	sftJobOnce.Do(func() {
		job, err := windows.CreateJobObject(nil, nil)
		if err != nil {
			sftJobErr = fmt.Errorf("CreateJobObject: %w", err)
			return
		}
		info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
			BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{LimitFlags: windows.JOB_OBJECT_LIMIT_BREAKAWAY_OK},
		}
		if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
			uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
			windows.CloseHandle(job)
			sftJobErr = fmt.Errorf("SetInformationJobObject: %w", err)
			return
		}
		if err := windows.AssignProcessToJobObject(job, windows.CurrentProcess()); err != nil {
			windows.CloseHandle(job)
			sftJobErr = fmt.Errorf("AssignProcessToJobObject(self): %w", err)
			return
		}
		sftJob = job
	})
	if sftJobErr != nil {
		return nil, sftJobErr
	}
	return &sftSpawnRecorder{}, nil
}

// Count is the number of processes the job has held: the test process plus
// every process started since it joined.
func (*sftSpawnRecorder) Count() (int, error) {
	var a sftAccounting
	if err := windows.QueryInformationJobObject(sftJob, windows.JobObjectBasicAccountingInformation,
		uintptr(unsafe.Pointer(&a)), uint32(unsafe.Sizeof(a)), nil); err != nil {
		return 0, fmt.Errorf("QueryInformationJobObject: %w", err)
	}
	return int(a.TotalProcesses), nil
}

func (*sftSpawnRecorder) Close() {}
