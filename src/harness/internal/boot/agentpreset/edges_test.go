package agentpreset_test

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
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
)

// failSession fails Read with readErr and Append with appendErr when set.
type failSession struct {
	session.Session
	readErr, appendErr error
}

func (s *failSession) Read(ctx context.Context, from session.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.Session.Read(ctx, from, limit, opts...)
}

func (s *failSession) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	if s.appendErr != nil {
		return event.Receipt{}, s.appendErr
	}
	return s.Session.Append(ctx, d)
}

func totalLeases(rs []agentpreset.RevisionStatus) int {
	n := 0
	for _, r := range rs {
		n += r.Leases
	}
	return n
}

func loadTwo(t *testing.T, e *env, cfg agentpreset.Config) {
	t.Helper()
	other := standard("v2")
	other.ID = "other"
	if err := e.reg.Load(cfg, []agentpreset.Declaration{standard("v1"), other}); err != nil {
		t.Fatal(err)
	}
}

// DH-ADR-BUNDLE-7: a child agent that names no preset joins its creator's exact
// revision, even after an edit retired it; the retired revision stays mounted
// until the child's lease goes too. A holder bound to nothing, or a target that
// lacks its session, scope or owner, is refused.
func TestBindFromJoinsTheCreatorsExactRevision(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, memstore.New())
	cfg := agentpreset.Config{Default: "standard"}
	if err := e.reg.Load(cfg, []agentpreset.Declaration{standard("v1")}); err != nil {
		t.Fatal(err)
	}
	_, parent, tp := e.agent(t, "parent")
	rev1, err := e.reg.Bind(ctx, tp, "", agentpreset.CauseCreate)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.reg.Load(cfg, []agentpreset.Declaration{standard("v2")}); err != nil {
		t.Fatal(err)
	}

	hc, child, tc := e.agent(t, "child")
	got, err := e.reg.BindFrom(ctx, tc, parent.Key())
	if err != nil || got.Revision != rev1.Revision || got.Digest != rev1.Digest {
		t.Fatalf("bind from the creator = %+v, %v; want its revision %d", got, err, rev1.Revision)
	}
	if b := e.binding(t, hc); b.Preset != "standard" || b.Digest != rev1.Digest || b.Cause != agentpreset.CauseInherit {
		t.Fatalf("child's fold = %+v, want rev 1 with cause inherit", b)
	}
	if v, ok := e.reg.Lookup(child.Key(), "tool.read"); !ok || v != "v1" {
		t.Fatalf("child sees tool.read = %v %v, want the creator's v1", v, ok)
	}
	if r, ok := revision(e.reg.Revisions(), "standard", 1); !ok || !r.Retired || r.Leases != 2 {
		t.Fatalf("rev 1 = %+v %v, want retired with the creator's and the child's lease", r, ok)
	}
	if err := parent.Close(); err != nil {
		t.Fatal(err)
	}
	if r, ok := revision(e.reg.Revisions(), "standard", 1); !ok || r.Leases != 1 {
		t.Fatalf("rev 1 after the creator closed = %+v %v, want kept for the child", r, ok)
	}

	_, stranger, _ := e.agent(t, "stranger")
	_, _, tg := e.agent(t, "orphan")
	before := totalLeases(e.reg.Revisions())
	if _, err := e.reg.BindFrom(ctx, tg, stranger.Key()); err == nil || !strings.Contains(err.Error(), "bound to no preset") {
		t.Fatalf("bind from an unbound holder = %v", err)
	}
	if n := totalLeases(e.reg.Revisions()); n != before {
		t.Fatalf("a refused inherit changed the lease count from %d to %d", before, n)
	}
	for name, bad := range map[string]agentpreset.Target{
		"no session": {Scope: tg.Scope, Owner: tg.Owner},
		"no scope":   {Session: tg.Session, Owner: tg.Owner},
		"no owner":   {Session: tg.Session, Scope: tg.Scope},
	} {
		if _, err := e.reg.BindFrom(ctx, bad, child.Key()); err == nil {
			t.Errorf("BindFrom with %s ran", name)
		}
		if _, err := e.reg.Bind(ctx, bad, "", agentpreset.CauseCreate); err == nil {
			t.Errorf("Bind with %s ran", name)
		}
	}
}

