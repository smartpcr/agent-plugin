package profile

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/sharing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func catalogueNames(cat *catalog.Catalog) []string {
	var names []string
	for _, it := range cat.Items() {
		names = append(names, it.Name)
	}
	return names
}

// The shipped build is covered: every component of the build catalogue is mounted
// by a shipped layer or is on the reviewed optional list, never both, and the list
// names only catalogue components.
//
//dsh:test DH-T-BUNDLE-1
func TestShippedBuildIsCovered(t *testing.T) {
	cat, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	if err := Coverage(catalogueNames(cat)); err != nil {
		t.Fatal(err)
	}
	optional, err := Optional()
	if err != nil {
		t.Fatal(err)
	}
	if len(optional) == 0 {
		t.Fatal("the optional list is empty; review it with the catalogue")
	}
}

// TC-08a: an in-memory catalogue with a component in no profile and not on the
// optional list fails coverage naming it; listing it passes. A listed component
// that is also mounted, and a row naming no catalogue component, fail as well.
//
//dsh:test DH-T-BUNDLE-1
func TestCoverageFailsOnAnUnbundledComponent(t *testing.T) {
	const x = "boot.x"
	planted := catalog.Item{Name: x, Component: cordis.Define(x, func(*cordis.Context, struct{}) error { return nil })}
	cat, err := catalog.BuildFor("linux", append(builtin.Sets(), []catalog.Item{planted})...)
	if err != nil {
		t.Fatal(err)
	}
	names := catalogueNames(cat)
	mounted, err := Mounted(allDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	optional, err := Optional()
	if err != nil {
		t.Fatal(err)
	}

	err = CheckCoverage(names, mounted, optional)
	if herr.Kind(err) != ReasonUncovered || !strings.Contains(err.Error(), x+" is mounted by no shipped layer and is not on the optional list") {
		t.Fatalf("unlisted %s: err = %v, want %s naming it", x, err, ReasonUncovered)
	}
	if strings.Count(err.Error(), "is mounted by no shipped layer") != 1 {
		t.Fatalf("err = %v, want only %s named", err, x)
	}

	listed := append(slices.Clone(optional), OptionalItem{Name: x, Owner: "A21", Reason: "planted by the test"})
	if err := CheckCoverage(names, mounted, listed); err != nil {
		t.Fatalf("%s on the optional list: %v", x, err)
	}

	both := append(slices.Clone(optional), OptionalItem{Name: "boot.logging", Owner: "A21", Reason: "planted"})
	if err := CheckCoverage(catalogueNames(mustCatalog(t)), mounted, both); herr.Kind(err) != ReasonUncovered ||
		!strings.Contains(err.Error(), "boot.logging is on the optional list and mounted by acp, admin, core,") {
		t.Fatalf("listed and mounted: err = %v", err)
	}

	stray := append(slices.Clone(optional), OptionalItem{Name: "boot.gone", Owner: "A21", Reason: "planted"})
	if err := CheckCoverage(catalogueNames(mustCatalog(t)), mounted, stray); herr.Kind(err) != ReasonUncovered ||
		!strings.Contains(err.Error(), "the optional list names boot.gone, which is not in the catalogue") {
		t.Fatalf("stray row: err = %v", err)
	}
}

func mustCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	cat, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

// A preset child mounts its component: by its component field, or by its key
// without an "@<instance>" suffix; a null child mounts nothing. Entries under a
// disabled group count as mounted.
//
//dsh:test DH-T-BUNDLE-1
func TestMountsCountPresetChildrenAndDisabledEntries(t *testing.T) {
	decl := json.RawMessage(`{"id":"standard","children":{
		"persona":{"component":"agent.persona"},
		"fs.tools":{},
		"tool.subagent@spawn":{},
		"tool.ask-user":null}}`)
	tree := []loader.EntryOptions{
		{ID: "preset.standard", Name: presetName, Config: decl},
		{ID: "x.feature", Group: true, Disabled: true, Children: []loader.EntryOptions{{ID: "w", Name: "util.watch"}}},
	}
	out := map[string][]string{}
	if err := mounts(out, "p", tree); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		presetName: "p", "agent.persona": "p preset standard", "fs.tools": "p preset standard",
		"tool.subagent": "p preset standard", "util.watch": "p",
	}
	for name, where := range want {
		if got := out[name]; len(got) != 1 || got[0] != where {
			t.Errorf("%s mounted at %v, want [%s]", name, got, where)
		}
	}
	if _, ok := out["tool.ask-user"]; ok || len(out) != len(want) {
		t.Errorf("mounts = %v, want exactly %v", out, want)
	}
}

