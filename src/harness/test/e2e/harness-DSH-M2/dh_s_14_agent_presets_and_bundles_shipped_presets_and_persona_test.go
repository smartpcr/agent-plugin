//go:build e2e

package e2e

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
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset/persona"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
	fstools "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	shtools "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/tools"
)

// spspDirs is every session's working directory.
type spspDirs struct{ root string }

func (d spspDirs) Access(session.ID) (workdir.Access, error) { return d, nil }
func (d spspDirs) Current() (workdir.WorkingDirectory, error) {
	return workdir.WorkingDirectory{Path: d.root, Root: d.root}, nil
}
func (d spspDirs) Path() (string, error) { return d.root, nil }
func (d spspDirs) Root() (string, error) { return d.root, nil }

type spspMaint struct{}

func (spspMaint) Maintain(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type spspIn struct {
	Q string `json:"q"`
}

type spspOut struct{}

// spspFake registers tools named names under the catalogue name of a real tool set,
// with that set's config type, so a shipped preset's child configs are checked as
// they would be in a release.
func spspFake[C any](name string, names ...string) catalog.Item {
	c := cordis.Define(name, func(ctx *cordis.Context, _ C) error {
		svc, err := htools.Key.Get(ctx)
		if err != nil {
			return err
		}
		var ds []*htools.Declaration
		for _, n := range names {
			ds = append(ds, htools.Define(htools.Spec[spspIn, spspOut]{
				Name: n, Description: "Fake tool " + n + ".",
				Run: func(context.Context, spspIn, htools.Run) (spspOut, error) { return spspOut{}, nil },
			}))
		}
		_, err = svc.RegisterSet(ctx, ds)
		return err
	}, cordis.WithInject(htools.Key.Name()))
	return catalog.Item{Name: name, Component: c, Config: reflect.TypeFor[C]()}
}

type spspWorld struct {
	ctx      context.Context
	cleanups []func()

	// Persona outside a preset.
	base    string
	homeDir string
	overlay string
	status  int
	stderr  string

	// Registry rig.
	root   *cordis.Context
	cat    *catalog.Catalog
	tab    scope.Table
	tools  htools.Service
	prompt *prompt.Service
	reg    *agentpreset.Registry
	fold   *views.Registry
	view   views.Handle[agentpreset.Binding]
	sess   session.Service

	std       agentpreset.Declaration
	agent     *scope.Scope
	handle    session.Handle
	info      agentpreset.Info
	fixErr    error
	fixIssues []string

	// Real constructors.
	real    bool
	ops     *ops.Service
	shells  *shellexec.Registry
	res     sandbox.Resolver
	wd      spspDirs
	dbPath  string
	st      *sqlite.Store
	session session.ID
}

func (w *spspWorld) defer_(f func()) { w.cleanups = append(w.cleanups, f) }

func (w *spspWorld) cleanup() {
	for i := len(w.cleanups) - 1; i >= 0; i-- {
		w.cleanups[i]()
	}
}

func (w *spspWorld) tempDir(prefix string) (string, error) {
	d, err := os.MkdirTemp("", prefix)
	if err != nil {
		return "", err
	}
	if r, err := filepath.EvalSymlinks(d); err == nil {
		d = r
	}
	w.defer_(func() { _ = os.RemoveAll(d) })
	return d, nil
}

func spspNames(list string) []string { return strings.Fields(list) }

func spspSorted(ns []string) []string { return slices.Sorted(slices.Values(ns)) }

// ---- Scenario: Persona outside a preset refused ----

func (w *spspWorld) topLevelPersonaLayer(component, id string) error {
	base, err := w.tempDir("spsp-boot-")
	if err != nil {
		return err
	}
	w.base = base
	w.homeDir = filepath.Join(base, "home")
	w.overlay = filepath.Join(base, "overlay.yml")
	layer := fmt.Sprintf("- insert:\n    - id: %s\n      name: %s\n      config:\n        prefix: You are a test.\n", id, component)
	return os.WriteFile(w.overlay, []byte(layer), 0o600)
}

func (w *spspWorld) bootLoads() error {
	// The real persona component is in the catalogue: the refusal must come from the
	// whole-tree rule, not from an unknown component name. The build catalogue
	// holds the preset registry and declaration items already (DH-D-BUNDLE-3).
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{persona.Item()})...)
	if err != nil {
		return fmt.Errorf("build catalogue: %w", err)
	}
	def := profile.Definition{Name: "probe", Bundles: []string{profile.Admin, profile.Core}}
	var stderr testkit.Buffer
	w.status = boot.Run(w.ctx, boot.Options{
		Request: launch.Request{Args: []string{"dsh", "probe"}, Cwd: w.base, Home: w.homeDir, Profile: def.Name, Overlays: []string{w.overlay}},
		Profile: def,
		Catalog: cat,
		Stderr:  &stderr,
	})
	w.stderr = stderr.String()
	return nil
}

