//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/analyzers"
)

// tlGoldenOverlayTest is added to the real golden package of src/harness through
// "go test -overlay", so the scenario exercises golden.Assert unchanged. It moves
// into the scenario's temporary directory first, so the relative testdata/ path of
// the golden resolves there and the repository is never written.
const tlGoldenOverlayTest = `package golden_test

import (
	"os"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

func TestE2EGoldenDrift(t *testing.T) {
	t.Chdir(os.Getenv("E2E_GOLDEN_DIR"))
	golden.Assert(t, os.Getenv("E2E_GOLDEN_NAME"), os.Getenv("E2E_GOLDEN_GOT"))
}
`

// tlGateDriverSource replaces main.go of harness-gate through "go build -overlay":
// it runs one named gate of the real Gates list, through the real Run, on --root.
const tlGateDriverSource = `package main

import (
	"context"
	"fmt"
	"os"
)

func main() {
	cfg, err := NewConfig(os.Args[2])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, g := range Gates(cfg) {
		if g.Name == os.Args[1] {
			os.Exit(ExitCode(Run(context.Background(), os.Stdout, []Gate{g})))
		}
	}
	fmt.Fprintln(os.Stderr, "unknown gate", os.Args[1])
	os.Exit(2)
}
`

var (
	tlBuildOnce sync.Once
	tlBuildDir  string
	tlDriverBin string
	tlBuildErr  error
)

// tlEnv is the child environment: no workspace override, no CI (golden refuses
// -update under CI) and no GOFLAGS, so a scenario sets exactly what it tests.
func tlEnv(extra ...string) []string {
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool {
		k, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(k) {
		case "CI", "GOFLAGS", "GOWORK":
			return true
		}
		return false
	})
	return append(env, extra...)
}

// tlRepoRoot is the repository root, two levels above tools/harness-gate.
func tlRepoRoot() (string, error) {
	mod, err := moduleRoot()
	if err != nil {
		return "", err
	}
	root := filepath.Clean(filepath.Join(mod, "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "src", "harness", "internal", "testkit", "golden", "golden.go")); err != nil {
		return "", fmt.Errorf("golden helper not found under %s: %w", root, err)
	}
	return root, nil
}

func tlBuildDriver() error {
	tlBuildOnce.Do(func() {
		mod, err := moduleRoot()
		if err != nil {
			tlBuildErr = err
			return
		}
		tlBuildDir, err = os.MkdirTemp("", "harness-gate-e2e-layers-")
		if err != nil {
			tlBuildErr = err
			return
		}
		src := filepath.Join(tlBuildDir, "driver.go")
		if err := os.WriteFile(src, []byte(tlGateDriverSource), 0o644); err != nil {
			tlBuildErr = err
			return
		}
		overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(mod, "main.go"): src}})
		ov := filepath.Join(tlBuildDir, "overlay.json")
		if err := os.WriteFile(ov, overlay, 0o644); err != nil {
			tlBuildErr = err
			return
		}
		tlDriverBin = filepath.Join(tlBuildDir, exe("gate-driver"))
		cmd := exec.Command("go", "build", "-overlay", ov, "-o", tlDriverBin, ".")
		cmd.Dir, cmd.Env = mod, tlEnv("GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			tlBuildErr = fmt.Errorf("build gate driver: %v\n%s", err, out)
		}
	})
	return tlBuildErr
}

// tlRun runs cmd and returns its combined output and exit code.
func tlRun(cmd *exec.Cmd) (string, int, error) {
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return string(out), 0, nil
	case errors.As(err, &exit):
		return string(out), exit.ExitCode(), nil
	default:
		return string(out), -1, fmt.Errorf("run %s: %w\n%s", cmd.Path, err, out)
	}
}

type testLayers struct {
	dirs []string

	goldenDir string
	output    string
	exitCode  int

	sources  map[string]string
	findings []analyzers.Finding

	scratch string
}

