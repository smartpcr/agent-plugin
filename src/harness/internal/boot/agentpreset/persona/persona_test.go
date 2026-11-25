package persona_test

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset/persona"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	fstools "github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/tools"
	shtools "github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec/tools"
)

type in struct {
	Q string `json:"q"`
}

type out struct{}

// fake is a component registering tools named names under the catalogue name of a
// real tool set, with that set's config type, so a shipped preset's child configs
// are checked as they would be in a release.
func fake[C any](name string, names ...string) catalog.Item {
	c := cordis.Define(name, func(ctx *cordis.Context, _ C) error {
		svc, err := tools.Key.Get(ctx)
		if err != nil {
			return err
		}
		var ds []*tools.Declaration
		for _, n := range names {
			ds = append(ds, tools.Define(tools.Spec[in, out]{
				Name: n, Description: "Tool " + n + ".",
				Run: func(context.Context, in, tools.Run) (out, error) { return out{}, nil },
			}))
		}
		_, err = svc.RegisterSet(ctx, ds)
		return err
	}, cordis.WithInject(tools.Key.Name()))
	return catalog.Item{Name: name, Component: c, Config: reflect.TypeFor[C]()}
}

type env struct {
	root     *cordis.Context
	cat      *catalog.Catalog
	tab      scope.Table
	tools    tools.Service
	prompt   *prompt.Service
	reg      *agentpreset.Registry
	sessions session.Service
}

func use(t *testing.T, root *cordis.Context, c *cordis.Component, cfg any) {
	t.Helper()
	f, err := root.Use(c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatalf("%s: %v", c.Name(), err)
	}
}

