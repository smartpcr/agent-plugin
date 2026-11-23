package storetest_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/storetest"
)

// hookless is memstore as a production build of a provider whose
// RefuseDerived is test-only: the field shadows the promoted method, so the
// store is everything memstore is except a storetest.DerivedRefuser.
type hookless struct {
	*memstore.Store
	RefuseDerived struct{}
}

// WithoutTestHooks skips exactly the cases whose test-only hook the store lacks,
// saying why, and runs every other case; a store that has the hook runs the case
// under the option too.
func TestWithoutTestHooksSkipsOnlyTheHookCases(t *testing.T) {
	if _, ok := any(hookless{}).(storetest.DerivedRefuser); ok {
		t.Fatal("the fixture still has the hook")
	}
	for _, tc := range []struct {
		name    string
		factory func() store.Store
		skipped []string
	}{
		{"hookless", func() store.Store { return hookless{Store: memstore.New()} }, []string{"DerivedRowRefused"}},
		{"with the hook", func() store.Store { return memstore.New() }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var skipped []string
			storetest.Run(t, func(t *testing.T) store.Store {
				t.Cleanup(func() {
					if t.Skipped() {
						mu.Lock()
						skipped = append(skipped, lastElem(t.Name()))
						mu.Unlock()
					}
				})
				return tc.factory()
			}, storetest.WithoutTestHooks())
			mu.Lock()
			defer mu.Unlock()
			if !slices.Equal(skipped, tc.skipped) {
				t.Fatalf("skipped %v, want %v", skipped, tc.skipped)
			}
		})
	}
}

func lastElem(name string) string {
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' {
			return name[i+1:]
		}
	}
	return name
}
