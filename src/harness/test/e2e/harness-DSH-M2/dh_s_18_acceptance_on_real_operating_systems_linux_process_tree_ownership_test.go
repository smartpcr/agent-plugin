//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_linux_process_tree_ownership.feature
// (DH-S-18 Stage 8.4, impl.md 18.3: DH-E-EXEC-1 and DH-E-EXEC-10, TC-21s2).
//
// Each scenario's machine runs a machine agent: this suite's own test binary,
// built for Linux and started there with ptoAgentEnv naming the role. The agent
// records the preflight (systemctl --user state, cgroup v2, whether its own
// control group is writable), then opens the real process provider
// (subprocess.OpenRegistry with the real systemd user manager, which asks
// systemd-run --user once for a delegated scope), starts a deep fixture tree
// through the real subprocess.Start and stops it with StopTree. It prints one
// JSON report: the owner the product reported, the delegated scope, the limits
// outcome, each member's control group while alive and which members outlived
// the stop. The steps assert on it here.
//
// The machines (proof service:linux-systemd-cgroup): when
// DSH_SCENARIO_EXEC10_SSH / DSH_SCENARIO_EXEC10_SSH_NOMANAGER name an SSH
// alias (variants.md: EXEC-1 and EXEC-10 share that machine set), that machine,
// the provided instance, is used as it is. Otherwise the machine is ephemeral,
// on this host's Linux (native, or WSL from Windows), logged in as an ordinary
// user: the systemd machine is that login as it is; the no-user-manager machine
// is the same login with the user bus out of reach (XDG_RUNTIME_DIR an empty
// directory, DBUS_SESSION_BUS_ADDRESS a socket that does not exist), as for an
// account whose manager is stopped. Either way the preflight must hold, or the
// scenario fails; it never skips.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// The agent's inputs, set by the host on the machine.
const (
	ptoAgentEnv   = "PTO_E2E_AGENT"   // the machine role: systemd or nomanager
	ptoEmulateEnv = "PTO_E2E_EMULATE" // "1": make this login the role's machine
	ptoBaseEnv    = "PTO_E2E_BASE"    // the scratch directory on the machine
	ptoDepthEnv   = "PTO_E2E_DEPTH"   // levels of the fixture tree
	ptoMemoryEnv  = "PTO_E2E_MEMORY"  // memory limit in MiB; "" or 0 for none
)

// ptoReportMark prefixes the agent's one report line on its stdout.
const ptoReportMark = "PTO-REPORT "

const (
	ptoAgentWithin = 3 * time.Minute
	ptoBuildWithin = 10 * time.Minute
)

// ptoMachines maps the scenario's machine to its role and the variable that
// names a provided one.
var ptoMachines = map[string]struct{ role, env string }{
	"linux-e2:systemd":         {"systemd", "DSH_SCENARIO_EXEC10_SSH"},
	"linux-e2:no-user-manager": {"nomanager", "DSH_SCENARIO_EXEC10_SSH_NOMANAGER"},
}

// ptoScopeName is the unit name the provider requests (subprocess/scope_linux.go).
var ptoScopeName = regexp.MustCompile(`^dsh-\d+-[0-9a-f]+\.scope$`)

func init() {
	if role := os.Getenv(ptoAgentEnv); role != "" {
		os.Exit(ptoAgentMain(role))
	}
}

// ptoFixture forks a chain of depth+1 shells, each writing its pid to
// level-<n> and ignoring SIGTERM. Level 1 also starts a grandchild that calls
// setsid (leaving the process group) and an orphan, double-forked so its parent
// is gone; both ignore SIGTERM too, so only a kill of the whole tree ends them.
const ptoFixture = `#!/bin/sh
dir=$1; n=$2
trap '' TERM
echo $$ > "$dir/level-$n"
if [ "$n" -gt 0 ]; then
  /bin/sh "$0" "$dir" $((n-1)) &
fi
if [ "$n" -eq 1 ]; then
  setsid /bin/sh -c 'trap "" TERM; echo $$ > "$1/setsid"; while :; do sleep 1; done' sh "$dir" &
  ( /bin/sh -c 'trap "" TERM; echo $$ > "$1/orphan"; while :; do sleep 1; done' sh "$dir" & )
fi
while :; do sleep 1; done
`

