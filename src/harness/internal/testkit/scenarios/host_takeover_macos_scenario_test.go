//go:build scenario

package scenarios

import (
	"runtime"
	"testing"
)

// TestModelCommandCannotBecomeOwnerMacOS is the DH-E-HOST-8 scenario
// (docs/harness/areas/host-telemetry.md; DH-D-HOST-2, DH-D-HOST-3; tech-spec sec.
// 4.5: authored in DH-S-15, run in DH-S-39 step 39.4): the DH-E-HOST-7 attempt
// under Seatbelt.
//
// Given a macOS desktop session with the built binary and the home, host, browser,
// session and takeover script of DH-E-HOST-7 (takeover_test.go), with K once in
// file and once in keychain protection, the script also trying keychain-read (the
// item through /usr/bin/security), debugger (attaching to the host), launchd (a
// job that repeats the steps from outside) and apple-event (the browser asked to
// open the approval page),
// When the script runs under workspace-write, again with network denied, and
// again under danger-full-access,
// Then confined: no write lands in the home; the read of <home>/secrets and every
// connection to the control socket are refused by the profile; the debugger cannot
// attach; no reply holds a credential; the peer rule and the outcomes of the
// cookie replay, the keychain read, the launchd job and the Apple Event are
// recorded and shown by dsh sandbox status, and each that succeeds is listed as an
// effect the backend does not govern, never as a pass. Under danger-full-access
// the result is as in DH-E-HOST-7.
// Cleanup: as DH-E-HOST-7; DSH_SCENARIO_TAKEOVER_CLEANUP also removes the
// submitted job and the keychain item, and logs their labels when it cannot.
//
// Inputs: DSH_SCENARIO_WEB_ARGS (host_web_test.go); DSH_SCENARIO_TAKEOVER_*
// (takeover_test.go), the script written for bash, its STORE taking {protection}
// file or keychain.
//
//dsh:scenario DH-E-HOST-8
func TestModelCommandCannotBecomeOwnerMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Fatalf("DH-E-HOST-8 runs on macOS, not %s", runtime.GOOS)
	}
	bin := dshBinary(t)
	for _, protection := range []string{"file", "keychain"} {
		for _, m := range takeoverModes {
			t.Run(protection+"/"+m.name, func(t *testing.T) {
				r := runTakeover(t, bin, protection, m.mode, m.network, false)
				if !r.confined() {
					return
				}
				r.refused(t, "read-secrets", "debugger")
				r.controlRefused(t)
				for _, n := range []string{"get-cookie", "post-cookie", "keychain-read", "launchd", "apple-event"} {
					s := r.step(t, n)
					t.Logf("%s: %s ended %s: %s", r.mode, n, s.Outcome, s.Detail)
				}
				if r.peerRule == "" {
					t.Errorf("%s: dsh sandbox status shows no peer rule:\n%s", r.mode, r.status)
				}
			})
		}
	}
}
