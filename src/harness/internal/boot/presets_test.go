package boot

import (
	"context"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
)

// bundle-preset.md §6.2, §6.3: the start-up tree is held to the presets' set rule
// before anything is applied, whether or not the profile reloads: a Default that
// names no declaration or an Extends cycle stops the launch (status 78) with one
// fatal message naming the entry at fault.
func TestStartupRejectsABrokenPresetSet(t *testing.T) {
	cases := map[string]struct{ layer, path, msg string }{
		"unknown default": {`- insert:
    - id: presets
      name: agent.presets
      config:
        default: ghost
    - id: standard
      name: agent.preset
      config:
        id: standard
`, "presets.config.default", `default "ghost" is not declared`},
		"extends cycle": {`- insert:
    - id: presets
      name: agent.presets
      config:
        default: a
    - id: a
      name: agent.preset
      config:
        id: a
        extends: b
    - id: b
      name: agent.preset
      config:
        id: b
        extends: a
`, "a.config.extends", "extends cycle: a -> b -> a"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			opts, _ := fixture(t, tc.layer)
			if opts.Profile.Reload > 0 {
				t.Fatal("the profile reloads; this case is the boot without reload")
			}
			var cat *catalog.Catalog
			items := agentpreset.Items(func() agentpreset.Host { return agentpreset.Host{Catalogue: cat, Version: "test"} })
			cat, err := catalog.Build(append(builtin.Sets(), items)...)
			if err != nil {
				t.Fatal(err)
			}
			var stderr testkit.Buffer
			opts.Catalog, opts.Stderr = cat, &stderr
			if status := Run(context.Background(), opts); status != output.Config {
				t.Errorf("status = %d, want %d", status, output.Config)
			}
			got := stderr.String()
			if n := strings.Count(got, "dsh: fatal:"); n != 1 {
				t.Fatalf("%d fatal messages, want 1:\n%s", n, got)
			}
			if !strings.Contains(got, tc.path) || !strings.Contains(got, tc.msg) {
				t.Errorf("the message does not name %s (%s):\n%s", tc.path, tc.msg, got)
			}
		})
	}
}
