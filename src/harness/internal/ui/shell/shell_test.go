package shell_test

import (
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/ui/shell"
)

// Regions and contributions are effects of their owner and every change bumps the
// epoch: a contribution needs a declared region, unloading a contributor removes
// what it placed, and unloading the region's owner removes the region.
func TestRegionsAndContributionsFollowTheirOwners(t *testing.T) {
	root, s := testkit.Tree(t), shell.New()
	load := func(name string, body func(*cordis.Context) error) (*cordis.Fiber, error) {
		f, err := root.Apply(name, body)
		if err != nil {
			t.Fatal(err)
		}
		return f, f.Wait()
	}
	contribute := func(id string) func(*cordis.Context) error {
		return func(c *cordis.Context) error { return s.Contribute(c, shell.Contribution{Region: "sidebar", ID: id}) }
	}
	if _, err := load("early", contribute("a")); err == nil {
		t.Fatal("a contribution to an undeclared region loaded")
	}
	region, err := load("region", func(c *cordis.Context) error { return s.Declare(c, shell.Region{Name: "sidebar", Kind: shell.List}) })
	if err != nil {
		t.Fatal(err)
	}
	before := s.Epoch()
	item, err := load("item", contribute("a"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := load("dup", contribute("a")); err == nil {
		t.Error("a second contribution with id a loaded")
	}
	if s.Epoch() <= before {
		t.Errorf("epoch %d did not move past %d on a contribution", s.Epoch(), before)
	}
	before = s.Epoch()
	item.Dispose()
	if s.Epoch() <= before {
		t.Errorf("epoch %d did not move past %d when the contributor unloaded", s.Epoch(), before)
	}
	if _, err := load("again", contribute("a")); err != nil {
		t.Errorf("id a is still held after its contributor unloaded: %v", err)
	}
	region.Dispose()
	if _, err := load("late", contribute("b")); err == nil {
		t.Error("sidebar outlived its owner")
	}
}
