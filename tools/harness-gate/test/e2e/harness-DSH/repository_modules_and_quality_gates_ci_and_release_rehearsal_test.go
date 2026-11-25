//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck"
)

// crRehearsalBinaries are the six release targets of QG-8 and their GOOS/GOARCH.
var crRehearsalBinaries = map[string][2]string{
	"dsh-linux-amd64":       {"linux", "amd64"},
	"dsh-linux-arm64":       {"linux", "arm64"},
	"dsh-darwin-amd64":      {"darwin", "amd64"},
	"dsh-darwin-arm64":      {"darwin", "arm64"},
	"dsh-windows-amd64.exe": {"windows", "amd64"},
	"dsh-windows-arm64.exe": {"windows", "arm64"},
}

// crLanes are the hosted CI lanes of decision platforms-windows-wsl: regression
// signals on Linux and Windows; macOS is out of M1 scope.
var crLanes = []string{"ubuntu-latest", "windows-latest"}

// crModelMarkers name the environment variables that could carry a model
// credential. The preflight reads none (models-signed-in-claude-copilot), so the
// scenarios run it with every such variable removed.
var crModelMarkers = []string{"API_KEY", "DEEPSEEK", "ANTHROPIC", "OPENAI", "CLAUDE", "COPILOT"}

func crIsModelVar(name string) bool {
	upper := strings.ToUpper(name)
	return slices.ContainsFunc(crModelMarkers, func(m string) bool { return strings.Contains(upper, m) })
}

// crEnvWithoutModelVars is crEnv with every model variable removed as well.
func crEnvWithoutModelVars(drop []string, extra ...string) []string {
	env := slices.DeleteFunc(crEnv(drop), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return crIsModelVar(k)
	})
	return append(env, extra...)
}

// crProductModules are the module directories the gate and the scenario index scan.
var crProductModules = []string{"src/go", "src/sdk", "src/harness"}

// crRepoRoot is the repository root, two levels above tools/harness-gate.
func crRepoRoot() (string, error) {
	mod, err := moduleRoot()
	if err != nil {
		return "", err
	}
	root := filepath.Clean(filepath.Join(mod, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(acceptance.IndexPath))); err != nil {
		return "", fmt.Errorf("acceptance index not found under %s: %w", root, err)
	}
	return root, nil
}

// crEnv is a child environment without the variables a scenario controls, plus extra.
func crEnv(drop []string, extra ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		return slices.Contains(drop, strings.ToUpper(k))
	})
	return append(env, extra...)
}

var crGoDrop = []string{"CI", "GOFLAGS", "GOWORK", "CGO_ENABLED", "GOOS", "GOARCH"}

// This suite builds its own binaries: the sibling suites remove theirs when they end.
var (
	crBuildOnce sync.Once
	crBuildDir  string
	crGateBin   string // the real harness-gate command
	crDriverBin string // harness-gate with main.go replaced by tlGateDriverSource: one named gate
	crBuildErr  error
)

func crBuild() error {
	crBuildOnce.Do(func() {
		mod, err := moduleRoot()
		if err != nil {
			crBuildErr = err
			return
		}
		if crBuildDir, err = os.MkdirTemp("", "harness-gate-e2e-ci-"); err != nil {
			crBuildErr = err
			return
		}
		env := crEnv(crGoDrop, "GOWORK=off")
		crGateBin = filepath.Join(crBuildDir, exe("harness-gate"))
		if out, _, code, err := crRun(mod, env, "go", "build", "-o", crGateBin, "."); err != nil || code != 0 {
			crBuildErr = fmt.Errorf("build harness-gate (exit %d): %v\n%s", code, err, out)
			return
		}
		src := filepath.Join(crBuildDir, "driver.go")
		if err := os.WriteFile(src, []byte(tlGateDriverSource), 0o644); err != nil {
			crBuildErr = err
			return
		}
		overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(mod, "main.go"): src}})
		ov := filepath.Join(crBuildDir, "overlay.json")
		if err := os.WriteFile(ov, overlay, 0o644); err != nil {
			crBuildErr = err
			return
		}
		crDriverBin = filepath.Join(crBuildDir, exe("gate-driver"))
		if out, errOut, code, err := crRun(mod, env, "go", "build", "-overlay", ov, "-o", crDriverBin, "."); err != nil || code != 0 {
			crBuildErr = fmt.Errorf("build one-gate driver (exit %d): %v\n%s%s", code, err, out, errOut)
		}
	})
	return crBuildErr
}

func crRun(dir string, env []string, name string, args ...string) (string, string, int, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir, cmd.Env = dir, env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return stdout.String(), stderr.String(), 0, nil
	case errors.As(err, &exit):
		return stdout.String(), stderr.String(), exit.ExitCode(), nil
	default:
		return stdout.String(), stderr.String(), -1, fmt.Errorf("run %s %v: %w\n%s", name, args, err, stderr.String())
	}
}

