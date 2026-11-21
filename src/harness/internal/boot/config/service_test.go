package config_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// probeConfig is the config of the test component: a level its Validate checks
// (so only the component's schema rejects a bad level) and a typed port (so the
// static check rejects a port of the wrong type).
type probeConfig struct {
	Level string `json:"level,omitempty"`
	Port  int    `json:"port,omitempty"`
}

func (c probeConfig) Validate() error {
	switch c.Level {
	case "", "debug", "info", "warn":
		return nil
	}
	return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Path: "level", Message: fmt.Sprintf("level %q is not debug, info or warn", c.Level)}}}
}

// strictConfig is the config of x.strict, which cannot run on its defaults: its
// schema refuses an empty name.
type strictConfig struct {
	Name string `json:"name,omitempty"`
}

func (c strictConfig) Validate() error {
	if c.Name == "" {
		return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Path: "name", Message: "name is required"}}}
	}
	return nil
}

// probes is a catalogue of two counting components: x.probe, which runs on its
// defaults, and x.strict, which does not.
type probes struct {
	cat    *catalog.Catalog
	starts atomic.Int64
	// gate holds the cleanup of every x.hold until it is closed.
	gate chan struct{}
	// build holds the constructor of every x.slow until it is closed; building is
	// told each time one starts.
	build, building chan struct{}
}

// literals is an evaluator of three sources: true and false are booleans, word is a
// string, and anything else fails to evaluate.
type literals struct{}

func (literals) Check(string, loader.ScopeKind) error { return nil }

func (literals) Eval(source string, _ loader.Scope) (any, error) {
	switch source {
	case "true", "false":
		return source == "true", nil
	case "word":
		return "word", nil
	}
	return nil, fmt.Errorf("cannot evaluate %q", source)
}

func newProbes(t *testing.T) *probes {
	t.Helper()
	p := &probes{gate: make(chan struct{}), build: make(chan struct{}), building: make(chan struct{}, 8)}
	component := cordis.Define("x.probe", func(*cordis.Context, probeConfig) error {
		p.starts.Add(1)
		return nil
	})
	strict := cordis.Define("x.strict", func(*cordis.Context, strictConfig) error {
		p.starts.Add(1)
		return nil
	})
	hold := cordis.Define("x.hold", func(ctx *cordis.Context, _ struct{}) error {
		_, err := ctx.Effect("held", func() (cordis.Disposer, error) {
			return func() error { <-p.gate; return nil }, nil
		})
		return err
	})
	slow := cordis.Define("x.slow", func(*cordis.Context, struct{}) error {
		p.building <- struct{}{}
		<-p.build
		return nil
	})
	cat, err := catalog.Build([]catalog.Item{
		{Name: "x.probe", Component: component, Config: reflect.TypeFor[probeConfig]()},
		{Name: "x.strict", Component: strict, Config: reflect.TypeFor[strictConfig]()},
		{Name: "x.hold", Component: hold, Config: reflect.TypeFor[struct{}]()},
		{Name: "x.slow", Component: slow, Config: reflect.TypeFor[struct{}]()},
	})
	if err != nil {
		t.Fatal(err)
	}
	p.cat = cat
	return p
}

// layer parses a YAML layer held in memory, with its document for positions.
func layer(t *testing.T, label, file, text string) (loader.Layer, *loader.LayerDocument) {
	t.Helper()
	l, issues, err := profile.ParseLayer(label, file, []byte(text))
	if err != nil || len(issues) > 0 {
		t.Fatalf("layer %s: %v %v", label, err, issues)
	}
	config.Register()
	doc, err := loader.ParseLayer(nil, file, []byte(text))
	if err != nil {
		t.Fatal(err)
	}
	return l, doc
}

const bundleText = `- insert:
    - id: probe
      name: x.probe
      config:
        level: info
        port: 1
    - id: quiet
      name: x.probe
      disabled: true
`

const userText = `# the user's own choices
- id: probe
  merge:
    level: debug
`

// TC-06a: preview composes and checks layers held in memory, names the layer that
// owns each entry and value, and constructs no component.
func TestPreviewWithoutStart(t *testing.T) {
	p := newProbes(t)
	bundle, bdoc := layer(t, "bundle:core", "core.yml", bundleText)
	user, udoc := layer(t, "user", "user.yml", userText)
	opts := config.CheckOptions{Catalog: p.cat, Docs: map[string]*loader.LayerDocument{"bundle:core": bdoc, "user": udoc}}

	r := config.Preview([]loader.Layer{bundle, user}, opts)
	if !r.OK() {
		t.Fatalf("issues: %v", r.Issues)
	}
	entries := r.Entries()
	if len(entries) != 2 || entries[0].ID != "probe" || entries[1].ID != "quiet" {
		t.Fatalf("entries = %+v", entries)
	}
	if entries[0].Layer != "bundle:core" || !entries[1].Disabled {
		t.Errorf("owners = %+v", entries)
	}
	if got := string(entries[0].Config); got != `{"level":"debug","port":1}` {
		t.Errorf("composed config = %s", got)
	}
	if !reflect.DeepEqual(entries[0].Fields, []config.FieldOwner{{Field: "config.level", Layer: "user"}}) {
		t.Errorf("field owners = %+v", entries[0].Fields)
	}
	ex, err := r.Explain("probe", "config.level")
	if err != nil {
		t.Fatal(err)
	}
	if ex.Layer != "user" || string(ex.Value) != `"debug"` || !strings.HasPrefix(ex.At, "user.yml:4:") {
		t.Errorf("explain = %+v", ex)
	}
	if ex, err := r.Explain("probe", "config.port"); err != nil || ex.Layer != "bundle:core" || string(ex.Value) != "1" {
		t.Errorf("explain port = %+v, %v", ex, err)
	}
	if _, err := r.Explain("nobody", ""); err == nil {
		t.Error("explain of an unknown entry succeeded")
	}
	first, err := r.Dump()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := config.Preview([]loader.Layer{bundle, user}, opts).Dump()
	if string(first) != string(second) {
		t.Errorf("dump is not deterministic:\n%s\n%s", first, second)
	}
	if n := p.starts.Load(); n != 0 {
		t.Errorf("preview constructed %d components", n)
	}
}

