package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/analyzers"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck"
)

// vetToolEnv, set to "1" in the environment of "go vet -vettool", makes this binary
// act as the vet tool that runs the project's own analyzers.
const vetToolEnv = "HARNESS_GATE_VETTOOL"

// budgetPattern selects the performance-budget tests (QG-7). The test gate runs
// them with every other test under -race, so a race in a budget test is caught; the
// coverage gate leaves them out, and the budget gate runs them alone without race
// or coverage instrumentation so a compiled-in budget measures the real code.
const budgetPattern = "^TestBudget"

// LayerTags are the build tags of the test layers the keyless gate never runs
// (DH-ADR-TOOLCHAIN-4; DH-T-TOOLCHAIN-4):
//
//   - replay:   keyless replay of recorded model traffic (the helpers of DH-S-16);
//   - scenario: DH-E-* scenarios against the real environment, authored as
//     *_scenario_test.go files with //go:build scenario and run only by the
//     credentialed scenario workflow.
//
// A file of either layer is excluded from every go test of the gate: the test and
// coverage gates pass an explicit empty tag list (noTags), which overrides any
// -tags a GOFLAGS setting would add. The vet gate still compiles and analyzes them
// (layerTags), so their findings are reported without credentials (QG-2).
var LayerTags = []string{"replay", "scenario"}

// noTags is the build-tag flag of the gate's go test runs; see LayerTags.
const noTags = "-tags="

// BenchTag is the build tag of the benchmark files (*_bench_test.go with
// //go:build bench), run by hand with "go test -tags bench -bench". No gate runs
// them, but vet, the own analyzers and staticcheck compile and check them with the
// LayerTags (layerTags), so a bench file that does not build, or breaks an
// analyzer rule, fails the gate.
const BenchTag = "bench"

// layerTags is the build-tag flag that selects every LayerTags file, and the
// BenchTag files, for go vet and staticcheck.
var layerTags = "-tags=" + strings.Join(append(slices.Clone(LayerTags), BenchTag), ",")

// E2ETag is the build tag of the Gherkin e2e suites (test/e2e). The gate runs them
// in no gate -- test, coverage and self pass -tags= -- but vet compiles and analyzes
// them (vetTagSets), so an e2e file that does not build, or breaks an analyzer
// rule, fails the gate (M1-D-GATE-25 A).
const E2ETag = "e2e"

// vetTagSets are the build-tag flags vet runs with: no tags, the LayerTags with
// the bench files, and the e2e layer.
var vetTagSets = []string{noTags, layerTags, "-tags=" + E2ETag}

// Targets are the GOOS/GOARCH pairs cmd/dsh must build for with CGO_ENABLED=0 (QG-8).
var Targets = []struct{ GOOS, GOARCH string }{
	{"linux", "amd64"}, {"linux", "arm64"},
	{"darwin", "amd64"}, {"darwin", "arm64"},
	{"windows", "amd64"}, {"windows", "arm64"},
}

// Config is what the gates run against.
type Config struct {
	Root    string   // repository root, or a single module's directory
	Modules []string // absolute module directories the per-module gates run in
	Go      string   // the go command: the floor toolchain's own go binary (Toolchain.Go)
	VetTool string   // this binary, run by "go vet -vettool"
	Environ []string // the parent environment the children's environments derive from

	// Toolchain is the go floor every child runs on (M1-G-TOOL-14); NewConfig
	// fails setup when the toolchain it selects is not the floor.
	Toolchain Toolchain

	// RacePreflight checks that -race can run on this lane; nil selects
	// cCompilerPreflight. A lane where it fails fails the test gate (QG-4).
	RacePreflight func(ctx context.Context, c *Config) error

	// CrossOut, when set, keeps the cross-compiled binaries there; otherwise they go
	// to a temporary directory that is removed afterwards.
	CrossOut string

	// TestTimeout bounds every coverage test binary the gate runs directly (zero
	// selects DefaultTestTimeout, go test's own default): a blocked test fails the
	// coverage gate, named, instead of hanging the runner.
	TestTimeout time.Duration

	// ToolsDir is the tools module whose pinned linters the staticcheck and vulncheck
	// gates run ("go tool -n"); empty selects toolsModuleDir.
	ToolsDir string

	// VulnDB, when set, is the vulnerability database the vulncheck gate reads
	// (govulncheck -db), such as a file:// fixture; empty selects govulncheck's
	// default, https://vuln.go.dev.
	VulnDB string
}