func newEnv(t *testing.T) *env {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	use(t, root, scope.Component, cordis.None{})
	use(t, root, core.Component, cordis.None{})
	use(t, root, prompt.Component, prompt.Defaults())
	e := &env{root: root}
	g, err := root.Inject([]string{scope.TableKey.Name(), tools.Key.Name(), prompt.Key.Name()}, func(c *cordis.Context) error {
		var err error
		if e.tab, err = scope.TableKey.Get(c); err != nil {
			return err
		}
		if e.tools, err = tools.Key.Get(c); err != nil {
			return err
		}
		e.prompt, err = prompt.Key.Get(c)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	e.cat, err = catalog.Build([]catalog.Item{
		persona.Item(),
		fake[fstools.Config](fstools.Name, fstools.ReadName, fstools.WriteName, fstools.EditName,
			fstools.ApplyPatchName, fstools.GlobName, fstools.GrepName),
		fake[shtools.OneShotConfig](shtools.Name, shtools.BashName, shtools.PwshName),
	}, agentpreset.Items(func() agentpreset.Host { return agentpreset.Host{Catalogue: e.cat, Version: "test"} }))
	if err != nil {
		t.Fatal(err)
	}
	if e.reg, err = agentpreset.New(root, agentpreset.Options{Catalogue: e.cat, Scopes: e.tab, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	fold := views.New()
	if _, err := agentpreset.RegisterView(fold); err != nil {
		t.Fatal(err)
	}
	e.sessions = session.NewService(root, memstore.New(), session.Config{Folder: fold})
	return e
}

// bind loads decls with def the default and binds a new agent to preset id,
// returning the agent's scope.
func (e *env) bind(t *testing.T, def, id string, decls ...agentpreset.Declaration) (*scope.Scope, error) {
	t.Helper()
	if err := e.reg.Load(agentpreset.Config{Default: def}, decls); err != nil {
		t.Fatal(err)
	}
	h, err := e.sessions.Create(context.Background(), nil, session.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	sc, err := e.tab.New(e.root, "agent")
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.reg.Bind(context.Background(), agentpreset.Target{Session: h, Scope: sc, Owner: sc.Ctx(), Maintainer: maint{}}, id, agentpreset.CauseCreate)
	return sc, err
}

type maint struct{}

func (maint) Maintain(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

func (e *env) assemble(t *testing.T, k scope.Key) prompt.Assembly {
	t.Helper()
	v, err := e.tools.Resolve(context.Background(), tools.Target{Scope: k})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	a, err := e.prompt.Assemble(context.Background(), prompt.Input{View: v})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func sections(a prompt.Assembly) map[string]string {
	out := map[string]string{}
	for _, s := range a.Sections {
		out[s.Name] = s.Text
	}
	return out
}

func contexts(a prompt.Assembly) []string {
	var out []string
	for _, s := range a.Contexts {
		out = append(out, s.Name)
	}
	return out
}

func preset(id, cfg string) agentpreset.Declaration {
	return agentpreset.Declaration{ID: id, Children: map[string]*agentpreset.Child{
		"persona": {Component: persona.Name, Config: json.RawMessage(cfg)},
	}}
}

// TC-09b, DH-D-BUNDLE-5: the committed standard.yml declares exactly the six file
// tools and the two one-shot shell tools, and an agent bound to it sees exactly
// those and the preset's persona.
func TestStandardPreset(t *testing.T) {
	std, err := agentpreset.Shipped(agentpreset.Standard)
	if err != nil {
		t.Fatal(err)
	}
	if names := agentpreset.ShippedNames(); !slices.Contains(names, agentpreset.Standard) {
		t.Fatalf("shipped presets %v lack standard", names)
	}
	e := newEnv(t)
	if is := agentpreset.Validate(agentpreset.Config{Default: std.ID}, []agentpreset.Declaration{std}, e.cat); len(is) > 0 {
		t.Fatalf("standard.yml has issues: %v", is)
	}
	sc, err := e.bind(t, std.ID, "", std)
	if err != nil {
		t.Fatal(err)
	}
	a := e.assemble(t, sc.Key())
	var got []string
	for _, ts := range a.Tools {
		got = append(got, ts.Name)
	}
	want := []string{"read", "write", "edit", "apply_patch", "glob", "grep", "bash", "pwsh"}
	if !reflect.DeepEqual(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(want))) {
		t.Fatalf("standard's tools = %v, want exactly %v", got, want)
	}
	if p := sections(a)[prompt.SectionPersonaPrefix]; !strings.HasPrefix(p, "You are a coding agent.") {
		t.Fatalf("standard's persona prefix = %q", p)
	}
}

// TC-09b logic: an unknown tool set in a preset is refused naming it.
func TestUnknownChildRefusedByName(t *testing.T) {
	e := newEnv(t)
	d, err := agentpreset.ParseDeclaration("fixture.yml", []byte("id: fx\nchildren:\n  tool.nope: {}\n  fs.tools: {}\n"), "fx")
	if err != nil {
		t.Fatal(err)
	}
	is := agentpreset.Validate(agentpreset.Config{Default: "fx"}, []agentpreset.Declaration{d}, e.cat)
	if len(is) != 1 || !strings.Contains(is[0].String(), "tool.nope") {
		t.Fatalf("issues = %v, want one naming tool.nope", is)
	}
	if err := e.reg.Load(agentpreset.Config{Default: "fx"}, []agentpreset.Declaration{d}); err == nil || !strings.Contains(err.Error(), "tool.nope") {
		t.Fatalf("load = %v, want a refusal naming tool.nope", err)
	}
}

func TestParseDeclarationIsStrict(t *testing.T) {
	for name, c := range map[string]struct{ text, want string }{
		"unknown key": {"id: a\ntools: [read]\n", "tools"},
		"wrong id":    {"id: b\n", `"b"`},
		"wrong type":  {"id: a\norder: x\n", "order"},
		"broken yaml": {"id: [\n", "a.yml"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := agentpreset.ParseDeclaration("a.yml", []byte(c.text), "a"); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want one mentioning %s", err, c.want)
			}
		})
	}
	if _, err := agentpreset.Shipped("ghost"); err == nil || !strings.Contains(err.Error(), agentpreset.Standard) {
		t.Fatalf("Shipped(ghost) = %v, want an error listing the shipped presets", err)
	}
}

// DH-T-BUNDLE-16: a preset's persona shadows the global prefix and suffix for its
// agents only.
func TestScopedPersonaShadowsTheGlobal(t *testing.T) {
	e := newEnv(t)
	cfg := e.prompt.Config()
	cfg.PersonaPrefix, cfg.PersonaSuffix = "global prefix", "global suffix"
	if err := e.prompt.Configure(cfg); err != nil {
		t.Fatal(err)
	}
	plain := agentpreset.Declaration{ID: "plain"}
	scoped, err := e.bind(t, "plain", "scoped", plain, preset("scoped", `{"prefix":"scoped prefix"}`))
	if err != nil {
		t.Fatal(err)
	}
	got := sections(e.assemble(t, scoped.Key()))
	if got[prompt.SectionPersonaPrefix] != "scoped prefix" {
		t.Fatalf("scoped prefix = %q", got[prompt.SectionPersonaPrefix])
	}
	if _, ok := got[prompt.SectionPersonaSuffix]; ok {
		t.Fatalf("an empty scoped suffix should shadow the global one: %v", got)
	}
	if _, ok := got[prompt.SectionIdentity]; !ok {
		t.Fatalf("the identity line should stay: %v", got)
	}
	other, err := e.bind(t, "plain", "plain", plain, preset("scoped", `{"prefix":"scoped prefix"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := sections(e.assemble(t, other.Key())); got[prompt.SectionPersonaPrefix] != "global prefix" || got[prompt.SectionPersonaSuffix] != "global suffix" {
		t.Fatalf("another preset's agent sees %v, want the global persona", got)
	}
}

// DH-T-BUNDLE-16: Complete leaves one section, the persona.
func TestCompletePersona(t *testing.T) {
	e := newEnv(t)
	sc, err := e.bind(t, "p", "", preset("p", `{"prefix":"only me","suffix":"bye","complete":true}`))
	if err != nil {
		t.Fatal(err)
	}
	a := e.assemble(t, sc.Key())
	if len(a.Sections) != 1 || a.Sections[0].Text != "only me\n\nbye" || !a.Sections[0].Complete {
		t.Fatalf("sections = %+v, want the one complete persona", a.Sections)
	}
}

// DH-T-BUNDLE-16: a named suppression removes one optional context;
// includeRuntimeContext false removes every optional one; a required context stays.
func TestPersonaSuppression(t *testing.T) {
	e := newEnv(t)
	for _, c := range []prompt.RuntimeContext{
		{Name: "clock", Order: 1, Optional: true},
		{Name: "cwd", Order: 2, Optional: true},
		{Name: "rules", Order: 3},
	} {
		name := c.Name
		c.Func = func(prompt.Input) (string, bool, error) { return name, true, nil }
		if _, err := e.prompt.Context(e.root, c); err != nil {
			t.Fatal(err)
		}
	}
	decls := []agentpreset.Declaration{
		preset("named", `{"prefix":"x","suppressContexts":["clock"]}`),
		preset("none", `{"prefix":"x","includeRuntimeContext":false}`),
	}
	named, err := e.bind(t, "named", "named", decls...)
	if err != nil {
		t.Fatal(err)
	}
	if got := contexts(e.assemble(t, named.Key())); !reflect.DeepEqual(got, []string{"cwd", "rules"}) {
		t.Fatalf("named suppression leaves %v", got)
	}
	none, err := e.bind(t, "named", "none", decls...)
	if err != nil {
		t.Fatal(err)
	}
	if got := contexts(e.assemble(t, none.Key())); !reflect.DeepEqual(got, []string{"rules"}) {
		t.Fatalf("includeRuntimeContext false leaves %v", got)
	}

	for name, c := range map[string]struct{ cfg, want string }{
		"unknown":   {`{"prefix":"x","suppressContexts":["ghost"]}`, `"ghost"`},
		"required":  {`{"prefix":"x","suppressContexts":["rules"]}`, "required"},
		"no prefix": {`{"suffix":"x"}`, "prefix"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := e.bind(t, "bad", "", preset("bad", c.cfg)); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("bind = %v, want a failure mentioning %s", err, c.want)
			}
		})
	}
}

// TC-09b, DH-T-BUNDLE-16: a persona mounted outside a preset refuses to load,
// naming the prompt component's fields.
func TestPersonaOutsidePresetRefused(t *testing.T) {
	e := newEnv(t)
	f, err := e.root.Use(persona.Component, persona.Config{Prefix: "x"})
	if err == nil {
		err = f.Wait()
	}
	if err == nil || !strings.Contains(err.Error(), persona.Name) || !strings.Contains(err.Error(), "personaPrefix") {
		t.Fatalf("top-level persona = %v, want a refusal naming %s and personaPrefix", err, persona.Name)
	}
}
