package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// gateBinary is harness-gate built once for the end-to-end tests (TC-01b): the vet
// gate runs the binary itself as "go vet -vettool", so the tests run a real build.
var (
	gateOnce sync.Once
	gateBin  string
	gateErr  error
	gateDir  string
)

func TestMain(m *testing.M) {
	code := m.Run()
	if gateDir != "" {
		os.RemoveAll(gateDir)
	}
	os.Exit(code)
}

func buildGate(t *testing.T) string {
	t.Helper()
	gateOnce.Do(func() {
		gateDir, gateErr = os.MkdirTemp("", "harness-gate-bin-")
		if gateErr != nil {
			return
		}
		gateBin = filepath.Join(gateDir, "harness-gate")
		if runtime.GOOS == "windows" {
			gateBin += ".exe"
		}
		cmd := exec.Command("go", "build", "-o", gateBin, ".")
		cmd.Env = setEnv(os.Environ(), "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			gateErr = errors.New(string(out))
		}
	})
	if gateErr != nil {
		t.Fatalf("build harness-gate: %v", gateErr)
	}
	return gateBin
}

// scratch copies testdata/<name> into a fresh t.TempDir() and returns its path.
func scratch(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", name)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
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
		t.Fatalf("copy %s: %v", name, err)
	}
	return dst
}

// runGate runs the built harness-gate against root and returns its stdout and exit
// code.
func runGate(t *testing.T, root string) (string, int) {
	t.Helper()
	cmd := exec.Command(buildGate(t), "--root", root)
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return stdout.String(), 0
	case errors.As(err, &exit):
		return stdout.String(), exit.ExitCode()
	default:
		t.Fatalf("run harness-gate: %v\n%s", err, stderr.String())
		return "", 0
	}
}

// assertStoppedAt checks that the run named gate as the failure, exited non-zero and
// started no later gate.
func assertStoppedAt(t *testing.T, out string, code int, gate string) {
	t.Helper()
	if code == 0 {
		t.Errorf("exit code = 0, want non-zero\n%s", out)
	}
	if !strings.Contains(out, "harness-gate: FAILED at gate "+gate+"\n") {
		t.Errorf("output does not name gate %q as failed:\n%s", gate, out)
	}
	var names []string
	for _, g := range Gates(&Config{}) {
		names = append(names, g.Name)
	}
	for _, later := range names[slices.Index(names, gate)+1:] {
		if strings.Contains(out, " "+later+" -- ") {
			t.Errorf("gate %q started after %q failed:\n%s", later, gate, out)
		}
	}
}

// TestSeededViolationFailsNamedGate seeds one violation per gate and checks the run
// stops non-zero at the gate that names it: a data race fails the race-enabled unit
// command, and a package at 40% fails the coverage floor.
//
//dsh:test DH-T-TOOLCHAIN-2, DH-T-TOOLCHAIN-7
func TestSeededViolationFailsNamedGate(t *testing.T) {
	tests := []struct {
		module, gate, detail string
	}{
		{"race-seeded", "test", "data race detected"},
		{"budget-race", "test", "failing tests: TestBudgetRacy; data race detected"},
		{"unformatted-file", "gofmt", "add.go"},
		{"coverage-40pct", "coverage", "example.com/cov40/p: 40% < 85%"},
		{"untidy-module", "modules", "-require golang.org/x/mod v0.43.0"},
	}
	for _, tt := range tests {
		t.Run(tt.module, func(t *testing.T) {
			out, code := runGate(t, scratch(t, tt.module))
			assertStoppedAt(t, out, code, tt.gate)
			if !strings.Contains(out, tt.detail) {
				t.Errorf("output lacks %q:\n%s", tt.detail, out)
			}
		})
	}
}