type ciRehearsal struct {
	dirs []string
	root string

	// preflight
	index     *acceptance.Index
	scenarios []acceptance.Scenario
	env       map[string]string
	plan      acceptance.Plan
	planErr   error

	// processes
	stdout, stderr string
	exitCode       int

	// rehearsal
	rehearseEnv []string
	outDir      string
	tagsBefore  string

	// hosted-lane gate
	head  string
	gated string // commit recorded in the evidence's Source and build identity row

	// scenario index
	vetOut     string
	vetCode    int
	authored   map[string][]string // ID -> scenario files, scanned independently of the gate
	authoredFn map[string]string   // ID -> TestXxx
}

func (s *ciRehearsal) tempDir() (string, error) {
	dir, err := os.MkdirTemp("", "dsh-e2e-ci-")
	if err != nil {
		return "", err
	}
	s.dirs = append(s.dirs, dir)
	return dir, nil
}

func (s *ciRehearsal) cleanup() {
	for _, d := range s.dirs {
		os.RemoveAll(d)
	}
}

func (s *ciRehearsal) repo() (string, error) {
	if s.root == "" {
		root, err := crRepoRoot()
		if err != nil {
			return "", err
		}
		s.root = root
	}
	return s.root, nil
}

// --- Preflight ------------------------------------------------------------------

func (s *ciRehearsal) anInMemoryIndex(id, stage, authored string) error {
	x, err := acceptance.ParseIndex(strings.NewReader(fmt.Sprintf(
		"built-through: DH-S-1\nstages:\n  %s:\n    - id: %s\n      authored: %s\n      credential: none\n", stage, id, authored)))
	if err != nil {
		return fmt.Errorf("parse in-memory index: %w", err)
	}
	src := fmt.Sprintf("//go:build scenario\n\npackage x\n\nimport \"testing\"\n\n//dsh:scenario %s\nfunc TestX(t *testing.T) {}\n", id)
	scenarios, err := acceptance.ParseScenarioFile("x_scenario_test.go", []byte(src))
	if err != nil {
		return fmt.Errorf("parse in-memory scenario file: %w", err)
	}
	if err := acceptance.Check(x, scenarios); err != nil {
		return fmt.Errorf("in-memory index and scenario disagree before the preflight: %w", err)
	}
	s.index, s.scenarios = x, scenarios
	return nil
}

// envWithoutModelVars injects an environment with no model variable at all: the
// preflight must decide without a key (models-signed-in-claude-copilot).
func (s *ciRehearsal) envWithoutModelVars(require string) error {
	s.env = map[string]string{acceptance.RequireEnv: require, "PATH": "/usr/bin", "DSH_HOME": "/nonexistent"}
	for k := range s.env {
		if crIsModelVar(k) {
			return fmt.Errorf("injected environment holds model variable %s", k)
		}
	}
	return nil
}

func (s *ciRehearsal) preflightRuns() error {
	s.plan, s.planErr = acceptance.Preflight(s.env, s.index, s.scenarios)
	return nil
}

func (s *ciRehearsal) preflightFailsNaming(name string) error {
	if s.planErr == nil {
		return fmt.Errorf("preflight passed with plan %v, want a failure naming %s", s.plan.Tests(), name)
	}
	if !strings.Contains(s.planErr.Error(), name) {
		return fmt.Errorf("preflight failure does not name %s:\n%v", name, s.planErr)
	}
	return nil
}

func (s *ciRehearsal) noScenarioStarted() error {
	if len(s.plan.Scenarios) != 0 || s.plan.RunPattern() != "" || len(s.plan.Tests()) != 0 {
		return fmt.Errorf("failed preflight still plans scenarios: %v (run %q)", s.plan.Tests(), s.plan.RunPattern())
	}
	return nil
}

func (s *ciRehearsal) emptyRequiredRuns() error {
	s.env[acceptance.RequireEnv] = ""
	return s.preflightRuns()
}

func (s *ciRehearsal) preflightPassesEmpty() error {
	if s.planErr != nil {
		return fmt.Errorf("preflight with an empty required list failed: %v", s.planErr)
	}
	return s.noScenarioStarted()
}

// runPreflightSubcommand runs the built "harness-gate preflight" on the repository
// with no model variable in its environment and DSH_REQUIRE_SCENARIOS=require; it
// returns the output, the exit status and the GITHUB_OUTPUT path.
func (s *ciRehearsal) runPreflightSubcommand(require string) (stdout, stderr string, code int, ghOut string, err error) {
	if err = crBuild(); err != nil {
		return
	}
	root, err := s.repo()
	if err != nil {
		return
	}
	dir, err := s.tempDir()
	if err != nil {
		return
	}
	ghOut = filepath.Join(dir, "github_output")
	env := crEnvWithoutModelVars([]string{acceptance.RequireEnv, "GITHUB_OUTPUT"},
		acceptance.RequireEnv+"="+require, "GITHUB_OUTPUT="+ghOut)
	stdout, stderr, code, err = crRun(root, env, crGateBin, "preflight", "--root", root)
	return
}