func (s *testLayers) tempDir() (string, error) {
	dir, err := os.MkdirTemp("", "dsh-e2e-layers-")
	if err != nil {
		return "", err
	}
	s.dirs = append(s.dirs, dir)
	return dir, nil
}

func (s *testLayers) cleanup() {
	for _, d := range s.dirs {
		os.RemoveAll(d)
	}
}

// --- golden helper --------------------------------------------------------------

func (s *testLayers) goldenPath(name string) string {
	return filepath.Join(s.goldenDir, "testdata", filepath.FromSlash(name)+".golden")
}

func (s *testLayers) aGoldenFileWithContent(name, content string) error {
	dir, err := s.tempDir()
	if err != nil {
		return err
	}
	s.goldenDir = dir
	p := s.goldenPath(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(content), 0o644)
}

func (s *testLayers) goldenAssert(name, got string, update bool) error {
	root, err := tlRepoRoot()
	if err != nil {
		return err
	}
	pkgDir := filepath.Join(root, "src", "harness", "internal", "testkit", "golden")
	work, err := s.tempDir()
	if err != nil {
		return err
	}
	testSrc := filepath.Join(work, "e2e_golden_test.go")
	if err := os.WriteFile(testSrc, []byte(tlGoldenOverlayTest), 0o644); err != nil {
		return err
	}
	overlay, _ := json.Marshal(map[string]map[string]string{"Replace": {filepath.Join(pkgDir, "zz_e2e_golden_test.go"): testSrc}})
	ov := filepath.Join(work, "overlay.json")
	if err := os.WriteFile(ov, overlay, 0o644); err != nil {
		return err
	}
	args := []string{"test", "-overlay", ov, "-count=1", "-run", "^TestE2EGoldenDrift$", "-v", "."}
	if update {
		args = append(args, "-args", "-update")
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = pkgDir
	cmd.Env = tlEnv("E2E_GOLDEN_DIR="+s.goldenDir, "E2E_GOLDEN_NAME="+name, "E2E_GOLDEN_GOT="+got)
	s.output, s.exitCode, err = tlRun(cmd)
	if err != nil {
		return err
	}
	if !strings.Contains(s.output, "TestE2EGoldenDrift") {
		return fmt.Errorf("the overlay test did not run:\n%s", s.output)
	}
	return nil
}

func (s *testLayers) goldenAssertWithout(name, got string) error {
	return s.goldenAssert(name, got, false)
}

func (s *testLayers) goldenAssertWith(name, got string) error {
	return s.goldenAssert(name, got, true)
}

func (s *testLayers) goldenTestFailsWithDiff(want, got string) error {
	if s.exitCode == 0 || !strings.Contains(s.output, "--- FAIL: TestE2EGoldenDrift") {
		return fmt.Errorf("want the golden test to fail; exit %d:\n%s", s.exitCode, s.output)
	}
	for _, frag := range []string{"differs", "--- want", "+++ got", "-" + want, "+" + got} {
		if !strings.Contains(s.output, frag) {
			return fmt.Errorf("failure output lacks %q:\n%s", frag, s.output)
		}
	}
	return nil
}

func (s *testLayers) goldenTestPasses() error {
	if s.exitCode != 0 || !strings.Contains(s.output, "--- PASS: TestE2EGoldenDrift") {
		return fmt.Errorf("want the golden test to pass; exit %d:\n%s", s.exitCode, s.output)
	}
	return nil
}

func (s *testLayers) goldenFileHolds(name, want string) error {
	data, err := os.ReadFile(s.goldenPath(name))
	if err != nil {
		return err
	}
	if string(data) != want {
		return fmt.Errorf("golden %s holds %q, want %q", name, data, want)
	}
	return nil
}

// --- fuzzcheck ------------------------------------------------------------------

const tlMarkedSource = `// Package frame decodes untrusted frames.
package frame

//dsh:untrusted-parser

// Decode returns the frame length.
func Decode(b []byte) int { return len(b) }
`

const tlPlainTest = `package frame

import "testing"

func TestDecode(t *testing.T) {
	if Decode(nil) != 0 {
		t.Fatal("Decode(nil) != 0")
	}
}
`

const tlSeedsTest = `package frame

import "testing"

func FuzzDecode(f *testing.F) {
	f.Add([]byte("{}"))
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b) })
}
`

func (s *testLayers) aMarkedPackageWithoutFuzz() error {
	s.sources = map[string]string{"frame.go": tlMarkedSource, "frame_test.go": tlPlainTest}
	return nil
}

func (s *testLayers) fuzzcheckRuns() error {
	fset := token.NewFileSet()
	names := make([]string, 0, len(s.sources))
	for n := range s.sources {
		names = append(names, n)
	}
	slices.Sort(names)
	var files []analyzers.PackageFile
	for _, n := range names {
		f, err := parser.ParseFile(fset, n, s.sources[n], parser.ParseComments)
		if err != nil {
			return err
		}
		files = append(files, analyzers.PackageFile{Name: n, File: f})
	}
	s.findings = analyzers.FuzzCheckFindings(files)
	return nil
}

func (s *testLayers) exactlyOneMissingTargetDiagnostic() error {
	if len(s.findings) != 1 {
		return fmt.Errorf("want 1 diagnostic, got %d: %v", len(s.findings), s.findings)
	}
	msg := s.findings[0].Message
	if !strings.Contains(msg, analyzers.UntrustedParserMarker) || !strings.Contains(msg, "no fuzz target") {
		return fmt.Errorf("diagnostic %q does not name the missing fuzz target", msg)
	}
	return nil
}

func (s *testLayers) fuzzTargetAdded(signature string) error {
	if !strings.Contains(tlSeedsTest, signature) {
		return fmt.Errorf("seeds_test.go source does not declare %q", signature)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), "seeds_test.go", tlSeedsTest, 0); err != nil {
		return err
	}
	s.sources["seeds_test.go"] = tlSeedsTest
	return nil
}

