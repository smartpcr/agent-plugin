package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// No test of this package runs a real client: the probe in force refuses, and a
// test that needs a sign-in injects its own.
func init() {
	signInProbe = func(context.Context, acceptance.Credential) (acceptance.SignInStatus, error) {
		return acceptance.SignInStatus{Command: "(test)", Detail: "refused"}, errors.New("tests never run a real client's status command")
	}
}

// scenarioRoot writes a repository root with one module, src/harness, holding the
// scenario files, and an index listing them under DH-S-17.
func scenarioRoot(t *testing.T, index string, files map[string]string) *Config {
	t.Helper()
	root := t.TempDir()
	mod := filepath.Join(root, "src", "harness")
	writeFile(t, filepath.Join(mod, "go.mod"), "module example.com/harness\n\ngo 1.26\n")
	for name, src := range files {
		writeFile(t, filepath.Join(mod, "s", name), src)
	}
	writeFile(t, filepath.Join(root, filepath.FromSlash(acceptance.IndexPath)), index)
	return &Config{Root: root, Modules: []string{mod}, Go: "go", Environ: setEnv(os.Environ(), "GOFLAGS=", "GOTOOLCHAIN=local")}
}

func scenarioSrc(id, test, body string) string {
	return "//go:build scenario\n\npackage s\n\nimport \"testing\"\n\n//dsh:scenario " + id + "\nfunc " + test + "(t *testing.T) {" + body + "}\n"
}

const scenarioIndex = "built-through: DH-S-17\nstages:\n  DH-S-17:\n" +
	"    - id: DH-E-LLM-1\n      authored: DH-S-9\n      credential: claude-session\n" +
	"    - id: DH-E-STORE-1\n      authored: DH-S-7\n      credential: none\n"

type countingProbe struct {
	signedIn bool
	asked    []acceptance.Credential
}

func (p *countingProbe) probe(_ context.Context, c acceptance.Credential) (acceptance.SignInStatus, error) {
	p.asked = append(p.asked, c)
	return acceptance.SignInStatus{SignedIn: p.signedIn, Command: "fake status", Detail: "loggedIn=" + map[bool]string{true: "true", false: "false"}[p.signedIn]}, nil
}

// TestScenarioGatesStopWhenSignedOut: "harness-gate scenarios" on a stage with a
// claude-session entry and an injected probe reporting signed out fails its
// preflight gate naming the scenario and the client, and the scenarios gate never
// starts (M1-G-TOOL-16; M1-D-TOOL-6 (a)).
//
//dsh:test DH-T-TOOLCHAIN-6
func TestScenarioGatesStopWhenSignedOut(t *testing.T) {
	c := scenarioRoot(t, scenarioIndex, map[string]string{
		"llm_scenario_test.go":   scenarioSrc("DH-E-LLM-1", "TestLLM", ""),
		"store_scenario_test.go": scenarioSrc("DH-E-STORE-1", "TestStore", ""),
	})
	p := &countingProbe{}
	var out bytes.Buffer
	results := Run(context.Background(), &out, c.ScenarioGates("DH-S-17", p.probe))
	if len(results) != 1 || results[0].Name != "preflight" || results[0].Passed() {
		t.Fatalf("results = %+v, want the preflight alone, failed:\n%s", results, out.String())
	}
	for _, want := range []string{"DH-E-LLM-1 needs claude-session", "Claude Code (claude)", "no scenario is started", "preflight: sign-in claude-session via Claude Code (claude): `fake status`: loggedIn=false"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "gate 2/2") || strings.Contains(out.String(), "--- PASS") {
		t.Errorf("a scenario ran after a failed preflight:\n%s", out.String())
	}
	if !slices.Equal(p.asked, []acceptance.Credential{acceptance.CredentialClaude}) {
		t.Errorf("probed %v, want claude-session once", p.asked)
	}
}

