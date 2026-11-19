package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
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
	names := []string{"gofmt", "vet", "test", "coverage", "trace", "budget", "cross-compile"}
	for _, later := range names[slices.Index(names, gate)+1:] {
		if strings.Contains(out, " "+later+" -- ") {
			t.Errorf("gate %q started after %q failed:\n%s", later, gate, out)
		}
	}
}

func TestSeededViolationFailsNamedGate(t *testing.T) {
	tests := []struct {
		module, gate, detail string
	}{
		{"race-seeded", "test", "data race detected"},
		{"unformatted-file", "gofmt", "add.go"},
		{"coverage-40pct", "coverage", "example.com/cov40/p: 40% < 85%"},
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
		"go.mod":    "module example.com/vetseeded\n\ngo 1.26.0\n",
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

func newTestConfig(t *testing.T, root string) *Config {
	t.Helper()
	cfg, err := NewConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// TestBudgetGateClearsOverrides runs the budget gate with DSH_* and GO* overrides in
// the parent environment; the budget test would relax itself if one reached it.
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

func TestBudgetEnv(t *testing.T) {
	parent := []string{"PATH=/bin", "DSH_X=1", "GOGC=off", "GOMAXPROCS=1", "GOFLAGS=-v", "GOWORK=/w", "GOTOOLCHAIN=go1.99",
		"GOCACHE=/c", "GOMODCACHE=/m", "GOPATH=/p", "GOROOT=/r", "CGO_ENABLED=1"}
	got := BudgetEnv(parent)
	want := []string{"PATH=/bin", "GOCACHE=/c", "GOMODCACHE=/m", "GOPATH=/p", "GOROOT=/r", "CGO_ENABLED=1", "GOWORK=off", "GOTOOLCHAIN=local"}
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
	results := Run(context.Background(), &out, []Gate{Gates(cfg)[2]})
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