func (s *ciRehearsal) preflightSubcommandPasses(require string, count int) error {
	stdout, stderr, got, ghOut, err := s.runPreflightSubcommand(require)
	if err != nil {
		return err
	}
	if got != 0 {
		return fmt.Errorf("preflight subcommand exited %d, want 0:\n%s%s", got, stdout, stderr)
	}
	if want := fmt.Sprintf("harness-gate preflight: passed; %d required scenarios", count); !strings.Contains(stdout, want) {
		return fmt.Errorf("preflight subcommand output lacks %q:\n%s", want, stdout)
	}
	data, err := os.ReadFile(ghOut)
	if err != nil {
		return fmt.Errorf("passing preflight wrote no GITHUB_OUTPUT: %w", err)
	}
	if want := fmt.Sprintf("count=%d\n", count); !strings.HasPrefix(string(data), want) {
		return fmt.Errorf("GITHUB_OUTPUT = %q, want it to start with %q", data, want)
	}
	return nil
}

func (s *ciRehearsal) preflightSubcommand(require string, code int, name string) error {
	stdout, stderr, got, ghOut, err := s.runPreflightSubcommand(require)
	if err != nil {
		return err
	}
	if got != code {
		return fmt.Errorf("preflight subcommand exited %d, want %d:\n%s%s", got, code, stdout, stderr)
	}
	if !strings.Contains(stdout, name) || !strings.Contains(stdout, "no scenario is started") {
		return fmt.Errorf("preflight subcommand output does not name %s and say no scenario is started:\n%s", name, stdout)
	}
	if _, err := os.Stat(ghOut); !errors.Is(err, fs.ErrNotExist) {
		data, _ := os.ReadFile(ghOut)
		return fmt.Errorf("failed preflight wrote a scenario plan to GITHUB_OUTPUT (%v):\n%s", err, data)
	}
	return nil
}

// --- Release rehearsal ------------------------------------------------------------

func (s *ciRehearsal) rehearsalEnvironment() error {
	// The gate runs every child on the go floor and fails setup when "go version" is
	// not the floor (M1-G-TOOL-14). Offline, Go cannot fetch or verify a toolchain,
	// so the floor is resolved here, once, with the normal environment, and the
	// offline run starts with that toolchain first on PATH: its "go version" is
	// already the floor and nothing is fetched. The floor check itself stays strict.
	root, err := s.repo()
	if err != nil {
		return err
	}
	floor := "go" + modcheck.Floor
	out, errOut, code, err := crRun(root, crEnv([]string{"GOTOOLCHAIN", "GOWORK"}, "GOTOOLCHAIN="+floor, "GOWORK=off"), "go", "env", "GOROOT")
	if err != nil || code != 0 {
		return fmt.Errorf("resolve the %s toolchain (exit %d): %v\n%s%s", floor, code, err, out, errOut)
	}
	goroot := strings.TrimSpace(out)
	path := filepath.Join(goroot, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")
	// GOPROXY=off and a dead proxy make any module download or HTTP call fail; the
	// parent's CGO_ENABLED=1 and GOFLAGS=-race must not reach the binaries.
	s.rehearseEnv = crEnv(append(crGoDrop, "GOPROXY", "GOSUMDB", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "PATH", "GOROOT", "GOTOOLCHAIN"),
		"GOWORK=off", "CGO_ENABLED=1", "GOFLAGS=-race", "GOPROXY=off", "GOSUMDB=off",
		"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9",
		"PATH="+path, "GOROOT="+goroot, "GOTOOLCHAIN=local")
	return nil
}

func (s *ciRehearsal) gitTags(root string) (string, error) {
	out, stderr, code, err := crRun(root, os.Environ(), "git", "for-each-ref", "refs/tags")
	if err != nil {
		return "", err
	}
	if code != 0 {
		return "", fmt.Errorf("git for-each-ref refs/tags exited %d: %s", code, stderr)
	}
	return out, nil
}

func (s *ciRehearsal) rehearseRuns() error {
	if err := crBuild(); err != nil {
		return err
	}
	root, err := s.repo()
	if err != nil {
		return err
	}
	if s.tagsBefore, err = s.gitTags(root); err != nil {
		return err
	}
	if s.outDir, err = s.tempDir(); err != nil {
		return err
	}
	s.stdout, s.stderr, s.exitCode, err = crRun(root, s.rehearseEnv, crGateBin, "rehearse", "--root", root, "--out", s.outDir)
	return err
}

func (s *ciRehearsal) rehearsalExits0(text string) error {
	if s.exitCode != 0 {
		return fmt.Errorf("rehearse exited %d:\n%s%s", s.exitCode, s.stdout, s.stderr)
	}
	if !strings.Contains(s.stdout, "rehearse: 6 binaries, "+text) {
		return fmt.Errorf("rehearse output lacks %q:\n%s", "rehearse: 6 binaries, "+text, s.stdout)
	}
	return nil
}

func (s *ciRehearsal) sixBinaries() error {
	entries, err := os.ReadDir(s.outDir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		// dist/ is goreleaser's snapshot (the archives and checksums.txt the binaries
		// were extracted from and verified against; plan 19.1.1).
		if e.IsDir() && e.Name() == "dist" {
			continue
		}
		names = append(names, e.Name())
	}
	var want []string
	for n := range crRehearsalBinaries {
		want = append(want, n)
	}
	slices.Sort(names)
	slices.Sort(want)
	if !slices.Equal(names, want) {
		return fmt.Errorf("rehearsal directory holds %v, want exactly %v", names, want)
	}
	return nil
}

func (s *ciRehearsal) buildInfoPureGo() error {
	var errs []error
	for name, target := range crRehearsalBinaries {
		info, err := buildinfo.ReadFile(filepath.Join(s.outDir, name))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
			continue
		}
		set := map[string]string{}
		for _, kv := range info.Settings {
			set[kv.Key] = kv.Value
		}
		want := map[string]string{"CGO_ENABLED": "0", "-trimpath": "true", "GOOS": target[0], "GOARCH": target[1]}
		for k, v := range want {
			if set[k] != v {
				errs = append(errs, fmt.Errorf("%s: build setting %s=%q, want %q", name, k, set[k], v))
			}
		}
		if strings.Contains(set["-tags"], "race") || set["-race"] == "true" {
			errs = append(errs, fmt.Errorf("%s: parent GOFLAGS=-race leaked into the build: %v", name, set))
		}
		if info.Path != "github.com/smartpcr/agent-plugin/src/harness/cmd/dsh" {
			errs = append(errs, fmt.Errorf("%s: main package is %s, want cmd/dsh of src/harness", name, info.Path))
		}
	}
	return errors.Join(errs...)
}

