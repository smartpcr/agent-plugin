package guarded

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

var stage16 = []string{"DH-E-STORE-1", "DH-E-STORE-2", "DH-E-STORE-6", "DH-E-FS-3"}

func TestSelectDropsLinuxOnlyOnWindows(t *testing.T) {
	run, out := Select(WSL, stage16)
	if !slices.Equal(run, stage16) || len(out) != 0 {
		t.Errorf("wsl: run %v, out %v; want all four, none out", run, out)
	}
	run, out = Select(Windows, stage16)
	if want := []string{"DH-E-STORE-1", "DH-E-STORE-6", "DH-E-FS-3"}; !slices.Equal(run, want) {
		t.Errorf("windows: run %v, want %v", run, want)
	}
	if len(out) != 1 || out[0].ID != "DH-E-STORE-2" || out[0].Result() != "out of scope (impl.md 16.8 Linux only; TC-19s2)" {
		t.Fatalf("windows: out %v, want DH-E-STORE-2 out of scope by impl.md 16.8", out)
	}
	// The row the Windows lane records must be one the stage close accepts.
	ok := slices.ContainsFunc(closure.ScopeDecisions, func(d closure.ScopeDecision) bool {
		return d.ID == out[0].Decision && d.Applies(out[0].ID, Windows.Variant())
	})
	if !ok {
		t.Errorf("decision %q is not a closure scope decision covering %s %s", out[0].Decision, out[0].ID, Windows.Variant())
	}
}

func TestParseLane(t *testing.T) {
	for in, want := range map[string]Lane{"wsl": WSL, " Windows ": Windows} {
		if got, err := ParseLane(in); err != nil || got != want {
			t.Errorf("ParseLane(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseLane("macos"); err == nil {
		t.Error("ParseLane(macos) passed; macOS has no lane (platforms-windows-wsl)")
	}
}

func TestRequiresBuiltAllowsOnlyTheExecutingStage(t *testing.T) {
	x := &acceptance.Index{BuiltThrough: "DH-S-11", Stages: []acceptance.Stage{{ID: "DH-S-16", Scenarios: []acceptance.Entry{
		{ID: "DH-E-FS-3", Requires: []string{"DH-S-16"}},
		{ID: "DH-E-STORE-1"},
		{ID: "DH-E-X-1", Requires: []string{"DH-S-11", "DH-S-14"}},
	}}}}
	if err := RequiresBuilt(x, "DH-S-16", []string{"DH-E-FS-3", "DH-E-STORE-1"}); err != nil {
		t.Errorf("the executing stage as a requirement: %v", err)
	}
	err := RequiresBuilt(x, "DH-S-16", []string{"DH-E-X-1"})
	if err == nil || !strings.Contains(err.Error(), "DH-E-X-1 requires DH-S-14, not built") || strings.Contains(err.Error(), "DH-S-11,") {
		t.Errorf("an unbuilt earlier stage: %v; want DH-E-X-1 naming DH-S-14 only", err)
	}
	if err := RequiresBuilt(x, "DH-S-17", []string{"DH-E-FS-3"}); err == nil {
		t.Error("DH-S-16 unbuilt passed for a run of DH-S-17")
	}
}

// TestInputsOfTheStageScenarios reads the four step 16.8 scenario files: the
// preflight must name what the scenarios themselves read.
func TestInputsOfTheStageScenarios(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "src", "harness", "internal", "testkit", "scenarios")
	for _, tc := range []struct {
		file, test string
		want       []string
	}{
		{"store_two_process_scenario_test.go", "TestTwoProcessesShareOneHomeWithOneWriter", []string{
			"DSH_SCENARIO_STORE1_BEGIN", "DSH_SCENARIO_STORE1_CREATE", "DSH_SCENARIO_STORE1_KILL",
			"DSH_SCENARIO_STORE1_PID", "DSH_SCENARIO_STORE1_RESUME", "DSH_SCENARIO_STORE1_START"}},
		{"store_crash_recovery_scenario_test.go", "TestKilledHostRecoversHonestly", []string{
			"DSH_SCENARIO_CLAUDE_CODE_MODEL", "DSH_SCENARIO_STORE2_DELETE", "DSH_SCENARIO_STORE2_KILL", "DSH_SCENARIO_STORE2_PROMPT",
			"DSH_SCENARIO_STORE2_REQUEST", "DSH_SCENARIO_STORE2_RESUME", "DSH_SCENARIO_STORE2_START"}},
		{"fs_two_process_scenario_test.go", "TestTwoProcessesAndAnEditorChangeOneFile", []string{
			"DSH_SCENARIO_FS3_EDITOR", "DSH_SCENARIO_FS3_LAYER", "DSH_SCENARIO_FS3_LOCK_TIMEOUT",
			"DSH_SCENARIO_FS3_MODEL", "DSH_SCENARIO_FS3_ROUTE"}},
	} {
		src, err := os.ReadFile(filepath.Join(dir, tc.file))
		if err != nil {
			t.Fatal(err)
		}
		got, err := Inputs(tc.file, src, tc.test)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: inputs %v, %v; want %v", tc.test, got, err, tc.want)
		}
	}
	if _, err := Inputs("x_test.go", []byte("package x\n"), "TestMissing"); err == nil {
		t.Error("a missing test function passed")
	}
}

func TestMissing(t *testing.T) {
	env := map[string]string{"A": "a", "B": "  "}
	got := Missing([]string{"A", "B", "C"}, func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if !slices.Equal(got, []string{"B", "C"}) {
		t.Errorf("Missing = %v, want [B C]: blank counts as unset", got)
	}
}
