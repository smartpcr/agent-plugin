package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuthoredScenariosAreIndexedAndCompileButDoNotRun: on the repository, the
// scenario-index gate passes with DH-E-TOOLCHAIN-2 authored and indexed under
// DH-S-18 and DH-E-TOOLCHAIN-1 indexed under DH-S-17 (authored at DH-S-11, no file
// yet); "go vet -tags scenario" compiles the scenario files; and the default test
// run of the gate (-tags=) lists no scenario test.
func TestAuthoredScenariosAreIndexedAndCompileButDoNotRun(t *testing.T) {
	cfg := newTestConfig(t, filepath.Join("..", ".."))
	var out bytes.Buffer
	if err := cfg.scenarioIndexGate(context.Background(), &out); err != nil {
		t.Fatalf("scenario-index gate on the repository: %v", err)
	}
	x, scenarios, err := cfg.acceptance()
	if err != nil {
		t.Fatal(err)
	}
	where := map[string]string{}
	for _, s := range x.Stages {
		for _, e := range s.Scenarios {
			where[e.ID] = s.ID + " authored " + e.Authored
		}
	}
	if where["DH-E-TOOLCHAIN-2"] != "DH-S-18 authored DH-S-1" || where["DH-E-TOOLCHAIN-1"] != "DH-S-17 authored DH-S-11" {
		t.Errorf("index entries = %v", where)
	}
	files := map[string]string{}
	for _, s := range scenarios {
		files[s.ID] = s.File + ":" + s.Test
	}
	if files["DH-E-TOOLCHAIN-2"] != "src/harness/internal/testkit/scenarios/toolchain_rehearsal_scenario_test.go:TestToolchainRehearsal" {
		t.Errorf("scenario files = %v", files)
	}
	if _, ok := files["DH-E-TOOLCHAIN-1"]; ok {
		t.Errorf("DH-E-TOOLCHAIN-1 has a file before DH-S-11: %v", files)
	}

	harness := filepath.Join(cfg.Root, "src", "harness")
	if err := cfg.goStep(context.Background(), &out, harness, cfg.env(), "vet", "-tags", "scenario", "./..."); err != nil {
		t.Errorf("go vet -tags scenario: %v", err)
	}
	list, err := cfg.command(context.Background(), harness, cfg.env(), cfg.Go, "test", noTags, "-list", ".", "./internal/testkit/scenarios/...")
	if err != nil {
		t.Fatalf("go test -list: %v\n%s", err, list)
	}
	if strings.Contains(list, "Test") {
		t.Errorf("the default test run lists scenario tests:\n%s", list)
	}
	tagged, err := cfg.command(context.Background(), harness, cfg.env(), cfg.Go, "test", "-tags", "scenario", "-list", ".", "./internal/testkit/scenarios/...")
	if err != nil || !strings.Contains(tagged, "TestToolchainRehearsal") {
		t.Errorf("go test -tags scenario -list = %v\n%s", err, tagged)
	}
}

func TestScenarioIndexGateFailsWithoutIndex(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "race-seeded"))
	err := cfg.scenarioIndexGate(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "test/harness/acceptance-index.yaml not found") {
		t.Errorf("scenario-index gate = %v, want a failure naming the missing index", err)
	}
}

// TestScenarioIndexGateNamesUnindexedScenario: an authored scenario file the index
// does not list fails the gate, named.
func TestScenarioIndexGateNamesUnindexedScenario(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.26.0\n")
	writeFile(t, filepath.Join(root, "test", "harness", "acceptance-index.yaml"), "built-through: DH-S-1\nstages:\n  DH-S-18: []\n")
	writeFile(t, filepath.Join(root, "y_scenario_test.go"), "//go:build scenario\n\npackage x\n\nimport \"testing\"\n\n//dsh:scenario DH-E-X-1\nfunc TestY(t *testing.T) {}\n")
	cfg := newTestConfig(t, root)
	err := cfg.scenarioIndexGate(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "DH-E-X-1 (y_scenario_test.go: TestY) is not in test/harness/acceptance-index.yaml") {
		t.Errorf("scenario-index gate = %v, want DH-E-X-1 named", err)
	}
}