// The static check places a bad value at the line of the layer that wrote it.
func TestCheckPositionsBadValues(t *testing.T) {
	p := newProbes(t)
	bundle, bdoc := layer(t, "bundle:core", "core.yml", bundleText)
	user, udoc := layer(t, "user", "user.yml", "- id: probe\n  merge:\n    colour: red\n- insert:\n    - id: ghost\n      name: x.ghost\n")
	r := config.Check([]loader.Layer{bundle, user}, config.CheckOptions{Catalog: p.cat, Docs: map[string]*loader.LayerDocument{"bundle:core": bdoc, "user": udoc}})
	var got []string
	for _, issue := range r.Issues {
		got = append(got, issue.Error())
	}
	text := strings.Join(got, "\n")
	for _, want := range []string{"user.yml:3:", "probe.config.colour", `unknown field "colour"`, "user.yml:6:", "ghost.name", "unknown component"} {
		if !strings.Contains(text, want) {
			t.Errorf("issues lack %q:\n%s", want, text)
		}
	}
	r = config.Check([]loader.Layer{bundle, mustLayer(t, "user", "- id: probe\n  merge:\n    port: high\n")}, config.CheckOptions{Catalog: p.cat})
	if r.OK() || !strings.Contains(r.Issues[0].Path, "probe.config.port") {
		t.Errorf("a port of the wrong type passed: %v", r.Issues)
	}
	if p.starts.Load() != 0 {
		t.Error("the check constructed a component")
	}
}

func mustLayer(t *testing.T, label, text string) loader.Layer {
	l, _ := layer(t, label, label+".yml", text)
	return l
}

func hasReason(err error, r herr.Reason) bool { return err != nil && herr.Has(err, r) }

// home is a temp home holding a profile layer, with a stack reader over it.
type home struct {
	root  string
	def   profile.Definition
	stack config.Stack
}

const profileText = `# written by dsh; comments stay
- insert:
    - id: probe
      name: x.probe
      config:
        level: info
    - id: other
      name: x.probe
      config:
        port: 1
`

func newHome(t *testing.T) *home {
	t.Helper()
	h := &home{root: filepath.Join(t.TempDir(), "home"), def: profile.Definition{Name: "probe"}}
	write(t, profile.LayerPath(h.root, h.def.Name), profileText)
	h.stack = func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return profile.Stack(h.def, profile.Sources{Home: h.root, Read: profile.ReadFile(read)})
	}
	return h
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

// boot mounts a loader with the probes, applies the home's layers through a
// config service and returns both.
func boot(t *testing.T, h *home, p *probes) (*loader.Service, *config.Service) {
	t.Helper()
	return bootWith(t, h, p, config.ServiceOptions{})
}

// bootWith is boot with the service's options beyond the stack, loader and catalog.
func bootWith(t *testing.T, h *home, p *probes, opts config.ServiceOptions) (*loader.Service, *config.Service) {
	t.Helper()
	_, ldr, cs := bootRoot(t, h, p, opts)
	return ldr, cs
}

// bootRoot is bootWith that also returns the tree's root.
func bootRoot(t *testing.T, h *home, p *probes, opts config.ServiceOptions) (*cordis.Context, *loader.Service, *config.Service) {
	t.Helper()
	root := cordis.New(cordis.WithExporters())
	t.Cleanup(root.Dispose)
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: p.cat, Evaluator: opts.Evaluator})
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
	opts.Stack, opts.Loader, opts.Catalog, opts.Context = h.stack, ldr, p.cat, root
	cs, err := config.NewService(opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Current().Report.Err(); err != nil {
		t.Fatal(err)
	}
	if err := ldr.ApplyLayers(cs.Current().Layers); err != nil {
		t.Fatal(err)
	}
	return root, ldr, cs
}

func effective(t *testing.T, ldr *loader.Service, id string) probeConfig {
	t.Helper()
	entry := ldr.Find(id)
	if entry == nil {
		t.Fatalf("no entry %s", id)
	}
	cfg, ok := ldr.Fiber(entry).EffectiveConfig()
	if !ok {
		t.Fatalf("%s holds no effective config", id)
	}
	return cfg.(probeConfig)
}

