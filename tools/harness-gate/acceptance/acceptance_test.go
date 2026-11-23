package acceptance

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const goodIndex = `# comment
built-through: DH-S-1
stages:
  DH-S-16: []
  DH-S-17:
    - id: DH-E-TOOLCHAIN-1 # trailing comment
      authored: DH-S-11
    - id: DH-E-LLM-3
      authored: DH-S-9
      requires: [DH-S-11, DH-S-16]
  DH-S-18:
    - id: DH-E-TOOLCHAIN-2
      authored: DH-S-1
`

func mustParse(t *testing.T, src string) *Index {
	t.Helper()
	x, err := ParseIndex(strings.NewReader(src))
	if err != nil {
		t.Fatalf("ParseIndex: %v", err)
	}
	return x
}

func TestParseIndex(t *testing.T) {
	x := mustParse(t, goodIndex)
	if x.BuiltThrough != "DH-S-1" {
		t.Errorf("built-through = %q", x.BuiltThrough)
	}
	want := []Stage{
		{ID: "DH-S-16"},
		{ID: "DH-S-17", Scenarios: []Entry{{ID: "DH-E-TOOLCHAIN-1", Authored: "DH-S-11"}, {ID: "DH-E-LLM-3", Authored: "DH-S-9", Requires: []string{"DH-S-11", "DH-S-16"}}}},
		{ID: "DH-S-18", Scenarios: []Entry{{ID: "DH-E-TOOLCHAIN-2", Authored: "DH-S-1"}}},
	}
	if len(x.Stages) != len(want) {
		t.Fatalf("stages = %+v", x.Stages)
	}
	sameEntry := func(a, b Entry) bool {
		return a.ID == b.ID && a.Authored == b.Authored && slices.Equal(a.Requires, b.Requires)
	}
	for i := range want {
		if x.Stages[i].ID != want[i].ID || !slices.EqualFunc(x.Stages[i].Scenarios, want[i].Scenarios, sameEntry) {
			t.Errorf("stage %d = %+v, want %+v", i, x.Stages[i], want[i])
		}
	}
	if ids, err := x.Required("DH-S-18"); err != nil || !slices.Equal(ids, []string{"DH-E-TOOLCHAIN-2"}) {
		t.Errorf("Required(DH-S-18) = %v, %v", ids, err)
	}
	if ids, err := x.Required(""); err != nil || len(ids) != 0 {
		t.Errorf("Required(\"\") = %v, %v; want none", ids, err)
	}
	if _, err := x.Required("DH-S-99"); err == nil || !strings.Contains(err.Error(), "DH-S-99") {
		t.Errorf("Required(DH-S-99) = %v, want an error naming the stage", err)
	}
}

