package permission_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/permission"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)

var (
	bg    = context.Background()
	me    = agent.Ref{Session: "s-permission", Incarnation: 1}
	alice = human.Actor{Channel: "ui", Name: "alice", Peer: "p1"}
)

// table is the TC-02a preset table: ask (workspace-write, ask) is the default
// and auto (danger-full-access, never) the one switched to.
func table() permission.Config {
	return permission.Config{DefaultPreset: "ask", Presets: []permission.Preset{
		{Name: "ask", Mode: sandbox.WorkspaceWrite, Approval: approval.PolicyAsk},
		{Name: "auto", Mode: sandbox.DangerFullAccess, Approval: approval.PolicyNever},
		{Name: "locked", Mode: sandbox.ReadOnly, Approval: approval.PolicyAsk},
	}}
}

type world struct {
	root     *cordis.Context
	sessions session.Service
	resolver *policy.Resolver
	approval *approval.Service
	grants   *approval.Grants
	svc      *permission.Service
}

func owner(t *testing.T, root *cordis.Context, name string) *cordis.Context {
	t.Helper()
	var octx *cordis.Context
	f, err := root.Apply(name, func(c *cordis.Context) error { octx = c; return nil })
	if err == nil {
		err = f.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	return octx
}

func newWorld(t *testing.T, cfg permission.Config) *world {
	t.Helper()
	w := &world{root: cordis.New()}
	t.Cleanup(w.root.Dispose)
	w.sessions = session.NewService(w.root, memstore.New(), session.Config{})
	var err error
	if w.resolver, err = policy.New(policy.Config{}, policy.Deps{Owner: owner(t, w.root, "world.sandbox.policy"), Sessions: w.sessions}); err != nil {
		t.Fatal(err)
	}
	w.grants = approval.NewGrants()
	w.approval = approval.New(approval.Config{Registry: human.New(), Grants: w.grants, Sessions: w.sessions})
	if w.svc, err = permission.New(cfg, permission.Deps{Sessions: w.sessions, Sandbox: w.resolver, Approval: w.approval, Grants: w.grants}); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *world) live(t *testing.T, id session.ID) session.Handle {
	t.Helper()
	h, err := w.sessions.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(bg) })
	return h
}

