//go:build scenario

package scenarios

import (
	"runtime"
	"testing"
)

// TestModelCommandCannotBecomeOwnerLinux is the DH-E-HOST-7 scenario
// (docs/harness/areas/host-telemetry.md; DH-D-HOST-2, DH-D-HOST-3 with
// DH-D-SANDBOX-7 and DH-D-EXEC-2; tech-spec sec. 4.5: authored in DH-S-15, run in
// DH-S-39 step 39.4): a command run for the model cannot make itself the owner.
//
// Given the built binary on one of three Linux machines -- (a) bubblewrap; (b)
// Landlock only, with a desktop session bus; (c) as (a) but the test user's
// control group is not writable and no user service manager runs, so the
// process-group rung is used -- and on it a fresh home holding K in file
// protection, a running web host with one signed-in real browser, a session whose
// one tool always asks and the takeover script (takeover_test.go), which also
// tries proc-mem and proc-fd (the host's memory and descriptors through /proc),
// list-secrets and list-run (the listings of <home>/secrets and <home>/run) and
// service-manager (the user service manager running the steps from outside),
// When the script runs under workspace-write, again with network denied, and
// again under danger-full-access,
// Then confined, on every machine: no write lands in the home; no control reply
// holds a link or a cookie; the /proc reads fail; the tool call reaches the host
// and the approval stays pending. On (a) and (b) the exchange and both posts are
// refused auth/untrusted-peer and the peer rule is exact; on (c) it is best-effort
// and each outcome is recorded. On (a) secrets/ and run/ list empty and the
// service manager is not reached. On (b) the key file is readable and the result
// lists secret-read, the control endpoint answers without a credential, and the
// service-manager step is listed as delegated-execution with its outcome. Under
// danger-full-access the run is reported unconfined and whatever succeeds is
// logged as DH-ADR-HOST-12's residual risk; in no run does a harness interface
// return a credential.
// Cleanup: DSH_SCENARIO_TAKEOVER_CLEANUP signs out and removes any unit the
// service-manager step created; the host stops and the home is deleted here. A
// failed run keeps the script's log, the sandbox status and the host output in
// the test log.
//
// Inputs: DSH_SCENARIO_WEB_ARGS (host_web_test.go); DSH_SCENARIO_TAKEOVER_*
// (takeover_test.go), the script written for bash; DSH_SCENARIO_HOST7_MACHINE,
// a, b or c.
//
//dsh:scenario DH-E-HOST-7
func TestModelCommandCannotBecomeOwnerLinux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Fatalf("DH-E-HOST-7 runs on Linux, not %s", runtime.GOOS)
	}
	machine := need(t, "DSH_SCENARIO_HOST7_MACHINE", "a (bubblewrap), b (Landlock only, session bus) or c (process-group rung)")
	if machine != "a" && machine != "b" && machine != "c" {
		t.Fatalf("DSH_SCENARIO_HOST7_MACHINE is %q, want a, b or c", machine)
	}
	bin := dshBinary(t)
	backend := map[string]string{"a": "bwrap", "b": "landlock", "c": "bwrap"}[machine]
	p := takeoverPlatform{steps: []string{"proc-mem", "proc-fd", "list-secrets", "list-run", "service-manager"}, backends: []string{backend}}
	for _, m := range takeoverModes {
		t.Run(m.name, func(t *testing.T) {
			r := runTakeover(t, bin, "file", m.mode, m.network, p)
			if !r.confined() {
				return
			}
			r.refused(t, "proc-mem", "proc-fd")
			switch machine {
			case "a":
				r.peer(t, "exact")
				for _, n := range []string{"list-secrets", "list-run"} {
					if err := emptyListingError(r.step(t, n)); err != nil {
						t.Errorf("%s: %v", r.mode, err)
					}
				}
				r.refused(t, "service-manager")
			case "b":
				r.peer(t, "exact")
				// The spec: on (b) the stored key file is readable (Landlock grants
				// reads beneath /); a run where it is not has a misconfigured
				// fixture or an incomplete attempt, never a pass.
				if s := r.step(t, "read-secrets"); s.Outcome != "succeeded" {
					t.Errorf("%s: on Landlock only the key file read ended %s (%s), want it to succeed", r.mode, s.Outcome, s.Detail)
				}
				r.listed(t, "secret-read")
				r.listed(t, "delegated-execution")
				t.Logf("%s: service-manager ended %s", r.mode, r.step(t, "service-manager").Outcome)
			case "c":
				r.peer(t, "best-effort")
				t.Logf("%s: service-manager ended %s", r.mode, r.step(t, "service-manager").Outcome)
			}
		})
	}
}
