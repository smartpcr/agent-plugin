package scenarios

import (
	"testing"

	//lint:ignore scenarioimport the accepted site: a reasoned directive at the import
	"internal/world/subprocess"

	"internal/agent" // want `scenario file imports internal/agent: a scenario imports only`
)

// A scenario file that skips the helper is still a scenario file: the bypass
// is reported (TC-17a).
func TestBypass(t *testing.T) { agent.Run(); _ = subprocess.Spec{} }
