//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems.feature
// (DH-S-18 Stage 8.6: DH-E-FS-1, DH-E-FS-2, DH-E-VIEW-5; TC-21s3, TC-21s4).
//
// Each scenario runs the product's own scenario test, unmodified, built with
// -tags scenario for the variant and run there: this host when its OS matches,
// else the default WSL distribution through wsl.exe (a Windows host's Linux
// variant). The test's inputs are the operator's when all of them are set (the
// provided instance; DSH_S18_<LINUX|WINDOWS>_<NAME without DSH_> overrides one per
// variant), else an ephemeral instance this file stands up for the inputs it can
// give. No stand-in replaces a dependency: the model route is the variant's own
// signed-in claude (the s18 preflight: installed, claude auth status, one probe
// turn, on a run the tester declares theirs via DSH_S18_TESTER), and an input
// nothing provides fails its preflight, naming it. The verdict is the row the
// test writes to DSH_SCENARIO_REPORT, read back, plus the log lines that show
// each clause ran; each leg also keeps an evidence row (s86Evidence) with the
// verdict or failed preflight, the sandbox status and the fence under test.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
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
)

const (
	s86Pkg       = "./internal/testkit/scenarios"
	s86RunWithin = 24 * time.Hour
)

var s86OuterT *testing.T

// ---- variants ----

// s86Variant is a machine a scenario runs on.
type s86Variant struct {
	goos, goarch string
	wsl          bool // the Linux variant reached through wsl.exe from this Windows host
}

func (v *s86Variant) String() string {
	if v.wsl {
		return "linux (WSL)"
	}
	return v.goos
}

func s86Reach(goos string) (*s86Variant, error) {
	if runtime.GOOS == goos {
		return &s86Variant{goos: goos, goarch: runtime.GOARCH}, nil
	}
	if goos == "linux" && runtime.GOOS == "windows" {
		v := &s86Variant{goos: "linux", wsl: true}
		out, code, err := v.run(context.Background(), "", nil, nil, "uname", "-m")
		if err != nil || code != 0 {
			return nil, fmt.Errorf("the Linux variant is not reachable through wsl.exe: exit %d %v %s", code, err, out)
		}
		switch strings.TrimSpace(out) {
		case "x86_64":
			v.goarch = "amd64"
		case "aarch64":
			v.goarch = "arm64"
		default:
			return nil, fmt.Errorf("the Linux variant runs on %q, no release target", strings.TrimSpace(out))
		}
		return v, nil
	}
	return nil, fmt.Errorf("the %s variant is not reachable from this %s host", goos, runtime.GOOS)
}

// s86Env is this process's environment without the Go build variables, so a
// scenario test never takes them as a request for another dsh build.
func s86Env(set ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(strings.ToUpper(kv), "=")
		return slices.Contains([]string{"GOWORK", "GOFLAGS", "GOOS", "GOARCH", "CGO_ENABLED"}, k)
	})
	return append(env, set...)
}

// run runs argv on the variant in dir (a variant path; "" for the default) with
// env added and stdin, returning the combined output and the exit code.
func (v *s86Variant) run(ctx context.Context, dir string, env []string, stdin []byte, argv ...string) (string, int, error) {
	var cmd *exec.Cmd
	if v.wsl {
		args := []string{}
		if dir != "" {
			args = append(args, "--cd", dir)
		}
		args = append(append(append(args, "-e", "env"), env...), argv...)
		cmd = exec.CommandContext(ctx, "wsl.exe", args...)
		cmd.Env = s86Env()
	} else {
		cmd = exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = dir, s86Env(env...)
	}
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return string(out), exit.ExitCode(), nil
	}
	return string(out), 0, err
}

func (v *s86Variant) must(argv ...string) (string, error) {
	out, code, err := v.run(context.Background(), "", nil, nil, argv...)
	if err != nil || code != 0 {
		return out, fmt.Errorf("%v on %s: exit %d %v\n%s", argv, v, code, err, out)
	}
	return strings.TrimSpace(out), nil
}

// path is a host path as the variant names it.
func (v *s86Variant) path(p string) (string, error) {
	if !v.wsl {
		return p, nil
	}
	return v.must("wslpath", "-a", filepath.ToSlash(p))
}

func (v *s86Variant) exe(p string) string {
	if v.goos == "windows" {
		return p + ".exe"
	}
	return p
}

// tempDir is a scratch directory on the variant's own filesystem, removed at the end.
func (v *s86Variant) tempDir(prefix string) (string, error) {
	if !v.wsl {
		d, err := os.MkdirTemp("", prefix)
		if err == nil {
			s86OuterT.Cleanup(func() { _ = os.RemoveAll(d) })
		}
		return d, err
	}
	d, err := v.must("mktemp", "-d", "/tmp/"+prefix+"XXXXXX")
	if err == nil {
		s86OuterT.Cleanup(func() { _, _ = v.must("rm", "-rf", d) })
	}
	return d, err
}

func (v *s86Variant) join(dir string, elem ...string) string {
	if v.goos == "windows" {
		return filepath.Join(append([]string{dir}, elem...)...)
	}
	return strings.Join(append([]string{dir}, elem...), "/")
}

func (v *s86Variant) writeFile(p string, data []byte) error {
	if !v.wsl {
		return os.WriteFile(p, data, 0o600)
	}
	out, code, err := v.run(context.Background(), "", nil, data, "sh", "-c", `cat > "$1"`, "sh", p)
	if err != nil || code != 0 {
		return fmt.Errorf("writing %s on %s: exit %d %v %s", p, v, code, err, out)
	}
	return nil
}

func (v *s86Variant) readFile(p string) ([]byte, error) {
	if !v.wsl {
		return os.ReadFile(p)
	}
	out, err := v.must("cat", p)
	return []byte(out), err
}

func (v *s86Variant) sha256(p string) (string, error) {
	if !v.wsl {
		b, err := os.ReadFile(p)
		sum := sha256.Sum256(b)
		return hex.EncodeToString(sum[:]), err
	}
	out, err := v.must("sha256sum", p)
	if err != nil {
		return "", err
	}
	return strings.Fields(out)[0], nil
}

func (v *s86Variant) has(program string) bool {
	if !v.wsl {
		_, err := exec.LookPath(program)
		return err == nil
	}
	_, err := v.must("sh", "-c", `command -v "$1"`, "sh", program)
	return err == nil
}

// shell runs a command line as the scenario tests' shell helper does.
func (v *s86Variant) shell(line string) (string, error) {
	if v.goos == "windows" {
		return v.must("cmd", "/C", line)
	}
	return v.must("sh", "-c", line)
}

// fsType names the filesystem holding dir: "ext4" (the ext2/3/4 magic), "NTFS", ...
func (v *s86Variant) fsType(dir string) (string, error) {
	if v.goos == "windows" {
		out, err := v.must("powershell", "-NoProfile", "-Command",
			"[System.IO.DriveInfo]::new([System.IO.Path]::GetPathRoot('"+strings.ReplaceAll(dir, "'", "''")+"')).DriveFormat")
		return out, err
	}
	out, err := v.must("stat", "-f", "-c", "%T", dir)
	if out == "ext2/ext3" {
		out = "ext4"
	}
	return out, err
}

// ---- builds ----

var (
	s86BuildMu sync.Mutex
	s86Built   = map[string]string{}
)

