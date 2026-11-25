package policy

import (
	"context"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir/workdirtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// shipText makes tmpl the shipped text of platform for one test, standing in
// for the Stage 7.6 text no tree holds yet (M2-C-6).
func shipText(t *testing.T, platform, tmpl string) {
	t.Helper()
	if _, ok := texts[platform]; ok {
		t.Fatalf("a %s policy text ships already", platform)
	}
	texts[platform] = tmpl
	t.Cleanup(func() { delete(texts, platform) })
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
