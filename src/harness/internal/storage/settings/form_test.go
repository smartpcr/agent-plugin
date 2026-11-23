package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/settings"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

type richInner struct {
	Depth int `json:"depth" dsh:"live"`
}

type richEmbedded struct {
	Hidden string `dsh:"live"`
}

// richConfig exercises how live fields are found: nested and pointer structs,
// promoted fields of an embedded struct, a field json skips, untagged and
// unnamed json fields, an unexported field and a scalar that is not live.
type richConfig struct {
	richEmbedded
	Inner  richInner  `json:"inner"`
	Ptr    *richInner `json:"ptr,omitempty"`
	Skip   string     `json:"-" dsh:"live"`
	Bare   string     `dsh:"live"`
	Named  string     `json:",omitempty" dsh:"live"`
	Count  int        `json:"count"`
	unseen string
}

func richDefaults() richConfig { return richConfig{unseen: "kept out of the form"} }

const richProfile = `# written by dsh
- insert:
    - id: rich
      name: x.rich
      config:
        inner:
          depth: 3
    - id: plain
      name: x.plain
`

func richCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	rich := cordis.Define("x.rich", func(*cordis.Context, richConfig) error { return nil }, cordis.WithDefaults(richDefaults))
	plain := cordis.Define("x.plain", func(*cordis.Context, struct{}) error { return nil })
	cat, err := catalog.Build([]catalog.Item{
		{Name: "x.rich", Component: rich, Config: reflect.TypeFor[richConfig]()},
		{Name: "x.plain", Component: plain},
	})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

type richFixture struct {
	home, profile, user string
	config              *config.Service
	form                *settings.Service
}

// newRich builds the rich fixture; with running set, the tree is loaded so the
// form reads the components' resolved configs. guard, when not nil, replaces the
// layer lock of an edit.
func newRich(t *testing.T, running bool, guard loader.WriteGuard) *richFixture {
	t.Helper()
	cat := richCatalog(t)
	f := &richFixture{home: temphome.Dir(t)}
	def := profile.Definition{Name: "rich"}
	f.profile, f.user = profile.LayerPath(f.home, def.Name), profile.UserLayerPath(f.home)
	temphome.Write(t, f.profile, richProfile)
	opts := config.ServiceOptions{Catalog: cat, Guard: guard, Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return profile.Stack(def, profile.Sources{Home: f.home, Read: profile.ReadFile(read)})
	}}
	var ldr *loader.Service
	if running {
		root := testkit.Tree(t)
		fiber, err := root.Use(loader.Component, loader.Options{Resolver: cat})
		if err != nil {
			t.Fatal(err)
		}
		if err := fiber.Wait(); err != nil {
			t.Fatal(err)
		}
		if ldr, err = loader.Key.Get(root); err != nil {
			t.Fatal(err)
		}
		opts.Loader = ldr
	}
	cs, err := config.NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	if running {
		if err := ldr.ApplyLayers(cs.Current().Layers); err != nil {
			t.Fatal(err)
		}
	}
	f.config, f.form = cs, settings.New(cs, cat)
	return f
}

func paths(e settings.Entry) []string {
	var out []string
	for _, f := range e.Fields {
		out = append(out, f.Path)
	}
	slices.Sort(out)
	return out
}

func fieldAt(t *testing.T, e settings.Entry, path string) settings.Field {
	t.Helper()
	for _, f := range e.Fields {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("no %s in %+v", path, e.Fields)
	return settings.Field{}
}

// The form lists the live fields of nested and pointer structs and promoted
// fields, under their json names (the Go name when json gives none), and leaves
// out a field json skips, unexported and non-live fields, and an entry whose
// component has no config or no live field.
func TestDescribeFindsLiveFieldsByType(t *testing.T) {
	f := newRich(t, false, nil)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Entries) != 1 || form.Entries[0].ID != "rich" || form.Entries[0].Name != "x.rich" {
		t.Fatalf("entries = %+v, want rich only", form.Entries)
	}
	want := []string{"config.Bare", "config.Hidden", "config.Named", "config.inner.depth", "config.ptr.depth"}
	if got := paths(form.Entries[0]); !slices.Equal(got, want) {
		t.Fatalf("live paths = %v, want %v", got, want)
	}
	if d := fieldAt(t, form.Entries[0], "config.inner.depth"); string(d.Value) != "3" || d.Layer != "profile" {
		t.Errorf("inner.depth = %+v, want 3 from profile", d)
	}
	if b := fieldAt(t, form.Entries[0], "config.Bare"); string(b.Value) != `""` || b.Layer != settings.DefaultLayer {
		t.Errorf("Bare = %+v, want the component's empty default from the default layer", b)
	}
}

// Over a running tree a live field reads the resolved config; a field below a
// pointer the config leaves nil has no value there and stays null.
func TestDescribeReadsTheRunningConfig(t *testing.T) {
	f := newRich(t, true, nil)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	e := form.Entries[0]
	if d := fieldAt(t, e, "config.inner.depth"); string(d.Value) != "3" {
		t.Errorf("inner.depth = %+v, want the running 3", d)
	}
	if p := fieldAt(t, e, "config.ptr.depth"); string(p.Value) != "null" {
		t.Errorf("ptr.depth = %+v, want null below a nil pointer", p)
	}
	if b := fieldAt(t, e, "config.Bare"); string(b.Value) != `""` {
		t.Errorf("Bare = %+v, want the running empty string", b)
	}
}

