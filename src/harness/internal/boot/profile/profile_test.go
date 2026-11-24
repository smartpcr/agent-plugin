package profile

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// files is an in-memory ReadFile.
func files(m map[string]string) ReadFile {
	return func(p string) ([]byte, error) {
		if data, ok := m[p]; ok {
			return []byte(data), nil
		}
		return nil, fs.ErrNotExist
	}
}

func mustParse(t *testing.T, label, file, data string) loader.Layer {
	t.Helper()
	layer, issues, err := ParseLayer(label, file, []byte(data))
	if err != nil || len(issues) > 0 {
		t.Fatalf("parse %s: %v %v", file, err, issues)
	}
	return layer
}

// TC-06a: bundle {model: a} < profile {model: b} < user {}: the profile wins and the
// ownership index names the profile layer.
//
//dsh:test DH-T-BOOT-7
func TestLayerStackPrecedence(t *testing.T) {
	bundle := mustParse(t, BundleLabel+"test", "bundle.yml", "- insert:\n    - id: agent\n      name: test.agent\n      config:\n        model: a\n")
	prof := mustParse(t, ProfileLabel, "profile.yml", "- id: agent\n  merge:\n    model: b\n")
	user := mustParse(t, UserLabel, "user.yml", "")

	tree, index, issues := loader.Compose([]loader.Layer{bundle, prof, user})
	if len(issues) > 0 {
		t.Fatalf("issues: %v", issues)
	}
	if len(tree) != 1 {
		t.Fatalf("tree = %d entries, want 1", len(tree))
	}
	var cfg struct{ Model string }
	if err := json.Unmarshal(tree[0].Config, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "b" {
		t.Errorf("model = %q, want b", cfg.Model)
	}
	if owner, _ := index.Owner("agent", "config.model"); owner != ProfileLabel {
		t.Errorf("owner of config.model = %q, want %q", owner, ProfileLabel)
	}
	if owner, _ := index.Owner("agent", ""); owner != BundleLabel+"test" {
		t.Errorf("owner of entry = %q, want the bundle", owner)
	}
}

//dsh:test DH-T-BOOT-7
func TestStackOrderAndOneWritableLayer(t *testing.T) {
	root := filepath.Join("h")
	def, err := Lookup(SDKMinimal)
	if err != nil {
		t.Fatal(err)
	}
	overlay := filepath.Join("o", "extra.yml")
	read := files(map[string]string{
		LayerPath(root, SDKMinimal): "- id: logging\n  merge:\n    level: debug\n",
		overlay:                     "- id: logging\n  merge:\n    level: warn\n",
	})
	launcher := []loader.Op{{Kind: loader.OpOverride, ID: "logging", Set: map[string]json.RawMessage{"disabled": json.RawMessage("true")}}}
	layers, issues, err := Stack(def, Sources{Home: root, Overlays: []string{overlay}, Launcher: launcher, Read: read})
	if err != nil || len(issues) > 0 {
		t.Fatalf("stack: %v %v", err, issues)
	}
	var labels []string
	writable := 0
	for _, l := range layers {
		labels = append(labels, l.Label)
		if l.Writable {
			writable++
			if l.Label != ProfileLabel || l.Path != LayerPath(root, SDKMinimal) {
				t.Errorf("writable layer = %s %s", l.Label, l.Path)
			}
		}
	}
	want := []string{"bundle:core", "bundle:minimal", "bundle:sdk", "profile", "user", "overlay:1", "launcher"}
	if !slices.Equal(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
	if writable != 1 {
		t.Errorf("%d writable layers, want 1", writable)
	}

	tree, index, issues := loader.Compose(layers)
	if _, problems := pendingIssues(issues, tree); len(problems) > 0 {
		t.Fatalf("compose: %v", problems)
	}
	if owner, _ := index.Owner("logging", "config.level"); owner != "overlay:1" {
		t.Errorf("owner of config.level = %q, want overlay:1", owner)
	}
	if owner, _ := index.Owner("logging", "disabled"); owner != LauncherLabel || !tree[0].Disabled {
		t.Errorf("launcher override not applied: owner %q disabled %v", owner, tree[0].Disabled)
	}
}

//dsh:test DH-T-BOOT-7
func TestStackErrors(t *testing.T) {
	def, _ := Lookup(Web)
	_, _, err := Stack(def, Sources{Home: "h", Overlays: []string{"missing.yml"}, Read: files(nil)})
	if herr.Kind(err) != ReasonBadLayer || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("missing overlay: %v", err)
	}
	bad := files(map[string]string{UserLayerPath("h"): "{not: [a layer"})
	if _, _, err := Stack(def, Sources{Home: "h", Read: bad}); herr.Kind(err) != ReasonBadLayer {
		t.Errorf("bad user layer: %v", err)
	}
	if _, _, err := Stack(Definition{Name: "x", Bundles: []string{"nope"}}, Sources{Home: "h", Read: files(nil)}); herr.Kind(err) != ReasonBadBundle {
		t.Errorf("missing bundle: %v", err)
	}
}

// Every embedded bundle parses, and every component it inserts is in the catalogue
// of this binary (bar the carriers on unregistered). A bundle's other operations
// target entries of the bundles below it, so they are checked in their profiles'
// composition (TestBuiltinProfilesCompose), not alone.
func TestBundlesParseAndResolve(t *testing.T) {
	cat, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	used := map[string]bool{}
	for _, d := range append(Definitions(), Builtins()...) {
		for _, b := range d.Bundles {
			used[b] = true
		}
	}
	for _, name := range BundleNames() {
		if !used[name] {
			t.Errorf("bundle %s is used by no profile", name)
		}
		layer, err := Bundle(name)
		if err != nil {
			t.Fatal(err)
		}
		tree, _, issues := loader.Compose([]loader.Layer{insertsOf(layer)})
		if len(issues) > 0 {
			t.Errorf("bundle %s: %v", name, issues)
		}
		var walk func([]loader.EntryOptions)
		walk = func(entries []loader.EntryOptions) {
			for _, e := range entries {
				if !e.Group && !unregistered[e.Name] {
					if _, err := cat.Lookup(e.Name); err != nil {
						t.Errorf("bundle %s: entry %s: %v", name, e.ID, err)
					}
				}
				walk(e.Children)
			}
		}
		walk(tree)
	}
	for name := range used {
		if !slices.Contains(BundleNames(), name) {
			t.Errorf("bundle %s is not embedded", name)
		}
	}
}

//dsh:test DH-T-BOOT-7
func TestLookupUnknown(t *testing.T) {
	_, err := Lookup("nope")
	if herr.Kind(err) != ReasonUnknown {
		t.Fatalf("err = %v", err)
	}
	d, err := Lookup(Headless)
	if err != nil || d.Limits != DefaultLimits() {
		t.Fatalf("headless = %+v, %v", d, err)
	}
}

// insertsOf is layer with only its root inserts.
func insertsOf(layer loader.Layer) loader.Layer {
	out := loader.Layer{Label: layer.Label}
	for _, op := range layer.Ops {
		if op.Kind == loader.OpInsert && op.ID == "" {
			out.Ops = append(out.Ops, op)
		}
	}
	return out
}
