//go:build windows

package subprocess

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"slices"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
	//lint:ignore typeassert UpdateProcThreadAttribute takes the attribute value as unsafe.Pointer and its size from unsafe.Sizeof
	"unsafe"

	"golang.org/x/sys/windows"
)

// procThreadAttributeJobList is PROC_THREAD_ATTRIBUTE_JOB_LIST (Windows 10 and
// later): the new process is a member of the listed jobs from its creation.
// x/sys v0.50.0 does not declare it.
const procThreadAttributeJobList = 0x0002000D

// errNoJobList says this Windows does not take the job list at creation; the
// owner then falls back to creating suspended and assigning (nothing started).
var errNoJobList = errors.New("PROC_THREAD_ATTRIBUTE_JOB_LIST is not supported")

// afterCreate, when set, runs once the child exists inside its job and before
// its first thread runs. The NODEPROC-8 test sets it to hold the host at that
// point and kill it there.
var afterCreate func(pid int)

// forceSuspended makes createInJob report errNoJobList, so a test can run the
// fallback as a control.
var forceSuspended bool

// childStreams is the parent's side of the standard streams of a child created
// by createInJob: the copies os/exec would run, and the pipe ends they use.
type childStreams struct {
	waitDelay time.Duration
	parent    []*os.File // the host's pipe ends, closed when the copies end
	copies    []func() error
	wg        sync.WaitGroup
	errs      []error
}

func (s *childStreams) start() {
	s.errs = make([]error, len(s.copies))
	for i, c := range s.copies {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.errs[i] = c()
		}()
	}
}

// drain waits for the copies once the child has exited, as exec.Cmd.Wait does: up
// to waitDelay when it is set, after which the host's pipe ends are closed and
// exec.ErrWaitDelay is the result.
func (s *childStreams) drain() error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	var err error
	if s.waitDelay > 0 {
		t := time.NewTimer(s.waitDelay)
		select {
		case <-done:
		case <-t.C:
			closeFiles(s.parent)
			<-done
			err = exec.ErrWaitDelay
		}
		t.Stop()
	} else {
		<-done
	}
	closeFiles(s.parent)
	if err != nil {
		return err
	}
	return errors.Join(s.errs...)
}

func closeFiles(fs []*os.File) {
	for _, f := range fs {
		_ = f.Close()
	}
}

// createInJob creates cmd's process with its own CreateProcess call, inside job
// from the start (PROC_THREAD_ATTRIBUTE_JOB_LIST), so no moment exists in which
// the child or anything it starts is outside the job: if the host dies at any
// point, the job's kill-on-close ends the child (DH-ADR-NODEPROC-2, rule of
// execution.md sec. 6.1). It does what os/exec does for the rest: the resolved
// path as the application name, the quoted command line or SysProcAttr.CmdLine,
// the environment block, no window, only the listed handles inherited, and a copy
// for each stream that is not a file. On success cmd.Process is set, so cmd.Wait
// reaps the child; the returned streams are drained after it.
func createInJob(cmd *exec.Cmd, job windows.Handle) (*childStreams, error) {
	if forceSuspended {
		return nil, errNoJobList
	}
	sys := cmd.SysProcAttr
	if sys == nil {
		sys = &syscall.SysProcAttr{}
	}
	streams := &childStreams{waitDelay: cmd.WaitDelay}
	var child []*os.File // the child's pipe ends and null devices, closed after creation
	fail := func(err error) (*childStreams, error) {
		closeFiles(child)
		closeFiles(streams.parent)
		return nil, err
	}
	std := make([]windows.Handle, 3)
	for i := range std {
		if i == 2 && cmd.Stderr != nil && interfaceEqual(cmd.Stdout, cmd.Stderr) {
			// One pipe and one copy, as os/exec: the shared writer is never
			// written from two goroutines at once.
			std[2] = std[1]
			continue
		}
		f, err := childEnd(cmd, i, streams, &child)
		if err != nil {
			return fail(err)
		}
		std[i] = windows.Handle(f.Fd())
	}
	self := windows.CurrentProcess()
	inherit := make([]windows.Handle, 0, 3+len(sys.AdditionalInheritedHandles))
	for _, h := range std {
		var dup windows.Handle
		if err := windows.DuplicateHandle(self, h, self, &dup, 0, true, windows.DUPLICATE_SAME_ACCESS); err != nil {
			for _, d := range inherit {
				windows.CloseHandle(d)
			}
			return fail(fmt.Errorf("DuplicateHandle: %w", err))
		}
		inherit = append(inherit, dup)
	}
	dups := slices.Clone(inherit)
	defer func() {
		for _, d := range dups {
			windows.CloseHandle(d)
		}
	}()
	for _, h := range sys.AdditionalInheritedHandles {
		inherit = append(inherit, windows.Handle(h))
	}

	attrs, err := windows.NewProcThreadAttributeList(2)
	if err != nil {
		return fail(fmt.Errorf("InitializeProcThreadAttributeList: %w", err))
	}
	defer attrs.Delete()
	if err := attrs.Update(windows.PROC_THREAD_ATTRIBUTE_HANDLE_LIST, unsafe.Pointer(&inherit[0]), uintptr(len(inherit))*unsafe.Sizeof(inherit[0])); err != nil {
		return fail(fmt.Errorf("UpdateProcThreadAttribute(handle list): %w", err))
	}
	jobs := []windows.Handle{job}
	if err := attrs.Update(procThreadAttributeJobList, unsafe.Pointer(&jobs[0]), unsafe.Sizeof(jobs[0])); err != nil {
		return fail(fmt.Errorf("%w: %v", errNoJobList, err))
	}

	app, err := windows.UTF16PtrFromString(cmd.Path)
	if err != nil {
		return fail(err)
	}
	line := sys.CmdLine
	if line == "" {
		line = composeLine(cmd.Args)
	}
	linep, err := windows.UTF16PtrFromString(line)
	if err != nil {
		return fail(err)
	}
	var dirp *uint16
	if cmd.Dir != "" {
		if dirp, err = windows.UTF16PtrFromString(cmd.Dir); err != nil {
			return fail(err)
		}
	}
	env, err := envBlock(cmd.Env)
	if err != nil {
		return fail(err)
	}
	si := &windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()}
	si.Cb = uint32(unsafe.Sizeof(*si))
	si.Flags = windows.STARTF_USESTDHANDLES
	si.StdInput, si.StdOutput, si.StdErr = dups[0], dups[1], dups[2]
	flags := sys.CreationFlags | windows.CREATE_SUSPENDED | windows.CREATE_NO_WINDOW |
		windows.CREATE_UNICODE_ENVIRONMENT | windows.EXTENDED_STARTUPINFO_PRESENT
	var pi windows.ProcessInformation
	err = windows.CreateProcess(app, linep, nil, nil, true, flags, &env[0], dirp, &si.StartupInfo, &pi)
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) || errors.Is(err, windows.ERROR_NOT_SUPPORTED) {
		return fail(fmt.Errorf("%w: CreateProcess: %v", errNoJobList, err))
	}
	if err != nil {
		return fail(err)
	}
	closeFiles(child)
	child = nil
	defer windows.CloseHandle(pi.Thread)
	defer windows.CloseHandle(pi.Process)

	proc, err := os.FindProcess(int(pi.ProcessId))
	if err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		return fail(fmt.Errorf("open the created process: %w", err))
	}
	if afterCreate != nil {
		afterCreate(int(pi.ProcessId))
	}
	if _, err := windows.ResumeThread(pi.Thread); err != nil {
		_ = windows.TerminateProcess(pi.Process, 1)
		_ = proc.Release()
		return fail(fmt.Errorf("ResumeThread: %w", err))
	}
	cmd.Process = proc
	streams.start()
	return streams, nil
}

