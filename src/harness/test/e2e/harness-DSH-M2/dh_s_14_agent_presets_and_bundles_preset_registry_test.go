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
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

type pregReadConfig struct {
	Root string `json:"root"`
}

// pregGate is a maintenance gate, as driver.Driver provides one.
type pregGate struct{ mu sync.Mutex }

func (g *pregGate) Maintain(ctx context.Context, fn func(context.Context) error) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	return fn(ctx)
}

type pregAgent struct {
	h  session.Handle
	sc *scope.Scope
	tg agentpreset.Target
}

type pregState struct {
	ctx     context.Context
	cleanup []func()

	root   *cordis.Context
	cat    *catalog.Catalog
	live   atomic.Int32
	scopes scope.Table
	fold   *views.Registry
	view   views.Handle[agentpreset.Binding]
	sess   session.Service
	reg    *agentpreset.Registry
	cfg    agentpreset.Config

	s1, s2     pregAgent
	rev1, rev2 agentpreset.Info

	decls   []agentpreset.Declaration
	loadErr error
}

func pregStandard(root string) agentpreset.Declaration {
	return agentpreset.Declaration{ID: "standard", Children: map[string]*agentpreset.Child{
		"tool.read": {Config: json.RawMessage(`{"root":"` + root + `"}`)},
	}}
}

func (s *pregState) defer_(f func()) { s.cleanup = append(s.cleanup, f) }

// env builds the registry over st with a catalogue whose tool.read provides its
// configured root as the private service tool.read and counts live instances.
func (s *pregState) env(st store.Store) error {
	s.root = cordis.New()
	s.defer_(s.root.Dispose)
	read := cordis.Define("tool.read", func(ctx *cordis.Context, cfg pregReadConfig) error {
		if _, err := ctx.Effect("live", func() (cordis.Disposer, error) {
			s.live.Add(1)
			return func() error { s.live.Add(-1); return nil }, nil
		}); err != nil {
			return err
		}
		_, err := ctx.Provide("tool.read", cfg.Root)
		return err
	})
	cat, err := catalog.Build([]catalog.Item{
		{Name: "tool.read", Component: read, Config: reflect.TypeFor[pregReadConfig](), Provides: []string{"tool.read"}},
		{Name: "session.scope", Component: scope.Component},
	}, agentpreset.Items(func() agentpreset.Host { return agentpreset.Host{Catalogue: s.cat, Version: "e2e"} }))
	if err != nil {
		return fmt.Errorf("build catalogue: %w", err)
	}
	s.cat = cat
	s.scopes = scope.NewTable(s.root)
	s.fold = views.New()
	if s.view, err = agentpreset.RegisterView(s.fold); err != nil {
		return err
	}
	s.sess = session.NewService(s.root, st, session.Config{Folder: s.fold})
	s.reg, err = agentpreset.New(s.root, agentpreset.Options{Catalogue: s.cat, Scopes: s.scopes, Version: "e2e"})
	return err
}

func (s *pregState) agent(id string) (pregAgent, error) {
	h, err := s.sess.Create(s.ctx, nil, session.CreateOptions{Header: session.Header{ID: id}})
	if err != nil {
		return pregAgent{}, err
	}
	s.defer_(func() { h.Close(context.Background()) })
	sc, err := s.scopes.New(s.root, "agent:"+id)
	if err != nil {
		return pregAgent{}, err
	}
	return pregAgent{h: h, sc: sc, tg: agentpreset.Target{Session: h, Scope: sc, Owner: sc.Ctx(), Maintainer: &pregGate{}}}, nil
}

func (s *pregState) binding(h session.Handle) (agentpreset.Binding, error) {
	snap, err := s.fold.Read(s.ctx, h, agentpreset.FoldKey)
	if err != nil {
		return agentpreset.Binding{}, err
	}
	return s.view.Get(snap)
}

func (s *pregState) revision(id string, n int) (agentpreset.RevisionStatus, bool) {
	for _, r := range s.reg.Revisions() {
		if r.ID == id && r.Revision == n {
			return r, true
		}
	}
	return agentpreset.RevisionStatus{}, false
}

// ---- Session keeps its revision (TC-09b) ----

