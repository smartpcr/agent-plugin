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
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
)

const cgroup2Magic = 0x63677270

// fallbackNoCgroup is the reported reason when the host's own control group is
// not writable (register U-K1-1) and no provider start asked the user service
// manager for a delegated scope (scope_linux.go), as when a program starts a
// command before, or without, OpenRegistry.
const fallbackNoCgroup = "own control group not writable; delegated scope not requested (U-K1-1)"

var (
	cgroupOnce sync.Once
	cgroupBase string // writable directory new per-tree groups are made in; "" when none
	cgroupWhy  string
	// cgroupScope is the delegated scope unit the user service manager granted
	// at provider start and cgroupBase is; "" when none was asked for or granted.
	cgroupScope string
	cgroupSeq   atomic.Uint64

	// ownCgroupDir finds this process's control group directory; the tests of
	// the scope request replace it to fake a granted scope (scope_linux.go).
	ownCgroupDir = ownCgroup
	// startIntoGroup starts a command whose SysProcAttr places it in a new
	// control group; the same tests replace it, since a faked scope directory is
	// no control group the kernel can start a child in.
	startIntoGroup = startLocked
)

// probeCgroup chooses the parent of the per-tree groups once, without asking
// the user service manager; the provider start asks first (requestOnce).
func probeCgroup() { cgroupOnce.Do(func() { chooseBase(nil) }) }

// chooseBase sets the directory per-tree groups are made in: the host's own
// control group when it is writable; otherwise, when ask is given, the scope the
// user service manager delegates to this process; otherwise none, with the
// reason reported on every handle. ask returns the granted unit's name once this
// process is inside it.
func chooseBase(ask func() (string, error)) {
	dir, err := ownCgroupDir()
	if err != nil {
		cgroupWhy = fallbackNoCgroup + ": " + err.Error()
		return
	}
	if writableGroup(dir) == nil {
		cgroupBase = dir
		return
	}
	if ask == nil {
		cgroupWhy = fallbackNoCgroup
		return
	}
	unit, err := ask()
	if err != nil {
		cgroupWhy = "own control group not writable; " + err.Error()
		return
	}
	if dir, err = ownCgroupDir(); err == nil {
		err = writableGroup(dir)
	}
	if err != nil {
		cgroupWhy = fmt.Sprintf("own control group not writable; delegated scope %s not usable (U-K1-1): %v", unit, err)
		return
	}
	cgroupBase, cgroupScope = dir, unit
}

// writableGroup checks that a new control group can be made in dir.
func writableGroup(dir string) error {
	probe := filepath.Join(dir, fmt.Sprintf("dsh-probe-%d", os.Getpid()))
	if err := os.Mkdir(probe, 0o755); err != nil {
		return err
	}
	_ = os.Remove(probe)
	return nil
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

func newOwner(l Limits) (treeOwner, error) {
	if unit := scopeRequest.Load(); unit != nil {
		// The scope request is under way: its own launch must not wait for the
		// choice it is making (scope_linux.go).
		return &requestingOwner{unit: *unit, lim: l}, nil
	}
	return chosenOwner(l), nil
}

// chosenOwner is a new owner of the rung chosen once for this process.
func chosenOwner(l Limits) treeOwner {
	probeCgroup()
	if cgroupBase == "" {
		return &groupOwner{fallback: cgroupWhy, lim: l}
	}
	return &cgroupOwner{lim: l, group: groupOwner{lim: l}}
}

// cgroupOwner puts the child, at birth, in a fresh control group of its own; the
// group also is a process group so the polite signal reaches the tree at once.
type cgroupOwner struct {
	dir   string
	group groupOwner
	// fell is set when the control group could not be used for this child and the
	// process group owns it instead.
	fell bool
	// lim are the Spec's Limits, written to the group before the child starts;
	// limits is what was applied.
	lim    Limits
	limits LimitsReport
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
	o.limits = applyGroupLimits(dir, o.lim)
	fd, err := syscall.Open(dir, syscall.O_DIRECTORY|syscall.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		_ = os.Remove(dir)
		return o.fallBack(build, err)
	}
	defer syscall.Close(fd)
	cmd := build()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:     true,
		UseCgroupFD: true,
		CgroupFD:    fd,
		Pdeathsig:   syscall.SIGKILL,
	}
	if err := startIntoGroup(cmd); err != nil {
		_ = os.Remove(dir)
		if !cgroupStartError(cmd, err) {
			return nil, err
		}
		return o.fallBack(build, err)
	}
	o.dir = dir
	o.group.pid = cmd.Process.Pid
	return cmd, nil
}

// cgroupStartError reports whether a failed start is due to CLONE_INTO_CGROUP or
// the new control group rather than the program, its directory or its arguments.
// Only those failures may fall back to a process group; every other error is the
// caller's start failure and must not be retried or recorded as a fallback.
func cgroupStartError(cmd *exec.Cmd, err error) bool {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return false
	}
	switch errno {
	case syscall.ENOSYS, syscall.EOPNOTSUPP, syscall.EBUSY, syscall.E2BIG:
		return true
	case syscall.EACCES, syscall.EPERM:
		// The same errnos come from exec or chdir in the child; blame the control
		// group only when the program and directory are themselves usable.
		return execUsable(cmd)
	}
	return false
}

func execUsable(cmd *exec.Cmd) bool {
	if cmd.Dir != "" {
		st, err := os.Stat(cmd.Dir)
		if err != nil || !st.IsDir() || syscall.Access(cmd.Dir, 0x1) != nil {
			return false
		}
	}
	path := cmd.Path
	if !filepath.IsAbs(path) && cmd.Dir != "" {
		path = filepath.Join(cmd.Dir, path)
	}
	st, err := os.Stat(path)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	return syscall.Access(path, 0x1) == nil
}

