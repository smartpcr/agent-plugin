package profile

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// scopeComponent is the component no ancestor may inject a service for
// (boot.md "Where entries mount", area A01).
const scopeComponent = "session.scope"

// TestBuiltinProfilesCompose is DH-T-BOOT-2: for every built-in profile, through
// the real codec and loader, every bundle entry has an explicit id, resolves,
// decodes strictly and has valid expressions; every injected service has a
// declared provider; no component provides a service its item does not declare;
// an optional bundle only inserts under one disabled group; the scope component
// has no injecting ancestor; and the composed trees match a golden file.
//
//dsh:test DH-T-BOOT-2
func TestBuiltinProfilesCompose(t *testing.T) {
	cat, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	ev, err := expr.New(expr.Options{Views: cat.Views()})
	if err != nil {
		t.Fatal(err)
	}
	var trees bytes.Buffer
	entries := map[string]loader.EntryOptions{} // one entry per component, for the provide check
	for _, def := range allDefinitions() {
		var layers []loader.Layer
		for _, name := range def.Bundles {
			layer, err := Bundle(name)
			if err != nil {
				t.Fatalf("%s: %v", def.Name, err)
			}
			for _, p := range bundleProblems(layer) {
				t.Errorf("%s: bundle %s: %s", def.Name, name, p)
			}
			layers = append(layers, layer)
		}
		tree, issues, err := Compose(def, layers)
		if len(issues) > 0 {
			t.Errorf("%s: compose: %v", def.Name, issues)
		}
		if err != nil && herr.Kind(err) != ReasonMissingRequired {
			t.Errorf("%s: compose: %v", def.Name, err)
		}
		for _, p := range requiredProblems(cat, def, tree) {
			t.Errorf("%s: %s", def.Name, p)
		}
		for _, p := range treeProblems(cat, ev, tree) {
			t.Errorf("%s: %s", def.Name, p)
		}
		walkEntries(tree, nil, func(e loader.EntryOptions, _ []loader.EntryOptions) {
			if !e.Group {
				entries[e.Name] = e
			}
		})
		b, err := json.MarshalIndent(tree, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		trees.WriteString("# " + def.Name + "\n")
		if len(def.Required) > 0 {
			trees.WriteString("# required: " + strings.Join(def.Required, ", ") + "\n")
		}
		trees.Write(b)
		trees.WriteString("\n")
	}
	golden.Assert(t, "composed", trees.String())

	// What each component provides when it loads alone, under the services boot
	// provides at the root, is what its catalogue item declares.
	for name, e := range entries {
		item, _ := cat.Lookup(name)
		got := providedBy(t, cat, e)
		if len(item.Provides) > 0 && len(got) == 0 {
			t.Errorf("%s declares %v but nothing it provided was seen: the observation is broken", name, item.Provides)
		}
		for _, svc := range got {
			if !slices.Contains(item.Provides, svc) {
				t.Errorf("%s provides %s, which its item does not declare (declares %v)", name, svc, item.Provides)
			}
		}
	}
}

// The checks reject the faults they exist for, so their silence over the
// shipped bundles is meaningful.
//
//dsh:test DH-T-BOOT-2
func TestBuiltinProfileChecksCatchFaults(t *testing.T) {
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{{Name: scopeComponent, Component: scope.Component}})...)
	if err != nil {
		t.Fatal(err)
	}
	ev, err := expr.New(expr.Options{Views: cat.Views()})
	if err != nil {
		t.Fatal(err)
	}
	optional := func(ops ...loader.Op) loader.Layer { return loader.Layer{Label: "bundle:x", Ops: ops} }
	group := func(disabled bool, children ...loader.EntryOptions) loader.EntryOptions {
		return loader.EntryOptions{ID: "x.feature", Group: true, Disabled: disabled, Children: children}
	}
	for name, tc := range map[string]struct {
		layer loader.Layer
		want  string
	}{
		"enabled optional group":  {optional(loader.Op{Kind: loader.OpInsert, Entries: []loader.EntryOptions{group(false)}}), "disabled by default"},
		"two top entries":         {optional(loader.Op{Kind: loader.OpInsert, Entries: []loader.EntryOptions{group(true), {ID: "y", Name: "boot.logging"}}}), "one group"},
		"optional bundle merges":  {optional(loader.Op{Kind: loader.OpInsert, Entries: []loader.EntryOptions{group(true)}}, loader.Op{Kind: loader.OpMerge, ID: "logging"}), "only inserts"},
		"entry without an id":     {optional(loader.Op{Kind: loader.OpInsert, Entries: []loader.EntryOptions{{Name: "boot.logging"}}}), "explicit id"},
		"nested entry without id": {optional(loader.Op{Kind: loader.OpInsert, Entries: []loader.EntryOptions{group(true, loader.EntryOptions{Name: "boot.logging"})}}), "explicit id"},
	} {
		if p := strings.Join(bundleProblems(tc.layer), "; "); !strings.Contains(p, tc.want) {
			t.Errorf("%s: problems %q, want one naming %q", name, p, tc.want)
		}
	}
	for name, tc := range map[string]struct {
		tree []loader.EntryOptions
		want string
	}{
		"unknown component": {[]loader.EntryOptions{{ID: "a", Name: "boot.nope"}}, "does not resolve"},
		"unknown field":     {[]loader.EntryOptions{{ID: "a", Name: "boot.logging", Config: json.RawMessage(`{"levle":"info"}`)}}, "does not decode strictly"},
		"bad expression":    {[]loader.EntryOptions{{ID: "a", Name: "boot.logging", DisabledExpr: "readFile('x')"}}, "expression"},
		"bad config expression": {[]loader.EntryOptions{{ID: "a", Name: "boot.logging",
			Config: json.RawMessage(`{"level":{"$expr":"env[\"X\"] +"}}`)}}, "expression"},
		"no provider": {[]loader.EntryOptions{{ID: "a", Name: "boot.logging", Inject: []string{"nobody.provides"}}}, "no declared provider"},
		"scope under an injecting group": {[]loader.EntryOptions{{ID: "g", Group: true, Inject: []string{egressService(cat)},
			Children: []loader.EntryOptions{{ID: "s", Name: scopeComponent}}}}, "injecting ancestor"},
	} {
		if p := strings.Join(treeProblems(cat, ev, tc.tree), "; "); !strings.Contains(p, tc.want) {
			t.Errorf("%s: problems %q, want one naming %q", name, p, tc.want)
		}
	}
}