// bundle-preset.md §6.3: a Select whose append is refused re-encloses the old
// revision and releases the new lease; no event is written and the agent stays
// bound to what it had. A log that cannot be read fails the binding the same way.
func TestFailedSelectRestoresTheBinding(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, memstore.New())
	loadTwo(t, e, agentpreset.Config{Default: "standard"})
	h, sc, tg := e.agent(t, "s1")
	first, err := e.reg.Bind(ctx, tg, "", agentpreset.CauseCreate)
	if err != nil {
		t.Fatal(err)
	}
	encl := e.scopes.Enclosing(sc.Key())
	evs := types(t, h)

	for name, fs := range map[string]*failSession{
		"append refused": {Session: h, appendErr: errors.New("disk full")},
		"read fails":     {Session: h, readErr: errors.New("log unreadable")},
	} {
		tg.Session = fs
		_, err := e.reg.Select(ctx, tg, "other")
		if err == nil || !strings.Contains(err.Error(), `to "other"`) {
			t.Fatalf("%s: select = %v, want the bind error", name, err)
		}
		if of, ok := e.reg.Of(sc.Key()); !ok || of.ID != "standard" || of.Digest != first.Digest {
			t.Fatalf("%s: bound to %+v %v, want standard still", name, of, ok)
		}
		if got := e.scopes.Enclosing(sc.Key()); !slices.Equal(got, encl) {
			t.Fatalf("%s: encloses %v, want %v restored", name, got, encl)
		}
		if r, _ := revision(e.reg.Revisions(), "other", 1); r.Leases != 0 {
			t.Fatalf("%s: other kept a lease: %+v", name, r)
		}
		if got := types(t, h); !slices.Equal(got, evs) {
			t.Fatalf("%s: the log became %v", name, got)
		}
	}
}

// A binding whose agent scope is closed cannot enclose the revision: it is
// refused and the lease it took is given back.
func TestBindOnAClosedScopeGivesTheLeaseBack(t *testing.T) {
	e := newEnv(t, memstore.New())
	loadTwo(t, e, agentpreset.Config{Default: "standard"})
	_, sc, tg := e.agent(t, "s1")
	if err := sc.Close(); err != nil {
		t.Fatal(err)
	}
	tg.Owner = e.root // the lease outlives the scope, so only the enclose fails
	if _, err := e.reg.Bind(context.Background(), tg, "", agentpreset.CauseCreate); err == nil || !strings.Contains(err.Error(), "enclose") {
		t.Fatalf("bind on a closed scope = %v, want an enclose error", err)
	}
	if n := totalLeases(e.reg.Revisions()); n != 0 {
		t.Fatalf("%d leases left by a refused binding", n)
	}
	if _, ok := e.reg.Of(sc.Key()); ok {
		t.Fatal("a refused binding was recorded")
	}
}

// Select of an id that is not declared is refused with the available ids, and the
// session stays unbound.
func TestSelectUnknownPreset(t *testing.T) {
	e := newEnv(t, memstore.New())
	loadTwo(t, e, agentpreset.Config{Default: "standard"})
	h, sc, tg := e.agent(t, "s1")
	n := len(types(t, h))
	_, err := e.reg.Select(context.Background(), tg, "ghost")
	var pe *agentpreset.Error
	if !errors.As(err, &pe) || pe.Code != agentpreset.CodeNotFound || !strings.Contains(err.Error(), "available: other, standard") {
		t.Fatalf("select ghost = %v, want not-found listing other, standard", err)
	}
	if _, ok := e.reg.Of(sc.Key()); ok || len(types(t, h)) != n {
		t.Fatalf("a refused select bound the session: %v", types(t, h))
	}
}

// §6.3: a child that would write to a Shared registry faults its revision before
// anything mounts; it is listed broken naming the registry and Acquire refuses it.
func TestSharedRegistryChildFaultsTheRevision(t *testing.T) {
	e := newEnv(t, memstore.New())
	e.scopes.Declare("tool.missing", scope.Shared)
	if err := e.reg.Load(agentpreset.Config{Default: "needy"}, []agentpreset.Declaration{
		{ID: "needy", Children: map[string]*agentpreset.Child{"tool.needs": {}}},
	}); err != nil {
		t.Fatal(err)
	}
	in, err := e.reg.Resolve("")
	if err != nil || !strings.Contains(in.Broken, "Shared registry") {
		t.Fatalf("needy = %+v, %v; want broken naming the Shared registry", in, err)
	}
	if l, err := e.reg.Acquire(e.root, "needy"); !errors.Is(err, agentpreset.ErrInvalid) || l != nil {
		t.Fatalf("acquire needy = %v, %v; want agentpreset/invalid", l, err)
	}
	if n := totalLeases(e.reg.Revisions()); n != 0 {
		t.Fatalf("%d leases left", n)
	}
}