var crShaLine = regexp.MustCompile(`(?m)^([0-9a-f]{64})  (\S+)  version=\S+ vcs\.revision=\S+ vcs\.modified=\S+\r?$`)

func (s *ciRehearsal) shaMatches() error {
	printed := map[string]string{}
	for _, m := range crShaLine.FindAllStringSubmatch(s.stdout, -1) {
		printed[m[2]] = m[1]
	}
	var errs []error
	for name := range crRehearsalBinaries {
		data, err := os.ReadFile(filepath.Join(s.outDir, name))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); printed[name] != got {
			errs = append(errs, fmt.Errorf("%s: printed SHA-256 %q, content hashes to %s", name, printed[name], got))
		}
	}
	if len(printed) != len(crRehearsalBinaries) {
		errs = append(errs, fmt.Errorf("rehearse printed %d SHA-256 lines, want %d:\n%s", len(printed), len(crRehearsalBinaries), s.stdout))
	}
	return errors.Join(errs...)
}

func (s *ciRehearsal) tagsUnchanged() error {
	after, err := s.gitTags(s.root)
	if err != nil {
		return err
	}
	if after != s.tagsBefore {
		return fmt.Errorf("rehearsal changed the repository's tags:\nbefore:\n%s\nafter:\n%s", s.tagsBefore, after)
	}
	return nil
}

// --- Gate on both hosted CI operating systems --------------------------------------

func (s *ciRehearsal) storeBranchAtClosingCommit() error {
	root, err := s.repo()
	if err != nil {
		return err
	}
	out, stderr, code, err := crRun(root, os.Environ(), "git", "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git rev-parse HEAD exited %d: %s", code, stderr)
	}
	s.head = strings.TrimSpace(out)
	for _, rel := range []string{
		".github/workflows/ci.yml", ".github/workflows/harness-scenarios.yml",
		"tools/harness-gate/rehearse.go", "tools/harness-gate/acceptance/scenarios.go",
		"docs/stories/harness-DSH-M1/evidence/DH-S-1.md",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("HEAD %s is not at the stage-closing commit: %s missing", s.head, rel)
		}
	}
	sec, err := s.stage14Evidence()
	if err != nil {
		return err
	}
	row := regexp.MustCompile(`(?m)^\| Source and build identity \| .*$`).FindString(sec)
	m := regexp.MustCompile("Commit `([0-9a-f]{40})`").FindStringSubmatch(row)
	if m == nil {
		return fmt.Errorf("Source and build identity row names no 40-hex commit: %q", row)
	}
	s.gated = m[1]
	env := crEnv([]string{"GIT_DIR", "GIT_WORK_TREE"})
	if _, stderr, code, err := crRun(root, env, "git", "cat-file", "-e", s.gated+"^{commit}"); err != nil {
		return err
	} else if code != 0 {
		return fmt.Errorf("recorded commit %s is not in this checkout (fetch full history): %s", s.gated, stderr)
	}
	// The gated code must be identical to HEAD's; this e2e-tagged suite postdates
	// the recorded run and is not built or run by any gate, so it is excluded.
	out, stderr, code, err = crRun(root, env, "git", "diff", "--stat", s.gated, "HEAD", "--",
		"src", "tools", ".github", ":(exclude)tools/harness-gate/test/e2e")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("git diff %s HEAD exited %d: %s", s.gated, code, stderr)
	}
	if strings.TrimSpace(out) != "" {
		return fmt.Errorf("HEAD %s differs from the gated commit %s under src, tools, .github:\n%s", s.head, s.gated, out)
	}
	return nil
}

