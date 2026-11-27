//go:build scenario

package scenarios

import (
	"bytes"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestOneShotJobOnItsOwnKey is the DH-E-BUNDLE-1 scenario
// (docs/harness/areas/bundle-preset.md; DH-D-BUNDLE-3; tech-spec sec. 4.5:
// authored in DH-S-16, run in DH-S-48): a one-shot job runs on its own key and
// resumes under its recorded preset.
//
// Given the built dsh with the real sandbox backend, a home in which a web run
// saved a different, invalid key and created a blank session
// (DSH_SCENARIO_BUNDLE1_SETUP), a profile layer declaring lean (extends standard,
// removes the web tool), a real model API whose valid key is only in the job's
// environment, a workspace with one text file,
// When (1) dsh headless --json runs a task reading the file; (2) that session is
// resumed with a second task; (3) lean is selected for the blank session in the
// browser, one prompt is sent, the web host stops and the session is resumed with
// dsh headless; (4) lean is removed from the layer and the session resumed again,
// Then (1) succeeds on the environment key and the saved key file is unchanged;
// (2) continues the same session; (3) the request header lists the lean tools,
// not the removed one; (4) the run is refused before any model request, naming
// the missing preset and the available ones.
// Cleanup: the home and the workspace are removed by scenario.Run.
//
// Inputs: DSH_SCENARIO_BUNDLE1_LAYER and _BASE_LAYER, layer template files
// ({workspace}) with and without lean; DSH_SCENARIO_BUNDLE1_KEY, "NAME=value" of
// the valid key; DSH_SCENARIO_BUNDLE1_KEY_FILE, the saved key file over {home};
// DSH_SCENARIO_BUNDLE1_SETUP, a command over {home} making the invalid-key home
// and printing the blank session id last; DSH_SCENARIO_BUNDLE1_SELECT, a command
// over {home} and {session} selecting lean in the browser, sending one prompt and
// stopping the web host; DSH_SCENARIO_BUNDLE1_REMOVED_TOOL, the tool lean removes.
//
//dsh:scenario DH-E-BUNDLE-1
func TestOneShotJobOnItsOwnKey(t *testing.T) {
	scenario.Run(t, "DH-E-BUNDLE-1", scenario.Needs{
		Bin: true, Deadline: 45 * time.Minute,
		Env: []string{"DSH_SCENARIO_BUNDLE1_LAYER", "DSH_SCENARIO_BUNDLE1_BASE_LAYER", "DSH_SCENARIO_BUNDLE1_KEY", "DSH_SCENARIO_BUNDLE1_KEY_FILE", "DSH_SCENARIO_BUNDLE1_SETUP", "DSH_SCENARIO_BUNDLE1_SELECT", "DSH_SCENARIO_BUNDLE1_REMOVED_TOOL"},
	}, func(s *scenario.S) {
		vars := map[string]string{"workspace": s.Workspace, "home": s.Home}
		layer := func(input string) {
			for _, p := range []string{"web", "headless"} {
				profileLayer(t, s.Home, p, layerInput(t, input, "a profile layer", vars))
			}
		}
		layer("DSH_SCENARIO_BUNDLE1_LAYER")
		if err := os.WriteFile(s.Workspace+string(os.PathSeparator)+"note.txt", []byte("bundle1 note\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		blank := lastLine(operatorStep(t, "DSH_SCENARIO_BUNDLE1_SETUP", "makes the invalid-key home", vars))
		keyFile := expand(need(t, "DSH_SCENARIO_BUNDLE1_KEY_FILE", "the saved key file"), vars)
		saved, err := os.ReadFile(keyFile)
		if err != nil {
			t.Fatalf("the saved key file: %v", err)
		}
		env := []string{"DSH_HOME=" + s.Home, need(t, "DSH_SCENARIO_BUNDLE1_KEY", "the valid key")}
		removed := strings.ToLower(need(t, "DSH_SCENARIO_BUNDLE1_REMOVED_TOOL", "the tool lean removes"))

		r := dshIn(t, s, 10*time.Minute, env, "headless", "--json", "Read note.txt and quote it.")
		lines := exportLines(t, r.stdout)
		if r.code != 0 || len(lines) == 0 {
			t.Fatalf("(1) the job exited %d:\n%s", r.code, r.stderr)
		}
		job, _ := lines[len(lines)-1]["sessionId"].(string)
		if after, _ := os.ReadFile(keyFile); !bytes.Equal(after, saved) {
			t.Error("(1) the job changed the saved key file")
		}
		turns := len(ofType(storedHistory(t, s, s.Home, job), "turn/end"))
		if r := dshIn(t, s, 10*time.Minute, env, "headless", "--session", job, "Say what you read before."); r.code != 0 {
			t.Errorf("(2) the resumed job exited %d:\n%s", r.code, r.stderr)
		}
		if n := len(ofType(storedHistory(t, s, s.Home, job), "turn/end")); n <= turns {
			t.Errorf("(2) the resumed session holds %d turns, want more than %d", n, turns)
		}

		operatorStep(t, "DSH_SCENARIO_BUNDLE1_SELECT", "selects lean in the browser", map[string]string{"home": s.Home, "session": blank})
		if r := dshIn(t, s, 10*time.Minute, env, "headless", "--session", blank, "Say hello."); r.code != 0 {
			t.Errorf("(3) the resumed lean session exited %d:\n%s", r.code, r.stderr)
		}
		headers := headerTools(t, storedHistory(t, s, s.Home, blank))
		if len(headers) == 0 || headers[len(headers)-1][removed] {
			t.Errorf("(3) the last request header lists %v, want the lean tools without %s", headers, removed)
		}

		layer("DSH_SCENARIO_BUNDLE1_BASE_LAYER")
		before := len(ofType(storedHistory(t, s, s.Home, blank), "request/header"))
		r = dshIn(t, s, 5*time.Minute, env, "headless", "--session", blank, "Say hello again.")
		if msg := strings.ToLower(r.stderr); r.code == 0 || !strings.Contains(msg, "lean") || !strings.Contains(msg, "standard") {
			t.Errorf("(4) the resume without lean exited %d, want a refusal naming lean and standard:\n%s", r.code, r.stderr)
		}
		if n := len(ofType(storedHistory(t, s, s.Home, blank), "request/header")); n != before {
			t.Errorf("(4) the refused run made a model request (%d headers, was %d)", n, before)
		}
	})
}

// TestTwoPresetsInOneHost is the DH-E-BUNDLE-2 scenario
// (docs/harness/areas/bundle-preset.md; DH-D-BUNDLE-4, DH-D-BUNDLE-5; tech-spec
// sec. 4.5: authored in DH-S-16, run in DH-S-48): two presets in one host, and an
// edit while a session runs.
//
// Given the web profile on an empty home with the lean declaration of
// DH-E-BUNDLE-1 in its profile layer, a real browser, a real model API, a
// workspace,
// When (1) one session is created with standard and one with lean and both are
// prompted; (2) while the first is in a turn the web tool is removed from
// standard in the settings page; (3) a third session is created with standard;
// (4) standard is edited so one child cannot load; (5) the host restarts and the
// first session continues,
// Then (1) the request headers show different tool sets; (2) the running turn
// keeps the web tool and only the profile layer file changed; (3) the third
// session lacks it; (4) the roster shows standard broken with the child's error,
// new sessions with it are refused, the host and both old sessions keep working;
// (5) the first session's log gains one agent-preset/selected with cause resume
// and a new digest.
// Cleanup: the host is stopped; the home is removed by scenario.Run; the layer
// file is logged if a step fails.
//
// Inputs: DSH_SCENARIO_BUNDLE1_LAYER and _REMOVED_TOOL (TestOneShotJobOnItsOwnKey);
// DSH_SCENARIO_BUNDLE2_UI, a command over {origin}, {action}, {arg} and {out}
// driving the browser -- action create ({arg} the preset), prompt or long-prompt
// ({arg} the session), drop-web-tool, break-child and roster -- writing
// {"session":"","refused":false,"roster":{preset:error}} to {out}.
//
//dsh:scenario DH-E-BUNDLE-2
func TestTwoPresetsInOneHost(t *testing.T) {
	scenario.Run(t, "DH-E-BUNDLE-2", scenario.Needs{
		Bin: true, Deadline: 45 * time.Minute,
		Env: []string{"DSH_SCENARIO_BUNDLE1_LAYER", "DSH_SCENARIO_BUNDLE1_REMOVED_TOOL", "DSH_SCENARIO_BUNDLE2_UI"},
	}, func(s *scenario.S) {
		profileLayer(t, s.Home, "web", layerInput(t, "DSH_SCENARIO_BUNDLE1_LAYER", "a layer declaring lean", map[string]string{"workspace": s.Workspace}))
		removed := strings.ToLower(need(t, "DSH_SCENARIO_BUNDLE1_REMOVED_TOOL", "the web tool"))
		env := []string{"DSH_HOME=" + s.Home}
		var origin string
		start := func() *dshHost {
			h := startDshOnTerminal(t, env, s.Bin, "--home", s.Home, "web")
			origin = originOf(h.waitFor(t, 2*time.Minute, "a launch link", func(out string) string { return launchLink.FindString(plainText(out)) }))
			return h
		}
		type uiState struct {
			Session string            `json:"session"`
			Refused bool              `json:"refused"`
			Roster  map[string]string `json:"roster"`
		}
		ui := func(action, arg string) uiState {
			var st uiState
			operatorJSON(t, "DSH_SCENARIO_BUNDLE2_UI", "drives the browser: "+action, map[string]string{"origin": origin, "action": action, "arg": arg}, &st)
			return st
		}
		last := func(session string) map[string]bool {
			hs := headerTools(t, storedHistory(t, s, s.Home, session))
			if len(hs) == 0 {
				t.Fatalf("session %s holds no request header", session)
			}
			return hs[len(hs)-1]
		}
		layerFile := s.Home + string(os.PathSeparator) + strings.Join([]string{"config", "profiles", "web", "layer.yml"}, string(os.PathSeparator))
		defer func() {
			if t.Failed() {
				data, _ := os.ReadFile(layerFile)
				t.Logf("profile layer:\n%s", data)
			}
		}()

		h := start()
		first, second := ui("create", "standard").Session, ui("create", "lean").Session
		ui("prompt", second)
		ui("long-prompt", first)
		ui("drop-web-tool", "")
		if !last(first)[removed] || last(second)[removed] {
			t.Errorf("(1, 2) the first session's running turn lost %s or lean has it", removed)
		}
		if user, err := os.ReadFile(s.Home + string(os.PathSeparator) + "config" + string(os.PathSeparator) + "layer.yml"); err == nil && len(user) > 0 {
			t.Errorf("(2) the settings edit wrote the user layer, want only the profile layer:\n%s", user)
		}
		third := ui("create", "standard").Session
		ui("prompt", third)
		if last(third)[removed] {
			t.Errorf("(3) the third session still has %s", removed)
		}
		roster := ui("break-child", "")
		if roster.Roster["standard"] == "" {
			t.Errorf("(4) the roster shows standard without the child's error: %v", roster.Roster)
		}
		if !ui("create", "standard").Refused {
			t.Error("(4) a new standard session was not refused")
		}
		ui("prompt", second)
		if err := terminate(t, h); err != nil || !h.await(time.Minute) {
			t.Fatalf("(5) the host did not stop: %v", err)
		}

		selected := len(ofType(storedHistory(t, s, s.Home, first), "agent-preset/selected"))
		h = start()
		ui("prompt", first)
		var resumed []protocol.Event
		for _, e := range ofType(storedHistory(t, s, s.Home, first), "agent-preset/selected")[selected:] {
			if anyHolds([]protocol.Event{e}, `"resume"`) {
				resumed = append(resumed, e)
			}
		}
		if len(resumed) != 1 {
			t.Errorf("(5) the first session gained %d agent-preset/selected with cause resume, want 1", len(resumed))
		}
		_ = terminate(t, h)
		h.await(time.Minute)
	})
}

// headerTools is the tool names of each request/header in evs, in order.
func headerTools(t *testing.T, evs []protocol.Event) []map[string]bool {
	t.Helper()
	var out []map[string]bool
	for _, e := range ofType(evs, "request/header") {
		var h struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		}
		decode(t, e, &h)
		names := map[string]bool{}
		for _, tool := range h.Tools {
			names[strings.ToLower(tool.Name)] = true
		}
		out = append(out, names)
	}
	return out
}