// --- the report ---

type ptoReport struct {
	Role     string `json:"role"`
	Emulated bool   `json:"emulated"`
	Kernel   string `json:"kernel"`
	UID      int    `json:"uid"`

	// The preflight.
	UserManager string   `json:"userManager"` // systemctl --user is-system-running, or why it failed
	ManagerUp   bool     `json:"managerUp"`   // running or degraded
	CgroupFS    string   `json:"cgroupFS"`
	OwnCgroup   string   `json:"ownCgroup"` // before the provider start
	OwnWritable bool     `json:"ownWritable"`
	WritableWhy string   `json:"writableWhy,omitempty"`
	AfterCgroup string   `json:"afterCgroup"` // after the provider start
	RegistryErr string   `json:"registryErr,omitempty"`
	Announced   []string `json:"announced"`
	Scope       string   `json:"scope"` // subprocess.DelegatedScope
	TreeOwner   string   `json:"treeOwner"`

	// The tree.
	OwnerKind     string            `json:"ownerKind"`
	OwnerExact    bool              `json:"ownerExact"`
	OwnerFallback string            `json:"ownerFallback"`
	Limits        string            `json:"limits"`
	LimitsDetail  string            `json:"limitsDetail"`
	Members       map[string]int    `json:"members"`
	Cgroups       map[string]string `json:"cgroups"` // member to its cgroup while alive
	TreeDir       string            `json:"treeDir"` // the direct child's group directory
	StopErr       string            `json:"stopErr,omitempty"`
	StopCause     string            `json:"stopCause,omitempty"`
	Rungs         []string          `json:"rungs"`
	Survivors     []string          `json:"survivors"`
	TreeDirGone   bool              `json:"treeDirGone"`
	Swept         []string          `json:"swept,omitempty"` // survivors the agent killed before exiting
	Error         string            `json:"error,omitempty"`
}

// --- the agent, on the machine ---

func ptoAgentMain(role string) int {
	rep := &ptoReport{Role: role, Members: map[string]int{}, Cgroups: map[string]string{}}
	if err := ptoAgent(rep); err != nil {
		rep.Error = err.Error()
	}
	b, err := json.Marshal(rep)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	fmt.Println(ptoReportMark + string(b))
	return 0
}

