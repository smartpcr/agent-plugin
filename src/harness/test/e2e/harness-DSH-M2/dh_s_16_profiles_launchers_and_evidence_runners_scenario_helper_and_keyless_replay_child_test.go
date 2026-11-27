//go:build e2e

package e2e

// The scenario half of
// dh_s_16_profiles_launchers_and_evidence_runners_scenario_helper_and_keyless_replay_test.go:
// it imports the scenario helper, so the scenarioimport analyzer holds it to the
// helper, the launcher and the standard library; the step definitions, which
// reach other harness internals, stay in their own file.

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
)

const (
	shkBinEnv         = scenario.BinEnv
	shkRequireEnv     = scenario.RequireEnv
	shkReportEnv      = scenario.ReportEnv
	shkRequiredNotRun = scenario.RequiredNotRun
)

// TestE2E_shkRequiredScenarioChild is the scenario the "Missing input" steps run
// in a child of this test binary. Outside that child it has nothing to do.
func TestE2E_shkRequiredScenarioChild(t *testing.T) {
	v := os.Getenv(shkChildEnv)
	if v == "" {
		return
	}
	id, marker, _ := strings.Cut(v, "\t")
	scenario.Run(t, id, scenario.Needs{Bin: true, Deadline: time.Minute}, func(s *scenario.S) {
		if err := os.WriteFile(marker, []byte("ran"), 0o600); err != nil {
			s.Errorf("marker: %v", err)
		}
	})
}

type shkRow = scenario.Row

func shkReadReport(r io.Reader) ([]shkRow, error) { return scenario.ReadReport(r) }

func shkCheck(rows []shkRow, required []string) error { return scenario.Check(rows, required) }
