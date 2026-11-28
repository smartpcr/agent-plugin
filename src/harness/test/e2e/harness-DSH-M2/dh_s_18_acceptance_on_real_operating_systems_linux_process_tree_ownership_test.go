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
// The machines (proof service:linux-systemd-cgroup), always reached over SSH:
// when DSH_SCENARIO_EXEC10_SSH / DSH_SCENARIO_EXEC10_SSH_NOMANAGER name an SSH
// alias (variants.md: EXEC-1 and EXEC-10 share that machine set), that machine,
// the provided instance, is used as it is. Otherwise, on a Linux host, the
// suite stands up an ephemeral one on this host's systemd Linux through sudo -n:
// two fresh scratch accounts, a private sshd on
// 127.0.0.1 with its own host key, and a client key. On Windows with neither
// variable set the scenario fails: a WSL machine counts only as the WSL variant
// (M2-R-05), never as linux-e2. Each SSH login opens a real
// PAM/logind session. The no-user-manager account's user@<uid>.service is
// stopped and masked and the account never lingers, so its login has no
// systemd --user at all. Teardown ends the logins, stops sshd, unmasks the unit
// and removes both accounts. The agent's preflight records the account's
// user@<uid>.service state, linger and systemctl --user over that SSH login;
// it must hold, or the scenario fails. It never skips.

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

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// The agent's inputs, set by the host on the machine.
const (
	ptoAgentEnv  = "PTO_E2E_AGENT"  // the machine role: systemd or nomanager
	ptoBaseEnv   = "PTO_E2E_BASE"   // the scratch directory on the machine
	ptoDepthEnv  = "PTO_E2E_DEPTH"  // levels of the fixture tree
	ptoMemoryEnv = "PTO_E2E_MEMORY" // memory limit in MiB; "" or 0 for none
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
	Role   string `json:"role"`
	Kernel string `json:"kernel"`
	UID    int    `json:"uid"`
	User   string `json:"user"`

	// The preflight.
	SSHConnection string   `json:"sshConnection"` // $SSH_CONNECTION: the agent runs in an SSH login
	Session       string   `json:"session"`       // $XDG_SESSION_ID: a logind session
	ManagerUnit   string   `json:"managerUnit"`   // systemctl is-active user@<uid>.service
	ManagerFile   string   `json:"managerFile"`   // systemctl is-enabled user@<uid>.service
	Linger        string   `json:"linger"`        // loginctl show-user <uid> -p Linger
	UserManager   string   `json:"userManager"`   // systemctl --user is-system-running, or why it failed
	ManagerUp     bool     `json:"managerUp"`     // running or degraded
	CgroupFS      string   `json:"cgroupFS"`
	OwnCgroup     string   `json:"ownCgroup"` // before the provider start
	OwnWritable   bool     `json:"ownWritable"`
	WritableWhy   string   `json:"writableWhy,omitempty"`
	AfterCgroup   string   `json:"afterCgroup"` // after the provider start
	RegistryErr   string   `json:"registryErr,omitempty"`
	Announced     []string `json:"announced"`
	Scope         string   `json:"scope"` // subprocess.DelegatedScope
	TreeOwner     string   `json:"treeOwner"`

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
	StopReason    string            `json:"stopReason,omitempty"` // herr.Kind of StopTree's error
	StopCause     string            `json:"stopCause,omitempty"`
	Rungs         []string          `json:"rungs"`
	// What the product reports as left running: StopRecord().Survivors after the
	// stop, and its reason (subprocess/tree-survivors).
	RecordSurvivors string `json:"recordSurvivors,omitempty"`
	RecordReason    string `json:"recordReason,omitempty"`
	// ScanSurvivors is the agent's own /proc scan of the members after the stop:
	// corroboration only, never the product's report.
	ScanSurvivors []string `json:"scanSurvivors"`
	TreeDirGone   bool     `json:"treeDirGone"`
	Swept         []string `json:"swept,omitempty"`   // survivors the agent killed and saw gone before exiting
	Unswept       []string `json:"unswept,omitempty"` // survivors still running after the agent's kill, with why
	Error         string   `json:"error,omitempty"`
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
	rep.UID = os.Getuid()
	rep.User = os.Getenv("USER")
	rep.SSHConnection = os.Getenv("SSH_CONNECTION")
	rep.Session = os.Getenv("XDG_SESSION_ID")
	if out, err := exec.Command("uname", "-r").Output(); err == nil {
		rep.Kernel = strings.TrimSpace(string(out))
	}
	unit := fmt.Sprintf("user@%d.service", rep.UID)
	rep.ManagerUnit = ptoOutput("systemctl", "is-active", unit)
	rep.ManagerFile = ptoOutput("systemctl", "is-enabled", unit)
	rep.Linger = ptoOutput("loginctl", "show-user", strconv.Itoa(rep.UID), "-p", "Linger", "--value")
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
		// Whatever happened, nothing the fixture started outlives the agent. A
		// member counts as swept only once /proc shows it gone; one still
		// running after a bounded wait is reported, so the scenario fails
		// rather than leaving it on the machine.
		killed := map[string]int{}
		for _, name := range names {
			pid := ptoReadPid(filepath.Join(pids, name))
			if pid <= 0 || !ptoAlive(pid) {
				continue
			}
			p, err := os.FindProcess(pid)
			if err == nil {
				err = p.Kill()
			}
			if err != nil && ptoAlive(pid) {
				rep.Unswept = append(rep.Unswept, fmt.Sprintf("%s (pid %d): kill: %v", name, pid, err))
				continue
			}
			killed[name] = pid
		}
		gone := time.Now().Add(5 * time.Second)
		for len(killed) > 0 {
			for name, pid := range killed {
				if !ptoAlive(pid) {
					rep.Swept = append(rep.Swept, name)
					delete(killed, name)
				}
			}
			if len(killed) == 0 || time.Now().After(gone) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		for name, pid := range killed {
			rep.Unswept = append(rep.Unswept, fmt.Sprintf("%s (pid %d): still running 5 s after SIGKILL", name, pid))
		}
		slices.Sort(rep.Swept)
		slices.Sort(rep.Unswept)
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
		rep.StopErr, rep.StopReason = err.Error(), string(herr.Kind(err))
	}
	_ = p.Wait()
	rec := p.StopRecord()
	if rec.Cause != nil {
		rep.StopCause = rec.Cause.Error()
	}
	if rec.Survivors != nil {
		rep.RecordSurvivors, rep.RecordReason = rec.Survivors.Error(), string(herr.Kind(rec.Survivors))
	}
	for _, r := range rec.Rungs {
		rep.Rungs = append(rep.Rungs, fmt.Sprintf("%s ran=%v gone=%v", r.Rung, r.Ran, r.Gone))
	}
	// A killed member is a zombie until its parent, or init, reaps it.
	settle := time.Now().Add(3 * time.Second)
	for {
		rep.ScanSurvivors = rep.ScanSurvivors[:0]
		for name, pid := range rep.Members {
			if ptoAlive(pid) {
				rep.ScanSurvivors = append(rep.ScanSurvivors, name)
			}
		}
		if len(rep.ScanSurvivors) == 0 || time.Now().After(settle) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	slices.Sort(rep.ScanSurvivors)
	if rep.TreeDir != "" {
		_, err := os.Stat(rep.TreeDir)
		rep.TreeDirGone = errors.Is(err, os.ErrNotExist)
	}
	return nil
}

// ptoSelfCgroup is this process's cgroup v2 path, relative to the hierarchy.
func ptoSelfCgroup() string { return ptoCgroupOf(os.Getpid()) }

// ptoOutput is a command's trimmed output (a failing query still prints its
// answer, "inactive" or "masked"), or why it gave none.
func ptoOutput(argv ...string) string {
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if s := strings.TrimSpace(string(out)); s != "" {
		return s
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

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

// ptoTargetFor is the machine: the provided SSH alias, else the ephemeral
// scratch account over SSH this run provisions.
func ptoTargetFor(machine string) (lcssTarget, error) {
	m := ptoMachines[machine]
	if alias := strings.TrimSpace(os.Getenv(m.env)); alias != "" {
		return lcssSSH{alias}, nil
	}
	if runtime.GOOS == "windows" {
		return nil, fmt.Errorf("no %s machine: %s is unset, and an ephemeral machine on this Windows host would run in WSL, "+
			"whose results count only as the WSL variant (M2-R-05), never as %s; name a team-kept VM in %s", machine, m.env, machine, m.env)
	}
	p, err := ptoProvisioned()
	if err != nil {
		return nil, fmt.Errorf("no %s machine: %s is unset and an ephemeral one could not be stood up: %w", machine, m.env, err)
	}
	t := p.machine(m.role)
	if t.emulated() {
		return nil, fmt.Errorf("no %s machine: %s is unset and the ephemeral one is on %s, which counts only as the WSL variant (M2-R-05)", machine, m.env, p.venue)
	}
	return t, nil
}

// ptoRootFn runs argv as root on this host's Linux.
type ptoRootFn func(ctx context.Context, stdin []byte, argv ...string) (string, error)

// ptoRootRunner is root on this host's Linux: WSL's root from Windows, or
// sudo -n (no password prompt) on Linux.
func ptoRootRunner() (ptoRootFn, string, error) {
	switch rt := runtime.GOOS; rt {
	case "windows":
		root := func(ctx context.Context, stdin []byte, argv ...string) (string, error) {
			return lcssRun(exec.CommandContext(ctx, "wsl.exe", append([]string{"-u", "root", "-e"}, argv...)...), stdin)
		}
		out, err := root(context.Background(), nil, "uname", "-s")
		if err != nil || strings.TrimSpace(out) != "Linux" {
			return nil, "", fmt.Errorf("WSL has no running Linux distribution reachable as root (uname -s = %q): %v", strings.TrimSpace(out), err)
		}
		return root, "WSL on this Windows host", nil
	case "linux":
		root := func(ctx context.Context, stdin []byte, argv ...string) (string, error) {
			if os.Geteuid() == 0 {
				return lcssRun(exec.CommandContext(ctx, argv[0], argv[1:]...), stdin)
			}
			return lcssRun(exec.CommandContext(ctx, "sudo", append([]string{"-n", "--"}, argv...)...), stdin)
		}
		if _, err := root(context.Background(), nil, "true"); err != nil {
			return nil, "", fmt.Errorf("root on this Linux host is not available without a password (sudo -n): %v", err)
		}
		return root, "this Linux host", nil
	default:
		return nil, "", fmt.Errorf("this %s host has no Linux to stand a machine up on", rt)
	}
}

// ptoProvisionScript, run as root, stands up the ephemeral machine and prints
// key=value lines. Everything it makes is listed in $W/state for the teardown,
// which runs even when this fails part way. With no installed sshd it unpacks
// the distribution's openssh-server package into a cache directory, without
// installing it.
const ptoProvisionScript = `set -eu
W=$(mktemp -d /tmp/dsh-e2e-pto-sshd.XXXXXX)
chmod 711 "$W"; : > "$W/state"
echo "dir=$W"
SSHD=/usr/sbin/sshd; L=
if [ ! -x "$SSHD" ]; then
  C=/var/cache/dsh-e2e/openssh-server
  if [ ! -x "$C/usr/sbin/sshd" ]; then
    command -v apt-get >/dev/null || { echo "no sshd installed and no apt-get to fetch openssh-server" >&2; exit 3; }
    rm -rf "$C" "$C.deb"; mkdir -p "$C.deb"
    (cd "$C.deb" && apt-get download openssh-server >&2 && { apt-get download libwrap0 >&2 || true; })
    for d in "$C.deb"/*.deb; do dpkg -x "$d" "$C"; done
  fi
  SSHD=$C/usr/sbin/sshd
  L=$(find "$C" -name 'libwrap.so.0' -printf '%h' -quit)
fi
[ -n "$L" ] && export LD_LIBRARY_PATH="$L"
echo "sshd=$SSHD"
if ! getent passwd sshd >/dev/null; then
  useradd -r -M -d /run/sshd -s /usr/sbin/nologin sshd; echo "created-sshd-user -" >> "$W/state"
fi
if [ ! -d /run/sshd ]; then mkdir -p /run/sshd; echo "created-run-sshd -" >> "$W/state"; fi
chmod 755 /run/sshd
R=$(od -An -N3 -tx1 /dev/urandom | tr -d ' \n')
S=dshptos$R; N=dshpton$R
for u in "$S" "$N"; do useradd -m -s /bin/sh "$u"; echo "user $u" >> "$W/state"; done
ssh-keygen -q -t ed25519 -N '' -C dsh-e2e-pto-host -f "$W/host"
ssh-keygen -q -t ed25519 -N '' -C dsh-e2e-pto-client -f "$W/client"
for u in "$S" "$N"; do
  h=$(getent passwd "$u" | cut -d: -f6)
  install -d -o "$u" -g "$u" -m 700 "$h/.ssh"
  install -o "$u" -g "$u" -m 600 "$W/client.pub" "$h/.ssh/authorized_keys"
done
NU=$(id -u "$N")
loginctl disable-linger "$N" 2>/dev/null || true
systemctl stop "user@$NU.service" 2>/dev/null || true
systemctl mask --runtime "user@$NU.service" >&2
echo "masked user@$NU.service" >> "$W/state"
ok=
for i in 1 2 3 4 5 6 7 8 9 10; do
  P=$(( $(od -An -N2 -tu2 /dev/urandom | tr -d ' ') % 20000 + 20000 ))
  cat > "$W/sshd_config" <<EOF
Port $P
ListenAddress 127.0.0.1
HostKey $W/host
PidFile $W/sshd.pid
UsePAM yes
PubkeyAuthentication yes
PasswordAuthentication no
KbdInteractiveAuthentication no
PermitRootLogin no
AllowUsers $S $N
EOF
  if "$SSHD" -f "$W/sshd_config" -E "$W/sshd.log"; then ok=1; break; fi
done
[ -n "$ok" ] || { cat "$W/sshd.log" >&2; echo "sshd did not start" >&2; exit 4; }
for i in $(seq 50); do [ -s "$W/sshd.pid" ] && break; sleep 0.1; done
echo "[127.0.0.1]:$P $(cut -d' ' -f1,2 "$W/host.pub")" > "$W/known_hosts"
echo "port=$P"
echo "key=$W/client"
echo "known=$W/known_hosts"
echo "systemd=$S"
echo "nomanager=$N"
echo "nomanager-unit=user@$NU.service $(systemctl is-active "user@$NU.service" || true) $(systemctl is-enabled "user@$NU.service" || true)"
`

// ptoTeardownScript, run as root with $W, removes what the provisioning made.
const ptoTeardownScript = `W=$1
case "$W" in /tmp/dsh-e2e-pto-sshd.*) ;; *) echo "refusing to tear down $W" >&2; exit 2;; esac
[ -s "$W/sshd.pid" ] && kill "$(cat "$W/sshd.pid")" 2>/dev/null
users=$(awk '$1=="user"{print $2}' "$W/state")
for u in $users; do loginctl terminate-user "$u" 2>/dev/null; done
sleep 1
for u in $users; do pkill -KILL -u "$u" 2>/dev/null; done
sleep 0.5
fail=0
while read -r k v; do
  case $k in
    masked) systemctl unmask --runtime "$v" >/dev/null 2>&1 || fail=1;;
    user) userdel -r "$v" >/dev/null 2>&1; ! id "$v" >/dev/null 2>&1 || fail=1;;
    created-sshd-user) userdel sshd >/dev/null 2>&1;;
    created-run-sshd) rmdir /run/sshd 2>/dev/null;;
  esac
done < "$W/state"
rm -rf "$W"
exit $fail
`

// ptoProvision is the ephemeral machine: both scratch accounts behind one
// private sshd.
type ptoProvision struct {
	root          ptoRootFn
	venue         string
	wsl           bool // stood up in WSL: the WSL variant only (M2-R-05)
	port          string
	key, known    string
	users         map[string]string // role to account
	nomanagerUnit string            // the unit, is-active and is-enabled as root read them
}

func (p *ptoProvision) machine(role string) ptoSSHMachine {
	return ptoSSHMachine{root: p.root, venue: p.venue, wsl: p.wsl, port: p.port, key: p.key, known: p.known, user: p.users[role]}
}

var (
	ptoProvMu  sync.Mutex
	ptoProv    *ptoProvision
	ptoProvErr error
)

// ptoProvisioned stands the ephemeral machine up once per run; the outer
// test's cleanup tears it down.
func ptoProvisioned() (*ptoProvision, error) {
	ptoProvMu.Lock()
	defer ptoProvMu.Unlock()
	if ptoProv != nil || ptoProvErr != nil {
		return ptoProv, ptoProvErr
	}
	ptoProv, ptoProvErr = ptoStandUp()
	return ptoProv, ptoProvErr
}

func ptoStandUp() (*ptoProvision, error) {
	root, venue, err := ptoRootRunner()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out, runErr := root(ctx, []byte(ptoProvisionScript), "sh", "-s")
	kv := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			kv[k] = v
		}
	}
	if dir := kv["dir"]; strings.HasPrefix(dir, "/tmp/dsh-e2e-pto-sshd.") {
		ptoOuterT.Cleanup(func() {
			tctx, tcancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer tcancel()
			if out, err := root(tctx, []byte(ptoTeardownScript), "sh", "-s", "--", dir); err != nil {
				ptoOuterT.Errorf("tearing down the ephemeral machine %s: %v\n%s", dir, err, out)
			}
		})
	}
	if runErr != nil {
		return nil, runErr
	}
	p := &ptoProvision{root: root, venue: venue, wsl: runtime.GOOS == "windows", port: kv["port"], key: kv["key"], known: kv["known"],
		users: map[string]string{"systemd": kv["systemd"], "nomanager": kv["nomanager"]}, nomanagerUnit: kv["nomanager-unit"]}
	if p.port == "" || p.key == "" || p.users["systemd"] == "" || p.users["nomanager"] == "" {
		return nil, fmt.Errorf("the provisioning printed no machine:\n%s", out)
	}
	if f := strings.Fields(p.nomanagerUnit); len(f) != 3 || f[1] == "active" || f[2] != "masked-runtime" {
		return nil, fmt.Errorf("the no-user-manager account's unit reads %q as root; want it inactive and masked-runtime", p.nomanagerUnit)
	}
	fmt.Printf("    [ephemeral machine on %s: sshd 127.0.0.1:%s, scratch accounts %s (user manager on login) and %s (%s)]\n",
		venue, p.port, p.users["systemd"], p.users["nomanager"], p.nomanagerUnit)
	return p, nil
}

// ptoSSHMachine is one scratch account of the ephemeral machine, reached by a
// real SSH login (key auth, pinned host key) from root on this host's Linux.
type ptoSSHMachine struct {
	root                    ptoRootFn
	venue, port, key, known string
	wsl                     bool
	user                    string
}

func (m ptoSSHMachine) name() string {
	return fmt.Sprintf("ephemeral machine %s@127.0.0.1:%s (scratch account over SSH, %s)", m.user, m.port, m.venue)
}
func (m ptoSSHMachine) emulated() bool { return m.wsl }
func (m ptoSSHMachine) shell(ctx context.Context, stdin []byte, argv ...string) (string, error) {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
	}
	return m.root(ctx, stdin, "ssh", "-F", "/dev/null", "-o", "BatchMode=yes", "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=yes", "-o", "UserKnownHostsFile="+m.known, "-o", "LogLevel=ERROR",
		"-i", m.key, "-p", m.port, m.user+"@127.0.0.1", strings.Join(quoted, " "))
}
func (m ptoSSHMachine) put(ctx context.Context, src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	_, err = m.shell(ctx, b, "sh", "-c", "cat > \"$0\" && chmod 0755 \"$0\"", dst)
	return err
}

