package main

import (
	"bytes"
	"context"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// TestAuthoredScenariosAreIndexedAndCompileButDoNotRun: on the repository, the
// scenario-index gate passes with DH-E-TOOLCHAIN-2 authored and indexed under
// DH-S-18 and DH-E-TOOLCHAIN-1 indexed under DH-S-17 (authored at DH-S-11, its
// file written by Stage 11.4); "go vet -tags scenario" compiles the scenario
// files; and the default test run of the gate (-tags=) lists no scenario test.
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
	if files["DH-E-TOOLCHAIN-1"] != "src/harness/internal/testkit/scenarios/toolchain_record_replay_scenario_test.go:TestRecordThenReplayWithoutAKey" {
		t.Errorf("DH-E-TOOLCHAIN-1 file = %q", files["DH-E-TOOLCHAIN-1"])
	}
	// DH-S-9's, DH-S-10's and DH-S-11's scenarios are authored ahead of the stages
	// they exercise: one file each, indexed under DH-S-17 with the stages they
	// require, and the files and the helpers they call import only the product
	// boundary.
	for id, want := range map[string]struct {
		file, authored string
		requires       []string
	}{
		"DH-E-LLM-1":     {"llm_turn_scenario_test.go:TestTurnWithReasoningAndToolCall", "DH-S-9", nil},
		"DH-E-LLM-3":     {"llm_signed_out_scenario_test.go:TestSignedOutIsAFact", "DH-S-9", nil},
		"DH-E-VIEW-1":    {"view_title_scenario_test.go:TestModelTitlesSession", "DH-S-9", nil},
		"DH-E-TOOLS-1":   {"tools_pipeline_scenario_test.go:TestGuardedPipelineRefusesTheWrite", "DH-S-10", nil},
		"DH-E-SESSION-1": {"session_rebuild_scenario_test.go:TestRequestsReceivedEqualRequestsRebuilt", "DH-S-11", nil},
		"DH-E-SESSION-2": {"session_kill_resume_scenario_test.go:TestKilledDuringToolCallResumesIntoAcceptedHistory", "DH-S-11", []string{"DH-S-11", "DH-S-13", "DH-S-16"}},
		"DH-E-AGENT-1":   {"agent_turn_scenario_test.go:TestRealMultiStepTurnIsBracketed", "DH-S-11", nil},
		"DH-E-AGENT-2":   {"agent_cancel_scenario_test.go:TestCancelledStreamKeepsWhatWasShown", "DH-S-11", nil},
		"DH-E-AGENT-3":   {"agent_stop_kill_scenario_test.go:TestStopAndKillLeaveAnHonestLog", "DH-S-11", []string{"DH-S-11", "DH-S-13", "DH-S-16"}},
		"DH-E-AGENT-4":   {"agent_budget_scenario_test.go:TestBudgetStopsARealToolLoop", "DH-S-11", nil},
	} {
		requires := want.requires
		if requires == nil {
			requires = []string{"DH-S-11", "DH-S-16"}
		}
		if files[id] != "src/harness/internal/testkit/scenarios/"+want.file {
			t.Errorf("%s file = %q, want %s", id, files[id], want.file)
		}
		if where[id] != "DH-S-17 authored "+want.authored {
			t.Errorf("%s index entry = %q", id, where[id])
		}
		if s, _ := x.Stage("DH-S-17"); !slices.ContainsFunc(s.Scenarios, func(e acceptance.Entry) bool {
			return e.ID == id && slices.Equal(e.Requires, requires)
		}) {
			t.Errorf("%s does not require %v", id, requires)
		}
	}
	for _, helper := range []string{"sdkhost_test.go", "binary_test.go", "realenv_test.go", "signedin_routes_test.go", "signedin_routes_copilot_test.go", "session_rebuild_scenario_test.go", "session_kill_resume_scenario_test.go", "agent_turn_scenario_test.go", "agent_cancel_scenario_test.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(cfg.Root, "src", "harness", "internal", "testkit", "scenarios", helper), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range f.Imports {
			if path, _ := strconv.Unquote(spec.Path.Value); !acceptance.BoundaryImport(path) {
				t.Errorf("%s, called by DH-S-9's scenarios, imports %s", helper, path)
			}
		}
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
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/x\n\ngo 1.27.4\n")
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

// unsetModelVariables removes from the process environment, for the rest of the
// test, every variable that could carry a model credential, so the preflight runs
// with no model variable at all.
func unsetModelVariables(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		for _, marker := range []string{"API_KEY", "DEEPSEEK", "ANTHROPIC", "OPENAI", "CLAUDE", "COPILOT"} {
			if strings.Contains(upper, marker) {
				t.Setenv(name, "") // restores the value when the test ends
				os.Unsetenv(name)
				break
			}
		}
	}
}