func (w *spspWorld) bootFailsConfig() error {
	if w.status != output.Config {
		return fmt.Errorf("status = %d, want %d (config); stderr:\n%s", w.status, output.Config, w.stderr)
	}
	if n := strings.Count(w.stderr, "dsh: fatal:"); n != 1 {
		return fmt.Errorf("%d fatal messages, want 1:\n%s", n, w.stderr)
	}
	return nil
}

func (w *spspWorld) fatalNames(path, name string) error {
	if !strings.Contains(w.stderr, path) || !strings.Contains(w.stderr, name) {
		return fmt.Errorf("the fatal message does not name %s and %s:\n%s", path, name, w.stderr)
	}
	return nil
}

func (w *spspWorld) fatalSaysChildOf(decl string) error {
	if !strings.Contains(w.stderr, agentpreset.PersonaOutsidePreset) || !strings.Contains(w.stderr, decl) {
		return fmt.Errorf("the fatal message lacks %q:\n%s", agentpreset.PersonaOutsidePreset, w.stderr)
	}
	return nil
}

// ---- Registry rig ----

func (w *spspWorld) shippedPreset(id string) error {
	if !slices.Contains(agentpreset.ShippedNames(), id) {
		return fmt.Errorf("shipped presets %v lack %q", agentpreset.ShippedNames(), id)
	}
	d, err := agentpreset.Shipped(id)
	if err != nil {
		return err
	}
	if d.ID != id {
		return fmt.Errorf("shipped %q declares id %q", id, d.ID)
	}
	w.std = d
	return nil
}

func (w *spspWorld) use(c *cordis.Component, cfg any) error {
	f, err := w.root.Use(c, cfg)
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		return fmt.Errorf("%s: %w", c.Name(), err)
	}
	return nil
}

