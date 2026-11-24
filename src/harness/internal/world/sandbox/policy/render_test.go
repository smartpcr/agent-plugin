package policy_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/policy"
)

// platformRoots are fixed, platform-style workspace, grant and scratch paths,
// so each golden is the same on every host.
var platformRoots = map[string][3]string{
	"linux":   {"/home/dev/proj", "/srv/shared/out", "/home/dev/.dsh/scratch/s-policy"},
	"darwin":  {"/Users/dev/proj", "/Volumes/shared/out", "/Users/dev/Library/Caches/dsh/scratch/s-policy"},
	"windows": {`C:\Users\dev\proj`, `D:\shared\out`, `C:\Users\dev\AppData\Local\dsh\scratch\s-policy`},
}

// TC-01g: the renderer fills the test-only fixture template per platform with
// each mode the resolver yields; the output is byte-identical to
// testdata/render_fixture_<platform>.golden. These goldens depend only on the
// fixture template, never on a shipped text.
func TestRenderFixtureGolden(t *testing.T) {
	tmpl, err := os.ReadFile(filepath.Join("testdata", "fixture_template.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, platform := range policy.Platforms {
		t.Run(platform, func(t *testing.T) {
			r := platformRoots[platform]
			cases := []struct {
				label string
				res   policy.Resolved
			}{
				{"read-only", policy.Resolved{Mode: sandbox.ReadOnly}},
				{"workspace-write", policy.Resolved{Mode: sandbox.WorkspaceWrite, Network: true, WriteRoots: []sandbox.WriteRoot{
					{Path: r[0], Source: sandbox.SourceWorkspace}, {Path: r[1], Source: sandbox.SourceGrant}, {Path: r[2], Source: sandbox.SourceScratch}}}},
				{"danger-full-access", policy.Resolved{Mode: sandbox.DangerFullAccess, Network: true}},
			}
			var b strings.Builder
			for _, c := range cases {
				out, err := policy.Render(platform, c.res, strings.ReplaceAll(string(tmpl), "\r\n", "\n"))
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&b, "== mode %s\n%s", c.label, out)
				if c.res.Mode == sandbox.DangerFullAccess && !strings.Contains(out, policy.FullAccessLabel) {
					t.Errorf("full access does not carry FullAccessLabel:\n%s", out)
				}
			}
			golden.Assert(t, "render_fixture_"+platform, b.String())
		})
	}
}

func TestRenderRefuses(t *testing.T) {
	if _, err := policy.Render("plan9", policy.Resolved{Mode: sandbox.ReadOnly}, "x"); !errors.Is(err, policy.ErrUnknownPlatform) {
		t.Errorf("unknown platform: %v", err)
	}
	if _, err := policy.Render("linux", policy.Resolved{Mode: "full"}, "x"); err == nil {
		t.Error("a bogus mode rendered")
	}
	if _, err := policy.Render("linux", policy.Resolved{Mode: sandbox.ReadOnly}, "{{.Nope}}"); err == nil {
		t.Error("a template naming an unknown field rendered")
	}
	if _, err := policy.Render("linux", policy.Resolved{Mode: sandbox.ReadOnly}, "{{"); err == nil {
		t.Error("a malformed template rendered")
	}
}

// fixedView is a step view with no tools for one agent.
type fixedView struct{ target tools.Target }

func (v fixedView) Target() tools.Target                   { return v.target }
func (fixedView) Tools() []*tools.Declaration              { return nil }
func (fixedView) Lookup(string) (*tools.Declaration, bool) { return nil, false }
func (fixedView) Release()                                 {}

// TC-01a no text means no section: no world/sandbox/policy/text/ directory
// exists, Text is ErrNoText on every platform, a resolved policy records no
// text version, and the prompt the builder composes has no sandbox policy
// section although the resolver registered it.
func TestNoTextMeansNoSection(t *testing.T) {
	if _, err := os.Stat("text"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("world/sandbox/policy/text/ must not exist before Stage 7.6 (M2-C-6): %v", err)
	}
	for _, platform := range policy.Platforms {
		if _, err := policy.Text(platform); !errors.Is(err, policy.ErrNoText) {
			t.Errorf("Text(%s) = %v, want ErrNoText", platform, err)
		}
	}
	if _, err := policy.Text("plan9"); !errors.Is(err, policy.ErrUnknownPlatform) {
		t.Errorf("Text(plan9) = %v", err)
	}

	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	w := newWorld(t)
	deps := baseDeps(t, root, w)
	r := newResolver(t, policy.Config{DefaultMode: sandbox.WorkspaceWrite}, deps)
	if p, err := r.Resolve(ctx, sandbox.Request{Session: sid}); err != nil || p.TextVersion() != "" {
		t.Fatalf("resolved text version = %q, %v", p.TextVersion(), err)
	}
	ps, err := prompt.New(root, scope.NewTable(root), prompt.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RegisterPromptSection(deps.Owner, ps); err != nil {
		t.Fatal(err)
	}
	a, err := ps.Assemble(ctx, prompt.Input{View: fixedView{tools.Target{Agent: agent.Ref{Session: sid, Incarnation: 1}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range a.Manifest() {
		if m.Name == prompt.SectionSandboxPolicy {
			t.Fatalf("the prompt has a sandbox policy section with no shipped text: %+v", m)
		}
	}
	text, err := ps.Render(a)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "Sandbox policy") || strings.Contains(text, policy.EscalationHint) {
		t.Fatalf("the prompt states a sandbox policy:\n%s", text)
	}
}