func ptoAgent(rep *ptoReport) error {
	base := os.Getenv(ptoBaseEnv)
	if !filepath.IsAbs(base) {
		return fmt.Errorf("%s = %q is not an absolute directory", ptoBaseEnv, base)
	}
	depth, err := strconv.Atoi(os.Getenv(ptoDepthEnv))
	if err != nil || depth < 2 {
		return fmt.Errorf("%s = %q: want a depth of at least 2", ptoDepthEnv, os.Getenv(ptoDepthEnv))
	}
	var mib int64
	if v := os.Getenv(ptoMemoryEnv); v != "" {
		if mib, err = strconv.ParseInt(v, 10, 64); err != nil {
			return fmt.Errorf("%s = %q: %w", ptoMemoryEnv, v, err)
		}
	}
	if os.Getenv(ptoEmulateEnv) == "1" {
		rep.Emulated = true
		if rep.Role == "nomanager" {
			// The account's user manager is out of reach: no bus in the runtime
			// directory and no session bus address that answers.
			xdg := filepath.Join(base, "xdg")
			if err := os.MkdirAll(xdg, 0o700); err != nil {
				return err
			}
			_ = os.Setenv("XDG_RUNTIME_DIR", xdg)
			_ = os.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(xdg, "no-bus"))
		}
	}
	rep.UID = os.Getuid()
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		rep.Kernel = strings.TrimSpace(string(out))
	}
	out, err := exec.Command("systemctl", "--user", "is-system-running").CombinedOutput()
	rep.UserManager = strings.TrimSpace(string(out))
	if err != nil && rep.UserManager == "" {
		rep.UserManager = err.Error()
	}
	rep.ManagerUp = rep.UserManager == "running" || rep.UserManager == "degraded"
	if out, err := exec.Command("stat", "-fc", "%T", "/sys/fs/cgroup").Output(); err == nil {
		rep.CgroupFS = strings.TrimSpace(string(out))
	}
	rep.OwnCgroup = ptoSelfCgroup()
	if rep.OwnCgroup != "" {
		probe := filepath.Join("/sys/fs/cgroup", rep.OwnCgroup, fmt.Sprintf("pto-probe-%d", os.Getpid()))
		if err := os.Mkdir(probe, 0o755); err == nil {
			rep.OwnWritable = true
			_ = os.Remove(probe)
		} else {
			rep.WritableWhy = err.Error()
		}
	}

	// The provider start: the real systemd user manager is asked, once.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run := filepath.Join(base, "home", "run")
	if err := os.MkdirAll(run, 0o700); err != nil {
		return err
	}
	var mu sync.Mutex
	_, _, err = subprocess.OpenRegistry(ctx, subprocess.ProviderOptions{RunDir: run, Announce: func(o subprocess.Owner) {
		mu.Lock()
		defer mu.Unlock()
		rep.Announced = append(rep.Announced, o.String())
	}})
	if err != nil {
		rep.RegistryErr = err.Error()
	}
	rep.Scope = subprocess.DelegatedScope()
	rep.TreeOwner = subprocess.TreeOwner().String()
	rep.AfterCgroup = ptoSelfCgroup()

	// The tree.
	pids := filepath.Join(base, "pids")
	fixture := filepath.Join(base, "tree.sh")
	if err := os.MkdirAll(pids, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(fixture, []byte(ptoFixture), 0o700); err != nil {
		return err
	}
	names := []string{"setsid", "orphan"}
	for n := depth; n >= 0; n-- {
		names = append(names, fmt.Sprintf("level-%d", n))
	}
	defer func() {
		// Whatever happened, nothing the fixture started outlives the agent.
		for _, name := range names {
			if pid := ptoReadPid(filepath.Join(pids, name)); pid > 0 && ptoAlive(pid) {
				if p, err := os.FindProcess(pid); err == nil && p.Kill() == nil {
					rep.Swept = append(rep.Swept, name)
				}
			}
		}
	}()
	p, err := subprocess.Start(ctx, subprocess.Spec{
		Argv:   []string{"/bin/sh", fixture, pids, strconv.Itoa(depth)},
		Env:    subprocess.Env{"PATH=/usr/sbin:/usr/bin:/sbin:/bin"},
		Limits: subprocess.Limits{MemoryBytes: mib << 20},
		Launch: sandbox.Trusted("e2e: DH-E-EXEC-1/-10 fixture tree, written by the scenario"),
	})
	if err != nil {
		return fmt.Errorf("start the fixture tree: %w", err)
	}
	o := p.Owner()
	rep.OwnerKind, rep.OwnerExact, rep.OwnerFallback = string(o.Kind), o.Exact, o.Fallback
	lim := p.Limits()
	rep.Limits, rep.LimitsDetail = string(lim.State), lim.Detail

	deadline := time.Now().Add(15 * time.Second)
	for {
		for _, name := range names {
			if pid := ptoReadPid(filepath.Join(pids, name)); pid > 0 {
				rep.Members[name] = pid
			}
		}
		if len(rep.Members) == len(names) || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	for name, pid := range rep.Members {
		rep.Cgroups[name] = ptoCgroupOf(pid)
	}
	if cg := rep.Cgroups[fmt.Sprintf("level-%d", depth)]; cg != "" {
		rep.TreeDir = filepath.Join("/sys/fs/cgroup", cg)
	}
	if len(rep.Members) != len(names) {
		_ = p.Kill()
		return fmt.Errorf("after 15 s the fixture wrote %v, want %v", rep.Members, names)
	}

	sctx, scancel := context.WithTimeout(ctx, 30*time.Second)
	defer scancel()
	if err := p.StopTree(sctx, 500*time.Millisecond); err != nil {
		rep.StopErr = err.Error()
	}
	_ = p.Wait()
	rec := p.StopRecord()
	if rec.Cause != nil {
		rep.StopCause = rec.Cause.Error()
	}
	for _, r := range rec.Rungs {
		rep.Rungs = append(rep.Rungs, fmt.Sprintf("%s ran=%v gone=%v", r.Rung, r.Ran, r.Gone))
	}
	// A killed member is a zombie until its parent, or init, reaps it.
	settle := time.Now().Add(3 * time.Second)
	for {
		rep.Survivors = rep.Survivors[:0]
		for name, pid := range rep.Members {
			if ptoAlive(pid) {
				rep.Survivors = append(rep.Survivors, name)
			}
		}
		if len(rep.Survivors) == 0 || time.Now().After(settle) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	slices.Sort(rep.Survivors)
	if rep.TreeDir != "" {
		_, err := os.Stat(rep.TreeDir)
		rep.TreeDirGone = errors.Is(err, os.ErrNotExist)
	}
	return nil
}

// ptoSelfCgroup is this process's cgroup v2 path, relative to the hierarchy.
func ptoSelfCgroup() string { return ptoCgroupOf(os.Getpid()) }

func ptoCgroupOf(pid int) string {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cgroup")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rel, ok := strings.CutPrefix(line, "0::"); ok {
			return rel
		}
	}
	return ""
}

