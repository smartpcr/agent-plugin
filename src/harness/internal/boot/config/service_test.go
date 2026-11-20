package config_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

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

// probes is a catalogue of one counting component, x.probe.
type probes struct {
	cat    *catalog.Catalog
	starts atomic.Int64
}

func newProbes(t *testing.T) *probes {
	t.Helper()
	p := &probes{}
	component := cordis.Define("x.probe", func(*cordis.Context, probeConfig) error {
		p.starts.Add(1)
		return nil
	})
	cat, err := catalog.Build([]catalog.Item{{Name: "x.probe", Component: component, Config: reflect.TypeFor[probeConfig]()}})
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
	root := cordis.New(cordis.WithExporters())
	t.Cleanup(root.Dispose)
	fiber, err := root.Use(loader.Component, loader.Options{Resolver: p.cat})
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
	cs, err := config.NewService(config.ServiceOptions{Stack: h.stack, Loader: ldr, Catalog: p.cat})
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Current().Report.Err(); err != nil {
		t.Fatal(err)
	}
	if err := ldr.ApplyLayers(cs.Current().Layers); err != nil {
		t.Fatal(err)
	}
	return ldr, cs
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