// TestPreflightSubcommand drives "harness-gate preflight" on the repository through
// the process environment, as the scenario workflow does. No model variable is set:
// the preflight reads no key (models-signed-in-claude-copilot; M1-G-TOOL-15;
// DH-T-TOOLCHAIN-6).
func TestPreflightSubcommand(t *testing.T) {
	root := filepath.Join("..", "..")
	tests := []struct {
		name, require string
		code          int
		want          string
	}{
		{"unknown required scenario", "DH-E-FAKE-9", 1, "required scenario DH-E-FAKE-9"},
		{"empty required list", "", 0, "harness-gate preflight: passed; 0 required scenarios"},
		{"required list", "DH-E-TOOLCHAIN-2", 0, "harness-gate preflight: passed; 1 required scenarios"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ghOut := filepath.Join(t.TempDir(), "github_output")
			unsetModelVariables(t)
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
		// DH-S-13 (#149) added its scenarios to DH-S-18 and DH-S-39 in the index.
		{"DH-S-18", 0, "DH-E-TOOLCHAIN-2,DH-E-CHOKIDAR-1,DH-E-CHOKIDAR-2,DH-E-NODEFS-1,DH-E-RIPGREP-1,DH-E-RIPGREP-2,DH-E-EXEC-1,DH-E-HOST-4,DH-E-EXEC-10,DH-E-STORAGE-6,DH-E-FS-1,DH-E-FS-2,DH-E-VIEW-5," +
			"DH-E-SANDBOX-1,DH-E-NATIVESYSTEM-1,DH-E-NATIVESYSTEM-2,DH-E-NATIVESYSTEM-3,DH-E-NATIVESYSTEM-4,DH-E-CONTEXT-2\n"},
		{"DH-S-17", 0, "DH-E-SDK-1,DH-E-TOOLCHAIN-1,DH-E-LOADER-1,DH-E-LLM-1,DH-E-LLM-3,DH-E-VIEW-1,DH-E-TOOLS-1,DH-E-SESSION-1,DH-E-SESSION-2,DH-E-AGENT-1,DH-E-AGENT-2,DH-E-AGENT-3,DH-E-AGENT-4,DH-E-SANDBOX-7\n"},
		{"DH-S-21", 0, "DH-E-BOOT-2\n"},
		// DH-S-15 (stage 5.9) added DH-E-HOST-1/2 to DH-S-22 and DH-E-HOST-7/8/9 to DH-S-39;
		// DH-S-16 (stage 6.11) added SDK-1, BUNDLE-3, CLI-1/2, SDK-2, UTIL-2, BUNDLE-1/2/4 and API-5.
		{"DH-S-22", 0, "DH-E-BOOT-1,DH-E-HUMAN-6,DH-E-HOST-1,DH-E-HOST-2,DH-E-BUNDLE-3\n"},
		{"DH-S-27", 0, "DH-E-CLI-1,DH-E-CLI-2\n"},
		{"DH-S-39", 0, "DH-E-UTIL-1,DH-E-STORAGE-3,DH-E-TOOLS-3,DH-E-SANDBOX-2,DH-E-EXEC-7,DH-E-EXEC-8,DH-E-HOST-7,DH-E-HOST-8,DH-E-HOST-9,DH-E-SDK-2,DH-E-UTIL-2\n"},
		{"DH-S-48", 0, "DH-E-BUNDLE-1,DH-E-BUNDLE-2,DH-E-BUNDLE-4,DH-E-API-5\n"},
		{"DH-S-16", 0, "DH-E-STORE-1,DH-E-STORE-2,DH-E-STORE-6,DH-E-FS-3\n"},
		{"DH-S-35", 0, "DH-E-STORE-5\n"},
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

// hostedLanes is the hosted CI runner list of decision platforms-windows-wsl: the
// harness lanes on GitHub-hosted runners are regression signals on Linux and
// Windows only; macOS is out of M1 scope, and the evidence of record is the local
// Windows and WSL lanes (lanes/).
var hostedLanes = []string{"ubuntu-latest", "windows-latest"}

// workflowJob returns the body of the top-level job name of a workflow file: the
// lines after "  name:" up to the next top-level job.
func workflowJob(t *testing.T, workflow, name string) string {
	t.Helper()
	workflow = strings.ReplaceAll(workflow, "\r\n", "\n")
	_, body, ok := strings.Cut(workflow, "\n  "+name+":\n")
	if !ok {
		t.Fatalf("workflow has no %s job", name)
	}
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		if len(l) > 2 && strings.HasPrefix(l, "  ") && l[2] != ' ' && l[2] != '#' {
			return strings.Join(lines[:i], "\n")
		}
	}
	return body
}

// matrixOS returns the runner list of a job's "os: [...]" matrix line, failing when
// the job has not exactly one.
func matrixOS(t *testing.T, job string) []string {
	t.Helper()
	var lists [][]string
	for _, l := range strings.Split(job, "\n") {
		v, ok := strings.CutPrefix(strings.TrimSpace(l), "os: [")
		if !ok {
			continue
		}
		v, ok = strings.CutSuffix(v, "]")
		if !ok {
			t.Fatalf("matrix line %q is not a flow list", l)
		}
		var list []string
		for _, f := range strings.Split(v, ",") {
			list = append(list, strings.TrimSpace(f))
		}
		lists = append(lists, list)
	}
	if len(lists) != 1 {
		t.Fatalf("job has %d os matrix lines, want 1", len(lists))
	}
	return lists[0]
}

// TestHarnessJobRunsTheGateOnTwoSystems pins the keyless CI lane of
// DH-D-TOOLCHAIN-4 (QG-4, QG-10) under decision platforms-windows-wsl: the harness
// job and the scenario job run on exactly the hosted lanes (ubuntu-latest,
// windows-latest; no macOS), with cgo on and the floor toolchain from
// src/harness/go.mod; the harness job runs the documented gate command, runs the
// rehearsal on Linux only and uploads the output; the manual-check workflow drops
// macOS; the scenario workflow is never triggered by pull requests and reads no
// model key (models-signed-in-claude-copilot) (M1-G-GATE-21, M1-G-TOOL-13;
// DH-T-TOOLCHAIN-9).
func TestHarnessJobRunsTheGateOnTwoSystems(t *testing.T) {
	read := func(name string) string {
		data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", name))
		if err != nil {
			t.Fatal(err)
		}
		return strings.ReplaceAll(string(data), "\r\n", "\n")
	}
	ci := read("ci.yml")
	job := workflowJob(t, ci, "harness")
	if got := matrixOS(t, job); !slices.Equal(got, hostedLanes) {
		t.Errorf("ci.yml harness matrix = %v, want %v (platforms-windows-wsl)", got, hostedLanes)
	}
	for _, want := range []string{
		`CGO_ENABLED: "1"`,
		"go-version-file: src/harness/go.mod",
		"GOWORK=off go -C tools/harness-gate run . --root ../..",
		"actions/upload-artifact@",
	} {
		if !strings.Contains(job, want) {
			t.Errorf("harness job lacks %q", want)
		}
	}
	// The rehearsal step is on the Linux lane only.
	_, rehearsal, ok := strings.Cut(job, "- name: Release rehearsal")
	rehearsal, _, _ = strings.Cut(rehearsal, "\n      - ")
	if !ok || !strings.Contains(rehearsal, "if: runner.os == 'Linux'\n") || !strings.Contains(rehearsal, "GOWORK=off go -C tools/harness-gate run . rehearse --root ../..") {
		t.Errorf("harness job does not run the rehearsal on the Linux lane only:\n%s", rehearsal)
	}
	header, _, _ := strings.Cut(ci, "\nname: ci\n")
	for _, want := range []string{"platforms-windows-wsl", "regression signal", "tools/harness-gate/lanes/"} {
		if !strings.Contains(header, want) {
			t.Errorf("ci.yml header comment lacks %q", want)
		}
	}
	for _, name := range []string{"ci.yml", "harness-scenarios.yml", "harness-manual-checks.yml"} {
		if strings.Contains(read(name), "macos-latest") {
			t.Errorf("%s still names macos-latest", name)
		}
	}
	if got := matrixOS(t, workflowJob(t, read("harness-manual-checks.yml"), "manual-checks")); !slices.Equal(got, hostedLanes) {
		t.Errorf("harness-manual-checks.yml matrix = %v, want %v", got, hostedLanes)
	}
	scen := read("harness-scenarios.yml")
	if got := matrixOS(t, workflowJob(t, scen, "scenarios")); !slices.Equal(got, hostedLanes) {
		t.Errorf("harness-scenarios.yml scenarios matrix = %v, want %v (platforms-windows-wsl)", got, hostedLanes)
	}
	if strings.Contains(scen, "secrets.") || strings.Contains(scen, "API_KEY") {
		t.Error("harness-scenarios.yml reads a model key; models-signed-in-claude-copilot removes it")
	}
	_, on, _ := strings.Cut(scen, "\non:\n")
	on, _, _ = strings.Cut(on, "\njobs:")
	for _, bad := range []string{"pull_request", "push:"} {
		if strings.Contains(on, bad) {
			t.Errorf("harness-scenarios.yml is triggered by %s", bad)
		}
	}
	for _, want := range []string{"workflow_dispatch:", "schedule:", "GOWORK=off go -C tools/harness-gate run . preflight --root ../..", "run . require --root ../.. --stage"} {
		if !strings.Contains(scen, want) {
			t.Errorf("harness-scenarios.yml lacks %q", want)
		}
	}
}
