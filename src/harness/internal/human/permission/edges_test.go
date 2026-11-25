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
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// flakySandbox is the world's resolver with a failing Mode or SetMode.
type flakySandbox struct {
	permission.Sandbox
	modeErr, setErr error
}

func (s *flakySandbox) Mode(ctx context.Context, id session.ID) (sandbox.Mode, error) {
	if s.modeErr != nil {
		return "", s.modeErr
	}
	return s.Sandbox.Mode(ctx, id)
}

func (s *flakySandbox) SetMode(ctx context.Context, id session.ID, m sandbox.Mode) error {
	if s.setErr != nil {
		return s.setErr
	}
	return s.Sandbox.SetMode(ctx, id, m)
}

// flakyApproval is the world's approval service with a failing SetPolicy.
type flakyApproval struct {
	permission.Approval
	setErr error
}

func (a *flakyApproval) SetPolicy(ctx context.Context, ag agent.Ref, p approval.Policy, by approval.By) error {
	if a.setErr != nil {
		return a.setErr
	}
	return a.Approval.SetPolicy(ctx, ag, p, by)
}

// flakyLog is a live session whose Read or Append fails.
type flakyLog struct {
	session.Session
	readErr, appendErr error
}

func (s *flakyLog) Read(ctx context.Context, from session.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error) {
	if s.readErr != nil {
		return nil, s.readErr
	}
	return s.Session.Read(ctx, from, limit, opts...)
}

func (s *flakyLog) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	if s.appendErr != nil {
		return event.Receipt{}, s.appendErr
	}
	return s.Session.Append(ctx, d)
}

// sessionsOf serves exactly the sessions it holds.
type sessionsOf map[session.ID]session.Session

func (m sessionsOf) Get(id session.ID) (session.Session, bool) {
	s, ok := m[id]
	return s, ok
}