func egressService(cat *catalog.Catalog) string {
	for _, it := range cat.Items() {
		if len(it.Provides) > 0 {
			return it.Provides[0]
		}
	}
	return ""
}

// allDefinitions are the five definitions of DH-D-BUNDLE-1 followed by the other
// launchable profiles, each once.
func allDefinitions() []Definition {
	out := Definitions()
	for _, d := range Builtins() {
		if !slices.ContainsFunc(out, func(o Definition) bool { return o.Name == d.Name }) {
			out = append(out, d)
		}
	}
	return out
}

// unbuilt are the Required ids whose component the build catalogue does not have
// yet. The stage that registers such a component inserts its entry in the profile's
// layers and takes the id off this list in the same change: the golden test fails
// while a listed id resolves or is held, and while a missing id is not listed.
// DH-T-BUNDLE-5 holds in full once the list is empty.
var unbuilt = map[string]bool{
	"session.store": true, "agent.registry": true, "agent.driver": true, "tools.registry": true, "agent.presets": true,
	"host.listen": true, "host.auth": true, "host.assets": true, "host.web": true, "host.access": true, "host.stream": true, "ui.shell": true,
	"cli.headless": true,
	"wire.stdio":   true, "wire.sdk": true,
}

// requiredProblems checks def's Required list against its composed tree and the
// catalogue: an id the tree lacks must be listed in unbuilt, and a listed id must
// neither resolve in the catalogue nor be held by the tree.
func requiredProblems(cat *catalog.Catalog, def Definition, tree []loader.EntryOptions) []string {
	held := map[string]bool{}
	walkEntries(tree, nil, func(e loader.EntryOptions, _ []loader.EntryOptions) { held[e.ID] = true })
	var out []string
	for _, id := range def.Required {
		_, lookupErr := cat.Lookup(id)
		switch {
		case unbuilt[id] && lookupErr == nil:
			out = append(out, "required "+id+" is in the catalogue now: insert its entry and take it off unbuilt")
		case unbuilt[id] && held[id]:
			out = append(out, "required "+id+" is held by the layers: take it off unbuilt")
		case !unbuilt[id] && !held[id]:
			out = append(out, "required "+id+" is not held by the composed layers")
		}
	}
	return out
}