// The registry's refusals: a registry without its context, catalogue or scope
// table; a declaration without its owner or with an empty or ":" id; a lease
// without a live owner (the count returns to zero); reads of an agent bound to
// nothing or of a service its revision does not provide; and every change once
// the registry has unloaded.
func TestRegistryRefusals(t *testing.T) {
	e := newEnv(t, memstore.New())
	for name, o := range map[string]agentpreset.Options{
		"no catalogue": {Scopes: e.scopes},
		"no scopes":    {Catalogue: e.fx.cat},
	} {
		if _, err := agentpreset.New(e.root, o); err == nil {
			t.Errorf("New with %s succeeded", name)
		}
	}
	if _, err := agentpreset.New(nil, agentpreset.Options{Catalogue: e.fx.cat, Scopes: e.scopes}); err == nil {
		t.Error("New without a context succeeded")
	}
	if err := e.reg.Declare(nil, standard("v1")); err == nil {
		t.Error("a declaration without an owner was accepted")
	}
	for _, id := range []string{"", "a:b"} {
		if err := e.reg.Declare(e.root, agentpreset.Declaration{ID: id}); !errors.Is(err, agentpreset.ErrInvalid) {
			t.Errorf("declare %q = %v, want agentpreset/invalid", id, err)
		}
	}
	if _, err := e.reg.Resolve(""); !errors.Is(err, agentpreset.ErrNotFound) || !strings.Contains(err.Error(), "none is declared") {
		t.Fatalf("resolve on an empty registry = %v", err)
	}

	loadTwo(t, e, agentpreset.Config{Default: "standard"})
	if _, err := e.reg.Acquire(nil, ""); err == nil {
		t.Error("a lease without an owner was handed out")
	}
	gone := cordis.New()
	gone.Dispose()
	if _, err := e.reg.Acquire(gone, ""); err == nil {
		t.Error("a lease on a disposed owner was handed out")
	}
	if n := totalLeases(e.reg.Revisions()); n != 0 {
		t.Fatalf("refused leases left %d counted", n)
	}

	_, sc, tg := e.agent(t, "s1")
	if _, ok := e.reg.Of(sc.Key()); ok {
		t.Error("an unbound agent reads a binding")
	}
	if _, ok := e.reg.Lookup(sc.Key(), "tool.read"); ok {
		t.Error("an unbound agent sees a preset service")
	}
	if _, err := e.reg.Bind(context.Background(), tg, "", agentpreset.CauseCreate); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.reg.Lookup(sc.Key(), "tool.note"); ok {
		t.Error("a service the revision does not provide is visible")
	}

	e.root.Dispose()
	if err := e.reg.Declare(e.root, agentpreset.Declaration{ID: "late"}); !errors.Is(err, agentpreset.ErrClosed) {
		t.Errorf("declare after unload = %v, want agentpreset/closed", err)
	}
	if err := e.reg.Load(agentpreset.Config{Default: "standard"}, []agentpreset.Declaration{standard("v1")}); !errors.Is(err, agentpreset.ErrClosed) {
		t.Errorf("load after unload = %v, want agentpreset/closed", err)
	}
	if _, err := e.reg.AcquireFrom(e.root, sc.Key()); err == nil {
		t.Error("an inherit after unload was handed out")
	}
}

// Default is the saved choice while it names a declaration, else Config.Default;
// "" resolves to it.
func TestSelectedDefaultWinsWhileDeclared(t *testing.T) {
	e := newEnv(t, memstore.New())
	loadTwo(t, e, agentpreset.Config{Default: "standard", SelectedDefault: "other"})
	if d := e.reg.Default(); d != "other" {
		t.Fatalf("default = %q, want the saved choice other", d)
	}
	if in, err := e.reg.Resolve(""); err != nil || in.ID != "other" {
		t.Fatalf("resolve default = %+v, %v", in, err)
	}
	loadTwo(t, e, agentpreset.Config{Default: "standard", SelectedDefault: "gone"})
	if d := e.reg.Default(); d != "standard" {
		t.Fatalf("default = %q, want standard while the saved choice names nothing", d)
	}
}

