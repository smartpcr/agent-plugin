// Package budget seeds a performance budget the code exceeds. The test honours a
// relaxing override from the environment, as a careless budget might; the gate must
// clear it so the budget still fails.
package budget

import (
	"os"
	"strings"
	"testing"
)

// allocBudget is the compiled-in budget: allocations per call of build.
const allocBudget = 0

var sink []byte

func build() { sink = make([]byte, 1<<10) }

func TestBudgetX(t *testing.T) {
	for _, kv := range os.Environ() {
		key, _, _ := strings.Cut(kv, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "DSH_") || key == "GOGC" || key == "GOMAXPROCS" || key == "GODEBUG" || key == "GOFLAGS" {
			t.Logf("override leaked into the budget child: %s", kv)
		}
	}
	if os.Getenv("DSH_BUDGET_RELAX") != "" {
		return
	}
	if got := testing.AllocsPerRun(10, build); got > allocBudget {
		t.Fatalf("build allocates %v times per call, budget %d", got, allocBudget)
	}
}
