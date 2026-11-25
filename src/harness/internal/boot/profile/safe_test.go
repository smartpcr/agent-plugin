package profile

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
)

// writeFile writes text at path, creating its directory.
func writeFile(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// safeProfileText is a profile layer with an installed plugin in the plugins
// group, a first-party entry outside it, and an entry whose owner switches it off
// in safe mode itself (`disabled: !expr launch.safe`, as MCP servers do).
const safeProfileText = `# a person's profile layer
- insert:
    - id: plugins
      group: true
      children:
        - id: demo
          name: plugin.entry
          disabled: true
          config:
            dir: plugins/demo
    - id: mine
      name: boot.logging
      config:
        level: debug
    - id: mcp
      name: boot.logging
      disabled: !expr 'launch.safe'
`

// optionalBundle is an optional bundle as DH-T-BOOT-2 requires one: one insert of
// one group, disabled by default, with an "x." id.
const optionalBundle = `- insert:
    - id: x.demo
      group: true
      disabled: true
      children:
        - id: tool
          name: boot.logging
`

// TestSafeModeDisablesPluginsAndOptionalBundlesOnly is DH-T-BOOT-23 (DH-ADR-BOOT-12):
// with Safe set, the launcher layer disables the plugins group and every optional
// bundle's group and nothing else; the composed tree otherwise equals the ordinary
// one; launch.safe is true in expressions; an Apply that enables a plugin is saved
// to the profile layer while the launcher still holds the group off; and without
// Safe the launcher layer holds neither operation.
//
//dsh:test DH-T-BOOT-23
func TestSafeModeDisablesPluginsAndOptionalBundlesOnly(t *testing.T) {
	root := t.TempDir()
	def, err := Lookup(Web)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, LayerPath(root, def.Name), safeProfileText)
	writeFile(t, UserLayerPath(root), "- id: mine\n  merge:\n    destination: stdout\n")

	ordinary, issues, err := Stack(def, Sources{Home: root})
	if err != nil || len(issues) > 0 {
		t.Fatalf("stack: %v %v", err, issues)
	}
	safe, issues, err := Stack(def, Sources{Home: root, Safe: true})
	if err != nil || len(issues) > 0 {
		t.Fatalf("safe stack: %v %v", err, issues)
	}
	top := func(layers []loader.Layer) loader.Layer { return layers[len(layers)-1] }
	if l := top(ordinary); l.Label != LauncherLabel || len(l.Ops) != 0 {
		t.Errorf("without Safe the launcher layer holds %v, want nothing", l.Ops)
	}
	// The same files are read in both launches: only the launcher layer differs.
	if !slices.EqualFunc(ordinary[:len(ordinary)-1], safe[:len(safe)-1], func(a, b loader.Layer) bool {
		x, _ := json.Marshal(a.Ops)
		y, _ := json.Marshal(b.Ops)
		return a.Label == b.Label && a.Path == b.Path && a.Writable == b.Writable && string(x) == string(y)
	}) {
		t.Errorf("safe mode changed a layer below the launcher layer")
	}
	// The shipped web profile has no optional bundle yet, so through Stack the
	// launcher disables the plugins group alone.
	assertSafeOps(t, top(safe).Ops, PluginsGroup)
	assertOnlyDisabled(t, ordinary, safe, PluginsGroup)

	// With an optional bundle in the stack that an overlay has switched on, the
	// launcher also disables its group, and still nothing else.
	opt, _, err := ParseLayer(BundleLabel+"x-demo", "x-demo.yml", []byte(optionalBundle))
	if err != nil {
		t.Fatal(err)
	}
	if got := OptionalGroups(opt); !slices.Equal(got, []string{"x.demo"}) {
		t.Fatalf("optional groups = %v", got)
	}
	enable, _, err := ParseLayer(OverlayLabel+"1", "on.yml", []byte("- id: x.demo\n  disabled: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	files := ordinary[:len(ordinary)-1]
	n := len(def.Bundles)
	lower := slices.Concat(files[:n], []loader.Layer{opt}, files[n:], []loader.Layer{enable})
	ops := SafeOps(lower)
	assertSafeOps(t, ops, PluginsGroup, "x.demo")
	assertOnlyDisabled(t,
		append(slices.Clone(lower), loader.Layer{Label: LauncherLabel}),
		append(slices.Clone(lower), loader.Layer{Label: LauncherLabel, Ops: ops}),
		PluginsGroup, "x.demo")

	// A person's own "x." group in a file layer is not an optional bundle, and a
	// stack without a plugins group gets no operation that would match nothing.
	if got := OptionalGroups(loader.Layer{Label: ProfileLabel, Ops: opt.Ops}); len(got) != 0 {
		t.Errorf("a profile layer's x. group counted as an optional bundle: %v", got)
	}
	bare, _, err := Stack(def, Sources{Home: t.TempDir(), Safe: true})
	if err != nil {
		t.Fatal(err)
	}
	if l := top(bare); len(l.Ops) != 0 {
		t.Errorf("no plugins group, yet the launcher holds %v", l.Ops)
	}
	if _, _, issues := loader.Compose(bare); len(issues) > 0 {
		t.Errorf("safe compose of a bare home: %v", issues)
	}

	// launch.safe reads the flag, and the entry that reads it keeps its expression.
	for _, on := range []bool{true, false} {
		ev, err := expr.New(launch.Scope(launch.Data{Profile: def.Name, Home: root, Safe: on}, nil, nil))
		if err != nil {
			t.Fatal(err)
		}
		got, err := ev.Eval("launch.safe", loader.Scope{Kind: loader.DisabledScope})
		if err != nil || got != on {
			t.Errorf("launch.safe with Safe=%v = %v, %v", on, got, err)
		}
	}
	tree, _, _ := loader.Compose(safe)
	if e := find(tree, "mcp"); e == nil || e.DisabledExpr != "launch.safe" {
		t.Errorf("mcp entry = %+v, want disabled: !expr launch.safe kept", e)
	}

	// An edit made in safe mode that enables the plugin is saved to the profile
	// layer; the launcher layer, which outranks it, keeps the group off, so it takes
	// effect at the next ordinary start.
	svc, err := config.NewService(config.ServiceOptions{Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return Stack(def, Sources{Home: root, Safe: true, Read: ReadFile(read)})
	}})
	if err != nil {
		t.Fatal(err)
	}
	demo := PluginsGroup + loader.Separator + "demo"
	on := loader.Op{Kind: loader.OpOverride, ID: demo, Set: map[string]json.RawMessage{"disabled": json.RawMessage("false")}}
	if _, err := svc.Apply(context.Background(), config.Edit{Revision: svc.Desired().Revision, Ops: []loader.Op{on}}); err != nil {
		t.Fatalf("apply in safe mode: %v", err)
	}
	report := svc.Desired().Report
	if e := find(report.Tree, demo); e == nil || e.Disabled {
		t.Errorf("the plugin is not enabled in the saved layer: %+v", e)
	}
	if g := find(report.Tree, PluginsGroup); g == nil || !g.Disabled {
		t.Errorf("the plugins group is not held off in safe mode: %+v", g)
	}
	if owner, _ := report.Index.Owner(PluginsGroup, "disabled"); owner != LauncherLabel {
		t.Errorf("owner of plugins.disabled = %q, want %q", owner, LauncherLabel)
	}
	next, _, err := Stack(def, Sources{Home: root})
	if err != nil {
		t.Fatal(err)
	}
	tree, index, _ := loader.Compose(next)
	if e, g := find(tree, demo), find(tree, PluginsGroup); e == nil || e.Disabled || g == nil || g.Disabled {
		t.Errorf("next ordinary start: plugin %+v, group %+v; want both enabled", e, g)
	}
	if owner, _ := index.Owner(demo, "disabled"); owner != ProfileLabel {
		t.Errorf("owner of the enabled plugin = %q, want the profile layer", owner)
	}
}

// assertSafeOps checks that ops are exactly `disabled: true` overrides of ids.
func assertSafeOps(t *testing.T, ops []loader.Op, ids ...string) {
	t.Helper()
	var got []string
	for _, op := range ops {
		got = append(got, op.ID)
		if op.Kind != loader.OpOverride || len(op.Set) != 1 || string(op.Set["disabled"]) != "true" || op.Name != "" {
			t.Errorf("launcher op %s = %+v, want only disabled: true", op.ID, op)
		}
	}
	if !slices.Equal(got, ids) {
		t.Errorf("launcher ops target %v, want %v", got, ids)
	}
}

// assertOnlyDisabled composes both stacks and checks that the safe tree is the
// ordinary one with each id disabled, owned by the launcher layer, and with every
// other ownership unchanged.
func assertOnlyDisabled(t *testing.T, ordinary, safe []loader.Layer, ids ...string) {
	t.Helper()
	want, wantIndex, issues := loader.Compose(ordinary)
	if len(issues) > 0 {
		t.Fatalf("ordinary compose: %v", issues)
	}
	got, gotIndex, issues := loader.Compose(safe)
	if len(issues) > 0 {
		t.Fatalf("safe compose: %v", issues)
	}
	for _, id := range ids {
		e := find(want, id)
		if e == nil {
			t.Fatalf("%s is not in the ordinary tree", id)
		}
		if e.Disabled {
			t.Fatalf("%s is already disabled in the ordinary tree: the check would be empty", id)
		}
		e.Disabled = true
		if owner, _ := gotIndex.Owner(id, "disabled"); owner != LauncherLabel {
			t.Errorf("owner of %s.disabled = %q, want %q", id, owner, LauncherLabel)
		}
	}
	a, _ := json.Marshal(want)
	b, _ := json.Marshal(got)
	if string(a) != string(b) {
		t.Errorf("safe tree differs beyond %v:\n got %s\nwant %s", ids, b, a)
	}
	changed := map[string]bool{}
	for _, id := range ids {
		changed[id+"\x00disabled"] = true
	}
	var before, after []loader.Ownership
	for _, o := range wantIndex.Owners() {
		if !changed[o.ID+"\x00"+o.Field] {
			before = append(before, o)
		}
	}
	for _, o := range gotIndex.Owners() {
		if !changed[o.ID+"\x00"+o.Field] {
			after = append(after, o)
		}
	}
	if !slices.Equal(before, after) {
		t.Errorf("safe mode changed other ownership:\n got %v\nwant %v", after, before)
	}
}