func ptoReadPid(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return 0
	}
	return pid
}

// ptoAlive reports whether pid runs something: a zombie counts as gone.
func ptoAlive(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 || i+2 >= len(s) {
		return false
	}
	return s[i+2] != 'Z'
}

// --- the machines, from the host ---

var (
	ptoOuterT   *testing.T
	ptoBuildMu  sync.Mutex
	ptoBuilds   = map[string]string{}
	ptoBuildDir string
)

// ptoBuild cross-compiles (or, on Linux of the same architecture, builds) this
// suite for linux/arch, once per architecture.
func ptoBuild(arch string) (string, error) {
	ptoBuildMu.Lock()
	defer ptoBuildMu.Unlock()
	if b, ok := ptoBuilds[arch]; ok {
		return b, nil
	}
	if ptoBuildDir == "" {
		d, err := os.MkdirTemp("", "dsh-e2e-pto-build-")
		if err != nil {
			return "", err
		}
		ptoOuterT.Cleanup(func() { _ = os.RemoveAll(d) })
		ptoBuildDir = d
	}
	pkg, err := os.Getwd()
	if err != nil {
		return "", err
	}
	bin := filepath.Join(ptoBuildDir, "pto-agent-"+arch)
	ctx, cancel := context.WithTimeout(context.Background(), ptoBuildWithin)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "-c", "-tags", "e2e", "-o", bin, ".")
	cmd.Dir = pkg
	cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go test -c (linux/%s): %v\n%s", arch, err, out)
	}
	ptoBuilds[arch] = bin
	return bin, nil
}

// ptoTargetFor is the machine: the provided SSH alias, else this host's Linux.
func ptoTargetFor(machine string) (lcssTarget, error) {
	m := ptoMachines[machine]
	if alias := strings.TrimSpace(os.Getenv(m.env)); alias != "" {
		return lcssSSH{alias}, nil
	}
	switch rt := runtime.GOOS; rt {
	case "linux":
		return lcssLocal{}, nil
	case "windows":
		out, err := lcssWSL{}.shell(context.Background(), nil, "uname", "-s")
		if err != nil || strings.TrimSpace(out) != "Linux" {
			return nil, fmt.Errorf("no %s machine: %s is unset and WSL has no running Linux distribution (uname -s = %q): %v",
				machine, m.env, strings.TrimSpace(out), err)
		}
		return lcssWSL{}, nil
	default:
		return nil, fmt.Errorf("no %s machine: %s is unset and this %s host has no Linux to stand one up on", machine, m.env, rt)
	}
}