// Two spellings of one child config (whitespace, key order) have one digest, so a
// reload that only respells it keeps the revision.
func TestRespelledConfigKeepsTheRevision(t *testing.T) {
	e := newEnv(t, memstore.New())
	cfg := agentpreset.Config{Default: "p"}
	decl := func(raw string) []agentpreset.Declaration {
		return []agentpreset.Declaration{{ID: "p", Children: map[string]*agentpreset.Child{
			"r": {Component: "tool.read", Config: json.RawMessage(raw)},
		}}}
	}
	if err := e.reg.Load(cfg, decl(`{"root":"v1"}`)); err != nil {
		t.Fatal(err)
	}
	before, _ := e.reg.Resolve("p")
	if err := e.reg.Load(cfg, decl("{ \"root\" :\n \"v1\" }")); err != nil {
		t.Fatal(err)
	}
	after, _ := e.reg.Resolve("p")
	if after.Revision != before.Revision || after.Digest != before.Digest {
		t.Fatalf("a respelled config rebuilt %+v into %+v", before, after)
	}
	if got := agentpreset.ComponentOf("r", agentpreset.Child{Component: "tool.read"}); got != "tool.read" {
		t.Fatalf("ComponentOf with a component = %q", got)
	}
	if got := agentpreset.ComponentOf("tool.note@extra", agentpreset.Child{}); got != "tool.note" {
		t.Fatalf("ComponentOf of an instance key = %q", got)
	}
}

// The durable event carries a binding's preset and digest and one of the five
// causes; anything else is refused at the draft. The fold ignores other events.
func TestSelectedEventContract(t *testing.T) {
	for _, c := range []agentpreset.Cause{agentpreset.CauseCreate, agentpreset.CauseSelect, agentpreset.CauseResume,
		agentpreset.CauseInherit, agentpreset.CauseRebind} {
		if !c.Valid() {
			t.Errorf("cause %q is not valid", c)
		}
		if _, err := agentpreset.SelectedType.Draft(agentpreset.Selected{Preset: "p", Digest: "d", Cause: c}); err != nil {
			t.Errorf("draft with cause %q: %v", c, err)
		}
	}
	for name, s := range map[string]agentpreset.Selected{
		"no preset": {Digest: "d", Cause: agentpreset.CauseCreate},
		"no digest": {Preset: "p", Cause: agentpreset.CauseCreate},
		"bad cause": {Preset: "p", Digest: "d", Cause: "whim"},
	} {
		if _, err := agentpreset.SelectedType.Draft(s); err == nil {
			t.Errorf("a draft with %s was accepted", name)
		}
	}

	e := newEnv(t, memstore.New())
	h, _, _ := e.agent(t, "s1")
	d, err := event.TurnStartType.Draft(event.TurnStart{Turn: 1})
	if err == nil {
		_, err = h.Append(context.Background(), d)
	}
	if err != nil {
		t.Fatal(err)
	}
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil || len(evs) == 0 || evs[len(evs)-1].Type() != "turn/start" {
		t.Fatalf("read %v, %v", evs, err)
	}
	last := evs[len(evs)-1]
	b := agentpreset.Binding{Preset: "p", Digest: "d", Cause: agentpreset.CauseCreate}
	if got, changed := agentpreset.BindingView().Apply(b, last); changed || got != b {
		t.Fatalf("the fold applied %s: %+v", last.Type(), got)
	}
	if _, err := agentpreset.BindingOf(context.Background(), &failSession{Session: h, readErr: errors.New("gone")}); err == nil {
		t.Fatal("BindingOf of an unreadable log succeeded")
	}
}