func (s *pregState) sessionBoundInSQLite(id string, rev int) error {
	dir, err := os.MkdirTemp("", "preg-sqlite-*")
	if err != nil {
		return err
	}
	s.defer_(func() { os.RemoveAll(dir) })
	st, err := sqlite.Open(s.ctx, filepath.Join(dir, "sessions.db"))
	if err != nil {
		return fmt.Errorf("open embedded sqlite: %w", err)
	}
	s.defer_(func() { st.Close(context.Background()) })
	if err := s.env(st); err != nil {
		return err
	}
	s.cfg = agentpreset.Config{Default: id}
	if err := s.reg.Load(s.cfg, []agentpreset.Declaration{pregStandard("v1")}); err != nil {
		return fmt.Errorf("load rev 1: %w", err)
	}
	if s.s1, err = s.agent("s1"); err != nil {
		return err
	}
	s.rev1, err = s.reg.Bind(s.ctx, s.s1.tg, "", agentpreset.CauseCreate)
	if err != nil {
		return fmt.Errorf("bind s1: %w", err)
	}
	if s.rev1.ID != id || s.rev1.Revision != rev {
		return fmt.Errorf("s1 bound %s rev %d, want %s rev %d", s.rev1.ID, s.rev1.Revision, id, rev)
	}
	return nil
}

func (s *pregState) presetEdited(id string, rev int) error {
	if err := s.reg.Load(s.cfg, []agentpreset.Declaration{pregStandard("v2")}); err != nil {
		return fmt.Errorf("load edit: %w", err)
	}
	in, err := s.reg.Resolve(id)
	if err != nil {
		return err
	}
	if in.Revision != rev {
		return fmt.Errorf("after the edit %s is rev %d, want %d", id, in.Revision, rev)
	}
	return nil
}

func (s *pregState) runningFoldReadsRev1() error {
	b, err := s.binding(s.s1.h)
	if err != nil {
		return err
	}
	if b.Preset != "standard" || b.Digest != s.rev1.Digest || b.Cause != agentpreset.CauseCreate {
		return fmt.Errorf("s1's fold = %+v, want standard rev 1 (%s) by create", b, s.rev1.Digest)
	}
	return nil
}

func (s *pregState) runningSees(want string) error {
	if v, ok := s.reg.Lookup(s.s1.sc.Key(), "tool.read"); !ok || v != want {
		return fmt.Errorf("s1 sees tool.read = %v %v, want %s", v, ok, want)
	}
	return nil
}

func (s *pregState) revLeasesRetired(n int, id string, leases int) error {
	r, ok := s.revision(id, n)
	if !ok || r.Leases != leases || !r.Retired {
		return fmt.Errorf("%s rev %d = %+v (found %v), want retired with %d lease(s); all: %+v", id, n, r, ok, leases, s.reg.Revisions())
	}
	return nil
}

func (s *pregState) revLeasesCurrent(n int, id string, leases int) error {
	r, ok := s.revision(id, n)
	if !ok || r.Leases != leases || r.Retired {
		return fmt.Errorf("%s rev %d = %+v (found %v), want current with %d lease(s)", id, n, r, ok, leases)
	}
	if live := s.live.Load(); live != 2 {
		return fmt.Errorf("%d tool.read instances live, want one per revision", live)
	}
	return nil
}

func (s *pregState) newSessionBinds(n int, id string) error {
	var err error
	if s.s2, err = s.agent("s2"); err != nil {
		return err
	}
	s.rev2, err = s.reg.Bind(s.ctx, s.s2.tg, "", agentpreset.CauseCreate)
	if err != nil {
		return fmt.Errorf("bind s2: %w", err)
	}
	if s.rev2.ID != id || s.rev2.Revision != n || s.rev2.Digest == s.rev1.Digest {
		return fmt.Errorf("s2 bound %+v, want %s rev %d with a new digest", s.rev2, id, n)
	}
	return nil
}

func (s *pregState) newFoldReadsRev2(want string) error {
	b, err := s.binding(s.s2.h)
	if err != nil {
		return err
	}
	if b.Preset != "standard" || b.Digest != s.rev2.Digest {
		return fmt.Errorf("s2's fold = %+v, want rev 2 (%s)", b, s.rev2.Digest)
	}
	if v, ok := s.reg.Lookup(s.s2.sc.Key(), "tool.read"); !ok || v != want {
		return fmt.Errorf("s2 sees tool.read = %v %v, want %s", v, ok, want)
	}
	if b1, err := s.binding(s.s1.h); err != nil || b1.Digest != s.rev1.Digest {
		return fmt.Errorf("s1's fold moved to %+v (%v)", b1, err)
	}
	return nil
}