// TC-06b: a bad user-layer edit is rejected whole at reload, with a positioned
// diagnostic, and no running component changes, not even the one the same edit
// changed validly.
func TestBadLayerEditLeavesRunningTreeUnchanged(t *testing.T) {
	cases := map[string]struct{ text, at, path string }{
		// Only the component's schema refuses this level: DH-D-CORDIS-5.
		"schema": {"- id: other\n  merge:\n    port: 2\n- id: probe\n  merge:\n    level: loud\n", ":6:", "probe.config.level"},
		// The static check refuses a port that is not a number.
		"static": {"- id: other\n  merge:\n    port: 2\n- id: probe\n  merge:\n    port: high\n", ":6:", "probe.config.port"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, p := newHome(t), newProbes(t)
			ldr, cs := boot(t, h, p)
			before := map[string]probeConfig{"probe": effective(t, ldr, "probe"), "other": effective(t, ldr, "other")}
			starts, revision := p.starts.Load(), cs.Revision()
			var told []error
			cs.OnRejected(func(err error) { told = append(told, err) })

			user := profile.UserLayerPath(h.root)
			write(t, user, tc.text)
			err := cs.Reload()
			if err == nil {
				t.Fatal("the bad edit was accepted")
			}
			if !strings.Contains(err.Error(), user+tc.at) || !strings.Contains(err.Error(), tc.path) {
				t.Errorf("diagnostic is not positioned at %s%s %s:\n%v", user, tc.at, tc.path, err)
			}
			if got := config.ReasonRejected; !hasReason(err, got) {
				t.Errorf("reason of %v is not %s", err, got)
			}
			for id, want := range before {
				if got := effective(t, ldr, id); got != want {
					t.Errorf("%s effective config = %+v, want %+v", id, got, want)
				}
			}
			if n := p.starts.Load(); n != starts || cs.Revision() != revision {
				t.Errorf("starts %d -> %d, revision changed %v", starts, n, cs.Revision() != revision)
			}
			_ = cs.Reload()
			if len(told) != 1 {
				t.Errorf("listeners told %d times, want once", len(told))
			}

			// Fixing the file reloads it.
			write(t, user, "- id: other\n  merge:\n    port: 2\n")
			if err := cs.Reload(); err != nil {
				t.Fatal(err)
			}
			if got := effective(t, ldr, "other"); got.Port != 2 {
				t.Errorf("other after the fix = %+v", got)
			}
		})
	}
}

// TC-06b: two edit-path clients on one home edit different keys from the same
// read; one wins, the other is told its source is stale and wrote nothing, and
// after reading again its retry lands, so both values are on disk.
func TestConcurrentSettingsEditsDoNotLoseAWrite(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	clients := make([]*config.Service, 2)
	for i := range clients {
		cs, err := config.NewService(config.ServiceOptions{Stack: h.stack, Catalog: p.cat})
		if err != nil {
			t.Fatal(err)
		}
		clients[i] = cs
	}
	edits := []loader.Op{
		{Kind: loader.OpMerge, ID: "probe", Patch: json.RawMessage(`{"level":"debug"}`)},
		{Kind: loader.OpMerge, ID: "other", Patch: json.RawMessage(`{"port":9}`)},
	}
	read, err := clients[0].Draft()
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	errs := make([]error, 2)
	done := make(chan int, 2)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	for i := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, errs[i] = clients[i].Apply(context.Background(), config.Edit{Revision: read.Revision, Ops: []loader.Op{edits[i]}})
			done <- i
		}()
	}
	close(start)
	<-done
	<-done
	var loser = -1
	for i, err := range errs {
		switch {
		case err == nil:
		case hasReason(err, config.ReasonStale):
			if loser >= 0 {
				t.Fatalf("both edits were stale: %v", errs)
			}
			loser = i
		default:
			t.Fatalf("client %d: %v", i, err)
		}
	}
	if loser < 0 {
		t.Fatal("both edits from the same read succeeded: one write was lost")
	}
	again, err := clients[loser].Draft()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := clients[loser].Apply(context.Background(), config.Edit{Revision: again.Revision, Ops: []loader.Op{edits[loser]}}); err != nil {
		t.Fatalf("retry: %v", err)
	}

	data, err := os.ReadFile(profile.LayerPath(h.root, h.def.Name))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# written by dsh; comments stay") {
		t.Errorf("the write lost the layer's comment:\n%s", data)
	}
	l, _, err := profile.ParseLayer("profile", "layer.yml", data)
	if err != nil {
		t.Fatal(err)
	}
	r := config.Check([]loader.Layer{l}, config.CheckOptions{Catalog: p.cat})
	if !r.OK() {
		t.Fatal(r.Issues)
	}
	for id, want := range map[string]string{"probe": `{"level":"debug"}`, "other": `{"port":9}`} {
		ex, err := r.Explain(id, "config")
		if err != nil || string(ex.Value) != want {
			t.Errorf("%s on disk = %s (%v), want %s\n%s", id, ex.Value, err, want, data)
		}
	}
}

// A rejected edit writes nothing.
func TestRejectedEditWritesNothing(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	cs, err := config.NewService(config.ServiceOptions{Stack: h.stack, Catalog: p.cat})
	if err != nil {
		t.Fatal(err)
	}
	path := profile.LayerPath(h.root, h.def.Name)
	before, _ := os.ReadFile(path)
	_, err = cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{{Kind: loader.OpMerge, ID: "other", Patch: json.RawMessage(`{"port":"high"}`)}}})
	if !hasReason(err, config.ReasonRejected) || !strings.Contains(err.Error(), "other.config.port") {
		t.Fatalf("err = %v", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Errorf("a rejected edit changed the file:\n%s", after)
	}
}