// TestScenarioGatesRunThePlan: with the client signed in the planned scenario tests
// run under -tags scenario with their output in the record; a skip fails the run
// (DH-ADR-TOOLCHAIN-6), and a stage of none entries probes nothing.
func TestScenarioGatesRunThePlan(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test on a scratch module")
	}
	t.Run("signed in passes", func(t *testing.T) {
		c := scenarioRoot(t, scenarioIndex, map[string]string{
			"llm_scenario_test.go":   scenarioSrc("DH-E-LLM-1", "TestLLM", ""),
			"store_scenario_test.go": scenarioSrc("DH-E-STORE-1", "TestStore", ""),
		})
		p := &countingProbe{signedIn: true}
		var out bytes.Buffer
		if code := ExitCode(Run(context.Background(), &out, c.ScenarioGates("DH-S-17", p.probe))); code != 0 {
			t.Fatalf("exit %d:\n%s", code, out.String())
		}
		for _, want := range []string{"scenarios: budget 1h40m0s ", "--- PASS: TestLLM ", "--- PASS: TestStore ", "go test -tags scenario -count=1 -p 1 -timeout <left> -v -run ^(TestLLM|TestStore)$ ./..."} {
			if !strings.Contains(budgetFree(out.String()), want) {
				t.Errorf("output lacks %q:\n%s", want, out.String())
			}
		}
	})
	t.Run("a skip fails", func(t *testing.T) {
		index := "built-through: DH-S-17\nstages:\n  DH-S-17:\n    - id: DH-E-STORE-1\n      authored: DH-S-7\n      credential: none\n"
		c := scenarioRoot(t, index, map[string]string{"store_scenario_test.go": scenarioSrc("DH-E-STORE-1", "TestStore", `t.Skip("no input")`)})
		p := &countingProbe{}
		var out bytes.Buffer
		results := Run(context.Background(), &out, c.ScenarioGates("DH-S-17", p.probe))
		if ExitCode(results) != 1 || results[len(results)-1].Name != "scenarios" {
			t.Fatalf("results = %+v, want the scenarios gate failed:\n%s", results, out.String())
		}
		if err := results[len(results)-1].Err.Error(); !strings.Contains(err, "TestStore skipped") || !strings.Contains(err, "required scenario TestStore did not pass") {
			t.Errorf("scenarios gate = %s", err)
		}
		if len(p.asked) != 0 {
			t.Errorf("a none-only stage probed %v", p.asked)
		}
	})
}

func TestScenarioVerdict(t *testing.T) {
	if err := scenarioVerdict("--- PASS: TestA (0.01s)\n--- PASS: TestB (0.00s)\n", []string{"TestA", "TestB"}, nil); err != nil {
		t.Errorf("verdict on passes = %v", err)
	}
	err := scenarioVerdict("    --- SKIP: TestA/x (0.00s)\n--- PASS: TestA (0.01s)\n--- FAIL: TestB (0.00s)\n", []string{"TestA", "TestB"}, errors.New("exit status 1"))
	for _, want := range []string{"go test: exit status 1", "TestA/x skipped", "required scenario TestB did not pass"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("verdict = %v, want %q", err, want)
		}
	}
	// A required run makes no exception for any leg or subtest: a parent
	// failure (a report row the test kit could not write) beside passing legs
	// fails it, and so does any non-zero exit.
	exit1 := errors.New("exit status 1")
	for name, out := range map[string]string{
		"a parent error after its legs":  "    --- PASS: TestA/claude-code (1.00s)\n    scenario.go:511: report: write: denied\n--- FAIL: TestA (2.00s)\n",
		"a failing subtest":              "    --- FAIL: TestA/copilot-cli (1.00s)\n--- PASS: TestA (2.00s)\n",
		"an exit with every test passed": "    --- PASS: TestA/claude-code (1.00s)\n--- PASS: TestA (2.00s)\nscenario: report: DH-E-A has no row\n",
	} {
		if err := scenarioVerdict(out, []string{"TestA"}, exit1); err == nil {
			t.Errorf("%s: verdict passed, want a failure", name)
		}
	}
}

