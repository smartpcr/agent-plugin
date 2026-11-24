package agentpreset_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

type readConfig struct {
	Root string `json:"root"`
}

// fixture is a test catalogue: tool.read provides its root as the private service
// tool.read, tool.note provides nothing, both count their live instances, and
// tool.fail never loads, tool.needs waits for a service nobody provides.
type fixture struct {
	cat  *catalog.Catalog
	live atomic.Int32
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	read := cordis.Define("tool.read", func(ctx *cordis.Context, cfg readConfig) error {
		if _, err := ctx.Effect("live", func() (cordis.Disposer, error) {
			f.live.Add(1)
			return func() error { f.live.Add(-1); return nil }, nil
		}); err != nil {
			return err
		}
		_, err := ctx.Provide("tool.read", cfg.Root)
		return err
	})
	note := cordis.Define("tool.note", func(ctx *cordis.Context, cfg readConfig) error {
		_, err := ctx.Effect("live", func() (cordis.Disposer, error) {
			f.live.Add(1)
			return func() error { f.live.Add(-1); return nil }, nil
		})
		return err
	})
	fail := cordis.Define("tool.fail", func(*cordis.Context, cordis.None) error { return errors.New("boom") })
	needs := cordis.Define("tool.needs", func(*cordis.Context, cordis.None) error { return nil },
		cordis.WithInject("tool.missing"))
	cat, err := catalog.Build([]catalog.Item{
		{Name: "tool.read", Component: read, Config: reflect.TypeFor[readConfig](), Provides: []string{"tool.read"}},
		{Name: "tool.note", Component: note, Config: reflect.TypeFor[readConfig]()},
		{Name: "tool.fail", Component: fail},
		{Name: "tool.needs", Component: needs},
		{Name: "session.scope", Component: scope.Component},
	}, agentpreset.Items(func() agentpreset.Host { return agentpreset.Host{Catalogue: f.cat, Version: "test"} }))
	if err != nil {
		t.Fatal(err)
	}
	f.cat = cat
	return f
}

type env struct {
	root     *cordis.Context
	fx       *fixture
	scopes   scope.Table
	reg      *agentpreset.Registry
	sessions session.Service
	fold     *views.Registry
	view     views.Handle[agentpreset.Binding]
}