// A second client, which runs no tree, still validates a candidate through each
// component's schema (DH-D-CORDIS-5): a level only Validate refuses is rejected for
// a changed entry, for a disabled entry an edit enables and for a new entry.
func TestEditWithoutRunningTreeValidatesSchema(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	path := profile.LayerPath(h.root, h.def.Name)
	write(t, path, profileText+"    - id: quiet\n      name: x.probe\n      disabled: true\n      config:\n        level: loud\n")
	cs, err := config.NewService(config.ServiceOptions{Stack: h.stack, Catalog: p.cat})
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Current().Report.Err(); err != nil {
		t.Fatalf("a disabled entry was resolved: %v", err)
	}
	before, _ := os.ReadFile(path)
	cases := map[string]struct {
		op   loader.Op
		path string
	}{
		"changed": {loader.Op{Kind: loader.OpMerge, ID: "probe", Patch: json.RawMessage(`{"level":"loud"}`)}, "probe.config.level"},
		"enabled": {loader.Op{Kind: loader.OpOverride, ID: "quiet", Set: map[string]json.RawMessage{"disabled": json.RawMessage("false")}}, "quiet.config.level"},
		"new":     {loader.Op{Kind: loader.OpInsert, Entries: []loader.EntryOptions{{ID: "fresh", Name: "x.probe", Config: json.RawMessage(`{"level":"loud"}`)}}}, "fresh.config.level"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{tc.op}})
			if !hasReason(err, config.ReasonRejected) || !strings.Contains(err.Error(), tc.path) || !strings.Contains(err.Error(), `"loud"`) {
				t.Fatalf("err = %v, want rejected at %s", err, tc.path)
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Errorf("a rejected edit changed the file:\n%s", after)
			}
		})
	}
	if p.starts.Load() != 0 {
		t.Error("validation constructed a component")
	}
}

// A running tree rejects at reload a new entry whose config only its component's
// schema refuses, though no fiber of it runs yet.
func TestReloadRejectsInvalidNewEntry(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	ldr, cs := boot(t, h, p)
	write(t, profile.UserLayerPath(h.root), "- insert:\n    - id: fresh\n      name: x.probe\n      config:\n        level: loud\n")
	if err := cs.Reload(); !hasReason(err, config.ReasonRejected) || !strings.Contains(err.Error(), "fresh.config.level") {
		t.Fatalf("reload = %v", err)
	}
	if ldr.Find("fresh") != nil {
		t.Error("the rejected entry was mounted")
	}
}

// Of two merges of one entry in one layer, the diagnostic names the one that wrote
// the bad field, not the last one.
func TestCheckPositionsTheMergeThatWroteTheField(t *testing.T) {
	p := newProbes(t)
	bundle, bdoc := layer(t, "bundle:core", "core.yml", bundleText)
	user, udoc := layer(t, "user", "user.yml", "- id: probe\n  merge:\n    level: loud\n- id: probe\n  merge:\n    port: 2\n")
	r := config.Check([]loader.Layer{bundle, user}, config.CheckOptions{Catalog: p.cat, Docs: map[string]*loader.LayerDocument{"bundle:core": bdoc, "user": udoc}})
	if r.OK() {
		t.Fatal("the bad level passed")
	}
	if got := r.Issues[0].Error(); !strings.Contains(got, "user.yml:3:") || !strings.Contains(got, "probe.config.level") {
		t.Errorf("issue = %s, want it at user.yml:3", got)
	}
}

// A listener runs outside the service's locks: it may read the state and reload.
func TestRejectedListenerMayCallTheService(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	_, cs := boot(t, h, p)
	var seen []string
	cs.OnRejected(func(error) {
		seen = append(seen, cs.Current().Revision, cs.Revision())
		_ = cs.Reload()
	})
	write(t, profile.UserLayerPath(h.root), "- id: probe\n  merge:\n    level: loud\n")
	done := make(chan error, 1)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	wg.Add(1)
	go func() {
		defer wg.Done()
		done <- cs.Reload()
	}()
	select {
	case err := <-done:
		if !hasReason(err, config.ReasonRejected) {
			t.Fatalf("reload = %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a listener calling the service deadlocked")
	}
	if len(seen) != 2 || seen[0] != seen[1] {
		t.Errorf("listener saw %v", seen)
	}
}

// A layer that does not parse is rejected once per content, however often it is
// polled; new broken content is told again, and the fix is applied.
func TestUnparsableLayerIsToldOncePerContent(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	ldr, cs := boot(t, h, p)
	var told []error
	cs.OnRejected(func(err error) { told = append(told, err) })
	user := profile.UserLayerPath(h.root)
	write(t, user, "- id: probe\n  merge: [unclosed\n")
	for range 3 {
		if err := cs.Reload(); !hasReason(err, config.ReasonRejected) || !strings.Contains(err.Error(), user) {
			t.Fatalf("reload = %v", err)
		}
	}
	if len(told) != 1 {
		t.Fatalf("told %d times for one content, want once", len(told))
	}
	write(t, user, "- id: probe\n  merge: {unclosed\n")
	_ = cs.Reload()
	if len(told) != 2 {
		t.Errorf("told %d times after new broken content, want 2", len(told))
	}
	write(t, user, "- id: probe\n  merge:\n    level: warn\n")
	if err := cs.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := effective(t, ldr, "probe"); got.Level != "warn" {
		t.Errorf("probe after the fix = %+v", got)
	}
}

// An edit replacing a value an expression computes is refused, and writes nothing,
// unless it says to overwrite; a sibling field is not computed.
func TestEditOfComputedFieldNeedsOverwrite(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	path := profile.LayerPath(h.root, h.def.Name)
	write(t, path, profileText+"- id: probe\n  merge:\n    level: !expr '\"info\"'\n")
	cs, err := config.NewService(config.ServiceOptions{Stack: h.stack, Catalog: p.cat})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	level := loader.Op{Kind: loader.OpMerge, ID: "probe", Patch: json.RawMessage(`{"level":"debug"}`)}
	whole := loader.Op{Kind: loader.OpOverride, ID: "probe", Set: map[string]json.RawMessage{"config": json.RawMessage(`{"level":"warn"}`)}}
	for _, op := range []loader.Op{level, whole} {
		_, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{op}})
		if !hasReason(err, config.ReasonComputed) || !strings.Contains(err.Error(), "probe.config.level") {
			t.Fatalf("edit %v = %v, want computed", op.Kind, err)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Fatalf("a refused edit changed the file:\n%s", after)
		}
	}
	port := loader.Op{Kind: loader.OpMerge, ID: "probe", Patch: json.RawMessage(`{"port":3}`)}
	rev, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{port}})
	if err != nil {
		t.Fatalf("sibling edit: %v", err)
	}
	if _, err := cs.Apply(context.Background(), config.Edit{Revision: rev, Ops: []loader.Op{level}, Overwrite: true}); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if ex, err := cs.Current().Report.Explain("probe", "config.level"); err != nil || string(ex.Value) != `"debug"` {
		t.Errorf("level after overwrite = %s, %v", ex.Value, err)
	}
}