// over is a service over w's knob owners with the given replacements.
func over(t *testing.T, w *world, sessions permission.Sessions, sb permission.Sandbox, apr permission.Approval) *permission.Service {
	t.Helper()
	if sessions == nil {
		sessions = w.sessions
	}
	if sb == nil {
		sb = w.resolver
	}
	if apr == nil {
		apr = w.approval
	}
	svc, err := permission.New(table(), permission.Deps{Sessions: sessions, Sandbox: sb, Approval: apr})
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

// New refuses a service without its sessions, sandbox or approval owner, and one
// whose fold registry already holds another fold under the permissions key.
func TestNewRefusesMissingCollaborators(t *testing.T) {
	w := newWorld(t, table())
	for name, d := range map[string]permission.Deps{
		"no sessions": {Sandbox: w.resolver, Approval: w.approval},
		"no sandbox":  {Sessions: w.sessions, Approval: w.approval},
		"no approval": {Sessions: w.sessions, Sandbox: w.resolver},
	} {
		if _, err := permission.New(table(), d); err == nil {
			t.Errorf("New with %s succeeded", name)
		}
	}
	reg := views.New()
	if _, err := views.Register(reg, views.Definition[int]{Key: permission.ViewKey, Version: 7,
		Init:  func(session.Header) int { return 0 },
		Apply: func(s int, _ event.Event) (int, bool) { return s, false }}); err != nil {
		t.Fatal(err)
	}
	if _, err := permission.New(table(), permission.Deps{Sessions: w.sessions, Sandbox: w.resolver, Approval: w.approval, Views: reg}); err == nil || !strings.Contains(err.Error(), "register the permissions fold") {
		t.Fatalf("New over a registry holding another permissions fold = %v", err)
	}
	if svc, err := permission.New(table(), permission.Deps{Sessions: w.sessions, Sandbox: w.resolver, Approval: w.approval}); err != nil || svc.Handle() != (views.Handle[permission.State]{}) {
		t.Fatalf("New without a fold registry = %v, handle %v; want the zero handle", err, svc.Handle())
	}
}

// A layer's presets replace the table whole, so an entry that leaves out its mode
// and approval is refused rather than inheriting the shipped entry's; a layer
// without presets keeps the table; a layer that does not decode is an error.
func TestConfigLayerReplacesTheTableWhole(t *testing.T) {
	c := permission.DefaultConfig()
	if err := json.Unmarshal([]byte(`{"presets":[{"name":"x"}],"defaultPreset":"x"}`), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Presets) != 1 || c.Presets[0].Mode != "" || c.Presets[0].Approval != "" {
		t.Fatalf("decoded %+v, want the one entry with no inherited mode or policy", c.Presets)
	}
	var ce *cordis.ConfigError
	if err := c.Validate(); !errors.As(err, &ce) || len(ce.Issues) != 2 || ce.Issues[0].Path != "presets.0.mode" || ce.Issues[1].Path != "presets.0.approval" {
		t.Fatalf("validate = %v, want the mode and the approval of presets.0 refused", err)
	}

	c = permission.DefaultConfig()
	if err := json.Unmarshal([]byte(`{"defaultPreset":"read-only"}`), &c); err != nil {
		t.Fatal(err)
	}
	if len(c.Presets) != 3 || c.DefaultPreset != permission.PresetReadOnly || c.Validate() != nil {
		t.Fatalf("a layer without presets gave %+v", c)
	}
	if err := json.Unmarshal([]byte(`{"presets":1}`), &c); err == nil {
		t.Fatal("a layer whose presets are not a list decoded")
	}
	if err := (permission.Config{Presets: []permission.Preset{{Name: "Bad Name", Mode: sandbox.ReadOnly, Approval: approval.PolicyAsk}}, DefaultPreset: "Bad Name"}).Validate(); !errors.As(err, &ce) || ce.Issues[0].Path != "presets.0.name" {
		t.Fatalf("a name outside the command alphabet = %v", err)
	}
}

// The tree check skips a config that does not decode and places an entry with no
// id at its catalogue name.
func TestCheckTreeEdges(t *testing.T) {
	check := permission.CheckTree()
	if is := check([]loader.EntryOptions{{ID: "p", Name: permission.Name, Config: json.RawMessage(`{"presets":1}`)}}); len(is) != 0 {
		t.Fatalf("an undecodable config raised %+v", is)
	}
	is := check([]loader.EntryOptions{{Name: permission.Name, Config: json.RawMessage(`{"defaultPreset":""}`)}})
	if len(is) != 1 || is[0].Path != permission.Name+".config.defaultPreset" {
		t.Fatalf("issues %+v, want one at %s.config.defaultPreset", is, permission.Name)
	}
}

// A switch, a read or a pin needs a live session, an agent and a principal; a knob
// owner that cannot read or write fails the operation with its error, naming the
// knob.
func TestKnobOwnerFailures(t *testing.T) {
	w := newWorld(t, table())
	h := w.live(t, me.Session)
	if err := w.svc.Pin(bg, me); err != nil {
		t.Fatal(err)
	}
	alicePrincipal := approval.By{Principal: "alice"}
	gone := agent.Ref{Session: "s-gone", Incarnation: 1}
	if _, err := w.svc.Current(bg, gone.Session); !errors.Is(err, permission.ErrNoSession) {
		t.Errorf("current of a session not live = %v", err)
	}
	if err := w.svc.Pin(bg, gone); !errors.Is(err, permission.ErrNoSession) {
		t.Errorf("pin of a session not live = %v", err)
	}
	if _, err := w.svc.Set(bg, agent.Ref{}, "auto", alicePrincipal); err == nil {
		t.Error("a switch without an agent ran")
	}
	if _, err := w.svc.Set(bg, me, "auto", approval.By{}); err == nil {
		t.Error("a switch without a principal ran")
	}

	blind := over(t, w, nil, &flakySandbox{Sandbox: w.resolver, modeErr: errors.New("resolver down")}, nil)
	if _, err := blind.Current(bg, me.Session); err == nil || !strings.Contains(err.Error(), "read the sandbox mode") {
		t.Errorf("current with an unreadable mode = %v", err)
	}
	if _, err := blind.Set(bg, me, "auto", alicePrincipal); err == nil || !strings.Contains(err.Error(), "read the sandbox mode") {
		t.Errorf("switch with an unreadable mode = %v", err)
	}

	n := len(types(t, h, 0))
	stuck := over(t, w, nil, nil, &flakyApproval{Approval: w.approval, setErr: errors.New("policy locked")})
	if _, err := stuck.Set(bg, me, "auto", alicePrincipal); err == nil || !strings.Contains(err.Error(), "set the approval policy") {
		t.Fatalf("switch with a refused policy write = %v", err)
	}
	// The selection and the mode landed, the policy did not: Current reads the
	// knobs in force, which no entry matches.
	if got := types(t, h, n); !slices.Equal(got, []string{permission.PresetType.Name(), sandbox.ModeEventType}) {
		t.Fatalf("a switch whose policy write failed appended %v", got)
	}
	if cur, err := w.svc.Current(bg, me.Session); err != nil || cur != permission.Custom {
		t.Fatalf("current after a half switch = %q, %v; want custom", cur, err)
	}
}

// A log that cannot be read fails a read, a switch and a pin; a selection that
// cannot be appended writes no knob.
func TestLogFailures(t *testing.T) {
	w := newWorld(t, table())
	h := w.live(t, me.Session)
	unread := over(t, w, sessionsOf{me.Session: &flakyLog{Session: h, readErr: errors.New("log unreadable")}}, nil, nil)
	if _, err := unread.Current(bg, me.Session); err == nil || !strings.Contains(err.Error(), "read the log") {
		t.Errorf("current over an unreadable log = %v", err)
	}
	if _, err := unread.Set(bg, me, "auto", approval.By{Principal: "alice"}); err == nil || !strings.Contains(err.Error(), "read the log") {
		t.Errorf("switch over an unreadable log = %v", err)
	}
	if err := unread.Pin(bg, me); err == nil || !strings.Contains(err.Error(), "read the log") {
		t.Errorf("pin over an unreadable log = %v", err)
	}

	full := over(t, w, sessionsOf{me.Session: &flakyLog{Session: h, appendErr: errors.New("disk full")}}, nil, nil)
	if err := full.Pin(bg, me); err == nil || !strings.Contains(err.Error(), "append permission/preset") {
		t.Fatalf("pin over a full log = %v", err)
	}
	if got := types(t, h, 0); len(got) != 0 {
		t.Fatalf("a refused selection still wrote %v", got)
	}
	if m, err := w.resolver.Mode(bg, me.Session); err != nil || m != sandbox.ReadOnly {
		t.Fatalf("mode after a refused selection = %s, %v; want the default read-only", m, err)
	}
}

// Pin writes nothing on a session that is not fresh and owes no knob: one whose
// last selection is not a pin, or whose knobs were written with no selection.
func TestPinLeavesAStartedSessionAlone(t *testing.T) {
	w := newWorld(t, table())
	h := w.live(t, me.Session)
	d, err := permission.PresetType.Draft(permission.PresetSet{Preset: "auto", Mode: sandbox.DangerFullAccess,
		Approval: approval.PolicyNever, By: approval.By{Principal: "alice"}})
	if err == nil {
		_, err = h.Append(bg, d)
	}
	if err != nil {
		t.Fatal(err)
	}
	n := len(types(t, h, 0))
	if err := w.svc.Pin(bg, me); err != nil || len(types(t, h, 0)) != n {
		t.Fatalf("pin after a selection: %v, the log grew to %v", err, types(t, h, 0))
	}

	other := agent.Ref{Session: "s-knob-only", Incarnation: 1}
	h2 := w.live(t, other.Session)
	if err := w.resolver.SetMode(bg, other.Session, sandbox.DangerFullAccess); err != nil {
		t.Fatal(err)
	}
	n = len(types(t, h2, 0))
	if err := w.svc.Pin(bg, other); err != nil || len(types(t, h2, 0)) != n {
		t.Fatalf("pin after a direct knob write: %v, the log grew to %v", err, types(t, h2, 0))
	}
	if m, _ := w.resolver.Mode(bg, other.Session); m != sandbox.DangerFullAccess {
		t.Fatalf("pin moved the directly written mode to %s", m)
	}
}

// Bind pins only a session the service can reach: an agent whose session is not
// among its sessions is created unpinned.
func TestBindSkipsASessionItCannotReach(t *testing.T) {
	w := newWorld(t, table())
	blind := over(t, w, sessionsOf{}, nil, nil)
	if _, err := blind.Bind(w.root); err != nil {
		t.Fatal(err)
	}
	a, err := lifecycle(t, w).Create(w.root, agent.CreateOptions{Session: "s-unreached"})
	if err != nil {
		t.Fatalf("creation: %v", err)
	}
	t.Cleanup(func() { _ = a.Dispose(bg) })
	h, _ := w.sessions.Get(a.Agent().Ref().Session)
	if got := types(t, h, 0); len(got) != 0 {
		t.Fatalf("an unreached session was written: %v", got)
	}
}

// /permission attributes a switch to the actor by name, else by channel, else as
// unknown; an unknown preset and a session that is not live are its errors.
func TestCommandAttributionAndErrors(t *testing.T) {
	w := newWorld(t, table())
	h := w.live(t, me.Session)
	if err := w.svc.Pin(bg, me); err != nil {
		t.Fatal(err)
	}
	run := w.svc.Command().Run
	for _, tc := range []struct {
		by   human.Actor
		to   string
		want approval.By
	}{
		{human.Actor{Channel: "ui", Name: "alice", Peer: "p1"}, "auto", approval.By{Principal: "alice", Channel: "ui", Peer: "p1"}},
		{human.Actor{Channel: "tui"}, "locked", approval.By{Principal: "tui", Channel: "tui"}},
		{human.Actor{}, "ask", approval.By{Principal: "unknown"}},
	} {
		out, err := run(bg, command.Call{Agent: me, By: tc.by, Rest: " " + tc.to + " "})
		if err != nil || !strings.HasPrefix(out, "permission preset "+tc.to+":") {
			t.Fatalf("/permission %s by %+v = %q, %v", tc.to, tc.by, out, err)
		}
		if st := w.state(t, h); st.Selected != tc.to || st.By != tc.want {
			t.Fatalf("/permission %s by %+v recorded %+v, want by %+v", tc.to, tc.by, st, tc.want)
		}
	}
	if _, err := run(bg, command.Call{Agent: me, By: alice, Rest: "ghost"}); !errors.Is(err, permission.ErrUnknownPreset) {
		t.Errorf("/permission ghost = %v", err)
	}
	if _, err := run(bg, command.Call{Agent: agent.Ref{Session: "s-gone", Incarnation: 1}, By: alice}); !errors.Is(err, permission.ErrNoSession) {
		t.Errorf("/permission on a session not live = %v", err)
	}
}

// The component accepts an update of its table in place: the service is not
// replaced and no session's knobs change; a session whose selection left the table
// reads the first entry its knobs match, else custom.
func TestUpdateSwapsTheTableInPlace(t *testing.T) {
	w := newWorld(t, table())
	reg := views.New()
	cmds := command.NewRegistry(scope.NewTable(w.root))
	providers, err := w.root.Apply("test.providers", func(c *cordis.Context) error {
		if _, err := session.Key.Provide(c, w.sessions); err != nil {
			return err
		}
		if _, err := views.Key.Provide(c, reg); err != nil {
			return err
		}
		if _, err := sandbox.ResolverKey.Provide(c, sandbox.Resolver(w.resolver)); err != nil {
			return err
		}
		if _, err := approval.Key.Provide(c, w.approval); err != nil {
			return err
		}
		_, err := command.Key.Provide(c, cmds)
		return err
	})
	if err == nil {
		err = providers.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	comp, err := w.root.Use(permission.Component, table())
	if err == nil {
		err = comp.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
	svc, err := permission.Key.Get(w.root)
	if err != nil {
		t.Fatal(err)
	}
	a, err := lifecycle(t, w).Create(w.root, agent.CreateOptions{Session: "s-update"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Dispose(bg) })
	ref := a.Agent().Ref()
	h, _ := w.sessions.Get(ref.Session)
	n := len(types(t, h, 0))

	update := func(c permission.Config) {
		t.Helper()
		if err := comp.Update(c, true); err != nil {
			t.Fatal(err)
		}
		if now, err := permission.Key.Get(w.root); err != nil || now != svc {
			t.Fatalf("the update replaced the service: %v", err)
		}
	}
	update(permission.Config{DefaultPreset: "careful", Presets: []permission.Preset{
		{Name: "open", Mode: sandbox.DangerFullAccess, Approval: approval.PolicyNever},
		{Name: "careful", Mode: sandbox.WorkspaceWrite, Approval: approval.PolicyAsk},
	}})
	if d := svc.Default(); d.Name != "careful" || len(svc.Presets()) != 2 {
		t.Fatalf("after the update: default %+v, table %+v", d, svc.Presets())
	}
	if _, ok := svc.Lookup("ask"); ok {
		t.Fatal("the old entry ask is still in the table")
	}
	if cur, err := svc.Current(bg, ref.Session); err != nil || cur != "careful" {
		t.Fatalf("current after ask left the table = %q, %v; want careful, the knobs' match", cur, err)
	}
	update(permission.Config{DefaultPreset: "open", Presets: []permission.Preset{
		{Name: "open", Mode: sandbox.DangerFullAccess, Approval: approval.PolicyNever},
	}})
	if cur, err := svc.Current(bg, ref.Session); err != nil || cur != permission.Custom {
		t.Fatalf("current when no entry matches = %q, %v; want custom", cur, err)
	}
	if got := types(t, h, 0); len(got) != n {
		t.Fatalf("an update of the table wrote %v", got[n:])
	}
	if m, err := w.resolver.Mode(bg, ref.Session); err != nil || m != sandbox.WorkspaceWrite || w.approval.Policy(ref.Session) != approval.PolicyAsk {
		t.Fatalf("knobs after the update: %s, %v, %s", m, err, w.approval.Policy(ref.Session))
	}
}
