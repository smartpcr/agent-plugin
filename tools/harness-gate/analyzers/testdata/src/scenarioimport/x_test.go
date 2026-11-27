package scenarioimport

import (
	"testing"

	"internal/agent" // want `scenario file imports internal/agent: a scenario imports only`
	"internal/testkit/launcher"
	"internal/testkit/scenario"
)

func TestScenario(t *testing.T) {
	scenario.Run()
	launcher.Start()
	agent.Run()
}