func TestParseIndexRejects(t *testing.T) {
	tests := map[string]struct{ src, want string }{
		"no built-through":    {"stages:\n  DH-S-1: []\n", "no built-through"},
		"no stages":           {"built-through: DH-S-1\n", "no stages"},
		"bad stage":           {"built-through: DH-S-1\nstages:\n  S-18: []\n", `"S-18" is not a stage ID`},
		"duplicate stage":     {"built-through: DH-S-1\nstages:\n  DH-S-2: []\n  DH-S-2: []\n", "stage DH-S-2 listed twice"},
		"bad scenario":        {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-T-X-1\n      authored: DH-S-1\n", "want \"- id: DH-E-"},
		"missing authored":    {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n    - id: DH-E-X-2\n      authored: DH-S-1\n", "DH-E-X-1 has no authored stage"},
		"trailing no author":  {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n", "DH-E-X-1 has no authored stage"},
		"entry under []":      {"built-through: DH-S-1\nstages:\n  DH-S-2: []\n    - id: DH-E-X-1\n", "line 4: unexpected"},
		"unknown key":         {"built-through: DH-S-1\nowner: me\nstages:\n", "line 2: unexpected"},
		"tab indentation":     {"built-through: DH-S-1\nstages:\n\tDH-S-2: []\n", "tab in indentation"},
		"bad built-through":   {"built-through: one\nstages:\n", `built-through "one"`},
		"two authored stages": {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n      authored: DH-S-1\n      authored: DH-S-1\n", "two authored stages"},
		"requires not a list": {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n      authored: DH-S-1\n      requires: DH-S-1\n", "line 6: DH-E-X-1: requires \"DH-S-1\" is not a non-empty list"},
		"requires empty":      {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n      authored: DH-S-1\n      requires: []\n", "is not a non-empty list"},
		"requires bad stage":  {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n      authored: DH-S-1\n      requires: [S-1]\n", `requires "S-1" is not a stage ID`},
		"requires later":      {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n      authored: DH-S-1\n      requires: [DH-S-3]\n", "requires DH-S-3, after its executing stage DH-S-2"},
		"requires twice":      {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n      authored: DH-S-1\n      requires: [DH-S-1, DH-S-1]\n", "requires DH-S-1 twice"},
		"two requires lists":  {"built-through: DH-S-1\nstages:\n  DH-S-2:\n    - id: DH-E-X-1\n      authored: DH-S-1\n      requires: [DH-S-1]\n      requires: [DH-S-2]\n", "two requires lists"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseIndex(strings.NewReader(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseIndex = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

const scenarioFile = "//go:build scenario\n\npackage scenarios\n\nimport \"testing\"\n\n// TestRehearsal is a scenario.\n//\n//dsh:scenario DH-E-TOOLCHAIN-2\nfunc TestRehearsal(t *testing.T) {}\n"

func TestParseScenarioFile(t *testing.T) {
	got, err := ParseScenarioFile("s/a_scenario_test.go", []byte(scenarioFile))
	if err != nil {
		t.Fatal(err)
	}
	want := []Scenario{{ID: "DH-E-TOOLCHAIN-2", File: "s/a_scenario_test.go", Test: "TestRehearsal"}}
	if !slices.Equal(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestParseScenarioFileRejects(t *testing.T) {
	tests := map[string]struct{ src, want string }{
		"no constraint":    {"package s\n\n//dsh:scenario DH-E-X-1\nfunc TestX(t *testing.T) {}\n", "no //go:build scenario constraint"},
		"other constraint": {"//go:build scenario && linux\n\npackage s\n\n//dsh:scenario DH-E-X-1\nfunc TestX(t *testing.T) {}\n", "want exactly //go:build scenario"},
		"no directive":     {"//go:build scenario\n\npackage s\n\nfunc TestX(t *testing.T) {}\n", "no //dsh:scenario directive"},
		"not a test":       {"//go:build scenario\n\npackage s\n\n//dsh:scenario DH-E-X-1\nfunc helper() {}\n", "is not on a TestXxx function"},
		"floating":         {"//go:build scenario\n\npackage s\n\nfunc TestX(t *testing.T) {\n\t//dsh:scenario DH-E-X-1\n}\n", "is not on a TestXxx function"},
		"bad id":           {"//go:build scenario\n\npackage s\n\n//dsh:scenario DH-T-X-1\nfunc TestX(t *testing.T) {}\n", "is not a scenario ID"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseScenarioFile("x_scenario_test.go", []byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseScenarioFile = %v, want an error containing %q", err, tt.want)
			}
		})
	}
}

func TestScanScenariosSkipsTestdataAndNestedModules(t *testing.T) {
	root := t.TempDir()
	write := func(rel, src string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("m/go.mod", "module m\n")
	write("m/s/a_scenario_test.go", scenarioFile)
	write("m/s/testdata/b_scenario_test.go", "broken")
	write("m/nested/go.mod", "module n\n")
	write("m/nested/c_scenario_test.go", "broken")
	write("m/s/plain_test.go", "broken")
	got, err := ScanScenarios(root, []string{filepath.Join(root, "m")})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].File != "m/s/a_scenario_test.go" || got[0].ID != "DH-E-TOOLCHAIN-2" {
		t.Errorf("ScanScenarios = %+v", got)
	}
	write("m/s/d_scenario_test.go", "//go:build scenario\n\npackage s\n")
	if _, err := ScanScenarios(root, []string{filepath.Join(root, "m")}); err == nil || !strings.Contains(err.Error(), "m/s/d_scenario_test.go") {
		t.Errorf("ScanScenarios = %v, want the file without a directive named", err)
	}
}

func TestCheck(t *testing.T) {
	x := mustParse(t, goodIndex)
	rehearsal := Scenario{ID: "DH-E-TOOLCHAIN-2", File: "s/a_scenario_test.go", Test: "TestRehearsal"}
	if err := Check(x, []Scenario{rehearsal}); err != nil {
		t.Errorf("Check on an agreeing index = %v", err)
	}
	tests := map[string]struct {
		index     string
		scenarios []Scenario
		want      []string
	}{
		"authored file missing from the index": {
			goodIndex, []Scenario{rehearsal, {ID: "DH-E-X-1", File: "s/x_scenario_test.go", Test: "TestX"}},
			[]string{"DH-E-X-1 (s/x_scenario_test.go: TestX) is not in test/harness/acceptance-index.yaml"},
		},
		"indexed and authored already, no file": {
			goodIndex, nil,
			[]string{"DH-E-TOOLCHAIN-2 is indexed under DH-S-18, authored at DH-S-1 (built through DH-S-1), and has no scenario test"},
		},
		"indexed twice": {
			goodIndex + "  DH-S-22:\n    - id: DH-E-TOOLCHAIN-2\n      authored: DH-S-1\n", []Scenario{rehearsal},
			[]string{"DH-E-TOOLCHAIN-2 is indexed 2 times (DH-S-18, DH-S-22)"},
		},
		"two tests claim one ID": {
			goodIndex, []Scenario{rehearsal, {ID: "DH-E-TOOLCHAIN-2", File: "s/b_scenario_test.go", Test: "TestB"}},
			[]string{"DH-E-TOOLCHAIN-2 is authored by 2 tests"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := Check(mustParse(t, tt.index), tt.scenarios)
			for _, w := range tt.want {
				if err == nil || !strings.Contains(err.Error(), w) {
					t.Errorf("Check = %v, want %q", err, w)
				}
			}
		})
	}
	// DH-E-TOOLCHAIN-1 is authored at DH-S-11: once built-through reaches it, its
	// missing file fails the check.
	later := mustParse(t, strings.Replace(goodIndex, "built-through: DH-S-1", "built-through: DH-S-11", 1))
	if err := Check(later, []Scenario{rehearsal}); err == nil || !strings.Contains(err.Error(), "DH-E-TOOLCHAIN-1 is indexed under DH-S-17, authored at DH-S-11") {
		t.Errorf("Check built through DH-S-11 = %v, want DH-E-TOOLCHAIN-1 named", err)
	}
}

func TestPreflight(t *testing.T) {
	x := mustParse(t, goodIndex)
	scenarios := []Scenario{{ID: "DH-E-TOOLCHAIN-2", File: "s/a_scenario_test.go", Test: "TestRehearsal"}}
	tests := map[string]struct {
		env      map[string]string
		fail     string // "" passes
		planned  []string
		notNamed string
	}{
		"unknown required scenario": {
			env: map[string]string{RequireEnv: "DH-E-FAKE-9"}, fail: "required scenario DH-E-FAKE-9",
		},
		"indexed but unauthored required scenario": {
			env: map[string]string{RequireEnv: "DH-E-TOOLCHAIN-1"}, fail: "required scenario DH-E-TOOLCHAIN-1",
		},
		"empty required list": {
			env: map[string]string{RequireEnv: ""},
		},
		"required list": {
			env: map[string]string{RequireEnv: " DH-E-TOOLCHAIN-2,DH-E-TOOLCHAIN-2 "}, planned: []string{"TestRehearsal"},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			plan, err := Preflight(tt.env, x, scenarios)
			if tt.fail != "" {
				if err == nil || !strings.Contains(err.Error(), tt.fail) {
					t.Errorf("Preflight = %v, want a failure naming %q", err, tt.fail)
				}
				if len(plan.Scenarios) != 0 {
					t.Errorf("a failed preflight planned %v; no scenario may start", plan.Tests())
				}
				return
			}
			if err != nil {
				t.Fatalf("Preflight = %v, want a pass", err)
			}
			if !slices.Equal(plan.Tests(), tt.planned) {
				t.Errorf("planned %v, want %v", plan.Tests(), tt.planned)
			}
		})
	}
	// A scenario authored ahead of the stages it exercises is refused until
	// built-through reaches every stage its entry requires (DH-S-9's scenarios
	// require DH-S-11 and DH-S-16 and run at DH-S-17).
	authored := append(slices.Clone(scenarios),
		Scenario{ID: "DH-E-TOOLCHAIN-1", File: "s/t_scenario_test.go", Test: "TestToolchain"},
		Scenario{ID: "DH-E-LLM-3", File: "s/l_scenario_test.go", Test: "TestBadKey"})
	need := map[string]string{RequireEnv: "DH-E-TOOLCHAIN-1,DH-E-LLM-3"}
	early := mustParse(t, strings.Replace(goodIndex, "built-through: DH-S-1", "built-through: DH-S-11", 1))
	if plan, err := Preflight(need, early, authored); err == nil || !strings.Contains(err.Error(), "required scenario DH-E-LLM-3 requires DH-S-16, not built (built through DH-S-11)") || len(plan.Scenarios) != 0 {
		t.Errorf("Preflight built through DH-S-11 = %v, %v; want DH-E-LLM-3 refused for DH-S-16 and no plan", plan.Tests(), err)
	}
	built := mustParse(t, strings.Replace(goodIndex, "built-through: DH-S-1", "built-through: DH-S-16", 1))
	if plan, err := Preflight(need, built, authored); err != nil || !slices.Equal(plan.Tests(), []string{"TestToolchain", "TestBadKey"}) {
		t.Errorf("Preflight built through DH-S-16 = %v, %v; want both planned", plan.Tests(), err)
	}
	// The index disagreeing with the scenario tests fails the preflight too.
	if _, err := Preflight(map[string]string{}, x, nil); err == nil || !strings.Contains(err.Error(), "DH-E-TOOLCHAIN-2 is indexed under DH-S-18") {
		t.Errorf("Preflight with the authored file missing = %v", err)
	}
}

// TestPreflightNeedsNoModelKey: under models-signed-in-claude-copilot the preflight
// reads no model key. With no model variable at all and an empty required list it
// passes with an empty plan; with DSH_REQUIRE_SCENARIOS=DH-E-FAKE-9 it fails naming
// that ID and plans nothing (M1-G-TOOL-15; DH-T-TOOLCHAIN-6).
func TestPreflightNeedsNoModelKey(t *testing.T) {
	x := mustParse(t, goodIndex)
	scenarios := []Scenario{{ID: "DH-E-TOOLCHAIN-2", File: "s/a_scenario_test.go", Test: "TestRehearsal"}}
	for _, env := range []map[string]string{nil, {}, {RequireEnv: ""}, {RequireEnv: " , "}} {
		plan, err := Preflight(env, x, scenarios)
		if err != nil || len(plan.Scenarios) != 0 {
			t.Errorf("Preflight(%v) = %v, %v; want a pass with an empty plan", env, plan.Tests(), err)
		}
	}
	plan, err := Preflight(map[string]string{RequireEnv: "DH-E-FAKE-9"}, x, scenarios)
	if err == nil || !strings.Contains(err.Error(), "required scenario DH-E-FAKE-9 ("+RequireEnv+") has no scenario test") {
		t.Errorf("Preflight with DH-E-FAKE-9 = %v, want a failure naming DH-E-FAKE-9", err)
	}
	if len(plan.Scenarios) != 0 {
		t.Errorf("a failed preflight planned %v", plan.Tests())
	}
}

// TestPreflightCallsTheCredentialHook: the hook in force passes on any environment,
// and a failing hook (as stage 19.2's check may be) fails the preflight, named,
// with an empty plan (M1-G-TOOL-15; DH-T-TOOLCHAIN-6).
func TestPreflightCallsTheCredentialHook(t *testing.T) {
	for _, env := range []map[string]string{nil, {"DSH_HOME": "/h", RequireEnv: "DH-E-X-1"}} {
		if err := Credentials(env); err != nil {
			t.Errorf("the credential hook in force failed on %v: %v", env, err)
		}
	}
	x := mustParse(t, goodIndex)
	scenarios := []Scenario{{ID: "DH-E-TOOLCHAIN-2", File: "s/a_scenario_test.go", Test: "TestRehearsal"}}
	saved := Credentials
	t.Cleanup(func() { Credentials = saved })
	var seen map[string]string
	Credentials = func(env map[string]string) error {
		seen = env
		return errors.New("route claude is signed out")
	}
	env := map[string]string{RequireEnv: "DH-E-TOOLCHAIN-2"}
	plan, err := Preflight(env, x, scenarios)
	if err == nil || !strings.Contains(err.Error(), "credentials: route claude is signed out") || len(plan.Scenarios) != 0 {
		t.Errorf("Preflight with a failing hook = %v, %v; want the hook's failure and no plan", plan.Tests(), err)
	}
	if seen[RequireEnv] != "DH-E-TOOLCHAIN-2" {
		t.Errorf("the hook saw %v, want the preflight's environment", seen)
	}
}

func TestBoundaryImport(t *testing.T) {
	for path, want := range map[string]bool{
		"encoding/json":                                         true,
		"net/http/httptest":                                     true,
		productModule + "src/sdk/protocol":                      true,
		productModule + "src/sdk/jsonrpc":                       true,
		productModule + "src/harness/internal/testkit/launcher": true,
		productModule + "src/harness/internal/llm":              false,
		productModule + "src/harness/internal/agent":            false,
		productModule + "src/harness/internal/session/views":    false,
		productModule + "src/harness/internal/testkit":          false,
		productModule + "src/sdkextra/x":                        false,
		"golang.org/x/sync/errgroup":                            false,
	} {
		if got := BoundaryImport(path); got != want {
			t.Errorf("BoundaryImport(%s) = %v, want %v", path, got, want)
		}
	}
}

func TestCheckBoundary(t *testing.T) {
	root := t.TempDir()
	write := func(name, imports string) Scenario {
		src := "//go:build scenario\n\npackage s\n\nimport (\n" + imports + ")\n"
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return Scenario{ID: strings.TrimSuffix(name, ".go"), File: name, Test: "TestX"}
	}
	clean := write("DH-E-A-1.go", "\t\"testing\"\n\t\""+productModule+"src/sdk/protocol\"\n\t\""+launcherImport+"\"\n")
	dirty := write("DH-E-A-2.go", "\t\"testing\"\n\t\""+productModule+"src/harness/internal/llm\"\n")
	free := write("DH-E-A-3.go", "\t\""+productModule+"src/harness/internal/llm\"\n")
	x := mustParse(t, "built-through: DH-S-1\nstages:\n  DH-S-17:\n"+
		"    - id: DH-E-A-1\n      authored: DH-S-9\n      requires: [DH-S-16]\n"+
		"    - id: DH-E-A-2\n      authored: DH-S-9\n      requires: [DH-S-16]\n"+
		"    - id: DH-E-A-3\n      authored: DH-S-9\n")
	err := CheckBoundary(root, x, []Scenario{clean, dirty, free})
	if err == nil || !strings.Contains(err.Error(), "DH-E-A-2 (DH-E-A-2.go) imports "+productModule+"src/harness/internal/llm") {
		t.Fatalf("CheckBoundary = %v, want DH-E-A-2's internal import named", err)
	}
	if strings.Contains(err.Error(), "DH-E-A-1") || strings.Contains(err.Error(), "DH-E-A-3") {
		t.Errorf("CheckBoundary = %v: a clean file and an entry without requires are not refused", err)
	}
}

func TestPlanRunPattern(t *testing.T) {
	if p := (Plan{}).RunPattern(); p != "" {
		t.Errorf("empty plan pattern = %q", p)
	}
	p := Plan{Scenarios: []Scenario{{Test: "TestA"}, {Test: "TestB"}}}
	if got := p.RunPattern(); got != "^(TestA|TestB)$" {
		t.Errorf("RunPattern = %q", got)
	}
}

func TestEnviron(t *testing.T) {
	env := Environ([]string{"A=1", "B=x=y", "=C:=C:\\", "bad"})
	if env["A"] != "1" || env["B"] != "x=y" || len(env) != 2 {
		t.Errorf("Environ = %v", env)
	}
}
