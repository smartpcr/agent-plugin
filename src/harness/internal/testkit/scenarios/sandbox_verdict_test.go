//go:build scenario

package scenarios

import "testing"

// The status reports of the confinement machines. bwrapFull is the report the
// pure-Go build printed under WSL2 (Ubuntu 24.04, bwrap installed): its backend
// lines name landlock as available too, so a report naming a backend somewhere
// is not the verdict of the machine.
const (
	bwrapFull = `state:   Full
backend: bwrap
missing: none
exposed: none
backends:
  bwrap: available, canary refused, governs fs-write, network, harness-home, delegated-execution
  landlock: available, canary refused, governs fs-write, harness-home
`
	landlockPartial = `state:   Partial
backend: landlock
missing: network
exposed: network
backends:
  bwrap: unavailable: bwrap not found on an absolute path
  landlock: available, canary refused, governs fs-write, harness-home
`
	partialUnnamed = `state:   Partial
backend: landlock
missing: none
exposed: none
backends:
  landlock: available, canary refused, governs fs-write
`
)

func TestVerdictIsTheReportHead(t *testing.T) {
	v := readVerdict(landlockPartial)
	if got, want := v.String(), "Partial landlock, missing [network], exposed [network]"; got != want {
		t.Errorf("verdict %q, want %q", got, want)
	}
	cases := []struct {
		name, report, backend string
		states                []string
		wrong                 bool
	}{
		{"bwrap machine as bwrap", bwrapFull, "bwrap", []string{"Full"}, false},
		{"bwrap machine as landlock-only", bwrapFull, "landlock", []string{"Full", "Partial"}, true},
		{"landlock machine as landlock", landlockPartial, "landlock", []string{"Full", "Partial"}, false},
		{"landlock machine as bwrap", landlockPartial, "bwrap", []string{"Full"}, true},
		{"landlock machine as ABI >= 5", landlockPartial, "landlock", []string{"Full"}, true},
		{"partial naming nothing missing", partialUnnamed, "landlock", []string{"Full", "Partial"}, true},
	}
	for _, c := range cases {
		if p := verdictProblem(readVerdict(c.report), c.backend, c.states...); (p != "") != c.wrong {
			t.Errorf("%s: problem %q, want wrong=%v", c.name, p, c.wrong)
		}
	}
}