// The five definitions carry the content of DH-D-BUNDLE-1: profile.Spine in every
// launchable one and in the kernel, each launchable one's carrier set after it, no
// required entry in admin; every unbuilt id is still some profile's.
func TestProfileDefinitions(t *testing.T) {
	want := map[string][]string{
		Admin:    nil,
		Core:     Spine,
		Web:      append(slices.Clone(Spine), "host.listen", "host.auth", "host.assets", "host.web", "host.access", "host.stream", "ui.shell"),
		Headless: append(slices.Clone(Spine), "cli.headless"),
		SDK:      append(slices.Clone(Spine), "wire.stdio", "wire.sdk"),
	}
	var names []string
	for _, d := range Definitions() {
		names = append(names, d.Name)
		if !slices.Equal(d.Required, want[d.Name]) {
			t.Errorf("%s: Required = %v, want %v", d.Name, d.Required, want[d.Name])
		}
		if d.Limits != DefaultLimits() {
			t.Errorf("%s: limits = %+v", d.Name, d.Limits)
		}
		if (d.Reload != 0) != (d.Name == Web) {
			t.Errorf("%s: reload = %v; only web reloads", d.Name, d.Reload)
		}
	}
	if !slices.Equal(names, []string{Admin, Core, Web, Headless, SDK}) {
		t.Errorf("definitions = %v", names)
	}
	for _, name := range []string{Admin, Core} {
		if _, err := Lookup(name); herr.Kind(err) != ReasonUnknown {
			t.Errorf("%s is launchable by name: %v", name, err)
		}
	}
	required := map[string]bool{}
	for _, d := range allDefinitions() {
		for _, id := range d.Required {
			required[id] = true
		}
	}
	for id := range unbuilt {
		if !required[id] {
			t.Errorf("unbuilt %s is required by no profile", id)
		}
	}
	// A definition is a copy: changing it does not change the next one returned.
	Definitions()[1].Required[0] = "changed"
	if Definitions()[1].Required[0] != Spine[0] {
		t.Error("Definitions shares its Required list")
	}
}