// ptoScopeCgroups lists cgroup directories named unit on the ephemeral
// machine, read as root; a provided machine is not asked ("", false).
func ptoScopeCgroups(t lcssTarget, unit string) (string, bool, error) {
	m, ok := t.(ptoSSHMachine)
	if !ok {
		return "", false, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := m.root(ctx, nil, "find", "/sys/fs/cgroup/user.slice", "-type", "d", "-name", unit)
	return strings.TrimSpace(out), true, err
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
			if len(r.Unswept) > 0 {
				return &r, out, fmt.Errorf("the agent on %s could not end fixture processes, which are left running (error %q): %v", t.name(), r.Error, r.Unswept)
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
	fmt.Printf("    [%s, %s, kernel %s, %s uid %d over SSH %q session %q; preflight: user@%d.service %q (%s), linger %q, systemctl --user %q, %s, own cgroup %s writable=%v; scope %q; owner %s]\n",
		w.machine, w.target.name(), r.Kernel, r.User, r.UID, r.SSHConnection, r.Session, r.UID, r.ManagerUnit, r.ManagerFile, r.Linger,
		r.UserManager, r.CgroupFS, r.OwnCgroup, r.OwnWritable, r.Scope, r.TreeOwner)
	return w.preflight()
}

// preflight checks the machine is the variant's (variants.md): an SSH login,
// cgroup v2, an ordinary account whose own control group it cannot write, and
// its user manager running, or stopped with no lingering to start it.
func (w *ptoWorld) preflight() error {
	r := w.report
	if r.SSHConnection == "" {
		return fmt.Errorf("preflight: the agent on %s is not in an SSH login (SSH_CONNECTION unset)", w.target.name())
	}
	if r.CgroupFS != "cgroup2fs" {
		return fmt.Errorf("preflight: /sys/fs/cgroup on %s is %q, not cgroup v2", w.target.name(), r.CgroupFS)
	}
	if r.UID == 0 || r.OwnWritable {
		return fmt.Errorf("preflight: the login on %s (uid %d) can write its own control group %s: not an ordinary login, no scope would be requested",
			w.target.name(), r.UID, r.OwnCgroup)
	}
	if w.wantManager == "running" && (!r.ManagerUp || r.ManagerUnit != "active") {
		return fmt.Errorf("preflight: on %s user@%d.service is %q and systemctl --user reads %q; want the user manager active and running",
			w.target.name(), r.UID, r.ManagerUnit, r.UserManager)
	}
	if w.wantManager == "stopped" && (r.ManagerUp || r.ManagerUnit == "active" || r.ManagerUnit == "activating" || r.Linger == "yes") {
		return fmt.Errorf("preflight: on %s user@%d.service is %q (%s), linger %q, systemctl --user reads %q; want the user manager stopped and not lingering",
			w.target.name(), r.UID, r.ManagerUnit, r.ManagerFile, r.Linger, r.UserManager)
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
	if r.RecordSurvivors != "" {
		return fmt.Errorf("StopRecord reports survivors: %s", r.RecordSurvivors)
	}
	if len(r.ScanSurvivors) > 0 {
		return fmt.Errorf("after the stop %v of %v run on (the agent then killed %v)", r.ScanSurvivors, r.Members, r.Swept)
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
			// The user manager no longer lists it; on the ephemeral machine its
			// control group must be gone from the hierarchy too.
			dirs, asked, err := ptoScopeCgroups(w.target, unit)
			if err != nil {
				return err
			}
			if asked && dirs != "" {
				return fmt.Errorf("after the host exited the scope %s is unlisted but its control group remains: %s", unit, dirs)
			}
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
	pid := r.Members["setsid"]
	if pid == 0 {
		return fmt.Errorf("the fixture never started its setsid grandchild (members %v)", r.Members)
	}
	// The product's report: StopTree's error and StopRecord().Survivors must both
	// carry subprocess/tree-survivors naming the setsid grandchild's pid. A
	// product that sees the group empty and reports nothing fails here, whatever
	// the agent's own scan finds.
	named := regexp.MustCompile(`\b` + strconv.Itoa(pid) + `\b`)
	want := string(subprocess.ReasonSurvivors)
	if r.StopReason != want || !named.MatchString(r.StopErr) {
		return fmt.Errorf("StopTree returned %q (reason %q); want %s naming the setsid grandchild pid %d", r.StopErr, r.StopReason, want, pid)
	}
	if r.RecordReason != want || !named.MatchString(r.RecordSurvivors) {
		return fmt.Errorf("StopRecord().Survivors = %q (reason %q); want %s naming the setsid grandchild pid %d", r.RecordSurvivors, r.RecordReason, want, pid)
	}
	// Corroboration from /proc: the group's members were ended and only the
	// grandchild that left it runs on.
	if !slices.Equal(r.ScanSurvivors, []string{"setsid"}) {
		return fmt.Errorf("after the stop /proc shows %v of %v running; want the setsid grandchild alone", r.ScanSurvivors, r.Members)
	}
	if len(r.Unswept) > 0 || !slices.Contains(r.Swept, "setsid") {
		return fmt.Errorf("the agent did not confirm the reported survivor ended (swept %v, still running %v)", r.Swept, r.Unswept)
	}
	fmt.Printf("    [survivor reported by the product: setsid grandchild pid %d: %s]\n", pid, r.RecordSurvivors)
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
	ctx.Step(`^the "([^"]*)" machine, an account reached over SSH, whose preflight records the user manager "(running|stopped)"$`, w.givenMachine)
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
