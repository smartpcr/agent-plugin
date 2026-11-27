//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/sandboxstatus"
)

// The recorded "dsh sandbox status" reports of the impl stage, by the name the
// feature gives them.
var wbpFixtures = map[string]string{
	"bwrap Full":       "bwrap-full.txt",
	"landlock Partial": "landlock-partial.txt",
	"Unconfined":       "unconfined.txt",
}

var (
	wbpBuildOnce sync.Once
	wbpGateBin   string
	wbpBuildErr  error
)

// wbpModuleRoot is tools/harness-gate, seen from tools/harness-gate/test/e2e/harness-DSH-M2.
func wbpModuleRoot() (string, error) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	if !bytes.Contains(data, []byte("module github.com/smartpcr/agent-plugin/tools/harness-gate")) {
		return "", fmt.Errorf("%s is not the harness-gate module", root)
	}
	return root, nil
}

func wbpEnv() []string {
	return append(slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(strings.ToUpper(kv), "GOWORK=")
	}), "GOWORK=off")
}

func wbpBuildGate() (string, error) {
	wbpBuildOnce.Do(func() {
		root, err := wbpModuleRoot()
		if err != nil {
			wbpBuildErr = err
			return
		}
		dir, err := os.MkdirTemp("", "harness-gate-wbp-e2e-")
		if err != nil {
			wbpBuildErr = err
			return
		}
		name := "harness-gate"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		wbpGateBin = filepath.Join(dir, name)
		cmd := exec.Command("go", "build", "-o", wbpGateBin, ".")
		cmd.Dir = root
		cmd.Env = wbpEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			wbpBuildErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
		}
	})
	return wbpGateBin, wbpBuildErr
}

type wbpRun struct {
	stdout, stderr string
	code           int
}

func wbpExec(ctx context.Context, stdin []byte, name string, args ...string) (wbpRun, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = wbpEnv()
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || ctx.Err() != nil {
			return wbpRun{stdout: stdout.String(), stderr: stderr.String()}, fmt.Errorf("run %s %v: %w\nstdout:\n%s\nstderr:\n%s", name, args, errors.Join(err, ctx.Err()), stdout.String(), stderr.String())
		}
		code = exit.ExitCode()
	}
	return wbpRun{stdout: stdout.String(), stderr: stderr.String(), code: code}, nil
}

type wbpVerdict struct {
	status sandboxstatus.Status
	err    error
}

type wbpState struct {
	reports  map[string]string
	verdicts map[string]wbpVerdict
	lane     wbpRun
	commit   string
}

func (s *wbpState) givenRecordedOutputs(a, b, c string) error {
	s.reports = map[string]string{}
	dir, err := wbpModuleRoot()
	if err != nil {
		return err
	}
	for _, name := range []string{a, b, c} {
		file, ok := wbpFixtures[name]
		if !ok {
			return fmt.Errorf("no recorded report named %q", name)
		}
		data, err := os.ReadFile(filepath.Join(dir, "sandboxstatus", "testdata", file))
		if err != nil {
			return err
		}
		s.reports[name] = string(data)
	}
	return nil
}

func (s *wbpState) givenReportWithoutLine(line string) error {
	dir, err := wbpModuleRoot()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(dir, "sandboxstatus", "testdata", wbpFixtures["bwrap Full"]))
	if err != nil {
		return err
	}
	var kept []string
	for _, l := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(l, line) {
			kept = append(kept, l)
		}
	}
	report := strings.Join(kept, "\n")
	if report == string(data) {
		return fmt.Errorf("the bwrap Full fixture has no %q line to remove", line)
	}
	s.reports = map[string]string{"unreadable": report}
	return nil
}

func (s *wbpState) whenParserEvaluates() error {
	if len(s.reports) == 0 {
		return errors.New("no report recorded")
	}
	s.verdicts = map[string]wbpVerdict{}
	for name, report := range s.reports {
		st, err := sandboxstatus.Guarded(report)
		s.verdicts[name] = wbpVerdict{status: st, err: err}
	}
	return nil
}

func (s *wbpState) thenOnlyPasses(want string) error {
	var passed []string
	for name, v := range s.verdicts {
		if v.err == nil {
			passed = append(passed, name)
		}
	}
	if len(passed) != 1 || passed[0] != want {
		return fmt.Errorf("passed %v, want only %q", passed, want)
	}
	if got := s.verdicts[want].status; got.Backend != "bwrap" || got.State != "Full" {
		return fmt.Errorf("%q passed as %v, want backend bwrap, state Full", want, got)
	}
	return nil
}

func (s *wbpState) thenFailsNaming(name, backend, state string) error {
	v, ok := s.verdicts[name]
	if !ok {
		return fmt.Errorf("%q was not evaluated", name)
	}
	if v.err == nil {
		return fmt.Errorf("%q passed the guarded preflight, want a failure", name)
	}
	if v.status.Backend != backend || v.status.State != state {
		return fmt.Errorf("%q read as %v, want backend %s, state %s", name, v.status, backend, state)
	}
	msg := v.err.Error()
	for _, part := range []string{"backend " + backend, "state " + state, "needs backend bwrap, state Full"} {
		if !strings.Contains(msg, part) {
			return fmt.Errorf("%q failed with %q, which does not name %q", name, msg, part)
		}
	}
	return nil
}

