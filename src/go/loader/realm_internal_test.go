package loader

import (
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	. "github.com/smartpcr/agent-plugin/src/go/internal/testkit"
)

func realmCount(s *Service) (keys, users int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, n := range s.realmUsers {
		users += n
	}
	return len(s.realms), users
}

// DH-T-LOADER-3: a shared realm key is gone after its last entry.
func TestASharedRealmKeyIsReleasedWithItsLastEntry(t *testing.T) {
	root, _ := Quiet()
	t.Cleanup(func() { Within(t, "root dispose", root.Dispose) })
	MustWait(t, Must(root.Use(Component, Options{Resolver: Components{"CounterService": Counter}})))
	s := Must(Key.Get(root))
	shared := map[string]string{"x": "shared"}
	apply := func(entries ...EntryOptions) {
		t.Helper()
		Within(t, "apply", func() { NoError(t, s.Apply(entries)) })
	}

	apply(EntryOptions{ID: "a", Name: "CounterService", Isolate: shared}, EntryOptions{ID: "b", Name: "CounterService", Isolate: map[string]string{"x": "shared", "counter": "true"}})
	keys, users := realmCount(s)
	Equal(t, 2, keys, "x@shared and the private counter realm")
	Equal(t, 3, users)
	Equal(t, cordis.Active, s.Fiber(s.Find("a")).State())
	Equal(t, cordis.Active, s.Fiber(s.Find("b")).State())

	apply(EntryOptions{ID: "a", Name: "CounterService", Isolate: shared})
	keys, users = realmCount(s)
	Equal(t, 1, keys, "the private realm outlived its entry")
	Equal(t, 1, users)

	apply()
	keys, users = realmCount(s)
	Equal(t, 0, keys, "the shared realm outlived its last entry")
	Equal(t, 0, users)
}