// An entry that cannot run on its defaults is rejected when an edit inserts or
// enables it without a config, as its load would fail: nothing is written and the
// running tree, the valid sibling change of the same edit included, is unchanged.
func TestOmittedConfigIsValidatedToo(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	path := profile.LayerPath(h.root, h.def.Name)
	write(t, path, profileText+"    - id: dormant\n      name: x.strict\n      disabled: true\n")
	ldr, cs := boot(t, h, p)
	before, _ := os.ReadFile(path)
	starts := p.starts.Load()
	sibling := loader.Op{Kind: loader.OpMerge, ID: "other", Patch: json.RawMessage(`{"port":5}`)}
	cases := map[string]struct {
		op loader.Op
		id string
	}{
		"inserted": {loader.Op{Kind: loader.OpInsert, Entries: []loader.EntryOptions{{ID: "bare", Name: "x.strict"}}}, "bare"},
		"enabled":  {loader.Op{Kind: loader.OpOverride, ID: "dormant", Set: map[string]json.RawMessage{"disabled": json.RawMessage("false")}}, "dormant"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{sibling, tc.op}})
			if !hasReason(err, config.ReasonRejected) || !strings.Contains(err.Error(), tc.id+".config.name") || !strings.Contains(err.Error(), "name is required") {
				t.Fatalf("err = %v, want rejected at %s.config.name", err, tc.id)
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Errorf("a rejected edit changed the file:\n%s", after)
			}
			if got := effective(t, ldr, "other"); got.Port != 1 {
				t.Errorf("the sibling changed to %+v", got)
			}
			if e := ldr.Find(tc.id); e != nil && ldr.Fiber(e) != nil {
				t.Errorf("%s was loaded", tc.id)
			}
		})
	}
	if p.starts.Load() != starts {
		t.Error("a rejected edit started a component")
	}
	if r := config.Check(cs.Current().Layers, config.CheckOptions{Catalog: p.cat}); !r.OK() {
		t.Errorf("a disabled entry without config was resolved: %v", r.Issues)
	}
}

// The loader's notices of a reload or an edit are published after the service's
// locks are released, and once the service has made the applied snapshot Current:
// a lifecycle or Reloaded listener sees the tree it is told about (its entries,
// owners and revision, nothing pending) and may reload and edit without
// deadlocking.
func TestReloadListenerMayCallTheService(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	root, ldr, cs := bootRoot(t, h, p, config.ServiceOptions{})
	var mu sync.Mutex
	var seen, heard []string
	var nested []error
	// consistent describes what is wrong with Current as a listener sees it.
	consistent := func() string {
		cur := cs.Current()
		var ids []string
		for _, e := range cur.Report.Entries() {
			ids = append(ids, e.ID)
		}
		ex, err := cur.Report.Explain("probe", "config.level")
		switch {
		case cs.Pending() || cur.Revision != cs.Revision():
			return fmt.Sprintf("Current %s while %s is pending", short(cur.Revision), short(cs.Revision()))
		case !slices.Contains(ids, "fresh"):
			return fmt.Sprintf("Current lacks the inserted entry: %v", ids)
		case err != nil || ex.Layer != "user" || string(ex.Value) != `"warn"`:
			return fmt.Sprintf("Current owns probe.config.level as %+v (%v), want warn from user", ex, err)
		}
		if got := effective(t, ldr, "probe"); got.Level != "warn" {
			return fmt.Sprintf("probe runs %+v", got)
		}
		return ""
	}
	remove := ldr.OnLifecycle(func(ev loader.LifecycleEvent) {
		if ev.Kind != loader.EntryUpdated {
			return
		}
		bad := consistent()
		rev := cs.Revision()
		err := cs.Reload()
		_, aerr := cs.Apply(context.Background(), config.Edit{Revision: "old", Ops: nil})
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, rev+bad)
		nested = append(nested, err, aerr)
	})
	t.Cleanup(remove)
	if _, err := loader.Reloaded.On(root, func(_ *cordis.Context, _ loader.ReloadNotice) error {
		bad := consistent()
		mu.Lock()
		defer mu.Unlock()
		heard = append(heard, bad)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	run := func(what string, fn func() error) {
		t.Helper()
		done := make(chan error, 1)
		var wg sync.WaitGroup
		t.Cleanup(wg.Wait)
		wg.Add(1)
		go func() {
			defer wg.Done()
			done <- fn()
		}()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("a listener calling the service deadlocked the %s", what)
		}
	}
	write(t, profile.UserLayerPath(h.root), "- id: probe\n  merge:\n    level: warn\n- insert:\n    - id: fresh\n      name: x.probe\n")
	run("reload", cs.Reload)
	run("edit", func() error {
		_, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{{Kind: loader.OpMerge, ID: "other", Patch: json.RawMessage(`{"port":7}`)}}})
		return err
	})
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[1] != cs.Revision() {
		t.Fatalf("listener saw %q, want 2 consistent revisions ending at %s", seen, cs.Revision())
	}
	if seen[0] == seen[1] {
		t.Error("the listener saw the snapshot from before the edit")
	}
	if len(heard) != 2 || heard[0] != "" || heard[1] != "" {
		t.Errorf("Reloaded listener saw %q, want 2 consistent notices", heard)
	}
	for i := 0; i < len(nested); i += 2 {
		if nested[i] != nil || !hasReason(nested[i+1], config.ReasonStale) {
			t.Errorf("nested reload = %v, edit = %v", nested[i], nested[i+1])
		}
	}
	if got := effective(t, ldr, "probe"); got.Level != "warn" {
		t.Errorf("probe = %+v", got)
	}
}