func (o *cgroupOwner) fallBack(build func() *exec.Cmd, cause error) (*exec.Cmd, error) {
	o.fell = true
	o.group.fallback = fallbackNoCgroup + ": " + cause.Error()
	return o.group.start(build)
}

// startLocked starts cmd from one goroutine locked to its OS thread; Pdeathsig
// fires when the thread that started the child exits, so that thread must live as
// long as the host. The goroutine is started at package initialisation, outside
// any testing/synctest bubble: started lazily inside one, it would belong to that
// bubble and outlive it.
var starter = func() chan func() {
	ch := make(chan func())
	go func() {
		runtime.LockOSThread()
		for f := range ch {
			f()
		}
	}()
	return ch
}()

func startLocked(cmd *exec.Cmd) error {
	done := make(chan error, 1)
	starter <- func() { done <- cmd.Start() }
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

func (o *cgroupOwner) limitsReport() LimitsReport {
	if o.fell {
		return o.group.limitsReport()
	}
	return o.limits
}

// member is exact: pid's control group is the tree's or one below it.
func (o *cgroupOwner) member(pid int) bool {
	if o.fell {
		return o.group.member(pid)
	}
	if o.dir == "" {
		return false
	}
	rel, ok := procCgroup(pid)
	if !ok {
		return false
	}
	dir := filepath.Join("/sys/fs/cgroup", filepath.Clean("/"+rel))
	return dir == o.dir || strings.HasPrefix(dir, o.dir+"/")
}

func (o *cgroupOwner) release() {
	if !o.fell && o.dir != "" {
		_ = os.Remove(o.dir)
	}
}

// procCgroup is the cgroup v2 path of pid, relative to the hierarchy's root.
func procCgroup(pid int) (string, bool) {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rel, ok := strings.CutPrefix(line, "0::"); ok {
			return rel, true
		}
	}
	return "", false
}

// The controllers the Limits need, enabled once for the children of the parent
// group (cgroupBase). cgroup v2 lets a group hand controllers to its children
// only while it holds no process itself, so the host first moves into a leaf
// group of its own, dsh-host-<pid>, beside the per-command groups.
var (
	controllersOnce sync.Once
	controllersOn   map[string]bool
	controllersWhy  string
)

// HostLeafPrefix starts the name of the leaf group the host moves into when a
// tree first asks for Limits.
const HostLeafPrefix = "dsh-host-"

func enableControllers() {
	controllersOnce.Do(func() {
		controllersOn = map[string]bool{}
		base := cgroupBase
		avail, err := os.ReadFile(filepath.Join(base, "cgroup.controllers"))
		if err != nil {
			controllersWhy = "the parent group's controllers cannot be read: " + err.Error()
			return
		}
		var want []string
		for _, c := range strings.Fields(string(avail)) {
			if c == "memory" || c == "pids" {
				want = append(want, c)
			}
		}
		if len(want) == 0 {
			controllersWhy = "the memory and pids controllers are not delegated to " + base
			return
		}
		if !subtreeHas(base, want) {
			if own, err := ownCgroupDir(); err == nil && own == base {
				leaf := filepath.Join(base, HostLeafPrefix+strconv.Itoa(os.Getpid()))
				if err := os.Mkdir(leaf, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
					controllersWhy = "the host's leaf group cannot be made: " + err.Error()
					return
				}
				if err := os.WriteFile(filepath.Join(leaf, "cgroup.procs"), []byte(strconv.Itoa(os.Getpid())), 0); err != nil {
					controllersWhy = "the host cannot move into its leaf group: " + err.Error()
					return
				}
			}
			ctl := "+" + strings.Join(want, " +")
			if err := os.WriteFile(filepath.Join(base, "cgroup.subtree_control"), []byte(ctl), 0); err != nil {
				controllersWhy = "the controllers cannot be enabled for the per-command groups: " + err.Error()
			}
		}
		b, _ := os.ReadFile(filepath.Join(base, "cgroup.subtree_control"))
		for _, c := range strings.Fields(string(b)) {
			controllersOn[c] = true
		}
	})
}

func subtreeHas(base string, want []string) bool {
	b, err := os.ReadFile(filepath.Join(base, "cgroup.subtree_control"))
	if err != nil {
		return false
	}
	on := strings.Fields(string(b))
	for _, c := range want {
		if !slices.Contains(on, c) {
			return false
		}
	}
	return true
}

// applyGroupLimits writes the Limits to a new per-command group before its child
// starts: memory.max and pids.max, where those controllers are enabled.
func applyGroupLimits(dir string, l Limits) LimitsReport {
	if l.IsZero() {
		return LimitsReport{State: LimitsNone}
	}
	enableControllers()
	var applied, missing []string
	set := func(ctl, file string, v int64) {
		if v <= 0 {
			return
		}
		if !controllersOn[ctl] {
			why := controllersWhy
			if why == "" {
				why = "the " + ctl + " controller is not enabled"
			}
			missing = append(missing, file+" unavailable: "+why)
			return
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(strconv.FormatInt(v, 10)), 0); err != nil {
			missing = append(missing, file+" unavailable: "+err.Error())
			return
		}
		applied = append(applied, fmt.Sprintf("%s=%d", file, v))
	}
	set("memory", "memory.max", l.MemoryBytes)
	set("pids", "pids.max", int64(l.Processes))
	return limitsOf(l, applied, missing)
}