// TestRecordedLegs: the routed tests are those with a claude-code result in the
// required run; each one's copilot-cli result is read from the recorded run, and a
// missing result is "not run", never a pass.
func TestRecordedLegs(t *testing.T) {
	req := "    --- PASS: TestA/claude-code (1.00s)\n--- PASS: TestA (1.00s)\n--- PASS: TestB (0.00s)\n    --- FAIL: TestC/claude-code (1.00s)\n--- FAIL: TestC (1.00s)\n"
	if got := routedTests(req, []string{"TestA", "TestB", "TestC"}); !slices.Equal(got, []string{"TestA", "TestC"}) {
		t.Errorf("routed = %q, want TestA and TestC", got)
	}
	rec := "    --- FAIL: TestA/copilot-cli (1.00s)\n--- FAIL: TestA (1.00s)\nFAIL\n"
	got := recordedLegs(rec, errors.New("exit status 1"), []string{"TestA", "TestC"})
	want := []string{"TestA/copilot-cli failed", "TestC/copilot-cli not run (no copilot-cli result line)"}
	if !slices.Equal(got, want) {
		t.Errorf("recorded = %q, want %q", got, want)
	}
}

// TestPhasesOrderDHS17: DH-S-17 runs TOOLCHAIN-1 first and SANDBOX-7 last, each in its
// own go test run, with every other planned scenario between them in plan order
// (architecture.md rule 7, R-03); another stage is one run in plan order.
func TestPhasesOrderDHS17(t *testing.T) {
	var plan acceptance.Plan
	for _, id := range []string{"DH-E-SDK-1", "DH-E-SANDBOX-7", "DH-E-LLM-1", "DH-E-TOOLCHAIN-1", "DH-E-AGENT-4"} {
		plan.Scenarios = append(plan.Scenarios, acceptance.Scenario{ID: id, Test: "Test" + id})
	}
	var got [][]string
	for _, p := range phases("DH-S-17", plan) {
		var ids []string
		for _, s := range p.Scenarios {
			ids = append(ids, s.ID)
		}
		got = append(got, ids)
	}
	want := [][]string{{"DH-E-TOOLCHAIN-1"}, {"DH-E-SDK-1", "DH-E-LLM-1", "DH-E-AGENT-4"}, {"DH-E-SANDBOX-7"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("DH-S-17 phases = %q, want %q", got, want)
	}
	if p := phases("DH-S-13", plan); len(p) != 1 || len(p[0].Scenarios) != len(plan.Scenarios) {
		t.Errorf("DH-S-13 phases = %+v, want the plan as one run", p)
	}
	if p := phases("DH-S-17", acceptance.Plan{Scenarios: plan.Scenarios[2:3]}); len(p) != 1 || p[0].Scenarios[0].ID != "DH-E-LLM-1" {
		t.Errorf("phases of one middle scenario = %+v, want one run", p)
	}
}

// routesSrc is a scratch forEachRoute: it runs the legs DSH_SCENARIO_ROUTES
// selects, as signedin_routes_test.go does.
const routesSrc = "//go:build scenario\n\npackage s\n\nimport (\n\t\"os\"\n\t\"strings\"\n\t\"testing\"\n)\n\n" +
	"func forEachRoute(t *testing.T, leg func(t *testing.T, id string)) {\n\tfor _, id := range []string{\"claude-code\", \"copilot-cli\"} {\n" +
	"\t\tif v := os.Getenv(\"DSH_SCENARIO_ROUTES\"); v != \"\" && !strings.Contains(\",\"+v+\",\", \",\"+id+\",\") {\n\t\t\tcontinue\n\t\t}\n" +
	"\t\tt.Run(id, func(t *testing.T) { leg(t, id) })\n\t}\n}\n"

// copilotFails is a routed scenario body whose claude-code leg passes and whose
// copilot-cli leg fails.
const copilotFails = `
	forEachRoute(t, func(t *testing.T, id string) {
		if id == "copilot-cli" {
			t.Error("copilot signed out")
		}
	})
`

// budgetFree is out with each go test -timeout value replaced by <left>, the
// budget left when the run started.
func budgetFree(out string) string {
	return regexp.MustCompile(`-timeout \S+ `).ReplaceAllString(out, "-timeout <left> ")
}

// TestScenarioGatesS17OrderAndRecordedLeg runs a scratch DH-S-17 plan: TOOLCHAIN-1
// runs to its end before the others, SANDBOX-7 after them, each run requiring only
// its own IDs; a routed scenario runs its claude-code leg in the required run and its
// copilot-cli leg in a recorded run of its own, so a copilot-cli failure passes the
// gate with the failure in the record (M2-DEC-4).
func TestScenarioGatesS17OrderAndRecordedLeg(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test on a scratch module")
	}
	index := "built-through: DH-S-17\nstages:\n  DH-S-17:\n" +
		"    - id: DH-E-SANDBOX-7\n      authored: DH-S-15\n      credential: none\n" +
		"    - id: DH-E-LLM-1\n      authored: DH-S-9\n      credential: claude-session\n" +
		"    - id: DH-E-TOOLCHAIN-1\n      authored: DH-S-11\n      credential: none\n"
	c := scenarioRoot(t, index, map[string]string{
		"a_sandbox_scenario_test.go":   scenarioSrc("DH-E-SANDBOX-7", "TestSandbox", ""),
		"b_llm_scenario_test.go":       scenarioSrc("DH-E-LLM-1", "TestLLM", copilotFails),
		"c_toolchain_scenario_test.go": scenarioSrc("DH-E-TOOLCHAIN-1", "TestToolchain", ""),
		"routes_test.go":               routesSrc,
	})
	p := &countingProbe{signedIn: true}
	var out bytes.Buffer
	if code := ExitCode(Run(context.Background(), &out, c.ScenarioGates("DH-S-17", p.probe))); code != 0 {
		t.Fatalf("exit %d, want a pass with the copilot-cli leg recorded:\n%s", code, out.String())
	}
	o := budgetFree(out.String())
	order := []string{
		"DSH_REQUIRE_SCENARIOS=DH-E-TOOLCHAIN-1 DSH_SCENARIO_ROUTES=claude-code go test -tags scenario -count=1 -p 1 -timeout <left> -v -run ^(TestToolchain)$ ./...",
		"--- PASS: TestToolchain ",
		"DSH_REQUIRE_SCENARIOS=DH-E-LLM-1 DSH_SCENARIO_ROUTES=claude-code go test -tags scenario -count=1 -p 1 -timeout <left> -v -run ^(TestLLM)$ ./...",
		"--- PASS: TestLLM ",
		"--- PASS: TestLLM/claude-code ",
		"DSH_REQUIRE_SCENARIOS= DSH_SCENARIO_ROUTES=copilot-cli go test -tags scenario -count=1 -p 1 -timeout <left> -v -run ^(TestLLM)$ ./...",
		"--- FAIL: TestLLM/copilot-cli ",
		"scenarios: recorded leg: TestLLM/copilot-cli failed",
		"DSH_REQUIRE_SCENARIOS=DH-E-SANDBOX-7 DSH_SCENARIO_ROUTES=claude-code go test -tags scenario -count=1 -p 1 -timeout <left> -v -run ^(TestSandbox)$ ./...",
		"--- PASS: TestSandbox ",
	}
	at := 0
	for _, want := range order {
		i := strings.Index(o[at:], want)
		if i < 0 {
			t.Fatalf("output lacks %q after offset %d (order %q):\n%s", want, at, order, o)
		}
		at += i + len(want)
	}
	if strings.Contains(o, "DSH_SCENARIO_ROUTES=copilot-cli go test -tags scenario -count=1 -p 1 -timeout <left> -v -run ^(TestToolchain)$") {
		t.Errorf("an unrouted scenario got a recorded run:\n%s", o)
	}
}