func (s *testLayers) noDiagnostic() error {
	if len(s.findings) != 0 {
		var msgs []string
		for _, f := range s.findings {
			msgs = append(msgs, f.Message)
		}
		return fmt.Errorf("want no diagnostic, got %d:\n%s", len(msgs), strings.Join(msgs, "\n"))
	}
	return nil
}

// --- test layers in the gate ----------------------------------------------------

func (s *testLayers) aScratchModuleWithScenarioTest(file, tag string) error {
	mod, err := moduleRoot()
	if err != nil {
		return err
	}
	src := filepath.Join(mod, "testdata", "layer-tagged")
	dst, err := s.tempDir()
	if err != nil {
		return err
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
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
	data, err := os.ReadFile(filepath.Join(dst, file))
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(data, []byte(tag+"\n")) && !bytes.HasPrefix(data, []byte(tag+"\r\n")) {
		return fmt.Errorf("%s does not start with %q", file, tag)
	}
	if !bytes.Contains(data, []byte("t.Fatal(")) {
		return fmt.Errorf("%s does not always fail", file)
	}
	s.scratch = dst
	return nil
}

func (s *testLayers) runTestGate(extraEnv ...string) error {
	if err := tlBuildDriver(); err != nil {
		return err
	}
	cmd := exec.Command(tlDriverBin, "test", s.scratch)
	cmd.Env = tlEnv(extraEnv...)
	var err error
	s.output, s.exitCode, err = tlRun(cmd)
	return err
}

func (s *testLayers) theTestGateRuns() error { return s.runTestGate() }

func (s *testLayers) theTestGateRunsWithGoflags(flags string) error {
	return s.runTestGate("GOFLAGS=" + flags)
}

func (s *testLayers) theTestGatePasses() error {
	if s.exitCode != 0 {
		return fmt.Errorf("test gate exit %d, want 0:\n%s", s.exitCode, s.output)
	}
	if strings.Contains(s.output, "AlwaysFails") {
		return fmt.Errorf("test gate ran a layer-tagged test:\n%s", s.output)
	}
	return nil
}

func (s *testLayers) gateOutputDoesNotMention(text string) error {
	if strings.Contains(s.output, text) {
		return fmt.Errorf("gate output mentions %q:\n%s", text, s.output)
	}
	return nil
}

func (s *testLayers) goTestRunsWithTags(flags string) error {
	args := append([]string{"test", "-count=1"}, strings.Fields(flags)...)
	cmd := exec.Command("go", append(args, "./...")...)
	cmd.Dir, cmd.Env = s.scratch, tlEnv("GOWORK=off")
	var err error
	s.output, s.exitCode, err = tlRun(cmd)
	return err
}

func (s *testLayers) goTestFailsWith(test string) error {
	if s.exitCode == 0 || !strings.Contains(s.output, "--- FAIL: "+test) {
		return fmt.Errorf("want %s to fail; exit %d:\n%s", test, s.exitCode, s.output)
	}
	return nil
}

// InitializeScenario_repository_modules_and_quality_gates_test_layers_and_golden_helper
// registers the steps of Stage 1.3 (test layers and golden helper).
func InitializeScenario_repository_modules_and_quality_gates_test_layers_and_golden_helper(ctx *godog.ScenarioContext) {
	s := &testLayers{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.cleanup()
		return c, err
	})

	ctx.Step(`^a golden file "([^"]*)" in a temporary directory with content "([^"]*)"$`, s.aGoldenFileWithContent)
	ctx.Step(`^golden\.Assert is called for "([^"]*)" with "([^"]*)" without -update$`, s.goldenAssertWithout)
	ctx.Step(`^golden\.Assert is called for "([^"]*)" with "([^"]*)" with -update$`, s.goldenAssertWith)
	ctx.Step(`^the golden test fails with a diff from "([^"]*)" to "([^"]*)"$`, s.goldenTestFailsWithDiff)
	ctx.Step(`^the golden test passes$`, s.goldenTestPasses)
	ctx.Step(`^the golden file "([^"]*)" (?:still )?holds "([^"]*)"$`, s.goldenFileHolds)

	ctx.Step(`^in-memory source of a package marked //dsh:untrusted-parser with no Fuzz function$`, s.aMarkedPackageWithoutFuzz)
	ctx.Step(`^the fuzzcheck predicate runs on the parsed AST$`, s.fuzzcheckRuns)
	ctx.Step(`^it reports exactly one diagnostic naming the missing fuzz target$`, s.exactlyOneMissingTargetDiagnostic)
	ctx.Step(`^"([^"]*)" with compiled-in seeds is added in seeds_test\.go$`, s.fuzzTargetAdded)
	ctx.Step(`^it reports no diagnostic$`, s.noDiagnostic)

	ctx.Step(`^a scratch module containing "([^"]*)" with "([^"]*)" that always fails$`, s.aScratchModuleWithScenarioTest)
	ctx.Step(`^the harness-gate test gate runs on the scratch module$`, s.theTestGateRuns)
	ctx.Step(`^the harness-gate test gate runs on the scratch module with GOFLAGS "([^"]*)"$`, s.theTestGateRunsWithGoflags)
	ctx.Step(`^the test gate passes$`, s.theTestGatePasses)
	ctx.Step(`^the test gate output does not mention "([^"]*)"$`, s.gateOutputDoesNotMention)
	ctx.Step(`^go test runs on the scratch module with "([^"]*)"$`, s.goTestRunsWithTags)
	ctx.Step(`^go test fails with "([^"]*)"$`, s.goTestFailsWith)
}

func TestE2E_repository_modules_and_quality_gates_test_layers_and_golden_helper(t *testing.T) {
	t.Cleanup(func() {
		if tlBuildDir != "" {
			os.RemoveAll(tlBuildDir)
		}
	})
	suite := godog.TestSuite{
		Name:                "repository_modules_and_quality_gates_test_layers_and_golden_helper",
		ScenarioInitializer: InitializeScenario_repository_modules_and_quality_gates_test_layers_and_golden_helper,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"repository_modules_and_quality_gates_test_layers_and_golden_helper.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog suite failed")
	}
}