// TestCoverageMergesChildProcesses runs the coverage gate on a package whose
// statements are split between the test process, a re-executed test binary and a
// separately built -cover helper binary. Only with both children's GOCOVERDIR
// profiles merged by go tool covdata does p reach the floor; dropping either child
// leaves p at 70%.
//
//dsh:test DH-T-TOOLCHAIN-7
func TestCoverageMergesChildProcesses(t *testing.T) {
	tests := []struct {
		name, env, fail string
	}{
		{"both children", "", ""},
		{"no re-executed child", "COVCHILD_NO_REEXEC=1", "example.com/covchild/p: 70% < 85%"},
		{"no helper binary", "COVCHILD_NO_HELPER=1", "example.com/covchild/p: 70% < 85%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newTestConfig(t, scratch(t, "coverage-child"))
			if tt.env != "" {
				cfg.Environ = setEnv(cfg.Environ, tt.env)
			}
			var out bytes.Buffer
			err := cfg.coverageGate(context.Background(), &out)
			switch {
			case tt.fail == "" && err != nil:
				t.Fatalf("coverage gate: %v\n%s", err, out.String())
			case tt.fail == "" && !strings.Contains(out.String(), "coverage: example.com/covchild/p 100%"):
				t.Errorf("p is not at 100%%:\n%s", out.String())
			case tt.fail != "" && (err == nil || !strings.Contains(err.Error(), tt.fail)):
				t.Errorf("coverage gate = %v, want %q\n%s", err, tt.fail, out.String())
			}
			if !strings.Contains(out.String(), "go tool covdata textfmt") {
				t.Errorf("profiles were not merged with go tool covdata:\n%s", out.String())
			}
		})
	}
}

// TestCoverageBinaryIsBounded runs the coverage gate on a test that never returns:
// the binary's -test.timeout fails the gate and names the test instead of hanging.
func TestCoverageBinaryIsBounded(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "coverage-blocked"))
	cfg.TestTimeout = 3 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var out bytes.Buffer
	err := cfg.coverageGate(ctx, &out)
	if err == nil {
		t.Fatalf("coverage gate passed a blocked test:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "test timed out: TestBlocked") {
		t.Errorf("error = %v, want it to name the timed-out TestBlocked\n%s", err, out.String())
	}
	if ctx.Err() != nil {
		t.Errorf("gate returned only after the outer deadline: %v", ctx.Err())
	}
}

