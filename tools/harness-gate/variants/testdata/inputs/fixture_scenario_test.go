//go:build scenario

package fixture

import (
	"os"
	"testing"
)

const machinesEnv = "DSH_SCENARIO_FIX_MACHINES"

func need(t *testing.T, name, what string) string { return os.Getenv(name) }

func needList(t *testing.T, name, what string) []string { return []string{need(t, name, what)} }

// operatorStep forwards its input parameter as the name.
func operatorStep(t *testing.T, input, what string) string { return need(t, input, what) }

func helper(t *testing.T) {
	operatorStep(t, "DSH_SCENARIO_FIX_STEP", "a step")
	_ = needList(t, machinesEnv, "machines")
}

func writer(t *testing.T) {
	need(t, "DSH_SCENARIO_FIX_ACK", "read in the guest only")
}

func child(t *testing.T) {
	need(t, "DSH_SCENARIO_FIX_SUB", "set by the scenario for its child")
}

//dsh:scenario DH-E-FIX-1
func TestFixture(t *testing.T) {
	if os.Getenv("DSH_SCENARIO_FIX_ROLE") == "writer" {
		writer(t)
		return
	}
	_ = os.Environ()
	_ = []string{"DSH_SCENARIO_FIX_SUB=x"}
	need(t, "DSH_SCENARIO_FIX_LAYER", "a layer")
	_ = os.Getenv("DSH_SCENARIO_FIX_OPTIONAL")
	helper(t)
	child(t)
	func() { need(t, "DSH_SCENARIO_FIX_INNER", "in a closure") }()
}

//dsh:scenario DH-E-FIX-2
func TestOther(t *testing.T) {
	need(t, "DSH_SCENARIO_FIX_OTHER", "another test's input")
}