// A form over no catalog has no live field, so it lists nothing.
func TestDescribeWithoutACatalog(t *testing.T) {
	f := newRich(t, false, nil)
	form, err := settings.New(f.config, nil).Describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Entries) != 0 || form.Revision == "" {
		t.Fatalf("form = %+v, want no entry", form)
	}
}

// When a layer on disk does not parse, the form is pending with the parse
// issues, still shows the effective values, and an edit is not saved.
//
//dsh:test DH-T-STORAGE-21
func TestUnparsableLayerOnDisk(t *testing.T) {
	f := newRich(t, false, nil)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	rev := form.Revision
	temphome.Write(t, f.user, "- id: rich\n  merge: [\n")
	form, err = f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	if !form.Pending || len(form.Issues) == 0 || form.Issues[0].Message == "" {
		t.Fatalf("form over an unparsable layer: pending %v, issues %+v", form.Pending, form.Issues)
	}
	if d := fieldAt(t, form.Entries[0], "config.inner.depth"); string(d.Value) != "3" {
		t.Errorf("inner.depth = %+v, want the effective 3", d)
	}
	before := read(t, f.profile)
	res := f.form.Apply(context.Background(), rev, "rich", "config.inner.depth", json.RawMessage(`4`))
	if res.Status != settings.Unsaved || !herr.Has(res.Err, settings.ReasonUnsaved) {
		t.Fatalf("edit over an unparsable layer = %+v", res)
	}
	if read(t, f.profile) != before {
		t.Error("an unsaved edit changed the profile layer")
	}
}

// An edit of an entry that does not exist, or with a value that is not JSON, is
// refused with the field's path and writes nothing.
//
//dsh:test DH-T-STORAGE-20
func TestApplyRefusesUnknownEntryAndBadJSON(t *testing.T) {
	f := newRich(t, false, nil)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	before := read(t, f.profile)
	res := f.form.Apply(context.Background(), form.Revision, "nobody", "config.level", json.RawMessage(`1`))
	if res.Status != settings.Failed || !herr.Has(res.Err, settings.ReasonNotLive) || len(res.Issues) != 1 || res.Issues[0].Path != "nobody" {
		t.Fatalf("unknown entry = %+v", res)
	}
	res = f.form.Apply(context.Background(), form.Revision, "rich", "config.inner.depth", json.RawMessage(`{not json`))
	if res.Status != settings.Failed || res.Err == nil || len(res.Issues) != 1 || res.Issues[0].Path != "rich.config.inner.depth" {
		t.Fatalf("bad JSON = %+v", res)
	}
	if read(t, f.profile) != before {
		t.Error("a refused edit changed the profile layer")
	}
}

// A valid edit the layer write then fails is unsaved, with the write's error.
//
//dsh:test DH-T-STORAGE-21
func TestApplyWhoseWriteFailsIsUnsaved(t *testing.T) {
	diskFull := errors.New("disk full")
	f := newRich(t, false, func(string, func() error) error { return diskFull })
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	before := read(t, f.profile)
	res := f.form.Apply(context.Background(), form.Revision, "rich", "config.inner.depth", json.RawMessage(`4`))
	if res.Status != settings.Unsaved || !herr.Has(res.Err, settings.ReasonUnsaved) || !errors.Is(res.Err, diskFull) {
		t.Fatalf("failed write = %+v", res)
	}
	if after := read(t, f.profile); after != before || strings.Contains(after, "depth: 4") {
		t.Error("an unsaved edit changed the profile layer")
	}
}

// A nested live field is edited as one merge that sets only that field: the
// form then shows the new value from the profile layer, at the new revision,
// and the field beside it keeps its value.
//
//dsh:test DH-T-STORAGE-19
func TestApplyANestedField(t *testing.T) {
	f := newRich(t, false, nil)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	res := f.form.Apply(context.Background(), form.Revision, "rich", "config.inner.depth", json.RawMessage(`4`))
	if res.Status != settings.Applied {
		t.Fatalf("nested edit = %+v", res)
	}
	if form, err = f.form.Describe(); err != nil {
		t.Fatal(err)
	}
	if d := fieldAt(t, form.Entries[0], "config.inner.depth"); string(d.Value) != "4" || d.Layer != "profile" || form.Pending || form.Revision != res.Revision {
		t.Fatalf("after the edit inner.depth = %+v, pending %v, revision %s (edit %s)", d, form.Pending, form.Revision, res.Revision)
	}
	if b := fieldAt(t, form.Entries[0], "config.Bare"); string(b.Value) != `""` {
		t.Errorf("the edit changed Bare: %+v", b)
	}
	if after := read(t, f.profile); !strings.Contains(after, "merge") || !strings.Contains(after, "depth: 4") {
		t.Errorf("profile layer:\n%s", after)
	}
}
