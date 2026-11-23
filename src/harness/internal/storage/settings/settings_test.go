package settings_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/expr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/settings"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

type probeConfig struct {
	Level string `json:"level,omitempty" dsh:"live"`
	Token string `json:"token,omitempty" dsh:"live,secret"`
	Mode  string `json:"mode,omitempty" dsh:"live"`
	Port  int    `json:"port,omitempty"`
}

// probeDefaults supplies mode, which no layer sets.
func probeDefaults() probeConfig { return probeConfig{Mode: "auto"} }

const profileText = `# written by dsh
- insert:
    - id: probe
      name: x.probe
      config:
        level: info
        token: hunter2
        port: 1
`

type fixture struct {
	home, profile, user string
	config              *config.Service
	form                *settings.Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	cat := probeCatalog(t)
	f := &fixture{home: temphome.Dir(t)}
	def := profile.Definition{Name: "probe"}
	f.profile, f.user = profile.LayerPath(f.home, def.Name), profile.UserLayerPath(f.home)
	temphome.Write(t, f.profile, profileText)
	cs, err := config.NewService(config.ServiceOptions{Catalog: cat, Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return profile.Stack(def, profile.Sources{Home: f.home, Read: profile.ReadFile(read)})
	}})
	if err != nil {
		t.Fatal(err)
	}
	f.config, f.form = cs, settings.New(cs, cat)
	return f
}

func probeCatalog(t *testing.T) *catalog.Catalog {
	t.Helper()
	component := cordis.Define("x.probe", func(*cordis.Context, probeConfig) error { return nil }, cordis.WithDefaults(probeDefaults))
	cat, err := catalog.Build([]catalog.Item{{Name: "x.probe", Component: component, Config: reflect.TypeFor[probeConfig]()}})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

//dsh:test DH-T-STORAGE-21
func TestDescribeListsLiveFieldsWithTheirLayer(t *testing.T) {
	f := newFixture(t)
	temphome.Write(t, f.user, "- id: probe\n  merge:\n    level: warn\n")
	if err := f.config.Reload(); err != nil {
		t.Fatal(err)
	}
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	if len(form.Entries) != 1 || form.Revision == "" || form.Pending {
		t.Fatalf("form = %+v", form)
	}
	got := map[string]settings.Field{}
	for _, field := range form.Entries[0].Fields {
		got[field.Path] = field
	}
	if len(got) != 3 {
		t.Errorf("live fields = %+v, want level, token and mode only", form.Entries[0].Fields)
	}
	if l := got["config.level"]; string(l.Value) != `"warn"` || l.Layer != "user" {
		t.Errorf("level = %+v", l)
	}
	if tok := got["config.token"]; !tok.Secret || string(tok.Value) != `"<set>"` || tok.Layer != "profile" {
		t.Errorf("token = %+v", tok)
	}
}

func level(t *testing.T, form *settings.Form) settings.Field {
	t.Helper()
	for _, field := range form.Entries[0].Fields {
		if field.Path == "config.level" {
			return field
		}
	}
	t.Fatalf("no level in %+v", form)
	return settings.Field{}
}

// After a rejected reload the form shows the value the running tree keeps, not the
// rejected one on disk, and says the disk differs and why.
//
//dsh:test DH-T-STORAGE-21
func TestDescribeShowsEffectiveValueAfterRejection(t *testing.T) {
	f := newFixture(t)
	temphome.Write(t, f.user, "- id: probe\n  merge:\n    level: 7\n")
	if err := f.config.Reload(); !herr.Has(err, config.ReasonRejected) {
		t.Fatalf("reload = %v, want rejected", err)
	}
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	if l := level(t, form); string(l.Value) != `"info"` || l.Layer != "profile" {
		t.Errorf("level = %+v, want the effective info from profile", l)
	}
	if !form.Pending || len(form.Issues) == 0 || !strings.Contains(form.Issues[0].Path, "probe.config.level") {
		t.Errorf("pending = %v, issues = %+v", form.Pending, form.Issues)
	}
	if form.Revision == f.config.Revision() {
		t.Error("the form's revision is not the disk's, so an edit would be stale")
	}
}

// An edit to a field an expression computes is refused, and nothing is written.
//
//dsh:test DH-T-STORAGE-20, DH-T-BOOT-16
func TestComputedFieldEditIsRefused(t *testing.T) {
	f := newFixture(t)
	temphome.Write(t, f.profile, profileText+"- id: probe\n  merge:\n    level: !expr '\"warn\"'\n")
	if err := f.config.Reload(); err != nil {
		t.Fatal(err)
	}
	before := read(t, f.profile)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	res := f.form.Apply(context.Background(), form.Revision, "probe", "config.level", json.RawMessage(`"debug"`))
	if res.Status != settings.Failed || !herr.Has(res.Err, config.ReasonComputed) {
		t.Fatalf("result = %+v", res)
	}
	if read(t, f.profile) != before {
		t.Error("a refused edit changed the file")
	}
}

// A live edit lands as one merge in the profile layer; an edit from the same old
// read is then stale and changes nothing.
//
//dsh:test DH-T-STORAGE-19, DH-T-STORAGE-20
func TestApplyThenStaleEdit(t *testing.T) {
	f := newFixture(t)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	res := f.form.Apply(context.Background(), form.Revision, "probe", "config.level", json.RawMessage(`"debug"`))
	if res.Status != settings.Applied || res.Revision == form.Revision {
		t.Fatalf("result = %+v", res)
	}
	after := read(t, f.profile)
	if !strings.Contains(after, "# written by dsh") || !strings.Contains(after, "merge") || !strings.Contains(after, "debug") {
		t.Errorf("profile layer:\n%s", after)
	}
	if _, err := os.Stat(f.user); !os.IsNotExist(err) {
		t.Errorf("the user layer was written: %v", err)
	}
	res = f.form.Apply(context.Background(), form.Revision, "probe", "config.level", json.RawMessage(`"warn"`))
	if res.Status != settings.Stale || !herr.Has(res.Err, config.ReasonStale) {
		t.Fatalf("stale edit = %+v", res)
	}
	if read(t, f.profile) != after {
		t.Error("a stale edit changed the file")
	}
}

// A bad, a not-live and a shadowed edit are refused with the field path and no
// file change.
//
//dsh:test DH-T-STORAGE-20
func TestRefusedEditsChangeNothing(t *testing.T) {
	cases := []struct {
		name, path, value string
		status            settings.Status
		reason            herr.Reason
	}{
		{"invalid", "config.token", `7`, settings.Failed, config.ReasonRejected},
		{"not live", "config.port", `2`, settings.Failed, settings.ReasonNotLive},
		{"shadowed", "config.level", `"debug"`, settings.Overridden, settings.ReasonOverridden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			temphome.Write(t, f.user, "- id: probe\n  merge:\n    level: warn\n")
			before, user := read(t, f.profile), read(t, f.user)
			form, err := f.form.Describe()
			if err != nil {
				t.Fatal(err)
			}
			res := f.form.Apply(context.Background(), form.Revision, "probe", tc.path, json.RawMessage(tc.value))
			if res.Status != tc.status || !herr.Has(res.Err, tc.reason) {
				t.Fatalf("result = %+v", res)
			}
			if tc.status == settings.Failed && (len(res.Issues) == 0 || !strings.Contains(res.Issues[0].Path, "probe."+tc.path)) {
				t.Errorf("issues do not name the field: %+v", res.Issues)
			}
			if tc.status == settings.Overridden && res.Layer != "user" {
				t.Errorf("shadowing layer = %q", res.Layer)
			}
			if read(t, f.profile) != before || read(t, f.user) != user {
				t.Error("a refused edit changed a layer file")
			}
		})
	}
}

