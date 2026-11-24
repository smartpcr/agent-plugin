package sandbox_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
)

func TestCodesAreDeclared(t *testing.T) {
	for _, c := range []herr.Reason{sandbox.CodeUnavailable, sandbox.CodeInvalidRoot, sandbox.CodeInvalidMode, sandbox.CodeEscalationRejected} {
		if !c.Valid() || c.Domain() != "sandbox" {
			t.Errorf("code %q is malformed", c)
		}
	}
	for _, m := range sandbox.Modes {
		if got, err := sandbox.ParseMode(string(m)); err != nil || got != m {
			t.Errorf("ParseMode(%q) = %q, %v", m, got, err)
		}
	}
	if _, err := sandbox.ParseMode("full"); herr.Kind(err) != sandbox.CodeInvalidMode {
		t.Errorf("ParseMode(full): %v", err)
	}
	if !sandbox.DangerFullAccess.Wider(sandbox.WorkspaceWrite) || sandbox.ReadOnly.Wider(sandbox.ReadOnly) {
		t.Error("Wider is not the strict ladder")
	}
}

//dsh:test DH-T-SANDBOX-2
func TestZeroPolicyHasNoValue(t *testing.T) {
	var p sandbox.Policy
	if !p.IsZero() || p.Mode() != "" || p.WriteRoots() != nil || p.Network() || p.Require() != "" {
		t.Fatalf("zero policy reads as %q/%v", p.Mode(), p.WriteRoots())
	}
	if herr.Kind(p.Validate()) != sandbox.CodeUnavailable {
		t.Fatalf("zero policy Validate = %v", p.Validate())
	}
	q := sandboxtest.Policy(sandbox.ReadOnly)
	if q.IsZero() || q.Validate() != nil || q.Require() != sandbox.RequireAny {
		t.Fatalf("issued policy = %+v", q)
	}
}

func TestIssueValidates(t *testing.T) {
	root := t.TempDir()
	for _, c := range []struct {
		mode  sandbox.Mode
		roots []string
		want  herr.Reason
	}{
		{"bogus", nil, sandbox.CodeInvalidMode},
		{sandbox.ReadOnly, []string{root}, sandbox.CodeInvalidRoot},
		{sandbox.WorkspaceWrite, []string{"rel"}, sandbox.CodeInvalidRoot},
		{sandbox.WorkspaceWrite, []string{root + string(filepath.Separator) + "."}, sandbox.CodeInvalidRoot},
		{sandbox.WorkspaceWrite, []string{root}, ""},
	} {
		if _, err := sandboxtest.Issue(c.mode, false, c.roots...); herr.Kind(err) != c.want {
			t.Errorf("Issue(%s, %v) = %v, want %q", c.mode, c.roots, err, c.want)
		}
	}
	p := sandboxtest.Policy(sandbox.WorkspaceWrite, root)
	rs := p.WriteRoots()
	rs[0].Path = "mutated"
	if p.WriteRoots()[0].Path != root {
		t.Fatal("WriteRoots exposes the policy's slice")
	}
}