// ptoRunAgent copies the agent to a scratch directory on t and runs it as role;
// the directory is removed afterwards.
func ptoRunAgent(t lcssTarget, role string, depth int, mib int) (*ptoReport, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), ptoBuildWithin+ptoAgentWithin)
	defer cancel()
	m, err := t.shell(ctx, nil, "uname", "-m")
	if err != nil {
		return nil, "", err
	}
	arch, err := lcssGoArch(m)
	if err != nil {
		return nil, "", err
	}
	agent, err := ptoBuild(arch)
	if err != nil {
		return nil, "", err
	}
	dir, err := t.shell(ctx, nil, "sh", "-c", `mktemp -d "${HOME:-/tmp}/dsh-e2e-pto.XXXXXX"`)
	if err != nil {
		return nil, "", err
	}
	dir = strings.TrimSpace(dir)
	if !strings.HasPrefix(dir, "/") {
		return nil, "", fmt.Errorf("mktemp gave %q", dir)
	}
	defer func() { _, _ = t.shell(context.Background(), nil, "rm", "-rf", dir) }()
	bin := dir + "/pto-agent"
	if err := t.put(ctx, agent, bin); err != nil {
		return nil, "", err
	}
	argv := []string{"env", ptoAgentEnv + "=" + role, ptoBaseEnv + "=" + dir, ptoDepthEnv + "=" + strconv.Itoa(depth)}
	if mib > 0 {
		argv = append(argv, ptoMemoryEnv+"="+strconv.Itoa(mib))
	}
	if t.emulated() {
		argv = append(argv, ptoEmulateEnv+"=1")
	}
	actx, acancel := context.WithTimeout(ctx, ptoAgentWithin)
	defer acancel()
	out, err := t.shell(actx, nil, append(argv, bin)...)
	if err != nil {
		return nil, out, fmt.Errorf("the agent on %s: %w", t.name(), err)
	}
	for _, line := range strings.Split(out, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), ptoReportMark); ok {
			var r ptoReport
			if err := json.Unmarshal([]byte(rest), &r); err != nil {
				return nil, out, fmt.Errorf("the agent's report: %w", err)
			}
			if r.Error != "" {
				return &r, out, fmt.Errorf("the agent on %s failed: %s", t.name(), r.Error)
			}
			return &r, out, nil
		}
	}
	return nil, out, fmt.Errorf("the agent on %s printed no report:\n%s", t.name(), out)
}

// --- the scenario ---

type ptoWorld struct {
	machine, role string
	wantManager   string
	target        lcssTarget
	report        *ptoReport
	out           string
	depth         int
}

func (w *ptoWorld) givenMachine(machine, manager string) error {
	m, ok := ptoMachines[machine]
	if !ok {
		return fmt.Errorf("unknown machine %q", machine)
	}
	if (m.role == "systemd") != (manager == "running") {
		return fmt.Errorf("the %s machine's user manager is not %q", machine, manager)
	}
	t, err := ptoTargetFor(machine)
	if err != nil {
		return err
	}
	w.machine, w.role, w.wantManager, w.target = machine, m.role, manager, t
	return nil
}

func (w *ptoWorld) run(depth, mib int) error {
	w.depth = depth
	r, out, err := ptoRunAgent(w.target, w.role, depth, mib)
	w.report, w.out = r, out
	if err != nil {
		return err
	}
	fmt.Printf("    [%s (%s), kernel %s, uid %d; preflight: systemctl --user %q, %s, own cgroup %s writable=%v; scope %q; owner %s]\n",
		w.machine, w.target.name(), r.Kernel, r.UID, r.UserManager, r.CgroupFS, r.OwnCgroup, r.OwnWritable, r.Scope, r.TreeOwner)
	return w.preflight()
}

