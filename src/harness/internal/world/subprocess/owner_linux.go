//go:build linux

package subprocess

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

const cgroup2Magic = 0x63677270

// fallbackNoCgroup is the reported reason when the host's own control group is
// not writable (register U-K1-1). The systemd delegated scope is not requested.
const fallbackNoCgroup = "own control group not writable; delegated scope not requested (U-K1-1)"

var (
	cgroupOnce sync.Once
	cgroupBase string // writable directory new per-tree groups are made in; "" when none
	cgroupWhy  string
	cgroupSeq  atomic.Uint64
)

func probeCgroup() {
	cgroupOnce.Do(func() {
		dir, err := ownCgroup()
		if err != nil {
			cgroupWhy = fallbackNoCgroup + ": " + err.Error()
			return
		}
		probe := filepath.Join(dir, fmt.Sprintf("dsh-probe-%d", os.Getpid()))
		if err := os.Mkdir(probe, 0o755); err != nil {
			cgroupWhy = fallbackNoCgroup
			return
		}
		_ = os.Remove(probe)
		cgroupBase = dir
	})
}

// ownCgroup is the cgroup v2 directory of this process.
func ownCgroup() (string, error) {
	f, err := os.Open("/proc/self/cgroup")
	if err != nil {
		return "", err
	}
	defer f.Close()
	rel := ""
	found := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if p, ok := strings.CutPrefix(sc.Text(), "0::"); ok {
			rel, found = p, true
			break
		}
	}
	if !found {
		return "", errors.New("no cgroup v2 entry")
	}
	const root = "/sys/fs/cgroup"
	var st syscall.Statfs_t
	if err := syscall.Statfs(root, &st); err != nil {
		return "", err
	}
	if st.Type != cgroup2Magic {
		return "", errors.New("cgroup v2 is not mounted at " + root)
	}
	return filepath.Join(root, filepath.Clean("/"+rel)), nil
}

func choose() Owner {
	probeCgroup()
	if cgroupBase != "" {
		return Owner{Kind: ControlGroup, Exact: true}
	}
	return Owner{Kind: ProcessGroup, Fallback: cgroupWhy}
}

func newOwner() (treeOwner, error) {
	probeCgroup()
	if cgroupBase == "" {
		return &groupOwner{fallback: cgroupWhy}, nil
	}
	return &cgroupOwner{}, nil
}

// cgroupOwner puts the child, at birth, in a fresh control group of its own; the
// group also is a process group so the polite signal reaches the tree at once.
type cgroupOwner struct {
	dir   string
	group groupOwner
	// fell is set when the control group could not be used for this child and the
	// process group owns it instead.
	fell bool
}

func (o *cgroupOwner) report() Owner {
	if o.fell {
		return o.group.report()
	}
	return Owner{Kind: ControlGroup, Exact: true}
}

func (o *cgroupOwner) start(build func() *exec.Cmd) (*exec.Cmd, error) {
	dir := filepath.Join(cgroupBase, fmt.Sprintf("dsh-%d-%d", os.Getpid(), cgroupSeq.Add(1)))
	if err := os.Mkdir(dir, 0o755); err != nil {
		return o.fallBack(build, err)
	}
	fd, err := syscall.Open(dir, syscall.O_DIRECTORY|syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		_ = os.Remove(dir)
		return o.fallBack(build, err)
	}
	defer syscall.Close(fd)
	cmd := build()
	// Program and directory errors are the caller's, not the control group's;
	// they are returned unchanged and never retried under a process group.
	if err := preflight(cmd); err != nil {
		_ = os.Remove(dir)
		return nil, err
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:     true,
		UseCgroupFD: true,
		CgroupFD:    fd,
		Pdeathsig:   syscall.SIGKILL,
	}
	if err := startLocked(cmd); err != nil {
		cgroupFault := isCgroupStartError(err, dir)
		_ = os.Remove(dir)
		if !cgroupFault {
			return nil, err
		}
		return o.fallBack(build, err)
	}
	o.dir = dir
	o.group.pid = cmd.Process.Pid
	return cmd, nil
}

