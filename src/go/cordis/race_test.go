package cordis_test

import (
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

// A provider that becomes active while a dependent is being created must still be
// seen by the dependent: the dependent reads its providers and joins the set that
// provider notifications reach in one step, so no activation falls between the two.
func TestADependentCreatedWhileItsProviderActivatesStillLoads(t *testing.T) {
	for i := range 300 {
		root := cordis.New()
		var provider, consumer *cordis.Fiber
		var wg sync.WaitGroup
		wg.Add(2)
		t.Cleanup(func() { Within(t, "use goroutines", wg.Wait) })
		go func() { defer wg.Done(); provider = Must(root.Use(Counter, nil)) }()
		go func() { defer wg.Done(); consumer = Must(root.Use(NeedsCounter, nil)) }()
		Within(t, "use", wg.Wait)
		MustWait(t, provider)
		MustWait(t, consumer)
		if state := consumer.State(); state != cordis.Active {
			t.Fatalf("iteration %d: the dependent is %s with its provider Active", i, state)
		}
		Within(t, "dispose", root.Dispose)
	}
}
