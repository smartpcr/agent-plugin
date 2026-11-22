package approval_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
)

// granting is a service with a grant table whose agents are bound to channel ch,
// answered by a scripted answerer.
func granting(t *testing.T, ch human.Channel, refs []agent.Ref, replies ...answerer.Reply) (*approval.Service, *approval.Grants, *answerer.Answerer, *cordis.Context) {
	t.Helper()
	root := cordis.New()
	t.Cleanup(func() { root.Dispose() })
	reg := human.New()
	a := answerer.Register(t, root, reg, ch, replies...)
	for _, r := range refs {
		if _, err := reg.Bind(root, r, human.Binding{Channel: ch}); err != nil {
			t.Fatal(err)
		}
	}
	g := approval.NewGrants()
	return approval.New(approval.Config{Registry: reg, Grants: g}), g, a, root
}

func dirReq(ref agent.Ref, call, path string) approval.Request {
	return approval.Request{Agent: ref, Tool: "write", Call: call, Origin: "model", Reason: "writes",
		Subject: &approval.Subject{Kind: approval.ScopeDirectory, Path: path}}
}

// lifeAgent is a stub agent instance whose session is the real one of its id:
// created the first time, resumed (session.Open) after.
type lifeAgent struct {
	ref agent.Ref
	h   session.Handle
	sc  *scope.Scope
	// keep leaves the session live when the agent closes.
	keep bool
}

func (a *lifeAgent) Ref() agent.Ref               { return a.ref }
func (a *lifeAgent) Session() session.ReadSession { return a.h }
func (a *lifeAgent) Scope() *cordis.Context       { return a.sc.Ctx() }
func (a *lifeAgent) ScopeKey() scope.Key          { return a.sc.Key() }
func (a *lifeAgent) Status() agent.Status         { return agent.StatusIdle }
func (a *lifeAgent) State() agent.State           { return agent.StateLive }
func (a *lifeAgent) Close(ctx context.Context) error {
	_ = a.sc.Close()
	if a.keep {
		return nil
	}
	return a.h.Close(ctx)
}

