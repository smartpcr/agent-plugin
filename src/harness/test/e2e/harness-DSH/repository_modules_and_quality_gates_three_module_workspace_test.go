//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/cucumber/godog"
)

// rm1Modules are the three product modules of RM-1, keyed by repository-relative directory.
var rm1Modules = []struct{ Dir, Path string }{
	{"src/go", "github.com/smartpcr/agent-plugin/src/go"},
	{"src/sdk", "github.com/smartpcr/agent-plugin/src/sdk"},
	{"src/harness", "github.com/smartpcr/agent-plugin/src/harness"},
}

const goldenRel = "src/go/cmd/cordis-sample/testdata/output.golden"

// goldenReRecord is one sanctioned re-recording of goldenRel: the full commit,
// why, and the exact lines it adds (it may remove none).
type goldenReRecord struct {
	commit, reason string
	added          []string
}

// goldenReRecords are the only commits besides the introducing one allowed to
// touch goldenRel. Each is a deliberate re-record with -update (contract rule 3)
// whose intended diff is noted in the evidence; the check pins its exact lines.
var goldenReRecords = []goldenReRecord{
	{"cf6b2e72ad7b4197c09ecb7a26e47d52fd5549c1",
		"TC-02b, cf6b2e7: step 14.1.4's loader debug line, noted in evidence/DH-S-2.md",
		[]string{"[Debug] loader apply: beat2"}},
}

// cmdResult is one child process of the pinned go toolchain.
type cmdResult struct {
	Label  string
	Output string
	Err    error
}

type threeModuleWorkspace struct {
	root    string
	gowork  string // "" selects the root go.work, "off" ignores it
	tempDir string
	results []cmdResult

	// release build
	binary string

	// DH-T-TOOLCHAIN-11 probe
	replaceLine  string
	mutatedLine  int
	probeOutcome probeOutcome

	// golden
	golden     []byte
	goldenTest cmdResult
}

type probeOutcome struct {
	Committed string `json:"committed"`
	Mutated   string `json:"mutated"`
}

// repoRoot walks up from the working directory to the checkout that holds go.work.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "src", "harness", "go.mod")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no go.work found above the test directory")
		}
		dir = parent
	}
}

// goEnv is the process environment for a child go command: inherited GOWORK and
// GOFLAGS are dropped so the scenario, not the caller, decides the workspace mode.
func goEnv(gowork string, extra ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		key, _, _ := strings.Cut(kv, "=")
		key = strings.ToUpper(key)
		return key == "GOWORK" || key == "GOFLAGS"
	})
	if gowork != "" {
		env = append(env, "GOWORK="+gowork)
	}
	return append(env, extra...)
}

func (w *threeModuleWorkspace) goTool(dir, gowork string, args ...string) cmdResult {
	label := fmt.Sprintf("GOWORK=%q go %s (in %s)", gowork, strings.Join(args, " "), dir)
	gobin, err := exec.LookPath("go")
	if err != nil {
		return cmdResult{Label: label, Err: fmt.Errorf("the pinned go toolchain is required: %w", err)}
	}
	cmd := exec.Command(gobin, args...)
	cmd.Dir = dir
	cmd.Env = goEnv(gowork)
	out, err := cmd.CombinedOutput()
	return cmdResult{Label: label, Output: string(out), Err: err}
}

func (w *threeModuleWorkspace) run(dir, gowork string, args ...string) {
	w.results = append(w.results, w.goTool(dir, gowork, args...))
}

func (w *threeModuleWorkspace) modulePatterns() []string {
	var patterns []string
	for _, m := range rm1Modules {
		patterns = append(patterns, "./"+m.Dir+"/...")
	}
	return patterns
}

// ---------------------------------------------------------------- scenario 1