// TestNoExemptionFileIsHonoured writes every plausible baseline, suppression and
// exemption file listing p next to the under-floor module; the gate still fails p
// (QG-2; DH-T-TOOLCHAIN-8).
func TestNoExemptionFileIsHonoured(t *testing.T) {
	root := scratch(t, "coverage-40pct")
	exempt := "example.com/cov40/p\np\n"
	for _, name := range []string{
		".coverignore", "coverage-exempt.txt", "coverage.exemptions", ".harness-gate-baseline",
		"harness-gate.baseline", "baseline.json", ".nocover", "p/.coverignore", "p/.nocover",
	} {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(exempt), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runGate(t, root)
	assertStoppedAt(t, out, code, "coverage")
	if !strings.Contains(out, "example.com/cov40/p:") {
		t.Errorf("output does not name p:\n%s", out)
	}
}

func TestOwnAnalyzersRunInVetGate(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module example.com/vetseeded\n\ngo 1.27.4\n",
		"x.go":      "// Package x is seeded for the vet gate.\npackage x\n",
		"x_test.go": "package x\n\nimport (\n\t\"testing\"\n\t\"time\"\n)\n\nfunc TestX(t *testing.T) {\n\ttime.Sleep(1)\n}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runGate(t, root)
	assertStoppedAt(t, out, code, "vet")
	if !strings.Contains(out, "time.Sleep in a test file") {
		t.Errorf("output lacks the nosleep diagnostic:\n%s", out)
	}
}

// gateNamed is the gate called name of cfg's gate list.
func gateNamed(t *testing.T, cfg *Config, name string) Gate {
	t.Helper()
	for _, g := range Gates(cfg) {
		if g.Name == name {
			return g
		}
	}
	t.Fatalf("no gate %q", name)
	return Gate{}
}

func newTestConfig(t *testing.T, root string) *Config {
	t.Helper()
	cfg, err := NewConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestLayerTaggedTestsAreExcludedFromDefaultGate: replay- and scenario-tagged tests
// that always fail leave the test and coverage gates passing, even when GOFLAGS asks
// for the tags; built with -tags scenario (or replay) they fail (DH-ADR-TOOLCHAIN-4;
// DH-T-TOOLCHAIN-4).
func TestLayerTaggedTestsAreExcludedFromDefaultGate(t *testing.T) {
	root := scratch(t, "layer-tagged")
	for _, goflags := range []string{"", "-tags=" + strings.Join(LayerTags, ",")} {
		t.Run("GOFLAGS="+goflags, func(t *testing.T) {
			cfg := newTestConfig(t, root)
			cfg.Environ = setEnv(cfg.Environ, "GOFLAGS="+goflags)
			for _, gate := range []Gate{gateNamed(t, cfg, "test"), gateNamed(t, cfg, "coverage")} {
				var out bytes.Buffer
				if err := gate.Run(context.Background(), &out); err != nil {
					t.Errorf("%s gate: %v\n%s", gate.Name, err, out.String())
				}
				if strings.Contains(out.String(), "AlwaysFails") {
					t.Errorf("%s gate ran a layer test:\n%s", gate.Name, out.String())
				}
			}
		})
	}
	for tag, test := range map[string]string{"scenario": "TestScenarioAlwaysFails", "replay": "TestReplayAlwaysFails"} {
		cfg := newTestConfig(t, root)
		err := cfg.goStep(context.Background(), &bytes.Buffer{}, root, cfg.env(), "test", "-tags", tag, "-count=1", "./...")
		if err == nil || !strings.Contains(err.Error(), "failing tests: "+test) {
			t.Errorf("go test -tags %s = %v, want %s to fail", tag, err, test)
		}
	}
}

// TestFuzzCheckRunsInVetGate runs the vet gate, through the built vet tool, on a
// marked package whose test files go vet analyzes with it: seeded, it passes;
// without seeds_test.go, the fuzzcheck finding fails the gate.
func TestFuzzCheckRunsInVetGate(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/fuzzseeded\n\ngo 1.27.4\n",
		"p.go":          "// Package p decodes untrusted frames.\npackage p\n\n//dsh:untrusted-parser\n\n// Decode returns the frame length.\nfunc Decode(b []byte) int { return len(b) }\n",
		"p_test.go":     "package p\n\nimport \"testing\"\n\nfunc TestDecode(t *testing.T) {\n\tif Decode(nil) != 0 {\n\t\tt.Fatal(\"Decode\")\n\t}\n}\n",
		"seeds_test.go": "package p\n\nimport \"testing\"\n\nvar seeds = [][]byte{[]byte(\"{}\")}\n\nfunc FuzzDecode(f *testing.F) {\n\tfor _, s := range seeds {\n\t\tf.Add(s)\n\t}\n\tf.Fuzz(func(t *testing.T, b []byte) { Decode(b) })\n}\n",
	}
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg := newTestConfig(t, root)
	cfg.VetTool = buildGate(t)
	var out bytes.Buffer
	if err := cfg.vetGate(context.Background(), &out); err != nil {
		t.Fatalf("seeded package: vet gate = %v\n%s", err, out.String())
	}

	if err := os.Rename(filepath.Join(root, "seeds_test.go"), filepath.Join(root, "fuzz_test.go")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err := cfg.vetGate(context.Background(), &out)
	if err == nil || !strings.Contains(out.String(), "has no seeds_test.go") {
		t.Errorf("without seeds_test.go: vet gate = %v, want the fuzzcheck finding\n%s", err, out.String())
	}

	// p_test.go remains: go vet judges the package with its tests, and still reports.
	if err := os.Remove(filepath.Join(root, "fuzz_test.go")); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = cfg.vetGate(context.Background(), &out)
	if err == nil || !strings.Contains(out.String(), "p.go:4:1: package is marked //dsh:untrusted-parser but has no fuzz target") {
		t.Errorf("without a fuzz target: vet gate = %v, want the fuzzcheck finding at the marker\n%s", err, out.String())
	}
}

// TestBudgetGateClearsOverrides runs the budget gate with DSH_* and GO* overrides in
// the parent environment; the budget test would relax itself if one reached it.
//
//dsh:test DH-T-TOOLCHAIN-12
func TestBudgetGateClearsOverrides(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "budget-exceeded"))
	cfg.Environ = append(slices.Clone(cfg.Environ),
		"DSH_BUDGET_RELAX=1", "GOGC=off", "GOMAXPROCS=1", "GODEBUG=gctrace=0", "GOFLAGS=-count=2")
	var out bytes.Buffer
	err := cfg.budgetGate(context.Background(), &out)
	if err == nil {
		t.Fatalf("budget gate passed, want TestBudgetX over its budget:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "TestBudgetX") {
		t.Errorf("error does not name TestBudgetX: %v", err)
	}
	if strings.Contains(out.String(), "override leaked") {
		t.Errorf("an override reached the budget child:\n%s", out.String())
	}
}

// TestBudgetEnv: the budget child's environment drops every DSH_* and tuning GO*
// variable of the parent, so no override can change a compiled-in budget.
//
//dsh:test DH-T-TOOLCHAIN-12
func TestBudgetEnv(t *testing.T) {
	parent := []string{"PATH=/bin", "DSH_X=1", "GOGC=off", "GOMAXPROCS=1", "GOFLAGS=-v", "GOWORK=/w", "GOTOOLCHAIN=go1.99",
		"GOCACHE=/c", "GOMODCACHE=/m", "GOPATH=/p", "GOROOT=/r", "CGO_ENABLED=1"}
	got := BudgetEnv(parent)
	want := []string{"PATH=/bin", "GOCACHE=/c", "GOMODCACHE=/m", "GOPATH=/p", "GOROOT=/r", "CGO_ENABLED=1", "GOENV=off", "GOFLAGS=", "GOWORK=off", "GOTOOLCHAIN=local"}
	if !slices.Equal(got, want) {
		t.Errorf("BudgetEnv = %v\nwant        %v", got, want)
	}
}

// TestRaceUnavailableFailsTheTestGate: a lane where -race cannot run fails the gate,
// named, and is never reported as skipped (QG-4).
func TestRaceUnavailableFailsTheTestGate(t *testing.T) {
	cfg := &Config{RacePreflight: func(context.Context, *Config) error {
		return errors.New("race: -race is not supported on this lane")
	}}
	var out bytes.Buffer
	results := Run(context.Background(), &out, []Gate{gateNamed(t, cfg, "test")})
	if len(results) != 1 || results[0].Passed() || results[0].Name != "test" {
		t.Fatalf("results = %+v, want the test gate failed", results)
	}
	if !strings.Contains(out.String(), "FAILED at gate test") || strings.Contains(strings.ToLower(out.String()), "skip") {
		t.Errorf("output:\n%s", out.String())
	}
}

func TestCCompilerPreflightNamesMissingCompiler(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "race-seeded"))
	cfg.Environ = setEnv(cfg.Environ, "CC=no-such-c-compiler-for-harness-gate")
	err := cCompilerPreflight(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "race: no C compiler") {
		t.Errorf("preflight = %v, want race: no C compiler", err)
	}
}

