package launch

import (
	"net"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// What a confined stage observes about itself, after its confinement and before
// the canary (DH-ADR-SANDBOX-3: an effect counts only where it is shown). Each
// observation is a function variable so a test can stand in for the host.
var (
	// noNetwork reports that the stage sees only loopback interfaces, as in a
	// network namespace bubblewrap unshared.
	noNetwork = func() bool {
		ifs, err := net.Interfaces()
		if err != nil {
			return false
		}
		for _, i := range ifs {
			if i.Flags&net.FlagLoopback == 0 {
				return false
			}
		}
		return true
	}
	// busUnreachable reports that no same-user service that runs programs on a
	// caller's behalf (the session bus, the user's systemd manager) can be
	// reached from the stage (bus_linux.go; never shown elsewhere).
	busUnreachable = sessionBusUnreachable
	// homeKept reports that the harness home is read-only for the stage.
	homeKept = homeReadOnly
)

// delegated is the delegated-execution observation every confiner shares. When
// the stage can still reach a session bus, delegated execution cannot be stopped
// and the effect is not claimed, so Enforce lists it missing and the start is
// partial: the owner default "partial when delegated execution cannot be
// stopped" (I2 Q4, evidence/DH-S-13.md).
func delegated(governed []sandbox.Effect) []sandbox.Effect {
	if busUnreachable() {
		governed = append(governed, sandbox.EffectDelegatedExecution)
	}
	return governed
}

// confineBwrap is the bubblewrap confiner. bubblewrap confines the stage from
// outside (the stage is its command), so there is no step to take; the stage
// claims what it can show from inside: file writes (decided by the canary, which
// a read-only root refuses), the network when only loopback is visible, the
// harness home when it is mounted read-only above every write root, and delegated
// execution when no session bus is reachable.
func confineBwrap(p Plan) ([]sandbox.Effect, error) {
	governed := []sandbox.Effect{sandbox.EffectFSWrite}
	if noNetwork() {
		governed = append(governed, sandbox.EffectNetwork)
	}
	if homeAboveRoots(p.Home, p.Write) && homeKept(p.Home) {
		governed = append(governed, sandbox.EffectHarnessHome)
	}
	return delegated(governed), nil
}