// rig mounts the scope table, the tool registry and the prompt service on a fresh
// root, then the preset registry over a catalogue of items, logging to st.
func (w *spspWorld) rig(st store.Store, items []catalog.Item, provide func(*cordis.Context) error) error {
	w.root = cordis.New()
	w.root.Loggers().ClearExporters()
	w.root.Loggers().AddExporter(&cordis.MemoryExporter{})
	w.defer_(w.root.Dispose)
	for _, m := range []struct {
		c   *cordis.Component
		cfg any
	}{{scope.Component, cordis.None{}}, {core.Component, cordis.None{}}, {prompt.Component, prompt.Defaults()}} {
		if err := w.use(m.c, m.cfg); err != nil {
			return err
		}
	}
	g, err := w.root.Inject([]string{scope.TableKey.Name(), htools.Key.Name(), prompt.Key.Name()}, func(c *cordis.Context) error {
		var err error
		if w.tab, err = scope.TableKey.Get(c); err != nil {
			return err
		}
		if w.tools, err = htools.Key.Get(c); err != nil {
			return err
		}
		w.prompt, err = prompt.Key.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		return fmt.Errorf("inject services: %w", err)
	}
	if provide != nil {
		if err := provide(w.root); err != nil {
			return err
		}
	}
	w.cat, err = catalog.Build(append([]catalog.Item{persona.Item()}, items...),
		agentpreset.Items(func() agentpreset.Host { return agentpreset.Host{Catalogue: w.cat, Version: "e2e"} }))
	if err != nil {
		return fmt.Errorf("build catalogue: %w", err)
	}
	if w.reg, err = agentpreset.New(w.root, agentpreset.Options{Catalogue: w.cat, Scopes: w.tab, Version: "e2e"}); err != nil {
		return err
	}
	w.fold = views.New()
	if w.view, err = agentpreset.RegisterView(w.fold); err != nil {
		return err
	}
	w.sess = session.NewService(w.root, st, session.Config{Folder: w.fold})
	return nil
}

func (w *spspWorld) fakeRegistry(list string) error {
	want := spspNames(list)
	fsNames := []string{fstools.ReadName, fstools.WriteName, fstools.EditName, fstools.ApplyPatchName, fstools.GlobName, fstools.GrepName}
	shNames := []string{shtools.BashName, shtools.PwshName}
	if got := append(slices.Clone(fsNames), shNames...); !reflect.DeepEqual(spspSorted(got), spspSorted(want)) {
		return fmt.Errorf("the real tool names are %v, the scenario lists %v", got, want)
	}
	return w.rig(memstore.New(), []catalog.Item{
		spspFake[fstools.Config](fstools.Name, fsNames...),
		spspFake[shtools.OneShotConfig](shtools.Name, shNames...),
	}, nil)
}

// bind loads decls with def the default and binds a new agent to preset id.
func (w *spspWorld) bind(def, id string, decls ...agentpreset.Declaration) (*scope.Scope, session.Handle, agentpreset.Info, error) {
	if err := w.reg.Load(agentpreset.Config{Default: def}, decls); err != nil {
		return nil, nil, agentpreset.Info{}, fmt.Errorf("load: %w", err)
	}
	w.session = session.ID("s-spsp-e2e")
	h, err := w.sess.Create(w.ctx, nil, session.CreateOptions{Header: session.Header{ID: string(w.session)}})
	if err != nil {
		return nil, nil, agentpreset.Info{}, err
	}
	w.defer_(func() { _ = h.Close(context.Background()) })
	sc, err := w.tab.New(w.root, "agent")
	if err != nil {
		return nil, nil, agentpreset.Info{}, err
	}
	info, err := w.reg.Bind(w.ctx, agentpreset.Target{Session: h, Scope: sc, Owner: sc.Ctx(), Maintainer: spspMaint{}}, id, agentpreset.CauseCreate)
	return sc, h, info, err
}

func (w *spspWorld) resolveStandard(id string) error {
	var err error
	w.agent, w.handle, w.info, err = w.bind(id, id, w.std)
	if err != nil {
		return fmt.Errorf("bind %s: %w", id, err)
	}
	in, err := w.reg.Resolve(id)
	if err != nil {
		return err
	}
	if in.Broken != "" {
		return fmt.Errorf("%s is broken: %s", id, in.Broken)
	}
	return nil
}

func (w *spspWorld) noIssues() error {
	if is := agentpreset.Validate(agentpreset.Config{Default: w.std.ID}, []agentpreset.Declaration{w.std}, w.cat); len(is) > 0 {
		return fmt.Errorf("standard.yml has issues: %v", is)
	}
	return nil
}

func (w *spspWorld) toolsAt(k scope.Key) ([]*htools.Declaration, error) {
	v, err := w.tools.Resolve(w.ctx, htools.Target{Scope: k})
	if err != nil {
		return nil, err
	}
	defer v.Release()
	return v.Tools(), nil
}

func (w *spspWorld) agentHasExactly(list string) error {
	ds, err := w.toolsAt(w.agent.Key())
	if err != nil {
		return err
	}
	var got []string
	for _, d := range ds {
		got = append(got, d.Name())
	}
	if want := spspNames(list); !reflect.DeepEqual(spspSorted(got), spspSorted(want)) {
		return fmt.Errorf("the agent's tools = %v, want exactly %v", got, want)
	}
	return nil
}

func (w *spspWorld) personaPrefix(want string) error {
	v, err := w.tools.Resolve(w.ctx, htools.Target{Scope: w.agent.Key()})
	if err != nil {
		return err
	}
	defer v.Release()
	a, err := w.prompt.Assemble(w.ctx, prompt.Input{View: v})
	if err != nil {
		return err
	}
	for _, s := range a.Sections {
		if s.Name == prompt.SectionPersonaPrefix {
			if !strings.HasPrefix(s.Text, want) {
				return fmt.Errorf("persona prefix = %q, want it to start with %q", s.Text, want)
			}
			return nil
		}
	}
	return fmt.Errorf("the assembly has no %s section", prompt.SectionPersonaPrefix)
}

func (w *spspWorld) unboundHasNoTools() error {
	sc, err := w.tab.New(w.root, "unbound")
	if err != nil {
		return err
	}
	ds, err := w.toolsAt(sc.Key())
	if err != nil {
		return err
	}
	if len(ds) != 0 {
		var got []string
		for _, d := range ds {
			got = append(got, d.Name())
		}
		return fmt.Errorf("an unbound agent sees %v; the preset's tools leaked out of its revision", got)
	}
	return nil
}

func (w *spspWorld) fixturePreset(id, a, b string) error {
	text := fmt.Sprintf("id: %s\nchildren:\n  %s: {}\n  %s: {}\n", id, a, b)
	d, err := agentpreset.ParseDeclaration("fixture.yml", []byte(text), id)
	if err != nil {
		return fmt.Errorf("parse fixture: %w", err)
	}
	w.fixIssues = nil
	for _, is := range agentpreset.Validate(agentpreset.Config{Default: id}, []agentpreset.Declaration{d}, w.cat) {
		w.fixIssues = append(w.fixIssues, is.String())
	}
	w.fixErr = w.reg.Load(agentpreset.Config{Default: id}, []agentpreset.Declaration{d})
	return nil
}

func (w *spspWorld) fixtureRefused(name string) error {
	if len(w.fixIssues) != 1 || !strings.Contains(w.fixIssues[0], name) {
		return fmt.Errorf("issues = %v, want exactly one naming %s", w.fixIssues, name)
	}
	if w.fixErr == nil || !strings.Contains(w.fixErr.Error(), name) {
		return fmt.Errorf("load = %v, want a refusal naming %s", w.fixErr, name)
	}
	return nil
}

func (w *spspWorld) refusalNotNaming(name string) error {
	if strings.Contains(w.fixIssues[0], name) {
		return fmt.Errorf("the issue blames the known child %s: %s", name, w.fixIssues[0])
	}
	return nil
}

// ---- Scenario: Standard preset composes an agent with the real tools ----

func (w *spspWorld) realComponents(fsName, shName string) error {
	if fsName != fstools.Name || shName != shtools.Name {
		return fmt.Errorf("components %s, %s are not %s, %s", fsName, shName, fstools.Name, shtools.Name)
	}
	ws, err := w.tempDir("spsp-ws-")
	if err != nil {
		return err
	}
	w.wd = spspDirs{ws}
	w.res = &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: w.wd}
	w.shells = shellexec.NewRegistry()
	w.real = true
	return nil
}