func (s *wbpState) thenUnreadable() error {
	v := s.verdicts["unreadable"]
	if v.err == nil {
		return errors.New("a report with no backend line passed the guarded preflight")
	}
	if !strings.Contains(v.err.Error(), "not a dsh sandbox status report") || !strings.Contains(v.err.Error(), `"backend"`) {
		return fmt.Errorf("failed with %q, want it named as not a dsh sandbox status report missing \"backend\"", v.err)
	}
	return nil
}

func (s *wbpState) commandExit(ctx context.Context, name string) (wbpRun, error) {
	bin, err := wbpBuildGate()
	if err != nil {
		return wbpRun{}, err
	}
	report, ok := s.reports[name]
	if !ok {
		return wbpRun{}, fmt.Errorf("no report %q", name)
	}
	return wbpExec(ctx, []byte(report), bin, "sandbox-preflight")
}

func (s *wbpState) thenCommandExits(ctx context.Context, pass string) error {
	for name := range s.reports {
		r, err := s.commandExit(ctx, name)
		if err != nil {
			return err
		}
		want, prefix := 1, "harness-gate sandbox-preflight: FAILED: "
		if name == pass {
			want, prefix = 0, "harness-gate sandbox-preflight: passed: backend bwrap, state Full"
		}
		if r.code != want || !strings.HasPrefix(r.stdout, prefix) {
			return fmt.Errorf("sandbox-preflight on %q exited %d with %q, want %d with %q", name, r.code, r.stdout, want, prefix)
		}
	}
	return nil
}

func (s *wbpState) thenCommandExitsOne(ctx context.Context) error {
	r, err := s.commandExit(ctx, "unreadable")
	if err != nil {
		return err
	}
	if r.code != 1 || !strings.Contains(r.stdout, "FAILED: not a dsh sandbox status report") {
		return fmt.Errorf("sandbox-preflight exited %d with %q, want 1 naming an unreadable report", r.code, r.stdout)
	}
	return nil
}

// wbpLaneScript runs on the WSL lane machine: it unpacks the committed HEAD tree
// (a tar on standard input) into a scratch ext4 directory under TMPDIR,
// commits it so the lane's clean-tree check holds, and runs the lane script's
// guarded preflight there, exiting with its status.
const wbpLaneScript = `set -eu
d=$(mktemp -d "${TMPDIR:-/tmp}/dsh-e2e-guarded-preflight.XXXXXX")
trap 'rm -rf "$d"' EXIT
tar -x -C "$d"
cd "$d"
git init -q
git -c core.autocrlf=false add -A
git -c user.name=dsh-e2e -c user.email=dsh-e2e@localhost -c commit.gpgsign=false commit -q -m "e2e snapshot"
bash tools/harness-gate/lanes/wsl.sh --guarded-preflight
`

func (s *wbpState) givenLaneMachine() error {
	switch runtime.GOOS {
	case "linux":
		if _, err := exec.LookPath("bash"); err != nil {
			return fmt.Errorf("the WSL lane machine needs bash: %w", err)
		}
	case "windows":
		if _, err := exec.LookPath("wsl.exe"); err != nil {
			return fmt.Errorf("no WSL lane machine: wsl.exe is not on PATH (service:linux-sandbox-bwrap): %w", err)
		}
	default:
		return fmt.Errorf("no WSL lane machine on %s (service:linux-sandbox-bwrap)", runtime.GOOS)
	}
	return nil
}

