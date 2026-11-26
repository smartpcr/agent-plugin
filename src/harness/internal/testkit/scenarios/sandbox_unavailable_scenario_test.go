//go:build scenario

package scenarios

import (
	"strings"
	"testing"
)

// TestSandboxNoBackendConfinedCommandDoesNotRun is the DH-E-SANDBOX-2 scenario
// (docs/harness/areas/sandbox-ssh.md; tech-spec sec. 4.5: authored in DH-S-13,
// run in DH-S-39): on a Linux machine with no usable backend a confined command
// never runs, and only danger-full-access runs it, reported unconfined.
//
// Given the unconfined machine of DH-E-SANDBOX-1 (no bwrap, Landlock disabled at
// boot), the binary built from this checkout and a marker M in the workspace,
// When "dsh sandbox status" runs, and then a command creating M is asked for in
// a read-only session, a workspace-write session and a danger-full-access
// session,
// Then status, before any command, reads the unconfined contract of
// mustStatusUnconfined (verdict Unconfined, bwrap and landlock each unavailable); both
// confined starts fail with sandbox/unavailable and M is absent after each; the
// third creates M and its result reports the start unconfined.
//
// Inputs: the unconfined= machine of DSH_SCENARIO_SANDBOX1_MACHINES and the
// layer inputs of sandbox_remote_test.go. Restoring the machine image is the
// runbook's; the scratch directory is removed here.
//
//dsh:scenario DH-E-SANDBOX-2
func TestSandboxNoBackendConfinedCommandDoesNotRun(t *testing.T) {
	in := readSandboxInputs(t)
	r := prepareRemote(t, sandboxMachine(t, "unconfined"), pureGo)
	mustStatusUnconfined(t, r.status(t))
	m := startScriptedModel(t)
	h := r.sdk(t, r.bin, r.layer(t, in, m, "any", "sandbox2"), m)
	marker := r.ws + "/M"
	cmd := "touch '" + marker + "'"
	for _, mode := range []string{"read-only", "workspace-write"} {
		got := runCommands(t, h, m, h.createIn(t, in.route, r.ws, mode), cmd)
		mustRefuse(t, got[0], cmd+" under "+mode, "sandbox/unavailable")
		if r.exists(t, marker) {
			t.Fatalf("the %s start that was refused made %s", mode, marker)
		}
		t.Logf("%s: refused %s", mode, got[0].Raw)
	}
	got := runCommands(t, h, m, h.createIn(t, in.route, r.ws, "danger-full-access"), cmd)
	res := mustExit(t, got[0], cmd+" under danger-full-access", 0)
	if res.Confinement.State != "unconfined" {
		t.Errorf("the danger-full-access start reported %s; want unconfined", res.record())
	}
	if !r.exists(t, marker) {
		t.Errorf("the danger-full-access command did not make %s", marker)
	}
}

// unconfinedBackends are the Linux chain's backends, each of which status must
// list as unavailable on the unconfined machine.
var unconfinedBackends = []string{"bwrap", "landlock"}

// mustStatusUnconfined is the one status oracle for the unconfined machine,
// shared by DH-E-SANDBOX-1 (c), DH-E-SANDBOX-2 and DH-E-NATIVESYSTEM-3 (a): the
// overall verdict is the enforcement word Unconfined (tech-spec sec. 3,
// Full/Partial/Unconfined) and every backend line of unconfinedBackends says
// unavailable (sandbox.Status per backend, its Reason beside it). Neither half
// alone passes: the verdict without the lines, or the lines without the verdict,
// is a status that disagrees with itself.
func mustStatusUnconfined(t *testing.T, st string) {
	t.Helper()
	if !strings.Contains(st, "Unconfined") {
		t.Errorf("status on the unconfined machine does not give the verdict Unconfined:\n%s", st)
	}
	for _, b := range unconfinedBackends {
		found := false
		for _, line := range strings.Split(strings.ToLower(st), "\n") {
			if strings.Contains(line, b) && strings.Contains(line, "unavailable") {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("status on the unconfined machine has no %s line saying unavailable:\n%s", b, st)
		}
	}
}
