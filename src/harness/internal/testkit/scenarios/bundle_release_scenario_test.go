//go:build scenario

package scenarios

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
)

// TestReleaseBinaryAgreesWithItsLayers is the DH-E-BUNDLE-4 scenario
// (docs/harness/areas/bundle-preset.md; DH-D-BUNDLE-3, DH-D-BUNDLE-6; tech-spec
// sec. 4.5: authored in DH-S-16, run in DH-S-48): the release binary agrees with
// its own layers on every platform.
//
// Given the release archive for this platform unpacked on a real machine with
// nothing else installed (DSH_BIN names the unpacked dsh),
// When dsh <profile> --help runs for each profile in the build, each profile is
// started on an empty home and stopped, then web is started and stopped,
// Then no start-up report holds an issue; the enabled shell stack matches the
// system; office and computer-use report unavailable and nothing is downloaded;
// each host starts and stops cleanly.
// Cleanup: the temporary homes are removed by scenario.Run.
//
// Inputs: DSH_SCENARIO_BUNDLE4_PROFILES, the build's profiles, ";"-separated;
// DSH_SCENARIO_BUNDLE4_SHELLS, the shells this system has, ";"-separated;
// DSH_SCENARIO_BUNDLE4_REPORT, a command over {home} and {out} that waits for the
// host on {home} to be ready and writes its start-up report to {out} as JSON
// {"issues":[...],"shells":[...],"capabilities":{name:state},"downloads":[...]}.
//
//dsh:scenario DH-E-BUNDLE-4
func TestReleaseBinaryAgreesWithItsLayers(t *testing.T) {
	scenario.Run(t, "DH-E-BUNDLE-4", scenario.Needs{
		Bin: true, Deadline: 30 * time.Minute,
		Env: []string{"DSH_SCENARIO_BUNDLE4_PROFILES", "DSH_SCENARIO_BUNDLE4_SHELLS", "DSH_SCENARIO_BUNDLE4_REPORT"},
	}, func(s *scenario.S) {
		profiles := needList(t, "DSH_SCENARIO_BUNDLE4_PROFILES", "the build's profiles")
		shells := needList(t, "DSH_SCENARIO_BUNDLE4_SHELLS", "the system's shells")
		sort.Strings(shells)
		for _, p := range profiles {
			if r := runDsh(t, time.Minute, nil, s.Bin, p, "--help"); r.code != 0 || strings.TrimSpace(r.stdout+r.stderr) == "" {
				t.Errorf("dsh %s --help exited %d with %q", p, r.code, r.stdout+r.stderr)
			}
		}
		for _, p := range append(profiles, "web") {
			home := s.TempDir()
			h := startDshOnTerminal(t, []string{"DSH_HOME=" + home}, s.Bin, "--home", home, p)
			var report struct {
				Issues       []string          `json:"issues"`
				Shells       []string          `json:"shells"`
				Capabilities map[string]string `json:"capabilities"`
				Downloads    []string          `json:"downloads"`
			}
			operatorJSON(t, "DSH_SCENARIO_BUNDLE4_REPORT", "reads the start-up report", map[string]string{"home": home}, &report)
			if len(report.Issues) != 0 {
				t.Errorf("%s: the start-up report holds issues: %v", p, report.Issues)
			}
			sort.Strings(report.Shells)
			if strings.Join(report.Shells, ";") != strings.Join(shells, ";") {
				t.Errorf("%s: the enabled shells are %v, the system has %v", p, report.Shells, shells)
			}
			for _, c := range []string{"office", "computer-use"} {
				if report.Capabilities[c] != "unavailable" {
					t.Errorf("%s: %s reports %q, want unavailable", p, c, report.Capabilities[c])
				}
			}
			if len(report.Downloads) != 0 {
				t.Errorf("%s: the host downloaded %v", p, report.Downloads)
			}
			if err := terminate(t, h); err != nil || !h.await(time.Minute) || h.code() != 0 {
				t.Errorf("%s: the host did not stop cleanly: exit %d, %v\n%s", p, h.code(), err, h.output())
			}
		}
	})
}
