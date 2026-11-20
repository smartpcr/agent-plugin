package acceptance

import (
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
		{ID: "DH-S-17", Scenarios: []Entry{{"DH-E-TOOLCHAIN-1", "DH-S-11"}}},
		{ID: "DH-S-18", Scenarios: []Entry{{"DH-E-TOOLCHAIN-2", "DH-S-1"}}},
	}
	if len(x.Stages) != len(want) {
		t.Fatalf("stages = %+v", x.Stages)
	}
	for i := range want {
		if x.Stages[i].ID != want[i].ID || !slices.Equal(x.Stages[i].Scenarios, want[i].Scenarios) {
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
		"missing secret": {
			env: map[string]string{RequireEnv: "DH-E-TOOLCHAIN-2"}, fail: "secret DSH_DEEPSEEK_API_KEY is not set",
		},
		"blank secret": {
			env: map[string]string{SecretEnv: "  "}, fail: "secret DSH_DEEPSEEK_API_KEY is not set",
		},
		"unknown required scenario": {
			env: map[string]string{SecretEnv: "k", RequireEnv: "DH-E-FAKE-9"}, fail: "required scenario DH-E-FAKE-9",
		},
		"indexed but unauthored required scenario": {
			env: map[string]string{SecretEnv: "k", RequireEnv: "DH-E-TOOLCHAIN-1"}, fail: "required scenario DH-E-TOOLCHAIN-1",
		},
		"empty required list": {
			env: map[string]string{SecretEnv: "k", RequireEnv: ""},
		},
		"required list": {
			env: map[string]string{SecretEnv: "k", RequireEnv: " DH-E-TOOLCHAIN-2,DH-E-TOOLCHAIN-2 "}, planned: []string{"TestRehearsal"},
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
	// The index disagreeing with the scenario tests fails the preflight too.
	if _, err := Preflight(map[string]string{SecretEnv: "k"}, x, nil); err == nil || !strings.Contains(err.Error(), "DH-E-TOOLCHAIN-2 is indexed under DH-S-18") {
		t.Errorf("Preflight with the authored file missing = %v", err)
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
