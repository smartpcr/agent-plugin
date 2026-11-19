// Package budget seeds a data race inside a performance-budget test: the test gate
// runs TestBudget* under -race like every other test, so the race is caught there.
package budget

import (
	"sync"
	"testing"
)

// allocBudget is the compiled-in budget, which the test keeps.
const allocBudget = 1

func TestBudgetRacy(t *testing.T) {
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	hits := 0
	wg.Add(1)
	go func() {
		defer wg.Done()
		hits++
	}()
	hits++
	wg.Wait()
	if got := testing.AllocsPerRun(10, func() {}); got > allocBudget {
		t.Fatalf("%v allocations per call, budget %d (hits %d)", got, allocBudget, hits)
	}
}