func (w *spspWorld) embeddedSQLite() error {
	dir, err := w.tempDir("spsp-sqlite-")
	if err != nil {
		return err
	}
	w.dbPath = filepath.Join(dir, "sessions.db")
	if w.st, err = sqlite.Open(w.ctx, w.dbPath); err != nil {
		return fmt.Errorf("open embedded sqlite: %w", err)
	}
	st := w.st
	w.defer_(func() { _ = st.Close(context.Background()) })
	return w.rig(w.st, []catalog.Item{fstools.Item(), shtools.Item()}, func(root *cordis.Context) error {
		w.ops = ops.New(root, local.New(), w.res, ops.Options{Workdir: w.wd})
		if _, err := ops.Key.Provide(root, w.ops); err != nil {
			return err
		}
		if _, err := shellexec.Key.Provide(root, w.shells); err != nil {
			return err
		}
		if _, err := sandbox.ResolverKey.Provide(root, w.res); err != nil {
			return err
		}
		_, err := workdir.Key.Provide(root, workdir.Lookup(w.wd))
		return err
	})
}

func (w *spspWorld) agentCreated(id string) error {
	if !w.real || w.st == nil {
		return errors.New("the real tools and the SQLite store are not set up")
	}
	if err := w.noIssues(); err != nil {
		return err
	}
	return w.resolveStandard(id)
}