// stage14Evidence returns the Stage 1.4 section of the DH-S-1 evidence record.
func (s *ciRehearsal) stage14Evidence() (string, error) {
	root, err := s.repo()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "docs", "stories", "harness-DSH-M1", "evidence", "DH-S-1.md"))
	if err != nil {
		return "", err
	}
	doc := strings.ReplaceAll(string(data), "\r\n", "\n")
	i := strings.Index(doc, "## Stage 1.4")
	if i < 0 {
		return "", errors.New("DH-S-1 evidence record has no Stage 1.4 section")
	}
	sec := doc[i:]
	if j := strings.Index(sec[1:], "\n## "); j >= 0 {
		sec = sec[:j+1]
	}
	return sec, nil
}

// crJob returns the body of the top-level job name of a workflow file.
func crJob(workflow, name string) (string, bool) {
	workflow = strings.ReplaceAll(workflow, "\r\n", "\n")
	start := strings.Index(workflow, "\n  "+name+":\n")
	if start < 0 {
		return "", false
	}
	body := workflow[start+1:]
	next := regexp.MustCompile(`\n  [A-Za-z0-9_-]+:\n`).FindStringIndex(body[1:])
	if next != nil {
		body = body[:next[0]+1]
	}
	return body, true
}

func (s *ciRehearsal) ciHarnessJob() error {
	data, err := os.ReadFile(filepath.Join(s.root, ".github", "workflows", "ci.yml"))
	if err != nil {
		return err
	}
	job, ok := crJob(string(data), "harness")
	if !ok {
		return errors.New("ci.yml has no harness job")
	}
	var errs []error
	for _, want := range []string{
		"os: [" + strings.Join(crLanes, ", ") + "]",
		"runs-on: ${{ matrix.os }}",
		`CGO_ENABLED: "1"`,
		"GOWORK=off go -C tools/harness-gate run . --root ../..",
	} {
		if !strings.Contains(job, want) {
			errs = append(errs, fmt.Errorf("harness job of ci.yml lacks %q", want))
		}
	}
	if regexp.MustCompile(`(?i)secrets\.`).MatchString(job) {
		errs = append(errs, errors.New("harness job of ci.yml reads a secret; it must be keyless"))
	}
	rehearse := regexp.MustCompile(`if: runner\.os == 'Linux'\n\s+run: \|\n(?:.*\n)*?.*run \. rehearse --root \.\./\.\.`)
	if !rehearse.MatchString(job) {
		errs = append(errs, errors.New("harness job of ci.yml does not run the rehearsal on the Linux lane only"))
	}
	return errors.Join(errs...)
}

func (s *ciRehearsal) cCompiler() error {
	out, stderr, code, err := crRun(s.root, crEnv(crGoDrop, "CGO_ENABLED=1"), "go", "env", "CC")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("go env CC exited %d: %s", code, stderr)
	}
	cc := strings.TrimSpace(out)
	if fields := strings.Fields(cc); len(fields) > 0 {
		cc = fields[0]
	}
	if _, err := exec.LookPath(cc); err != nil {
		return fmt.Errorf("no C compiler %q on PATH: the -race test gate cannot run on this lane: %w", cc, err)
	}
	return nil
}

func (s *ciRehearsal) gateRunsOnThisLane() error {
	if err := crBuild(); err != nil {
		return err
	}
	var err error
	s.stdout, s.stderr, s.exitCode, err = crRun(s.root, crEnv(crGoDrop, "GOWORK=off", "CGO_ENABLED=1"), crGateBin, "--root", s.root)
	return err
}

func (s *ciRehearsal) laneEndsAllPassed() error {
	if s.exitCode != 0 {
		return fmt.Errorf("gate exited %d on this lane:\n%s%s", s.exitCode, crTail(s.stdout, 60), s.stderr)
	}
	lines := strings.Split(strings.TrimRight(strings.ReplaceAll(s.stdout, "\r\n", "\n"), "\n"), "\n")
	want := fmt.Sprintf("harness-gate: PASSED all %d gates", len(gateOrder))
	if last := lines[len(lines)-1]; last != want {
		return fmt.Errorf("gate output ends with %q, want %q", last, want)
	}
	var errs []error
	for _, g := range gateOrder {
		if !strings.Contains(s.stdout, "harness-gate: PASS "+g+" (") {
			errs = append(errs, fmt.Errorf("gate %s has no PASS line", g))
		}
	}
	return errors.Join(errs...)
}