// DH-D-HUMAN-3, NG-8: a session grant of directory w, given on an allowed-session
// answer, approves the next call inside w without asking; after the agent is
// disposed and its session resumed by a new incarnation, the grant list is empty
// and a call needing w asks again, although the log holds grant/created. Nothing
// here ends grants by hand: the table is bound to the agent lifecycle.
func TestGrantDoesNotSurviveResume(t *testing.T) {
	ctx := context.Background()
	w := filepath.Join(t.TempDir(), "w")
	if err := os.MkdirAll(filepath.Join(w, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	first := agent.Ref{Session: "s-grant", Incarnation: 1}
	resumed := agent.Ref{Session: "s-grant", Incarnation: 2}
	svc, grants, ans, root := granting(t, "ui", []agent.Ref{first, resumed}, answerer.AllowSession(), answerer.AllowOnce())

	sessions := session.NewService(root, memstore.New(), session.Config{})
	if _, err := grants.Bind(root, sessions); err != nil {
		t.Fatal(err)
	}
	reg := lifecycle(t, root, sessions, false)

	a1, err := reg.Create(root, agent.CreateOptions{Session: "s-grant"})
	if err != nil || a1.Agent().Ref() != first {
		t.Fatalf("create %v, %v", a1, err)
	}
	h, _ := sessions.Get("s-grant")
	d, err := svc.Request(ctx, h, dirReq(first, "c1", w))
	if err != nil || !d.Approved() || !strings.HasPrefix(d.Detail, approval.DetailGranted+":") {
		t.Fatalf("first decision %+v, %v", d, err)
	}
	list := grants.List(first)
	if len(list) != 1 || list[0].By.Channel != "ui" || list[0].Subject.Kind != approval.ScopeDirectory {
		t.Fatalf("grants %+v", list)
	}
	d, err = svc.Request(ctx, h, dirReq(first, "c2", filepath.Join(w, "sub", "f.txt")))
	if err != nil || !d.Approved() || d.Detail != approval.DetailAllowedByGrant+":"+list[0].ID || len(ans.Asked()) != 1 {
		t.Fatalf("covered call %+v, %v, asked %d", d, err, len(ans.Asked()))
	}
	if got := grants.Roots(first); len(got) != 1 || got[0] != list[0].Subject.Path {
		t.Fatalf("roots %v", got)
	}

	// The session ends with its agent; a new incarnation resumes it.
	if err := a1.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if got := grants.List(first); len(got) != 0 {
		t.Fatalf("grants of the disposed agent %+v", got)
	}
	a2, err := reg.Create(root, agent.CreateOptions{Session: "s-grant"})
	if err != nil || a2.Agent().Ref() != resumed {
		t.Fatalf("resume %v, %v", a2, err)
	}
	t.Cleanup(func() { _ = a2.Dispose(ctx) })
	h2, _ := sessions.Get("s-grant")
	if got := grants.List(resumed); len(got) != 0 {
		t.Fatalf("resumed grants %+v", got)
	}
	d, err = svc.Request(ctx, h2, dirReq(resumed, "c3", w))
	if err != nil || !d.Approved() || strings.HasPrefix(d.Detail, approval.DetailAllowedByGrant) || len(ans.Asked()) != 2 {
		t.Fatalf("resumed call %+v, %v, asked %d", d, err, len(ans.Asked()))
	}
	var created, ended int
	evs, _ := h2.Read(ctx, 0, 100)
	for _, ev := range evs {
		switch ev.Type() {
		case approval.GrantCreatedType.Name():
			created++
		case approval.GrantEndedType.Name():
			e, _ := approval.GrantEndedType.As(ev)
			if e.Reason != approval.EndSessionEnded {
				t.Errorf("ended reason %q", e.Reason)
			}
			ended++
		}
	}
	if created != 1 || ended != 1 {
		t.Fatalf("log has %d grant/created, %d grant/ended", created, ended)
	}
}

// An agent disposed while its session stays live (the session outlives its
// agent) ends its grants at once, session-ended.
func TestGrantEndsWhenItsAgentIsDisposed(t *testing.T) {
	ctx := context.Background()
	ref := agent.Ref{Session: "s-keep", Incarnation: 1}
	svc, grants, _, root := granting(t, "ui", []agent.Ref{ref}, answerer.AllowSession())
	sessions := session.NewService(root, memstore.New(), session.Config{})
	if _, err := grants.Bind(root, sessions); err != nil {
		t.Fatal(err)
	}
	reg := lifecycle(t, root, sessions, true)
	a, err := reg.Create(root, agent.CreateOptions{Session: "s-keep"})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := sessions.Get("s-keep")
	if _, err := svc.Request(ctx, h, dirReq(ref, "c1", t.TempDir())); err != nil || len(grants.List(ref)) != 1 {
		t.Fatalf("grant %v, %v", grants.List(ref), err)
	}
	if err := a.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if got := grants.List(ref); len(got) != 0 {
		t.Fatalf("grants after disposal %+v", got)
	}
	evs, _ := h.Read(ctx, 0, 100)
	if last := evs[len(evs)-1]; last.Type() != approval.GrantEndedType.Name() {
		t.Fatalf("last event %s", last.Type())
	} else if e, _ := approval.GrantEndedType.As(last); e.Reason != approval.EndSessionEnded {
		t.Fatalf("ended %+v", e)
	}
}

// lifecycle is an agent registry whose stub factory creates the session of an
// id the first time and resumes it after; keep leaves the session live when the
// agent closes.
func lifecycle(t *testing.T, root *cordis.Context, sessions session.Service, keep bool) agent.Registry {
	t.Helper()
	ctx := context.Background()
	tab := scope.NewTable(root)
	reg := agent.NewRegistry(root)
	opened := map[agent.Ref]bool{}
	if _, err := reg.SetFactory(root, agent.FactoryFunc(func(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
		var h session.Handle
		var err error
		if opened[agent.Ref{Session: o.Session}] {
			h, _, err = sessions.Open(ctx, owner, o.Session, session.OpenOptions{})
		} else {
			h, err = sessions.Create(ctx, owner, session.CreateOptions{Header: session.Header{ID: string(o.Session)}})
		}
		if err != nil {
			return nil, err
		}
		opened[agent.Ref{Session: o.Session}] = true
		sc, err := tab.New(owner, ref.String())
		if err != nil {
			return nil, err
		}
		return &lifeAgent{ref: ref, h: h, sc: sc, keep: keep}, nil
	})); err != nil {
		t.Fatal(err)
	}
	return reg
}

// DH-T-HUMAN-5 (subset): a directory grant covers the directory and paths inside
// it and nothing beside it, a prefix sibling included; an application grant
// matches both ids only; a process id is refused as a subject; no subject never
// matches.
func TestGrantMatching(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	w := filepath.Join(base, "w")
	for _, d := range []string{w, w + "2", filepath.Join(base, "out")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	svc, grants, _, _ := granting(t, "cli", []agent.Ref{me}, answerer.AllowSession(), answerer.AllowSession(), answerer.AllowSession())
	log := &recLog{}
	if _, err := svc.Request(ctx, log, dirReq(me, "c1", w)); err != nil {
		t.Fatal(err)
	}
	app := approval.Request{Agent: me, Tool: "computer", Call: "c2",
		Subject: &approval.Subject{Kind: approval.ScopeApplication, Provider: "macos", App: "com.apple.Notes"}}
	if _, err := svc.Request(ctx, log, app); err != nil {
		t.Fatal(err)
	}
	pid := approval.Request{Agent: me, Tool: "computer", Call: "c3", Subject: &approval.Subject{Kind: approval.ScopeApplication, PID: 42}}
	if d, err := svc.Request(ctx, log, pid); err != nil || !d.Approved() || strings.HasPrefix(d.Detail, approval.DetailGranted) {
		t.Fatalf("pid subject %+v, %v", d, err)
	}
	if n := len(grants.List(me)); n != 2 {
		t.Fatalf("%d grants, want 2", n)
	}
	link := filepath.Join(w, "link")
	hasLink := os.Symlink(filepath.Join(base, "out"), link) == nil
	for _, tc := range []struct {
		name string
		s    *approval.Subject
		want bool
	}{
		{"the directory", &approval.Subject{Kind: approval.ScopeDirectory, Path: w}, true},
		{"inside", &approval.Subject{Kind: approval.ScopeDirectory, Path: filepath.Join(w, "a", "b.txt")}, true},
		{"prefix sibling", &approval.Subject{Kind: approval.ScopeDirectory, Path: w + "2"}, false},
		{"parent", &approval.Subject{Kind: approval.ScopeDirectory, Path: base}, false},
		{"same app", &approval.Subject{Kind: approval.ScopeApplication, Provider: "macos", App: "com.apple.Notes"}, true},
		{"other provider", &approval.Subject{Kind: approval.ScopeApplication, Provider: "windows", App: "com.apple.Notes"}, false},
		{"a pid", &approval.Subject{Kind: approval.ScopeApplication, Provider: "macos", App: "com.apple.Notes", PID: 7}, false},
		{"no subject", nil, false},
	} {
		if _, ok := grants.Match(me, tc.s); ok != tc.want {
			t.Errorf("%s: match = %v, want %v", tc.name, ok, tc.want)
		}
	}
	if hasLink {
		if _, ok := grants.Match(me, &approval.Subject{Kind: approval.ScopeDirectory, Path: filepath.Join(link, "x")}); ok {
			t.Error("a link that points out of w matched")
		}
	}
}

// DH-T-HUMAN-6 (subset): revocation stops a grant and logs who; an sdk answer
// cannot create one; the cap is enforced.
func TestGrantRevokeChannelAndCap(t *testing.T) {
	ctx := context.Background()
	w := t.TempDir()
	svc, grants, ans, _ := granting(t, "ui", []agent.Ref{me}, answerer.AllowSession(), answerer.AllowOnce())
	log := &recLog{}
	if _, err := svc.Request(ctx, log, dirReq(me, "c1", w)); err != nil {
		t.Fatal(err)
	}
	g := grants.List(me)[0]
	if err := grants.Revoke(ctx, log, me, g.ID, "alice"); err != nil {
		t.Fatal(err)
	}
	if err := grants.Revoke(ctx, log, me, g.ID, "alice"); !errors.Is(err, approval.ErrGrantNotFound) {
		t.Fatalf("second revoke %v", err)
	}
	if _, err := svc.Request(ctx, log, dirReq(me, "c2", w)); err != nil || len(ans.Asked()) != 2 {
		t.Fatalf("after revoke: asked %d, %v", len(ans.Asked()), err)
	}
	if got := strings.Join(log.types(), ","); got != "approval/asked,grant/created,approval/decided,grant/ended,approval/asked,approval/decided" {
		t.Fatalf("log %s", got)
	}

	sdk, sdkGrants, _, _ := granting(t, "sdk", []agent.Ref{me}, answerer.AllowSession())
	if d, err := sdk.Request(ctx, &recLog{}, dirReq(me, "c1", w)); err != nil || !d.Approved() || len(sdkGrants.List(me)) != 0 {
		t.Fatalf("sdk answer %+v, %v, grants %v", d, err, sdkGrants.List(me))
	}

	replies := make([]answerer.Reply, approval.MaxGrantsPerSession+1)
	for i := range replies {
		replies[i] = answerer.AllowSession()
	}
	capped, capGrants, _, _ := granting(t, "ui", []agent.Ref{me}, replies...)
	for i := range replies {
		if _, err := capped.Request(ctx, &recLog{}, dirReq(me, "c", filepath.Join(w, string(rune('a'+i%26))+strings.Repeat("x", i/26)))); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(capGrants.List(me)); n != approval.MaxGrantsPerSession {
		t.Fatalf("%d grants, cap %d", n, approval.MaxGrantsPerSession)
	}
	if err := capGrants.EndAll(ctx, &recLog{}, me, approval.EndPolicyChanged); err != nil || len(capGrants.List(me)) != 0 {
		t.Fatalf("end all: %v", err)
	}
}

// A grant refused by rule approves the call once and says so; a grant whose
// grant/created cannot be written is an audit failure: the call is refused, the
// error returned, the denial still logged, and no grant is live.
func TestGrantCreateFailures(t *testing.T) {
	ctx := context.Background()
	w := t.TempDir()
	sdk, _, _, _ := granting(t, "sdk", []agent.Ref{me}, answerer.AllowSession())
	if d, err := sdk.Request(ctx, &recLog{}, dirReq(me, "c1", w)); err != nil || !d.Approved() || !strings.HasPrefix(d.Detail, approval.DetailGrantRefused+":") {
		t.Fatalf("refused grant %+v, %v", d, err)
	}

	svc, grants, _, _ := granting(t, "ui", []agent.Ref{me}, answerer.AllowSession())
	log := &recLog{failType: approval.GrantCreatedType.Name()}
	d, err := svc.Request(ctx, log, dirReq(me, "c1", w))
	if err == nil || !strings.Contains(err.Error(), "grant/created") || d.Approved() || d.Detail != "grant-audit-failed" {
		t.Fatalf("failed grant %+v, %v", d, err)
	}
	if n := len(grants.List(me)); n != 0 {
		t.Fatalf("%d grants live after a failed grant/created", n)
	}
	if got := strings.Join(log.types(), ","); got != "approval/asked,approval/decided" {
		t.Fatalf("log %s", got)
	}
}

// endFails is a session whose grant/ended appends fail.
type endFails struct{ session.Session }

func (s endFails) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	if d.Type() == approval.GrantEndedType.Name() {
		return event.Receipt{}, errors.New("injected grant/ended failure")
	}
	return s.Session.Append(ctx, d)
}

// failingEnds finds sessions wrapped in endFails.
type failingEnds struct{ session.Service }

func (s failingEnds) Get(id session.ID) (session.Session, bool) {
	h, ok := s.Service.Get(id)
	if !ok {
		return nil, false
	}
	return endFails{h}, true
}

// A grant/ended that cannot be written does not keep a grant alive: EndAll
// forgets every grant of the agent, fails closed, and reports each failure; a
// disposal whose audit fails leaves no grant behind either.
func TestEndAllForgetsGrantsWhoseEndCannotBeWritten(t *testing.T) {
	ctx := context.Background()
	svc, grants, _, _ := granting(t, "ui", []agent.Ref{me}, answerer.AllowSession(), answerer.AllowSession())
	log := &recLog{}
	for i, w := range []string{t.TempDir(), t.TempDir()} {
		if _, err := svc.Request(ctx, log, dirReq(me, "c"+string(rune('1'+i)), w)); err != nil {
			t.Fatal(err)
		}
	}
	list := grants.List(me)
	log.failType = approval.GrantEndedType.Name()
	err := grants.EndAll(ctx, log, me, approval.EndPolicyChanged)
	if err == nil || !strings.Contains(err.Error(), list[0].ID) || !strings.Contains(err.Error(), list[1].ID) {
		t.Fatalf("end all %v, grants %+v", err, list)
	}
	if got := grants.List(me); len(got) != 0 {
		t.Fatalf("grants after a failed end %+v", got)
	}
	if _, ok := grants.Match(me, &list[0].Subject); ok {
		t.Fatal("a grant whose end failed still matches")
	}

	ref := agent.Ref{Session: "s-fail", Incarnation: 1}
	svc2, grants2, _, root := granting(t, "ui", []agent.Ref{ref}, answerer.AllowSession())
	e := &cordis.MemoryExporter{}
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(e)
	sessions := session.NewService(root, memstore.New(), session.Config{})
	if _, err := grants2.Bind(root, failingEnds{sessions}); err != nil {
		t.Fatal(err)
	}
	a, err := lifecycle(t, root, sessions, true).Create(root, agent.CreateOptions{Session: "s-fail"})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := sessions.Get("s-fail")
	if _, err := svc2.Request(ctx, h, dirReq(ref, "c1", t.TempDir())); err != nil || len(grants2.List(ref)) != 1 {
		t.Fatalf("grant %v, %v", grants2.List(ref), err)
	}
	_ = a.Dispose(ctx)
	if got := grants2.List(ref); len(got) != 0 {
		t.Fatalf("grants after a disposal whose audit failed %+v", got)
	}
	logged := false
	for _, m := range e.Messages() {
		logged = logged || (m.Err != nil && strings.Contains(m.Err.Error(), "injected grant/ended failure"))
	}
	if !logged {
		t.Fatalf("the failed audit was not reported: %+v", e.Messages())
	}
}
