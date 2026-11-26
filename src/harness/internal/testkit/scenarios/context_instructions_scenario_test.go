//go:build scenario

package scenarios

import (
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// context2Settle bounds the wait after a change on disk for the instruction
// watch to report it (its debounce is well under this).
const context2Settle = 3 * time.Second

// instructionsOpen opens every instructions message (context/instructions
// render.go).
const instructionsOpen = "<instructions>\n"

// TestContextInstructionFileChangesReachModel is the DH-E-CONTEXT-2 scenario
// (e2e-scenarios.md Phase 10; docs/harness/areas/context-compaction-skill.md):
// an instruction file changed outside the harness -- by a real editor, by a git
// branch switch -- reaches the model as one update, and a link out of the
// project does not.
//
// Given a session in a temporary git repository whose AGENTS.md differs on two
// branches, and a secret-looking file outside the repository,
// When (1) a real editor saves a change to AGENTS.md, (2) git switches to the
// other branch, (3) sub/AGENTS.md is added as a link to the outside file, and
// one prompt is sent after each,
// Then after 1 and 2 the next model request holds exactly one more instructions
// message, and the latest holds the new text and no longer the replaced one;
// after 3 no request holds the outside file's text, and the session's
// diagnostics name sub/AGENTS.md as refused; no turn fails.
//
// Inputs: DSH_SCENARIO_CONTEXT2_EDITOR, a command line over {file} and {text}
// that has a real editor replace {file}'s content with {text} and save it;
// DSH_SCENARIO_CONTEXT2_LAYER, a layer over {endpoint} registering
// DSH_SCENARIO_CONTEXT2_ROUTE on the scripted endpoint {endpoint}, and
// DSH_SCENARIO_CONTEXT2_MODEL. git is taken from PATH. On Windows the link
// needs symbolic-link privilege (developer mode).
//
//dsh:scenario DH-E-CONTEXT-2
func TestContextInstructionFileChangesReachModel(t *testing.T) {
	editor := need(t, "DSH_SCENARIO_CONTEXT2_EDITOR", "a command line over {file} and {text} that has a real editor replace {file}'s content with {text} and save it")
	layerT := need(t, "DSH_SCENARIO_CONTEXT2_LAYER", "a layer over {endpoint} registering DSH_SCENARIO_CONTEXT2_ROUTE on {endpoint} with a scenario-local key")
	route := &protocol.ModelRoute{
		Provider: need(t, "DSH_SCENARIO_CONTEXT2_ROUTE", "the route the layer registers on the scripted endpoint"),
		Model:    need(t, "DSH_SCENARIO_CONTEXT2_MODEL", "a model of that route"),
	}
	scratch := t.TempDir()
	repo, outside := filepath.Join(scratch, "repo"), filepath.Join(scratch, "secret.txt")
	const (
		original = "context2: rule from main, original"
		edited   = "context2: rule from main, edited"
		branch   = "context2: rule from the other branch"
		secret   = "context2-outside-SECRET-do-not-read"
	)
	agents := filepath.Join(repo, "AGENTS.md")
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(t, repo, "init", "-q", "-b", "main")
	git(t, repo, "config", "user.email", "scenario@example.invalid")
	git(t, repo, "config", "user.name", "dsh scenario")
	git(t, repo, "config", "core.autocrlf", "false")
	context2Write(t, filepath.Join(repo, "sub", "keep.txt"), "keep")
	context2Write(t, agents, branch)
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "other")
	git(t, repo, "branch", "other")
	context2Write(t, agents, original)
	git(t, repo, "commit", "-q", "-am", "main")

	bin := dshBinary(t)
	m := startScriptedModel(t)
	h := startSDK(t, bin, nil, []string{"--layer", writeLayer(t, expand(layerT, map[string]string{"endpoint": m.URL}))})
	session := h.createIn(t, route, repo, "workspace-write")
	h.keepExport(t, session)

	prompt := func(what string) string {
		t.Helper()
		run := h.awaitRun(t, session, h.prompt(t, session, "Reply briefly: "+what), 5*time.Minute)
		if run.FinishReason != "stop" {
			t.Fatalf("the turn after %s ended %q, want stop", what, run.FinishReason)
		}
		reqs := m.requests()
		if len(reqs) == 0 {
			t.Fatalf("no model request after %s", what)
		}
		return requestText(t, reqs[len(reqs)-1])
	}
	last := prompt("start")
	if !strings.Contains(last, original) {
		t.Fatalf("the first request does not hold AGENTS.md's text %q", original)
	}

	step := func(what, want, gone string, change func()) {
		t.Helper()
		change()
		<-time.After(context2Settle)
		next := prompt(what)
		if n := strings.Count(next, instructionsOpen) - strings.Count(last, instructionsOpen); n != 1 {
			t.Errorf("after %s the request holds %d more instructions messages, want exactly 1", what, n)
		}
		latest := next[strings.LastIndex(next, instructionsOpen):]
		if !strings.Contains(latest, want) || strings.Contains(latest, gone) {
			t.Errorf("after %s the latest instructions message does not hold %q in place of %q:\n%s", what, want, gone, latest)
		}
		last = next
	}
	step("the editor's save", edited, original, func() {
		mustShell(t, expand(editor, map[string]string{"file": agents, "text": edited}))
	})
	step("the branch switch", branch, edited, func() {
		git(t, repo, "checkout", "-q", "-f", "other")
	})

	link := filepath.Join(repo, "sub", "AGENTS.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatalf("linking %s to %s: %v", link, outside, err)
	}
	<-time.After(context2Settle)
	prompt("the link")
	for i, body := range m.requests() {
		if strings.Contains(requestText(t, body), secret) {
			t.Errorf("model request %d holds the outside file's text", i+1)
		}
	}
	if !context2Refused(t, h.history(t, session)) {
		t.Error("no instructions record names sub/AGENTS.md as refused")
	}
}

// context2Refused reports whether an instructions record of evs lists
// sub/AGENTS.md among its refused files.
func context2Refused(t *testing.T, evs []protocol.Event) bool {
	t.Helper()
	var found bool
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if list, ok := x["refused"].([]any); ok {
				for _, e := range list {
					r, _ := e.(map[string]any)
					p, _ := r["path"].(string)
					if strings.HasSuffix(filepath.ToSlash(p), "sub/AGENTS.md") {
						found = true
					}
				}
			}
			for _, e := range x {
				walk(e)
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			// A record may travel as JSON text inside the message source.
			if strings.Contains(x, `"refused"`) {
				var inner any
				if json.Unmarshal([]byte(x), &inner) == nil {
					walk(inner)
				}
			}
		}
	}
	for _, e := range evs {
		var v any
		if json.Unmarshal(e.Data, &v) == nil {
			walk(v)
		}
	}
	return found
}

// requestText is every string of the JSON request body, joined by newlines:
// the text the model received, with the body's escaping undone.
func requestText(t *testing.T, body string) string {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("a model request is not JSON: %v", err)
	}
	var b strings.Builder
	var walk func(v any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			for _, k := range slices.Sorted(maps.Keys(x)) {
				walk(x[k])
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		case string:
			b.WriteString(x)
			b.WriteByte('\n')
		}
	}
	walk(v)
	return b.String()
}

func context2Write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// git runs git in dir and fails the scenario on a non-zero exit.
func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}
