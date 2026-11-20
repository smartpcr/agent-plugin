//go:build scenario

package x

import "testing"

func TestScenarioAlwaysFails(t *testing.T) {
	t.Fatal("a scenario test ran in the default gate")
}