func (w *threeModuleWorkspace) aCleanCheckoutWithTheRootGoWork() error {
	work, err := os.ReadFile(filepath.Join(w.root, "go.work"))
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(w.root, "go.mod")); err == nil {
		return errors.New("the repository root has a go.mod; RM-1 allows only the three product modules")
	}
	var uses []string
	inUse := false
	scanner := bufio.NewScanner(bytes.NewReader(work))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		switch {
		case line == "use (":
			inUse = true
		case inUse && line == ")":
			inUse = false
		case inUse && line != "":
			uses = append(uses, strings.TrimPrefix(line, "./"))
		case strings.HasPrefix(line, "use "):
			uses = append(uses, strings.TrimPrefix(strings.TrimSpace(strings.TrimPrefix(line, "use ")), "./"))
		}
	}
	var want []string
	for _, m := range rm1Modules {
		want = append(want, m.Dir)
	}
	slices.Sort(uses)
	slices.Sort(want)
	if !slices.Equal(uses, want) {
		return fmt.Errorf("go.work uses %v, want exactly %v", uses, want)
	}
	w.gowork = ""
	return nil
}

func (w *threeModuleWorkspace) commandRunsFromTheRepositoryRoot(command string) error {
	fields := strings.Fields(command)
	if len(fields) < 2 || fields[0] != "go" {
		return fmt.Errorf("unsupported command %q", command)
	}
	if !slices.Equal(fields[2:], w.modulePatterns()) {
		return fmt.Errorf("command %q must use the module-directory patterns %v", command, w.modulePatterns())
	}
	w.run(w.root, "", fields[1:]...)
	return nil
}

func (w *threeModuleWorkspace) commandRunsInEachModuleDirectory(command string) error {
	if command != "GOWORK=off go build ./..." {
		return fmt.Errorf("unsupported command %q", command)
	}
	for _, m := range rm1Modules {
		w.run(filepath.Join(w.root, filepath.FromSlash(m.Dir)), "off", "build", "./...")
	}
	return nil
}

func (w *threeModuleWorkspace) everyCommandExits0() error {
	if len(w.results) == 0 {
		return errors.New("no command ran")
	}
	var errs []error
	for _, r := range w.results {
		if r.Err != nil {
			errs = append(errs, fmt.Errorf("%s: %v\n%s", r.Label, r.Err, r.Output))
		}
	}
	return errors.Join(errs...)
}

func (w *threeModuleWorkspace) goListMReportsExactlyTheThreeModulePaths() error {
	r := w.goTool(w.root, "", "list", "-m")
	if r.Err != nil {
		return fmt.Errorf("%s: %v\n%s", r.Label, r.Err, r.Output)
	}
	got := strings.Fields(r.Output)
	var want []string
	for _, m := range rm1Modules {
		want = append(want, m.Path)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return fmt.Errorf("go list -m = %v, want %v", got, want)
	}
	return nil
}

// ---------------------------------------------------------------- scenario 2

func (w *threeModuleWorkspace) goworkOff() error {
	w.gowork = "off"
	return nil
}

func (w *threeModuleWorkspace) goBuildRunsInSrcHarness() error {
	w.binary = filepath.Join(w.tempDir, "dsh")
	if runtime.GOOS == "windows" {
		w.binary += ".exe"
	}
	w.run(filepath.Join(w.root, "src", "harness"), w.gowork, "build", "-o", w.binary, "./cmd/dsh")
	return nil
}

func (w *threeModuleWorkspace) theBuildSucceedsWithoutAnyReplaceDirective() error {
	if w.gowork != "off" {
		return errors.New("the release build must run with GOWORK=off")
	}
	if err := w.everyCommandExits0(); err != nil {
		return err
	}
	if _, err := os.Stat(w.binary); err != nil {
		return fmt.Errorf("the build wrote no binary: %w", err)
	}
	for _, m := range rm1Modules {
		data, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(m.Dir), "go.mod"))
		if err != nil {
			return err
		}
		for i, line := range strings.Split(string(data), "\n") {
			if f := strings.Fields(line); len(f) > 0 && f[0] == "replace" {
				return fmt.Errorf("%s/go.mod:%d carries a replace directive: %s", m.Dir, i+1, strings.TrimSpace(line))
			}
		}
	}
	return nil
}