// TestScenarioGatesS17ParentFailureWithRecordedLeg: a routed scenario whose
// claude-code leg passes and copilot-cli leg fails, and whose parent fails in a
// cleanup the way the test kit fails a report row it cannot write, fails the gate:
// the copilot-cli failure is recorded and never stands in for the parent's.
func TestScenarioGatesS17ParentFailureWithRecordedLeg(t *testing.T) {
	if testing.Short() {
		t.Skip("runs go test on a scratch module")
	}
	index := "built-through: DH-S-17\nstages:\n  DH-S-17:\n    - id: DH-E-LLM-1\n      authored: DH-S-9\n      credential: claude-session\n"
	body := "\n\tt.Cleanup(func() { t.Errorf(\"report: write: permission denied\") })" + copilotFails
	c := scenarioRoot(t, index, map[string]string{
		"b_llm_scenario_test.go": scenarioSrc("DH-E-LLM-1", "TestLLM", body),
		"routes_test.go":         routesSrc,
	})
	p := &countingProbe{signedIn: true}
	var out bytes.Buffer
	results := Run(context.Background(), &out, c.ScenarioGates("DH-S-17", p.probe))
	if ExitCode(results) != 1 || results[len(results)-1].Name != "scenarios" {
		t.Fatalf("results = %+v, want the scenarios gate failed:\n%s", results, out.String())
	}
	if err := results[len(results)-1].Err.Error(); !strings.Contains(err, "required scenario TestLLM did not pass") || !strings.Contains(err, "go test: exit status 1") {
		t.Errorf("scenarios gate = %s", err)
	}
	for _, want := range []string{"--- PASS: TestLLM/claude-code ", "report: write: permission denied", "scenarios: recorded leg: TestLLM/copilot-cli failed"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
func TestScenariosSubcommandUsage(t *testing.T) {
	for _, args := range [][]string{
		{"scenarios", "--evidence", "x.txt"},
		{"scenarios", "--stage", "S-17", "--evidence", "x.txt"},
		{"scenarios", "--stage", "DH-S-17"},
		{"scenarios", "--stage", "DH-S-17", "--evidence", "x.txt", "extra"},
		{"scenarios", "--stage", "DH-S-17", "--evidence", filepath.Join(t.TempDir(), "x.txt"), "--root", t.TempDir()}, // not a git checkout
	} {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, &bytes.Buffer{}, &stderr); code != 2 {
			t.Errorf("run(%q) = %d, want 2 (%s)", args, code, stderr.String())
		}
	}
}

// TestScenariosRefusesDirtyTree: a scenarios run is an evidence record, so a tree
// with changes is refused, naming the path, before any gate or probe runs.
func TestScenariosRefusesDirtyTree(t *testing.T) {
	root, _ := gitScratch(t, "unformatted-file")
	writeFile(t, filepath.Join(root, "untracked.txt"), "x\n")
	record := filepath.Join(t.TempDir(), "record.txt")
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"scenarios", "--root", root, "--stage", "DH-S-17", "--evidence", record}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "untracked.txt") {
		t.Errorf("scenarios on a dirty tree = %d:\n%s%s", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(record); !os.IsNotExist(err) {
		t.Errorf("a refused run wrote %s (%v)", record, err)
	}
}

func TestProbeEnvRemovesCredentials(t *testing.T) {
	env := probeEnv([]string{"PATH=/bin", "ANTHROPIC_API_KEY=k", "GH_TOKEN=t", "CLAUDE_CONFIG_DIR=/c", "COPILOT_GITHUB_TOKEN=c"})
	if !slices.Equal(env, []string{"PATH=/bin", "CLAUDE_CONFIG_DIR=/c"}) {
		t.Errorf("probeEnv = %v", env)
	}
}

// TestExecRunner runs the go command, never a client: exit 0 with its output, a
// non-zero exit as a status, and a missing program as an error.
func TestExecRunner(t *testing.T) {
	run := execRunner(os.Environ())
	ctx := context.Background()
	if out, code, err := run(ctx, "go", "env", "GOOS"); err != nil || code != 0 || len(bytes.TrimSpace(out)) == 0 {
		t.Errorf("go env GOOS = %q, %d, %v", out, code, err)
	}
	if _, code, err := run(ctx, "go", "no-such-command"); err != nil || code == 0 {
		t.Errorf("go no-such-command = %d, %v; want a non-zero status and no error", code, err)
	}
	if _, _, err := run(ctx, "harness-gate-no-such-program"); err == nil {
		t.Error("a missing program ran")
	}
}