func (s *pregState) rev1UnloadsOnClose(n int, id string) error {
	if err := s.s1.sc.Close(); err != nil {
		return err
	}
	if r, ok := s.revision(id, n); ok {
		return fmt.Errorf("%s rev %d outlived its last lease: %+v", id, n, r)
	}
	if live := s.live.Load(); live != 1 {
		return fmt.Errorf("%d tool.read instances live after rev 1's release, want 1", live)
	}
	return nil
}

// ---- Extends cycle refused (TC-09b logic) ----

func (s *pregState) cyclicDeclarations(a, b, b2, a2 string) error {
	if a != a2 || b != b2 {
		return fmt.Errorf("declarations %s->%s, %s->%s are not a two-cycle", a, b, b2, a2)
	}
	if err := s.env(memstore.New()); err != nil {
		return err
	}
	s.cfg = agentpreset.Config{Default: a}
	s.decls = []agentpreset.Declaration{{ID: a, Extends: b}, {ID: b, Extends: a}}
	return nil
}

func (s *pregState) registryLoads() error {
	s.loadErr = s.reg.Load(s.cfg, s.decls)
	return nil
}

func (s *pregState) loadFailsNamingCycle(cycle string) error {
	if !errors.Is(s.loadErr, agentpreset.ErrInvalid) {
		return fmt.Errorf("load = %v, want agentpreset/invalid", s.loadErr)
	}
	if !strings.Contains(s.loadErr.Error(), cycle) {
		return fmt.Errorf("error %q does not name the cycle %q", s.loadErr, cycle)
	}
	return nil
}

func (s *pregState) errorNamesBoth(a, b string) error {
	msg := s.loadErr.Error()
	if !strings.Contains(msg, a) || !strings.Contains(msg, b) {
		return fmt.Errorf("error %q does not name both %s and %s", msg, a, b)
	}
	return nil
}

func (s *pregState) nothingMounted() error {
	if l, r := s.reg.List(), s.reg.Revisions(); len(l) != 0 || len(r) != 0 {
		return fmt.Errorf("a refused load mounted roster %+v, revisions %+v", l, r)
	}
	return nil
}

func InitializeScenario_dh_s_14_agent_presets_and_bundles_preset_registry(ctx *godog.ScenarioContext) {
	s := &pregState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = pregState{ctx: context.Background()}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for i := len(s.cleanup) - 1; i >= 0; i-- {
			s.cleanup[i]()
		}
		s.cleanup = nil
		return c, nil
	})

	ctx.Step(`^a session bound to preset "([^"]*)" rev (\d+) in an embedded SQLite store$`, s.sessionBoundInSQLite)
	ctx.Step(`^the preset "([^"]*)" is edited to rev (\d+)$`, s.presetEdited)
	ctx.Step(`^the running session's fold still reads rev 1$`, s.runningFoldReadsRev1)
	ctx.Step(`^the running session still sees tool\.read "([^"]*)"$`, s.runningSees)
	ctx.Step(`^rev (\d+) of "([^"]*)" has (\d+) lease and is retired$`, s.revLeasesRetired)
	ctx.Step(`^rev (\d+) of "([^"]*)" has (\d+) leases and is current$`, s.revLeasesCurrent)
	ctx.Step(`^a new session binds rev (\d+) of "([^"]*)"$`, s.newSessionBinds)
	ctx.Step(`^the new session's fold reads rev 2 and sees tool\.read "([^"]*)"$`, s.newFoldReadsRev2)
	ctx.Step(`^rev (\d+) of "([^"]*)" unloads when the running session's scope closes$`, s.rev1UnloadsOnClose)

	ctx.Step(`^in-memory declarations "([^"]*)" extends "([^"]*)" and "([^"]*)" extends "([^"]*)"$`, s.cyclicDeclarations)
	ctx.Step(`^the registry loads$`, s.registryLoads)
	ctx.Step(`^the load fails with agentpreset/invalid naming the cycle "([^"]*)"$`, s.loadFailsNamingCycle)
	ctx.Step(`^the error names both "([^"]*)" and "([^"]*)"$`, s.errorNamesBoth)
	ctx.Step(`^the refused load mounted no revision$`, s.nothingMounted)
}

func TestE2E_dh_s_14_agent_presets_and_bundles_preset_registry(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_14_agent_presets_and_bundles_preset_registry,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_14_agent_presets_and_bundles_preset_registry.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("preset registry e2e scenarios failed")
	}
}