// An Error's text is its code, then its message; errors.Is matches only a
// sentinel (no message) of the same code. An Issue reads as its place and message.
func TestErrorAndIssueText(t *testing.T) {
	if s := (&agentpreset.Error{Code: agentpreset.CodeLocked}).Error(); s != "agentpreset/locked" {
		t.Fatalf("bare error = %q", s)
	}
	err := &agentpreset.Error{Code: agentpreset.CodeInvalid, Msg: "bad"}
	if err.Error() != "agentpreset/invalid: bad" || !errors.Is(err, agentpreset.ErrInvalid) || errors.Is(err, agentpreset.ErrLocked) {
		t.Fatalf("error %q matches wrongly", err)
	}
	if errors.Is(err, &agentpreset.Error{Code: agentpreset.CodeInvalid, Msg: "other"}) {
		t.Fatal("an error matched a non-sentinel")
	}
	for want, is := range map[string]agentpreset.Issue{
		"m":                   {Message: "m"},
		"preset p: m":         {Preset: "p", Message: "m"},
		"preset id: m":        {Path: "id", Message: "m"},
		"preset p.extends: m": {Preset: "p", Path: "extends", Message: "m"},
	} {
		if got := is.String(); got != want {
			t.Errorf("%+v reads %q, want %q", is, got, want)
		}
	}
}

func raw(t *testing.T, id, name, cfg string) loader.EntryOptions {
	t.Helper()
	return loader.EntryOptions{ID: id, Name: name, Config: json.RawMessage(cfg)}
}