// DefaultTestTimeout is the per-binary timeout of the coverage gate when
// Config.TestTimeout is zero.
const DefaultTestTimeout = 10 * time.Minute

func (c *Config) testTimeout() time.Duration {
	if c.TestTimeout > 0 {
		return c.TestTimeout
	}
	return DefaultTestTimeout
}

// NewConfig returns the configuration for root: the module at root when root holds a
// go.mod, otherwise the three product modules of RM-1 under root.
//
// It reads the go floor from src/harness/go.mod (a single-module root: its own
// go.mod), selects that toolchain with GOTOOLCHAIN=go<floor> and fails, with a
// *FloorError naming both versions, when "go version" is not the floor
// (M1-G-TOOL-14). Every child then runs the floor's own go binary with
// Toolchain.Environ.
func NewConfig(root string) (*Config, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	modules, err := discoverModules(root)
	if err != nil {
		return nil, err
	}
	gobin, err := exec.LookPath("go")
	if err != nil {
		return nil, fmt.Errorf("the go command is required: %w", err)
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	source := floorSource(root, modules)
	floor, err := ReadFloor(source)
	if err != nil {
		return nil, err
	}
	rel, err := filepath.Rel(root, source)
	if err != nil {
		rel = source
	}
	tc, err := ResolveToolchain(context.Background(), gobin, root, os.Environ(), floor, filepath.ToSlash(rel))
	if err != nil {
		return nil, err
	}
	return &Config{Root: root, Modules: modules, Go: tc.Go, VetTool: self, Environ: tc.Environ(os.Environ()), Toolchain: tc}, nil
}

func discoverModules(root string) ([]string, error) {
	if isFile(filepath.Join(root, "go.mod")) {
		return []string{root}, nil
	}
	var modules []string
	for _, m := range modcheck.Modules {
		dir := filepath.Join(root, filepath.FromSlash(m.Dir))
		if !isFile(filepath.Join(dir, "go.mod")) {
			return nil, fmt.Errorf("%s: product module %s has no go.mod (RM-1)", root, m.Dir)
		}
		modules = append(modules, dir)
	}
	return modules, nil
}

// Gates is the fixed, ordered gate list (QG-1, DH-ADR-TOOLCHAIN-9). Nothing reorders,
// skips or adds to it at run time.
func Gates(c *Config) []Gate {
	return []Gate{
		{Name: "gofmt", Title: "every Go file is gofmt-formatted", Run: c.gofmtGate},
		{Name: "vet", Title: "go vet and the project's own analyzers (nosleep, onespawn, cleanup, fuzzcheck, rootcall, typeassert, lintignore), also on the e2e layer; prints the //lint:ignore exception total", Run: c.vetGate},
		{Name: "staticcheck", Title: "the pinned staticcheck per module, with " + noTags + " and " + layerTags, Run: c.staticcheckGate},
		{Name: "modules", Title: "go mod tidy -diff and go mod verify per module, the module rules (RM-1..RM-4) and a reason on every pin (RM-10)", Run: c.modulesGate},
		{Name: "test", Title: "go test -race -shuffle=on -count=1 per module, CGO_ENABLED=1", Run: c.testGate},
		{Name: "coverage", Title: fmt.Sprintf("coverage merged across processes, %d%% of statements per package", CoverageFloor), Run: c.coverageGate},
		{Name: "trace", Title: "tools/harness-trace verify, and trace-tests: every DH-T ID of DH-S-1..built-through named by a test or validly deferred", Run: c.traceGate},
		{Name: "scenario-index", Title: "every authored DH-E-* scenario test is in " + acceptance.IndexPath + " exactly once, and every indexed one authored so far has its test", Run: c.scenarioIndexGate},
		{Name: "policy-text", Title: "M2-C-6: no file under " + PolicyTextDir + "/ without a passed " + PolicyTextScenario + " row of " + PolicyTextStage + ".md recording its SHA-256", Run: c.policyTextGate},
		{Name: "budget", Title: "performance budgets (TestBudget*) with DSH_* and GO* overrides cleared", Run: c.budgetGate},
		{Name: "cross-compile", Title: "cmd/dsh for linux, darwin, windows on amd64 and arm64 with CGO_ENABLED=0", Run: c.crossCompileGate},
		{Name: "vulncheck", Title: "govulncheck ./... per module against the vulnerability database; an unreachable database fails", Run: c.vulncheckGate},
		{Name: "self", Title: "the gate's own tests: go test -count=1 ./... in " + SelfModule + ", without the e2e tag", Run: c.selfGate},
	}
}

// SelfModule is the gate's own module, whose tests the self gate runs (M1-G-TOOL-3).
const SelfModule = "tools/harness-gate"

// selfSkip is the test the self gate leaves out: it runs this gate list on the
// repository, so inside the self gate it would recurse.
const selfSkip = "^TestRepositoryPassesEveryGate$"

// selfTimeout bounds the self gate's go test; the main package's tests run the gate
// on seeded modules and cross-compile cmd/dsh.
const selfTimeout = 30 * time.Minute

// testModules are the nested test-only modules (<module>/test/e2e/go.mod) of the
// gated modules, such as src/go/test/e2e (M1-D-TOOL-4 (A)).
func (c *Config) testModules() []string {
	var out []string
	for _, mod := range c.Modules {
		if dir := filepath.Join(mod, filepath.FromSlash(strings.TrimPrefix(modcheck.TestModuleSuffix, "/"))); isFile(filepath.Join(dir, "go.mod")) {
			out = append(out, dir)
		}
	}
	return out
}

// toolsModules are the tools/<name> modules of a repository root, such as the tools
// module tools/harness-gate (M1-D-TOOL-2 (a)); a single-module root has none.
func (c *Config) toolsModules() []string {
	if c.singleModule() {
		return nil
	}
	found, _ := filepath.Glob(filepath.Join(c.Root, "tools", "*", "go.mod"))
	var out []string
	for _, p := range found {
		out = append(out, filepath.Dir(p))
	}
	slices.Sort(out)
	return out
}

// singleModule reports whether the gates run on the one module at the root.
func (c *Config) singleModule() bool {
	return len(c.Modules) == 1 && c.Modules[0] == c.Root
}

// modulesGate is module hygiene (M1-G-TOOL-2; DH-T-TOOLCHAIN-11). In every module --
// the gated modules, their test modules and the tools modules -- "go mod tidy -diff"
// must print nothing and "go mod verify" must pass. Then the module files are checked
// without the go command: on a repository root modcheck.CheckToolchain11 (RM-1..RM-4,
// src/go standard-library only) and modcheck.CheckRepoReasons, on a single-module root
// modcheck.CheckReasons of its go.mod. Every failure is reported.
func (c *Config) modulesGate(ctx context.Context, w io.Writer) error {
	env := c.env("GOFLAGS=")
	var errs []error
	dirs := append(append(slices.Clone(c.Modules), c.testModules()...), c.toolsModules()...)
	for _, dir := range dirs {
		if err := c.tidyStep(ctx, w, dir, env); err != nil {
			errs = append(errs, err)
		}
		if err := c.goStep(ctx, w, dir, env, "mod", "verify"); err != nil {
			errs = append(errs, err)
		}
	}
	if c.singleModule() {
		data, err := os.ReadFile(filepath.Join(c.Root, "go.mod"))
		if err == nil {
			err = modcheck.CheckReasons("go.mod", data)
		}
		if err != nil {
			errs = append(errs, err)
		}
		fmt.Fprintf(w, "modules: %d modules checked\n", len(dirs))
		return errors.Join(errs...)
	}
	repo, err := modcheck.Load(c.Root)
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	if err := modcheck.CheckToolchain11(repo); err != nil {
		errs = append(errs, err)
	}
	if err := modcheck.CheckRepoReasons(repo); err != nil {
		errs = append(errs, err)
	}
	fmt.Fprintf(w, "modules: %d product, %d test and %d tools modules checked\n", len(repo.Mods), len(repo.Tests), len(repo.Tools))
	return errors.Join(errs...)
}

// tidyStep runs "go mod tidy -diff" in dir: a non-zero exit, or any output other than
// the go command's download notices, fails it, and the diff is printed.
func (c *Config) tidyStep(ctx context.Context, w io.Writer, dir string, env []string) error {
	out, err := c.command(ctx, dir, env, c.Go, "mod", "tidy", "-diff")
	var diff []string
	for _, line := range strings.SplitAfter(out, "\n") {
		if !strings.HasPrefix(line, "go: downloading ") && !strings.HasPrefix(line, "go: finding ") {
			diff = append(diff, line)
		}
	}
	text := strings.Join(diff, "")
	if err == nil && strings.TrimSpace(text) == "" {
		fmt.Fprintf(w, "ok   %s: go mod tidy -diff\n", c.rel(dir))
		return nil
	}
	fmt.Fprintf(w, "FAIL %s: go mod tidy -diff\n%s", c.rel(dir), text)
	if err == nil {
		err = errors.New("printed a diff")
	}
	return fmt.Errorf("%s: go mod tidy -diff: %w; the module is not tidy (run go mod tidy)", c.rel(dir), err)
}

// selfGate runs the gate's own tests -- modcheck, the analyzers, the runner, the
// acceptance index -- so a module or analyzer rule cannot regress unseen (M1-G-TOOL-3;
// DH-T-TOOLCHAIN-9). It runs "go test -tags= -count=1 -skip <selfSkip> ./..." in
// SelfModule on the floor toolchain, without the e2e tag. A root without the module
// fails: the check cannot be skipped.
func (c *Config) selfGate(ctx context.Context, w io.Writer) error {
	dir := filepath.Join(c.Root, filepath.FromSlash(SelfModule))
	if !isFile(filepath.Join(dir, "go.mod")) {
		return fmt.Errorf("%s not found under %s: the gate's own tests cannot run (M1-G-TOOL-3)", SelfModule, c.Root)
	}
	return c.goStep(ctx, w, dir, c.env(), "test", noTags, "-count=1", "-timeout="+selfTimeout.String(), "-skip", selfSkip, "./...")
}

// gofmtGate fails when any Go file of a module, or of its test module
// (testModules), is not gofmt-formatted. testdata, vendor, hidden and other
// nested-module directories are not part of a module's build and are not checked.
func (c *Config) gofmtGate(ctx context.Context, w io.Writer) error {
	gofmt, err := c.gofmtPath(ctx)
	if err != nil {
		return err
	}
	var files []string
	for _, mod := range append(slices.Clone(c.Modules), c.testModules()...) {
		found, err := goFiles(mod)
		if err != nil {
			return err
		}
		files = append(files, found...)
	}
	var unformatted []string
	for batch := range slices.Chunk(files, 100) {
		out, err := c.command(ctx, c.Root, c.env(), gofmt, append([]string{"-l"}, batch...)...)
		if err != nil {
			return fmt.Errorf("gofmt -l: %w\n%s", err, out)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			if line = strings.TrimSpace(line); line != "" {
				unformatted = append(unformatted, c.rel(line))
			}
		}
	}
	fmt.Fprintf(w, "gofmt: %d files checked\n", len(files))
	if len(unformatted) > 0 {
		return fmt.Errorf("not gofmt-formatted:\n  %s", strings.Join(unformatted, "\n  "))
	}
	return nil
}

func (c *Config) gofmtPath(ctx context.Context) (string, error) {
	out, err := c.command(ctx, c.Root, c.env(), c.Go, "env", "GOROOT")
	if err != nil {
		return "", fmt.Errorf("go env GOROOT: %w\n%s", err, out)
	}
	gofmt := filepath.Join(strings.TrimSpace(out), "bin", "gofmt")
	if runtime.GOOS == "windows" {
		gofmt += ".exe"
	}
	return gofmt, nil
}

// goFiles lists the .go files of the module rooted at mod.
func goFiles(mod string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(mod, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if p != mod && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || isFile(filepath.Join(p, "go.mod"))) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			files = append(files, p)
		}
		return nil
	})
	return files, err
}

