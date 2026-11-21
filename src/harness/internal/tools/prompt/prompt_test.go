package prompt_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

type in struct {
	Q string `json:"q"`
}

type out struct{}

func define(name string, disc tools.Disclosure) *tools.Declaration {
	return tools.Define(tools.Spec[in, out]{
		Name: name, Description: "Tool " + name + ".", Disclosure: disc,
		Run: func(context.Context, in, tools.Run) (out, error) { return out{}, nil },
	})
}

type env struct {
	root   *cordis.Context
	tab    scope.Table
	tools  tools.Service
	prompt *prompt.Service
	fiber  *cordis.Fiber
}

func use(t *testing.T, root *cordis.Context, c *cordis.Component, cfg any) *cordis.Fiber {
	t.Helper()
	f, err := root.Use(c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatalf("%s: %v", c.Name(), err)
	}
	return f
}

func newEnv(t *testing.T, cfg prompt.Config) *env {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	use(t, root, scope.Component, cordis.None{})
	use(t, root, core.Component, cordis.None{})
	e := &env{root: root, fiber: use(t, root, prompt.Component, cfg)}
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
	return e
}

func (e *env) scope(t *testing.T, label string, enclosing ...scope.Key) *scope.Scope {
	t.Helper()
	s, err := e.tab.New(e.root, label, enclosing...)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// owner mounts a component named name and returns its context, so what it
// registers is owned by name.
func (e *env) owner(t *testing.T, name string) *cordis.Context {
	t.Helper()
	var got *cordis.Context
	c := cordis.Define(name, func(ctx *cordis.Context, _ cordis.None) error { got = ctx; return nil })
	use(t, e.root, c, cordis.None{})
	return got
}

func (e *env) assemble(t *testing.T, k scope.Key) (prompt.Assembly, error) {
	t.Helper()
	v, err := e.tools.Resolve(context.Background(), tools.Target{Scope: k})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	return e.prompt.Assemble(context.Background(), prompt.Input{View: v})
}

func (e *env) must(t *testing.T, k scope.Key) prompt.Assembly {
	t.Helper()
	a, err := e.assemble(t, k)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func (e *env) render(t *testing.T, a prompt.Assembly) string {
	t.Helper()
	s, err := e.prompt.Render(a)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func ok(_ *cordis.EffectHandle, err error) func(t *testing.T) {
	return func(t *testing.T) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func text(s string) func(prompt.Input) (string, bool, error) {
	return func(prompt.Input) (string, bool, error) { return s, true, nil }
}

func value(s string) prompt.Variable {
	return func(prompt.Input) (string, bool, error) { return s, true, nil }
}

// DH-T-TOOLS-15: an unknown or valueless variable fails the assembly, {{{{ escapes,
// a value is not rescanned and a literal section is not interpolated.
func TestInterpolation(t *testing.T) {
	cases := []struct {
		name, text string
		literal    bool
		want       string
		err        error
	}{
		{name: "plain", text: "cwd is {{cwd}}.", want: "cwd is /work."},
		{name: "escape", text: "write {{{{cwd}} for {{cwd}}", want: "write {{cwd}} for /work"},
		{name: "no-rescan", text: "x={{tricky}}", want: "x={{cwd}}"},
		{name: "literal", text: "{{nothing}} here", literal: true, want: "{{nothing}} here"},
		{name: "unknown", text: "{{nothing}}", err: prompt.ErrUnknownVariable},
		{name: "valueless", text: "{{empty}}", err: prompt.ErrValuelessVariable},
		{name: "unclosed", text: "{{cwd", err: prompt.ErrMalformed},
		{name: "malformed", text: "{{two words}}", err: prompt.ErrMalformed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newEnv(t, prompt.Config{})
			ok(e.prompt.Variable(e.root, "cwd", value("/work")))(t)
			ok(e.prompt.Variable(e.root, "tricky", value("{{cwd}}")))(t)
			ok(e.prompt.Variable(e.root, "empty", func(prompt.Input) (string, bool, error) { return "", false, nil }))(t)
			ok(e.prompt.Section(e.root, prompt.Section{Name: "body", Order: prompt.OrderHarness, Text: c.text, Literal: c.literal}))(t)
			a, err := e.assemble(t, scope.Key{})
			if c.err != nil {
				if !errors.Is(err, c.err) {
					t.Fatalf("err = %v, want %v", err, c.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := e.render(t, a); got != c.want {
				t.Fatalf("rendered %q, want %q", got, c.want)
			}
		})
	}
}

// DH-T-TOOLS-15: one complete section replaces every section; two fail.
func TestCompleteSections(t *testing.T) {
	e := newEnv(t, prompt.Defaults())
	ok(e.prompt.Section(e.root, prompt.Section{Name: "rules", Order: prompt.OrderHarness, Text: "Be careful."}))(t)
	s := e.scope(t, "agent")
	ok(e.prompt.Section(s.Ctx(), prompt.Section{Name: "whole", Order: prompt.OrderProject, Text: "Only this.", Complete: true}))(t)
	if got := e.render(t, e.must(t, s.Key())); got != "Only this." {
		t.Fatalf("complete section rendered %q", got)
	}
	if got := e.render(t, e.must(t, scope.Key{})); !strings.Contains(got, "Be careful.") || !strings.HasPrefix(got, prompt.IdentityLine) {
		t.Fatalf("global assembly %q lost its sections", got)
	}
	ok(e.prompt.Section(s.Ctx(), prompt.Section{Name: "other", Text: "Also whole.", Complete: true}))(t)
	if _, err := e.assemble(t, s.Key()); !errors.Is(err, prompt.ErrTwoComplete) {
		t.Fatalf("two complete sections: %v", err)
	}
	if _, err := e.prompt.Section(s.Ctx(), prompt.Section{Name: "other", Text: "again"}); !errors.Is(err, prompt.ErrDuplicate) {
		t.Fatalf("a second section of one name in one scope: %v", err)
	}
}

// DH-T-TOOLS-14: a scoped shadow, Suppress and a scoped variable change exactly
// their part of the assembly, and the manifest names each section's owner.
func TestScopedChangesAreLocal(t *testing.T) {
	e := newEnv(t, prompt.Defaults())
	rules, project := e.owner(t, "demo.rules"), e.owner(t, "demo.project")
	ok(e.prompt.Section(rules, prompt.Section{Name: "rules", Order: prompt.OrderHarness, Text: "Rules for {{who}}."}))(t)
	ok(e.prompt.Section(project, prompt.Section{Name: "project", Order: prompt.OrderProject, Text: "Project notes."}))(t)
	ok(e.prompt.Variable(rules, "who", value("everyone")))(t)
	ok(e.prompt.Context(project, prompt.RuntimeContext{Name: "clock", Order: 1, Optional: true, Func: text("12:00")}))(t)
	ok(e.prompt.Context(project, prompt.RuntimeContext{Name: "git", Order: 2, Func: text("clean")}))(t)
	if _, err := e.tools.Register(e.root, define("read", tools.Full)); err != nil {
		t.Fatal(err)
	}

	s := e.scope(t, "agent")
	base := e.must(t, s.Key())
	wantManifest := []prompt.ManifestEntry{
		{Name: prompt.SectionIdentity, Order: prompt.OrderIdentity, Owner: prompt.Name},
		{Name: "rules", Order: prompt.OrderHarness, Owner: "demo.rules"},
		{Name: "project", Order: prompt.OrderProject, Owner: "demo.project"},
	}
	if got := base.Manifest(); !slices.Equal(got, wantManifest) {
		t.Fatalf("manifest %+v, want %+v", got, wantManifest)
	}

	ok(e.prompt.Section(s.Ctx(), prompt.Section{Name: "project", Order: prompt.OrderProject, Text: "Agent notes."}))(t)
	ok(e.prompt.Variable(s.Ctx(), "who", value("the agent")))(t)
	ok(e.prompt.Suppress(s.Ctx(), "clock"))(t)
	ok(e.prompt.Suppress(s.Ctx(), "git"))(t)
	got := e.must(t, s.Key())

	if got.Sections[0] != base.Sections[0] {
		t.Fatalf("identity changed: %+v", got.Sections[0])
	}
	if got.Sections[1].Text != "Rules for the agent." || got.Sections[2].Text != "Agent notes." {
		t.Fatalf("scoped shadows not applied: %+v", got.Sections)
	}
	if len(got.Tools) != 1 || !reflect.DeepEqual(got.Tools, base.Tools) {
		t.Fatalf("tools changed: %v", got.Tools)
	}
	if len(got.Contexts) != 1 || got.Contexts[0].Name != "git" || len(base.Contexts) != 2 {
		t.Fatalf("Suppress removed %v from %v; want only the optional clock gone", got.Contexts, base.Contexts)
	}
	if got.Fingerprint == base.Fingerprint {
		t.Fatal("the fingerprint did not change with the sections")
	}
	// The global assembly is untouched.
	if g := e.must(t, scope.Key{}); g.Sections[1].Text != "Rules for everyone." || g.Sections[2].Text != "Project notes." || len(g.Contexts) != 2 {
		t.Fatalf("scoped changes leaked to the global assembly: %+v", g)
	}

	// Render refuses an assembly that was changed after Assemble.
	got.Sections[1].Text = "tampered"
	if _, err := e.prompt.Render(got); !errors.Is(err, prompt.ErrNotAssembled) {
		t.Fatalf("Render(tampered) = %v", err)
	}
}

// DH-T-TOOLS-15: toolOrder is validated with its index path; a known tool that is
// not visible is skipped; deferred tools are not listed.
func TestToolOrder(t *testing.T) {
	for _, n := range []string{"order_a", "order_b", "order_c", "order_hidden"} {
		define(n, tools.Full)
	}
	define("order_lazy", tools.Deferred)

	bad := prompt.Config{ToolOrder: []string{"order_a", "no_such_tool", prompt.Unlisted, "order_a"}}
	var ce *cordis.ConfigError
	if err := bad.Validate(); !errors.As(err, &ce) || len(ce.Issues) != 2 || ce.Issues[0].Path != "toolOrder.1" || ce.Issues[1].Path != "toolOrder.3" {
		t.Fatalf("Validate = %v; want issues at toolOrder.1 and toolOrder.3", err)
	}
	if err := (prompt.Config{ToolOrder: []string{"order_a"}}).Validate(); !errors.As(err, &ce) || ce.Issues[0].Path != "toolOrder" {
		t.Fatalf("a toolOrder without %s: %v", prompt.Unlisted, err)
	}

	cfg := prompt.Config{ToolOrder: []string{"order_c", "order_hidden", prompt.Unlisted, "order_a"}}
	e := newEnv(t, cfg)
	for _, n := range []string{"order_a", "order_b", "order_c", "order_lazy"} {
		disc := tools.Full
		if n == "order_lazy" {
			disc = tools.Deferred
		}
		if _, err := e.tools.Register(e.root, define(n, disc)); err != nil {
			t.Fatal(err)
		}
	}
	var got []string
	for _, ts := range e.must(t, scope.Key{}).Tools {
		got = append(got, ts.Name)
	}
	if want := []string{"order_c", "order_b", "order_a"}; !slices.Equal(got, want) {
		t.Fatalf("tools %v, want %v", got, want)
	}
}

// The persona prefix and suffix are live: a config update changes the next
// assembly in place, and an invalid one is refused.
func TestLivePersona(t *testing.T) {
	e := newEnv(t, prompt.Defaults())
	ok(e.prompt.Section(e.root, prompt.Section{Name: "rules", Order: prompt.OrderHarness, Text: "Rules."}))(t)
	if got := e.render(t, e.must(t, scope.Key{})); got != prompt.IdentityLine+"\n\nRules." {
		t.Fatalf("before: %q", got)
	}
	next := prompt.Defaults()
	next.PersonaPrefix, next.PersonaSuffix = "You are terse.", "Answer in English. {{not-interpolated}}"
	if err := e.fiber.Update(next, true); err != nil {
		t.Fatal(err)
	}
	if err := e.fiber.Wait(); err != nil {
		t.Fatal(err)
	}
	want := prompt.IdentityLine + "\n\nYou are terse.\n\nRules.\n\nAnswer in English. {{not-interpolated}}"
	if got := e.render(t, e.must(t, scope.Key{})); got != want {
		t.Fatalf("after update: %q, want %q", got, want)
	}
	if err := e.prompt.Configure(prompt.Config{ToolOrder: []string{"no_such_tool", prompt.Unlisted}}); err == nil {
		t.Fatal("Configure accepted an unknown tool")
	}
	off := prompt.Config{}
	if err := e.prompt.Configure(off); err != nil {
		t.Fatal(err)
	}
	if got := e.render(t, e.must(t, scope.Key{})); got != "Rules." {
		t.Fatalf("identity off: %q", got)
	}
}

// The service keeps its own toolOrder: mutating the slice handed to the component,
// to Configure or to a live update, or the one Config returns, changes nothing.
func TestToolOrderIsCopied(t *testing.T) {
	for _, n := range []string{"copy_a", "copy_b"} {
		define(n, tools.Full)
	}
	order := func() []string { return []string{"copy_a", prompt.Unlisted, "copy_b"} }
	listed := func(e *env) []string {
		var got []string
		for _, ts := range e.must(t, scope.Key{}).Tools {
			got = append(got, ts.Name)
		}
		return got
	}
	want := []string{"copy_a", "copy_b"}

	given := order()
	e := newEnv(t, prompt.Config{ToolOrder: given})
	for _, n := range []string{"copy_a", "copy_b"} {
		if _, err := e.tools.Register(e.root, define(n, tools.Full)); err != nil {
			t.Fatal(err)
		}
	}
	given[2] = "copy_a"
	if got := listed(e); !slices.Equal(got, want) {
		t.Fatalf("after mutating the component config: %v, want %v", got, want)
	}

	e.prompt.Config().ToolOrder[2] = "copy_a"
	if got := listed(e); !slices.Equal(got, want) {
		t.Fatalf("after mutating Config(): %v, want %v", got, want)
	}

	configured := order()
	if err := e.prompt.Configure(prompt.Config{ToolOrder: configured}); err != nil {
		t.Fatal(err)
	}
	configured[2] = "copy_a"
	if got := listed(e); !slices.Equal(got, want) {
		t.Fatalf("after mutating the Configure argument: %v, want %v", got, want)
	}

	live := order()
	if err := e.fiber.Update(prompt.Config{ToolOrder: live}, true); err != nil {
		t.Fatal(err)
	}
	if err := e.fiber.Wait(); err != nil {
		t.Fatal(err)
	}
	live[2] = "copy_a"
	if got := listed(e); !slices.Equal(got, want) {
		t.Fatalf("after mutating the live update: %v, want %v", got, want)
	}
}
