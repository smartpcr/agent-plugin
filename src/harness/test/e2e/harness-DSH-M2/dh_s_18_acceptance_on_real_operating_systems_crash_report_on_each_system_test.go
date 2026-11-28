//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_crash_report_on_each_system.feature
// (DH-S-18 Stage 8.3, DH-E-HOST-4, TC-21s3).
//
// dsh (./cmd/dsh) is built from this checkout for the variant, once per suite and
// target. The sequence runs on the variant: start "dsh web", wait for "dsh status"
// to name it live, kill it with Process.Kill (SIGKILL / TerminateProcess), start dsh
// again, read the unclean-exit line and the report it names, run "dsh crash", then
// start "dsh --safe web" and wait for its listener to answer.
//
// When the variant is this host ("native") the sequence runs in this test process
// (s18cSequence), so no extra driver executable is built: on stock Windows hosts
// Defender quarantines the small CGO-disabled driver exe that spawns and force-kills
// children. Only for the cross-OS variants (wsl.exe from Windows, Windows interop
// from inside WSL) is the driver (./test/e2e/harness-DSH-M2/testdata/host4drive)
// built and run on the other side; it performs the same sequence and prints one
// JSON object. A build blocked by antivirus is reported as an environment blocker
// with its remediation. An unreachable variant fails; it never skips.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
)

const (
	s18cBuildWithin = 10 * time.Minute
	s18cDriveWithin = 6 * time.Minute
	s18cDriverPkg   = "./test/e2e/harness-DSH-M2/testdata/host4drive"
)

var s18cOuterT *testing.T

// s18cResult mirrors host4drive's host4Result; s18cSequence fills it in-process.
type s18cResult struct {
	GOOS       string `json:"goos"`
	Kill       string `json:"kill"`
	Home       string `json:"home"`
	ReportsDir string `json:"reportsDir"`

	FirstPID    int    `json:"firstPid"`
	FirstReady  string `json:"firstReady"`
	FirstStderr string `json:"firstStderr"`
	KilledExit  bool   `json:"killedExit"`

	NextStderr  string `json:"nextStderr"`
	UncleanLine string `json:"uncleanLine"`
	Report      string `json:"report"`
	ReportUnder bool   `json:"reportUnder"`
	ReportErr   string `json:"reportErr"`
	PrivateErr  string `json:"privateErr"`
	ReportText  string `json:"reportText"`
	CrashCode   int    `json:"crashCode"`
	CrashOut    string `json:"crashOut"`

	SafePID    int    `json:"safePid"`
	SafeReady  string `json:"safeReady"`
	SafeStderr string `json:"safeStderr"`

	Fatal string `json:"fatal"`
}

// s18cVariant is where the driver runs.
type s18cVariant struct {
	goos, goarch string
	how          string // "native", "wsl" (wsl.exe from Windows) or "interop" (a .exe from WSL)
}

// s18cBuilt is dsh and, for a cross-OS variant, the driver for one target.
type s18cBuilt struct{ dsh, driver string }

// s18cBuildError classifies a failed go build. Antivirus quarantine of the linked
// executable (Defender's "contains a virus or potentially unwanted software") is an
// environment blocker, not a product failure; it still fails the scenario.
func s18cBuildError(what, key string, err error) error {
	msg := err.Error()
	if strings.Contains(msg, "virus or potentially unwanted software") || strings.Contains(msg, "Operation did not complete successfully because the file contains a virus") {
		return fmt.Errorf("ENVIRONMENT BLOCKER: building %s for %s was blocked by antivirus (Windows Defender quarantined the linked executable). "+
			"Remediation: add a Defender exclusion for the Go build temp and output directories "+
			"(e.g. Add-MpPreference -ExclusionPath $env:TEMP\\go-build, $env:TEMP\\dsh-s18c-bin-, or set GOTMPDIR to an excluded directory), then rerun. "+
			"The scenario stays open until it passes: %w", what, key, err)
	}
	return fmt.Errorf("building %s for %s: %w", what, key, err)
}