// preflight reports exec errors (missing or non-executable program, bad working
// directory) before the child is cloned, so a start error that remains can be
// attributed to the control group.
func preflight(cmd *exec.Cmd) error {
	if cmd.Err != nil {
		return cmd.Err
	}
	st, err := os.Stat(cmd.Path)
	if err != nil {
		return &os.PathError{Op: "fork/exec", Path: cmd.Path, Err: underlying(err)}
	}
	if st.IsDir() {
		return &os.PathError{Op: "fork/exec", Path: cmd.Path, Err: syscall.EACCES}
	}
	if err := syscall.Access(cmd.Path, 1 /* X_OK */); err != nil {
		return &os.PathError{Op: "fork/exec", Path: cmd.Path, Err: err}
	}
	if cmd.Dir != "" {
		st, err := os.Stat(cmd.Dir)
		if err != nil {
			return &os.PathError{Op: "chdir", Path: cmd.Dir, Err: underlying(err)}
		}
		if !st.IsDir() {
			return &os.PathError{Op: "chdir", Path: cmd.Dir, Err: syscall.ENOTDIR}
		}
		if err := syscall.Access(cmd.Dir, 1 /* X_OK */); err != nil {
			return &os.PathError{Op: "chdir", Path: cmd.Dir, Err: err}
		}
	}
	return nil
}

func underlying(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// isCgroupStartError reports whether a start error comes from CLONE_INTO_CGROUP
// or the control group itself rather than from exec.
func isCgroupStartError(err error, dir string) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.ENOSYS, syscall.EOPNOTSUPP, syscall.EBUSY, syscall.E2BIG:
		return true
	case syscall.EACCES, syscall.EPERM:
		// Exec can also fail with these (for example a noexec mount); blame the
		// control group only when its process list is not writable.
		return syscall.Access(filepath.Join(dir, "cgroup.procs"), 2 /* W_OK */) != nil
	}
	return false
}

func (o *cgroupOwner) fallBack(build func() *exec.Cmd, cause error) (*exec.Cmd, error) {
	cmd, err := o.group.start(build)
	if err != nil {
		return nil, err
	}
	o.fell = true
	o.group.fallback = fallbackNoCgroup + ": " + cause.Error()
	return cmd, nil
}

// startLocked starts cmd from one goroutine locked to its OS thread; Pdeathsig
// fires when the thread that started the child exits, so that thread must live as
// long as the host.
var starter = sync.OnceValue(func() chan func() {
	ch := make(chan func())
	go func() {
		runtime.LockOSThread()
		for f := range ch {
			f()
		}
	}()
	return ch
})

func startLocked(cmd *exec.Cmd) error {
	done := make(chan error, 1)
	starter() <- func() { done <- cmd.Start() }
	return <-done
}

func (o *cgroupOwner) members() ([]int, error) {
	b, err := os.ReadFile(filepath.Join(o.dir, "cgroup.procs"))
	if err != nil {
		return nil, err
	}
	var pids []int
	for _, f := range strings.Fields(string(b)) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	return pids, nil
}

func (o *cgroupOwner) polite() error {
	if o.fell {
		return o.group.polite()
	}
	_ = o.group.polite()
	pids, err := o.members()
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	return err
}

func (o *cgroupOwner) kill() error {
	if o.fell {
		return o.group.kill()
	}
	_ = o.group.kill()
	if err := os.WriteFile(filepath.Join(o.dir, "cgroup.kill"), []byte("1"), 0); err == nil {
		return nil
	}
	pids, err := o.members()
	for _, pid := range pids {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return err
}

func (o *cgroupOwner) empty() (bool, error) {
	if o.fell {
		return o.group.empty()
	}
	b, err := os.ReadFile(filepath.Join(o.dir, "cgroup.events"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "populated "); ok {
			return strings.TrimSpace(v) == "0", nil
		}
	}
	return false, errors.New("cgroup.events has no populated line")
}

func (o *cgroupOwner) describe() string {
	if o.fell {
		return o.group.describe()
	}
	pids, err := o.members()
	if err != nil {
		return "control group state unknown: " + err.Error()
	}
	return fmt.Sprintf("control group %s still holds %v", filepath.Base(o.dir), pids)
}

func (o *cgroupOwner) release() {
	if !o.fell && o.dir != "" {
		_ = os.Remove(o.dir)
	}
}
