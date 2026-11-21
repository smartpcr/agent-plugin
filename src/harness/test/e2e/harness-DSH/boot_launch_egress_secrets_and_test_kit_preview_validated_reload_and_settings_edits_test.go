//go:build e2e

package e2e

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

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/settings"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// pvrProbeConfig is the config of the probe component: a level only its Validate
// refuses, and a port. Both are live settings.
type pvrProbeConfig struct {
	Level string `json:"level,omitempty" dsh:"live"`
	Port  int    `json:"port,omitempty" dsh:"live"`
}

func (c pvrProbeConfig) Validate() error {
	switch c.Level {
	case "", "debug", "info", "warn":
		return nil
	}
	return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Path: "level", Message: fmt.Sprintf("level %q is not debug, info or warn", c.Level)}}}
}

const (
	pvrBundleText = `- insert:
    - id: probe
      name: x.probe
      config:
        level: info
        port: 1
    - id: quiet
      name: x.probe
      disabled: true
`
	pvrUserText = `# the user's own choices
- id: probe
  merge:
    level: debug
`
	pvrProfileComment = "# written by dsh; comments stay"
	pvrProfileText    = pvrProfileComment + `
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
	pvrBadUserText   = "- id: other\n  merge:\n    port: 2\n- id: probe\n  merge:\n    level: loud\n"
	pvrFixedUserText = "- id: other\n  merge:\n    port: 2\n"
)

// pvrState is one scenario's world.
type pvrState struct {
	starts atomic.Int64
	cat    *catalog.Catalog

	// preview
	layers []loader.Layer
	opts   config.CheckOptions
	report *config.Report

	// home
	dir, home string
	def       profile.Definition
	stack     config.Stack

	// running tree
	root     *cordis.Context
	ldr      *loader.Service
	cs       *config.Service
	before   map[string]pvrProbeConfig
	startsAt int64
	revision string
	told     []error
	err      error

	// settings clients
	clients []*settings.Service
	edits   []pvrEdit
	results []settings.Result
	loser   int
	retry   settings.Result
}

type pvrEdit struct {
	id, path string
	value    json.RawMessage
}

func (s *pvrState) close() {
	if s.cs != nil {
		s.cs.Close()
	}
	if s.root != nil {
		s.root.Dispose()
	}
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

func (s *pvrState) catalog() error {
	probe := cordis.Define("x.probe", func(*cordis.Context, pvrProbeConfig) error {
		s.starts.Add(1)
		return nil
	})
	cat, err := catalog.Build([]catalog.Item{{Name: "x.probe", Component: probe, Config: reflect.TypeFor[pvrProbeConfig]()}})
	if err != nil {
		return err
	}
	s.cat = cat
	return nil
}

func pvrLayer(label, file, text string) (loader.Layer, *loader.LayerDocument, error) {
	l, issues, err := profile.ParseLayer(label, file, []byte(text))
	if err != nil {
		return loader.Layer{}, nil, err
	}
	if len(issues) > 0 {
		return loader.Layer{}, nil, fmt.Errorf("layer %s: %v", label, issues)
	}
	config.Register()
	doc, err := loader.ParseLayer(nil, file, []byte(text))
	if err != nil {
		return loader.Layer{}, nil, err
	}
	return l, doc, nil
}

func pvrWrite(path, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(text), 0o600)
}

// --- Preview ---------------------------------------------------------------

func (s *pvrState) layersInMemory() error {
	if err := s.catalog(); err != nil {
		return err
	}
	bundle, bdoc, err := pvrLayer("bundle:core", "core.yml", pvrBundleText)
	if err != nil {
		return err
	}
	user, udoc, err := pvrLayer("user", "user.yml", pvrUserText)
	if err != nil {
		return err
	}
	s.layers = []loader.Layer{bundle, user}
	s.opts = config.CheckOptions{Catalog: s.cat, Docs: map[string]*loader.LayerDocument{"bundle:core": bdoc, "user": udoc}}
	return nil
}

func (s *pvrState) previewRuns() error {
	s.report = config.Preview(s.layers, s.opts)
	return nil
}

func (s *pvrState) previewOK() error {
	if !s.report.OK() {
		return fmt.Errorf("preview issues: %v", s.report.Issues)
	}
	return nil
}

func (s *pvrState) previewEntries(a, b string) error {
	e := s.report.Entries()
	if len(e) != 2 || e[0].ID != a || e[1].ID != b {
		return fmt.Errorf("entries = %+v", e)
	}
	if !e[1].Disabled {
		return fmt.Errorf("entry %s is not disabled: %+v", b, e[1])
	}
	return nil
}

func (s *pvrState) entryOwned(id, layer, cfg string) error {
	for _, e := range s.report.Entries() {
		if e.ID != id {
			continue
		}
		if e.Layer != layer {
			return fmt.Errorf("%s owned by %q, want %q", id, e.Layer, layer)
		}
		if string(e.Config) != cfg {
			return fmt.Errorf("%s composed config = %s, want %s", id, e.Config, cfg)
		}
		return nil
	}
	return fmt.Errorf("no entry %s", id)
}

func (s *pvrState) fieldOwnedAt(field, id, layer, at string) error {
	ex, err := s.report.Explain(id, field)
	if err != nil {
		return err
	}
	if ex.Layer != layer || !strings.HasPrefix(ex.At, at) {
		return fmt.Errorf("explain %s.%s = %+v, want layer %s at %s", id, field, ex, layer, at)
	}
	return nil
}

func (s *pvrState) fieldOwned(field, id, layer string) error {
	ex, err := s.report.Explain(id, field)
	if err != nil {
		return err
	}
	if ex.Layer != layer {
		return fmt.Errorf("explain %s.%s = %+v, want layer %s", id, field, ex, layer)
	}
	return nil
}

func (s *pvrState) noConstruct() error {
	if n := s.starts.Load(); n != 0 {
		return fmt.Errorf("preview constructed %d components", n)
	}
	return nil
}

// --- Temp home and running tree --------------------------------------------

func (s *pvrState) tempHome(a, b string) error {
	if s.cat == nil {
		if err := s.catalog(); err != nil {
			return err
		}
	}
	dir, err := os.MkdirTemp("", "dsh-e2e-pvr-")
	if err != nil {
		return err
	}
	s.dir, s.home = dir, filepath.Join(dir, "home")
	s.def = profile.Definition{Name: "probe"}
	if !strings.Contains(pvrProfileText, "id: "+a) || !strings.Contains(pvrProfileText, "id: "+b) {
		return fmt.Errorf("profile layer does not define %s and %s", a, b)
	}
	if err := pvrWrite(profile.LayerPath(s.home, s.def.Name), pvrProfileText); err != nil {
		return err
	}
	home, def := s.home, s.def
	s.stack = func(read config.ReadFile) ([]loader.Layer, []loader.Issue, error) {
		return profile.Stack(def, profile.Sources{Home: home, Read: profile.ReadFile(read)})
	}
	return nil
}

func (s *pvrState) effective(id string) (pvrProbeConfig, error) {
	entry := s.ldr.Find(id)
	if entry == nil {
		return pvrProbeConfig{}, fmt.Errorf("no entry %s", id)
	}
	cfg, ok := s.ldr.Fiber(entry).EffectiveConfig()
	if !ok {
		return pvrProbeConfig{}, fmt.Errorf("%s holds no effective config", id)
	}
	return cfg.(pvrProbeConfig), nil
}

func (s *pvrState) bootTree() error {
	s.root = cordis.New(cordis.WithExporters())
	fiber, err := s.root.Use(loader.Component, loader.Options{Resolver: s.cat})
	if err != nil {
		return err
	}
	if err := fiber.Wait(); err != nil {
		return err
	}
	if s.ldr, err = loader.Key.Get(s.root); err != nil {
		return err
	}
	s.cs, err = config.NewService(config.ServiceOptions{Stack: s.stack, Loader: s.ldr, Catalog: s.cat, Context: s.root})
	if err != nil {
		return err
	}
	if err := s.cs.Current().Report.Err(); err != nil {
		return err
	}
	if err := s.ldr.ApplyLayers(s.cs.Current().Layers); err != nil {
		return err
	}
	s.before = map[string]pvrProbeConfig{}
	for _, id := range []string{"probe", "other"} {
		cfg, err := s.effective(id)
		if err != nil {
			return err
		}
		s.before[id] = cfg
	}
	if s.before["probe"].Level != "info" || s.before["other"].Port != 1 {
		return fmt.Errorf("booted configs = %+v", s.before)
	}
	s.startsAt, s.revision = s.starts.Load(), s.cs.Revision()
	if s.startsAt == 0 {
		return fmt.Errorf("boot constructed no component")
	}
	s.cs.OnRejected(func(err error) { s.told = append(s.told, err) })
	return nil
}

func (s *pvrState) badEditReload() error {
	if err := pvrWrite(profile.UserLayerPath(s.home), pvrBadUserText); err != nil {
		return err
	}
	s.err = s.cs.Reload()
	return nil
}

func (s *pvrState) rejectedWith(reason string) error {
	if s.err == nil {
		return fmt.Errorf("the bad edit was accepted")
	}
	if !herr.Has(s.err, herr.Reason(reason)) {
		return fmt.Errorf("reason of %v is not %s", s.err, reason)
	}
	if len(s.told) != 1 {
		return fmt.Errorf("rejection listeners told %d times, want 1", len(s.told))
	}
	return nil
}

func (s *pvrState) positioned(line int, path string) error {
	at := fmt.Sprintf("%s:%d:", profile.UserLayerPath(s.home), line)
	if msg := s.err.Error(); !strings.Contains(msg, at) || !strings.Contains(msg, path) {
		return fmt.Errorf("diagnostic not positioned at %s %s:\n%s", at, path, msg)
	}
	return nil
}

func (s *pvrState) unchanged() error {
	for id, want := range s.before {
		got, err := s.effective(id)
		if err != nil {
			return err
		}
		if got != want {
			return fmt.Errorf("%s effective config = %+v, want %+v", id, got, want)
		}
	}
	return nil
}

func (s *pvrState) notRestarted() error {
	if n := s.starts.Load(); n != s.startsAt {
		return fmt.Errorf("starts %d -> %d", s.startsAt, n)
	}
	if s.cs.Revision() != s.revision {
		return fmt.Errorf("revision changed %s -> %s", s.revision, s.cs.Revision())
	}
	return nil
}

func (s *pvrState) fixReload() error {
	if err := pvrWrite(profile.UserLayerPath(s.home), pvrFixedUserText); err != nil {
		return err
	}
	s.err = s.cs.Reload()
	return nil
}

func (s *pvrState) reloadOK(id string, port int) error {
	if s.err != nil {
		return fmt.Errorf("reload of the fixed layer: %v", s.err)
	}
	got, err := s.effective(id)
	if err != nil {
		return err
	}
	if got.Port != port {
		return fmt.Errorf("%s after the fix = %+v", id, got)
	}
	return nil
}

// --- Settings clients ------------------------------------------------------

func (s *pvrState) twoClients() error {
	for range 2 {
		cs, err := config.NewService(config.ServiceOptions{Stack: s.stack, Catalog: s.cat})
		if err != nil {
			return err
		}
		s.clients = append(s.clients, settings.New(cs, s.cat))
	}
	return nil
}

func (s *pvrState) concurrentEdits() error {
	form, err := s.clients[0].Describe()
	if err != nil {
		return err
	}
	other, err := s.clients[1].Describe()
	if err != nil {
		return err
	}
	if form.Revision != other.Revision {
		return fmt.Errorf("clients read different revisions: %s vs %s", form.Revision, other.Revision)
	}
	s.edits = []pvrEdit{
		{"probe", "config.level", json.RawMessage(`"debug"`)},
		{"other", "config.port", json.RawMessage(`9`)},
	}
	s.results = make([]settings.Result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := range s.clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			e := s.edits[i]
			s.results[i] = s.clients[i].Apply(context.Background(), form.Revision, e.id, e.path, e.value)
		}()
	}
	close(start)
	wg.Wait()
	return nil
}

func (s *pvrState) oneWinsOneStale() error {
	s.loser = -1
	applied := 0
	for i, r := range s.results {
		switch r.Status {
		case settings.Applied:
			applied++
		case settings.Stale:
			if !herr.Has(r.Err, config.ReasonStale) {
				return fmt.Errorf("client %d stale without %s: %v", i, config.ReasonStale, r.Err)
			}
			s.loser = i
		default:
			return fmt.Errorf("client %d: %s %v", i, r.Status, r.Err)
		}
	}
	if applied != 1 || s.loser < 0 {
		return fmt.Errorf("want one applied and one stale, got %+v", s.results)
	}
	return nil
}

func (s *pvrState) retryEdit() error {
	form, err := s.clients[s.loser].Describe()
	if err != nil {
		return err
	}
	e := s.edits[s.loser]
	s.retry = s.clients[s.loser].Apply(context.Background(), form.Revision, e.id, e.path, e.value)
	return nil
}

func (s *pvrState) retryApplied() error {
	if s.retry.Status != settings.Applied {
		return fmt.Errorf("retry: %s %v", s.retry.Status, s.retry.Err)
	}
	return nil
}

func (s *pvrState) readProfile() ([]byte, error) {
	return os.ReadFile(profile.LayerPath(s.home, s.def.Name))
}

func (s *pvrState) bothOnDisk(level, probe string, port int, other string) error {
	data, err := s.readProfile()
	if err != nil {
		return err
	}
	l, _, err := profile.ParseLayer("profile", "layer.yml", data)
	if err != nil {
		return err
	}
	r := config.Check([]loader.Layer{l}, config.CheckOptions{Catalog: s.cat})
	if !r.OK() {
		return fmt.Errorf("layer on disk fails the check: %v", r.Issues)
	}
	for _, c := range []struct{ id, field, want string }{
		{probe, "config.level", fmt.Sprintf("%q", level)},
		{other, "config.port", fmt.Sprint(port)},
	} {
		ex, err := r.Explain(c.id, c.field)
		if err != nil || string(ex.Value) != c.want {
			return fmt.Errorf("%s.%s on disk = %s (%v), want %s\n%s", c.id, c.field, ex.Value, err, c.want, data)
		}
	}
	return nil
}

func (s *pvrState) keepsComment() error {
	data, err := s.readProfile()
	if err != nil {
		return err
	}
	if !strings.Contains(string(data), pvrProfileComment) {
		return fmt.Errorf("the write lost the layer's comment:\n%s", data)
	}
	return nil
}

func InitializeScenario_boot_launch_egress_secrets_and_test_kit_preview_validated_reload_and_settings_edits(ctx *godog.ScenarioContext) {
	var s *pvrState
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &pvrState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s != nil {
			s.close()
		}
		return c, nil
	})

	ctx.Step(`^a bundle layer and a user layer held in memory$`, func() error { return s.layersInMemory() })
	ctx.Step(`^preview runs over the layers$`, func() error { return s.previewRuns() })
	ctx.Step(`^the preview reports no issues$`, func() error { return s.previewOK() })
	ctx.Step(`^the preview lists entries "([^"]*)" and "([^"]*)" in order$`, func(a, b string) error { return s.previewEntries(a, b) })
	ctx.Step(`^entry "([^"]*)" is owned by layer "([^"]*)" with composed config (\{.*\})$`, func(id, l, c string) error { return s.entryOwned(id, l, c) })
	ctx.Step(`^field "([^"]*)" of entry "([^"]*)" is owned by layer "([^"]*)" at "([^"]*)"$`, func(f, id, l, at string) error { return s.fieldOwnedAt(f, id, l, at) })
	ctx.Step(`^field "([^"]*)" of entry "([^"]*)" is owned by layer "([^"]*)"$`, func(f, id, l string) error { return s.fieldOwned(f, id, l) })
	ctx.Step(`^no component's construct function was called$`, func() error { return s.noConstruct() })

	ctx.Step(`^a temp home whose profile layer defines entries "([^"]*)" and "([^"]*)"$`, func(a, b string) error { return s.tempHome(a, b) })
	ctx.Step(`^a running tree booted from the home's layers$`, func() error { return s.bootTree() })
	ctx.Step(`^the user layer is rewritten with an invalid level and reload fires$`, func() error { return s.badEditReload() })
	ctx.Step(`^reload is rejected with reason "([^"]*)"$`, func(r string) error { return s.rejectedWith(r) })
	ctx.Step(`^the diagnostic names the user layer at line (\d+) and path "([^"]*)"$`, func(line int, p string) error { return s.positioned(line, p) })
	ctx.Step(`^every component's effective config is unchanged$`, func() error { return s.unchanged() })
	ctx.Step(`^no component was constructed again and the revision is unchanged$`, func() error { return s.notRestarted() })
	ctx.Step(`^the user layer is fixed and reload fires$`, func() error { return s.fixReload() })
	ctx.Step(`^reload succeeds and entry "([^"]*)" has effective port (\d+)$`, func(id string, port int) error { return s.reloadOK(id, port) })

	ctx.Step(`^two settings clients on the home$`, func() error { return s.twoClients() })
	ctx.Step(`^both clients edit different keys from the same read at once$`, func() error { return s.concurrentEdits() })
	ctx.Step(`^one edit is applied and the other is refused as stale$`, func() error { return s.oneWinsOneStale() })
	ctx.Step(`^the stale client reads again and retries its edit$`, func() error { return s.retryEdit() })
	ctx.Step(`^the retry is applied$`, func() error { return s.retryApplied() })
	ctx.Step(`^the profile layer on disk holds level "([^"]*)" for "([^"]*)" and port (\d+) for "([^"]*)"$`, func(level, probe string, port int, other string) error {
		return s.bothOnDisk(level, probe, port, other)
	})
	ctx.Step(`^the profile layer on disk keeps its comment$`, func() error { return s.keepsComment() })
}

func TestE2E_boot_launch_egress_secrets_and_test_kit_preview_validated_reload_and_settings_edits(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_boot_launch_egress_secrets_and_test_kit_preview_validated_reload_and_settings_edits,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"boot_launch_egress_secrets_and_test_kit_preview_validated_reload_and_settings_edits.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