var (
	s18cBuildMu sync.Mutex
	s18cBuilds  = map[string]s18cBuilt{}
)

func s18cCommand(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w\n%s%s", name, strings.Join(args, " "), err, out, stderr.String())
	}
	return string(out), nil
}

// s18cModuleRoot is src/harness: the directory above the working directory that
// holds go.mod and cmd/dsh.
func s18cModuleRoot() (string, error) {
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
			return "", errors.New("the harness module (go.mod beside cmd/dsh) not found above the working directory")
		}
		dir = parent
	}
}

// s18cBuild builds dsh for v's target, and the driver too unless v is native (the
// native sequence runs in-process), once per suite.
func s18cBuild(ctx context.Context, v s18cVariant) (s18cBuilt, error) {
	key := v.goos + "/" + v.goarch + "/" + v.how
	s18cBuildMu.Lock()
	defer s18cBuildMu.Unlock()
	if b, ok := s18cBuilds[key]; ok {
		return b, nil
	}
	root, err := s18cModuleRoot()
	if err != nil {
		return s18cBuilt{}, err
	}
	dir, err := os.MkdirTemp("", "dsh-s18c-bin-")
	if err != nil {
		return s18cBuilt{}, err
	}
	s18cOuterT.Cleanup(func() { _ = os.RemoveAll(dir) })
	exe := ""
	if v.goos == "windows" {
		exe = ".exe"
	}
	b := s18cBuilt{dsh: filepath.Join(dir, "dsh"+exe)}
	env := []string{"GOOS=" + v.goos, "GOARCH=" + v.goarch, "CGO_ENABLED=0"}
	bctx, cancel := context.WithTimeout(ctx, s18cBuildWithin)
	defer cancel()
	if _, err := s18cCommand(bctx, root, env, "go", "build", "-o", b.dsh, "./cmd/dsh"); err != nil {
		return s18cBuilt{}, s18cBuildError("dsh", key, err)
	}
	if v.how != "native" {
		b.driver = filepath.Join(dir, "host4drive"+exe)
		if _, err := s18cCommand(bctx, root, env, "go", "build", "-o", b.driver, s18cDriverPkg); err != nil {
			return s18cBuilt{}, s18cBuildError("the driver", key, err)
		}
	}
	s18cBuilds[key] = b
	return b, nil
}

func s18cInsideWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	_, err := os.Stat("/proc/sys/fs/binfmt_misc/WSLInterop")
	return err == nil
}

