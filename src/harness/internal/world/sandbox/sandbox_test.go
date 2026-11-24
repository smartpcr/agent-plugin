package sandbox_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

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