// CheckTree's placement and its reading of declarations field by field: a second
// registry and a declaration with no registry are issues; an empty id is placed at
// its own entry; a disabled entry, a config that does not decode, a whole-config
// expression and a child that is an expression or names no literal component take
// no part; a {"$expr"} reference is computed; entries in a group carry its path.
func TestCheckTreeEdges(t *testing.T) {
	fx := newFixture(t)
	check := agentpreset.CheckTree(fx.cat, envEvaluator{"P": "std"})
	reg := raw(t, "presets", agentpreset.RegistryName, `{"default":"std"}`)
	std := raw(t, "std", agentpreset.DeclarationName, `{"id":"std"}`)
	paths := func(is []loader.Issue) string {
		var out []string
		for _, i := range is {
			out = append(out, i.Path+": "+i.Message)
		}
		return strings.Join(out, "\n")
	}
	cases := map[string]struct {
		tree []loader.EntryOptions
		want []string // substrings, one per issue; none for a clean tree
	}{
		"nothing of ours": {tree: []loader.EntryOptions{raw(t, "x", "tool.read", `{}`)}},
		"second registry": {tree: []loader.EntryOptions{reg, std, raw(t, "again", agentpreset.RegistryName, `{"default":"std"}`)},
			want: []string{"again.config: a second agent.presets entry (the first is presets)"}},
		"no registry": {tree: []loader.EntryOptions{std},
			want: []string{"std.config: an agent.preset entry needs an agent.presets entry"}},
		"only unknown ids and no registry": {tree: []loader.EntryOptions{raw(t, "c", agentpreset.DeclarationName, `"${{ env.NOPE }}"`)}},
		"empty id placed at its entry": {tree: []loader.EntryOptions{reg, std, raw(t, "blank", agentpreset.DeclarationName, `{"name":"x"}`)},
			want: []string{"blank.config.id: a declaration needs an id"}},
		"null and undecodable configs skipped": {tree: []loader.EntryOptions{reg, std,
			raw(t, "n", agentpreset.DeclarationName, `null`), raw(t, "bad", agentpreset.DeclarationName, `[1]`)},
			want: []string{"n.config.id: a declaration needs an id"}},
		"registry config that does not decode":  {tree: []loader.EntryOptions{raw(t, "presets", agentpreset.RegistryName, `[1]`), std}},
		"registry default that is not a string": {tree: []loader.EntryOptions{raw(t, "presets", agentpreset.RegistryName, `{"default":3}`), std}},
		"registry config that is an expression": {tree: []loader.EntryOptions{raw(t, "presets", agentpreset.RegistryName, `"${{ env.CFG }}"`), std}},
		"null registry config needs a default": {tree: []loader.EntryOptions{raw(t, "presets", agentpreset.RegistryName, ``), std},
			want: []string{"presets.config.default: preset default: the registry needs a default preset"}},
		"$expr references computed": {tree: []loader.EntryOptions{
			raw(t, "presets", agentpreset.RegistryName, `{"default":{"$expr":"env.P"},"selectedDefault":"std"}`),
			std, raw(t, "kid", agentpreset.DeclarationName, `{"id":"kid","extends":{"$expr":" env.P "}}`)}},
		"malformed expressions stay unknown": {tree: []loader.EntryOptions{reg, std,
			raw(t, "k1", agentpreset.DeclarationName, `{"id":"${{ env.P }} and more"}`),
			raw(t, "k2", agentpreset.DeclarationName, `{"id":{"$expr":"env.P","x":1}}`),
			raw(t, "k3", agentpreset.DeclarationName, `{"id":"${{ }}"}`),
			raw(t, "k4", agentpreset.DeclarationName, `{"id":{"$expr":3}}`)}},
		"children read field by field": {tree: []loader.EntryOptions{reg, raw(t, "std", agentpreset.DeclarationName,
			`{"id":"std","name":"${{ env.P }}","order":2,"children":{
				"gone":null,
				"computed":"${{ env.KID }}",
				"odd":[1],
				"named":{"component":{"$expr":"env.C"}},
				"tool.read":{"disabled":"${{ env.OFF }}","config":{"root":"x"}},
				"tool.nope":{}}}`)},
			want: []string{"std.config.children.tool.nope: preset std.children.tool.nope: component \"tool.nope\""}},
		"children that are not a map":     {tree: []loader.EntryOptions{reg, raw(t, "std", agentpreset.DeclarationName, `{"id":"std","children":[1]}`)}},
		"children that are an expression": {tree: []loader.EntryOptions{reg, raw(t, "std", agentpreset.DeclarationName, `{"id":"std","children":"${{ env.K }}"}`)}},
		"disabled entries take no part": {tree: []loader.EntryOptions{reg, std,
			{ID: "off", Name: agentpreset.DeclarationName, Disabled: true, Config: json.RawMessage(`{"id":"std"}`)}}},
		"group path": {tree: []loader.EntryOptions{reg, std, {ID: "g", Group: true, Children: []loader.EntryOptions{
			raw(t, "orphan", agentpreset.DeclarationName, `{"id":"orphan","extends":"nowhere"}`)}}},
			want: []string{"g" + loader.Separator + "orphan.config.extends"}},
		"unnamed registry entry": {tree: []loader.EntryOptions{raw(t, "", agentpreset.RegistryName, `{"default":"ghost"}`), std},
			want: []string{"agent.presets.config.default"}},
		"persona at the top level": {tree: []loader.EntryOptions{raw(t, "who", agentpreset.PersonaName, `{"prefix":"x"}`)},
			want: []string{"who.name: agent.persona mounts only as a child of an agent.preset declaration; a persona for every agent is tools.prompt's personaPrefix"}},
		"persona in a group": {tree: []loader.EntryOptions{{ID: "g", Group: true, Children: []loader.EntryOptions{
			raw(t, "who", agentpreset.PersonaName, `{"prefix":"x"}`)}}},
			want: []string{"g" + loader.Separator + "who.name: agent.persona"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := check(tc.tree)
			if len(got) != len(tc.want) {
				t.Fatalf("issues:\n%s\nwant %d: %v", paths(got), len(tc.want), tc.want)
			}
			for i, w := range tc.want {
				if s := got[i].Path + ": " + got[i].Message; !strings.Contains(s, w) {
					t.Errorf("issue %d = %q, want it to contain %q", i, s, w)
				}
			}
		})
	}
	// Without an evaluator every expression stays unknown.
	if is := agentpreset.CheckTree(fx.cat, nil)([]loader.EntryOptions{
		raw(t, "presets", agentpreset.RegistryName, `{"default":{"$expr":"env.P"}}`), std}); len(is) != 0 {
		t.Fatalf("issues without an evaluator: %s", paths(is))
	}
}

