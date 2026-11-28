//go:build e2e

package e2e

// Step definitions of
// dh_s_18_acceptance_on_real_operating_systems_crash_report_on_each_system.feature
// (DH-S-18 Stage 8.3, DH-E-HOST-4, TC-21s3).
//
// dsh (./cmd/dsh) and the driver (./test/e2e/harness-DSH-M2/testdata/host4drive)
// are built from this checkout for the variant, once per suite and target. The
// driver runs on the variant -- this host when its OS matches, else the other side
// of WSL (wsl.exe from Windows, Windows interop from inside WSL) -- and performs
// the whole sequence there: start "dsh web", wait for "dsh status" to name it live,
// kill it with Process.Kill (SIGKILL / TerminateProcess), start dsh again, read the
// unclean-exit line and the report it names, run "dsh crash", then start
// "dsh --safe web" and wait for its listener to answer. It prints one JSON object,
// which the Then steps assert on. An unreachable variant fails; it never skips.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"
)

const (
	s18cBuildWithin = 10 * time.Minute
	s18cDriveWithin = 6 * time.Minute
	s18cDriverPkg   = "./test/e2e/harness-DSH-M2/testdata/host4drive"
)

var s18cOuterT *testing.T

// s18cResult mirrors host4drive's host4Result.
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

// s18cBuilt is dsh and the driver for one target.
type s18cBuilt struct{ dsh, driver string }

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

// s18cBuild builds dsh and the driver for v's target, once per suite.
func s18cBuild(ctx context.Context, v s18cVariant) (s18cBuilt, error) {
	key := v.goos + "/" + v.goarch
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
	b := s18cBuilt{dsh: filepath.Join(dir, "dsh"+exe), driver: filepath.Join(dir, "host4drive"+exe)}
	env := []string{"GOOS=" + v.goos, "GOARCH=" + v.goarch, "CGO_ENABLED=0"}
	bctx, cancel := context.WithTimeout(ctx, s18cBuildWithin)
	defer cancel()
	if _, err := s18cCommand(bctx, root, env, "go", "build", "-o", b.dsh, "./cmd/dsh"); err != nil {
		return s18cBuilt{}, fmt.Errorf("building dsh for %s: %w", key, err)
	}
	if _, err := s18cCommand(bctx, root, env, "go", "build", "-o", b.driver, s18cDriverPkg); err != nil {
		return s18cBuilt{}, fmt.Errorf("building the driver for %s: %w", key, err)
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

// s18cDrive runs the driver on v with the built dsh and decodes its result.
func s18cDrive(ctx context.Context, v s18cVariant, b s18cBuilt) (*s18cResult, error) {
	ctx, cancel := context.WithTimeout(ctx, s18cDriveWithin)
	defer cancel()
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
		cmd = exec.CommandContext(ctx, b.driver, "-bin", b.dsh)
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
