//go:build scenario

package scenarios

import (
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
)

// TestMain runs the scenarios through the scenario helper, which removes the dsh
// built once for this test binary when DSH_BIN does not name one.
func TestMain(m *testing.M) { scenario.Main(m) }
