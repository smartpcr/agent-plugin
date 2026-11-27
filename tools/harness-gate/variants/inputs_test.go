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
// branch, not names the package sets for a child, not another test's; a name a
// method builds from its receiver's field (route.model) is resolved; a branch on
// runtime.GOOS counts only on the OS that takes it.
func TestInputsDerivesTheTestsInputs(t *testing.T) {
	sc := []acceptance.Scenario{
		{ID: "DH-E-FIX-1", File: "inputs/fixture_scenario_test.go", Test: "TestFixture"},
		{ID: "DH-E-FIX-2", File: "inputs/fixture_scenario_test.go", Test: "TestOther"},
	}
	base := []string{"DSH_SCENARIO_FIX_C_MODEL", "DSH_SCENARIO_FIX_INNER", "DSH_SCENARIO_FIX_LAYER", "DSH_SCENARIO_FIX_MACHINES"}
	for goos, extra := range map[string][]string{
		"":        {"DSH_SCENARIO_FIX_POSIX", "DSH_SCENARIO_FIX_STEP", "DSH_SCENARIO_FIX_WINDOWS"},
		"linux":   {"DSH_SCENARIO_FIX_POSIX", "DSH_SCENARIO_FIX_STEP"},
		"windows": {"DSH_SCENARIO_FIX_STEP", "DSH_SCENARIO_FIX_WINDOWS"},
	} {
		got, err := InputsFor("testdata", sc, []string{"DH-E-FIX-1"}, goos)
		if err != nil {
			t.Fatal(err)
		}
		want := append(slices.Clone(base), extra...)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("InputsFor(goos %q) = %v, want %v", goos, got, want)
		}
	}
	if _, err := InputsFor("testdata", sc, []string{"DH-E-FIX-9"}, ""); err == nil {
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
		names, err := InputsFor(repoRoot, scenarios, ids, v.GOOS())
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

// TestVariantInputsPinned is an oracle written by hand from the scenario sources,
// independent of the extractor: what each case's test must (and must not) get.
func TestVariantInputsPinned(t *testing.T) {
	f, x := committed(t)
	scenarios, err := acceptance.ScanScenarios(repoRoot, []string{filepath.Join(repoRoot, "src", "harness")})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		variant   string
		want, not []string
	}{
		// FS-2 and VIEW-5 call claudeCodeRoute.model(t) (signedin_routes_test.go).
		{"windows-host", []string{"DSH_SCENARIO_CLAUDE_CODE_MODEL", "DSH_SCENARIO_FS2_TASKS"}, nil},
		{"linux-wsl", []string{"DSH_SCENARIO_CLAUDE_CODE_MODEL", "DSH_SCENARIO_VIEW5_MOVE"}, nil},
		// STORAGE-6 reads the host commands only when runtime.GOOS != "windows".
		{"windows-host:two-account", []string{"DSH_SCENARIO_STORAGE6_USER", "DSH_SCENARIO_STORAGE6_SEED"},
			[]string{"DSH_SCENARIO_STORAGE6_START", "DSH_SCENARIO_STORAGE6_STATUS", "DSH_SCENARIO_STORAGE6_STOP"}},
		{"linux-wsl:two-account", []string{"DSH_SCENARIO_STORAGE6_USER", "DSH_SCENARIO_STORAGE6_START", "DSH_SCENARIO_STORAGE6_STATUS", "DSH_SCENARIO_STORAGE6_STOP"}, nil},
	} {
		v, ok := f.Variant(tc.variant)
		if !ok {
			t.Fatalf("no variant %s", tc.variant)
		}
		ids, err := f.Require(x, Stage, v.Label)
		if err != nil {
			t.Fatal(err)
		}
		got, err := InputsFor(repoRoot, scenarios, ids, v.GOOS())
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range tc.want {
			if !slices.Contains(got, n) {
				t.Errorf("%s: %s is not required; got %v", tc.variant, n, got)
			}
		}
		for _, n := range tc.not {
			if slices.Contains(got, n) {
				t.Errorf("%s (%s) requires %s, which its test reads only on another OS", tc.variant, v.GOOS(), n)
			}
		}
	}
}