func types(t *testing.T, h session.ReadSession, from int) []string {
	t.Helper()
	evs, err := h.Read(bg, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range evs[from:] {
		out = append(out, e.Type())
	}
	return out
}

// pinSeq is the seq of the first permission/preset of h, the pin.
func pinSeq(t *testing.T, h session.ReadSession) event.Seq {
	t.Helper()
	evs, err := h.Read(bg, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Type() == permission.PresetType.Name() {
			if p, _ := permission.PresetType.As(e); !p.Pinned {
				t.Fatalf("the first permission/preset %+v is not the pin", p)
			}
			return e.Seq()
		}
	}
	t.Fatal("no permission/preset")
	return 0
}

func count(xs []string, x string) int {
	n := 0
	for _, s := range xs {
		if s == x {
			n++
		}
	}
	return n
}

// TC-02a, DH-D-HUMAN-4: on a session pinned to ask, /permission auto run by the
// owner appends exactly one sandbox/mode and then one approval/policy, and the
// sandbox-mode, approval and permissions views all read auto. A second
// /permission auto writes nothing; anyone but the owner is refused.
func TestSwitchMovesModeAndApprovalTogether(t *testing.T) {
	w := newWorld(t, table())
	h := w.live(t, me.Session)
	if err := w.svc.Pin(bg, me); err != nil {
		t.Fatal(err)
	}
	if cur, err := w.svc.Current(bg, me.Session); err != nil || cur != "ask" {
		t.Fatalf("pinned preset = %q, %v; want ask", cur, err)
	}
	from := len(types(t, h, 0))

	tab := scope.NewTable(w.root)
	cmds := command.NewRegistry(tab)
	if _, err := w.svc.RegisterCommand(w.root, cmds); err != nil {
		t.Fatal(err)
	}
	sc, err := tab.New(w.root, "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	target := command.Target{Agent: me, Scope: sc.Key(), Log: h, Owner: "alice"}

	res, err := cmds.Execute(bg, target, alice, "/permission auto")
	if err != nil || res.Outcome != command.OutcomeOK {
		t.Fatalf("/permission auto: %+v, %v", res, err)
	}
	got := types(t, h, from)
	if count(got, sandbox.ModeEventType) != 1 || count(got, approval.PolicyType.Name()) != 1 {
		t.Fatalf("appended %v; want exactly one sandbox/mode and one approval/policy", got)
	}
	if slices.Index(got, sandbox.ModeEventType) > slices.Index(got, approval.PolicyType.Name()) {
		t.Fatalf("appended %v; want sandbox/mode before approval/policy", got)
	}
	if count(got, permission.PresetType.Name()) != 1 {
		t.Fatalf("appended %v; want one permission/preset", got)
	}

	reg := views.New()
	modes, err := policy.RegisterView(reg)
	if err != nil {
		t.Fatal(err)
	}
	policies, err := approval.RegisterView(reg)
	if err != nil {
		t.Fatal(err)
	}
	perms, err := permission.RegisterView(reg)
	if err != nil {
		t.Fatal(err)
	}
	read := func(key string) views.Snapshot {
		snap, err := reg.Read(bg, h, key)
		if err != nil {
			t.Fatal(err)
		}
		return snap
	}
	if st, err := modes.Get(read(policy.ModeKey)); err != nil || st.Mode != sandbox.DangerFullAccess || st.By.Principal != "alice" {
		t.Fatalf("sandbox-mode view %+v, %v", st, err)
	}
	if st, err := policies.Get(read(approval.PolicyKey)); err != nil || st.Policy != approval.PolicyNever || st.By.Principal != "alice" {
		t.Fatalf("approval view %+v, %v", st, err)
	}
	if st, err := perms.Get(read(permission.ViewKey)); err != nil || st.Preset != "auto" || st.Mode != sandbox.DangerFullAccess || st.Approval != approval.PolicyNever || !st.Pinned || st.PinnedAt != pinSeq(t, h) {
		t.Fatalf("permissions view %+v, %v", st, err)
	}
	if m, err := w.resolver.Mode(bg, me.Session); err != nil || m != sandbox.DangerFullAccess || w.approval.Policy(me.Session) != approval.PolicyNever {
		t.Fatalf("knobs in force: %s, %v, %s", m, err, w.approval.Policy(me.Session))
	}
	if cur, err := w.svc.Current(bg, me.Session); err != nil || cur != "auto" {
		t.Fatalf("current = %q, %v; want auto", cur, err)
	}

	n := len(types(t, h, 0))
	if _, err := w.svc.Set(bg, me, "auto", approval.By{Principal: "alice"}); err != nil || len(types(t, h, 0)) != n {
		t.Fatalf("a switch to the preset in force: %v, the log grew", err)
	}
	if _, err := cmds.Execute(bg, target, human.Actor{Channel: "ui", Name: "mallory"}, "/permission ask"); !errors.Is(err, command.ErrForbidden) {
		t.Fatalf("a non-owner's switch: %v, want %v", err, command.ErrForbidden)
	}
	res, err = cmds.Execute(bg, target, alice, "/permission")
	if err != nil || !strings.Contains(res.Output, "permission preset: auto") || !strings.Contains(res.Output, "* auto:") {
		t.Fatalf("/permission lists %+v, %v", res, err)
	}
}

// A switch to a name outside the table is refused and writes nothing; a switch
// that only moves the mode appends no approval/policy.
func TestSwitchEdges(t *testing.T) {
	w := newWorld(t, table())
	h := w.live(t, me.Session)
	if err := w.svc.Pin(bg, me); err != nil {
		t.Fatal(err)
	}
	n := len(types(t, h, 0))
	if _, err := w.svc.Set(bg, me, "ghost", approval.By{Principal: "alice"}); !errors.Is(err, permission.ErrUnknownPreset) || len(types(t, h, 0)) != n {
		t.Fatalf("unknown preset: %v", err)
	}
	if _, err := w.svc.Set(bg, me, "locked", approval.By{Principal: "alice"}); err != nil {
		t.Fatal(err)
	}
	if got := types(t, h, n); !slices.Equal(got, []string{permission.PresetType.Name(), sandbox.ModeEventType}) {
		t.Fatalf("mode-only switch appended %v", got)
	}
	if _, err := w.svc.Set(bg, agent.Ref{Session: "s-gone", Incarnation: 1}, "auto", approval.By{Principal: "alice"}); !errors.Is(err, permission.ErrNoSession) {
		t.Fatalf("a session not live: %v", err)
	}
}

// lifeAgent is a stub agent whose session is the real one of its id: created the
// first time, resumed after.
type lifeAgent struct {
	ref agent.Ref
	h   session.Handle
	sc  *scope.Scope
}

func (a *lifeAgent) Ref() agent.Ref               { return a.ref }
func (a *lifeAgent) Session() session.ReadSession { return a.h }
func (a *lifeAgent) Scope() *cordis.Context       { return a.sc.Ctx() }
func (a *lifeAgent) ScopeKey() scope.Key          { return a.sc.Key() }
func (a *lifeAgent) Status() agent.Status         { return agent.StatusIdle }
func (a *lifeAgent) State() agent.State           { return agent.StateLive }
func (a *lifeAgent) Close(ctx context.Context) error {
	_ = a.sc.Close()
	return a.h.Close(ctx)
}

func lifecycle(t *testing.T, w *world) agent.Registry {
	t.Helper()
	tab := scope.NewTable(w.root)
	reg := agent.NewRegistry(w.root)
	opened := map[session.ID]bool{}
	if _, err := reg.SetFactory(w.root, agent.FactoryFunc(func(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
		var h session.Handle
		var err error
		if opened[o.Session] {
			h, _, err = w.sessions.Open(bg, owner, o.Session, session.OpenOptions{})
		} else {
			h, err = w.sessions.Create(bg, owner, session.CreateOptions{Header: session.Header{ID: string(o.Session)}})
		}
		if err != nil {
			return nil, err
		}
		opened[o.Session] = true
		sc, err := tab.New(owner, ref.String())
		if err != nil {
			return nil, err
		}
		return &lifeAgent{ref: ref, h: h, sc: sc}, nil
	})); err != nil {
		t.Fatal(err)
	}
	return reg
}

// DH-D-HUMAN-4: creating an agent on a fresh session pins it to the default
// preset (by the harness, marked pinned) and writes both knobs; resuming it
// keeps the values it had, a switch included, and writes nothing.
func TestCreationPinsAFreshSession(t *testing.T) {
	w := newWorld(t, table())
	if _, err := w.svc.Bind(w.root); err != nil {
		t.Fatal(err)
	}
	reg := lifecycle(t, w)
	a1, err := reg.Create(w.root, agent.CreateOptions{Session: "s-pin"})
	if err != nil {
		t.Fatal(err)
	}
	ref := a1.Agent().Ref()
	h, ok := w.sessions.Get(ref.Session)
	if !ok {
		t.Fatal("the agent's session is not live")
	}
	if got := types(t, h, 0); !slices.Contains(got, permission.PresetType.Name()) || count(got, sandbox.ModeEventType) != 1 || count(got, approval.PolicyType.Name()) != 0 {
		// ask is the approval service's default policy, so the pin writes no approval/policy.
		t.Fatalf("pin appended %v", got)
	}
	if m, err := w.resolver.Mode(bg, ref.Session); err != nil || m != sandbox.WorkspaceWrite {
		t.Fatalf("pinned mode %s, %v", m, err)
	}
	if _, err := w.svc.Set(bg, ref, "auto", approval.By{Principal: "alice"}); err != nil {
		t.Fatal(err)
	}
	if err := a1.Dispose(bg); err != nil {
		t.Fatal(err)
	}
	a2, err := reg.Create(w.root, agent.CreateOptions{Session: "s-pin"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a2.Dispose(bg) })
	h2, _ := w.sessions.Get(a2.Agent().Ref().Session)
	if got := types(t, h2, 0); count(got, permission.PresetType.Name()) != 2 {
		t.Fatalf("resume re-pinned: %v", got)
	}
	if cur, err := w.svc.Current(bg, a2.Agent().Ref().Session); err != nil || cur != "auto" {
		t.Fatalf("resumed preset %q, %v; want auto", cur, err)
	}
}

// TC-02a: a table whose default names no preset is refused with a configuration
// error naming the preset at defaultPreset; so are auto as the default, a
// preset named custom, a duplicate name and an unknown mode or policy.
func TestValidateRefusesABrokenTable(t *testing.T) {
	if err := permission.DefaultConfig().Validate(); err != nil {
		t.Fatalf("the default table: %v", err)
	}
	if err := table().Validate(); err != nil {
		t.Fatalf("the test table: %v", err)
	}
	ghost := permission.DefaultConfig()
	ghost.DefaultPreset = "ghost"
	err := ghost.Validate()
	var ce *cordis.ConfigError
	if !errors.As(err, &ce) || !strings.Contains(err.Error(), `"ghost"`) {
		t.Fatalf("unknown default: %v", err)
	}
	if !slices.ContainsFunc(ce.Issues, func(i cordis.ConfigIssue) bool { return i.Path == "defaultPreset" }) {
		t.Fatalf("issues %+v do not name defaultPreset", ce.Issues)
	}
	if _, err := permission.New(ghost, permission.Deps{}); !errors.As(err, &ce) {
		t.Fatalf("New over an unknown default: %v", err)
	}

	cases := map[string]func(*permission.Config){
		"auto default": func(c *permission.Config) {
			c.Presets = append(c.Presets, permission.Preset{Name: permission.Auto, Mode: sandbox.DangerFullAccess, Approval: approval.PolicyNever})
			c.DefaultPreset = permission.Auto
		},
		"empty default": func(c *permission.Config) { c.DefaultPreset = "" },
		"custom name":   func(c *permission.Config) { c.Presets[0].Name = permission.Custom },
		"duplicate":     func(c *permission.Config) { c.Presets[1].Name = c.Presets[0].Name },
		"bad mode":      func(c *permission.Config) { c.Presets[0].Mode = "wide-open" },
		"bad policy":    func(c *permission.Config) { c.Presets[0].Approval = "sometimes" },
		"empty table":   func(c *permission.Config) { c.Presets = nil },
	}
	for name, mutate := range cases {
		c := permission.DefaultConfig()
		mutate(&c)
		if err := c.Validate(); !errors.As(err, &ce) {
			t.Errorf("%s: %v, want a configuration error", name, err)
		}
	}
}

// The tree check places an unresolvable default at its entry's
// config.defaultPreset, nested ids included; a valid, an omitted, a disabled and
// an expression config raise nothing.
func TestCheckTreePlacesAnUnresolvableDefault(t *testing.T) {
	check := permission.CheckTree()
	bad := loader.EntryOptions{ID: "permissions", Name: permission.Name, Config: json.RawMessage(`{"defaultPreset":"ghost"}`)}
	issues := check([]loader.EntryOptions{{ID: "human", Group: true, Children: []loader.EntryOptions{bad}}})
	if len(issues) != 1 || issues[0].Path != "human"+loader.Separator+"permissions.config.defaultPreset" || !strings.Contains(issues[0].Message, `"ghost"`) {
		t.Fatalf("issues %+v", issues)
	}
	quiet := []loader.EntryOptions{
		{ID: "a", Name: permission.Name},
		{ID: "b", Name: permission.Name, Config: json.RawMessage(`{"defaultPreset":"read-only"}`)},
		{ID: "c", Name: permission.Name, Disabled: true, Config: bad.Config},
		{ID: "d", Name: permission.Name, Config: json.RawMessage(`{"defaultPreset":"${{ env.PRESET }}"}`)},
		{ID: "e", Name: "other", Config: bad.Config},
	}
	if issues := check(quiet); len(issues) != 0 {
		t.Fatalf("issues %+v", issues)
	}
}
