//go:build scenario

package scenarios

import (
	"runtime"
	"testing"
)

// TestModelCommandCannotBecomeOwnerWindows is the DH-E-HOST-9 scenario
// (docs/harness/areas/host-telemetry.md; DH-D-HOST-2, DH-D-HOST-3; tech-spec sec.
// 4.5: authored in DH-S-15, run in DH-S-39 step 39.4): the DH-E-HOST-7 attempt
// under the restricted token.
//
// Given Windows 11 or Windows Server 2022 with the built binary and the home,
// host, browser, session and takeover script of DH-E-HOST-7 (takeover_test.go),
// written for PowerShell, with K once in file and once in keychain protection, the
// script also trying credential-manager (the stored item), open-process (the host
// process for memory read), scheduled-task and com-server (a task and an
// out-of-process COM server repeating the steps from outside) and break-away
// (leaving the job),
// When the script runs under workspace-write and under danger-full-access (the
// restricted token cannot deny the network, so there is no network-denied run),
// Then confined: no write lands in the home; the read of secrets\ and every
// connection to the control socket fail on the directory label; the host process
// cannot be opened; the job cannot be left; the exchange and both posts are
// refused auth/untrusted-peer and the peer rule is exact; the outcomes of the
// credential-manager read, the scheduled task and the COM server are recorded,
// each run to an end, and listed as ungoverned where they succeed
// (observationError); dsh tool call either reaches the host
// (the approval stays pending) or reports unavailable, and the result says which.
// Under danger-full-access the result is as in DH-E-HOST-7.
// Cleanup: as DH-E-HOST-7; DSH_SCENARIO_TAKEOVER_CLEANUP also deletes the
// scheduled task and runs dsh sandbox cleanup, and keeps the security descriptors
// of secrets\ or run\ when their label cannot be read back.
//
// Inputs: DSH_SCENARIO_WEB_ARGS (host_web_test.go); DSH_SCENARIO_TAKEOVER_*
// (takeover_test.go), the script written for PowerShell, its STORE taking
// {protection} file or keychain.
//
//dsh:scenario DH-E-HOST-9
func TestModelCommandCannotBecomeOwnerWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Fatalf("DH-E-HOST-9 runs on Windows, not %s", runtime.GOOS)
	}
	bin := dshBinary(t)
	p := takeoverPlatform{steps: []string{"credential-manager", "open-process", "scheduled-task", "com-server", "break-away"}, unavailableOK: true}
	for _, protection := range []string{"file", "keychain"} {
		for _, m := range windowsTakeoverModes() {
			t.Run(protection+"/"+m.name, func(t *testing.T) {
				r := runTakeover(t, bin, protection, m.mode, m.network, p)
				if !r.confined() {
					return
				}
				r.refused(t, "read-secrets", "open-process", "break-away")
				r.controlRefused(t)
				r.peer(t, "exact")
				for _, n := range []string{"credential-manager", "scheduled-task", "com-server"} {
					s := r.step(t, n)
					t.Logf("%s: %s ended %s: %s", r.mode, n, s.Outcome, s.Detail)
					if err := observationError(s, r.shell.Confinement.Exposed); err != nil {
						t.Errorf("%s: %v", r.mode, err)
					}
				}
			})
		}
	}
}

// windowsTakeoverModes are the runs DH-E-HOST-9 makes: workspace-write and
// danger-full-access, both with network allowed. The restricted token cannot
// deny the network, and a launch that asks it to is refused with
// sandbox/network-unenforceable (sandbox-ssh.md), so there is no network-denied run.
func windowsTakeoverModes() []struct{ name, mode, network string } {
	var out []struct{ name, mode, network string }
	for _, m := range takeoverModes {
		if m.network == "allowed" {
			out = append(out, m)
		}
	}
	return out
}