func short(rev string) string { return rev[:min(len(rev), 8)] }

// The edit path itself refuses an edit a higher layer shadows, whoever calls it,
// and writes nothing; a field no higher layer sets is written.
func TestApplyRefusesShadowedEdit(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	write(t, profile.UserLayerPath(h.root), "- id: probe\n  merge:\n    level: warn\n")
	cs, err := config.NewService(config.ServiceOptions{Stack: h.stack, Catalog: p.cat})
	if err != nil {
		t.Fatal(err)
	}
	path := profile.LayerPath(h.root, h.def.Name)
	before, _ := os.ReadFile(path)
	shadowed := []loader.Op{
		{Kind: loader.OpMerge, ID: "probe", Patch: json.RawMessage(`{"level":"debug"}`)},
		{Kind: loader.OpMerge, ID: "probe", Patch: json.RawMessage(`{"level":null}`)},
		// Replacing the whole config does not take effect where the user merges level.
		{Kind: loader.OpOverride, ID: "probe", Set: map[string]json.RawMessage{"config": json.RawMessage(`{"level":"debug"}`)}},
	}
	for _, op := range shadowed {
		_, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{op}})
		var se *config.ShadowedError
		if !hasReason(err, config.ReasonShadowed) || !errors.As(err, &se) || len(se.Fields) != 1 ||
			se.Fields[0] != (config.Shadow{Field: "probe.config.level", Layer: "user"}) {
			t.Fatalf("edit %s %s %s = %v, want shadowed by user", op.Kind, op.Patch, op.Set["config"], err)
		}
		if _, err := cs.Candidate(config.Edit{Revision: cs.Revision(), Ops: []loader.Op{op}}); !hasReason(err, config.ReasonShadowed) {
			t.Errorf("candidate = %v, want shadowed", err)
		}
		if after, _ := os.ReadFile(path); string(after) != string(before) {
			t.Fatalf("a shadowed edit changed the file:\n%s", after)
		}
	}
	if _, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{{Kind: loader.OpMerge, ID: "probe", Patch: json.RawMessage(`{"port":4}`)}}}); err != nil {
		t.Fatalf("unshadowed edit: %v", err)
	}
}

// Effective is the config the component runs with: its defaults and evaluated
// expressions from the running fiber, and, without one, the literal config resolved
// through the component's schema.
func TestEffectiveIsTheResolvedConfig(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	ldr, cs := boot(t, h, p)
	if got, ok := cs.Effective("probe"); !ok || string(got) != `{"level":"info"}` {
		t.Errorf("running effective = %s, %v", got, ok)
	}
	if got := effective(t, ldr, "probe"); got.Level != "info" {
		t.Errorf("probe = %+v", got)
	}
	second, err := config.NewService(config.ServiceOptions{Stack: h.stack, Catalog: p.cat})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := second.Effective("other"); !ok || string(got) != `{"port":1}` {
		t.Errorf("resolved effective = %s, %v", got, ok)
	}
	if _, ok := second.Effective("nobody"); ok {
		t.Error("an unknown entry has an effective config")
	}
}