func s86Module() (string, error) {
	dir, err := filepath.Abs(".")
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "cmd", "dsh")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("the harness module (cmd/dsh, go.mod) is not above the working directory")
		}
		dir = parent
	}
}

// build builds what (the scenario test binary or the testhook dsh) for v once per suite and returns its path as v names it.
func (v *s86Variant) build(what string) (string, error) {
	s86BuildMu.Lock()
	defer s86BuildMu.Unlock()
	key := v.goos + "/" + v.goarch + "/" + what
	if p, ok := s86Built[key]; ok {
		return p, nil
	}
	mod, err := s86Module()
	if err != nil {
		return "", err
	}
	out := v.exe(filepath.Join(s86OuterT.TempDir(), strings.ReplaceAll(key, "/", "-")))
	args := map[string][]string{
		"scenarios": {"test", "-c", "-trimpath", "-tags", "scenario", "-o", out, s86Pkg},
		"dsh":       {"build", "-trimpath", "-tags", "testhook", "-o", out, "./cmd/dsh"},
	}[what]
	cmd := exec.Command("go", args...)
	cmd.Dir, cmd.Env = mod, append(s86Env(), "GOWORK=off", "GOOS="+v.goos, "GOARCH="+v.goarch, "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("go %v for %s: %v\n%s", args, v, err, b)
	}
	p, err := v.path(out)
	if err == nil {
		s86Built[key] = p
	}
	return p, err
}

// ---- state ----

// s86Leg is one run of a scenario test on one variant.
type s86Leg struct {
	id       string
	v        *s86Variant
	fs       string
	provided bool              // every input is the operator's
	op       map[string]string // the inputs the operator set
	inputs   map[string]string // the run's DSH_* inputs
	claude   string            // the variant's own signed-in claude, as the variant names it
	tester   string            // DSH_S18_TESTER, who declared the signed-in run theirs
	state    string            // the head of "dsh sandbox status" on the variant
	backend  string
	status   string
	notRun   string // the preflight check that stopped the leg, if one did
	reason   string
	volumes  string // FS-1: the filesystem each checked directory and the share are on
	log      string
	logPath  string
	report   string
	row      map[string]any
	ran      error
	evidence string // the file holding the leg's persisted evidence row
}

type s86State struct {
	id, test string
	legs     []*s86Leg
}

// s86Inputs are what each scenario test reads (their doc comments), with DSH_BIN.
var s86Inputs = map[string][]string{
	"DH-E-FS-1":   {"DSH_BIN", "DSH_SCENARIO_FS1_DIRS", "DSH_SCENARIO_FS1_SHARE", "DSH_SCENARIO_FS1_LAYER", "DSH_SCENARIO_FS1_ROUTE", "DSH_SCENARIO_FS1_MODEL"},
	"DH-E-FS-2":   {"DSH_BIN", "DSH_SCENARIO_FS2_TASKS", "DSH_SCENARIO_FS_CHECKOUTS", "DSH_SCENARIO_FS2_CHILDREN", "DSH_SCENARIO_FS2_LAYER", "DSH_SCENARIO_FS2_SHARD", "DSH_SCENARIO_CLAUDE_CODE_MODEL", "DSH_SCENARIO_FS3_EDITOR"},
	"DH-E-VIEW-5": {"DSH_BIN", "DSH_SCENARIO_VIEW5_LAYER", "DSH_SCENARIO_VIEW5_MOVE", "DSH_SCENARIO_CLAUDE_CODE_MODEL"},
}

func s86Input(v *s86Variant, name string) string {
	if s := strings.TrimSpace(os.Getenv("DSH_S18_" + strings.ToUpper(v.goos) + "_" + strings.TrimPrefix(name, "DSH_"))); s != "" {
		return s
	}
	return strings.TrimSpace(os.Getenv(name))
}

// leg reaches the variant and takes every input the operator set; the
// ephemeral instance gives the others the values it can (set), and a preflight
// fails, naming it, for any input neither provides.
func (s *s86State) leg(goos, fs string) (*s86Leg, error) {
	v, err := s86Reach(goos)
	if err != nil {
		return nil, err
	}
	l := &s86Leg{id: s.id, v: v, fs: fs, op: map[string]string{}, inputs: map[string]string{}}
	for _, n := range s86Inputs[s.id] {
		if val := s86Input(v, n); val != "" {
			l.op[n] = val
		}
	}
	l.provided = len(l.op) == len(s86Inputs[s.id])
	maps.Copy(l.inputs, l.op)
	if l.inputs["DSH_BIN"] == "" {
		if l.inputs["DSH_BIN"], err = v.build("dsh"); err != nil {
			return nil, err
		}
	}
	s.legs = append(s.legs, l)
	s86OuterT.Logf("s86 %s on %s: %s", s.id, v, l.lane())
	return l, nil
}

func (l *s86Leg) lane() string {
	switch {
	case l.provided:
		return "provided instance"
	case len(l.op) == 0:
		return "ephemeral instance (testhook dsh)"
	}
	return "ephemeral instance (testhook dsh) with the operator's " + strings.Join(slices.Sorted(maps.Keys(l.op)), ", ")
}

// set gives an input its ephemeral value unless the operator set it.
func (l *s86Leg) set(name, value string) {
	if _, ok := l.op[name]; !ok {
		l.inputs[name] = value
	}
}

func (l *s86Leg) unset(names ...string) []string {
	return slices.DeleteFunc(slices.Clone(names), func(n string) bool { return l.inputs[n] != "" })
}

// preflight stops the leg before its run: its evidence row reads "not run
// (<check> preflight failed)" (e2e-scenarios.md, the FS-2 edge scenario).
func (l *s86Leg) preflight(check string, err error) error {
	l.notRun, l.reason = check, err.Error()
	return fmt.Errorf("%s on %s: not run (%s preflight failed): %w", l.id, l.v, check, err)
}

// scratch is a directory on the variant's filesystem under test.
func (l *s86Leg) scratch(prefix string) (string, error) {
	d, err := l.v.tempDir(prefix)
	if err != nil {
		return "", err
	}
	got, err := l.v.fsType(d)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(got, l.fs) {
		return "", fmt.Errorf("%s on %s is on %s, not %s", d, l.v, got, l.fs)
	}
	return d, nil
}

func s86Quote(s string) string { return strconv.Quote(s) }

// s86RouteItem is the testhook item registering the real claude-code route
// over client, the variant's own signed-in claude.
func s86RouteItem(client, temp string) string {
	return "    - id: route\n      name: llm.testkit-claude-code\n      config:\n        program: " + s86Quote(client) +
		"\n        tempDir: " + s86Quote(temp) + "\n"
}

// fileLayer is the configuration layer of the ephemeral instance: the six file
// tools over the fenced seam in workspace-write, with extra write roots, and the
// real claude-code route over the variant's signed-in claude when client is set.
func (l *s86Leg) fileLayer(client, clientTemp string, extra ...string) string {
	var b strings.Builder
	b.WriteString("- insert:\n")
	if client != "" {
		b.WriteString(s86RouteItem(client, clientTemp))
	}
	b.WriteString("    - id: fs.local\n      name: fs.local\n")
	b.WriteString("    - id: session.workdir\n      name: session.workdir\n")
	b.WriteString("    - id: sandbox.policy\n      name: sandbox.policy\n      config:\n        defaultMode: workspace-write\n")
	if len(extra) > 0 {
		b.WriteString("        extraWriteRoots: [" + strings.Join(extra, ", ") + "]\n")
	}
	b.WriteString("    - id: fs.ops\n      name: fs.ops\n    - id: fs.tools\n      name: fs.tools\n")
	return b.String()
}

