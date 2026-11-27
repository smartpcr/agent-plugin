//go:build scenario

package scenarios

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
)

// TestRequiredScenarioCannotSkip is the DH-E-UTIL-2 scenario
// (docs/harness/areas/util-test-support.md; DH-D-UTIL-8; tech-spec sec. 4.5:
// authored in DH-S-16, run in DH-S-39): a required scenario that cannot run fails
// the acceptance job.
//
// Given the acceptance host where the user is signed in to Claude Code (no API
// key exists anywhere: tech-spec sec. 4.3, M2-NG-2), DSH_REQUIRE_SCENARIOS
// listing DH-E-UTIL-1 and one scenario of another area (DSH_SCENARIO_UTIL2_IDS),
// and the client's configuration home (claudeCodeRoute.HomeVar) pointed at an
// empty directory in one run -- the client signed out, nothing of the user's
// sign-in touched -- and left as the user's in a second,
// When the job -- this scenario binary re-run, from this package's directory so
// scenario.Main finds the //dsh:scenario directives, on those scenarios
// (DSH_SCENARIO_UTIL2_TESTS) with a report of its own -- and then the checker
// (scenario.Check) run, once per run,
// Then signed out the job fails, the report has a failed row for a required
// scenario whose reason names the missing sign-in, and no row says passed;
// signed in the job passes only if both rows say passed and carry the digest of
// the binary under test.
// Cleanup: the reports and the empty configuration home are removed with the
// scenario's temporary directories.
//
// Inputs: DSH_SCENARIO_UTIL2_IDS, the two required IDs, comma-separated;
// DSH_SCENARIO_UTIL2_TESTS, a -test.run pattern selecting exactly their tests.
//
//dsh:scenario DH-E-UTIL-2
func TestRequiredScenarioCannotSkip(t *testing.T) {
	scenario.Run(t, "DH-E-UTIL-2", scenario.Needs{
		Bin: true, Deadline: time.Hour,
		Env: []string{"DSH_SCENARIO_UTIL2_IDS", "DSH_SCENARIO_UTIL2_TESTS"},
	}, func(s *scenario.S) {
		ids := scenario.SplitRequired(need(t, "DSH_SCENARIO_UTIL2_IDS", "DH-E-UTIL-1 and one scenario of another area"))
		if len(ids) != 2 || !contains(ids, "DH-E-UTIL-1") {
			t.Fatalf("DSH_SCENARIO_UTIL2_IDS is %v, want DH-E-UTIL-1 and one other", ids)
		}
		pattern := need(t, "DSH_SCENARIO_UTIL2_TESTS", "a -test.run pattern selecting the two scenarios")
		if strings.Contains(pattern, "TestRequiredScenarioCannotSkip") {
			t.Fatal("DSH_SCENARIO_UTIL2_TESTS selects this scenario itself")
		}
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		// go test runs the binary in its package directory, where scenario.Main
		// reads the directives that map each test to its ID.
		pkg, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(pkg, "main_test.go")); err != nil {
			t.Fatalf("the working directory %s is not the scenarios package: %v", pkg, err)
		}
		job := func(name string, env ...string) ([]scenario.Row, int) {
			report := filepath.Join(s.TempDir(), name+".jsonl")
			env = append(env, scenario.RequireEnv+"="+strings.Join(ids, ","), scenario.ReportEnv+"="+report, scenario.BinEnv+"="+s.Bin)
			h, err := launcher.Run(t, 25*time.Minute, launcher.Program{Argv: []string{self, "-test.run", pattern, "-test.v"}, Env: env, Dir: pkg})
			if err != nil {
				t.Fatalf("%s: the job: %v\n%s", name, err, h.Stdout.String())
			}
			f, err := os.Open(report)
			if err != nil {
				t.Fatalf("%s: the job wrote no report: %v\n%s", name, err, h.Stdout.String())
			}
			defer f.Close()
			rows, err := scenario.ReadReport(f)
			if err != nil {
				t.Fatal(err)
			}
			return rows, h.Process.ExitCode()
		}

		rows, code := job("signed-out", claudeCodeRoute.signedOut(t))
		if code == 0 {
			t.Error("signed out the job passed")
		}
		if scenario.Check(rows, ids) == nil {
			t.Error("signed out the checker passed")
		}
		named := false
		for _, r := range rows {
			if r.Result == scenario.Passed {
				t.Errorf("signed out %s says passed", r.ID)
			}
			reason := strings.ToLower(r.Reason)
			named = named || (contains(ids, r.ID) && r.Result == scenario.Failed &&
				(strings.Contains(reason, "sign-in") || strings.Contains(reason, "signed out") || strings.Contains(r.Reason, claudeCodeRoute.HomeVar)))
		}
		if !named {
			t.Errorf("signed out no failed row of a required scenario names the missing sign-in: %+v", rows)
		}

		rows, code = job("signed-in")
		if err := scenario.Check(rows, ids); code != 0 || err != nil {
			t.Errorf("signed in the job exited %d and the checker said %v", code, err)
		}
		data, err := os.ReadFile(s.Bin)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		for _, r := range rows {
			if contains(ids, r.ID) && r.Build != hex.EncodeToString(sum[:]) {
				t.Errorf("%s's row carries build %q, not the binary under test", r.ID, r.Build)
			}
		}
	})
}