// The optional list is not a bundle, and a malformed row is refused.
func TestOptionalListIsNotABundle(t *testing.T) {
	for _, name := range BundleNames() {
		if name == OptionalName {
			t.Fatalf("BundleNames lists %s", OptionalName)
		}
	}
	if _, err := Bundle(OptionalName); herr.Kind(err) != ReasonBadBundle {
		t.Fatalf("Bundle(%s) err = %v, want %s", OptionalName, err, ReasonBadBundle)
	}
	for name, tc := range map[string]struct{ data, want string }{
		"no owner":    {"- name: a.b\n  reason: r\n", "has no owner"},
		"no reason":   {"- name: a.b\n  owner: A21\n", "has no reason"},
		"no name":     {"- owner: A21\n  reason: r\n", "has no name"},
		"twice":       {"- {name: a.b, owner: A21, reason: r}\n- {name: a.b, owner: A21, reason: r}\n", "listed twice"},
		"unknown key": {"- {name: a.b, owner: A21, reason: r, until: x}\n", "unknown field"},
	} {
		_, err := ParseOptional("optional.yml", []byte(tc.data))
		if herr.Kind(err) != ReasonBadOptional || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %s naming %q", name, err, ReasonBadOptional, tc.want)
		}
	}
	if items, err := ParseOptional("optional.yml", []byte("  \n")); err != nil || items != nil {
		t.Errorf("blank list = %v, %v", items, err)
	}
}

// TC-08a, NG-7: each default profile composed from its shipped bundles, with no
// route and no home layer, has an empty egress set. A layer that turns a kind on
// with an endpoint puts it in the set, so the empty result is meaningful. The sets
// are testdata/egress.golden, one line per profile, which "harness-gate release"
// reads as the default egress set (implementation-plan Stage 9.2, M2-DOD-7).
func TestDefaultProfilesSendNothing(t *testing.T) {
	var golden strings.Builder
	for _, def := range allDefinitions() {
		layers, err := BundleLayers(def)
		if err != nil {
			t.Fatal(err)
		}
		tree, _, _ := loader.Compose(layers)
		got, err := Egress(tree)
		if err != nil {
			t.Fatalf("%s: %v", def.Name, err)
		}
		if len(got) != 0 {
			t.Errorf("%s: egress set %v, want empty (NG-7)", def.Name, got)
		}
		sends := make([]string, 0, len(got))
		for _, st := range got {
			sends = append(sends, strings.TrimSpace(string(st.Kind)+" "+st.Endpoint))
		}
		fmt.Fprintf(&golden, "%s: [%s]\n", def.Name, strings.Join(sends, ", "))
	}
	want, err := os.ReadFile(filepath.Join("testdata", "egress.golden"))
	if err != nil {
		t.Fatal(err)
	}
	var wantSets []string
	for _, l := range strings.Split(strings.ReplaceAll(string(want), "\r\n", "\n"), "\n") {
		if l != "" && !strings.HasPrefix(l, "#") {
			wantSets = append(wantSets, l)
		}
	}
	if got := strings.TrimSuffix(golden.String(), "\n"); got != strings.Join(wantSets, "\n") {
		t.Errorf("default egress sets:\n%s\nwant testdata/egress.golden:\n%s", got, strings.Join(wantSets, "\n"))
	}

	def, err := Lookup(Headless)
	if err != nil {
		t.Fatal(err)
	}
	layers, err := BundleLayers(def)
	if err != nil {
		t.Fatal(err)
	}
	on, err := Merge("sharing", map[string]any{"kinds": map[string]any{
		string(sharing.ModelRequest): map[string]any{"enabled": true, "endpoint": "https://api.example.test"},
		string(sharing.Telemetry):    map[string]any{"enabled": true},
	}})
	if err != nil {
		t.Fatal(err)
	}
	tree, _, _ := loader.Compose(append(layers, loader.Layer{Label: UserLabel, Ops: []loader.Op{on}}))
	got, err := Egress(tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != sharing.ModelRequest || got[0].Endpoint != "https://api.example.test" {
		t.Fatalf("configured route: egress set %+v, want only %s to its endpoint", got, sharing.ModelRequest)
	}

	off := []loader.EntryOptions{{ID: "g", Group: true, Disabled: true, Children: tree}}
	if got, err := Egress(off); err != nil || len(got) != 0 {
		t.Fatalf("under a disabled group: %v, %v", got, err)
	}
	bad := []loader.EntryOptions{{ID: "sharing", Name: sharing.Name, Config: json.RawMessage(`{"of":true}`)}}
	if _, err := Egress(bad); herr.Kind(err) != ReasonBadSharing {
		t.Fatalf("bad config: err = %v, want %s", err, ReasonBadSharing)
	}
}