// A live field no layer sets shows the component's default, from the default layer.
func TestDescribeShowsDefaults(t *testing.T) {
	f := newFixture(t)
	form, err := f.form.Describe()
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range form.Entries[0].Fields {
		if field.Path == "config.mode" {
			if string(field.Value) != `"auto"` || field.Layer != settings.DefaultLayer || field.Computed {
				t.Errorf("mode = %+v, want the default auto", field)
			}
			return
		}
	}
	t.Fatalf("no mode in %+v", form.Entries[0].Fields)
}

// Over a running tree, a field an expression computes shows the value the running
// component holds, marked computed, with the layer that holds the expression.
//
//dsh:test DH-T-STORAGE-21
func TestDescribeShowsEvaluatedExpression(t *testing.T) {
	cat := probeCatalog(t)
	home := temphome.Dir(t)
	def := profile.Definition{Name: "probe"}
	temphome.Write(t, profile.LayerPath(home, def.Name), profileText+"- id: probe\n  merge:\n    level: !expr '\"wa\" + \"rn\"'\n")
	evaluator, err := expr.New(expr.Options{})
	if err != nil {
		t.Fatal(err)
	}
	root := testkit.Tree(t)
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: cat, Evaluator: evaluator})
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
	cs, err := config.NewService(config.ServiceOptions{Catalog: cat, Loader: ldr, Evaluator: evaluator, Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return profile.Stack(def, profile.Sources{Home: home, Read: profile.ReadFile(read)})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Current().Report.Err(); err != nil {
		t.Fatal(err)
	}
	if err := ldr.ApplyLayers(cs.Current().Layers); err != nil {
		t.Fatal(err)
	}
	form, err := settings.New(cs, cat).Describe()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]settings.Field{}
	for _, field := range form.Entries[0].Fields {
		got[field.Path] = field
	}
	if l := got["config.level"]; string(l.Value) != `"warn"` || !l.Computed || l.Layer != "profile" {
		t.Errorf("level = %+v, want the evaluated warn, computed, from profile", l)
	}
	if m := got["config.mode"]; string(m.Value) != `"auto"` || m.Layer != settings.DefaultLayer {
		t.Errorf("mode = %+v, want the running default", m)
	}
	if tok := got["config.token"]; string(tok.Value) != `"<set>"` {
		t.Errorf("token = %+v, want the presence marker", tok)
	}
}

