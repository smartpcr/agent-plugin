//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/analyzers"
)

// gateOrder is the fixed gate list of harness-gate (QG-1), used to prove that no
// gate after the failing one started.
var gateOrder = []string{"gofmt", "vet", "test", "coverage", "trace", "scenario-index", "budget", "cross-compile"}

// driverSource replaces main.go of package main through "go build -overlay", so
// the in-process scenarios (TC-01a) call the real Run, ExitCode, ParseProfile,
// Coverage and CheckFloor of runner.go and coverage.go, unchanged, with fake gate
// funcs and in-memory profiles.
const driverSource = `package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

func main() {
	switch os.Args[1] {
	case "runner":
		failing := os.Args[3]
		var gates []Gate
		for _, name := range strings.Split(os.Args[2], ",") {
			gates = append(gates, Gate{Name: name, Title: "e2e fake gate " + name, Run: func(ctx context.Context, w io.Writer) error {
				fmt.Fprintf(w, "e2e: ran %s\n", name)
				if name == failing {
					return errors.New("e2e: seeded failure in " + name)
				}
				return nil
			}})
		}
		os.Exit(ExitCode(Run(context.Background(), os.Stdout, gates)))
	case "coverage":
		var profiles []*Profile
		for _, path := range os.Args[2:] {
			f, err := os.Open(path)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			p, err := ParseProfile(f)
			f.Close()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			profiles = append(profiles, p)
		}
		cov := Coverage(profiles...)
		for _, c := range cov {
			fmt.Printf("e2e: package %s %d/%d %s%%\n", c.Package, c.Covered, c.Total, formatPercent(c.Percent()))
		}
		if err := CheckFloor(cov, CoverageFloor); err != nil {
			fmt.Printf("e2e: floor FAIL\n%v\n", err)
			os.Exit(1)
		}
		fmt.Println("e2e: floor PASS")
	default:
		fmt.Fprintln(os.Stderr, "unknown mode", os.Args[1])
		os.Exit(2)
	}
}
`

// binaries are built once per suite run from the module under test.
var (
	buildOnce sync.Once
	buildDir  string
	gateBin   string
	driverBin string
	buildErr  error
)

func exe(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// moduleRoot is tools/harness-gate, seen from tools/harness-gate/test/e2e/harness-DSH.
func moduleRoot() (string, error) {
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	if !bytes.Contains(data, []byte("module github.com/smartpcr/agent-plugin/tools/harness-gate\n")) {
		return "", fmt.Errorf("%s is not the harness-gate module", root)
	}
	return root, nil
}

func goEnv() []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		return strings.HasPrefix(strings.ToUpper(kv), "GOWORK=")
	})
	return append(env, "GOWORK=off")
}

func buildBinaries() error {
	buildOnce.Do(func() {
		root, err := moduleRoot()
		if err != nil {
			buildErr = err
			return
		}
		buildDir, err = os.MkdirTemp("", "harness-gate-e2e-")
		if err != nil {
			buildErr = err
			return
		}
		gateBin = filepath.Join(buildDir, exe("harness-gate"))
		cmd := exec.Command("go", "build", "-o", gateBin, ".")
		cmd.Dir, cmd.Env = root, goEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build harness-gate: %v\n%s", err, out)
			return
		}

		driverSrc := filepath.Join(buildDir, "driver.go")
		if err := os.WriteFile(driverSrc, []byte(driverSource), 0o644); err != nil {
			buildErr = err
			return
		}
		overlay, err := json.Marshal(map[string]map[string]string{
			"Replace": {filepath.Join(root, "main.go"): driverSrc},
		})
		if err != nil {
			buildErr = err
			return
		}
		overlayPath := filepath.Join(buildDir, "overlay.json")
		if err := os.WriteFile(overlayPath, overlay, 0o644); err != nil {
			buildErr = err
			return
		}
		driverBin = filepath.Join(buildDir, exe("gate-driver"))
		cmd = exec.Command("go", "build", "-overlay", overlayPath, "-o", driverBin, ".")
		cmd.Dir, cmd.Env = root, goEnv()
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = fmt.Errorf("build in-process driver: %v\n%s", err, out)
		}
	})
	return buildErr
}

