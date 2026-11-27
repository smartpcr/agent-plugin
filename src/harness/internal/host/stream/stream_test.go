package stream_test

import (
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/stream"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
)

// A topic is registered once, as an effect of its owner: a second owner of the
// same topic fails to load, and unloading the owner removes the topic.
func TestSourcesAreEffectsOfTheirOwner(t *testing.T) {
	root, hub := testkit.Tree(t), stream.New()
	register := func(name, topic string) (*cordis.Fiber, error) {
		f, err := root.Apply(name, func(c *cordis.Context) error { return hub.Register(c, stream.Source{Topic: topic}) })
		if err != nil {
			t.Fatal(err)
		}
		return f, f.Wait()
	}
	owner, err := register("owner", "session.list")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := register("again", "session.list"); err == nil {
		t.Error("a second registration of session.list loaded")
	}
	if got := hub.Topics(); !slices.Equal(got, []string{"session.list"}) {
		t.Fatalf("topics = %v", got)
	}
	owner.Dispose()
	if got := hub.Topics(); len(got) != 0 {
		t.Errorf("topics after the owner unloaded = %v", got)
	}
}
