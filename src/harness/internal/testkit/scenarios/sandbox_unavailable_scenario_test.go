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
// Then status reports the backend unavailable before any command; both
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
	if st := strings.ToLower(r.status(t)); !strings.Contains(st, "unavailable") {
		t.Errorf("status before any command does not report the backend unavailable:\n%s", st)
	}
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