func (w *threeModuleWorkspace) theBinaryPrintsItsVersionAndExits0() error {
	out, err := exec.Command(w.binary, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("dsh --version exited with %v\n%s", err, out)
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != "dsh" || fields[1] == "" {
		return fmt.Errorf("dsh --version printed %q, want \"dsh <version> (...)\"", out)
	}
	return nil
}

// ---------------------------------------------------------------- scenario 3

func (w *threeModuleWorkspace) anInMemoryCopyWithReplaceLineAdded(line string) error {
	data, err := os.ReadFile(filepath.Join(w.root, "src", "harness", "go.mod"))
	if err != nil {
		return err
	}
	w.replaceLine = line
	w.mutatedLine = bytes.Count(data, []byte("\n")) + 1
	if len(data) > 0 && data[len(data)-1] != '\n' {
		w.mutatedLine++
	}
	return nil
}

const probeMain = `package main

import (
	"encoding/json"
	"os"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck"
)

func describe(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func main() {
	root, replaceLine := os.Args[1], os.Args[2]
	repo, err := modcheck.Load(root)
	if err != nil {
		panic(err)
	}
	committed := modcheck.CheckToolchain11(repo)

	harness := append([]byte(nil), repo.Mods["src/harness"]...)
	if n := len(harness); n > 0 && harness[n-1] != '\n' {
		harness = append(harness, '\n')
	}
	harness = append(harness, replaceLine+"\n"...)
	mutated := repo
	mutated.Mods = map[string][]byte{}
	for dir, data := range repo.Mods {
		mutated.Mods[dir] = data
	}
	mutated.Mods["src/harness"] = harness

	json.NewEncoder(os.Stdout).Encode(map[string]string{
		"committed": describe(committed),
		"mutated":   describe(modcheck.CheckToolchain11(mutated)),
	})
}
`

// dhTToolchain11ChecksTheParsedBytes runs modcheck.CheckToolchain11 (tools/harness-gate)
// on the committed module files and on the in-memory copy carrying the replace line.
// The gate is its own module outside go.work, so a throwaway probe module in a temp
// directory links it; nothing in the checkout is written.
func (w *threeModuleWorkspace) dhTToolchain11ChecksTheParsedBytes() error {
	probe := filepath.Join(w.tempDir, "probe")
	if err := os.MkdirAll(probe, 0o755); err != nil {
		return err
	}
	gate := filepath.ToSlash(filepath.Join(w.root, "tools", "harness-gate"))
	mod := "module dshe2eprobe\n\ngo 1.27.4\n\n" +
		"require github.com/smartpcr/agent-plugin/tools/harness-gate v0.0.0\n\n" +
		"replace github.com/smartpcr/agent-plugin/tools/harness-gate => " + gate + "\n"
	if err := os.WriteFile(filepath.Join(probe, "go.mod"), []byte(mod), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(probe, "main.go"), []byte(probeMain), 0o644); err != nil {
		return err
	}
	r := w.goTool(probe, "off", "run", ".", w.root, w.replaceLine)
	if r.Err != nil {
		return fmt.Errorf("%s: %v\n%s", r.Label, r.Err, r.Output)
	}
	if err := json.Unmarshal([]byte(lastLine(r.Output)), &w.probeOutcome); err != nil {
		return fmt.Errorf("probe output %q: %w", r.Output, err)
	}
	return nil
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func (w *threeModuleWorkspace) itFailsNamingTheReplaceLine() error {
	msg := w.probeOutcome.Mutated
	if msg == "" {
		return errors.New("DH-T-TOOLCHAIN-11 passed on a go.mod carrying a replace directive")
	}
	args := strings.TrimSpace(strings.TrimPrefix(w.replaceLine, "replace"))
	want := fmt.Sprintf("src/harness/go.mod:%d: replace %s: replace directive is not allowed", w.mutatedLine, args)
	if !strings.Contains(msg, want) {
		return fmt.Errorf("failure does not name the replace line %q:\n%s", want, msg)
	}
	return nil
}

func (w *threeModuleWorkspace) withTheCommittedBytesItPasses() error {
	if w.probeOutcome.Committed != "" {
		return fmt.Errorf("DH-T-TOOLCHAIN-11 fails on the committed bytes:\n%s", w.probeOutcome.Committed)
	}
	return nil
}

// ---------------------------------------------------------------- scenario 4

func (w *threeModuleWorkspace) goldenCommittedBeforeTheFirstDHS2Edit() error {
	data, err := os.ReadFile(filepath.Join(w.root, filepath.FromSlash(goldenRel)))
	if err != nil {
		return fmt.Errorf("the golden is not committed: %w", err)
	}
	w.golden = data
	gitbin, err := exec.LookPath("git")
	if err != nil {
		return fmt.Errorf("git is required to read the committed golden: %w", err)
	}
	cmd := exec.Command(gitbin, "show", "HEAD:"+goldenRel)
	cmd.Dir = w.root
	committed, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("%s is not tracked at HEAD: %w", goldenRel, err)
	}
	normalize := func(b []byte) []byte { return bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n")) }
	if !bytes.Equal(normalize(committed), normalize(data)) {
		return fmt.Errorf("%s differs from its committed bytes; the golden must not be re-recorded", goldenRel)
	}
	// The golden's history must be the commit that added it and, after it, only
	// the sanctioned re-recordings of goldenReRecords, each adding exactly its
	// listed lines and removing none; any other modifying commit fails here.
	gitLog := func(extra ...string) ([]string, error) {
		args := append([]string{"log", "--format=%H"}, extra...)
		args = append(args, "--", goldenRel)
		c := exec.Command(gitbin, args...)
		c.Dir = w.root
		out, err := c.Output()
		if err != nil {
			return nil, err
		}
		return strings.Fields(string(out)), nil
	}
	touching, err := gitLog()
	if err != nil {
		return fmt.Errorf("cannot read the history of %s: %w", goldenRel, err)
	}
	added, err := gitLog("--diff-filter=A")
	if err != nil {
		return fmt.Errorf("cannot read the history of %s: %w", goldenRel, err)
	}
	if len(added) != 1 || len(touching) == 0 || touching[len(touching)-1] != added[0] {
		return fmt.Errorf("%s must be introduced by exactly one commit, the oldest of its history; history %v, introduced by %v", goldenRel, touching, added)
	}
	for _, c := range touching[:len(touching)-1] {
		i := slices.IndexFunc(goldenReRecords, func(r goldenReRecord) bool { return r.commit == c })
		if i < 0 {
			return fmt.Errorf("%s must be touched only by the commit that introduced it or a sanctioned re-recording; history %v, introduced by %v: %s re-recorded the golden", goldenRel, touching, added, c)
		}
		show := exec.Command(gitbin, "show", "--format=", "--unified=0", c, "--", goldenRel)
		show.Dir = w.root
		diff, err := show.Output()
		if err != nil {
			return fmt.Errorf("cannot read %s's change to %s: %w", c, goldenRel, err)
		}
		var plus, minus []string
		for _, line := range strings.Split(string(normalize(diff)), "\n") {
			switch {
			case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			case strings.HasPrefix(line, "+"):
				plus = append(plus, line[1:])
			case strings.HasPrefix(line, "-"):
				minus = append(minus, line[1:])
			}
		}
		if r := goldenReRecords[i]; len(minus) != 0 || !slices.Equal(plus, r.added) {
			return fmt.Errorf("%s's re-recording of %s (%s) must add exactly %q and remove nothing; it added %q and removed %q", c, goldenRel, r.reason, r.added, plus, minus)
		}
	}
	if bytes.Contains(data, []byte(`\`)) || !bytes.Contains(data, []byte("<TMP>/cordis-sample.json")) {
		return fmt.Errorf("%s is not normalized (<TMP> path, / separators)", goldenRel)
	}
	if regexp.MustCompile(`\d{2}:\d{2}:\d{2}`).Match(data) {
		return fmt.Errorf("%s carries a timestamp; the exporter must be timestamp-free", goldenRel)
	}
	return nil
}

func (w *threeModuleWorkspace) cordisSampleTestRunsWithoutUpdate() error {
	w.goldenTest = w.goTool(filepath.Join(w.root, "src", "go"), "", "test", "-count=20", "-v", "./cmd/cordis-sample")
	return nil
}

func (w *threeModuleWorkspace) everyRunMatchesTheGolden() error {
	r := w.goldenTest
	if r.Err != nil {
		return fmt.Errorf("%s: %v\n%s", r.Label, r.Err, r.Output)
	}
	if n := strings.Count(r.Output, "--- PASS: TestOutputMatchesGolden"); n != 20 {
		return fmt.Errorf("TestOutputMatchesGolden passed %d times, want 20\n%s", n, r.Output)
	}
	if strings.Contains(r.Output, "--- FAIL") {
		return fmt.Errorf("a cordis-sample test failed\n%s", r.Output)
	}
	return nil
}

func (w *threeModuleWorkspace) theGoldenHoldsBeatWindows(a, b, c, d int) error {
	var runs []int
	current := 0
	for _, line := range strings.Split(string(w.golden), "\n") {
		if strings.Contains(line, "beat: ") {
			current++
			continue
		}
		if current > 0 {
			runs = append(runs, current)
			current = 0
		}
	}
	if current > 0 {
		runs = append(runs, current)
	}
	if want := []int{a, b, c, d}; !slices.Equal(runs, want) {
		return fmt.Errorf("golden beat: windows are %v, want %v", runs, want)
	}
	return nil
}

func (w *threeModuleWorkspace) noCoincidentEventsFailure(phrase string) error {
	if strings.Contains(w.goldenTest.Output, "--- FAIL") && strings.Contains(w.goldenTest.Output, phrase) {
		return fmt.Errorf("a %q failure was raised\n%s", phrase, w.goldenTest.Output)
	}
	if n := strings.Count(w.goldenTest.Output, "--- PASS: TestSameInstantIsCoincidentEvents"); n != 20 {
		return fmt.Errorf("the coincident-events guard passed %d times, want 20", n)
	}
	return nil
}

func (w *threeModuleWorkspace) mainStillCallsRunWithTheRealEnvironment() error {
	path := filepath.Join(w.root, "src", "go", "cmd", "cordis-sample", "main.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return err
	}
	render := func(n ast.Node) string {
		var b strings.Builder
		_ = printer.Fprint(&b, fset, n)
		return b.String()
	}
	var mainFunc *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" {
			mainFunc = fn
		}
	}
	if mainFunc == nil {
		return errors.New("cordis-sample main.go declares no func main")
	}
	envs := map[string]*ast.CompositeLit{}
	var runArgs []string
	ast.Inspect(mainFunc.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				if lit, ok := rhs.(*ast.CompositeLit); ok && render(lit.Type) == "sampleEnv" && i < len(n.Lhs) {
					envs[render(n.Lhs[i])] = lit
				}
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "run" && len(n.Args) == 1 {
				runArgs = append(runArgs, render(n.Args[0]))
			}
		}
		return true
	})
	if len(runArgs) != 1 || envs[runArgs[0]] == nil {
		return fmt.Errorf("main must call run once with a sampleEnv literal, found run(%v)", runArgs)
	}
	fields := map[string]string{}
	for _, elt := range envs[runArgs[0]].Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			fields[render(kv.Key)] = render(kv.Value)
		}
	}
	want := map[string]string{
		"Out":      "os.Stdout",
		"TempDir":  "os.TempDir()",
		"Exporter": "cordis.NewConsoleExporter(os.Stdout, os.Stderr)",
		"Pause":    "time.Sleep",
		"Settle":   "func() {}",
	}
	var errs []error
	for k, v := range want {
		if fields[k] != v {
			errs = append(errs, fmt.Errorf("main passes %s: %q, want %q", k, fields[k], v))
		}
	}
	if len(fields) != len(want) {
		errs = append(errs, fmt.Errorf("main sets %v, want exactly %v", fields, want))
	}
	return errors.Join(errs...)
}

// ---------------------------------------------------------------- wiring

func InitializeScenario_repository_modules_and_quality_gates_three_module_workspace(ctx *godog.ScenarioContext) {
	w := &threeModuleWorkspace{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		root, err := repoRoot()
		if err != nil {
			return c, err
		}
		dir, err := os.MkdirTemp("", "dsh-e2e-*")
		if err != nil {
			return c, err
		}
		*w = threeModuleWorkspace{root: root, tempDir: dir}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w.tempDir != "" {
			_ = os.RemoveAll(w.tempDir)
		}
		return c, err
	})

	ctx.Step(`^a clean checkout with the root go\.work$`, w.aCleanCheckoutWithTheRootGoWork)
	ctx.Step(`^"([^"]+)" runs from the repository root$`, w.commandRunsFromTheRepositoryRoot)
	ctx.Step(`^"([^"]+)" runs in each of the three module directories$`, w.commandRunsInEachModuleDirectory)
	ctx.Step(`^every command exits 0$`, w.everyCommandExits0)
	ctx.Step(`^"go list -m" reports exactly the three module paths of RM-1$`, w.goListMReportsExactlyTheThreeModulePaths)

	ctx.Step(`^GOWORK=off$`, w.goworkOff)
	ctx.Step(`^"go build -o <tmp>/dsh \./cmd/dsh" runs in src/harness$`, w.goBuildRunsInSrcHarness)
	ctx.Step(`^the build succeeds without any replace directive$`, w.theBuildSucceedsWithoutAnyReplaceDirective)
	ctx.Step(`^the binary prints its version and exits 0$`, w.theBinaryPrintsItsVersionAndExits0)

	ctx.Step(`^an in-memory copy of src/harness/go\.mod with the line "([^"]+)" added$`, w.anInMemoryCopyWithReplaceLineAdded)
	ctx.Step(`^DH-T-TOOLCHAIN-11 checks the parsed bytes$`, w.dhTToolchain11ChecksTheParsedBytes)
	ctx.Step(`^it fails naming the replace line$`, w.itFailsNamingTheReplaceLine)
	ctx.Step(`^with the committed bytes it passes$`, w.withTheCommittedBytesItPasses)

	ctx.Step(`^testdata/output\.golden committed before the first DH-S-2 edit$`, w.goldenCommittedBeforeTheFirstDHS2Edit)
	ctx.Step(`^"go -C src/go test -count=20 \./cmd/cordis-sample" runs without -update$`, w.cordisSampleTestRunsWithoutUpdate)
	ctx.Step(`^every run's normalized output is byte-identical to the golden$`, w.everyRunMatchesTheGolden)
	ctx.Step(`^the golden holds beat: lines in windows of (\d+), (\d+), (\d+) and (\d+)$`, w.theGoldenHoldsBeatWindows)
	ctx.Step(`^no "([^"]+)" failure is raised$`, w.noCoincidentEventsFailure)
	ctx.Step(`^main still calls run with os\.Stdout, os\.TempDir\(\), the console exporter, time\.Sleep and a no-op Settle$`, w.mainStillCallsRunWithTheRealEnvironment)
}

func TestE2E_repository_modules_and_quality_gates_three_module_workspace(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "repository_modules_and_quality_gates_three_module_workspace",
		ScenarioInitializer: InitializeScenario_repository_modules_and_quality_gates_three_module_workspace,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"repository_modules_and_quality_gates_three_module_workspace.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("three module workspace scenarios failed")
	}
}