// bundle-preset.md §6.1: an update of agent.presets' config is accepted in place
// (the registry, its revisions and its bindings survive; only the default moves),
// and an update of an agent.preset entry that keeps its id is accepted in place too:
// the declaration is swapped and rebuilt only where its children changed. An update
// that changes the id restarts the entry under the new id.
func TestComponentUpdatesInPlace(t *testing.T) {
	fx := newFixture(t)
	root := cordis.New()
	t.Cleanup(root.Dispose)
	other := standard("v2")
	other.ID = "other"
	tree := func(cfg agentpreset.Config, std agentpreset.Declaration) loader.Layer {
		return loader.Layer{Label: "host", Ops: []loader.Op{{Kind: loader.OpInsert, Entries: []loader.EntryOptions{
			{ID: "scope", Name: "session.scope"},
			entry(t, "presets", agentpreset.RegistryName, cfg),
			entry(t, "standard", agentpreset.DeclarationName, std),
			entry(t, "other", agentpreset.DeclarationName, other),
		}}}}
	}
	host, err := loader.New(root, loader.Options{Resolver: fx.cat}, tree(agentpreset.Config{Default: "standard"}, standard("v1")))
	if err != nil {
		t.Fatal(err)
	}
	host.WaitAll()
	reg, err := agentpreset.Key.Get(root)
	if err != nil {
		t.Fatal(err)
	}
	l, err := reg.Acquire(root, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(l.Release)
	apply := func(cfg agentpreset.Config, std agentpreset.Declaration) {
		t.Helper()
		if err := host.ApplyLayers([]loader.Layer{tree(cfg, std)}); err != nil {
			t.Fatal(err)
		}
		host.WaitAll()
		if now, err := agentpreset.Key.Get(root); err != nil || now != reg {
			t.Fatalf("the registry was replaced by the update: %v", err)
		}
	}

	apply(agentpreset.Config{Default: "other"}, standard("v1"))
	if reg.Default() != "other" {
		t.Fatalf("default after the update = %q, want other", reg.Default())
	}
	if r, ok := revision(reg.Revisions(), "standard", 1); !ok || r.Retired || r.Leases != 1 {
		t.Fatalf("standard rev 1 after a config update = %+v %v, want current with its lease", r, ok)
	}

	renamed := standard("v1")
	renamed.Name = "Standard"
	apply(agentpreset.Config{Default: "other"}, renamed)
	if in, _ := reg.Resolve("standard"); in.Revision != 1 || in.Name != "Standard" {
		t.Fatalf("a renamed standard = %+v, want rev 1 renamed", in)
	}

	apply(agentpreset.Config{Default: "other"}, standard("v3"))
	if in, _ := reg.Resolve("standard"); in.Revision != 2 {
		t.Fatalf("standard after a child edit = rev %d, want 2", in.Revision)
	}
	if r, ok := revision(reg.Revisions(), "standard", 1); !ok || !r.Retired || r.Leases != 1 {
		t.Fatalf("rev 1 after the edit = %+v %v, want retired with its lease", r, ok)
	}
	if in, _ := reg.Resolve("other"); in.Revision != 1 {
		t.Fatalf("other rebuilt to rev %d by an edit of standard", in.Revision)
	}

	moved := standard("v3")
	moved.ID = "moved"
	apply(agentpreset.Config{Default: "other"}, moved)
	if _, err := reg.Resolve("standard"); !errors.Is(err, agentpreset.ErrNotFound) {
		t.Fatalf("standard after its id changed = %v, want not-found", err)
	}
	if in, err := reg.Resolve("moved"); err != nil || in.Revision != 1 || in.Broken != "" {
		t.Fatalf("moved = %+v, %v", in, err)
	}
}

// agent.presets refuses to mount without the host's catalogue.
func TestRegistryComponentNeedsACatalogue(t *testing.T) {
	cat, err := catalog.Build([]catalog.Item{
		{Name: "session.scope", Component: scope.Component},
		{Name: agentpreset.RegistryName, Component: agentpreset.RegistryComponent(func() agentpreset.Host { return agentpreset.Host{} }),
			Config: reflect.TypeFor[agentpreset.Config](), Provides: []string{agentpreset.RegistryName}},
	})
	if err != nil {
		t.Fatal(err)
	}
	root := cordis.New()
	t.Cleanup(root.Dispose)
	host, err := loader.New(root, loader.Options{Resolver: cat}, loader.Layer{Label: "host", Ops: []loader.Op{{Kind: loader.OpInsert,
		Entries: []loader.EntryOptions{{ID: "scope", Name: "session.scope"}, entry(t, "presets", agentpreset.RegistryName, agentpreset.Config{Default: "x"})}}}})
	if err != nil {
		t.Fatal(err)
	}
	host.WaitAll()
	if f := host.Fiber(host.Find("presets")); f == nil || f.Err() == nil || !strings.Contains(f.Err().Error(), "without a catalogue") {
		t.Fatalf("agent.presets without a catalogue = %v", f)
	}
}