// preflight checks the machine is the variant's (variants.md): cgroup v2, an
// ordinary login whose own control group it cannot write, and the user manager
// running or not.
func (w *ptoWorld) preflight() error {
	r := w.report
	if r.CgroupFS != "cgroup2fs" {
		return fmt.Errorf("preflight: /sys/fs/cgroup on %s is %q, not cgroup v2", w.target.name(), r.CgroupFS)
	}
	if r.UID == 0 || r.OwnWritable {
		return fmt.Errorf("preflight: the login on %s (uid %d) can write its own control group %s: not an ordinary login, no scope would be requested",
			w.target.name(), r.UID, r.OwnCgroup)
	}
	if w.wantManager == "running" && !r.ManagerUp {
		return fmt.Errorf("preflight: systemctl --user on %s reads %q; want running or degraded", w.target.name(), r.UserManager)
	}
	if w.wantManager == "stopped" && r.ManagerUp {
		return fmt.Errorf("preflight: systemctl --user on %s reads %q; want the user manager stopped", w.target.name(), r.UserManager)
	}
	return nil
}

func (w *ptoWorld) whenTree(depth int) error { return w.run(depth, 0) }

func (w *ptoWorld) whenTreeLimited(depth, mib int) error { return w.run(depth, mib) }

func (w *ptoWorld) thenOwnerExact(kind string) error {
	r := w.report
	if r.OwnerKind != kind || !r.OwnerExact || r.OwnerFallback != "" {
		return fmt.Errorf("the tree's owner is %q exact=%v fallback %q; want %q, exact, no fallback (registry: %s)",
			r.OwnerKind, r.OwnerExact, r.OwnerFallback, kind, r.RegistryErr)
	}
	return nil
}

func (w *ptoWorld) thenDelegatedScope() error {
	r := w.report
	if !ptoScopeName.MatchString(r.Scope) {
		return fmt.Errorf("the delegated scope reported is %q; want a dsh-<pid>-<nonce>.scope the user manager granted (owner %s)", r.Scope, r.TreeOwner)
	}
	if filepath.Base(r.AfterCgroup) != r.Scope {
		return fmt.Errorf("after the provider start the host is in %q, not in its delegated scope %s", r.AfterCgroup, r.Scope)
	}
	scopeDir := r.AfterCgroup
	for name, cg := range r.Cgroups {
		if !strings.HasPrefix(cg, scopeDir+"/dsh-") {
			return fmt.Errorf("member %s (pid %d) is in %q, not in a tree group of the scope %s", name, r.Members[name], cg, scopeDir)
		}
		if cg != r.Cgroups[fmt.Sprintf("level-%d", w.depth)] {
			return fmt.Errorf("member %s is in %q, the direct child in %q: the tree is split", name, cg, r.Cgroups[fmt.Sprintf("level-%d", w.depth)])
		}
	}
	fmt.Printf("    [scope %s holds the tree's group %s with %d members]\n", r.Scope, filepath.Base(r.TreeDir), len(r.Members))
	return nil
}

func (w *ptoWorld) thenNoSurvivor() error {
	r := w.report
	if r.StopErr != "" {
		return fmt.Errorf("StopTree: %s", r.StopErr)
	}
	if len(r.Survivors) > 0 {
		return fmt.Errorf("after the stop %v of %v run on (the agent then killed %v)", r.Survivors, r.Members, r.Swept)
	}
	for _, name := range []string{"setsid", "orphan"} {
		if r.Members[name] == 0 {
			return fmt.Errorf("the fixture never started its %s descendant", name)
		}
	}
	return nil
}

func (w *ptoWorld) thenGroupRemoved() error {
	if w.report.TreeDir == "" || !w.report.TreeDirGone {
		return fmt.Errorf("the tree's control group %q is not removed after the stop", w.report.TreeDir)
	}
	return nil
}