// TestRepositoryPassesEveryGate runs the built harness-gate binary on the repository
// root, exactly as the documented command does: every gate, in order, must pass on
// the product modules (an analyzer or floor joins the required set only while the
// tree is clean under it).
func TestRepositoryPassesEveryGate(t *testing.T) {
	out, code := runGate(t, filepath.Join("..", ".."))
	if code != 0 || !strings.Contains(out, fmt.Sprintf("PASSED all %d gates", len(Gates(&Config{})))) {
		t.Fatalf("repository gate exited %d:\n%s", code, out)
	}
	for _, gate := range Gates(&Config{}) {
		if !strings.Contains(out, "PASS "+gate.Name+" (") {
			t.Errorf("gate %s did not report PASS:\n%s", gate.Name, out)
		}
	}
}

func TestCrossCompileBuildsSixTargets(t *testing.T) {
	cfg := newTestConfig(t, filepath.Join("..", ".."))
	cfg.CrossOut = t.TempDir()
	var out bytes.Buffer
	if err := cfg.crossCompileGate(context.Background(), &out); err != nil {
		t.Fatalf("cross-compile gate: %v\n%s", err, out.String())
	}
	entries, err := os.ReadDir(cfg.CrossOut)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	want := []string{"dsh-darwin-amd64", "dsh-darwin-arm64", "dsh-linux-amd64", "dsh-linux-arm64", "dsh-windows-amd64.exe", "dsh-windows-arm64.exe"}
	if !slices.Equal(got, want) {
		t.Errorf("binaries = %v, want %v", got, want)
	}
}

func TestCrossCompileNamesFailingTarget(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "cross-broken"))
	err := cfg.crossCompileGate(context.Background(), &bytes.Buffer{})
	if err == nil {
		t.Fatal("cross-compile gate passed, want windows/arm64 to fail")
	}
	msg := err.Error()
	if !strings.Contains(msg, "windows/arm64:") {
		t.Errorf("error does not name windows/arm64: %v", err)
	}
	for _, ok := range []string{"linux/amd64:", "darwin/arm64:", "windows/amd64:"} {
		if strings.Contains(msg, ok) {
			t.Errorf("error names %s, which builds: %v", ok, err)
		}
	}
}

func TestTraceGateFailsWithoutTool(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "race-seeded"))
	err := cfg.traceGate(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "tools/harness-trace not found") {
		t.Errorf("trace gate = %v, want a failure naming the missing tool", err)
	}
}

func TestGofmtSkipsTestdataAndNestedModules(t *testing.T) {
	files, err := goFiles(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.Contains(filepath.ToSlash(f), "testdata/") {
			t.Errorf("goFiles lists %s", f)
		}
	}
	if !slices.Contains(files, "main.go") {
		t.Errorf("goFiles misses main.go: %v", files)
	}
}
