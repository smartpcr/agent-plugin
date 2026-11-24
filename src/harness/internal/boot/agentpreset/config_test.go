package agentpreset_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// derived extends standard and is declared before it: a forward reference, valid
// because the declarations are one set.
const presetsProfile = `- insert:
    - id: scope
      name: session.scope
    - id: presets
      name: agent.presets
      config:
        default: standard
    - id: derived
      name: agent.preset
      config:
        id: derived
        extends: standard
    - id: standard
      name: agent.preset
      config:
        id: standard
        children:
          tool.read:
            config:
              root: v1
`

// bundle-preset.md §6.2, §6.3: the configuration path checks the presets of a
// candidate tree as one set, so an edit whose Extends names no declaration or
// closes a cycle, or whose Default names no declaration, is rejected whole and the
// running registry keeps its set; a forward reference loads.
func TestConfigEditRejectsABrokenPresetSet(t *testing.T) {
	cases := map[string]struct{ text, path, msg string }{
		"unknown extends": {"- insert:\n    - id: orphan\n      name: agent.preset\n      config:\n        id: orphan\n        extends: nowhere\n",
			"orphan.config.extends", `extends "nowhere", which is not declared`},
		"extends cycle": {"- id: standard\n  merge:\n    extends: derived\n",
			"derived.config.extends", "extends cycle: derived -> standard -> derived"},
		"unknown default": {"- id: presets\n  merge:\n    default: ghost\n",
			"presets.config.default", `default "ghost" is not declared`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t)
			home, def := temphome.Dir(t), profile.Definition{Name: "presets"}
			temphome.Write(t, profile.LayerPath(home, def.Name), presetsProfile)
			root := testkit.Tree(t)
			fiber, err := root.Use(loader.Component, loader.Options{Resolver: fx.cat})
			if err != nil {
				t.Fatal(err)
			}
			if err := fiber.Wait(); err != nil {
				t.Fatal(err)
			}
			ldr, err := loader.Key.Get(root)
			if err != nil {
				t.Fatal(err)
			}
			cs, err := config.NewService(config.ServiceOptions{
				Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
					return profile.Stack(def, profile.Sources{Home: home, Read: profile.ReadFile(read)})
				},
				Loader: ldr, Catalog: fx.cat, Context: root,
				TreeChecks: []config.TreeCheck{agentpreset.CheckTree(fx.cat)},
			})
			if err != nil {
				t.Fatal(err)
			}
			if err := cs.Current().Report.Err(); err != nil {
				t.Fatalf("the forward reference was refused: %v", err)
			}
			if err := ldr.ApplyLayers(cs.Current().Layers); err != nil {
				t.Fatal(err)
			}
			reg, err := agentpreset.Key.Get(root)
			if err != nil {
				t.Fatal(err)
			}
			roster := func() string {
				var out []string
				for _, in := range reg.List() {
					out = append(out, fmt.Sprintf("%s#%d/%s/%s", in.ID, in.Revision, in.Digest, in.Broken))
				}
				return strings.Join(out, ",") + " default " + reg.Default()
			}
			want := roster()
			for _, in := range reg.List() {
				if in.Broken != "" {
					t.Fatalf("%s is broken after boot: %s", in.ID, in.Broken)
				}
			}
			if ids := len(reg.List()); ids != 2 || reg.Default() != "standard" {
				t.Fatalf("roster after boot = %s", want)
			}

			temphome.Write(t, profile.UserLayerPath(home), tc.text)
			err = cs.Reload()
			if err == nil {
				t.Fatal("the broken preset set was accepted")
			}
			if !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), tc.msg) {
				t.Errorf("diagnostic does not name %s (%s):\n%v", tc.path, tc.msg, err)
			}
			if got := roster(); got != want {
				t.Errorf("roster after the rejected edit = %q, want %q", got, want)
			}
		})
	}
}