// TC-08a: a headless bundle lacking one entry of the headless Required list fails
// composition naming it; one holding it disabled, alone or under a disabled group,
// fails naming it too; the full list composes.
func TestComposeRequiresEveryRequiredEntry(t *testing.T) {
	def, err := Lookup(Headless)
	if err != nil {
		t.Fatal(err)
	}
	carrier := def.Required[len(def.Required)-1]
	entries := func(ids []string, edit func(*loader.EntryOptions)) []loader.Layer {
		var es []loader.EntryOptions
		for _, id := range ids {
			e := loader.EntryOptions{ID: id, Name: id}
			if edit != nil {
				edit(&e)
			}
			es = append(es, e)
		}
		return []loader.Layer{{Label: BundleLabel + Headless, Ops: []loader.Op{{Kind: loader.OpInsert, Entries: es}}}}
	}
	if _, _, err := Compose(def, entries(def.Required, nil)); err != nil {
		t.Fatalf("full list: %v", err)
	}
	for name, tc := range map[string]struct {
		layers []loader.Layer
		want   string
	}{
		"lacks the carrier": {entries(def.Required[:len(def.Required)-1], nil), "lacks " + carrier},
		"disables the carrier": {entries(def.Required, func(e *loader.EntryOptions) {
			e.Disabled = e.ID == carrier
		}), "disables " + carrier},
		"carrier under a disabled group": {[]loader.Layer{{Label: BundleLabel + Headless, Ops: []loader.Op{{Kind: loader.OpInsert,
			Entries: append(entries(def.Required[:len(def.Required)-1], nil)[0].Ops[0].Entries,
				loader.EntryOptions{ID: "g", Group: true, Disabled: true, Children: []loader.EntryOptions{{ID: carrier, Name: carrier}}})}}}},
			"lacks " + carrier},
	} {
		_, _, err := Compose(def, tc.layers)
		if herr.Kind(err) != ReasonMissingRequired || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %s naming %q", name, err, ReasonMissingRequired, tc.want)
		}
	}
	nested := Definition{Name: "nested", Required: []string{"g" + loader.Separator + carrier}}
	group := []loader.Layer{{Label: "x", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{
		{ID: "g", Group: true, Disabled: true, Children: []loader.EntryOptions{{ID: carrier, Name: carrier}}}}}}}}
	if _, _, err := Compose(nested, group); err == nil || !strings.Contains(err.Error(), "disables g"+loader.Separator+carrier) {
		t.Errorf("nested under a disabled group: %v", err)
	}
}

// bundleProblems checks a bundle layer as written: every inserted entry has an
// explicit id, and a bundle that inserts an optional group (id "x.<feature>")
// holds nothing but that one insert of one group disabled by default.
func bundleProblems(layer loader.Layer) []string {
	var out []string
	optional := false
	tops := 0
	for _, op := range layer.Ops {
		if op.Kind != loader.OpInsert {
			continue
		}
		for _, e := range op.Entries {
			tops++
			if e.Group && strings.HasPrefix(e.ID, "x.") {
				optional = true
				if !e.Disabled || e.DisabledExpr != "" {
					out = append(out, "optional group "+e.ID+" is not disabled by default")
				}
			}
		}
		walkEntries(op.Entries, nil, func(e loader.EntryOptions, _ []loader.EntryOptions) {
			if e.ID == "" {
				out = append(out, "an entry of "+e.Name+" has no explicit id")
			}
		})
	}
	if optional {
		if len(layer.Ops) != 1 || layer.Ops[0].Kind != loader.OpInsert {
			out = append(out, "an optional bundle only inserts")
		}
		if tops != 1 {
			out = append(out, "an optional bundle inserts one group")
		}
	}
	return out
}

// treeProblems checks a composed tree against the catalogue: every component
// resolves and decodes its config strictly, every expression compiles in its
// scope, every injected service (the component's own and the entry's) has a
// provider declared by an item of the tree, and no ancestor of the scope
// component injects a service.
func treeProblems(cat *catalog.Catalog, ev *expr.Evaluator, tree []loader.EntryOptions) []string {
	var out []string
	declared := map[string]bool{}
	walkEntries(tree, nil, func(e loader.EntryOptions, _ []loader.EntryOptions) {
		if item, err := cat.Lookup(e.Name); err == nil {
			for _, s := range item.Provides {
				declared[s] = true
			}
		}
	})
	walkEntries(tree, nil, func(e loader.EntryOptions, ancestors []loader.EntryOptions) {
		if e.DisabledExpr != "" {
			if err := ev.Check(e.DisabledExpr, loader.DisabledScope); err != nil {
				out = append(out, e.ID+": disabled expression: "+err.Error())
			}
		}
		inject := slices.Clone(e.Inject)
		if e.Name == scopeComponent {
			for _, a := range ancestors {
				if len(a.Inject) > 0 {
					out = append(out, e.ID+": the scope component has an injecting ancestor "+a.ID)
				}
			}
		}
		if !e.Group {
			item, err := cat.Lookup(e.Name)
			if err != nil {
				out = append(out, e.ID+": "+e.Name+" does not resolve: "+err.Error())
				return
			}
			for _, d := range item.Component.Inject() {
				inject = append(inject, d.Name)
			}
			if p := decodeStrict(item, e.Config); p != "" {
				out = append(out, e.ID+": "+p)
			}
			var cfg any
			if len(e.Config) > 0 && json.Unmarshal(e.Config, &cfg) == nil {
				for _, src := range configExprs(cfg) {
					if err := ev.Check(src, loader.ConfigScope); err != nil {
						out = append(out, e.ID+": config expression: "+err.Error())
					}
				}
			}
		}
		for _, s := range inject {
			if !declared[s] {
				out = append(out, e.ID+": injects "+s+", which has no declared provider")
			}
		}
	})
	return out
}