func TestFakeResolver(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	const s session.ID = "s1"
	wd := workdirtest.New()
	wd.Set(s, root, root)
	r := &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd}

	p, err := r.Resolve(ctx, sandbox.Request{Session: s})
	if err != nil || p.Mode() != sandbox.WorkspaceWrite || len(p.WriteRoots()) != 1 || p.WriteRoots()[0].Path != root {
		t.Fatalf("default resolve = %+v, %v", p.WriteRoots(), err)
	}
	if p, _ := r.Resolve(ctx, sandbox.Request{Session: s, Mode: sandbox.ReadOnly}); p.Mode() != sandbox.ReadOnly {
		t.Fatalf("explicit mode did not win: %s", p.Mode())
	}

	refuse := errors.New("busy")
	r.OnModeChanging = func(sandbox.ModeChange) error { return refuse }
	if err := r.SetMode(ctx, s, sandbox.ReadOnly); !errors.Is(err, refuse) || len(r.Changes()) != 0 {
		t.Fatalf("refused SetMode = %v, changes %v", err, r.Changes())
	}
	r.OnModeChanging = nil
	if err := r.SetMode(ctx, s, sandbox.ReadOnly); err != nil {
		t.Fatal(err)
	}
	if p, _ := r.Resolve(ctx, sandbox.Request{Session: s}); p.Mode() != sandbox.ReadOnly {
		t.Fatalf("session mode did not outrank the default: %s", p.Mode())
	}
	if c := r.Changes(); len(c) != 1 || c[0].From != sandbox.WorkspaceWrite || c[0].To != sandbox.ReadOnly {
		t.Fatalf("changes = %+v", c)
	}

	if _, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: s}); herr.Kind(err) != sandbox.CodeEscalationRejected {
		t.Fatalf("escalation with no approver: %v", err)
	}
	r.Approve = func(sandbox.EscalationRequest) (sandbox.Mode, []string, error) { return sandbox.ReadOnly, nil, nil }
	if _, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: s}); herr.Kind(err) != sandbox.CodeEscalationRejected {
		t.Fatalf("escalation that is not wider: %v", err)
	}

	r.SetMode(ctx, s, sandbox.WorkspaceWrite)
	extra := filepath.Join(root, "..", "grant")
	release, err := r.GrantRoot(ctx, s, filepath.Clean(extra))
	if err != nil {
		t.Fatal(err)
	}
	if p, _ := r.Resolve(ctx, sandbox.Request{Session: s}); len(p.WriteRoots()) != 2 || p.WriteRoots()[1].Source != sandbox.SourceGrant {
		t.Fatalf("grant not a root: %+v", p.WriteRoots())
	}
	release()
	release()
	if p, _ := r.Resolve(ctx, sandbox.Request{Session: s}); len(p.WriteRoots()) != 1 {
		t.Fatalf("released grant still a root: %+v", p.WriteRoots())
	}

	wd.Set(s, root, root).Gone()
	if _, err := r.Resolve(ctx, sandbox.Request{Session: s}); herr.Kind(err) != "workdir/unavailable" {
		t.Fatalf("resolve with the directory gone: %v", err)
	}
}

func TestFakeSetModeSerializesTransitions(t *testing.T) {
	ctx := context.Background()
	const s session.ID = "s1"
	r := &sandboxtest.Resolver{}
	entered := make(chan sandbox.ModeChange, 2)
	release := make(chan struct{})
	r.OnModeChanging = func(c sandbox.ModeChange) error {
		if p, err := r.Resolve(ctx, sandbox.Request{Session: s}); err != nil || p.Mode() != c.From {
			t.Errorf("Resolve inside the listener = %s, %v; want the previous mode %s", p.Mode(), err, c.From)
		}
		entered <- c
		if c.To == sandbox.DangerFullAccess {
			<-release
		}
		return nil
	}
	done := make(chan error, 2)
	go func() { done <- r.SetMode(ctx, s, sandbox.DangerFullAccess) }()
	first := <-entered
	go func() { done <- r.SetMode(ctx, s, sandbox.WorkspaceWrite) }()
	select {
	case c := <-entered:
		t.Fatalf("a second transition %+v entered while %+v was in progress", c, first)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	second := <-entered
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if first.From != sandbox.ReadOnly || second.From != sandbox.DangerFullAccess {
		t.Fatalf("listeners saw %+v then %+v; want read-only→danger-full-access→workspace-write", first, second)
	}
	prev := sandbox.ReadOnly
	for _, c := range r.Changes() {
		if c.From != prev {
			t.Fatalf("changes %+v do not form a chain", r.Changes())
		}
		prev = c.To
	}
	if prev != sandbox.WorkspaceWrite || len(r.Changes()) != 2 {
		t.Fatalf("changes = %+v", r.Changes())
	}

	r.OnModeChanging = func(sandbox.ModeChange) error { return errors.New("refused") }
	if err := r.SetMode(ctx, s, sandbox.ReadOnly); err == nil || len(r.Changes()) != 2 {
		t.Fatalf("refused SetMode = %v, changes %+v", err, r.Changes())
	}
}

func TestFakeEscalationMustWiden(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	extra := filepath.Join(filepath.Dir(root), filepath.Base(root)+"-extra")
	const s session.ID = "s1"
	wd := workdirtest.New()
	wd.Set(s, root, root)
	answer := func(m sandbox.Mode, paths ...string) func(sandbox.EscalationRequest) (sandbox.Mode, []string, error) {
		return func(sandbox.EscalationRequest) (sandbox.Mode, []string, error) { return m, paths, nil }
	}
	for _, c := range []struct {
		name    string
		session sandbox.Mode
		approve func(sandbox.EscalationRequest) (sandbox.Mode, []string, error)
	}{
		{"read-only plus paths", sandbox.ReadOnly, answer(sandbox.ReadOnly, extra)},
		{"full access plus paths", sandbox.DangerFullAccess, answer(sandbox.DangerFullAccess, extra)},
		{"wider full access plus ignored paths", sandbox.WorkspaceWrite, answer(sandbox.DangerFullAccess, extra)},
		{"workspace-write plus an already writable path", sandbox.WorkspaceWrite, answer(sandbox.WorkspaceWrite, filepath.Join(root, "sub"))},
		{"workspace-write plus nothing", sandbox.WorkspaceWrite, answer(sandbox.WorkspaceWrite)},
	} {
		r := &sandboxtest.Resolver{Default: c.session, Workdir: wd, Approve: c.approve}
		if p, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: s}); herr.Kind(err) != sandbox.CodeEscalationRejected {
			t.Errorf("%s: Escalate = %s %v, %v; want %s", c.name, p.Mode(), p.WriteRoots(), err, sandbox.CodeEscalationRejected)
		}
	}

	r := &sandboxtest.Resolver{Default: sandbox.WorkspaceWrite, Workdir: wd, Approve: answer(sandbox.WorkspaceWrite, extra)}
	p, err := r.Escalate(ctx, sandbox.EscalationRequest{Session: s})
	if err != nil {
		t.Fatal(err)
	}
	roots := p.WriteRoots()
	if len(roots) != 2 || roots[1].Path != extra || roots[1].Source != sandbox.SourceEscalation {
		t.Fatalf("escalated roots = %+v", roots)
	}
	if next, _ := r.Resolve(ctx, sandbox.Request{Session: s}); len(next.WriteRoots()) != 1 {
		t.Fatalf("the one-call escalation leaked into Resolve: %+v", next.WriteRoots())
	}
}