func writeFile(t *testing.T, p, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestPreflightSubcommand drives "harness-gate preflight" on the repository through
// the process environment, as the scenario workflow does.
func TestPreflightSubcommand(t *testing.T) {
	root := filepath.Join("..", "..")
	tests := []struct {
		name, key, require string
		code               int
		want               string
	}{
		{"missing secret", "", "", 1, "secret DSH_DEEPSEEK_API_KEY is not set"},
		{"unknown required scenario", "k", "DH-E-FAKE-9", 1, "required scenario DH-E-FAKE-9"},
		{"empty required list", "k", "", 0, "0 required scenarios"},
		{"required list", "k", "DH-E-TOOLCHAIN-2", 0, "1 required scenarios"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ghOut := filepath.Join(t.TempDir(), "github_output")
			t.Setenv("DSH_DEEPSEEK_API_KEY", tt.key)
			t.Setenv("DSH_REQUIRE_SCENARIOS", tt.require)
			t.Setenv("GITHUB_OUTPUT", ghOut)
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), []string{"preflight", "--root", root}, &stdout, &stderr)
			if code != tt.code || !strings.Contains(stdout.String(), tt.want) {
				t.Errorf("preflight exited %d, want %d with %q:\n%s%s", code, tt.code, tt.want, stdout.String(), stderr.String())
			}
			written, _ := os.ReadFile(ghOut)
			switch {
			case tt.code != 0 && len(written) != 0:
				t.Errorf("a failed preflight wrote outputs %q", written)
			case tt.require == "DH-E-TOOLCHAIN-2" && string(written) != "count=1\nrun=^(TestToolchainRehearsal)$\ntests=TestToolchainRehearsal\n":
				t.Errorf("outputs = %q", written)
			case tt.code == 0 && tt.require == "" && string(written) != "count=0\nrun=\ntests=\n":
				t.Errorf("outputs = %q", written)
			}
		})
	}
}

func TestRequireSubcommand(t *testing.T) {
	root := filepath.Join("..", "..")
	tests := []struct {
		stage string
		code  int
		out   string
	}{
		{"DH-S-18", 0, "DH-E-TOOLCHAIN-2,DH-E-CHOKIDAR-1,DH-E-CHOKIDAR-2,DH-E-NODEFS-1,DH-E-RIPGREP-1,DH-E-RIPGREP-2,DH-E-EXEC-1,DH-E-HOST-4\n"},
		{"DH-S-17", 0, "DH-E-TOOLCHAIN-1,DH-E-LOADER-1\n"},
		{"DH-S-21", 0, "DH-E-BOOT-2\n"},
		{"DH-S-22", 0, "DH-E-BOOT-1\n"},
		{"DH-S-39", 0, "DH-E-UTIL-1\n"},
		{"DH-S-16", 0, "\n"},
		{"", 0, "\n"},
		{"DH-S-99", 1, ""},
	}
	for _, tt := range tests {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), []string{"require", "--root", root, "--stage", tt.stage}, &stdout, &stderr)
		if code != tt.code || stdout.String() != tt.out {
			t.Errorf("require --stage %q = %d %q, want %d %q (%s)", tt.stage, code, stdout.String(), tt.code, tt.out, stderr.String())
		}
	}
}

func TestSubcommandUsageErrors(t *testing.T) {
	for _, args := range [][]string{
		{"rehearse", "extra"}, {"preflight", "--bogus"}, {"require", "--root", "..", "x"},
		{"preflight", "--root", t.TempDir()}, {"require", "--root", t.TempDir()},
	} {
		if code := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
			t.Errorf("run(%q) = %d, want 2", args, code)
		}
	}
}

// TestHarnessJobRunsTheGateOnThreeSystems pins the keyless CI lane of
// DH-D-TOOLCHAIN-4 (QG-4, QG-10): the harness job runs on Linux, macOS and Windows
// with cgo on, runs the documented gate command, runs the rehearsal on Linux only and
// uploads the output; the scenario workflow is never triggered by pull requests.
func TestHarnessJobRunsTheGateOnThreeSystems(t *testing.T) {
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	ci := read("ci.yml")
	_, job, ok := strings.Cut(ci, "\n  harness:\n")
	if !ok {
		t.Fatal("ci.yml has no harness job")
	}
	for _, want := range []string{
		"os: [ubuntu-latest, macos-latest, windows-latest]",
		`CGO_ENABLED: "1"`,
		"GOWORK=off go -C tools/harness-gate run . --root ../..",
		"GOWORK=off go -C tools/harness-gate run . rehearse --root ../..",
		"if: runner.os == 'Linux'",
		"actions/upload-artifact@",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("harness job lacks %q", want)
		}
	}
	scen := read("harness-scenarios.yml")
	_, on, _ := strings.Cut(scen, "\non:\n")
	on, _, _ = strings.Cut(on, "\njobs:")
	for _, bad := range []string{"pull_request", "push:"} {
		if strings.Contains(on, bad) {
			t.Errorf("harness-scenarios.yml is triggered by %s", bad)
		}
	}
	for _, want := range []string{"workflow_dispatch:", "schedule:", "GOWORK=off go -C tools/harness-gate run . preflight --root ../..", "secrets.DSH_DEEPSEEK_API_KEY", "run . require --root ../.. --stage"} {
		if !strings.Contains(scen, want) {
			t.Errorf("harness-scenarios.yml lacks %q", want)
		}
	}
}
