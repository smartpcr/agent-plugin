package scenarioimport

import (
	"testing"

	"internal/agent"
)

// A test file that does not use the scenario helper is not a scenario file.
func TestUnit(t *testing.T) { agent.Run() }
