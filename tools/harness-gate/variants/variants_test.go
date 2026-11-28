package variants

import (
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
	"github.com/smartpcr/agent-plugin/tools/harness-gate/closure"
)

var update = flag.Bool("update", false, "rewrite testdata/coverage.golden from the committed variants file")

const repoRoot = "../../.."

func committed(t *testing.T) (*Fleet, *acceptance.Index) {
	t.Helper()
	x, err := acceptance.ReadIndex(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Read(repoRoot, closure.DefaultStory)
	if err != nil {
		t.Fatal(err)
	}
	return f, x
}

// TestVariantListCoversTheIndex is TC-21s1 "The variant list covers the index": the
// committed variants file and the DH-S-18 index entry cross-reference without a
// problem, macOS rows read "out of scope (platforms-windows-wsl)", and the coverage
// equals the golden.
func TestVariantListCoversTheIndex(t *testing.T) {
	f, x := committed(t)
	if p := f.Check(x, Stage); len(p) > 0 {
		t.Fatalf("the variant list does not cover %s:\n%s", Stage, strings.Join(p, "\n"))
	}
	macos := 0
	for _, v := range f.Variants {
		if isMacOS(v) {
			macos++
			if v.Scope != MacOSScope {
				t.Errorf("macOS row %s reads %q", v.Label, v.Scope)
			}
		}
	}
	if macos == 0 {
		t.Error("no macOS row; M2-EN-2 lists them out of scope")
	}
	got := f.Coverage(x, Stage)
	golden := filepath.Join("testdata", "coverage.golden")
	if *update {
		if os.Getenv("CI") != "" {
			t.Fatal("-update is refused under CI")
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("coverage differs from %s (rerun with -update after checking the change):\n%s", golden, got)
	}
}

const fixtureHead = `# fixture

## Variants

| Variant | Platform | Runner | Provider | Service | Preflight | Scenarios | Scope |
|---|---|---|---|---|---|---|---|
`

const fixtureTail = `
## Owner removals

| Scenario | Decision |
|---|---|
`

// allRow runs every S18 scenario on one well-formed variant.
func allRow(t *testing.T, x *acceptance.Index) string {
	ids, err := x.Required(Stage)
	if err != nil {
		t.Fatal(err)
	}
	return "| `linux-wsl` | Ubuntu | `self-hosted, linux, dsh-wsl` | tester | `service:linux-sandbox-bwrap` | bwrap Full | " +
		strings.Join(ids, ", ") + " | in scope |\n"
}

func parse(t *testing.T, doc string) *Fleet {
	t.Helper()
	f, err := Parse("variants.md", strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCheckRefuses(t *testing.T) {
	_, x := committed(t)
	ids, _ := x.Required(Stage)
	without := func(drop string) string {
		return "| `linux-wsl` | Ubuntu | `self-hosted, linux, dsh-wsl` | tester | `service:os-process` | p | " +
			strings.Join(slices.DeleteFunc(slices.Clone(ids), func(s string) bool { return s == drop }), ", ") + " | in scope |\n"
	}
	cases := []struct {
		name, rows, removals, want string
	}{
		{"a scenario with no variant", without("DH-E-NODEFS-1"), "", `DH-E-NODEFS-1 of DH-S-18 has no in-scope variant and no owner decision removing it \(M2-DOD-4\)`},
		{"a scenario only out of scope", without("DH-E-NODEFS-1") + "| `vm:macos-guest` | macOS guest | - | - | - | - | DH-E-NODEFS-1 | out of scope (platforms-windows-wsl) |\n", "", `DH-E-NODEFS-1 of DH-S-18 has no in-scope variant`},
		{"a removal by an invented decision", without("DH-E-NODEFS-1"), "| DH-E-NODEFS-1 | no hardware |\n", `removal of DH-E-NODEFS-1 names "no hardware", which is not an owner decision`},
		{"a macOS decision on a Linux row", allRow(t, x) + "| `linux-e2:musl` | Alpine | - | - | - | - | DH-E-NATIVESYSTEM-4 | out of scope (platforms-windows-wsl) |\n", "", `decision "platforms-windows-wsl" does not put DH-E-NATIVESYSTEM-4 on "Alpine" out of scope`},
		{"an unknown decision", allRow(t, x) + "| `linux-e2:musl` | Alpine | - | - | - | - | DH-E-NATIVESYSTEM-4 | out of scope (budget) |\n", "", `unknown decision "budget"`},
		{"a macOS row in scope", allRow(t, x) + "| `macos` | macOS | `self-hosted, linux, mac` | team | `service:os-process` | p | DH-E-HOST-4 | in scope |\n", "", `macOS row; it must read "out of scope \(platforms-windows-wsl\)"`},
		{"a wrong preflight", allRow(t, x) + "| `linux-e2:landlock` | Fedora | `self-hosted, linux, dsh-e2-controller` | team | `service:linux-landlock-only` | bwrap Full | DH-E-SANDBOX-1 | in scope |\n", "", `service:linux-landlock-only, whose preflight is "landlock", not "bwrap Full"`},
		{"an unknown service", allRow(t, x) + "| `linux-e2:x` | Fedora | `ubuntu-latest` | team | `service:magic` | p | DH-E-SANDBOX-1 | in scope |\n", "", `service "service:magic" is not a service of architecture.md`},
		{"a bad runner", allRow(t, x) + "| `linux-e2:x` | Fedora | `self-hosted, mac, x` | team | `service:os-process` | p | DH-E-SANDBOX-1 | in scope |\n", "", `runner "self-hosted, mac, x" is not`},
		{"a scenario outside the stage", allRow(t, x) + "| `linux-e2:x` | Fedora | `ubuntu-latest` | team | `service:os-process` | p | DH-E-FAKE-1 | in scope |\n", "", `lists DH-E-FAKE-1, which is not in the DH-S-18 entry`},
		{"a duplicate label", allRow(t, x) + allRow(t, x), "", `variant linux-wsl given twice`},
		{"a bad label", allRow(t, x) + "| `Linux E2` | Fedora | `ubuntu-latest` | team | `service:os-process` | p | DH-E-SANDBOX-1 | in scope |\n", "", `variant "Linux E2" is not a label`},
		{"no provider", allRow(t, x) + "| `linux-e2:x` | Fedora | `ubuntu-latest` |  | `service:os-process` | p | DH-E-SANDBOX-1 | in scope |\n", "", `names no provider`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := parse(t, fixtureHead+c.rows+fixtureTail+c.removals)
			p := f.Check(x, Stage)
			if !regexp.MustCompile(c.want).MatchString(strings.Join(p, "\n")) {
				t.Errorf("problems %q do not match %q", p, c.want)
			}
			if _, err := f.Require(x, Stage, "linux-wsl"); err == nil {
				t.Error("Require passed a fleet Check refuses")
			}
		})
	}
}

func TestParseRefusesAMissingTableOrColumn(t *testing.T) {
	for name, doc := range map[string]string{
		"no removals table": fixtureHead,
		"no scope column":   "## Variants\n\n| Variant | Platform | Runner | Provider | Service | Preflight | Scenarios |\n|---|---|---|---|---|---|---|\n" + fixtureTail,
	} {
		if _, err := Parse("variants.md", strings.NewReader(doc)); err == nil {
			t.Errorf("%s: parsed", name)
		}
	}
}

func TestRequireFiltersToTheVariant(t *testing.T) {
	f, x := committed(t)
	got, err := f.Require(x, Stage, "linux-e2:landlock")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"DH-E-SANDBOX-1", "DH-E-NATIVESYSTEM-1", "DH-E-NATIVESYSTEM-3", "DH-E-NATIVESYSTEM-4"}
	if !slices.Equal(got, want) {
		t.Errorf("Require = %v, want %v (index order)", got, want)
	}
	for label, want := range map[string]string{"macos": "out of scope", "nowhere": "lists no variant"} {
		if _, err := f.Require(x, Stage, label); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Require(%s) = %v, want an error containing %q", label, err, want)
		}
	}
}

func TestRunsOn(t *testing.T) {
	for runner, want := range map[string]string{
		"windows-latest":                        `"windows-latest"`,
		"self-hosted, linux, dsh-e2-controller": `["self-hosted","linux","dsh-e2-controller"]`,
	} {
		if got := (Variant{Runner: runner}).RunsOn(); got != want {
			t.Errorf("RunsOn(%q) = %s, want %s", runner, got, want)
		}
	}
}

var matrixEntry = regexp.MustCompile(`(?m)^\s*- variant: (\S+)\s*\n\s*runner: '([^']+)'`)

// TestWorkflowRunsEveryInScopeVariant binds the s18 matrix of harness-scenarios.yml
// to the variants file: one job per in-scope variant, on that variant's runner.
func TestWorkflowRunsEveryInScopeVariant(t *testing.T) {
	f, _ := committed(t)
	wf, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "harness-scenarios.yml"))
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]string{}
	for _, m := range matrixEntry.FindAllStringSubmatch(strings.ReplaceAll(string(wf), "\r\n", "\n"), -1) {
		if _, dup := jobs[m[1]]; dup {
			t.Errorf("s18 matrix lists %s twice", m[1])
		}
		jobs[m[1]] = m[2]
	}
	for _, v := range f.Variants {
		runner, ok := jobs[v.Label]
		delete(jobs, v.Label)
		switch {
		case !v.InScope() && ok:
			t.Errorf("s18 matrix runs %s, which is %s", v.Label, v.Scope)
		case v.InScope() && !ok:
			t.Errorf("s18 matrix has no job for in-scope variant %s", v.Label)
		case ok && runner != v.RunsOn():
			t.Errorf("s18 job %s runs on %s, variants.md gives %s", v.Label, runner, v.RunsOn())
		}
	}
	for label := range jobs {
		t.Errorf("s18 matrix runs %s, which variants.md does not list", label)
	}
}