// A Reloaded listener that reads the form sees the reload it is told about: the
// new value, from the layer that now owns it, with nothing pending.
func TestDescribeFromReloadedListenerSeesTheNewTree(t *testing.T) {
	cat := probeCatalog(t)
	home := temphome.Dir(t)
	def := profile.Definition{Name: "probe"}
	temphome.Write(t, profile.LayerPath(home, def.Name), profileText)
	root := testkit.Tree(t)
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: cat})
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
	cs, err := config.NewService(config.ServiceOptions{Catalog: cat, Loader: ldr, Context: root, Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return profile.Stack(def, profile.Sources{Home: home, Read: profile.ReadFile(read)})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ldr.ApplyLayers(cs.Current().Layers); err != nil {
		t.Fatal(err)
	}
	form := settings.New(cs, cat)
	var told []*settings.Form
	if _, err := loader.Reloaded.On(root, func(*cordis.Context, loader.ReloadNotice) error {
		f, err := form.Describe()
		if err != nil {
			t.Error(err)
			return nil
		}
		told = append(told, f)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	temphome.Write(t, profile.UserLayerPath(home), "- id: probe\n  merge:\n    level: warn\n")
	if err := cs.Reload(); err != nil {
		t.Fatal(err)
	}
	if len(told) != 1 {
		t.Fatalf("told %d times, want once", len(told))
	}
	if l := level(t, told[0]); string(l.Value) != `"warn"` || l.Layer != "user" || told[0].Pending || told[0].Revision != cs.Revision() {
		t.Errorf("listener's form = level %+v, pending %v, revision %s; want warn from user at %s", l, told[0].Pending, told[0].Revision, cs.Revision())
	}
}

// A lifecycle observer registered before the service may hand a settings edit to
// another goroutine and wait for it. The form read by the listener told of that
// edit's reconciliation shows it, and the reload still in publication, with nothing
// pending.
func TestDescribeFromEditHandedOffByAnEarlierObserverSeesTheEdit(t *testing.T) {
	cat := probeCatalog(t)
	home := temphome.Dir(t)
	def := profile.Definition{Name: "probe"}
	temphome.Write(t, profile.LayerPath(home, def.Name), profileText)
	root := testkit.Tree(t)
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: cat})
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
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	var form *settings.Service
	var cs *config.Service
	var fired atomic.Bool
	var handed settings.Result
	t.Cleanup(ldr.OnLifecycle(func(ev loader.LifecycleEvent) {
		if ev.Kind != loader.EntryUpdated || form == nil || !fired.CompareAndSwap(false, true) {
			return
		}
		done := make(chan settings.Result, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			done <- form.Apply(context.Background(), cs.Revision(), "probe", "config.mode", json.RawMessage(`"manual"`))
		}()
		select {
		case handed = <-done:
		case <-time.After(10 * time.Second):
			handed = settings.Result{Status: settings.Failed}
		}
	}))
	cs, err = config.NewService(config.ServiceOptions{Catalog: cat, Loader: ldr, Context: root, Stack: func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return profile.Stack(def, profile.Sources{Home: home, Read: profile.ReadFile(read)})
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := ldr.ApplyLayers(cs.Current().Layers); err != nil {
		t.Fatal(err)
	}
	form = settings.New(cs, cat)
	var told []*settings.Form
	var revisions []string
	if _, err := loader.Reloaded.On(root, func(*cordis.Context, loader.ReloadNotice) error {
		f, err := form.Describe()
		if err != nil {
			t.Error(err)
			return nil
		}
		told, revisions = append(told, f), append(revisions, cs.Revision())
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	temphome.Write(t, profile.UserLayerPath(home), "- id: probe\n  merge:\n    level: warn\n")
	if err := cs.Reload(); err != nil {
		t.Fatal(err)
	}
	if handed.Status != settings.Applied {
		t.Fatalf("handed edit = %+v, want applied", handed)
	}
	if len(told) == 0 {
		t.Fatal("no listener was told")
	}
	// the first notice is the edit's: the reload is still telling its observers
	got := map[string]settings.Field{}
	for _, field := range told[0].Entries[0].Fields {
		got[field.Path] = field
	}
	if m := got["config.mode"]; string(m.Value) != `"manual"` || m.Layer != "profile" {
		t.Errorf("edit's listener: mode = %+v, want the edit's manual from profile", m)
	}
	if l := got["config.level"]; string(l.Value) != `"warn"` || l.Layer != "user" {
		t.Errorf("edit's listener: level = %+v, want the reload's warn from user", l)
	}
	if told[0].Pending || told[0].Revision != revisions[0] {
		t.Errorf("edit's listener: form revision %s, pending %v; want %s, nothing pending", told[0].Revision, told[0].Pending, revisions[0])
	}
}