var (
	literalNeed = regexp.MustCompile(`\bneed(?:List)?\(t, "(DSH_SCENARIO_[A-Z0-9_]+)"`)
	routeModel  = regexp.MustCompile(`\b(?:claudeCodeRoute|r)\.model\(t\)`)
)

// TestInputsCoverTheTestBodies is a second, textual oracle: every name a
// required test's own body passes literally to need/needList, and the Claude
// model for a body that calls a route's model, is derived for its variant on some
// OS (the OS-free derivation).
func TestInputsCoverTheTestBodies(t *testing.T) {
	f, x := committed(t)
	scenarios, err := acceptance.ScanScenarios(repoRoot, []string{filepath.Join(repoRoot, "src", "harness")})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range f.Variants {
		if !v.InScope() {
			continue
		}
		ids, err := f.Require(x, Stage, v.Label)
		if err != nil {
			t.Fatal(err)
		}
		got, err := InputsFor(repoRoot, scenarios, ids, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, sc := range scenarios {
			if !slices.Contains(ids, sc.ID) {
				continue
			}
			src, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(sc.File)))
			if err != nil {
				t.Fatal(err)
			}
			_, body, ok := strings.Cut(strings.ReplaceAll(string(src), "\r\n", "\n"), "\nfunc "+sc.Test+"(")
			if !ok {
				t.Fatalf("%s: no func %s", sc.File, sc.Test)
			}
			body, _, _ = strings.Cut(body, "\n}\n")
			want := []string{}
			for _, m := range literalNeed.FindAllStringSubmatch(body, -1) {
				want = append(want, m[1])
			}
			if routeModel.MatchString(body) {
				want = append(want, "DSH_SCENARIO_CLAUDE_CODE_MODEL")
			}
			for _, n := range want {
				if !slices.Contains(got, n) {
					t.Errorf("%s: %s (%s) reads %s, not derived", v.Label, sc.Test, sc.ID, n)
				}
			}
		}
	}
}

// TestWorkflowLocksSharedHypervisor: the vm:* jobs, which share one hypervisor's
// guests, recorder address and power commands, hold one lock for the whole job, and
// a lock has at most two variants (GitHub keeps a single pending job per group).
func TestWorkflowLocksSharedHypervisor(t *testing.T) {
	job := s18Job(t)
	if !strings.Contains(job, "concurrency:\n      group: ${{ matrix.lock && format('dsh-s18-lock-{0}', matrix.lock)") ||
		!strings.Contains(job, "cancel-in-progress: false") {
		t.Error("the s18 job has no per-lock concurrency group that queues instead of cancelling")
	}
	locks := map[string][]string{}
	for _, entry := range strings.Split(job, "\n          - variant: ")[1:] {
		label, rest, _ := strings.Cut(entry, "\n")
		lock := ""
		for _, l := range strings.Split(rest, "\n") {
			if !strings.HasPrefix(l, "            ") {
				break
			}
			if v, ok := strings.CutPrefix(strings.TrimSpace(l), "lock: "); ok {
				lock = v
			}
		}
		if strings.Contains(rest, "check: power-cut") && lock == "" {
			t.Errorf("%s drives the hypervisor's guests without a lock", label)
		}
		if lock != "" {
			locks[lock] = append(locks[lock], label)
		}
	}
	for lock, labels := range locks {
		if len(labels) > 2 {
			t.Errorf("lock %s has %d variants %v; a third would cancel the pending one", lock, len(labels), labels)
		}
	}
	if len(locks["hypervisor"]) != 2 {
		t.Errorf("lock hypervisor holds %v, want both vm:* variants", locks["hypervisor"])
	}
}
