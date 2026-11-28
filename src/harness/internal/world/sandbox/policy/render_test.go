package policy_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
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
				if n := strings.Count(out, policy.FullAccessLabel); n != map[bool]int{true: 1, false: 0}[c.res.Mode == sandbox.DangerFullAccess] {
					t.Errorf("mode %s carries FullAccessLabel %d times:\n%s", c.res.Mode, n, out)
				}
			}
			if strings.Contains(string(tmpl), fullAccessToken) {
				t.Fatalf("the fixture template holds %s itself", fullAccessToken)
			}
			// The goldens hold fullAccessToken where the label goes, so changing
			// FullAccessLabel alone is the owner's one-line diff (impl.md sec. 9.1,
			// I2 Q3); everything else is compared byte for byte.
			golden.Assert(t, "render_fixture_"+platform, strings.ReplaceAll(b.String(), policy.FullAccessLabel, fullAccessToken))
		})
	}
}

// fullAccessToken stands for policy.FullAccessLabel in the render goldens.
const fullAccessToken = "<<policy.FullAccessLabel>>"

// TC-01g for the shipped texts: testdata/policy_section_<platform>.golden exists
// exactly when the platform's text ships, and is the section that text renders
// in each mode, so a text and its golden arrive in one commit and a later edit
// of either fails here until both are re-proved (M2-C-6; harness-gate
// policy-text binds the text's bytes to its DH-E-SANDBOX-7 run).
func TestShippedTextGolden(t *testing.T) {
	for _, platform := range policy.Platforms {
		t.Run(platform, func(t *testing.T) {
			name := "policy_section_" + platform
			tmpl, err := policy.Text(platform)
			if errors.Is(err, policy.ErrNoText) {
				if _, err := os.Stat(golden.Path(name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s exists but no %s policy text ships: a golden arrives with its text (%v)", filepath.ToSlash(golden.Path(name)), platform, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			r := platformRoots[platform]
			var b strings.Builder
			for _, c := range []struct {
				label string
				res   policy.Resolved
			}{
				{"read-only", policy.Resolved{Mode: sandbox.ReadOnly}},
				{"workspace-write", policy.Resolved{Mode: sandbox.WorkspaceWrite, Network: false, WriteRoots: []sandbox.WriteRoot{
					{Path: r[0], Source: sandbox.SourceWorkspace}, {Path: r[1], Source: sandbox.SourceGrant}, {Path: r[2], Source: sandbox.SourceScratch}}}},
				{"danger-full-access", policy.Resolved{Mode: sandbox.DangerFullAccess, Network: true}},
			} {
				out, err := policy.Render(platform, c.res, tmpl)
				if err != nil {
					t.Fatal(err)
				}
				fmt.Fprintf(&b, "== mode %s\n%s", c.label, out)
			}
			golden.Assert(t, name, strings.ReplaceAll(b.String(), policy.FullAccessLabel, fullAccessToken))
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

// TC-01a no text means no section. The text/ directory holds exactly the texts
// that ship: before Stage 7.6, and on main until the s17/policy-text branch
// merges with a passed DH-E-SANDBOX-7 run (M2-C-6), there is no text/
// directory and Text is ErrNoText on every platform. With no text shipping, a
// resolved policy records no text version, and the prompt the builder composes
// has no sandbox policy section although the resolver registered it.
func TestNoTextMeansNoSection(t *testing.T) {
	shipped := policy.ShippedPlatforms()
	if _, err := os.Stat("text"); errors.Is(err, os.ErrNotExist) {
		if len(shipped) != 0 {
			t.Fatalf("%v ship a policy text with no world/sandbox/policy/text/ directory", shipped)
		}
	} else if err != nil {
		t.Fatal(err)
	} else {
		onDisk, err := policy.LoadTextDir("text")
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Sorted(maps.Keys(onDisk)); !slices.Equal(got, shipped) {
			t.Fatalf("world/sandbox/policy/text/ holds %v, the binary ships %v (text_embed.go)", got, shipped)
		}
		for p, want := range onDisk {
			if got, err := policy.Text(p); err != nil || got != want {
				t.Errorf("Text(%s) is not text/%s.md: %v", p, p, err)
			}
		}
	}
	policy.WithoutShippedTexts(t)
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
