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

// DH-D-HUMAN-3, NG-8: a session grant of directory w, given on an allowed-session
// answer, approves the next call inside w without asking; after the session ends
// and is resumed, the grant list is empty and a call needing w asks again,
// although the log holds grant/created.
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
	h, err := sessions.Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: "s-grant"}})
	if err != nil {
		t.Fatal(err)
	}
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

	// The session ends: no EndAll, as after a crash; the resume closes the log's
	// grants and starts with an empty table.
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	h2, _, err := sessions.Open(ctx, root, "s-grant", session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { h2.Close(ctx) })
	closed, err := approval.CloseGrantsOnResume(ctx, h2)
	if err != nil || len(closed) != 1 || closed[0] != list[0].ID {
		t.Fatalf("closed on resume %v, %v", closed, err)
	}
	if again, _ := approval.CloseGrantsOnResume(ctx, h2); len(again) != 0 {
		t.Fatalf("closed twice: %v", again)
	}
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
