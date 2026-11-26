//go:build linux

package launch

import (
	"strings"
	"testing"

	"github.com/landlock-lsm/go-landlock/landlock"
)

// TestLandlockConfigRefusesUnknownKinds: the config of every kernel version's
// handled set carries that set, and a set with a kind outside the reference set
// is refused before any ruleset exists.
//
//dsh:test DH-T-SANDBOX-4
func TestLandlockConfigRefusesUnknownKinds(t *testing.T) {
	for abi := 0; abi <= LandlockReference+2; abi++ {
		rs := Ruleset{ABI: abi, Version: min(abi, LandlockReference), Handled: LandlockHandled(abi)}
		cfg, err := landlockConfig(rs)
		if err != nil {
			t.Fatalf("ABI %d: %v", abi, err)
		}
		if cfg.HandledAccessFS != landlock.AccessFSSet(rs.Handled) {
			t.Errorf("ABI %d: handled %v, want %#x", abi, cfg.HandledAccessFS, rs.Handled)
		}
	}
	unknown := uint64(1) << 63
	rs := Ruleset{ABI: LandlockReference, Version: LandlockReference, Handled: LandlockHandled(LandlockReference) | unknown}
	if _, err := landlockConfig(rs); err == nil || !strings.Contains(err.Error(), "outside the reference set") {
		t.Errorf("unknown kind %#x: err %v, want a refusal", unknown, err)
	}
}
