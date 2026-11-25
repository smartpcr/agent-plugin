package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
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
		for _, want := range []string{"--- PASS: TestLLM ", "--- PASS: TestStore ", "go test -tags scenario -count=1 -v -run ^(TestLLM|TestStore)$ ./..."} {
			if !strings.Contains(out.String(), want) {
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
	err := scenarioVerdict("    --- SKIP: TestA/copilot-cli (0.00s)\n--- PASS: TestA (0.01s)\n--- FAIL: TestB (0.00s)\n", []string{"TestA", "TestB"}, errors.New("exit status 1"))
	for _, want := range []string{"go test: exit status 1", "TestA/copilot-cli skipped", "required scenario TestB did not pass"} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("verdict = %v, want %q", err, want)
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