// runProcess runs bin with args and returns its stdout and exit code.
func runProcess(bin string, args ...string) (string, string, int, error) {
	cmd := exec.Command(bin, args...)
	cmd.Env = os.Environ()
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
		return "", "", 0, fmt.Errorf("run %s: %w\n%s", bin, err, stderr.String())
	}
}

// errRecorder is the analysistest.Testing the fixture scenario hands in, so a
// missing or unexpected diagnostic becomes a step failure.
type errRecorder struct{ errs []string }

func (r *errRecorder) Errorf(format string, args ...any) {
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

type gateRunnerCore struct {
	tempDirs []string

	// last process run
	stdout, stderr string
	exitCode       int

	// first failing gate scenario
	gateNames []string
	failing   string

	// coverage scenario
	pkg           string
	parentProfile string
	childProfile  string

	// nosleep scenario
	fset     *token.FileSet
	file     *ast.File
	filename string
	findings []analyzers.Finding

	// seeded module scenarios
	scratch string

	// analyzer fixtures scenario
	fixtures string
	recorder *errRecorder
	reported map[string]int
}

func (s *gateRunnerCore) tempDir() (string, error) {
	dir, err := os.MkdirTemp("", "harness-gate-e2e-scenario-")
	if err != nil {
		return "", err
	}
	s.tempDirs = append(s.tempDirs, dir)
	return dir, nil
}

func (s *gateRunnerCore) run(bin string, args ...string) error {
	out, errOut, code, err := runProcess(bin, args...)
	if err != nil {
		return err
	}
	s.stdout, s.stderr, s.exitCode = out, errOut, code
	return nil
}

// --- First failing gate is named ---

func (s *gateRunnerCore) anInMemoryGateList(list, failing string) error {
	s.gateNames = strings.Split(list, ",")
	if !slices.Contains(s.gateNames, failing) {
		return fmt.Errorf("failing gate %q is not in %q", failing, list)
	}
	if slices.Index(s.gateNames, failing) != 2 {
		return fmt.Errorf("failing gate %q is not the third gate", failing)
	}
	s.failing = failing
	return nil
}

func (s *gateRunnerCore) theRunnerExecutes() error {
	if err := buildBinaries(); err != nil {
		return err
	}
	return s.run(driverBin, "runner", strings.Join(s.gateNames, ","), s.failing)
}

func (s *gateRunnerCore) gatesRan(list string) error {
	for _, name := range strings.Split(list, ",") {
		if !strings.Contains(s.stdout, "e2e: ran "+name+"\n") {
			return fmt.Errorf("gate %q did not run:\n%s", name, s.stdout)
		}
	}
	return nil
}

func (s *gateRunnerCore) gatesNeverRan(list string) error {
	for _, name := range strings.Split(list, ",") {
		if strings.Contains(s.stdout, "e2e: ran "+name+"\n") {
			return fmt.Errorf("gate %q ran after the failure:\n%s", name, s.stdout)
		}
		if strings.Contains(s.stdout, " "+name+" -- ") {
			return fmt.Errorf("gate %q was started after the failure:\n%s", name, s.stdout)
		}
	}
	if strings.Contains(s.stdout, "PASSED all") {
		return fmt.Errorf("runner reported every gate passed:\n%s", s.stdout)
	}
	return nil
}

func (s *gateRunnerCore) stdoutNamesGate(name string) error {
	if !strings.Contains(s.stdout, "harness-gate: FAIL "+name+" (") {
		return fmt.Errorf("stdout has no FAIL line for %q:\n%s", name, s.stdout)
	}
	if !strings.Contains(s.stdout, "harness-gate: FAILED at gate "+name+"\n") {
		return fmt.Errorf("stdout does not name %q as the failed gate:\n%s", name, s.stdout)
	}
	return nil
}

func (s *gateRunnerCore) theExitCodeIsNonZero() error {
	if s.exitCode == 0 {
		return fmt.Errorf("exit code = 0, want non-zero\nstdout:\n%s\nstderr:\n%s", s.stdout, s.stderr)
	}
	return nil
}

// --- Coverage floor arithmetic over merged profiles ---

// profile renders a set-mode profile of total one-statement blocks of pkg/p.go;
// block i (1-based) is covered when covered(i).
func profile(pkg string, total int, covered func(int) bool) string {
	var b strings.Builder
	b.WriteString("mode: set\n")
	for i := 1; i <= total; i++ {
		count := 0
		if covered(i) {
			count = 1
		}
		fmt.Fprintf(&b, "%s/p.go:%d.2,%d.20 1 %d\n", pkg, i+2, i+2, count)
	}
	return b.String()
}

const profileStatements = 50

func (s *gateRunnerCore) aParentCoverageProfile(pkg string, covered, total int) error {
	if total != profileStatements {
		return fmt.Errorf("scenario uses %d statements, got %d", profileStatements, total)
	}
	s.pkg = pkg
	s.parentProfile = profile(pkg, total, func(i int) bool { return i <= covered })
	return nil
}

func (s *gateRunnerCore) aChildProfileCovering(further int) error {
	parentCovered := strings.Count(s.parentProfile, " 1 1\n")
	s.childProfile = profile(s.pkg, profileStatements, func(i int) bool {
		return i > parentCovered && i <= parentCovered+further
	})
	return nil
}

func (s *gateRunnerCore) floorCheck(profiles ...string) error {
	if err := buildBinaries(); err != nil {
		return err
	}
	dir, err := s.tempDir()
	if err != nil {
		return err
	}
	var paths []string
	for i, p := range profiles {
		path := filepath.Join(dir, "profile"+strconv.Itoa(i)+".out")
		if err := os.WriteFile(path, []byte(p), 0o644); err != nil {
			return err
		}
		paths = append(paths, path)
	}
	if err := s.run(driverBin, append([]string{"coverage"}, paths...)...); err != nil {
		return err
	}
	if s.exitCode == 2 {
		return fmt.Errorf("profile parse failed:\n%s", s.stderr)
	}
	return nil
}

func (s *gateRunnerCore) theFloorCheckRunsOverBoth() error {
	return s.floorCheck(s.parentProfile, s.childProfile)
}

func (s *gateRunnerCore) theFloorCheckRunsOverParentOnly() error {
	return s.floorCheck(s.parentProfile)
}

func (s *gateRunnerCore) packageIsAtAndPasses(pkg string, percent int) error {
	want := fmt.Sprintf("e2e: package %s %d/%d %d%%\n", pkg, percent*profileStatements/100, profileStatements, percent)
	if !strings.Contains(s.stdout, want) {
		return fmt.Errorf("want %q in:\n%s", want, s.stdout)
	}
	if s.exitCode != 0 || !strings.Contains(s.stdout, "e2e: floor PASS\n") {
		return fmt.Errorf("floor check did not pass (exit %d):\n%s", s.exitCode, s.stdout)
	}
	return nil
}

func (s *gateRunnerCore) theFloorCheckFailsNaming(detail string) error {
	if s.exitCode == 0 || !strings.Contains(s.stdout, "e2e: floor FAIL\n") {
		return fmt.Errorf("floor check passed, want failure:\n%s", s.stdout)
	}
	if !strings.Contains(s.stdout, detail) {
		return fmt.Errorf("failure does not name %q:\n%s", detail, s.stdout)
	}
	return nil
}

// --- Analyzer flags sleep in a test file ---

func (s *gateRunnerCore) sourceParsed(body, filename string) error {
	src := "package x\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\n" + body + "\n"
	s.fset = token.NewFileSet()
	file, err := parser.ParseFile(s.fset, filename, src, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	s.file, s.filename = file, filename
	return nil
}

func (s *gateRunnerCore) theNoSleepPredicateRuns() error {
	s.findings = analyzers.NoSleepFindings(s.filename, s.file, nil)
	return nil
}

func (s *gateRunnerCore) exactlyOneDiagnosticAtCall() error {
	if len(s.findings) != 1 {
		return fmt.Errorf("got %d diagnostics, want exactly 1: %v", len(s.findings), s.findings)
	}
	var call *ast.CallExpr
	ast.Inspect(s.file, func(n ast.Node) bool {
		if c, ok := n.(*ast.CallExpr); ok && call == nil {
			call = c
		}
		return true
	})
	if call == nil {
		return errors.New("no call expression in the source")
	}
	if s.findings[0].Pos != call.Pos() {
		return fmt.Errorf("diagnostic at %v, want the call at %v",
			s.fset.Position(s.findings[0].Pos), s.fset.Position(call.Pos()))
	}
	if !strings.Contains(s.findings[0].Message, "time.Sleep") {
		return fmt.Errorf("diagnostic %q does not mention time.Sleep", s.findings[0].Message)
	}
	return nil
}

// --- Seeded violation modules ---

func (s *gateRunnerCore) theSeededModuleCopied(name string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	src := filepath.Join(root, "testdata", name)
	if _, err := os.Stat(filepath.Join(src, "go.mod")); err != nil {
		return fmt.Errorf("seeded module %s: %w", name, err)
	}
	dst, err := s.tempDir()
	if err != nil {
		return err
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		return err
	}
	s.scratch = dst
	return nil
}

func (s *gateRunnerCore) harnessGateRunsAgainstIt() error {
	if err := buildBinaries(); err != nil {
		return err
	}
	return s.run(gateBin, "--root", s.scratch)
}

func (s *gateRunnerCore) theRunStopsAtGate(gate string) error {
	idx := slices.Index(gateOrder, gate)
	if idx < 0 {
		return fmt.Errorf("unknown gate %q", gate)
	}
	if err := s.stdoutNamesGate(gate); err != nil {
		return err
	}
	for _, earlier := range gateOrder[:idx] {
		if !strings.Contains(s.stdout, "harness-gate: PASS "+earlier+" (") {
			return fmt.Errorf("gate %q before %q did not pass:\n%s", earlier, gate, s.stdout)
		}
	}
	for _, later := range gateOrder[idx+1:] {
		if strings.Contains(s.stdout, " "+later+" -- ") {
			return fmt.Errorf("gate %q started after %q failed:\n%s", later, gate, s.stdout)
		}
	}
	return nil
}

func (s *gateRunnerCore) theOutputMentions(detail string) error {
	if !strings.Contains(filepath.ToSlash(s.stdout), detail) {
		return fmt.Errorf("output does not mention %q:\n%s", detail, s.stdout)
	}
	return nil
}

// --- Own analyzers against fixture packages ---

func (s *gateRunnerCore) theCommittedFixtures(rel string) error {
	root, err := moduleRoot()
	if err != nil {
		return err
	}
	dir := filepath.Join(root, filepath.FromSlash(rel))
	for _, pkg := range []string{"nosleep", "onespawn", "cleanup", "internal/world/subprocess"} {
		if _, err := os.Stat(filepath.Join(dir, "src", filepath.FromSlash(pkg))); err != nil {
			return fmt.Errorf("fixture package %s: %w", pkg, err)
		}
	}
	s.fixtures = dir
	return nil
}

func (s *gateRunnerCore) analysistestRunLoadsThem() error {
	s.recorder = &errRecorder{}
	s.reported = map[string]int{}
	runs := []struct {
		a        *analysis.Analyzer
		patterns []string
	}{
		{analyzers.NoSleep, []string{"nosleep"}},
		{analyzers.OneSpawn, []string{"onespawn", "internal/world/subprocess"}},
		{analyzers.Cleanup, []string{"cleanup"}},
	}
	for _, r := range runs {
		for _, res := range analysistest.Run(s.recorder, s.fixtures, r.a, r.patterns...) {
			if res.Err != nil {
				s.recorder.Errorf("%s: %v", r.a.Name, res.Err)
			}
			s.reported[r.a.Name] += len(res.Diagnostics)
		}
	}
	return nil
}

func (s *gateRunnerCore) everyExpectedDiagnosticReported() error {
	if len(s.recorder.errs) > 0 {
		return fmt.Errorf("analysistest reported %d problem(s):\n%s", len(s.recorder.errs), strings.Join(s.recorder.errs, "\n"))
	}
	return nil
}

func (s *gateRunnerCore) atLeastOneDiagnosticEach(list string) error {
	for _, name := range strings.Split(list, ",") {
		if s.reported[name] == 0 {
			return fmt.Errorf("analyzer %q reported no diagnostic on its fixtures (%v)", name, s.reported)
		}
	}
	return nil
}

func InitializeScenario_repository_modules_and_quality_gates_gate_runner_core(ctx *godog.ScenarioContext) {
	s := &gateRunnerCore{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for _, dir := range s.tempDirs {
			os.RemoveAll(dir)
		}
		s.tempDirs = nil
		return c, err
	})

	ctx.Step(`^an in-memory gate list "([^"]*)" whose gate "([^"]*)" returns an error$`, s.anInMemoryGateList)
	ctx.Step(`^the runner executes$`, s.theRunnerExecutes)
	ctx.Step(`^gates "([^"]*)" ran$`, s.gatesRan)
	ctx.Step(`^gates "([^"]*)" never ran$`, s.gatesNeverRan)
	ctx.Step(`^stdout names gate "([^"]*)" as the failure$`, s.stdoutNamesGate)
	ctx.Step(`^the exit code is non-zero$`, s.theExitCodeIsNonZero)

	ctx.Step(`^an in-memory parent coverage profile for package "([^"]*)" covering (\d+) of (\d+) statements$`, s.aParentCoverageProfile)
	ctx.Step(`^an in-memory re-executed child profile for the same package covering (\d+) further statements$`, s.aChildProfileCovering)
	ctx.Step(`^the floor check runs over both profiles$`, s.theFloorCheckRunsOverBoth)
	ctx.Step(`^the floor check runs over the parent profile only$`, s.theFloorCheckRunsOverParentOnly)
	ctx.Step(`^package "([^"]*)" is at (\d+)% and passes the floor$`, s.packageIsAtAndPasses)
	ctx.Step(`^the floor check fails naming "([^"]*)"$`, s.theFloorCheckFailsNaming)

	ctx.Step(`^source "([^"]*)" parsed with go/parser\.ParseFile from a string as "([^"]*)"$`, s.sourceParsed)
	ctx.Step(`^the nosleep predicate runs$`, s.theNoSleepPredicateRuns)
	ctx.Step(`^exactly one diagnostic is returned at the call position$`, s.exactlyOneDiagnosticAtCall)

	ctx.Step(`^the seeded module "([^"]*)" copied into a temporary directory$`, s.theSeededModuleCopied)
	ctx.Step(`^harness-gate runs against it$`, s.harnessGateRunsAgainstIt)
	ctx.Step(`^the run stops at gate "([^"]*)" and names it$`, s.theRunStopsAtGate)
	ctx.Step(`^the output mentions "([^"]*)"$`, s.theOutputMentions)

	ctx.Step(`^the committed analyzer fixtures under "([^"]*)"$`, s.theCommittedFixtures)
	ctx.Step(`^analysistest\.Run loads them for every own analyzer$`, s.analysistestRunLoadsThem)
	ctx.Step(`^every expected diagnostic is reported and none extra$`, s.everyExpectedDiagnosticReported)
	ctx.Step(`^at least one diagnostic was reported by each of "([^"]*)"$`, s.atLeastOneDiagnosticEach)
}

func TestE2E_repository_modules_and_quality_gates_gate_runner_core(t *testing.T) {
	t.Cleanup(func() {
		if buildDir != "" {
			os.RemoveAll(buildDir)
		}
	})
	suite := godog.TestSuite{
		Name:                "repository_modules_and_quality_gates_gate_runner_core",
		ScenarioInitializer: InitializeScenario_repository_modules_and_quality_gates_gate_runner_core,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"repository_modules_and_quality_gates_gate_runner_core.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if status := suite.Run(); status != 0 {
		t.Fatalf("godog suite exited with status %d", status)
	}
}