// ---- provisioning ----

func (s *s86State) givenFS1Variants(fsLinux, fsWindows string) error {
	s.id, s.test = "DH-E-FS-1", "TestFenceHoldsUnderALinkSwap"
	var errs []error
	for _, want := range [][2]string{{"linux", fsLinux}, {"windows", fsWindows}} {
		l, err := s.leg(want[0], want[1])
		if err == nil {
			err = l.readStatus()
		}
		if err == nil {
			err = l.provisionFS1()
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// provisionFS1 gives FS-1 its directories: one on the variant's own filesystem
// and, on the Linux variant of a Windows host, one on this host's NTFS volume
// through drvfs; the share is an SMB mount of the variant (smbShare). The route
// is the operator's alone: no item of the dsh binary registers a route on an
// Anthropic Messages endpoint, the scripted endpoint's dialect
// (llm/wire/anthropic), so none is named in its place.
func (l *s86Leg) provisionFS1() error {
	var checks []string
	var why []error
	if l.inputs["DSH_SCENARIO_FS1_DIRS"] == "" {
		if err := l.ephemeralFS1(); err != nil {
			return err
		}
	}
	if l.inputs["DSH_SCENARIO_FS1_SHARE"] == "" {
		if share, err := l.smbShare(); err != nil {
			checks, why = append(checks, "share"), append(why, err)
		} else {
			l.set("DSH_SCENARIO_FS1_SHARE", share)
		}
	}
	if missing := l.unset("DSH_SCENARIO_FS1_LAYER", "DSH_SCENARIO_FS1_ROUTE", "DSH_SCENARIO_FS1_MODEL"); len(missing) > 0 {
		checks = append(checks, "route")
		why = append(why, fmt.Errorf("%s unset: no item of the dsh binary registers a route on an Anthropic Messages endpoint (the scripted endpoint's dialect, llm/wire/anthropic), so only the operator's layer, route and model name one (runbook-smb-share.md step 2)", strings.Join(missing, ", ")))
	}
	if fs := l.checkFS1(); len(fs) > 0 {
		checks, why = append([]string{"filesystem"}, checks...), append(fs, why...)
	}
	if len(checks) > 0 {
		return l.preflight(strings.Join(checks, ", "), errors.Join(why...))
	}
	s86OuterT.Logf("s86 %s on %s: filesystems checked: %s", l.id, l.v, l.volumes)
	return nil
}

// ephemeralFS1 makes the directories of the ephemeral instance.
func (l *s86Leg) ephemeralFS1() error {
	dir, err := l.scratch("dsh-s86-fs1-")
	if err != nil {
		return err
	}
	dirs := []string{dir}
	if l.v.wsl {
		nt, err := os.MkdirTemp("", "dsh-s86-fs1-ntfs-")
		if err != nil {
			return err
		}
		s86OuterT.Cleanup(func() { _ = os.RemoveAll(nt) })
		if got, err := (&s86Variant{goos: runtime.GOOS}).fsType(nt); err != nil || !strings.EqualFold(got, "NTFS") {
			return fmt.Errorf("%s is on %q, not NTFS (%v)", nt, got, err)
		}
		p, err := l.v.path(nt)
		if err != nil {
			return err
		}
		dirs = append(dirs, p)
	}
	l.set("DSH_SCENARIO_FS1_DIRS", strings.Join(dirs, ";"))
	return nil
}

// s86FS1Need are the filesystems FS-1's directories must cover on each variant:
// ext4 and NTFS volumes on the Linux variant, NTFS on the Windows host.
var s86FS1Need = map[string][]string{"linux": {"ext4", "NTFS"}, "windows": {"NTFS"}}

// checkFS1 proves what the run will use, whoever supplied it: every directory
// of DSH_SCENARIO_FS1_DIRS exists on the variant and is on a real filesystem,
// together they cover the variant's filesystems, and DSH_SCENARIO_FS1_SHARE is
// a writable directory on an SMB mount. Any gap fails the filesystem preflight
// before a call runs, so a directory on another filesystem is never reported
// as ext4 or NTFS.
func (l *s86Leg) checkFS1() []error {
	var why []error
	seen := map[string]bool{}
	var kinds []string
	for _, d := range strings.Split(l.inputs["DSH_SCENARIO_FS1_DIRS"], ";") {
		if d = strings.TrimSpace(d); d == "" {
			continue
		}
		kind, err := l.v.volumeKind(d)
		if err != nil {
			why = append(why, fmt.Errorf("DSH_SCENARIO_FS1_DIRS entry %s: %v", d, err))
			continue
		}
		seen[strings.ToLower(kind)] = true
		kinds = append(kinds, kind+" "+d)
	}
	for _, need := range s86FS1Need[l.v.goos] {
		if !seen[strings.ToLower(need)] {
			why = append(why, fmt.Errorf("no directory of DSH_SCENARIO_FS1_DIRS is on %s on the %s variant (got: %s)", need, l.v, strings.Join(kinds, "; ")))
		}
	}
	if share := l.inputs["DSH_SCENARIO_FS1_SHARE"]; share != "" {
		if err := l.v.checkSMB(share); err != nil {
			why = append(why, fmt.Errorf("DSH_SCENARIO_FS1_SHARE %s: %v", share, err))
		} else {
			kinds = append(kinds, "SMB "+share)
		}
	}
	l.volumes = strings.Join(kinds, "; ")
	return why
}

// volumeKind names the filesystem under an existing directory, as fsType
// does, but through a WSL drvfs mount (9p, v9fs) to the host volume behind it.
func (v *s86Variant) volumeKind(dir string) (string, error) {
	if v.goos == "windows" {
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return "", fmt.Errorf("not a directory (%v)", err)
		}
	} else if _, err := v.must("test", "-d", dir); err != nil {
		return "", errors.New("not a directory on the variant")
	}
	kind, err := v.fsType(dir)
	if err != nil {
		return "", err
	}
	switch strings.ToLower(kind) {
	case "ntfs", "ntfs3":
		return "NTFS", nil
	case "v9fs", "9p":
		if !v.wsl {
			return kind, nil
		}
		host, err := v.must("wslpath", "-w", dir)
		if err != nil {
			return "", err
		}
		return (&s86Variant{goos: runtime.GOOS}).fsType(host)
	}
	return kind, nil
}

// s86SMBProbe asks Windows how S86_SMB_PATH is reached: the drive type of its
// root (4 is DRIVE_REMOTE) and the network provider serving it
// (WNetGetResourceInformation; SMB is "Microsoft Windows Network").
const s86SMBProbe = `Add-Type -TypeDefinition @"
using System; using System.Runtime.InteropServices;
public static class S86Smb {
  [StructLayout(LayoutKind.Sequential, CharSet=CharSet.Unicode)] public class NR { public int Scope, Type, DisplayType, Usage; public string Local, Remote, Comment, Provider; }
  [DllImport("mpr.dll", CharSet=CharSet.Unicode)] static extern int WNetGetResourceInformationW(NR r, IntPtr buf, ref int size, out IntPtr sys);
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode)] public static extern uint GetDriveTypeW(string root);
  public static string Provider(string path) {
    var r = new NR(); r.Remote = path; int size = 8192; IntPtr buf = Marshal.AllocHGlobal(size); IntPtr sys;
    try { int rc = WNetGetResourceInformationW(r, buf, ref size, out sys); if (rc != 0) return "error " + rc; return ((NR)Marshal.PtrToStructure(buf, typeof(NR))).Provider; }
    finally { Marshal.FreeHGlobal(buf); }
  }
}
"@
$p = $env:S86_SMB_PATH; $root = [System.IO.Path]::GetPathRoot($p); if (-not $root.EndsWith('\')) { $root += '\' }
"drive=" + [S86Smb]::GetDriveTypeW($root)
"provider=" + [S86Smb]::Provider($p)`

// s86UNC matches \\server\share[\...], the only form an SMB path takes; the
// device and object namespaces (\\?\, \\.\, \??\) are local paths in disguise.
var s86UNC = regexp.MustCompile(`^\\\\([^\\/?.*:][^\\/?*:]*)\\([^\\/?*:]+)(\\.*)?$`)

// checkSMB requires share to be a writable directory reached over SMB: on
// Windows a \\server\share path that the SMB client (the "Microsoft Windows
// Network" provider) serves from a remote drive; on Linux a cifs/smb mount.
func (v *s86Variant) checkSMB(share string) error {
	if v.goos == "windows" {
		if !s86UNC.MatchString(share) {
			return errors.New(`not a UNC path \\server\share\... (device and object namespaces such as \\?\ and \\.\ are local)`)
		}
		if st, err := os.Stat(share); err != nil || !st.IsDir() {
			return fmt.Errorf("not a reachable directory (%v)", err)
		}
		out, code, err := v.run(context.Background(), "", []string{"S86_SMB_PATH=" + share}, nil, "powershell", "-NoProfile", "-NonInteractive", "-Command", s86SMBProbe)
		if err != nil || code != 0 {
			return fmt.Errorf("the SMB transport probe failed: exit %d %v\n%s", code, err, out)
		}
		if !strings.Contains(out, "drive=4") || !strings.Contains(out, "provider=Microsoft Windows Network") {
			return fmt.Errorf("not served over SMB (want remote drive type 4 and provider Microsoft Windows Network): %s", strings.Join(strings.Fields(out), " "))
		}
		f, err := os.CreateTemp(share, "dsh-s86-smb-")
		if err != nil {
			return fmt.Errorf("not writable: %v", err)
		}
		_ = f.Close()
		return os.Remove(f.Name())
	}
	kind, err := v.fsType(share)
	if err != nil {
		return err
	}
	if k := strings.ToLower(kind); k != "cifs" && k != "smb2" && k != "smb3" && k != "smb" {
		return fmt.Errorf("on %s, not an SMB (cifs, smb2, smb3) mount", kind)
	}
	if _, err := v.must("sh", "-c", `p=$(mktemp "$1/dsh-s86-smb-XXXXXX") && rm -f "$p"`, "sh", share); err != nil {
		return fmt.Errorf("not writable: %v", err)
	}
	return nil
}

// smbShare is a scratch directory on an SMB share of the variant: on Windows
// this host's loopback share (\\localhost\<drive>$, the SMB client over the
// NTFS volume); on Linux the first writable cifs or smb3 mount in /proc/mounts.
// A missing share is never a pass (runbook-smb-share.md).
func (l *s86Leg) smbShare() (string, error) {
	if l.v.goos == "windows" {
		share, err := l.scratch("dsh-s86-fs1-share-")
		if err != nil {
			return "", err
		}
		unc := `\\localhost\` + share[:1] + `$` + share[2:]
		if _, err := os.Stat(unc); err != nil {
			return "", fmt.Errorf("the loopback SMB share %s is not reachable: %v", unc, err)
		}
		return unc, nil
	}
	mounts, err := l.v.readFile("/proc/mounts")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		if f := strings.Fields(line); len(f) >= 3 && (f[2] == "cifs" || f[2] == "smb3") {
			if d, err := l.v.must("mktemp", "-d", f[1]+"/dsh-s86-fs1-XXXXXX"); err == nil {
				s86OuterT.Cleanup(func() { _, _ = l.v.must("rm", "-rf", d) })
				return d, nil
			}
		}
	}
	return "", fmt.Errorf("the %s variant has no writable SMB (cifs, smb3) mount: mount the team share per runbook-smb-share.md step 1, or set DSH_S18_LINUX_SCENARIO_FS1_SHARE", l.v)
}

func s86Children(goos string) string {
	if goos == "windows" {
		// No double quotes (Go escapes them as \" for "cmd /C", which cmd does not
		// read) and no pipes (cmd would split the line): .Where/.ForEach instead.
		return `powershell -NoProfile -Command $all=Get-CimInstance Win32_Process; $k=@({pid}); while($k.Count){ $c=@($all.Where({$k -contains $_.ParentProcessId})); $c.ForEach({$_.Name}); $k=@($c.ForEach({$_.ProcessId})) }`
	}
	return `ps -e -o pid= -o ppid= -o comm= | awk -v r={pid} 'BEGIN{k[r]=1} {p[NR]=$1; q[NR]=$2; c[NR]=$3} END{do{n=0; for(i=1;i<=NR;i++) if(k[q[i]] && !k[p[i]]){k[p[i]]=1; print c[i]; n=1}} while(n)}'`
}

func s86Editor(goos string) string {
	if goos == "windows" {
		// No double quotes: Go escapes them as \" for "cmd /C", which cmd does not read.
		return `powershell -NoProfile -Command Set-Content -NoNewline -LiteralPath '{file}.dsh-save' -Value saved; Move-Item -Force -LiteralPath '{file}.dsh-save' -Destination '{file}'`
	}
	return `printf saved > '{file}.dsh-save' && mv -f '{file}.dsh-save' '{file}'`
}

// children checks the FS-2 descendant-process template on the variant: run over
// a process with a known child, it must print that child's name, so the
// scenario's "no rg child process" clause cannot pass on output that names nothing.
func (l *s86Leg) children() error {
	tmpl := l.inputs["DSH_SCENARIO_FS2_CHILDREN"]
	if tmpl == "" {
		tmpl = s86Children(l.v.goos)
	}
	var out, want string
	var err error
	if l.v.goos == "windows" {
		want = "ping.exe"
		parent := exec.Command("cmd", "/C", "ping -n 30 127.0.0.1")
		if err := parent.Start(); err != nil {
			return err
		}
		defer func() { _ = parent.Process.Kill(); _ = parent.Wait() }()
		deadline := time.Now().Add(15 * time.Second)
		for {
			out, err = l.v.shell(strings.ReplaceAll(tmpl, "{pid}", strconv.Itoa(parent.Process.Pid)))
			if err != nil || strings.Contains(strings.ToLower(out), want) || time.Now().After(deadline) {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
	} else {
		want = "sleep"
		out, err = l.v.must("sh", "-c", "sleep 30 & sleep 1; "+strings.ReplaceAll(tmpl, "{pid}", "$$")+"; kill $! 2>/dev/null; wait")
	}
	if err != nil {
		return fmt.Errorf("service:process-listing: the DSH_SCENARIO_FS2_CHILDREN template failed on the %s variant: %v %s", l.v, err, out)
	}
	for _, name := range strings.Fields(strings.ToLower(out)) {
		if name == want {
			return nil
		}
	}
	return fmt.Errorf("service:process-listing: the DSH_SCENARIO_FS2_CHILDREN template on the %s variant did not name the known child %s: %q", l.v, want, out)
}

// gitAndEditor checks git (its version recorded) and the editor save script on
// the variant: the script replaces a probe file by rename.
func (l *s86Leg) gitAndEditor(dir string) error {
	if !l.v.has("git") {
		return fmt.Errorf("service:git-and-editor: git is not on the PATH of the %s variant", l.v)
	}
	ver, err := l.v.must("git", "--version")
	if err != nil {
		return err
	}
	s86OuterT.Logf("s86 %s: %s", l.v, ver)
	editor := l.inputs["DSH_SCENARIO_FS3_EDITOR"]
	if editor == "" {
		editor = s86Editor(l.v.goos)
	}
	probe := l.v.join(dir, "editor-probe.txt")
	if err := l.v.writeFile(probe, []byte("before")); err != nil {
		return err
	}
	if out, err := l.v.shell(strings.ReplaceAll(editor, "{file}", probe)); err != nil {
		return fmt.Errorf("service:git-and-editor: the editor save script failed: %v %s", err, out)
	}
	if b, err := l.v.readFile(probe); err != nil || strings.TrimSpace(string(b)) == "before" {
		return fmt.Errorf("service:git-and-editor: the editor save script left %s as %q (%v)", probe, b, err)
	}
	return nil
}

// signedIn is the s18 job's signed-in route preflight on the variant (M2-EN-8):
// the variant's own claude, installed there (a Windows client reached through
// WSL interop does not count), signed in (claude auth status) and answering one
// probe turn through dsh (TestSignedInRoutePreflight). No stand-in takes its
// place. The probe and the run spend prompts of the tester's subscription, so
// they run only on a run the tester declares theirs (DSH_S18_TESTER, recorded
// in the row): never an agent session (runbook-smb-share.md, Who).
func (l *s86Leg) signedIn(dir string) error {
	var path string
	var err error
	if l.v.wsl {
		path, err = l.v.must("sh", "-c", "command -v claude")
		if err == nil && strings.HasPrefix(path, "/mnt/") {
			err = fmt.Errorf("%s is this Windows host's client through interop, not one installed on the Linux variant", path)
		}
	} else if path, err = exec.LookPath("claude"); err == nil {
		path, err = filepath.Abs(path)
	}
	if err != nil {
		return l.preflight("signed-in", fmt.Errorf("service:model-route-claude-code: claude is not installed on the %s variant: %v", l.v, err))
	}
	ver, err := l.v.must(path, "--version")
	if err != nil {
		return l.preflight("signed-in", err)
	}
	out, err := l.v.must(path, "auth", "status")
	var auth struct {
		LoggedIn   bool   `json:"loggedIn"`
		AuthMethod string `json:"authMethod"`
	}
	if err != nil || json.Unmarshal([]byte(out), &auth) != nil || !auth.LoggedIn {
		return l.preflight("signed-in", fmt.Errorf("service:model-route-claude-code: claude %s on %s is not signed in (claude auth status: %v %s)", ver, l.v, err, out))
	}
	l.claude = path
	s86OuterT.Logf("s86 %s: claude %s at %s, signed in (%s)", l.v, ver, path, auth.AuthMethod)
	if l.tester = strings.TrimSpace(os.Getenv("DSH_S18_TESTER")); l.tester == "" {
		return l.preflight("signed-in", errors.New("DSH_S18_TESTER is not set: the probe turn and the run spend the tester's signed-in subscription, so only a run the tester declares theirs drives them (runbook-smb-share.md, Who: never an agent session)"))
	}
	return l.probe(dir)
}

// probe runs the product's probe turn, TestSignedInRoutePreflight, on route
// claude-code over the variant's claude (or the operator's preflight layer).
func (l *s86Leg) probe(dir string) error {
	bin, err := l.v.build("scenarios")
	if err != nil {
		return err
	}
	pkg, err := l.pkgDir()
	if err != nil {
		return err
	}
	layer := s86Input(l.v, "DSH_SCENARIO_PREFLIGHT_LAYER")
	if layer == "" {
		layer = "- insert:\n" + s86RouteItem(l.claude, l.v.join(dir, "probe-client"))
	}
	env := []string{"DSH_BIN=" + l.inputs["DSH_BIN"], "DSH_SCENARIO_ROUTES=claude-code", "DSH_SCENARIO_PREFLIGHT_LAYER=" + layer,
		"DSH_SCENARIO_PREFLIGHT_PROMPT=Reply with the word ready.", "DSH_SCENARIO_CLAUDE_CODE_MODEL=" + l.model()}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	out, code, err := l.v.run(ctx, pkg, env, nil, bin, "-test.run", "^TestSignedInRoutePreflight$", "-test.v", "-test.count=1", "-test.timeout", "10m")
	if err != nil || code != 0 {
		return l.preflight("signed-in", fmt.Errorf("the probe turn on claude-code failed: exit %d %v\n%s", code, err, out[max(0, len(out)-2000):]))
	}
	s86OuterT.Logf("s86 %s: the probe turn on claude-code passed (tester %s)", l.v, l.tester)
	return nil
}

// model is the claude-code model of the run: the operator's, else the client's
// first alias.
func (l *s86Leg) model() string {
	if m := l.inputs["DSH_SCENARIO_CLAUDE_CODE_MODEL"]; m != "" {
		return m
	}
	return "sonnet"
}

func (s *s86State) givenFS2(goos, fs string) error {
	s.id, s.test = "DH-E-FS-2", "TestLongSessionCorrectsItsOwnPatterns"
	l, err := s.leg(goos, fs)
	if err != nil {
		return err
	}
	if err := l.readStatus(); err != nil {
		return err
	}
	dir, err := l.scratch("dsh-s86-fs2-")
	if err != nil {
		return err
	}
	if err := l.gitAndEditor(dir); err != nil {
		return l.preflight("git-and-editor", err)
	}
	if err := l.children(); err != nil {
		return l.preflight("process-listing", err)
	}
	if err := l.signedIn(dir); err != nil || l.provided {
		return err
	}
	var checkouts []string
	for i := range 2 {
		co := l.v.join(dir, fmt.Sprintf("repo%d", i))
		if _, err := l.v.must("git", "init", "--quiet", co); err != nil {
			return err
		}
		for name, text := range map[string]string{"notes.txt": "alpha\nbeta\ngamma\n", "extra.txt": "one\ntwo\n"} {
			if err := l.v.writeFile(l.v.join(co, name), []byte(text)); err != nil {
				return err
			}
		}
		for _, argv := range [][]string{{"git", "-C", co, "add", "."}, {"git", "-C", co, "-c", "user.name=dsh", "-c", "user.email=dsh@example.invalid", "commit", "--quiet", "-m", "seed"}} {
			if _, err := l.v.must(argv...); err != nil {
				return err
			}
		}
		checkouts = append(checkouts, co)
	}
	var tasks strings.Builder
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&tasks, "FS-2 task %02d: in {checkout}, search notes.txt for beta, read line 2 of notes.txt, replace beta with BETA in notes.txt, then patch notes.txt and extra.txt in one patch.\n", i)
	}
	tasksFile := l.v.join(dir, "fs2-tasks.txt")
	if err := l.v.writeFile(tasksFile, []byte(tasks.String())); err != nil {
		return err
	}
	l.set("DSH_SCENARIO_FS2_TASKS", tasksFile)
	l.set("DSH_SCENARIO_FS_CHECKOUTS", strings.Join(checkouts, ";"))
	l.set("DSH_SCENARIO_FS2_CHILDREN", s86Children(l.v.goos))
	l.set("DSH_SCENARIO_FS2_LAYER", l.fileLayer(l.claude, l.v.join(dir, "client")))
	l.set("DSH_SCENARIO_FS2_SHARD", "all")
	l.set("DSH_SCENARIO_CLAUDE_CODE_MODEL", l.model())
	l.set("DSH_SCENARIO_FS3_EDITOR", s86Editor(l.v.goos))
	return nil
}

func (s *s86State) givenFS2Linux(fs string) error   { return s.givenFS2("linux", fs) }
func (s *s86State) givenFS2Windows(fs string) error { return s.givenFS2("windows", fs) }

func (s *s86State) givenView5() error {
	s.id, s.test = "DH-E-VIEW-5", "TestHostMoveIntoARealWorktree"
	l, err := s.leg("linux", "ext4")
	if err != nil {
		return err
	}
	if err := l.readStatus(); err != nil {
		return err
	}
	if !l.v.has("bwrap") {
		return l.preflight("bwrap", fmt.Errorf("service:linux-sandbox-bwrap: bwrap is not on the PATH of the %s variant", l.v))
	}
	dir, err := l.scratch("dsh-s86-view5-")
	if err != nil {
		return err
	}
	if err := l.gitAndEditor(dir); err != nil {
		return l.preflight("git-and-editor", err)
	}
	if err := l.signedIn(dir); err != nil {
		return err
	}
	l.set("DSH_SCENARIO_VIEW5_LAYER", l.fileLayer(l.claude, l.v.join(dir, "client")))
	l.set("DSH_SCENARIO_CLAUDE_CODE_MODEL", l.model())
	if l.inputs["DSH_SCENARIO_VIEW5_MOVE"] == "" {
		return l.preflight("host-move", errors.New("DSH_SCENARIO_VIEW5_MOVE is not set: no host operation of the product moves a session through session/query yet, so only the operator's command does (evidence/DH-S-18.md, Stage 8.6 blockers)"))
	}
	return nil
}

// readStatus runs "dsh sandbox status" on the variant (a fresh home; the CLI
// takes no --layer for it) and keeps the head of its report for the row.
func (l *s86Leg) readStatus() error {
	dir, err := l.v.tempDir("dsh-s86-status-")
	if err != nil {
		return err
	}
	home := l.v.join(dir, "home")
	out, _, err := l.v.run(context.Background(), "", []string{"DSH_HOME=" + home}, nil, l.inputs["DSH_BIN"], "--home", home, "sandbox", "status")
	if err != nil {
		return err
	}
	head := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if k, val, ok := strings.Cut(strings.TrimSpace(line), ":"); ok {
			if _, seen := head[k]; !seen {
				head[k] = strings.TrimSpace(val)
			}
		}
	}
	if head["state"] == "" {
		return fmt.Errorf("dsh sandbox status on %s names no state:\n%s", l.v, out)
	}
	l.state, l.backend, l.status = head["state"], head["backend"], out
	s86OuterT.Logf("s86 %s on %s: dsh sandbox status reads state %s, backend %s; fence under test: %s", l.id, l.v, l.state, l.backend, l.fence())
	return nil
}

// fence is the fence under test the row records: the C32 filesystem seam when
// no backend confines (Windows in M2, tech-spec sec. 4.2 row 35), else the
// backend and its state.
func (l *s86Leg) fence() string {
	if l.state == "Unconfined" {
		return "C32 filesystem seam"
	}
	return strings.TrimSpace(l.backend + " " + l.state)
}

// sandboxStatus requires the status read on the leg's variant to read state
// (and backend when set).
func (s *s86State) sandboxStatus(backend, state string) error {
	l := s.legs[len(s.legs)-1]
	if l.state != state || (backend != "" && l.backend != backend) {
		return fmt.Errorf("dsh sandbox status on %s reads state %q backend %q, want state %q%s:\n%s", l.v, l.state, l.backend, state,
			map[bool]string{true: " backend " + strconv.Quote(backend)}[backend != ""], l.status)
	}
	return nil
}

func (s *s86State) givenUnconfined(state string) error       { return s.sandboxStatus("", state) }
func (s *s86State) givenBackend(backend, state string) error { return s.sandboxStatus(backend, state) }

// ---- the run ----

// run runs the scenario test on each leg; every leg runs even when one fails.
func (s *s86State) run() error {
	var failed []string
	for _, l := range s.legs {
		if l.ran = s.runLeg(l); l.ran != nil {
			failed = append(failed, l.ran.Error())
		}
		if err := l.persist(); err != nil {
			failed = append(failed, err.Error())
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "\n\n"))
	}
	return nil
}

// pkgDir is the scenario package's source directory as the variant names it
// (scenario.Main runs there).
func (l *s86Leg) pkgDir() (string, error) {
	mod, err := s86Module()
	if err != nil {
		return "", err
	}
	return l.v.path(filepath.Join(mod, filepath.FromSlash(strings.TrimPrefix(s86Pkg, "./"))))
}

func (s *s86State) runLeg(l *s86Leg) error {
	bin, err := l.v.build("scenarios")
	if err != nil {
		return err
	}
	pkg, err := l.pkgDir()
	if err != nil {
		return err
	}
	dir, err := l.v.tempDir("dsh-s86-report-")
	if err != nil {
		return err
	}
	report := l.v.join(dir, "report.jsonl")
	l.report = report
	env := []string{"DSH_SCENARIO_REPORT=" + report, "DSH_REQUIRE_SCENARIOS=" + s.id}
	for _, k := range slices.Sorted(maps.Keys(l.inputs)) {
		env = append(env, k+"="+l.inputs[k])
	}
	ctx, cancel := context.WithTimeout(context.Background(), s86RunWithin)
	defer cancel()
	out, code, err := l.v.run(ctx, pkg, env, nil, bin, "-test.run", "^"+s.test+"$", "-test.v", "-test.count=1", "-test.timeout", s86RunWithin.String())
	if err != nil {
		return err
	}
	f, ferr := os.CreateTemp("", "dsh-s86-"+strings.ToLower(s.id)+"-"+l.v.goos+"-*.log")
	if ferr == nil {
		_, _ = f.WriteString(out)
		_ = f.Close()
		l.logPath = f.Name()
		s86OuterT.Logf("s86 %s on %s: exit %d, log kept at %s", s.id, l.v, code, f.Name())
	}
	l.log = out
	rows, rerr := l.v.readFile(report)
	sc := bufio.NewScanner(bytes.NewReader(rows))
	for sc.Scan() {
		var row map[string]any
		if json.Unmarshal(sc.Bytes(), &row) == nil && row["id"] == s.id {
			l.row = row
		}
	}
	if code != 0 || l.row == nil || l.row["result"] != "passed" {
		tail := out[max(0, len(out)-3000):]
		return fmt.Errorf("%s (%s) on %s, %s: exit %d, row %v (report %v)%s\n...\n%s", s.test, s.id, l.v,
			map[bool]string{true: "provided instance", false: "ephemeral instance"}[l.provided], code, l.row, rerr, s.diagnose(l), tail)
	}
	return nil
}

// diagnose names the known causes a failed run shows.
func (s *s86State) diagnose(l *s86Leg) string {
	var why []string
	for _, m := range regexp.MustCompile(`(DSH_[A-Z0-9_]+) is not set: ([^\r\n"]*)\r?\n`).FindAllStringSubmatch(l.log, -1) {
		why = append(why, m[1]+" is not set ("+m[2]+"): no product part provides it on this variant; the operator supplies it (evidence/DH-S-18.md, Stage 8.6 blockers)")
	}
	if strings.Contains(l.log, `--layer applies to a profile launch, not to "sandbox"`) {
		why = append(why, `the scenario test runs "dsh --layer L sandbox status", which the CLI refuses (--layer is launch-only)`)
	}
	if strings.Contains(l.log, "this host serves no permission presets") {
		why = append(why, `session/create with permissionMode "workspace-write" is refused (permission/unavailable): no item the dsh binary boots provides permission.presets (evidence/DH-S-14.md step 14.5)`)
	}
	if strings.Contains(l.log, "llm/sign-in-required") {
		why = append(why, "the claude-code client is signed out (llm/sign-in-required)")
	}
	if len(why) == 0 {
		return ""
	}
	return ":\n  - " + strings.Join(why, "\n  - ")
}

// ---- evidence ----

// s86RunID tells this suite run's evidence rows from earlier ones in the same file.
var s86RunID string

// s86Evidence is the row the suite keeps for each leg past the run, beside the
// scenario test's own (DSH_SCENARIO_REPORT, which has no field for them): the
// verdict, or the preflight that stopped the leg, and the sandbox status the
// run had with the fence under test.
type s86Evidence struct {
	Kind           string `json:"kind"`
	Run            string `json:"run"`
	ID             string `json:"id"`
	OS             string `json:"os"`
	Variant        string `json:"variant"`
	FS             string `json:"fs"`
	Lane           string `json:"lane"`
	Result         string `json:"result"`
	Reason         string `json:"reason,omitempty"`
	Build          string `json:"build,omitempty"`
	DurationMS     any    `json:"durationMs,omitempty"`
	SandboxState   string `json:"sandboxState,omitempty"`
	SandboxBackend string `json:"sandboxBackend,omitempty"`
	Fence          string `json:"fence,omitempty"`
	Volumes        string `json:"volumes,omitempty"`
	Claude         string `json:"claude,omitempty"`
	Tester         string `json:"tester,omitempty"`
	Report         string `json:"report,omitempty"`
	Log            string `json:"log,omitempty"`
	At             string `json:"at"`
}

// s86EvidenceDir holds the rows: DSH_S18_EVIDENCE_DIR, else a directory under
// this host's temporary directory that the suite never removes.
func s86EvidenceDir() string {
	if d := strings.TrimSpace(os.Getenv("DSH_S18_EVIDENCE_DIR")); d != "" {
		return d
	}
	return filepath.Join(os.TempDir(), "dsh-s18-evidence")
}

// persist appends the leg's row to <dir>/<id>-<os>.jsonl, once.
func (l *s86Leg) persist() error {
	if l.evidence != "" {
		return nil
	}
	e := s86Evidence{Kind: "s18-leg", Run: s86RunID, ID: l.id, OS: l.v.goos, Variant: l.v.String(), FS: l.fs, Lane: l.lane(),
		SandboxState: l.state, SandboxBackend: l.backend, Volumes: l.volumes, Claude: l.claude, Tester: l.tester, Report: l.report, Log: l.logPath,
		At: time.Now().UTC().Format(time.RFC3339)}
	if l.state != "" {
		e.Fence = l.fence()
	}
	switch {
	case l.notRun != "":
		e.Result, e.Reason = "not run ("+l.notRun+" preflight failed)", l.reason
	case l.row != nil:
		e.Result, _ = l.row["result"].(string)
		e.Reason, _ = l.row["reason"].(string)
		e.Build, _ = l.row["build"].(string)
		e.DurationMS = l.row["durationMs"]
	case l.ran != nil:
		e.Result, e.Reason = "failed", l.ran.Error()
	default:
		e.Result, e.Reason = "not run", l.reason
	}
	dir := s86EvidenceDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(dir, strings.ToLower(l.id)+"-"+l.v.goos+".jsonl")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(e)
	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	l.evidence = p
	s86OuterT.Logf("s86 %s on %s: evidence row %q kept in %s", l.id, l.v, e.Result, p)
	return nil
}

// thenEvidence reads each leg's persisted row back and requires it to record
// the sandbox state and the fence under test.
func (s *s86State) thenEvidence(state, fence string) error {
	for _, l := range s.legs {
		if l.evidence == "" {
			return fmt.Errorf("%s on %s: no evidence row was kept", s.id, l.v)
		}
		b, err := os.ReadFile(l.evidence)
		if err != nil {
			return err
		}
		var got *s86Evidence
		for _, line := range bytes.Split(b, []byte("\n")) {
			var e s86Evidence
			if json.Unmarshal(line, &e) == nil && e.Run == s86RunID && e.ID == s.id && e.OS == l.v.goos {
				got = &e
			}
		}
		if got == nil {
			return fmt.Errorf("%s on %s: %s holds no row of run %s", s.id, l.v, l.evidence, s86RunID)
		}
		if got.SandboxState != state || got.Fence != fence {
			return fmt.Errorf("%s on %s: the evidence row in %s records sandbox state %q and fence %q, want %q and %q", s.id, l.v, l.evidence, got.SandboxState, got.Fence, state, fence)
		}
	}
	return nil
}

// ---- Then ----

func (s *s86State) thenRowPassed() error {
	for _, l := range s.legs {
		if l.ran != nil || l.row == nil {
			return fmt.Errorf("%s on %s did not run to a row: %v", s.id, l.v, l.ran)
		}
		if l.row["result"] != "passed" || l.row["os"] != l.v.goos {
			return fmt.Errorf("%s on %s: row %v, want passed on %s", s.id, l.v, l.row, l.v.goos)
		}
		sum, err := l.v.sha256(l.inputs["DSH_BIN"])
		if err != nil {
			return err
		}
		if l.row["build"] != sum {
			return fmt.Errorf("%s on %s: row build %v is not the SHA-256 %s of the dsh it ran", s.id, l.v, l.row["build"], sum)
		}
	}
	return nil
}

func (s *s86State) logs(check func(l *s86Leg) error) error {
	for _, l := range s.legs {
		if l.ran != nil {
			return fmt.Errorf("%s on %s did not pass: %v", s.id, l.v, l.ran)
		}
		if err := check(l); err != nil {
			return fmt.Errorf("%s on %s: %w", s.id, l.v, err)
		}
	}
	return nil
}

var s86Swaps = regexp.MustCompile(`the helper swapped W/sub (\d+) times using a (.+)`)

func (s *s86State) thenSwapped() error {
	return s.logs(func(l *s86Leg) error {
		dirs := len(strings.Split(l.inputs["DSH_SCENARIO_FS1_DIRS"], ";")) + 1
		ms := s86Swaps.FindAllStringSubmatch(l.log, -1)
		if len(ms) != dirs {
			return fmt.Errorf("%d swap reports for %d filesystems", len(ms), dirs)
		}
		for _, m := range ms {
			if n, _ := strconv.Atoi(m[1]); n == 0 {
				return fmt.Errorf("the helper never swapped the link (%s)", m[2])
			}
		}
		return nil
	})
}

func (s *s86State) thenOutsideUnchanged() error {
	return s.logs(func(l *s86Leg) error {
		dirs := len(strings.Split(l.inputs["DSH_SCENARIO_FS1_DIRS"], ";")) + 1
		for i := 1; i <= dirs; i++ {
			if !strings.Contains(l.log, fmt.Sprintf("--- PASS: %s/fs%d ", s.test, i)) {
				return fmt.Errorf("the listing of O on filesystem %d was not proved unchanged (no passing fs%d leg)", i, i)
			}
		}
		if strings.Contains(l.log, "files outside every root changed") || strings.Contains(l.log, "an outside file changed") {
			return errors.New("the hash listing of the outside directory changed")
		}
		return nil
	})
}

func (s *s86State) thenTasksPassed() error {
	return s.logs(func(l *s86Leg) error {
		from, to := 1, 20
		if sh := l.inputs["DSH_SCENARIO_FS2_SHARD"]; sh != "all" {
			if _, err := fmt.Sscanf(sh, "%d-%d", &from, &to); err != nil {
				return fmt.Errorf("shard %q: %v", sh, err)
			}
		}
		for i := from; i <= to; i++ {
			if !strings.Contains(l.log, fmt.Sprintf("--- PASS: %s/task%02d ", s.test, i)) {
				return fmt.Errorf("task %02d of the shard did not pass", i)
			}
		}
		return nil
	})
}

// thenNoOutside: the scenario test lists each task's parent directory before
// and after and judges every write; a pass with no outside-change line is that
// clause holding.
func (s *s86State) thenNoOutside() error {
	return s.logs(func(l *s86Leg) error {
		for _, bad := range []string{"outside the write root changed", "files outside the working directory", "outside the working directory of its run"} {
			if strings.Contains(l.log, bad) {
				return fmt.Errorf("the log reports %q", bad)
			}
		}
		if !strings.Contains(l.log, "--- PASS: "+s.test+" ") {
			return errors.New("the scenario test did not pass")
		}
		return nil
	})
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems(ctx *godog.ScenarioContext) {
	var s *s86State
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &s86State{}
		return c, nil
	})
	// A leg stopped before its run keeps its evidence row too.
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for _, l := range s.legs {
			if err != nil && l.notRun == "" && l.row == nil && l.ran == nil && l.reason == "" {
				l.reason = err.Error()
			}
			if perr := l.persist(); perr != nil {
				return c, perr
			}
		}
		return c, nil
	})
	ctx.Step(`^the variant's evidence row records sandbox state "([^"]*)" and the fence under test "([^"]*)"$`, func(st, fence string) error { return s.thenEvidence(st, fence) })
	ctx.Step(`^the "([^"]*)" variant on Linux and the "([^"]*)" variant on Windows$`, func(a, b string) error { return s.givenFS1Variants(a, b) })
	ctx.Step(`^10,000 tool calls run under the link-swap helper on each variant$`, func() error { return s.run() })
	ctx.Step(`^each variant's DH-E-FS-1 row reads passed for the binary it ran$`, func() error { return s.thenRowPassed() })
	ctx.Step(`^on each variant the helper swapped the link while the calls ran$`, func() error { return s.thenSwapped() })
	ctx.Step(`^on each variant the hash listing of the outside directory is unchanged before and after$`, func() error { return s.thenOutsideUnchanged() })

	ctx.Step(`^the tester's Linux machine on "([^"]*)" with the signed-in Claude Code route, git and an editor save script$`, func(fs string) error { return s.givenFS2Linux(fs) })
	ctx.Step(`^the Windows variant machine on "([^"]*)" with the signed-in Claude Code CLI installed natively, git and an editor save script$`, func(fs string) error { return s.givenFS2Windows(fs) })
	ctx.Step(`^"dsh sandbox status" on the variant reads state "([^"]*)", recorded in the row$`, func(st string) error { return s.givenUnconfined(st) })
	ctx.Step(`^the long-running FS-2 run completes on the variant$`, func() error { return s.run() })
	ctx.Step(`^the long-running FS-2 run completes on the variant under mode "workspace-write"$`, func() error { return s.run() })
	ctx.Step(`^the variant's (DH-E-[A-Z0-9-]+) row reads passed for the binary it ran$`, func(string) error { return s.thenRowPassed() })
	ctx.Step(`^every task of the run's shard passed$`, func() error { return s.thenTasksPassed() })
	ctx.Step(`^every model-chosen write through the six file tools lands inside the session's write roots, with zero files outside them in the before and after listings$`, func() error { return s.thenNoOutside() })
	ctx.Step(`^no file appears outside the write roots in the before and after listings$`, func() error { return s.thenNoOutside() })

	ctx.Step(`^the tester's Linux machine with the signed-in route, a real git repository and bwrap$`, func() error { return s.givenView5() })
	ctx.Step(`^"dsh sandbox status" on the variant reads backend "([^"]*)" state "([^"]*)"$`, func(b, st string) error { return s.givenBackend(b, st) })
	ctx.Step(`^VIEW-5 runs on the variant under "workspace-write"$`, func() error { return s.run() })
	ctx.Step(`^every model-chosen write lands inside the session's working directory$`, func() error { return s.thenNoOutside() })
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems(t *testing.T) {
	s86OuterT = t
	s86RunID = time.Now().UTC().Format("20060102T150405Z") + "-" + strconv.Itoa(os.Getpid())
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_file_fence_and_working_directory_on_real_systems.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the file fence and working directory scenarios failed")
	}
}

// TestS86CheckSMBRejectsLocalPaths: a local directory, under any spelling, is
// never accepted as the FS-1 share; the loopback SMB share, when reachable, is.
func TestS86CheckSMBRejectsLocalPaths(t *testing.T) {
	v := &s86Variant{goos: runtime.GOOS, goarch: runtime.GOARCH}
	dir := t.TempDir()
	local := []string{dir}
	vol := filepath.VolumeName(dir)
	if v.goos == "windows" {
		rest := dir[len(vol):]
		local = append(local, `\\?\`+dir, `\\.\`+dir, `\??\`+dir, `\\?\GLOBALROOT\??\`+dir,
			`\\?\UNC\localhost\`+vol[:1]+`$`+rest, `\\.\UNC\localhost\`+vol[:1]+`$`+rest)
	}
	for _, p := range local {
		if err := v.checkSMB(p); err == nil {
			t.Errorf("checkSMB(%q) accepted a local directory as an SMB share", p)
		}
	}
	if v.goos == "windows" {
		unc := `\\localhost\` + vol[:1] + `$` + dir[len(vol):]
		if _, err := os.Stat(unc); err == nil {
			if err := v.checkSMB(unc); err != nil {
				t.Errorf("checkSMB(%q), the loopback SMB share, was refused: %v", unc, err)
			}
		} else {
			t.Logf("the loopback share %s is not reachable here (%v); only the refusals are checked", unc, err)
		}
	}
}