// A candidate that removes or disables a required entry, or a group enclosing one,
// is rejected with the operation's position, by a reload and by an edit, before
// anything is written or applied (DH-ADR-BOOT-6, boot.md "Required entries").
func TestRequiredEntriesSurviveReloadAndEdit(t *testing.T) {
	const text = profileText + `- insert:
    - id: core
      group: true
      children:
        - id: keep
          name: x.probe
          config:
            level: info
`
	keep := "core" + loader.Separator + "keep"
	start := func(t *testing.T) (*home, *probes, *loader.Service, *config.Service) {
		h, p := newHome(t), newProbes(t)
		write(t, profile.LayerPath(h.root, h.def.Name), text)
		ldr, cs := bootWith(t, h, p, config.ServiceOptions{Required: []string{"probe", keep}, Evaluator: literals{}})
		return h, p, ldr, cs
	}
	unchanged := func(t *testing.T, p *probes, ldr *loader.Service, cs *config.Service, starts int64, rev string) {
		t.Helper()
		for _, id := range []string{"probe", keep} {
			if e := ldr.Find(id); e == nil || !ldr.Active(e) {
				t.Errorf("required %s is no longer active", id)
			}
		}
		if p.starts.Load() != starts || cs.Revision() != rev {
			t.Errorf("the running tree changed: starts %d -> %d", starts, p.starts.Load())
		}
	}
	cases := map[string]struct {
		text, at, path string
		op             loader.Op
	}{
		"disable":         {"- id: probe\n  disabled: true\n", ":2:", "probe.disabled", loader.Op{Kind: loader.OpOverride, ID: "probe", Set: map[string]json.RawMessage{"disabled": json.RawMessage(`true`)}}},
		"remove":          {"- id: probe\n  remove: true\n", ":1:", "probe", loader.Op{Kind: loader.OpRemove, ID: "probe"}},
		"disable group":   {"- id: core\n  disabled: true\n", ":2:", keep + ".disabled", loader.Op{Kind: loader.OpOverride, ID: "core", Set: map[string]json.RawMessage{"disabled": json.RawMessage(`true`)}}},
		"remove group":    {"- id: core\n  remove: true\n", ":1:", keep, loader.Op{Kind: loader.OpRemove, ID: "core"}},
		"disable by expr": {"- id: probe\n  disabled: !expr 'true'\n", ":2:", "probe.disabled", loader.Op{}},
		"failing expr":    {"- id: probe\n  disabled: !expr 'broken'\n", ":2:", "probe.disabled", loader.Op{}},
		"non-bool expr":   {"- id: probe\n  disabled: !expr 'word'\n", ":2:", "probe.disabled", loader.Op{}},
		"group by expr":   {"- id: core\n  disabled: !expr 'true'\n", ":2:", keep + ".disabled", loader.Op{}},
	}
	for name, tc := range cases {
		t.Run("reload/"+name, func(t *testing.T) {
			h, p, ldr, cs := start(t)
			starts, rev := p.starts.Load(), cs.Revision()
			user := profile.UserLayerPath(h.root)
			write(t, user, tc.text)
			err := cs.Reload()
			if !hasReason(err, config.ReasonRejected) || !strings.Contains(err.Error(), "required") ||
				!strings.Contains(err.Error(), user+tc.at) || !strings.Contains(err.Error(), tc.path) {
				t.Fatalf("reload = %v, want rejected at %s%s %s", err, user, tc.at, tc.path)
			}
			unchanged(t, p, ldr, cs, starts, rev)
		})
		if tc.op.Kind == "" {
			continue
		}
		t.Run("edit/"+name, func(t *testing.T) {
			h, p, ldr, cs := start(t)
			starts, rev := p.starts.Load(), cs.Revision()
			path := profile.LayerPath(h.root, h.def.Name)
			before, _ := os.ReadFile(path)
			_, err := cs.Apply(context.Background(), config.Edit{Revision: rev, Ops: []loader.Op{tc.op}})
			if !hasReason(err, config.ReasonRejected) || !strings.Contains(err.Error(), "required") {
				t.Fatalf("edit = %v, want rejected as required", err)
			}
			if after, _ := os.ReadFile(path); string(after) != string(before) {
				t.Errorf("a rejected edit changed the file:\n%s", after)
			}
			unchanged(t, p, ldr, cs, starts, rev)
		})
	}
	// A disabled expression that evaluates to false leaves a required entry, or its
	// group, enabled: the reload is accepted and both keep running.
	for _, text := range []string{"- id: probe\n  disabled: !expr 'false'\n", "- id: core\n  disabled: !expr 'false'\n"} {
		t.Run("reload/false expr "+strings.Fields(text)[2], func(t *testing.T) {
			h, _, ldr, cs := start(t)
			rev := cs.Revision()
			write(t, profile.UserLayerPath(h.root), text)
			if err := cs.Reload(); err != nil {
				t.Fatalf("reload = %v, want accepted", err)
			}
			if cs.Revision() == rev || cs.Pending() {
				t.Errorf("revision %s -> %s, pending %v: the reload was not applied", rev, cs.Revision(), cs.Pending())
			}
			for _, id := range []string{"probe", keep} {
				if e := ldr.Find(id); e == nil || !ldr.Active(e) {
					t.Errorf("required %s is no longer active", id)
				}
			}
		})
	}
	// Disabling an entry no one requires is still an edit like any other.
	_, _, ldr, cs := start(t)
	if _, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{{Kind: loader.OpOverride, ID: "other", Set: map[string]json.RawMessage{"disabled": json.RawMessage(`true`)}}}}); err != nil {
		t.Fatalf("disabling a non-required entry: %v", err)
	}
	if e := ldr.Find("other"); e != nil && ldr.Active(e) {
		t.Error("other is still active")
	}
}

// A reload whose application is stuck (a cleanup that does not return) does not
// let a concurrent edit report success early: the edit is accepted and written,
// but neither call returns, and the edit stays Pending, until the stuck
// application settles; then both are effective.
func TestEditDuringStuckApplicationWaitsForItToSettle(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	write(t, profile.LayerPath(h.root, h.def.Name), profileText+"- insert:\n    - id: hold\n      name: x.hold\n")
	ldr, cs := boot(t, h, p)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	var once sync.Once
	release := func() { once.Do(func() { close(p.gate) }) }
	t.Cleanup(release)
	start := func(fn func() error) chan error {
		done := make(chan error, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			done <- fn()
		}()
		return done
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		deadline := time.After(10 * time.Second)
		for !ok() {
			select {
			case <-tick.C:
			case <-deadline:
				t.Fatalf("timed out waiting for %s", what)
			}
		}
	}

	write(t, profile.UserLayerPath(h.root), "- id: hold\n  remove: true\n")
	reload := start(cs.Reload)
	waitFor("the reload to be accepted", cs.Pending)
	edit := start(func() error {
		_, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{{Kind: loader.OpMerge, ID: "other", Patch: json.RawMessage(`{"port":7}`)}}})
		return err
	})
	waitFor("the edit to be written", func() bool {
		data, _ := os.ReadFile(profile.LayerPath(h.root, h.def.Name))
		return strings.Contains(string(data), "port: 7")
	})
	select {
	case err := <-edit:
		t.Fatalf("the edit returned %v while the reload before it was still applying", err)
	case err := <-reload:
		t.Fatalf("the reload returned %v while its cleanup was stuck", err)
	case <-time.After(200 * time.Millisecond):
	}
	if !cs.Pending() || cs.Current().Revision == cs.Revision() {
		t.Error("an accepted, unsettled edit is reported effective")
	}

	release()
	for what, done := range map[string]chan error{"reload": reload, "edit": edit} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the %s never returned after the cleanup was released", what)
		}
	}
	if cs.Pending() || cs.Current().Revision != cs.Revision() {
		t.Error("both settled, yet the service reports a pending tree")
	}
	if got := effective(t, ldr, "other"); got.Port != 7 {
		t.Errorf("other = %+v, want the edit's port 7", got)
	}
	if ldr.Find("hold") != nil {
		t.Error("hold is still in the running tree")
	}
}

