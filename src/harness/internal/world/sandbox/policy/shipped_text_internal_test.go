package policy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// shipText makes tmpl the shipped text of platform for one test, standing in
// for the Stage 7.6 text (M2-C-6); the text that ships, if any, is restored.
func shipText(t *testing.T, platform, tmpl string) {
	t.Helper()
	old, had := texts[platform]
	texts[platform] = tmpl
	t.Cleanup(func() {
		if had {
			texts[platform] = old
		} else {
			delete(texts, platform)
		}
	})
}

// The drafts of testdata/draft_text/ are what the branch s17/policy-text moves
// to text/ (runbook-s17-wsl.md): each is a platform text that loads and renders.
// Once moved, the directory is gone and TestShippedTextGolden checks the
// shipped texts instead.
func TestDraftTextsLoad(t *testing.T) {
	dir := filepath.Join("testdata", "draft_text")
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		if len(texts) == 0 {
			t.Fatal("neither a draft nor a shipped policy text exists")
		}
		return
	}
	got, err := loadTexts(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range Platforms {
		if strings.TrimSpace(got[p]) == "" {
			t.Errorf("no %s draft", p)
		}
	}
	for p, text := range got {
		for _, m := range []sandbox.Mode{sandbox.ReadOnly, sandbox.WorkspaceWrite} {
			out, err := Render(p, Resolved{Mode: m}, text)
			if err != nil {
				t.Fatal(err)
			}
			// The escalation the scenario counts: the hint, the denial code and
			// the two arguments the retry carries (sandbox7Escalated).
			for _, want := range []string{EscalationHint, "fs/sandbox-denied", "sandbox_permissions", "danger-full-access", "justification"} {
				if !strings.Contains(out, want) {
					t.Errorf("the %s draft in mode %s does not say %q:\n%s", p, m, want, out)
				}
			}
		}
		out, err := Render(p, Resolved{Mode: sandbox.DangerFullAccess}, text)
		if err != nil || !strings.Contains(out, FullAccessLabel) || strings.Contains(out, EscalationHint) || strings.Contains(out, "sandbox_permissions") {
			t.Errorf("the %s draft under full access: %v\n%s", p, err, out)
		}
	}
}

// loadTexts admits only <platform>.md files that render, so a stray file or a
// broken template under text/ fails the build's tests, not a model's turn.
func TestLoadTextsRefuses(t *testing.T) {
	ok := &fstest.MapFile{Data: []byte("Sandbox policy ({{.Platform}}): {{.ModeLabel}}")}
	if got, err := loadTexts(fstest.MapFS{"linux.md": ok}); err != nil || got["linux"] == "" || len(got) != 1 {
		t.Fatalf("loadTexts(linux.md) = %v, %v", got, err)
	}
	if got, err := loadTexts(fstest.MapFS{}); err != nil || len(got) != 0 {
		t.Fatalf("loadTexts(empty) = %v, %v", got, err)
	}
	for name, fsys := range map[string]fstest.MapFS{
		"another platform": {"plan9.md": ok},
		"not markdown":     {"linux.txt": ok},
		"a stray file":     {"linux.md": ok, "README.md": ok},
		"a directory":      {"linux.md/x": ok},
		"unknown field":    {"linux.md": {Data: []byte("{{.Nope}}")}},
		"malformed":        {"windows.md": {Data: []byte("{{")}},
	} {
		if got, err := loadTexts(fsys); err == nil {
			t.Errorf("%s: loadTexts = %v, want an error", name, got)
		}
	}
}

// Once a platform's text ships, every resolved policy records the text's
// version, and the prompt section is the text rendered with the call's policy.
func TestShippedTextIsVersionedAndRendered(t *testing.T) {
	const tmpl = "Sandbox policy ({{.Platform}}): {{.ModeLabel}}; network {{.Network}}.{{range .WriteRoots}} {{.Path}}={{.Source}}{{end}} {{.EscalationHint}}"
	shipText(t, "linux", tmpl)
	root := cordis.New()
	t.Cleanup(root.Dispose)

	if got, err := Text("linux"); err != nil || got != tmpl {
		t.Fatalf("Text(linux) = %q, %v", got, err)
	}
	r, err := New(Config{Platform: "linux", Network: NetworkDenied}, Deps{Owner: root})
	if err != nil {
		t.Fatal(err)
	}
	p, err := r.Resolve(context.Background(), sandbox.Request{Session: "s-text"})
	if err != nil || p.TextVersion() != TextVersion(tmpl) || !strings.HasPrefix(p.TextVersion(), "sha256:") {
		t.Fatalf("resolved text version = %q, %v; want %q", p.TextVersion(), err, TextVersion(tmpl))
	}
	got, err := r.PromptSection().Func(prompt.Input{})
	want := "Sandbox policy (linux): " + ModeLabel(sandbox.ReadOnly) + "; network denied. " + EscalationHint
	if err != nil || got != want {
		t.Fatalf("prompt section = %q, %v; want %q", got, err, want)
	}

	// A policy the section cannot resolve fails the section, not the prompt
	// silently: the workspace-write default needs the session's directory.
	bad, err := New(Config{Platform: "linux", DefaultMode: sandbox.WorkspaceWrite}, Deps{Owner: root, Workdir: workdirtest.New()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bad.PromptSection().Func(prompt.Input{}); err == nil {
		t.Fatal("the section rendered although the policy did not resolve")
	}
}
