package builtin

import (
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
)

// Every family's items build into one catalogue, and each family owns only its
// own prefix.
func TestSetsBuild(t *testing.T) {
	c, err := Catalog()
	if err != nil {
		t.Fatal(err)
	}
	families := map[string][]catalog.Item{
		"boot": Boot(), "host": Host(), "util": Util(),
		"session": Session(), "agent": Agent(), "llm": LLM(), "tools": Tools(),
		"fs": FS(), "sandbox": Sandbox(), "human": Human(), "permission": Permission(),
		"wire": Wire(), "cli": CLI(), "storage": Storage(), "ui": UI(),
	}
	total := 0
	for family, items := range families {
		for _, item := range items {
			if got, _, _ := strings.Cut(item.Name, "."); got != family {
				t.Fatalf("%s is listed under family %s", item.Name, family)
			}
			if _, ok := c.Resolve(item.Name); !ok {
				t.Fatalf("%s does not resolve", item.Name)
			}
		}
		total += len(items)
	}
	if len(c.Items()) != total || len(Sets()) != len(families) {
		t.Fatalf("catalogue has %d items, families %d", len(c.Items()), total)
	}
}