// thenScopeGone checks, after the agent exited, that the scope unit it was
// granted is collected (systemd-run --collect): the host leaves nothing.
func (w *ptoWorld) thenScopeGone() error {
	unit := w.report.Scope
	var out string
	for range 50 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		var err error
		out, err = w.target.shell(ctx, nil, "systemctl", "--user", "list-units", "--all", "--plain", "--no-legend", "--no-pager", unit)
		cancel()
		if err != nil {
			return err
		}
		if !strings.Contains(out, unit) {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("after the host exited the scope %s remains on %s:\n%s", unit, w.target.name(), out)
}

func (w *ptoWorld) thenOwnerFallback(kind string) error {
	r := w.report
	if r.OwnerKind != kind || r.OwnerExact {
		return fmt.Errorf("the tree's owner is %q exact=%v; want %q, not exact", r.OwnerKind, r.OwnerExact, kind)
	}
	if !strings.Contains(r.OwnerFallback, "user service manager") {
		return fmt.Errorf("the fallback %q does not name the user service manager", r.OwnerFallback)
	}
	fmt.Printf("    [fallback: %s]\n", r.OwnerFallback)
	return nil
}

func (w *ptoWorld) thenAnnouncedOnce() error {
	r := w.report
	if len(r.Announced) != 1 || !strings.Contains(r.Announced[0], string(subprocess.ProcessGroup)) || !strings.Contains(r.Announced[0], "fallback") {
		return fmt.Errorf("the provider announced %q; want the process-group fallback exactly once", r.Announced)
	}
	if r.Scope != "" {
		return fmt.Errorf("a delegated scope %q is reported with the user manager stopped", r.Scope)
	}
	return nil
}

func (w *ptoWorld) thenLimits(state string) error {
	r := w.report
	if r.Limits != state || r.LimitsDetail == "" {
		return fmt.Errorf("the memory limit is reported %q (%s); want %q with a reason", r.Limits, r.LimitsDetail, state)
	}
	return nil
}

func (w *ptoWorld) thenGroupEndedSetsidSurvives() error {
	r := w.report
	if r.StopErr != "" {
		return fmt.Errorf("StopTree: %s", r.StopErr)
	}
	if !slices.Equal(r.Survivors, []string{"setsid"}) {
		return fmt.Errorf("after the stop the survivors are %v of %v; want the setsid grandchild alone", r.Survivors, r.Members)
	}
	if !slices.Contains(r.Swept, "setsid") {
		return fmt.Errorf("the agent did not end the reported survivor (swept %v)", r.Swept)
	}
	fmt.Printf("    [survivor reported: setsid grandchild pid %d]\n", r.Members["setsid"])
	return nil
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_linux_process_tree_ownership(ctx *godog.ScenarioContext) {
	w := &ptoWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = ptoWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if err != nil && w.out != "" {
			fmt.Printf("    [machine agent output]\n%s\n", w.out)
		}
		return c, nil
	})
	ctx.Step(`^the "([^"]*)" machine whose preflight records the user manager "(running|stopped)"$`, w.givenMachine)
	ctx.Step(`^a command forks a tree (\d+) levels deep with a setsid grandchild and an orphan that ignore SIGTERM, and is stopped$`, w.whenTree)
	ctx.Step(`^a command forks a tree (\d+) levels deep with a setsid grandchild and an orphan that ignore SIGTERM, under a (\d+) MiB memory limit, and is stopped$`, w.whenTreeLimited)
	ctx.Step(`^the tree's owner is reported as "([^"]*)", exact, with no fallback$`, w.thenOwnerExact)
	ctx.Step(`^the owner is the delegated scope the user service manager granted, holding every member$`, w.thenDelegatedScope)
	ctx.Step(`^no descendant survives the stop$`, w.thenNoSurvivor)
	ctx.Step(`^the tree's control group is removed$`, w.thenGroupRemoved)
	ctx.Step(`^the delegated scope is gone once the host exits$`, w.thenScopeGone)
	ctx.Step(`^the tree's owner is reported as "([^"]*)", not exact, with a fallback naming the user service manager$`, w.thenOwnerFallback)
	ctx.Step(`^the fallback is announced exactly once and no delegated scope is reported$`, w.thenAnnouncedOnce)
	ctx.Step(`^the memory limit is reported "([^"]*)"$`, w.thenLimits)
	ctx.Step(`^every member left in the process group is ended and the setsid grandchild is reported as a survivor$`, w.thenGroupEndedSetsidSurvives)
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_linux_process_tree_ownership(t *testing.T) {
	ptoOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_linux_process_tree_ownership,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_linux_process_tree_ownership.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the Linux process tree ownership scenarios failed")
	}
}
