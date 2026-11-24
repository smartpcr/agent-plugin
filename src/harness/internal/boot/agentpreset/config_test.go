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
				TreeChecks: []config.TreeCheck{agentpreset.CheckTree(fx.cat, nil)},
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

// bundle-preset.md §6.1: a child's config may use os, env, launch and home
// expressions. The declaration keeps its literal id, extends and children in the
// set check, so it answers the Default and a child's Extends, while a reference
// that is genuinely unknown is still an issue; an id that is itself an expression
// cannot be placed, so the set's references are left to the load.
func TestPresetSetCheckKeepsDeclarationsWithExpressionConfigs(t *testing.T) {
	const base = `- insert:
    - id: scope
      name: session.scope
    - id: presets
      name: agent.presets
      config:
        default: %s
    - id: derived
      name: agent.preset
      config:
        id: derived
        extends: %s
        children:
          tool.read:
            config:
              root:
                $expr: env.HOME
    - id: standard
      name: agent.preset
      config:
        id: standard
        children:
          tool.read:
            config:
              root: ${{ env.HOME }}/work
`
	cases := map[string]struct {
		text      string
		env       map[string]string // the evaluator's env; nil for no evaluator
		path, msg string            // the one issue wanted; empty for none
	}{
		"default and parent with expression configs": {text: fmt.Sprintf(base, "standard", "standard")},
		"derived is the default":                     {text: fmt.Sprintf(base, "derived", "standard")},
		"unknown default still refused": {text: fmt.Sprintf(base, "ghost", "standard"),
			path: "presets.config.default", msg: `default "ghost" is not declared`},
		"unknown parent still refused": {text: fmt.Sprintf(base, "standard", "nowhere"),
			path: "derived.config.extends", msg: `extends "nowhere", which is not declared`},
		"computed id leaves references to the load": {text: fmt.Sprintf(base, "standard", "standard") +
			"    - id: computed\n      name: agent.preset\n      config:\n        id: ${{ env.PRESET }}\n"},
		"computed default is not checked": {text: fmt.Sprintf(base, "${{ env.PRESET }}", "standard")},
		"unknown id cannot hide a literal cycle": {text: strings.Replace(fmt.Sprintf(base, "standard", "standard"),
			"        id: standard\n", "        id: standard\n        extends: derived\n", 1) + computedDecl,
			path: "derived.config.extends", msg: "extends cycle: derived -> standard -> derived"},
		"computed id answers the default": {text: fmt.Sprintf(base, "picked", "standard") + computedDecl,
			env: map[string]string{"PRESET": "picked"}},
		"computed id that answers nothing": {text: fmt.Sprintf(base, "ghost", "standard") + computedDecl,
			env: map[string]string{"PRESET": "picked"}, path: "presets.config.default", msg: `default "ghost" is not declared`},
		"computed extends closes a cycle": {text: strings.Replace(fmt.Sprintf(base, "standard", "standard"),
			"        id: standard\n", "        id: standard\n        extends: ${{ env.BASE }}\n", 1),
			env: map[string]string{"BASE": "derived"}, path: "derived.config.extends", msg: "extends cycle: derived -> standard -> derived"},
		"computed extends names no declaration": {text: fmt.Sprintf(base, "standard", "${{ env.BASE }}"),
			env: map[string]string{"BASE": "nowhere"}, path: "derived.config.extends", msg: `extends "nowhere", which is not declared`},
		"computed default names no declaration": {text: fmt.Sprintf(base, "${{ env.PRESET }}", "standard"),
			env: map[string]string{"PRESET": "ghost"}, path: "presets.config.default", msg: `default "ghost" is not declared`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			fx := newFixture(t)
			home, def := temphome.Dir(t), profile.Definition{Name: "presets"}
			temphome.Write(t, profile.LayerPath(home, def.Name), tc.text)
			layers, issues, err := profile.Stack(def, profile.Sources{Home: home})
			if err != nil || len(issues) > 0 {
				t.Fatalf("stack: %v %v", err, issues)
			}
			var ev loader.Evaluator
			if tc.env != nil {
				ev = envEvaluator(tc.env)
			}
			r := config.Check(layers, config.CheckOptions{Catalog: fx.cat, TreeChecks: []config.TreeCheck{agentpreset.CheckTree(fx.cat, ev)}})
			if tc.path == "" {
				if err := r.Err(); err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if len(r.Issues) != 1 || r.Issues[0].Path != tc.path || !strings.Contains(r.Issues[0].Message, tc.msg) {
				t.Fatalf("issues = %v, want one at %s (%s)", r.Issues, tc.path, tc.msg)
			}
		})
	}
}

// computedDecl is a declaration whose id is computed: ${{ env.PRESET }}.
const computedDecl = "    - id: computed\n      name: agent.preset\n      config:\n        id: ${{ env.PRESET }}\n"

// envEvaluator computes "env.<NAME>" from env; any other source is an error, as a
// use name is without an entry's context.
type envEvaluator map[string]string

func (envEvaluator) Check(string, loader.ScopeKind) error { return nil }

func (e envEvaluator) Eval(source string, _ loader.Scope) (any, error) {
	if name, ok := strings.CutPrefix(source, "env."); ok {
		if v, ok := e[name]; ok {
			return v, nil
		}
	}
	return nil, fmt.Errorf("cannot compute %q", source)
}