func TestFakeSetModeHonoursCancellation(t *testing.T) {
	const s session.ID = "s1"
	r := &sandboxtest.Resolver{}
	var seen []sandbox.ModeChange
	var mu sync.Mutex
	entered, release := make(chan struct{}), make(chan struct{})
	r.OnModeChanging = func(c sandbox.ModeChange) error {
		mu.Lock()
		seen = append(seen, c)
		mu.Unlock()
		if c.To == sandbox.WorkspaceWrite {
			close(entered)
			<-release
		}
		return nil
	}
	first := make(chan error, 1)
	go func() { first <- r.SetMode(context.Background(), s, sandbox.WorkspaceWrite) }()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	queued := make(chan error, 1)
	go func() { queued <- r.SetMode(ctx, s, sandbox.DangerFullAccess) }()
	time.Sleep(50 * time.Millisecond) // let it reach the queue
	cancel()
	select {
	case err := <-queued:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled queued SetMode = %v, want an error wrapping context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled queued SetMode stayed blocked behind the running transition")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 1 || seen[0].To != sandbox.WorkspaceWrite {
		t.Fatalf("listeners ran for %+v; the cancelled call must not reach its listener", seen)
	}
	if c := r.Changes(); len(c) != 1 || c[0].To != sandbox.WorkspaceWrite {
		t.Fatalf("changes = %+v; the cancelled call must not change the mode", c)
	}
	if p, _ := r.Resolve(context.Background(), sandbox.Request{Session: s}); p.Mode() != sandbox.WorkspaceWrite {
		t.Fatalf("mode = %s, want workspace-write", p.Mode())
	}

	// Cancelled while its own listener runs: refused, nothing committed.
	ctx2, cancel2 := context.WithCancel(context.Background())
	r.OnModeChanging = func(sandbox.ModeChange) error { cancel2(); return nil }
	if err := r.SetMode(ctx2, s, sandbox.ReadOnly); !errors.Is(err, context.Canceled) || len(r.Changes()) != 1 {
		t.Fatalf("SetMode cancelled in its listener = %v, changes %+v", err, r.Changes())
	}
}