// decodeStrict decodes config, with its expressions replaced by null, into a new
// value of the item's config type, refusing unknown fields.
func decodeStrict(item catalog.Item, config json.RawMessage) string {
	if item.Config == nil || len(config) == 0 {
		return ""
	}
	var v any
	if err := json.Unmarshal(config, &v); err != nil {
		return "config does not decode strictly: " + err.Error()
	}
	plain, _ := json.Marshal(dropExprs(v))
	dec := json.NewDecoder(bytes.NewReader(plain))
	dec.DisallowUnknownFields()
	if err := dec.Decode(reflect.New(item.Config).Interface()); err != nil {
		return "config does not decode strictly: " + err.Error()
	}
	return ""
}

func isExpr(v any) (string, bool) {
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		s, ok := m["$expr"].(string)
		return s, ok
	}
	if s, ok := v.(string); ok {
		trimmed := strings.TrimSpace(s)
		if strings.HasPrefix(trimmed, "${{") && strings.HasSuffix(trimmed, "}}") {
			return strings.TrimSpace(trimmed[3 : len(trimmed)-2]), true
		}
	}
	return "", false
}

func configExprs(v any) []string {
	if src, ok := isExpr(v); ok {
		return []string{src}
	}
	var out []string
	switch x := v.(type) {
	case map[string]any:
		for _, e := range x {
			out = append(out, configExprs(e)...)
		}
	case []any:
		for _, e := range x {
			out = append(out, configExprs(e)...)
		}
	}
	return out
}

func dropExprs(v any) any {
	if _, ok := isExpr(v); ok {
		return nil
	}
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = dropExprs(e)
		}
	case []any:
		for i, e := range x {
			x[i] = dropExprs(e)
		}
	}
	return v
}

func walkEntries(entries []loader.EntryOptions, ancestors []loader.EntryOptions, fn func(loader.EntryOptions, []loader.EntryOptions)) {
	for _, e := range entries {
		fn(e, ancestors)
		walkEntries(e.Children, append(slices.Clone(ancestors), e), fn)
	}
}

// providedBy loads entry e alone through the real loader under a root that holds
// what boot provides before any entry loads (the sharing gate), and returns the
// services the entry's fiber provided.
func providedBy(t *testing.T, cat *catalog.Catalog, e loader.EntryOptions) []string {
	t.Helper()
	root := testkit.Tree(t)
	if _, err := sharing.Key.Provide(root, sharing.New(sharing.Config{}, nil)); err != nil {
		t.Fatal(err)
	}
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: cat})
	if err != nil {
		t.Fatal(err)
	}
	if err := fiber.Wait(); err != nil {
		t.Fatal(err)
	}
	svc, err := loader.Key.Get(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	ch := make(chan string, 16)
	remove := root.Internal().OnService(func(name string, value any) {
		if value != nil {
			ch <- name
		}
	})
	defer remove()
	if err := svc.Apply([]loader.EntryOptions{e}); err != nil {
		t.Fatalf("%s does not load alone: %v", e.Name, err)
	}
	for {
		select {
		case name := <-ch:
			got = append(got, name)
		default:
			return got
		}
	}
}