func s18cLinuxVariant(ctx context.Context) (s18cVariant, error) {
	if runtime.GOOS == "linux" {
		return s18cVariant{"linux", runtime.GOARCH, "native"}, nil
	}
	if runtime.GOOS != "windows" {
		return s18cVariant{}, fmt.Errorf("no Linux variant reachable from %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	m, err := s18cCommand(ctx, "", nil, "wsl.exe", "-e", "uname", "-m")
	if err != nil {
		return s18cVariant{}, fmt.Errorf("the Linux variant is the default WSL distribution, which did not answer: %w", err)
	}
	switch strings.TrimSpace(m) {
	case "x86_64":
		return s18cVariant{"linux", "amd64", "wsl"}, nil
	case "aarch64", "arm64":
		return s18cVariant{"linux", "arm64", "wsl"}, nil
	default:
		return s18cVariant{}, fmt.Errorf("the WSL distribution runs on %q, no dsh target", strings.TrimSpace(m))
	}
}

func s18cWindowsVariant() (s18cVariant, error) {
	if runtime.GOOS == "windows" {
		return s18cVariant{"windows", runtime.GOARCH, "native"}, nil
	}
	if runtime.GOOS == "linux" && s18cInsideWSL() {
		return s18cVariant{"windows", runtime.GOARCH, "interop"}, nil
	}
	return s18cVariant{}, fmt.Errorf("no Windows host reachable from %s/%s (neither Windows nor WSL interop)", runtime.GOOS, runtime.GOARCH)
}

// s18cDrive runs the sequence on v with the built dsh: in this process when v is
// native, else through the driver on the other side of WSL.
func s18cDrive(ctx context.Context, v s18cVariant, b s18cBuilt) (*s18cResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s18cDriveWithin)
	defer cancel()
	if v.how == "native" {
		return s18cRunInProcess(ctx, b.dsh), nil
	}
	var cmd *exec.Cmd
	switch v.how {
	case "wsl":
		drv, err := s18cCommand(ctx, "", nil, "wsl.exe", "-e", "wslpath", "-a", filepath.ToSlash(b.driver))
		if err != nil {
			return nil, err
		}
		dsh, err := s18cCommand(ctx, "", nil, "wsl.exe", "-e", "wslpath", "-a", filepath.ToSlash(b.dsh))
		if err != nil {
			return nil, err
		}
		// The driver's temp home is the distribution's own /tmp, not the Windows drive.
		cmd = exec.CommandContext(ctx, "wsl.exe", "-e", "env", "TMPDIR=/tmp", strings.TrimSpace(drv), "-bin", strings.TrimSpace(dsh))
	case "interop":
		dsh, err := s18cCommand(ctx, "", nil, "wslpath", "-w", b.dsh)
		if err != nil {
			return nil, err
		}
		cmd = exec.CommandContext(ctx, b.driver, "-bin", strings.TrimSpace(dsh))
	default:
		return nil, fmt.Errorf("unknown variant transport %q", v.how)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("the driver (%s) on %s/%s: %w\nstdout:\n%s\nstderr:\n%s", v.how, v.goos, v.goarch, err, out, stderr.String())
	}
	r := &s18cResult{}
	if err := json.Unmarshal(out, r); err != nil {
		return nil, fmt.Errorf("the driver printed no result: %w\nstdout:\n%s\nstderr:\n%s", err, out, stderr.String())
	}
	return r, nil
}

// The in-process sequence below is the same as host4drive's (keep them in step);
// it runs on this host, so the kill and the owner-only check are this OS's own.
const (
	s18cReadyWithin = time.Minute
	s18cExitWithin  = time.Minute
	s18cLineWithin  = time.Minute
)

var s18cUnclean = regexp.MustCompile(`did not stop cleanly; report: (\S+);.*--safe`)

// s18cBuffer is a goroutine-safe output sink.
type s18cBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *s18cBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *s18cBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

type s18cHost struct {
	cmd    *exec.Cmd
	stderr *s18cBuffer
	done   chan struct{}
}

func (h *s18cHost) exited(within time.Duration) bool {
	select {
	case <-h.done:
		return true
	case <-time.After(within):
		return false
	}
}

// kill is the forced rung alone: Process.Kill is SIGKILL on POSIX and
// TerminateProcess on Windows, so the host cannot remove its runtime record.
func (h *s18cHost) kill() { _ = h.cmd.Process.Kill() }

type s18cDriver struct {
	ctx       context.Context
	bin, root string
	env       []string
	hosts     []*s18cHost
}

func (d *s18cDriver) start(args ...string) (*s18cHost, error) {
	h := &s18cHost{stderr: &s18cBuffer{}, done: make(chan struct{})}
	h.cmd = exec.Command(d.bin, append([]string{"--home", d.root}, args...)...)
	h.cmd.Env, h.cmd.Dir, h.cmd.Stdout, h.cmd.Stderr = d.env, filepath.Dir(d.root), h.stderr, h.stderr
	if err := h.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { defer close(h.done); _ = h.cmd.Wait() }()
	d.hosts = append(d.hosts, h)
	return h, nil
}

func (d *s18cDriver) run(args ...string) (int, string, string) {
	cmd := exec.CommandContext(d.ctx, d.bin, append([]string{"--home", d.root}, args...)...)
	var out, errb bytes.Buffer
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = d.env, filepath.Dir(d.root), &out, &errb
	err := cmd.Run()
	var ee *exec.ExitError
	switch {
	case err == nil:
		return 0, out.String(), errb.String()
	case errors.As(err, &ee):
		return ee.ExitCode(), out.String(), errb.String()
	default:
		return -1, out.String(), err.Error()
	}
}

// answers waits until "dsh status" exits 0 naming h's process live, and returns
// that status line.
func (d *s18cDriver) answers(h *s18cHost) (string, error) {
	pid := "process " + strconv.Itoa(h.cmd.Process.Pid)
	deadline := time.Now().Add(s18cReadyWithin)
	var last string
	for time.Now().Before(deadline) {
		select {
		case <-h.done:
			return "", fmt.Errorf("the host exited %d before its listener answered:\n%s", h.cmd.ProcessState.ExitCode(), h.stderr)
		case <-d.ctx.Done():
			return "", fmt.Errorf("waiting for %s: %w", pid, d.ctx.Err())
		default:
		}
		code, out, errs := d.run("status")
		last = fmt.Sprintf("exit %d\n%s%s", code, out, errs)
		if code == 0 {
			for _, line := range strings.Split(out, "\n") {
				if strings.Contains(line, pid+"\t") && strings.Contains(line, "\tlive") {
					return strings.TrimSpace(line), nil
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return "", fmt.Errorf("dsh status did not name %s live within %s; last:\n%s\nhost stderr:\n%s", pid, s18cReadyWithin, last, h.stderr)
}

// waitLine waits for h's unclean-exit line naming process pid and returns the
// line and the report it names.
func (d *s18cDriver) waitLine(h *s18cHost, pid int) (string, string, error) {
	want := "(process " + strconv.Itoa(pid) + ")"
	find := func() (string, string, bool) {
		for _, line := range strings.Split(h.stderr.String(), "\n") {
			if m := s18cUnclean.FindStringSubmatch(line); m != nil && strings.Contains(line, want) {
				return strings.TrimSpace(line), m[1], true
			}
		}
		return "", "", false
	}
	deadline := time.After(s18cLineWithin)
	for {
		if line, report, ok := find(); ok {
			return line, report, nil
		}
		select {
		case <-h.done:
			if line, report, ok := find(); ok {
				return line, report, nil
			}
			return "", "", fmt.Errorf("dsh exited %d with no unclean-exit line naming %s:\n%s", h.cmd.ProcessState.ExitCode(), want, h.stderr)
		case <-deadline:
			return "", "", fmt.Errorf("no unclean-exit line naming %s within %s:\n%s", want, s18cLineWithin, h.stderr)
		case <-d.ctx.Done():
			return "", "", fmt.Errorf("waiting for the unclean-exit line naming %s: %w", want, d.ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func s18cInside(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !filepath.IsAbs(rel) && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (d *s18cDriver) sequence(r *s18cResult) error {
	first, err := d.start("web", "--no-open", "--port", "0")
	if err != nil {
		return fmt.Errorf("starting the first host: %w", err)
	}
	r.FirstPID = first.cmd.Process.Pid
	r.FirstReady, err = d.answers(first)
	r.FirstStderr = first.stderr.String()
	if err != nil {
		return err
	}
	first.kill()
	if r.KilledExit = first.exited(s18cExitWithin); !r.KilledExit {
		return fmt.Errorf("the killed host %d did not exit within %s", r.FirstPID, s18cExitWithin)
	}

	next, err := d.start("web", "--no-open", "--port", "0")
	if err != nil {
		return fmt.Errorf("starting dsh again: %w", err)
	}
	r.UncleanLine, r.Report, err = d.waitLine(next, r.FirstPID)
	r.NextStderr = next.stderr.String()
	if err != nil {
		return err
	}
	r.ReportUnder = s18cInside(r.ReportsDir, r.Report)
	if f, err := os.Open(r.Report); err != nil {
		r.ReportErr = err.Error()
	} else {
		if err := home.CheckPrivateFile(f); err != nil {
			r.PrivateErr = err.Error()
		}
		_ = f.Close()
		if b, err := os.ReadFile(r.Report); err != nil {
			r.ReportErr = err.Error()
		} else {
			r.ReportText = string(b)
		}
	}
	var crashErr string
	r.CrashCode, r.CrashOut, crashErr = d.run("crash")
	if r.CrashCode != 0 {
		r.CrashOut += crashErr
	}
	if _, err := d.answers(next); err != nil {
		return fmt.Errorf("the restarted host: %w", err)
	}
	next.kill()
	if !next.exited(s18cExitWithin) {
		return fmt.Errorf("the restarted host did not exit within %s of its kill", s18cExitWithin)
	}

	safe, err := d.start("--safe", "web", "--no-open", "--port", "0")
	if err != nil {
		return fmt.Errorf("starting dsh --safe web: %w", err)
	}
	r.SafePID = safe.cmd.Process.Pid
	r.SafeReady, err = d.answers(safe)
	r.SafeStderr = safe.stderr.String()
	return err
}

// s18cRunInProcess runs the sequence on this host against bin on a fresh short
// home, kills every host it started and removes the home. A failed step is
// recorded in Fatal, as the driver does.
func s18cRunInProcess(ctx context.Context, bin string) *s18cResult {
	r := &s18cResult{GOOS: runtime.GOOS, Kill: "SIGKILL"}
	if runtime.GOOS == "windows" {
		r.Kill = "TerminateProcess"
	}
	// A short home: the web host's control socket lives in <home>/run and a Unix
	// socket path is bounded (about 108 bytes).
	dir, err := os.MkdirTemp("", "dsh-h4e-")
	if err != nil {
		r.Fatal = err.Error()
		return r
	}
	root := filepath.Join(dir, "home")
	r.Home, r.ReportsDir = root, home.New(root).Reports()
	env := []string{"DSH_HOME=" + root}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "DSH_HOME=") {
			env = append(env, kv)
		}
	}
	d := &s18cDriver{ctx: ctx, bin: bin, root: root, env: env}
	if err := d.sequence(r); err != nil {
		r.Fatal = err.Error()
	}
	for _, h := range d.hosts {
		h.kill()
		h.exited(s18cExitWithin)
	}
	deadline := time.Now().Add(10 * time.Second)
	for os.RemoveAll(dir) != nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	return r
}

// s18cState is one scenario's state.
type s18cState struct {
	variant s18cVariant
	r       *s18cResult
}

func (s *s18cState) aLinuxVariant(ctx context.Context) error {
	v, err := s18cLinuxVariant(ctx)
	s.variant = v
	return err
}

func (s *s18cState) theWindowsHost() error {
	v, err := s18cWindowsVariant()
	s.variant = v
	return err
}

func (s *s18cState) killAndRestart(ctx context.Context) error {
	b, err := s18cBuild(ctx, s.variant)
	if err != nil {
		return err
	}
	if s.r, err = s18cDrive(ctx, s.variant, b); err != nil {
		return err
	}
	if s.r.Fatal != "" {
		return fmt.Errorf("the sequence on %s did not complete: %s", s.variant.goos, s.r.Fatal)
	}
	return nil
}

func (s *s18cState) ranOnVariant() error {
	want := "SIGKILL"
	if s.variant.goos == "windows" {
		want = "TerminateProcess"
	}
	if s.r.GOOS != s.variant.goos {
		return fmt.Errorf("the driver ran on %s, want the %s variant", s.r.GOOS, s.variant.goos)
	}
	if s.r.Kill != want {
		return fmt.Errorf("the driver killed the host with %q, want %q", s.r.Kill, want)
	}
	return nil
}

func (s *s18cState) firstAnsweredNoLine() error {
	if !strings.Contains(s.r.FirstReady, "process "+strconv.Itoa(s.r.FirstPID)) || !strings.Contains(s.r.FirstReady, "live") {
		return fmt.Errorf("dsh status did not name the first host %d live: %q", s.r.FirstPID, s.r.FirstReady)
	}
	if strings.Contains(s.r.FirstStderr, "did not stop cleanly") {
		return fmt.Errorf("the first start on a fresh home printed an unclean-exit line:\n%s", s.r.FirstStderr)
	}
	return nil
}

func (s *s18cState) killedExited() error {
	if !s.r.KilledExit {
		return fmt.Errorf("the killed host %d did not exit", s.r.FirstPID)
	}
	return nil
}

func (s *s18cState) uncleanLineNamesReport() error {
	line := s.r.UncleanLine
	if !strings.Contains(line, "did not stop cleanly") || !strings.Contains(line, "--safe") {
		return fmt.Errorf("no unclean-exit line with --safe:\n%s", s.r.NextStderr)
	}
	if !strings.Contains(line, "(process "+strconv.Itoa(s.r.FirstPID)+")") {
		return fmt.Errorf("the unclean-exit line does not name the killed process %d: %q", s.r.FirstPID, line)
	}
	if origin := strings.Fields(s.r.FirstReady); len(origin) == 0 || !strings.Contains(line, "the host of "+origin[0]+" ") {
		return fmt.Errorf("the unclean-exit line does not name the killed host's origin (status %q): %q", s.r.FirstReady, line)
	}
	if !strings.Contains(line, "report: "+s.r.Report+";") || !s.r.ReportUnder {
		return fmt.Errorf("the named report %q is not under %s: %q", s.r.Report, s.r.ReportsDir, line)
	}
	return nil
}

func (s *s18cState) reportReadablePrivate() error {
	if s.r.ReportErr != "" {
		return fmt.Errorf("the named report %s is not readable: %s", s.r.Report, s.r.ReportErr)
	}
	if s.r.PrivateErr != "" {
		return fmt.Errorf("the named report %s is not owner-only: %s", s.r.Report, s.r.PrivateErr)
	}
	if !strings.Contains(s.r.ReportText, "unclean exit") || !strings.Contains(s.r.ReportText, "pid:     "+strconv.Itoa(s.r.FirstPID)+"\n") {
		return fmt.Errorf("the report names no unclean exit of process %d:\n%s", s.r.FirstPID, s.r.ReportText)
	}
	return nil
}

func (s *s18cState) crashPrintsReport() error {
	if s.r.CrashCode != 0 || strings.TrimSpace(s.r.CrashOut) != s.r.Report {
		return fmt.Errorf("dsh crash exited %d with %q, want %s", s.r.CrashCode, s.r.CrashOut, s.r.Report)
	}
	return nil
}

func (s *s18cState) safeAnswers() error {
	if s.r.SafePID == 0 || !strings.Contains(s.r.SafeReady, "process "+strconv.Itoa(s.r.SafePID)) || !strings.Contains(s.r.SafeReady, "live") {
		return fmt.Errorf("dsh status did not name the --safe host %d live: %q\n%s", s.r.SafePID, s.r.SafeReady, s.r.SafeStderr)
	}
	return nil
}

func InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_crash_report_on_each_system(ctx *godog.ScenarioContext) {
	s := &s18cState{}
	ctx.Step(`^a Linux variant$`, s.aLinuxVariant)
	ctx.Step(`^the Windows host$`, s.theWindowsHost)
	ctx.Step(`^on the variant a host started on a fresh short home answers, is killed with no grace and dsh starts again on that home$`, s.killAndRestart)
	ctx.Step(`^the driver ran on the variant's operating system and killed the host with its forced kill$`, s.ranOnVariant)
	ctx.Step(`^the first start answered its readiness probe and printed no unclean-exit line$`, s.firstAnsweredNoLine)
	ctx.Step(`^the killed host exited$`, s.killedExited)
	ctx.Step(`^the next start prints the unclean-exit line naming the killed process, a report under <home>/reports and "--safe"$`, s.uncleanLineNamesReport)
	ctx.Step(`^the named report is readable, owner-only and records the unclean exit of the killed process$`, s.reportReadablePrivate)
	ctx.Step(`^"dsh crash" prints the same report path$`, s.crashPrintsReport)
	ctx.Step(`^a "--safe" start on the same home makes the listener answer$`, s.safeAnswers)
}

func TestE2E_dh_s_18_acceptance_on_real_operating_systems_crash_report_on_each_system(t *testing.T) {
	s18cOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_18_acceptance_on_real_operating_systems_crash_report_on_each_system,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_18_acceptance_on_real_operating_systems_crash_report_on_each_system.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