func newEnv(t *testing.T, st store.Store) *env {
	t.Helper()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	e := &env{root: root, fx: newFixture(t), scopes: scope.NewTable(root), fold: views.New()}
	var err error
	if e.view, err = agentpreset.RegisterView(e.fold); err != nil {
		t.Fatal(err)
	}
	e.sessions = session.NewService(root, st, session.Config{Folder: e.fold})
	e.reg, err = agentpreset.New(root, agentpreset.Options{Catalogue: e.fx.cat, Scopes: e.scopes, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// agent is one session with its agent scope, which owns the lease.
func (e *env) agent(t *testing.T, id string) (session.Handle, *scope.Scope, agentpreset.Target) {
	t.Helper()
	h, err := e.sessions.Create(context.Background(), nil, session.CreateOptions{Header: session.Header{ID: id}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h.Close(context.Background()) })
	sc, err := e.scopes.New(e.root, "agent:"+id)
	if err != nil {
		t.Fatal(err)
	}
	return h, sc, agentpreset.Target{Session: h, Scope: sc, Owner: sc.Ctx(), Maintainer: &gate{}}
}

// gate is a maintenance gate: a turn takes it to open (as driver.Driver does).
type gate struct{ mu sync.Mutex }

func (g *gate) Maintain(ctx context.Context, fn func(context.Context) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return fn(ctx)
}

func (e *env) binding(t *testing.T, h session.Handle) agentpreset.Binding {
	t.Helper()
	snap, err := e.fold.Read(context.Background(), h, agentpreset.FoldKey)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.view.Get(snap)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func standard(root string) agentpreset.Declaration {
	return agentpreset.Declaration{ID: "standard", Children: map[string]*agentpreset.Child{
		"tool.read": {Config: json.RawMessage(`{"root":"` + root + `"}`)},
	}}
}

func revision(rs []agentpreset.RevisionStatus, id string, n int) (agentpreset.RevisionStatus, bool) {
	for _, r := range rs {
		if r.ID == id && r.Revision == n {
			return r, true
		}
	}
	return agentpreset.RevisionStatus{}, false
}

// TC-09b, DH-D-BUNDLE-4: on the SQLite store, a session bound to standard rev 1
// keeps rev 1 across an edit of standard; rev 1 stays mounted with one lease, a new
// session binds rev 2, and rev 1 unloads at its last release.
func TestSessionKeepsItsRevision(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(ctx) })
	e := newEnv(t, st)
	cfg := agentpreset.Config{Default: "standard"}
	if err := e.reg.Load(cfg, []agentpreset.Declaration{standard("v1")}); err != nil {
		t.Fatal(err)
	}

	h1, sc1, t1 := e.agent(t, "s1")
	rev1, err := e.reg.Bind(ctx, t1, "", agentpreset.CauseCreate)
	if err != nil || rev1.ID != "standard" || rev1.Revision != 1 {
		t.Fatalf("bind s1 = %+v, %v", rev1, err)
	}

	if err := e.reg.Load(cfg, []agentpreset.Declaration{standard("v2")}); err != nil {
		t.Fatal(err)
	}
	if b := e.binding(t, h1); b.Preset != "standard" || b.Digest != rev1.Digest || b.Cause != agentpreset.CauseCreate {
		t.Fatalf("s1's fold after the edit = %+v, want rev 1 (%s)", b, rev1.Digest)
	}
	if v, ok := e.reg.Lookup(sc1.Key(), "tool.read"); !ok || v != "v1" {
		t.Fatalf("s1 sees tool.read = %v %v, want v1", v, ok)
	}
	revs := e.reg.Revisions()
	r1, ok1 := revision(revs, "standard", 1)
	r2, ok2 := revision(revs, "standard", 2)
	if !ok1 || r1.Leases != 1 || !r1.Retired || !ok2 || r2.Leases != 0 || r2.Retired {
		t.Fatalf("revisions = %+v, want rev 1 retired with 1 lease and rev 2 current", revs)
	}
	if n := e.fx.live.Load(); n != 2 {
		t.Fatalf("%d tool.read instances live, want one per revision", n)
	}

	h2, sc2, t2 := e.agent(t, "s2")
	rev2, err := e.reg.Bind(ctx, t2, "", agentpreset.CauseCreate)
	if err != nil || rev2.Revision != 2 || rev2.Digest == rev1.Digest {
		t.Fatalf("bind s2 = %+v, %v", rev2, err)
	}
	if b := e.binding(t, h2); b.Digest != rev2.Digest {
		t.Fatalf("s2's fold = %+v, want rev 2 (%s)", b, rev2.Digest)
	}
	if v, ok := e.reg.Lookup(sc2.Key(), "tool.read"); !ok || v != "v2" {
		t.Fatalf("s2 sees tool.read = %v %v, want v2", v, ok)
	}
	if b := e.binding(t, h1); b.Digest != rev1.Digest {
		t.Fatalf("s1's fold moved to %+v", b)
	}

	if err := sc1.Close(); err != nil {
		t.Fatal(err)
	}
	if _, ok := revision(e.reg.Revisions(), "standard", 1); ok {
		t.Fatalf("rev 1 outlived its last lease: %+v", e.reg.Revisions())
	}
	if n := e.fx.live.Load(); n != 1 {
		t.Fatalf("%d tool.read instances live after rev 1's release, want 1", n)
	}
}

// TC-09b logic: a extends b and b extends a is refused at load, naming both.
func TestExtendsCycleRefused(t *testing.T) {
	e := newEnv(t, memstore.New())
	err := e.reg.Load(agentpreset.Config{Default: "a"}, []agentpreset.Declaration{
		{ID: "a", Extends: "b"}, {ID: "b", Extends: "a"},
	})
	if !errors.Is(err, agentpreset.ErrInvalid) {
		t.Fatalf("load = %v, want agentpreset/invalid", err)
	}
	if !strings.Contains(err.Error(), "a -> b -> a") {
		t.Fatalf("error %q does not name the cycle a -> b -> a", err)
	}
	if len(e.reg.List()) != 0 || len(e.reg.Revisions()) != 0 {
		t.Fatalf("a refused load mounted %+v", e.reg.Revisions())
	}
}

// A broken child lists its revision broken; binding it is refused with
// agentpreset/invalid and leaves no lease; an unknown id lists the available ones.
func TestBrokenAndUnknownPresets(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, memstore.New())
	err := e.reg.Load(agentpreset.Config{Default: "standard"}, []agentpreset.Declaration{
		standard("v1"),
		{ID: "bad", Children: map[string]*agentpreset.Child{"tool.fail": {}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, tg := e.agent(t, "s1")
	if _, err := e.reg.Bind(ctx, tg, "bad", agentpreset.CauseCreate); !errors.Is(err, agentpreset.ErrInvalid) || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("bind bad = %v, want agentpreset/invalid naming the child's error", err)
	}
	if r, ok := revision(e.reg.Revisions(), "bad", 1); !ok || r.Leases != 0 || r.Broken == "" {
		t.Fatalf("bad's revision = %+v %v", r, ok)
	}
	_, err = e.reg.Bind(ctx, tg, "nope", agentpreset.CauseCreate)
	var pe *agentpreset.Error
	if !errors.As(err, &pe) || pe.Code != agentpreset.CodeNotFound || strings.Join(pe.Available, ",") != "bad,standard" {
		t.Fatalf("bind nope = %v, want not-found listing bad,standard", err)
	}
}

// Select rebinds a blank session, is a no-op on the current revision (no event),
// and is refused once the session has opened a turn.
func TestSelect(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, memstore.New())
	other := standard("v1")
	other.ID = "other"
	if err := e.reg.Load(agentpreset.Config{Default: "standard"}, []agentpreset.Declaration{standard("v1"), other}); err != nil {
		t.Fatal(err)
	}
	h, sc, tg := e.agent(t, "s1")
	if _, err := e.reg.Bind(ctx, tg, "", agentpreset.CauseCreate); err != nil {
		t.Fatal(err)
	}
	if info, err := e.reg.Select(ctx, tg, "other"); err != nil || info.ID != "other" {
		t.Fatalf("select other = %+v, %v", info, err)
	}
	if b := e.binding(t, h); b.Preset != "other" || b.Cause != agentpreset.CauseSelect {
		t.Fatalf("fold = %+v", b)
	}
	if r, _ := revision(e.reg.Revisions(), "standard", 1); r.Leases != 0 {
		t.Fatalf("standard kept the old lease: %+v", r)
	}
	if encl := e.scopes.Enclosing(sc.Key()); len(encl) != 1 {
		t.Fatalf("the agent scope encloses %v, want only other's revision", encl)
	}
	evs, _ := h.Read(ctx, 0, 0)
	if _, err := e.reg.Select(ctx, tg, "other"); err != nil {
		t.Fatal(err)
	}
	if again, _ := h.Read(ctx, 0, 0); len(again) != len(evs) {
		t.Fatalf("a no-op select wrote %d events", len(again)-len(evs))
	}
	td, err := event.TurnStartType.Draft(event.TurnStart{Turn: 1})
	if err == nil {
		_, err = h.Append(ctx, td)
	}
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.reg.Select(ctx, tg, "standard"); !errors.Is(err, agentpreset.ErrLocked) {
		t.Fatalf("select after a turn = %v, want agentpreset/locked", err)
	}
}
