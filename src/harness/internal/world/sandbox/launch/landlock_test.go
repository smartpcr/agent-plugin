package launch

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// TestLandlockRulesFollowTheKernelVersion: the handled kinds are those of
// min(kernel, reference); a kernel without Landlock is refused
// sandbox/unavailable; fs-write is governed only at the reference version and
// listed missing below it; harness-home needs truncation (version 3) and a home
// outside every write root (DH-ADR-NATIVESYSTEM-2, TC-04a).
//
//dsh:test DH-T-SANDBOX-4
func TestLandlockRulesFollowTheKernelVersion(t *testing.T) {
	base := t.TempDir()
	root, home := filepath.Join(base, "work"), filepath.Join(base, "home")
	plan := Plan{Kind: Confined, Backend: sandbox.BackendLandlock, Write: []string{root}, Home: home}
	for _, row := range []struct {
		abi               int
		handled           uint64
		governed, missing []sandbox.Effect
	}{
		{1, 1<<13 - 1, nil, []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectHarnessHome}},
		{2, 1<<14 - 1, nil, []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectHarnessHome}},
		{3, 1<<15 - 1, []sandbox.Effect{sandbox.EffectHarnessHome}, []sandbox.Effect{sandbox.EffectFSWrite}},
		{4, 1<<15 - 1, []sandbox.Effect{sandbox.EffectHarnessHome}, []sandbox.Effect{sandbox.EffectFSWrite}},
		{5, 1<<16 - 1, []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectHarnessHome}, nil},
		{7, 1<<16 - 1, []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectHarnessHome}, nil},
	} {
		rs, err := LandlockRules(row.abi, plan)
		if err != nil {
			t.Fatalf("ABI %d: %v", row.abi, err)
		}
		if rs.Handled != row.handled || rs.Version != min(row.abi, LandlockReference) {
			t.Errorf("ABI %d: handled %#x version %d, want %#x", row.abi, rs.Handled, rs.Version, row.handled)
		}
		if !slices.Equal(rs.Governed, row.governed) || !slices.Equal(rs.Missing, row.missing) {
			t.Errorf("ABI %d: governed %v missing %v, want %v and %v", row.abi, rs.Governed, rs.Missing, row.governed, row.missing)
		}
		for _, r := range rs.Rules {
			if r.Access&^rs.Handled != 0 || (!r.Dir && r.Access&^fileKinds != 0) {
				t.Errorf("ABI %d: rule %+v grants a kind the ruleset does not handle or a file cannot take", row.abi, r)
			}
		}
	}
	if _, err := LandlockRules(0, plan); herr.Kind(err) != sandbox.CodeUnavailable {
		t.Errorf("LandlockRules(0) = %v, want %s", err, sandbox.CodeUnavailable)
	}
	for name, write := range map[string][]string{
		"home is a root":       {home},
		"home inside a root":   {base},
		"root inside the home": {filepath.Join(home, "sub")},
	} {
		p := plan
		p.Write = write
		rs, err := LandlockRules(5, p)
		if err != nil || slices.Contains(rs.Governed, sandbox.EffectHarnessHome) {
			t.Errorf("%s: governed %v, %v; want no harness-home", name, rs.Governed, err)
		}
	}
	p := plan
	p.Home = ""
	if rs, _ := LandlockRules(5, p); slices.Contains(rs.Governed, sandbox.EffectHarnessHome) {
		t.Errorf("no home named: governed %v, want no harness-home", rs.Governed)
	}
}

// TestBwrapStageClaimsOnlyWhatItSees: the bubblewrap stage claims file writes
// (the canary decides them), the network only when it sees nothing but
// loopback, the harness home only when it is read-only and no root lies inside
// it, and delegated execution only when no session bus is reachable; a
// reachable bus leaves delegated-execution unclaimed (I2 Q4).
//
//dsh:test DH-T-SANDBOX-4
func TestBwrapStageClaimsOnlyWhatItSees(t *testing.T) {
	saved := []func() bool{noNetwork, busUnreachable}
	savedHome := homeKept
	t.Cleanup(func() { noNetwork, busUnreachable, homeKept = saved[0], saved[1], savedHome })
	base := t.TempDir()
	home := filepath.Join(base, "home")
	for _, row := range []struct {
		name               string
		offline, bus, kept bool
		write              []string
		want               []sandbox.Effect
	}{
		{"everything shown", true, false, true, []string{filepath.Join(base, "work")}, sandbox.Effects},
		{"home inside a root is still kept", true, false, true, []string{base}, sandbox.Effects},
		{"a bus is reachable", true, true, true, nil, []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectNetwork, sandbox.EffectHarnessHome}},
		{"the network is visible", false, false, true, nil, []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectHarnessHome, sandbox.EffectDelegatedExecution}},
		{"the home is writable", true, false, false, nil, []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectNetwork, sandbox.EffectDelegatedExecution}},
		{"a root inside the home", true, false, true, []string{filepath.Join(home, "w")}, []sandbox.Effect{sandbox.EffectFSWrite, sandbox.EffectNetwork, sandbox.EffectDelegatedExecution}},
	} {
		noNetwork = func() bool { return row.offline }
		busUnreachable = func() bool { return !row.bus }
		homeKept = func(string) bool { return row.kept }
		got, err := confineBwrap(Plan{Kind: Confined, Backend: sandbox.BackendBwrap, Write: row.write, Home: home})
		if err != nil || !slices.Equal(got, row.want) {
			t.Errorf("%s: confineBwrap = %v, %v; want %v", row.name, got, err, row.want)
		}
	}
}