func (s *wbpState) whenLaneRuns(ctx context.Context, script string) error {
	if script != "lanes/wsl.sh --guarded-preflight" {
		return fmt.Errorf("unexpected lane command %q", script)
	}
	mod, err := wbpModuleRoot()
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(ctx, 8*time.Minute)
	defer cancel()
	top, err := wbpExec(runCtx, nil, "git", "-C", mod, "rev-parse", "--show-toplevel")
	if err != nil || top.code != 0 {
		return fmt.Errorf("git rev-parse: %v %s", err, top.stderr)
	}
	root := strings.TrimSpace(top.stdout)
	// Evidence must tie to a commit: only a clean checkout is archived, by SHA.
	head, err := wbpExec(runCtx, nil, "git", "-C", root, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || head.code != 0 {
		return fmt.Errorf("git rev-parse HEAD: %v %s", err, head.stderr)
	}
	s.commit = strings.TrimSpace(head.stdout)
	st, err := wbpExec(runCtx, nil, "git", "-C", root, "status", "--porcelain", "--untracked-files=all")
	if err != nil || st.code != 0 {
		return fmt.Errorf("git status (commit %s): %v %s", s.commit, err, st.stderr)
	}
	if dirty := strings.TrimSpace(st.stdout); dirty != "" {
		return fmt.Errorf("the working tree is not the committed tree %s; commit or stash these changes before running the lane (service:linux-sandbox-bwrap):\n%s", s.commit, dirty)
	}
	fmt.Printf("wsl lane e2e: archived commit %s\n", s.commit)
	arch := exec.CommandContext(runCtx, "git", "-C", root, "archive", "--format=tar", s.commit)
	var tarball, archErr bytes.Buffer
	arch.Stdout, arch.Stderr = &tarball, &archErr
	if err := arch.Run(); err != nil {
		return fmt.Errorf("git archive %s: %v\n%s", s.commit, err, archErr.String())
	}
	var name string
	var args []string
	if runtime.GOOS == "windows" {
		name = "wsl.exe"
		if d := os.Getenv("DSH_WSL_DISTRO"); d != "" {
			args = append(args, "-d", d)
		}
		enc := base64.StdEncoding.EncodeToString([]byte(wbpLaneScript))
		args = append(args, "--exec", "bash", "-c", `eval "$(printf %s `+enc+` | base64 -d)"`)
	} else {
		name, args = "bash", []string{"-c", wbpLaneScript}
	}
	s.lane, err = wbpExec(runCtx, tarball.Bytes(), name, args...)
	return err
}

func (s *wbpState) thenLaneExits(code int) error {
	if s.lane.code != code {
		return fmt.Errorf("wsl.sh --guarded-preflight on commit %s exited %d, want %d\nstdout:\n%s\nstderr:\n%s", s.commit, s.lane.code, code, s.lane.stdout, s.lane.stderr)
	}
	return nil
}

func (s *wbpState) thenReportPrinted(backend, state string) error {
	out := strings.ReplaceAll(s.lane.stdout, "\r\n", "\n")
	_, report, ok := strings.Cut(out, "wsl lane: dsh sandbox status (bwrap ")
	if !ok {
		return fmt.Errorf("the lane printed no dsh sandbox status report:\n%s", out)
	}
	for _, re := range []string{`(?m)^  state:\s+` + regexp.QuoteMeta(state) + `$`, `(?m)^  backend:\s+` + regexp.QuoteMeta(backend) + `$`} {
		if !regexp.MustCompile(re).MatchString(report) {
			return fmt.Errorf("the printed report does not match %s:\n%s", re, report)
		}
	}
	return nil
}

func (s *wbpState) thenPassedLine(phrase, backend, state string) error {
	re := regexp.MustCompile(`(?m)^wsl lane: ` + regexp.QuoteMeta(phrase) + `: bwrap (/\S+); backend ` + regexp.QuoteMeta(backend) + `, state ` + regexp.QuoteMeta(state) + `$`)
	out := strings.ReplaceAll(s.lane.stdout, "\r\n", "\n")
	if !re.MatchString(out) {
		return fmt.Errorf("no line matching %s in:\n%s", re, out)
	}
	return nil
}

func InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_wsl_lane_bubblewrap_preflight(ctx *godog.ScenarioContext) {
	s := &wbpState{}
	ctx.Step(`^recorded "dsh sandbox status" outputs "([^"]*)", "([^"]*)" and "([^"]*)"$`, s.givenRecordedOutputs)
	ctx.Step(`^a "dsh sandbox status" report with no "([^"]*)" line$`, s.givenReportWithoutLine)
	ctx.Step(`^the preflight parser evaluates (?:them|it)$`, s.whenParserEvaluates)
	ctx.Step(`^only "([^"]*)" passes$`, s.thenOnlyPasses)
	ctx.Step(`^"([^"]*)" fails naming backend "([^"]*)" and state "([^"]*)"$`, s.thenFailsNaming)
	ctx.Step(`^it fails as not a dsh sandbox status report$`, s.thenUnreadable)
	ctx.Step(`^the sandbox-preflight command exits 0 for "([^"]*)" and 1 for the others$`, s.thenCommandExits)
	ctx.Step(`^the sandbox-preflight command exits 1 for it$`, s.thenCommandExitsOne)
	ctx.Step(`^the WSL lane machine with a clean ext4 checkout of the committed tree$`, s.givenLaneMachine)
	ctx.Step(`^"([^"]*)" runs$`, s.whenLaneRuns)
	ctx.Step(`^it exits (\d+)$`, s.thenLaneExits)
	ctx.Step(`^it prints the "dsh sandbox status" report with backend "([^"]*)" and state "([^"]*)"$`, s.thenReportPrinted)
	ctx.Step(`^it prints "([^"]*)" with bwrap on an absolute path, backend "([^"]*)", state "([^"]*)"$`, s.thenPassedLine)
}

func TestE2E_dh_s_16_profiles_launchers_and_evidence_runners_wsl_lane_bubblewrap_preflight(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_16_profiles_launchers_and_evidence_runners_wsl_lane_bubblewrap_preflight,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_16_profiles_launchers_and_evidence_runners_wsl_lane_bubblewrap_preflight.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