func spspSchema(d *htools.Declaration) (string, error) {
	b, err := json.Marshal(d.Schema())
	return string(b), err
}

func (w *spspWorld) realDeclarations() error {
	on := true
	fsDs, err := fstools.Declarations(w.ops, nil, fstools.Config{ApplyPatch: &on})
	if err != nil {
		return err
	}
	shDs, err := shtools.Declarations(shtools.Deps{Shells: w.shells, Resolver: w.res, Workdir: w.wd}, shtools.OneShotConfig{})
	if err != nil {
		return err
	}
	want := map[string]*htools.Declaration{}
	for _, d := range append(fsDs, shDs...) {
		want[d.Name()] = d
	}
	got, err := w.toolsAt(w.agent.Key())
	if err != nil {
		return err
	}
	for _, d := range got {
		r, ok := want[d.Name()]
		if !ok {
			return fmt.Errorf("tool %s is not one of the real constructors'", d.Name())
		}
		gs, err := spspSchema(d)
		if err != nil {
			return err
		}
		ws, err := spspSchema(r)
		if err != nil {
			return err
		}
		if d.Description() != r.Description() || gs != ws {
			return fmt.Errorf("tool %s differs from the real constructor's declaration:\n got %s\nwant %s", d.Name(), gs, ws)
		}
	}
	return nil
}

func (w *spspWorld) registryReports(id string, rev int) error {
	in, ok := w.reg.Of(w.agent.Key())
	if !ok || in.ID != id || in.Revision != rev || in.Broken != "" {
		return fmt.Errorf("the registry reports %+v (bound %v), want %s rev %d", in, ok, id, rev)
	}
	if w.info.ID != id || w.info.Revision != rev {
		return fmt.Errorf("Bind returned %+v, want %s rev %d", w.info, id, rev)
	}
	return nil
}

func (w *spspWorld) foldOf(h session.Handle) (agentpreset.Binding, error) {
	snap, err := w.fold.Read(w.ctx, h, agentpreset.FoldKey)
	if err != nil {
		return agentpreset.Binding{}, err
	}
	return w.view.Get(snap)
}

func (w *spspWorld) foldReads(id string) error {
	b, err := w.foldOf(w.handle)
	if err != nil {
		return err
	}
	if b.Preset != id || b.Digest != w.info.Digest || b.Cause != agentpreset.CauseCreate {
		return fmt.Errorf("the fold = %+v, want %s (%s) by create", b, id, w.info.Digest)
	}
	return nil
}

