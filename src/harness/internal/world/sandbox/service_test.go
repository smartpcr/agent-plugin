package sandbox_test

import (
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/internal/mint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

// TestWeakerEvidenceWins: a probe that showed every promised effect governed and a
// ready that does not name the network give Partial, missing the network; the
// same holds the other way round (TC-04a, DH-ADR-SANDBOX-3).
//
//dsh:test DH-T-SANDBOX-3
func TestWeakerEvidenceWins(t *testing.T) {
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, t.TempDir())
	all := sandbox.Promised(p)
	noNet := slices.DeleteFunc(slices.Clone(all), func(e sandbox.Effect) bool { return e == sandbox.EffectNetwork })
	for name, e := range map[string]sandbox.Evidence{
		"ready weaker": {Backend: sandbox.BackendLandlock, Probe: all, Start: noNet},
		"probe weaker": {Backend: sandbox.BackendLandlock, Probe: noNet, Start: all},
	} {
		c := sandbox.Enforce(p, e)
		if c.State != sandbox.StatePartial || c.Backend != sandbox.BackendLandlock || !slices.Equal(c.Missing, []sandbox.Effect{sandbox.EffectNetwork}) {
			t.Errorf("%s: Enforce = %+v, want Partial missing [network]", name, c)
		}
	}
	if c := sandbox.Enforce(p, sandbox.Evidence{Backend: sandbox.BackendBwrap, Probe: all, Start: all}); c.State != sandbox.StateFull || len(c.Missing) != 0 {
		t.Fatalf("both full: Enforce = %+v, want Full", c)
	}
}

// Unknown evidence is never Full, a custom runner is never Full, an unknown
// effect name governs nothing, and an allowed network is not promised.
//
//dsh:test DH-T-SANDBOX-3
func TestEnforcementIsNeverStrongerThanTheEvidence(t *testing.T) {
	p := sandboxtest.Policy(sandbox.ReadOnly)
	all := sandbox.Promised(p)
	if c := sandbox.Enforce(p, sandbox.Evidence{Backend: sandbox.BackendBwrap}); c.State != sandbox.StatePartial || !slices.Equal(c.Missing, all) {
		t.Errorf("no evidence: %+v, want Partial missing everything promised", c)
	}
	if c := sandbox.Enforce(p, sandbox.Evidence{Backend: sandbox.BackendBwrap, Custom: true, Probe: all, Start: all}); c.State != sandbox.StatePartial {
		t.Errorf("custom runner: %+v, want Partial", c)
	}
	if c := sandbox.Enforce(p, sandbox.Evidence{Backend: sandbox.BackendBwrap, Probe: []sandbox.Effect{"fs-writes"}, Start: []sandbox.Effect{"fs-writes"}}); !slices.Contains(c.Missing, sandbox.EffectFSWrite) {
		t.Errorf("unknown effect name: %+v, want fs-write missing", c)
	}
	open, err := sandbox.Issue(mint.Spec{Mode: string(sandbox.ReadOnly), Network: true})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(sandbox.Promised(open), sandbox.EffectNetwork) {
		t.Errorf("a policy that allows the network promises it: %v", sandbox.Promised(open))
	}
	exp := sandbox.Enforce(p, sandbox.Evidence{Backend: sandbox.BackendLandlock, Probe: all, Start: all, Exposed: []sandbox.Effect{"home-read", "home-read"}, Scratch: "/s"})
	if exp.State != sandbox.StateFull || !slices.Equal(exp.Exposed, []sandbox.Effect{"home-read"}) || exp.Scratch != "/s" {
		t.Errorf("exposure and scratch: %+v", exp)
	}
}

// danger-full-access is Unconfined with every effect missing, and only under
// backend none; a confined policy under backend none, danger-full-access under a
// confining backend and the zero Policy give no valid Confinement (M2-R-12).
//
//dsh:test DH-T-SANDBOX-3
func TestUnconfinedOnlyForDangerFullAccess(t *testing.T) {
	full := sandboxtest.Policy(sandbox.DangerFullAccess)
	c := sandbox.Enforce(full, sandbox.Evidence{Backend: sandbox.BackendNone})
	if c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone || !slices.Equal(c.Missing, sandbox.Effects) || !c.Valid() {
		t.Fatalf("danger-full-access: %+v", c)
	}
	if len(sandbox.Promised(full)) != 0 {
		t.Fatalf("danger-full-access promises %v", sandbox.Promised(full))
	}
	for name, c := range map[string]sandbox.Confinement{
		"confined under none":    sandbox.Enforce(sandboxtest.Policy(sandbox.ReadOnly), sandbox.Evidence{Backend: sandbox.BackendNone}),
		"unconfined under bwrap": sandbox.Enforce(full, sandbox.Evidence{Backend: sandbox.BackendBwrap}),
		"zero policy":            sandbox.Enforce(sandbox.Policy{}, sandbox.Evidence{Backend: sandbox.BackendNone}),
		"unknown backend":        sandbox.Enforce(full, sandbox.Evidence{Backend: "chroot"}),
	} {
		if c.Valid() {
			t.Errorf("%s: Enforce = %+v, want no valid confinement", name, c)
		}
	}
}

// Expect judges from the probe statuses: the first available backend is the
// selected one and its probe stands for the start, so a probe missing the
// network is Partial naming it; no available backend is Unconfined under none
// with every effect missing (C37, `dsh sandbox status`).
//
//dsh:test DH-T-SANDBOX-3
func TestExpectFromProbeStatuses(t *testing.T) {
	all := sandbox.Promised(sandboxtest.Policy(sandbox.WorkspaceWrite, t.TempDir()))
	noNet := slices.DeleteFunc(slices.Clone(all), func(e sandbox.Effect) bool { return e == sandbox.EffectNetwork })
	statuses := []sandbox.Status{
		{Backend: sandbox.BackendBwrap, Reason: "not installed"},
		{Backend: sandbox.BackendLandlock, Available: true, Governed: noNet, Exposed: []sandbox.Effect{sandbox.EffectDelegatedExecution}},
	}
	c := sandbox.Expect(statuses)
	if c.State != sandbox.StatePartial || c.Backend != sandbox.BackendLandlock || !slices.Equal(c.Missing, []sandbox.Effect{sandbox.EffectNetwork}) || !c.Valid() {
		t.Errorf("Expect = %+v, want Partial under landlock missing [network]", c)
	}
	if c := sandbox.Expect([]sandbox.Status{{Backend: sandbox.BackendBwrap, Available: true, Governed: all}}); c.State != sandbox.StateFull || len(c.Missing) != 0 {
		t.Errorf("Expect = %+v, want Full", c)
	}
	for _, none := range [][]sandbox.Status{nil, statuses[:1]} {
		if c := sandbox.Expect(none); c.State != sandbox.StateUnconfined || c.Backend != sandbox.BackendNone || !slices.Equal(c.Missing, sandbox.Effects) {
			t.Errorf("Expect(%v) = %+v, want Unconfined under none", none, c)
		}
	}
}