func crTail(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (s *ciRehearsal) evidenceRecordsLanes() error {
	sec, err := s.stage14Evidence()
	if err != nil {
		return err
	}
	var errs []error
	runRow := regexp.MustCompile(`(?m)^\| Run identifiers \| .*$`).FindString(sec)
	run := regexp.MustCompile(`actions/runs/(\d+)\)`).FindStringSubmatch(runRow)
	if run == nil {
		errs = append(errs, fmt.Errorf("Run identifiers row names no CI run id: %q", runRow))
	}
	if s.gated != "" && !strings.Contains(runRow, "at `"+s.gated[:7]+"`") {
		errs = append(errs, fmt.Errorf("Run identifiers row does not name the gated commit %s: %q", s.gated[:7], runRow))
	}
	jobIDs := map[string]int64{}
	for _, lane := range crLanes {
		m := regexp.MustCompile("`harness \\(" + regexp.QuoteMeta(lane) + "\\)` (\\d+)").FindStringSubmatch(runRow)
		if m == nil {
			errs = append(errs, fmt.Errorf("Run identifiers row names no job id for harness (%s)", lane))
		} else {
			var id int64
			fmt.Sscan(m[1], &id)
			jobIDs["harness ("+lane+")"] = id
		}
		if !regexp.MustCompile(`(?m)^\| ` + "`harness \\(" + regexp.QuoteMeta(lane) + "\\)`" + ` \| pass, all 8 gates \|$`).MatchString(sec) {
			errs = append(errs, fmt.Errorf("lane table records no pass of all 8 gates for harness (%s)", lane))
		}
	}
	if !strings.Contains(runRow, "all success") {
		errs = append(errs, fmt.Errorf("Run identifiers row does not record all lanes successful: %q", runRow))
	}
	if !strings.Contains(sec, "harness-gate: PASSED all 8 gates\n```") {
		errs = append(errs, errors.New("recorded lane output does not end with every gate passed"))
	}
	if run != nil && len(jobIDs) == len(crLanes) {
		errs = append(errs, s.recordedRunMatches(run[1], jobIDs))
	}
	return errors.Join(errs...)
}

type crGHJob struct {
	DatabaseID int64  `json:"databaseId"`
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

// recordedRunMatches confirms through the GitHub API, when a token is supplied,
// that the recorded run ran on the gated commit and each recorded harness job
// succeeded. Without a token only the offline commit identity check applies.
func (s *ciRehearsal) recordedRunMatches(runID string, jobIDs map[string]int64) error {
	if os.Getenv("GH_TOKEN") == "" && os.Getenv("GITHUB_TOKEN") == "" {
		return nil
	}
	if _, err := exec.LookPath("gh"); err != nil {
		return fmt.Errorf("GH_TOKEN is set but gh is not on PATH: %w", err)
	}
	out, stderr, code, err := crRun(s.root, os.Environ(), "gh", "run", "view", runID,
		"-R", "smartpcr/agent-plugin", "--json", "headSha,conclusion,jobs")
	if err != nil {
		return err
	}
	if code != 0 {
		return fmt.Errorf("gh run view %s exited %d: %s", runID, code, stderr)
	}
	var got struct {
		HeadSha    string    `json:"headSha"`
		Conclusion string    `json:"conclusion"`
		Jobs       []crGHJob `json:"jobs"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		return fmt.Errorf("gh run view %s: %w", runID, err)
	}
	var errs []error
	if got.HeadSha != s.gated {
		errs = append(errs, fmt.Errorf("run %s ran on %s, evidence records %s", runID, got.HeadSha, s.gated))
	}
	if got.Conclusion != "success" {
		errs = append(errs, fmt.Errorf("run %s concluded %q, want success", runID, got.Conclusion))
	}
	for name, id := range jobIDs {
		i := slices.IndexFunc(got.Jobs, func(j crGHJob) bool {
			return j.DatabaseID == id
		})
		switch {
		case i < 0:
			errs = append(errs, fmt.Errorf("run %s has no job %d (%s)", runID, id, name))
		case got.Jobs[i].Name != name:
			errs = append(errs, fmt.Errorf("job %d is %q, evidence records %q", id, got.Jobs[i].Name, name))
		case got.Jobs[i].Conclusion != "success":
			errs = append(errs, fmt.Errorf("job %d (%s) concluded %q, want success", id, name, got.Jobs[i].Conclusion))
		}
	}
	return errors.Join(errs...)
}

// --- Scenario index ---------------------------------------------------------------

var crDirective = regexp.MustCompile(`(?m)^//dsh:scenario (\S+)\s*\nfunc (Test\w+)\(`)

func (s *ciRehearsal) theCheckoutFiles(scenarioFile, index string) error {
	root, err := s.repo()
	if err != nil {
		return err
	}
	for _, rel := range []string{scenarioFile, index} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			return fmt.Errorf("%s not in the checkout: %w", rel, err)
		}
	}
	// Scan the authored scenarios independently of the gate's scanner.
	s.authored, s.authoredFn = map[string][]string{}, map[string]string{}
	for _, mod := range crProductModules {
		base := filepath.Join(root, filepath.FromSlash(mod))
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p != base && (d.Name() == "testdata" || d.Name() == "vendor" || strings.HasPrefix(d.Name(), ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), "_scenario_test.go") {
				return nil
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			src := strings.ReplaceAll(string(data), "\r\n", "\n")
			if !strings.HasPrefix(src, "//go:build scenario\n") {
				return fmt.Errorf("%s does not start with //go:build scenario", p)
			}
			rel, _ := filepath.Rel(root, p)
			ms := crDirective.FindAllStringSubmatch(src, -1)
			if len(ms) == 0 {
				return fmt.Errorf("%s has no //dsh:scenario directive on a TestXxx function", rel)
			}
			for _, m := range ms {
				s.authored[m[1]] = append(s.authored[m[1]], filepath.ToSlash(rel))
				s.authoredFn[m[1]] = m[2]
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if got := s.authored["DH-E-TOOLCHAIN-2"]; !slices.Equal(got, []string{scenarioFile}) {
		return fmt.Errorf("DH-E-TOOLCHAIN-2 authored in %v, want exactly %s", got, scenarioFile)
	}
	if s.index, err = acceptance.ReadIndex(root); err != nil {
		return err
	}
	return nil
}

func (s *ciRehearsal) scenarioIndexGateRuns() error {
	if err := crBuild(); err != nil {
		return err
	}
	var err error
	s.stdout, s.stderr, s.exitCode, err = crRun(s.root, crEnv(crGoDrop, "GOWORK=off"), crDriverBin, "scenario-index", s.root)
	return err
}

func (s *ciRehearsal) vetRuns(command string) error {
	args := strings.Fields(command)
	if len(args) < 2 || args[0] != "go" {
		return fmt.Errorf("unexpected command %q", command)
	}
	out, errOut, code, err := crRun(s.root, crEnv(crGoDrop, "GOWORK=off"), "go", args[1:]...)
	s.vetOut, s.vetCode = out+errOut, code
	return err
}

func (s *ciRehearsal) scenarioIndexPasses() error {
	if s.exitCode != 0 || !strings.Contains(s.stdout, "harness-gate: PASS scenario-index") {
		return fmt.Errorf("scenario-index gate exited %d:\n%s%s", s.exitCode, s.stdout, s.stderr)
	}
	return nil
}

func (s *ciRehearsal) exactlyOneIndexEntry() error {
	raw, err := os.ReadFile(filepath.Join(s.root, filepath.FromSlash(acceptance.IndexPath)))
	if err != nil {
		return err
	}
	var errs []error
	for id, files := range s.authored {
		if len(files) != 1 {
			errs = append(errs, fmt.Errorf("%s is authored %d times: %v", id, len(files), files))
		}
		var stages []string
		for _, st := range s.index.Stages {
			for _, e := range st.Scenarios {
				if e.ID == id {
					stages = append(stages, st.ID)
				}
			}
		}
		lines := regexp.MustCompile(`(?m)^\s*- id: `+regexp.QuoteMeta(id)+`\s*$`).FindAllString(string(raw), -1)
		if len(stages) != 1 || len(lines) != 1 {
			errs = append(errs, fmt.Errorf("%s (%v) appears in %d index entries %v (%d raw lines), want exactly one", id, files, len(stages), stages, len(lines)))
		}
	}
	return errors.Join(errs...)
}

func (s *ciRehearsal) indexedUnder(id, stage string) error {
	st, ok := s.index.Stage(stage)
	if !ok {
		return fmt.Errorf("index has no stage %s", stage)
	}
	if !slices.ContainsFunc(st.Scenarios, func(e acceptance.Entry) bool { return e.ID == id }) {
		return fmt.Errorf("%s is not indexed under %s: %v", id, stage, st.Scenarios)
	}
	return nil
}

func (s *ciRehearsal) builtThroughHaveFiles() error {
	built, ok := acceptance.StageNumber(s.index.BuiltThrough)
	if !ok {
		return fmt.Errorf("built-through %q is not a stage", s.index.BuiltThrough)
	}
	var errs []error
	checked := 0
	for _, st := range s.index.Stages {
		for _, e := range st.Scenarios {
			n, _ := acceptance.StageNumber(e.Authored)
			if n > built {
				continue
			}
			checked++
			if len(s.authored[e.ID]) == 0 {
				errs = append(errs, fmt.Errorf("%s (under %s, authored %s, built through %s) has no scenario file", e.ID, st.ID, e.Authored, s.index.BuiltThrough))
			}
		}
	}
	if checked == 0 {
		errs = append(errs, fmt.Errorf("no index ID is authored at or before %s; the check proves nothing", s.index.BuiltThrough))
	}
	return errors.Join(errs...)
}

func (s *ciRehearsal) vetExits0() error {
	if s.vetCode != 0 {
		return fmt.Errorf("go vet -tags scenario exited %d:\n%s", s.vetCode, s.vetOut)
	}
	return nil
}

// crList runs go test -list over every package of src/harness with tags, as the gate does.
func (s *ciRehearsal) crList(tags string) (map[string]bool, error) {
	out, errOut, code, err := crRun(filepath.Join(s.root, "src", "harness"), crEnv(crGoDrop, "GOWORK=off"),
		"go", "test", tags, "-list", ".", "./...")
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("go test %s -list exited %d:\n%s%s", tags, code, out, errOut)
	}
	listed := map[string]bool{}
	for _, l := range strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n") {
		listed[strings.TrimSpace(l)] = true
	}
	return listed, nil
}

func (s *ciRehearsal) defaultListsNoScenario() error {
	listed, err := s.crList("-tags=")
	if err != nil {
		return err
	}
	var leaked []string
	for id, fn := range s.authoredFn {
		if listed[fn] {
			leaked = append(leaked, id+" "+fn)
		}
	}
	if len(leaked) > 0 {
		return fmt.Errorf("the default gate test run lists scenario tests: %v", leaked)
	}
	return nil
}

func (s *ciRehearsal) taggedLists(tags, test string) error {
	listed, err := s.crList(tags)
	if err != nil {
		return err
	}
	if !listed[test] {
		return fmt.Errorf("go test %s -list does not list %s", tags, test)
	}
	return nil
}

// InitializeScenario_repository_modules_and_quality_gates_ci_and_release_rehearsal
// registers the steps of Stage 1.4 (CI and release rehearsal).
func InitializeScenario_repository_modules_and_quality_gates_ci_and_release_rehearsal(ctx *godog.ScenarioContext) {
	s := &ciRehearsal{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.cleanup()
		return c, err
	})

	ctx.Step(`^an in-memory acceptance index with "([^"]*)" under "([^"]*)" authored at "([^"]*)" and its scenario test$`, s.anInMemoryIndex)
	ctx.Step(`^an injected environment map with no model variables and DSH_REQUIRE_SCENARIOS "([^"]*)"$`, s.envWithoutModelVars)
	ctx.Step(`^the preflight decision function runs$`, s.preflightRuns)
	ctx.Step(`^it returns a failure naming "([^"]*)"$`, s.preflightFailsNaming)
	ctx.Step(`^no scenario is started$`, s.noScenarioStarted)
	ctx.Step(`^DSH_REQUIRE_SCENARIOS is empty and the preflight decision function runs$`, s.emptyRequiredRuns)
	ctx.Step(`^the preflight passes with an empty plan$`, s.preflightPassesEmpty)
	ctx.Step(`^the preflight subcommand on the repository with no model variables and DSH_REQUIRE_SCENARIOS "([^"]*)" exits (\d+) naming "([^"]*)" and writes no scenario plan$`, s.preflightSubcommand)
	ctx.Step(`^the preflight subcommand on the repository with no model variables and DSH_REQUIRE_SCENARIOS "([^"]*)" exits 0 and plans (\d+) scenarios$`, s.preflightSubcommandPasses)

	ctx.Step(`^GOWORK=off, a parent environment with CGO_ENABLED=1 and GOFLAGS=-race, and no network$`, s.rehearsalEnvironment)
	ctx.Step(`^harness-gate rehearse --out a temporary directory runs on the repository$`, s.rehearseRuns)
	ctx.Step(`^the rehearsal exits 0 and reports "([^"]*)"$`, s.rehearsalExits0)
	ctx.Step(`^the temporary directory holds exactly the six binaries for linux, darwin, windows on amd64 and arm64$`, s.sixBinaries)
	ctx.Step(`^each binary's embedded build info records CGO_ENABLED=0, -trimpath and its own GOOS and GOARCH$`, s.buildInfoPureGo)
	ctx.Step(`^the printed SHA-256 of each binary matches its content$`, s.shaMatches)
	ctx.Step(`^the repository's tags are unchanged$`, s.tagsUnchanged)

	ctx.Step(`^the story branch at the stage-closing commit$`, s.storeBranchAtClosingCommit)
	ctx.Step(`^the harness job of ci\.yml runs the gate on ubuntu-latest and windows-latest with CGO_ENABLED=1$`, s.ciHarnessJob)
	ctx.Step(`^a C compiler is available for -race on this host's lane$`, s.cCompiler)
	ctx.Step(`^the harness job's gate runs on this host's lane$`, s.gateRunsOnThisLane)
	ctx.Step(`^this lane's gate output ends with every gate passed$`, s.laneEndsAllPassed)
	ctx.Step(`^the DH-S-1 evidence record names the run id and a passing result for each of ubuntu-latest and windows-latest$`, s.evidenceRecordsLanes)

	ctx.Step(`^"([^"]*)" and "([^"]*)" in the checkout$`, s.theCheckoutFiles)
	ctx.Step(`^the gate's scenario-index check runs on the repository$`, s.scenarioIndexGateRuns)
	ctx.Step(`^"(go -C src/harness vet -tags scenario \./\.\.\.)" runs$`, s.vetRuns)
	ctx.Step(`^the scenario-index check passes$`, s.scenarioIndexPasses)
	ctx.Step(`^every \*_scenario_test\.go ID appears in exactly one index entry$`, s.exactlyOneIndexEntry)
	ctx.Step(`^"([^"]*)" is indexed under "([^"]*)"$`, s.indexedUnder)
	ctx.Step(`^every index ID authored at or before the built-through stage has a scenario file$`, s.builtThroughHaveFiles)
	ctx.Step(`^the vet exits 0$`, s.vetExits0)
	ctx.Step(`^the default gate go test run lists zero scenario tests$`, s.defaultListsNoScenario)
	ctx.Step(`^a "([^"]*)" listing does list "([^"]*)"$`, func(tags, test string) error {
		return s.taggedLists(strings.ReplaceAll(tags, " ", "="), test)
	})
}

func TestE2E_repository_modules_and_quality_gates_ci_and_release_rehearsal(t *testing.T) {
	t.Cleanup(func() {
		if crBuildDir != "" {
			os.RemoveAll(crBuildDir)
		}
	})
	suite := godog.TestSuite{
		Name:                "repository_modules_and_quality_gates_ci_and_release_rehearsal",
		ScenarioInitializer: InitializeScenario_repository_modules_and_quality_gates_ci_and_release_rehearsal,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"repository_modules_and_quality_gates_ci_and_release_rehearsal.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