func (w *spspWorld) foldPersists(id string) error {
	if err := w.handle.Close(w.ctx); err != nil {
		return fmt.Errorf("close the session: %w", err)
	}
	if err := w.st.Close(w.ctx); err != nil {
		return fmt.Errorf("close the store: %w", err)
	}
	st, err := sqlite.Open(w.ctx, w.dbPath)
	if err != nil {
		return fmt.Errorf("reopen embedded sqlite: %w", err)
	}
	w.defer_(func() { _ = st.Close(context.Background()) })
	fold := views.New()
	view, err := agentpreset.RegisterView(fold)
	if err != nil {
		return err
	}
	h, _, err := session.NewService(w.root, st, session.Config{Folder: fold}).Open(w.ctx, nil, w.session, session.OpenOptions{})
	if err != nil {
		return fmt.Errorf("reopen the session: %w", err)
	}
	w.defer_(func() { _ = h.Close(context.Background()) })
	snap, err := fold.Read(w.ctx, h, agentpreset.FoldKey)
	if err != nil {
		return err
	}
	b, err := view.Get(snap)
	if err != nil {
		return err
	}
	if b.Preset != id || b.Digest != w.info.Digest {
		return fmt.Errorf("the reopened fold = %+v, want %s (%s)", b, id, w.info.Digest)
	}
	return nil
}

func InitializeScenario_dh_s_14_agent_presets_and_bundles_shipped_presets_and_persona(ctx *godog.ScenarioContext) {
	w := &spspWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = spspWorld{ctx: context.Background()}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.cleanup()
		return c, nil
	})

	ctx.Step(`^a config layer that enables "([^"]*)" at top level as entry "([^"]*)"$`, w.topLevelPersonaLayer)
	ctx.Step(`^boot loads it$`, w.bootLoads)
	ctx.Step(`^the boot fails with the configuration status and one fatal message$`, w.bootFailsConfig)
	ctx.Step(`^the fatal message names "([^"]*)" and "([^"]*)"$`, w.fatalNames)
	ctx.Step(`^the fatal message says a persona mounts only as a child of an "([^"]*)"$`, w.fatalSaysChildOf)

	ctx.Step(`^the committed shipped preset "([^"]*)"$`, w.shippedPreset)
	ctx.Step(`^a test registry with fake tool registrations for "([^"]*)"$`, w.fakeRegistry)
	ctx.Step(`^the preset registry resolves "([^"]*)"$`, w.resolveStandard)
	ctx.Step(`^the declaration has no issues against the test catalogue$`, w.noIssues)
	ctx.Step(`^an agent bound to it has exactly the tools "([^"]*)"$`, w.agentHasExactly)
	ctx.Step(`^the agent's persona prefix starts with "([^"]*)"$`, w.personaPrefix)
	ctx.Step(`^an agent bound to no preset has no tools$`, w.unboundHasNoTools)
	ctx.Step(`^a fixture preset "([^"]*)" declares children "([^"]*)" and "([^"]*)"$`, w.fixturePreset)
	ctx.Step(`^the fixture is refused naming "([^"]*)"$`, w.fixtureRefused)
	ctx.Step(`^the refusal does not name "([^"]*)"$`, w.refusalNotNaming)

	ctx.Step(`^the real "([^"]*)" and "([^"]*)" components registered by their constructors$`, w.realComponents)
	ctx.Step(`^an embedded SQLite session store$`, w.embeddedSQLite)
	ctx.Step(`^an agent is created bound to preset "([^"]*)"$`, w.agentCreated)
	ctx.Step(`^the agent's tool list is exactly "([^"]*)"$`, w.agentHasExactly)
	ctx.Step(`^the agent's tools are the real constructors' declarations$`, w.realDeclarations)
	ctx.Step(`^the registry reports the agent bound to "([^"]*)" rev (\d+)$`, w.registryReports)
	ctx.Step(`^the session's preset fold reads "([^"]*)" by create$`, w.foldReads)
	ctx.Step(`^after reopening the SQLite store the session's preset fold still reads "([^"]*)"$`, w.foldPersists)
}

func TestE2E_dh_s_14_agent_presets_and_bundles_shipped_presets_and_persona(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_14_agent_presets_and_bundles_shipped_presets_and_persona,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_14_agent_presets_and_bundles_shipped_presets_and_persona.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