// A listener told of a reload may make a valid edit of its own: the nested edit is
// applied (not dropped or reported early) and the outer reload returns once the
// newer tree has settled.
func TestReloadListenerMayEditValidly(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	ldr, cs := boot(t, h, p)
	var fired atomic.Bool
	var nested error
	remove := ldr.OnLifecycle(func(ev loader.LifecycleEvent) {
		if ev.Kind != loader.EntryUpdated || !fired.CompareAndSwap(false, true) {
			return
		}
		_, nested = cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{{Kind: loader.OpMerge, ID: "other", Patch: json.RawMessage(`{"port":9}`)}}})
		if got := effective(t, ldr, "other"); nested == nil && got.Port != 9 {
			t.Errorf("the nested edit returned before it applied: other = %+v", got)
		}
	})
	t.Cleanup(remove)
	write(t, profile.UserLayerPath(h.root), "- id: probe\n  merge:\n    level: warn\n")
	if err := cs.Reload(); err != nil {
		t.Fatal(err)
	}
	if nested != nil {
		t.Fatalf("nested edit: %v", nested)
	}
	if got := effective(t, ldr, "other"); got.Port != 9 {
		t.Errorf("other = %+v, want the nested edit's port 9", got)
	}
	if got := effective(t, ldr, "probe"); got.Level != "warn" {
		t.Errorf("probe = %+v, want the reload's warn", got)
	}
	if cs.Pending() {
		t.Error("the service reports a pending tree after both settled")
	}
}

// Reload A is paused in a consumer's lifecycle callback while edit B, made from
// another goroutine, inserts a component whose constructor blocks. When A resumes
// and emits Reloaded, B must not become Current: B stays pending, and both calls
// wait, until B's constructor completes.
func TestOverlappingApplicationStaysPendingUntilItSettles(t *testing.T) {
	h, p := newHome(t), newProbes(t)
	root, ldr, cs := bootRoot(t, h, p, config.ServiceOptions{})
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	var built sync.Once
	finish := func() { built.Do(func() { close(p.build) }) }
	t.Cleanup(finish)
	paused, resume := make(chan string, 1), make(chan struct{})
	var resumed sync.Once
	t.Cleanup(func() { resumed.Do(func() { close(resume) }) })
	var first, told atomic.Bool
	var pendingAfter atomic.Value
	remove := ldr.OnLifecycle(func(ev loader.LifecycleEvent) {
		if ev.Kind != loader.EntryUpdated || !first.CompareAndSwap(false, true) {
			return
		}
		paused <- cs.Current().Revision
		<-resume
	})
	t.Cleanup(remove)
	// A's Reloaded notice comes after its lifecycle events, once A resumes; the
	// service hears it before this listener
	if _, err := loader.Reloaded.On(root, func(_ *cordis.Context, _ loader.ReloadNotice) error {
		if told.CompareAndSwap(false, true) {
			pendingAfter.Store(cs.Pending())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	start := func(fn func() error) chan error {
		done := make(chan error, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			done <- fn()
		}()
		return done
	}
	within := func(what string, ch <-chan struct{}) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for %s", what)
		}
	}

	write(t, profile.UserLayerPath(h.root), "- id: probe\n  merge:\n    level: warn\n")
	reload := start(cs.Reload)
	var revA string
	select {
	case revA = <-paused:
	case <-time.After(10 * time.Second):
		t.Fatal("reload A was never published")
	}
	edit := start(func() error {
		_, err := cs.Apply(context.Background(), config.Edit{Revision: cs.Revision(), Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{{ID: "slow", Name: "x.slow"}}}}})
		return err
	})
	within("B's constructor to start", p.building)
	revB := cs.Revision()
	if revB == revA {
		t.Fatal("edit B was not accepted")
	}
	resumed.Do(func() { close(resume) })

	select {
	case err := <-reload:
		t.Fatalf("reload A returned %v while the newer B was still constructing", err)
	case err := <-edit:
		t.Fatalf("edit B returned %v while its constructor was blocked", err)
	case <-time.After(200 * time.Millisecond):
	}
	if v, _ := pendingAfter.Load().(bool); !v {
		t.Error("A's resumed publication reported B effective while B's constructor was blocked")
	}
	if !cs.Pending() || cs.Current().Revision != revA {
		t.Errorf("Current = %s, Pending = %v; want A (%s) current and B (%s) pending", short(cs.Current().Revision), cs.Pending(), short(revA), short(revB))
	}

	finish()
	for what, done := range map[string]chan error{"reload": reload, "edit": edit} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("%s: %v", what, err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the %s never returned after B's constructor finished", what)
		}
	}
	if cs.Pending() || cs.Current().Revision != revB {
		t.Errorf("Current = %s, Pending = %v after B settled; want B (%s)", short(cs.Current().Revision), cs.Pending(), short(revB))
	}
	if ldr.Find("slow") == nil {
		t.Error("slow is not in the running tree")
	}
}
