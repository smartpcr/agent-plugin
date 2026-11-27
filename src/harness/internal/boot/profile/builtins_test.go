package profile

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
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
		skipped, issueProblems := pendingIssues(issues, tree)
		for _, p := range issueProblems {
			t.Errorf("%s: compose: %s", def.Name, p)
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
		for _, s := range skipped {
			trees.WriteString("# skipped, target not built: " + s + "\n")
		}
		trees.Write(b)
		trees.WriteString("\n")
	}
	golden.Assert(t, "composed", trees.String())

	// What each component provides when it loads alone, under the services boot
	// provides at the root, is what its catalogue item declares.
	for name, e := range entries {
		item, _ := cat.Lookup(name)
		got := providedBy(t, cat, e, entries)
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
	cat, err := catalog.Build(builtin.Sets()...)
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

// unbuilt are the Required ids no shipped layer holds yet: their component is not
// in the build catalogue, and the bundle that inserts it (core, web) is a later
// stage's. That stage inserts the entry and takes the id off this list in the same
// change: the golden test fails while a listed id resolves or is held, and while a
// missing id is not listed. DH-T-BUNDLE-5 holds in full once the list is empty.
var unbuilt = map[string]bool{
	"host.listen": true, "host.auth": true, "host.assets": true, "host.web": true, "host.access": true, "host.stream": true, "ui.shell": true,
}

// pendingTargets are the ids a shipped bundle merges into or switches off whose
// entry no lower bundle holds yet (base.yml and the spine are later stages'); the
// composer skips such an operation, reporting it as matching no entry.
var pendingTargets = map[string]bool{
	"human.secrets": true, "human.authorization": true, "session.title.llm": true,
	"preset.script": true, "preset.minimal": true, "preset.creator": true,
}

// presetComponent is the component of a preset declaration entry (bundle-preset.md
// §6.2, DH-D-BUNDLE-4).
const presetComponent = "agent.preset"

// effectiveChildren returns, per declaration id, the sorted names of the children
// each agent.preset entry of the tree resolves to: its own children over those of
// the declaration it extends, transitively (bundle-preset.md §6.2).
func effectiveChildren(t *testing.T, entries map[string]loader.EntryOptions) map[string][]string {
	t.Helper()
	type decl struct {
		ID       string
		Extends  string
		Children map[string]json.RawMessage
	}
	decls := map[string]decl{}
	for _, e := range entries {
		if e.Name != presetComponent {
			continue
		}
		var d decl
		if err := json.Unmarshal(e.Config, &d); err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		decls[d.ID] = d
	}
	var resolve func(id string, seen map[string]bool) map[string]bool
	resolve = func(id string, seen map[string]bool) map[string]bool {
		d, ok := decls[id]
		if !ok || seen[id] {
			t.Fatalf("declaration %q is missing or extends itself", id)
		}
		seen[id] = true
		out := map[string]bool{}
		if d.Extends != "" {
			for name := range resolve(d.Extends, seen) {
				out[name] = true
			}
		}
		for name, child := range d.Children {
			out[name] = string(child) != "null"
		}
		return out
	}
	got := map[string][]string{}
	for id := range decls {
		var names []string
		for name, on := range resolve(id, map[string]bool{}) {
			if on {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		got[id] = names
	}
	return got
}

// pendingIssues splits compose issues into the skipped operations on a pending
// target, returned for the golden, and problems: any other issue, and a pending
// target the tree now holds.
func pendingIssues(issues []loader.Issue, tree []loader.EntryOptions) (skipped, problems []string) {
	held := map[string]bool{}
	walkEntries(tree, nil, func(e loader.EntryOptions, _ []loader.EntryOptions) { held[e.ID] = true })
	for _, issue := range issues {
		pending := ""
		for id := range pendingTargets {
			if strings.HasSuffix(issue.Message, strconv.Quote(id)+" matches no entry") {
				pending = id
			}
		}
		if pending == "" {
			problems = append(problems, issue.Error())
			continue
		}
		skipped = append(skipped, issue.Error())
	}
	for id := range pendingTargets {
		if held[id] {
			problems = append(problems, "pending target "+id+" is held by the layers: take it off pendingTargets")
		}
	}
	return skipped, problems
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

// TC-08g, INV-10: the committed layers compose admin to its section of
// composed.golden, and that tree mounts no carrier, agent, model adapter or plugin
// process: what A24's offline commands run in acts on nothing.
func TestAdminCompositionIsOffline(t *testing.T) {
	var def Definition
	for _, d := range Definitions() {
		if d.Name == Admin {
			def = d
		}
	}
	layers, err := BundleLayers(def)
	if err != nil {
		t.Fatal(err)
	}
	tree, issues, err := Compose(def, layers)
	if err != nil || len(issues) > 0 {
		t.Fatalf("admin: %v %v", err, issues)
	}
	got, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if want := goldenSection(t, Admin); string(got) != want {
		t.Errorf("admin composes to\n%s\nwant its section of composed.golden\n%s", got, want)
	}
	carriers := map[string]bool{"wire.acp": true}
	for _, set := range [][]string{webCarrier, headlessCarrier, sdkCarrier} {
		for _, id := range set {
			carriers[id] = true
		}
	}
	acting := []string{"agent.", "llm.", "wire.", "cli.", "plugin.", "boot.plugins", "subagent."}
	walkEntries(tree, nil, func(e loader.EntryOptions, _ []loader.EntryOptions) {
		if carriers[e.Name] || carriers[e.ID] || slices.ContainsFunc(acting, func(p string) bool { return strings.HasPrefix(e.Name, p) }) {
			t.Errorf("admin mounts %s (%s): no carrier, agent, model adapter or plugin process (INV-10)", e.ID, e.Name)
		}
	})
	if len(tree) == 0 {
		t.Error("admin composes to nothing: it holds logging and the sharing gate")
	}
}

// goldenSection returns the composed tree of the profile name as composed.golden
// holds it: the lines under "# <name>" up to the next profile's header, comments
// dropped.
func goldenSection(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "composed.golden"))
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]bool{}
	for _, d := range allDefinitions() {
		headers["# "+d.Name] = true
	}
	var out []string
	in := false
	for _, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if headers[line] {
			if in {
				break
			}
			in = line == "# "+name
			continue
		}
		if in && !strings.HasPrefix(line, "# ") {
			out = append(out, line)
		}
	}
	if !in {
		t.Fatalf("composed.golden has no section %s", name)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
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

// The shipped headless and sdk bundles do what bundle-preset.md §6.2 gives them:
// applied over a lower layer holding the spine and every target, each composes
// with no issue and satisfies its profile's Required list, and the tree shows each
// operation; without the bundle the same lower layer fails, naming the carrier.
func TestCarrierBundleMembership(t *testing.T) {
	stub := func(id, config string) loader.EntryOptions {
		e := loader.EntryOptions{ID: id, Name: id}
		if config != "" {
			e.Config = json.RawMessage(config)
		}
		return e
	}
	lower := []loader.EntryOptions{
		stub("human.secrets", `{"backend":"auto","projectEnv":true,"homeEnv":true}`),
		stub("human.authorization", ""),
		stub("agent.presets", `{"default":"standard"}`),
		stub("session.title.llm", ""),
	}
	// The four shipped declarations, as presets/*.yml will hold them: standard and
	// creator carry the question tool, script inherits it, minimal never had it.
	for _, d := range []struct{ id, config string }{
		{"preset.standard", `{"id":"standard","children":{"tool.ask-user":{"component":"tool.ask-user"},"tool.todo":{"component":"tool.todo"},"fs.tools":{"component":"fs.tools","config":{"applyPatch":true}}}}`},
		{"preset.script", `{"id":"script","extends":"standard","children":{"program.mode":{"component":"program.mode"}}}`},
		{"preset.minimal", `{"id":"minimal","children":{"compact.basic":{"component":"compact.basic"}}}`},
		{"preset.creator", `{"id":"creator","extends":"standard","children":{"tool.ask-user":{"component":"tool.ask-user","config":{"timeout":"5m"}},"boot.plugins.tool":{"component":"boot.plugins.tool"}}}`},
	} {
		e := stub(d.id, d.config)
		e.Name = presetComponent
		lower = append(lower, e)
	}
	for _, id := range Spine {
		if id != "agent.presets" {
			lower = append(lower, stub(id, ""))
		}
	}
	byID := func(tree []loader.EntryOptions) map[string]loader.EntryOptions {
		out := map[string]loader.EntryOptions{}
		walkEntries(tree, nil, func(e loader.EntryOptions, _ []loader.EntryOptions) { out[e.ID] = e })
		return out
	}
	config := func(e loader.EntryOptions) map[string]any {
		var m map[string]any
		_ = json.Unmarshal(e.Config, &m)
		return m
	}
	for _, tc := range []struct {
		profile string
		check   func(t *testing.T, before, after map[string]loader.EntryOptions)
	}{
		{Headless, func(t *testing.T, before, after map[string]loader.EntryOptions) {
			if e := after["cli.headless"]; e.Name != "cli.headless" || e.Disabled {
				t.Errorf("runner = %+v, want cli.headless mounted", e)
			}
			got := config(after["human.secrets"])
			if got["backend"] != "env" || got["projectEnv"] != true || got["homeEnv"] != true {
				t.Errorf("human.secrets config = %v, want backend env merged over the rest", got)
			}
			if !after["human.authorization"].Disabled {
				t.Error("human.authorization is not disabled")
			}
			if !reflect.DeepEqual(after["agent.presets"], before["agent.presets"]) {
				t.Errorf("headless changes the registry: %+v", after["agent.presets"])
			}
			want := map[string][]string{
				"standard": {"fs.tools", "tool.todo"},
				"script":   {"fs.tools", "program.mode", "tool.todo"},
				"minimal":  {"compact.basic"},
				"creator":  {"boot.plugins.tool", "fs.tools", "tool.todo"},
			}
			if b := effectiveChildren(t, before); !slices.Contains(b["script"], "tool.ask-user") || !slices.Contains(b["creator"], "tool.ask-user") {
				t.Fatalf("lower presets %v: the fixture lost the question tool", b)
			}
			children := effectiveChildren(t, after)
			if !reflect.DeepEqual(children, want) {
				t.Errorf("effective preset children = %v, want %v (tool.ask-user gone, the rest kept)", children, want)
			}
			if c := config(after["preset.standard"])["children"].(map[string]any)["fs.tools"]; !reflect.DeepEqual(c, map[string]any{"component": "fs.tools", "config": map[string]any{"applyPatch": true}}) {
				t.Errorf("standard fs.tools child = %v, want it unchanged", c)
			}
			if after["session.title.llm"].Disabled {
				t.Error("headless switches session.title.llm off")
			}
		}},
		{SDK, func(t *testing.T, before, after map[string]loader.EntryOptions) {
			for _, id := range []string{"wire.stdio", "wire.sdk"} {
				if e := after[id]; e.Name != id || e.Disabled {
					t.Errorf("%s = %+v, want it mounted", id, e)
				}
			}
			if !after["session.title.llm"].Disabled {
				t.Error("session.title.llm is not disabled")
			}
			for _, id := range []string{"human.secrets", "human.authorization", "agent.presets", "preset.standard", "preset.creator"} {
				if !reflect.DeepEqual(after[id], before[id]) {
					t.Errorf("sdk changes %s: %+v", id, after[id])
				}
			}
		}},
	} {
		t.Run(tc.profile, func(t *testing.T) {
			def, err := Lookup(tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			bundle, err := Bundle(tc.profile)
			if err != nil {
				t.Fatal(err)
			}
			base := loader.Layer{Label: "lower", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: lower}}}
			if _, _, err := Compose(def, []loader.Layer{base}); herr.Kind(err) != ReasonMissingRequired ||
				!strings.Contains(err.Error(), def.Required[len(def.Required)-1]) {
				t.Errorf("lower layer alone: err = %v, want it to lack the carrier", err)
			}
			before, _, _ := Compose(def, []loader.Layer{base})
			tree, issues, err := Compose(def, []loader.Layer{base, bundle})
			if len(issues) > 0 || err != nil {
				t.Fatalf("compose: issues %v, err %v", issues, err)
			}
			tc.check(t, byID(before), byID(tree))
		})
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

// providedBy loads entry e through the real loader under a root that holds what
// boot provides before any entry loads (the sharing gate, the home and the
// presets' host), after the entries of entries that provide what e injects,
// transitively, have loaded; it returns the services provided once e joins them,
// which are e's own: none of those entries waits on e.
func providedBy(t *testing.T, cat *catalog.Catalog, e loader.EntryOptions, entries map[string]loader.EntryOptions) []string {
	t.Helper()
	// The home is made before the tree, so its cleanup runs after the tree's and
	// the store is closed before the directory goes.
	layout := home.New(t.TempDir())
	if err := home.Ensure(layout); err != nil {
		t.Fatal(err)
	}
	root := testkit.Tree(t)
	if _, err := sharing.Key.Provide(root, sharing.New(sharing.Config{}, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := home.Key.Provide(root, layout); err != nil {
		t.Fatal(err)
	}
	if _, err := agentpreset.HostKey.Provide(root, agentpreset.Host{Catalogue: cat, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	// The stop control and a client stream pair, so wire.stdio attaches to pipes
	// and never claims the test process's standard streams.
	if _, err := launch.AppKey.Provide(root, launch.App(launch.NewApp())); err != nil {
		t.Fatal(err)
	}
	in, client := io.Pipe()
	t.Cleanup(func() { client.Close() })
	if _, err := launch.StdioKey.Provide(root, launch.Stdio{In: in, Out: io.Discard}); err != nil {
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
	deps := providers(cat, e, entries)
	if err := svc.Apply(deps); err != nil {
		t.Fatalf("the providers of %s do not load: %v", e.Name, err)
	}
	var got []string
	ch := make(chan string, 64)
	remove := root.Internal().OnService(func(name string, value any) {
		// A preset revision's own loader is provided in the revision's scope, not
		// by the entry.
		if value != nil && name != loader.Key.Name() {
			ch <- name
		}
	})
	defer remove()
	if err := svc.Apply(append(slices.Clone(deps), e)); err != nil {
		t.Fatalf("%s does not load over its providers: %v", e.Name, err)
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

// providers are the entries of entries whose items provide what e injects, and
// what those inject, transitively, without e.
func providers(cat *catalog.Catalog, e loader.EntryOptions, entries map[string]loader.EntryOptions) []loader.EntryOptions {
	by := map[string]string{} // service -> component name
	for name := range entries {
		if item, err := cat.Lookup(name); err == nil {
			for _, s := range item.Provides {
				by[s] = name
			}
		}
	}
	seen := map[string]bool{e.Name: true}
	var out []loader.EntryOptions
	var visit func(loader.EntryOptions)
	visit = func(x loader.EntryOptions) {
		inject := slices.Clone(x.Inject)
		if item, err := cat.Lookup(x.Name); err == nil {
			for _, d := range item.Component.Inject() {
				inject = append(inject, d.Name)
			}
		}
		for _, s := range inject {
			name, ok := by[s]
			if !ok || seen[name] {
				continue
			}
			seen[name] = true
			dep := entries[name]
			out = append(out, dep)
			visit(dep)
		}
	}
	visit(e)
	return out
}
