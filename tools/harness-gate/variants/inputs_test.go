package variants

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/acceptance"
)

// TestInputsDerivesTheTestsInputs: the inputs of a scenario test are the names it
// and its package's helpers pass to need/needList, through a constant or a
// forwarding parameter, inside closures; not optional os.Getenv reads, not a role's
// branch, not names the package sets for a child, not another test's.
func TestInputsDerivesTheTestsInputs(t *testing.T) {
	sc := []acceptance.Scenario{
		{ID: "DH-E-FIX-1", File: "inputs/fixture_scenario_test.go", Test: "TestFixture"},
		{ID: "DH-E-FIX-2", File: "inputs/fixture_scenario_test.go", Test: "TestOther"},
	}
	got, err := InputsFor("testdata", sc, []string{"DH-E-FIX-1"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"DSH_SCENARIO_FIX_INNER", "DSH_SCENARIO_FIX_LAYER", "DSH_SCENARIO_FIX_MACHINES", "DSH_SCENARIO_FIX_STEP"}
	if !slices.Equal(got, want) {
		t.Errorf("InputsFor = %v, want %v", got, want)
	}
	if _, err := InputsFor("testdata", sc, []string{"DH-E-FIX-9"}); err == nil {
		t.Error("an ID no scenario test proves was accepted")
	}
}

var (
	s18Env   = regexp.MustCompile(`(?m)^      (DSH_SCENARIO_[A-Z0-9_]+): (.+)$`)
	s18Label = regexp.MustCompile(`(?m)^          - variant: (\S+)$`)
)

// s18Job returns the s18 job of the scenario workflow.
func s18Job(t *testing.T) string {
	t.Helper()
	wf, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "harness-scenarios.yml"))
	if err != nil {
		t.Fatal(err)
	}
	_, job, ok := strings.Cut(strings.ReplaceAll(string(wf), "\r\n", "\n"), "\n  s18:\n")
	if !ok {
		t.Fatal("the scenario workflow has no s18 job")
	}
	return job
}

// TestWorkflowBindsEveryVariantInput: every input the scenario tests of an
// in-scope variant read (require --inputs) is bound in the s18 job's env, from the
// environment's variables or, for the report, the workspace.
func TestWorkflowBindsEveryVariantInput(t *testing.T) {
	f, x := committed(t)
	scenarios, err := acceptance.ScanScenarios(repoRoot, []string{filepath.Join(repoRoot, "src", "harness")})
	if err != nil {
		t.Fatal(err)
	}
	job := s18Job(t)
	bound := map[string]string{}
	for _, m := range s18Env.FindAllStringSubmatch(job, -1) {
		bound[m[1]] = m[2]
	}
	for _, v := range f.Variants {
		if !v.InScope() {
			continue
		}
		ids, err := f.Require(x, Stage, v.Label)
		if err != nil {
			t.Fatal(err)
		}
		names, err := InputsFor(repoRoot, scenarios, ids)
		if err != nil {
			t.Fatalf("%s: %v", v.Label, err)
		}
		for _, n := range names {
			b, ok := bound[n]
			switch {
			case !ok:
				t.Errorf("%s reads %s, which the s18 job does not bind", v.Label, n)
			case n == "DSH_SCENARIO_REPORT":
				if !strings.HasPrefix(b, "${{ github.workspace }}/") {
					t.Errorf("DSH_SCENARIO_REPORT is %s, want a workspace path", b)
				}
			case b != "${{ vars."+n+" }}":
				t.Errorf("%s is bound to %s, want ${{ vars.%s }}", n, b, n)
			}
		}
	}
	if len(s18Label.FindAllString(job, -1)) == 0 {
		t.Error("the s18 job lists no variant")
	}
}

// TestWorkflowSandboxStatusHasAHome: each sandbox status check of the s18 job runs
// on an existing home it created first; an absent home reports Partial, never the
// Full the bwrap service requires (README "Guarded preflights" step 2).
func TestWorkflowSandboxStatusHasAHome(t *testing.T) {
	lines := strings.Split(s18Job(t), "\n")
	n := 0
	for i, line := range lines {
		if !strings.Contains(line, "sandbox status") {
			continue
		}
		n++
		before, _, _ := strings.Cut(line, "sandbox status")
		_, arg, ok := strings.Cut(before, "--home ")
		if !ok {
			t.Errorf("s18 line %d runs sandbox status without --home: %s", i+1, strings.TrimSpace(line))
			continue
		}
		home := strings.Trim(strings.Fields(arg)[0], `"`)
		created := false
		for j := i; j >= 0 && j >= i-5 && !created; j-- {
			l := lines[j]
			if j == i {
				l = before
			}
			if home == "$home" {
				created = strings.Contains(l, `home="$(mktemp -d`)
			} else {
				created = strings.Contains(l, "mkdir") && strings.Contains(l, home+" ")
			}
		}
		if !created {
			t.Errorf("s18 line %d passes home %s it did not create: %s", i+1, home, strings.TrimSpace(line))
		}
	}
	if n < 2 {
		t.Errorf("the s18 job runs sandbox status %d times, want the local and the SSH check", n)
	}
}