// vetGate runs the standard go vet checks, then the project's own analyzers through
// "go vet -vettool" (QG-2). No baseline or exemption file is read. Each runs three
// times (vetTagSets): without build tags, with every LayerTags tag and BenchTag, and
// with the e2e tag, so replay-, scenario-, bench- and e2e-tagged files are compiled
// and analyzed even though the gate never runs them. Vetting needs no credentials. Last it prints "exceptions: N", the accepted //lint:ignore
// directives of every Go file of the modules, of the own analyzers and staticcheck
// alike (analyzers.Exceptions; DH-ADR-TOOLCHAIN-8).
func (c *Config) vetGate(ctx context.Context, w io.Writer) error {
	var errs []error
	for _, mod := range c.Modules {
		for _, tags := range vetTagSets {
			if err := c.goStep(ctx, w, mod, c.env(), "vet", tags, "./..."); err != nil {
				errs = append(errs, err)
			}
			if err := c.goStep(ctx, w, mod, c.env(vetToolEnv+"=1"), "vet", tags, "-vettool="+c.VetTool, "./..."); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := c.printExceptions(w); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// printExceptions prints the exception total of the modules' Go files.
func (c *Config) printExceptions(w io.Writer) error {
	var files []string
	for _, mod := range c.Modules {
		found, err := goFiles(mod)
		if err != nil {
			return err
		}
		files = append(files, found...)
	}
	totals, err := analyzers.Exceptions(files)
	if err != nil {
		return fmt.Errorf("exceptions: %w", err)
	}
	fmt.Fprintf(w, "exceptions: %s\n", totals)
	return nil
}

// testGate runs every test package with the race detector, in shuffled order, once
// (QG-4), without the replay and scenario layers (LayerTags). A lane where -race
// cannot run fails here; it is never skipped.
func (c *Config) testGate(ctx context.Context, w io.Writer) error {
	preflight := c.RacePreflight
	if preflight == nil {
		preflight = cCompilerPreflight
	}
	if err := preflight(ctx, c); err != nil {
		return err
	}
	var errs []error
	for _, mod := range c.Modules {
		err := c.goStep(ctx, w, mod, c.env("CGO_ENABLED=1"), "test", noTags, "-race", "-shuffle=on", "-count=1", "./...")
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// cCompilerPreflight fails when the C compiler that cgo, and so -race, needs is not
// on PATH (EN-6).
func cCompilerPreflight(ctx context.Context, c *Config) error {
	out, err := c.command(ctx, c.Root, c.env("CGO_ENABLED=1"), c.Go, "env", "CC")
	if err != nil {
		return fmt.Errorf("race: go env CC: %w\n%s", err, out)
	}
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return errors.New("race: no C compiler (go env CC is empty)")
	}
	if _, err := exec.LookPath(fields[0]); err != nil {
		return fmt.Errorf("race: no C compiler (CC=%s): %w", fields[0], err)
	}
	return nil
}

// coverageGate runs the tests with coverage, collects the profiles of the test
// processes and of every child they re-execute (GOCOVERDIR), merges them and fails
// every package under the floor (QG-3).
func (c *Config) coverageGate(ctx context.Context, w io.Writer) error {
	var errs []error
	for _, mod := range c.Modules {
		cov, err := c.moduleCoverage(ctx, w, mod)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		for _, pc := range cov {
			fmt.Fprintf(w, "coverage: %s %s%%\n", pc.Package, formatPercent(pc.Percent()))
		}
		if err := CheckFloor(cov, CoverageFloor); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// moduleCoverage returns the merged statement coverage of every package of mod.
//
// Every package with tests is compiled once with "go test -c -cover
// -covermode=set -coverpkg=./..." and its test binary is run in the package
// directory with GOCOVERDIR and -test.gocoverdir both set to one directory the gate
// owns. Every child the tests start inherits that GOCOVERDIR: a binary built with
// -cover writes its counters there on exit, and a re-executed test binary writes
// there when started with -test.gocoverdir=$GOCOVERDIR (README.md). The directory
// is then merged with "go tool covdata textfmt", which joins the profiles of
// different binaries package by package (go test's own report keeps only the
// profiles of the test binary itself). A package without test files gets its 0%
// entries from "go test -cover -coverprofile", so no package escapes the floor.
func (c *Config) moduleCoverage(ctx context.Context, w io.Writer, mod string) ([]PackageCoverage, error) {
	tmp, err := os.MkdirTemp("", "harness-gate-cover-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	covdir, bindir := filepath.Join(tmp, "covdata"), filepath.Join(tmp, "bin")
	for _, d := range []string{covdir, bindir} {
		if err := os.Mkdir(d, 0o755); err != nil {
			return nil, err
		}
	}
	pkgs, err := c.listPackages(ctx, mod)
	if err != nil {
		return nil, err
	}
	var untested []string
	var errs []error
	for i, pkg := range pkgs {
		if !pkg.HasTests {
			untested = append(untested, pkg.ImportPath)
			continue
		}
		bin := filepath.Join(bindir, fmt.Sprintf("pkg%d.test", i))
		if runtime.GOOS == "windows" {
			bin += ".exe"
		}
		if err := c.goStep(ctx, w, mod, c.env(), "test", "-c", noTags, "-cover", "-covermode=set", "-coverpkg=./...", "-o", bin, pkg.ImportPath); err != nil {
			errs = append(errs, err)
			continue
		}
		env := c.env("GOCOVERDIR=" + covdir)
		label := pkg.ImportPath + ".test"
		// -test.timeout makes the binary panic with every goroutine's stack and name the
		// running test; the context deadline backs it up should the binary not exit.
		timeout := c.testTimeout()
		runCtx, cancel := context.WithTimeout(ctx, timeout+time.Minute)
		err := c.step(runCtx, w, pkg.Dir, env, label, bin, "-test.count=1", "-test.timeout="+timeout.String(), "-test.skip="+budgetPattern, "-test.gocoverdir="+covdir)
		cancel()
		if err != nil {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}

	var profiles []*Profile
	merged := filepath.Join(tmp, "covdata.out")
	if err := c.goStep(ctx, w, mod, c.env(), "tool", "covdata", "textfmt", "-i="+covdir, "-o="+merged); err != nil {
		return nil, err
	}
	if p, err := readProfile(merged); err != nil {
		return nil, err
	} else {
		profiles = append(profiles, p)
	}
	if len(untested) > 0 {
		zero := filepath.Join(tmp, "untested.out")
		args := append([]string{"test", noTags, "-cover", "-covermode=set", "-coverprofile=" + zero}, untested...)
		if err := c.goStep(ctx, w, mod, c.env(), args...); err != nil {
			return nil, err
		}
		p, err := readProfile(zero)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, p)
	}
	return Coverage(profiles...), nil
}

// goPackage is one package of a module as "go list" reports it.
type goPackage struct {
	ImportPath, Dir string
	HasTests        bool
}

// listPackages lists the packages of mod.
func (c *Config) listPackages(ctx context.Context, mod string) ([]goPackage, error) {
	const format = `{{.ImportPath}}{{"\t"}}{{.Dir}}{{"\t"}}{{len .TestGoFiles}}{{"\t"}}{{len .XTestGoFiles}}`
	out, err := c.command(ctx, mod, c.env(), c.Go, "list", noTags, "-f", format, "./...")
	if err != nil {
		return nil, fmt.Errorf("%s: go list: %w\n%s", c.rel(mod), err, out)
	}
	var pkgs []goPackage
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Split(strings.TrimSpace(line), "\t")
		if len(f) != 4 {
			return nil, fmt.Errorf("%s: go list: unexpected line %q", c.rel(mod), line)
		}
		pkgs = append(pkgs, goPackage{ImportPath: f[0], Dir: f[1], HasTests: f[2] != "0" || f[3] != "0"})
	}
	return pkgs, nil
}

// readProfile parses the text coverage profile at path.
func readProfile(path string) (*Profile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseProfile(bytes.NewReader(data))
}

// traceGate runs the two traceability checks (QG-9) and fails when either fails,
// naming both failures when both do: tools/harness-trace verify, then the test
// trace of trace-tests (M1-G-CLOSE-9, step 18.1.4) for DH-S-1 through the acceptance
// index's built-through, the subcommand's default stage. A root without the tool, or
// without impl.md or the index, fails: neither check can be skipped.
func (c *Config) traceGate(ctx context.Context, w io.Writer) error {
	return errors.Join(c.traceVerify(ctx, w), c.traceTests(w))
}

// traceVerify runs "tools/harness-trace verify" against the root.
func (c *Config) traceVerify(ctx context.Context, w io.Writer) error {
	dir := filepath.Join(c.Root, "tools", "harness-trace")
	if !isFile(filepath.Join(dir, "go.mod")) {
		return fmt.Errorf("tools/harness-trace not found under %s: the traceability check cannot run (QG-9)", c.Root)
	}
	out, err := c.command(ctx, dir, c.env(), c.Go, "run", ".", "verify", "-repo", c.Root)
	if err != nil {
		return fmt.Errorf("tools/harness-trace verify: %w\n%s", err, out)
	}
	fmt.Fprintf(w, "trace: %s\n", lastLine(out))
	return nil
}

// traceTests runs the check of "harness-gate trace-tests" with no --stage: every
// DH-T ID that a step of DH-S-1..built-through requires is named by a test or
// deferred by a doc.go deferral that impl.md backs. On a failure it prints the
// subcommand's full report and returns the same lines the subcommand prints after
// "FAILED:".
func (c *Config) traceTests(w io.Writer) error {
	r, err := acceptance.TraceTests(c.Root, "")
	if err != nil {
		return fmt.Errorf("trace-tests: %w", err)
	}
	if err := r.Err(); err != nil {
		r.Write(w)
		return fmt.Errorf("harness-gate trace-tests: FAILED:\n%w", err)
	}
	req, tag, def, miss := r.Totals("")
	fmt.Fprintf(w, "trace-tests: DH-S-1..%s: required %d, tagged %d, deferred %d, missing %d\n", r.Through, req, tag, def, miss)
	return nil
}

// scenarioIndexGate checks the acceptance index against the authored scenario tests
// of the modules (DH-ADR-TOOLCHAIN-6, EV-4): an authored scenario the index misses,
// or an indexed one that should be authored by now and is not, fails here, keylessly,
// long before the credentialed preflight of its acceptance stage. A root without the
// index fails: the check cannot be skipped.
func (c *Config) scenarioIndexGate(_ context.Context, w io.Writer) error {
	x, scenarios, err := c.acceptance()
	if err != nil {
		return err
	}
	if err := acceptance.Check(x, scenarios); err != nil {
		return err
	}
	if err := acceptance.CheckBoundary(c.Root, x, scenarios); err != nil {
		return err
	}
	indexed := 0
	for _, s := range x.Stages {
		indexed += len(s.Scenarios)
	}
	fmt.Fprintf(w, "scenario-index: %d scenario tests authored, %d scenarios indexed in %d stages, built through %s\n",
		len(scenarios), indexed, len(x.Stages), x.BuiltThrough)
	return nil
}

// acceptance reads the index under the root and scans the modules' scenario tests.
func (c *Config) acceptance() (*acceptance.Index, []acceptance.Scenario, error) {
	x, err := acceptance.ReadIndex(c.Root)
	if err != nil {
		return nil, nil, err
	}
	scenarios, err := acceptance.ScanScenarios(c.Root, c.Modules)
	if err != nil {
		return nil, nil, err
	}
	return x, scenarios, nil
}

// budgetGate runs the performance-budget tests. Their budgets are compiled in; the
// child environment has every DSH_* and GO* variable removed, so no override can
// relax them (QG-7).
func (c *Config) budgetGate(ctx context.Context, w io.Writer) error {
	env := BudgetEnv(c.Environ)
	var errs []error
	for _, mod := range c.Modules {
		if err := c.goStep(ctx, w, mod, env, "test", "-count=1", "-run", budgetPattern, "./..."); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// keptGoVars are the GO* variables a budget child keeps: where the toolchain and
// its caches live. None of them changes how the code under test runs.
var keptGoVars = []string{"GOROOT", "GOPATH", "GOMODCACHE", "GOCACHE", "GOTMPDIR"}

// BudgetEnv derives a budget child's environment from parent: every DSH_* and GO*
// variable is removed except the locations of keptGoVars, then the workspace is
// turned off, the user's go env file is ignored (GOENV=off, so persisted
// `go env -w` settings such as GOFLAGS cannot reach the budget build), GOFLAGS is
// set empty explicitly and the running toolchain is pinned.
func BudgetEnv(parent []string) []string {
	env := slices.DeleteFunc(slices.Clone(parent), func(kv string) bool {
		key := envKey(kv)
		if strings.HasPrefix(key, "DSH_") {
			return true
		}
		return strings.HasPrefix(key, "GO") && !slices.Contains(keptGoVars, key)
	})
	return append(env, "GOENV=off", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local")
}

// crossCompileGate builds cmd/dsh for every target with CGO_ENABLED=0 and the
// workspace off (QG-8). Every target is attempted; each failure names its target.
func (c *Config) crossCompileGate(ctx context.Context, w io.Writer) error {
	mod, err := c.dshModule()
	if err != nil {
		return err
	}
	out := c.CrossOut
	if out == "" {
		tmp, err := os.MkdirTemp("", "harness-gate-cross-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmp)
		out = tmp
	}
	var errs []error
	for _, t := range Targets {
		bin := filepath.Join(out, "dsh-"+t.GOOS+"-"+t.GOARCH)
		if t.GOOS == "windows" {
			bin += ".exe"
		}
		env := c.env("CGO_ENABLED=0", "GOOS="+t.GOOS, "GOARCH="+t.GOARCH)
		if err := c.goStep(ctx, w, mod, env, "build", "-o", bin, "./cmd/dsh"); err != nil {
			errs = append(errs, fmt.Errorf("%s/%s: %w", t.GOOS, t.GOARCH, err))
		}
	}
	return errors.Join(errs...)
}

// env is the parent environment with the workspace off (every per-module command
// resolves its module from its own go.mod) and kv set.
func (c *Config) env(kv ...string) []string {
	return setEnv(c.Environ, append([]string{"GOWORK=off"}, kv...)...)
}

// goStep runs "go args..." in dir. It prints one line when the step passes and the
// output when it fails; the error names the module, the command and any failing
// test.
func (c *Config) goStep(ctx context.Context, w io.Writer, dir string, env []string, args ...string) error {
	return c.step(ctx, w, dir, env, "go "+strings.Join(args, " "), c.Go, args...)
}

// step runs name with args in dir and env as one logged step called label; see
// goStep.
func (c *Config) step(ctx context.Context, w io.Writer, dir string, env []string, label, name string, args ...string) error {
	out, err := c.command(ctx, dir, env, name, args...)
	step := label
	if err == nil {
		fmt.Fprintf(w, "ok   %s: %s\n", c.rel(dir), step)
		return nil
	}
	fmt.Fprintf(w, "FAIL %s: %s\n%s", c.rel(dir), step, out)
	detail := ""
	if tests := failedTests(out); len(tests) > 0 {
		detail = "; failing tests: " + strings.Join(tests, ", ")
	}
	if strings.Contains(out, "WARNING: DATA RACE") {
		detail += "; data race detected"
	}
	if strings.Contains(out, "panic: test timed out") {
		detail += "; test timed out"
		if running := timedOutTests(out); len(running) > 0 {
			detail += ": " + strings.Join(running, ", ")
		}
	}
	if ctx.Err() != nil {
		detail += "; killed: " + ctx.Err().Error()
	}
	return fmt.Errorf("%s: %s: %w%s", c.rel(dir), step, err, detail)
}

var runningPattern = regexp.MustCompile(`(?m)^\t\t(\S+) \(\S+\)\r?$`)

// timedOutTests returns the tests a test binary listed as running when its
// -test.timeout fired.
func timedOutTests(out string) []string {
	_, after, ok := strings.Cut(out, "running tests:")
	if !ok {
		return nil
	}
	var names []string
	for _, m := range runningPattern.FindAllStringSubmatch(after, -1) {
		names = append(names, m[1])
	}
	return names
}

// command runs name with args in dir and env and returns its combined output.
func (c *Config) command(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	return runCommand(ctx, dir, env, name, args...)
}

var failPattern = regexp.MustCompile(`(?m)^\s*--- FAIL: (\S+)`)

// failedTests returns the names of the failed tests in go test output, in order.
func failedTests(out string) []string {
	var names []string
	for _, m := range failPattern.FindAllStringSubmatch(out, -1) {
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	return names
}

// setEnv returns env with each "KEY=value" of kv replacing any earlier KEY.
func setEnv(env []string, kv ...string) []string {
	out := slices.Clone(env)
	for _, pair := range kv {
		key := envKey(pair)
		out = slices.DeleteFunc(out, func(e string) bool { return envKey(e) == key })
		out = append(out, pair)
	}
	return out
}

// envKey is the variable name of "KEY=value"; on Windows names are case-insensitive.
func envKey(kv string) string {
	key, _, _ := strings.Cut(kv, "=")
	if runtime.GOOS == "windows" {
		key = strings.ToUpper(key)
	}
	return key
}

func (c *Config) rel(p string) string {
	if r, err := filepath.Rel(c.Root, p); err == nil && !strings.HasPrefix(r, "..") {
		return filepath.ToSlash(r)
	}
	return p
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func isFile(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.Mode().IsRegular()
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}