// childEnd is the file the child gets as standard stream i (0 in, 1 out, 2
// err): the null device for nil, the file itself for an *os.File, otherwise the
// child's end of a new pipe whose copy is added to streams. A file the host opens
// for the child is added to child.
func childEnd(cmd *exec.Cmd, i int, streams *childStreams, child *[]*os.File) (*os.File, error) {
	if i == 0 {
		switch r := cmd.Stdin.(type) {
		case nil:
			return openNull(os.O_RDONLY, child)
		case *os.File:
			return r, nil
		default:
			pr, pw, err := os.Pipe()
			if err != nil {
				return nil, err
			}
			*child = append(*child, pr)
			streams.parent = append(streams.parent, pw)
			streams.copies = append(streams.copies, func() error {
				_, _ = io.Copy(pw, r)
				_ = pw.Close()
				return nil // as os/exec: the child may stop reading its input
			})
			return pr, nil
		}
	}
	w := cmd.Stdout
	if i == 2 {
		w = cmd.Stderr
	}
	switch w := w.(type) {
	case nil:
		return openNull(os.O_WRONLY, child)
	case *os.File:
		return w, nil
	default:
		pr, pw, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		*child = append(*child, pw)
		streams.parent = append(streams.parent, pr)
		streams.copies = append(streams.copies, func() error {
			_, err := io.Copy(w, pr)
			_ = pr.Close()
			return err
		})
		return pw, nil
	}
}

// interfaceEqual is a == b, false where the comparison would panic (os/exec's).
func interfaceEqual(a, b any) (eq bool) {
	defer func() {
		if recover() != nil {
			eq = false
		}
	}()
	return a == b
}

func openNull(flag int, child *[]*os.File) (*os.File, error) {
	f, err := os.OpenFile(os.DevNull, flag, 0)
	if err != nil {
		return nil, err
	}
	*child = append(*child, f)
	return f, nil
}

// composeLine quotes args as os/exec does, with syscall.EscapeArg for each.
func composeLine(args []string) string {
	var b []byte
	for i, a := range args {
		if i > 0 {
			b = append(b, ' ')
		}
		b = append(b, syscall.EscapeArg(a)...)
	}
	return string(b)
}

// envBlock is the UTF-16 environment block of env, already merged and sorted by
// Env.block.
func envBlock(env []string) ([]uint16, error) {
	if len(env) == 0 {
		return []uint16{0, 0}, nil
	}
	var b []uint16
	for _, kv := range env {
		for _, r := range kv {
			if r == 0 {
				return nil, fmt.Errorf("environment entry holds a NUL byte")
			}
		}
		for _, r := range kv {
			b = utf16.AppendRune(b, r)
		}
		b = append(b, 0)
	}
	return append(b, 0), nil
}
