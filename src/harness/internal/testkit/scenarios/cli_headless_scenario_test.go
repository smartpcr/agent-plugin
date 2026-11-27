//go:build scenario

package scenarios

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
)

// TestHeadlessRunOnRealModel is the DH-E-CLI-2 scenario
// (docs/harness/areas/app-cli.md; DH-D-CLI-2, DH-D-CLI-4; tech-spec sec. 4.5:
// authored in DH-S-16, run in DH-S-27 step 27.8): a headless run against a real
// model -- a prompt, an approval on a terminal, a refusal without one, a cancel
// and a resume.
//
// Given the built dsh on Linux or Windows with the platform's real sandbox
// backend, a scratch home whose headless profile layer (DSH_SCENARIO_CLI2_LAYER)
// selects the signed-in Claude Code route and workspace-write, a workspace with
// one file and a file outside it,
// When (1) on a terminal, dsh headless runs a task that writes outside the
// workspace and "allow once" is answered; (2) the same task runs with piped
// streams; (3) a task runs with --json; (4) a long task runs on a terminal and
// Ctrl-C is pressed mid-turn; (5) dsh headless --session <id> "continue" runs,
// Then (1) the prompt is on the terminal, the write happens, the result stream
// holds the answer only, status 0, and the log names channel cli; (2) no prompt,
// the call is denied as unavailable and the file is unchanged; (3) every result
// line parses and the last is run/result; (4) status 130 and a closed turn; (5)
// the resumed run sees the earlier turns and exits 0.
// Cleanup: the home, the workspace and the outside file are removed by
// scenario.Run; a failure keeps both streams in the test log.
//
// The terminal is the scenario's own (startDshOnInteractiveTerminal): dsh's
// controlling terminal, where headless reads the answer (/dev/tty, CONIN$), takes
// the typed "y" and, on Linux, the typed Ctrl-C; dsh's standard output is
// captured apart from it. A pseudoconsole raises no console event for a typed
// Ctrl-C, so on Windows step (4) presses it through DSH_SCENARIO_TERMINAL_KEYS.
//
// Inputs: DSH_SCENARIO_CLI2_LAYER, a layer template file ({workspace});
// DSH_SCENARIO_CLI2_LONG_PROMPT, a task whose turn runs for minutes; on Windows
// DSH_SCENARIO_TERMINAL_KEYS (cli_launch_scenario_test.go), with the key "ctrl-c".
//
//dsh:scenario DH-E-CLI-2
func TestHeadlessRunOnRealModel(t *testing.T) {
	needEnv := []string{"DSH_SCENARIO_CLI2_LAYER", "DSH_SCENARIO_CLI2_LONG_PROMPT"}
	if runtime.GOOS == "windows" {
		needEnv = append(needEnv, "DSH_SCENARIO_TERMINAL_KEYS")
	}
	scenario.Run(t, "DH-E-CLI-2", scenario.Needs{
		Bin: true, OS: []string{"linux", "windows"}, Deadline: 45 * time.Minute,
		Env: needEnv,
	}, func(s *scenario.S) {
		profileLayer(t, s.Home, "headless", layerInput(t, "DSH_SCENARIO_CLI2_LAYER", "a headless layer on the signed-in route", map[string]string{"workspace": s.Workspace}))
		if err := os.WriteFile(filepath.Join(s.Workspace, "note.txt"), []byte("note\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(s.TempDir(), "outside.txt")
		task := "Write the single word written to the file " + filepath.ToSlash(outside) + " with your file tool, then answer done."
		env := []string{"DSH_HOME=" + s.Home}
		terminal := func(text string) (*dshHost, func(string)) {
			return startDshOnInteractiveTerminal(t, env, s.Bin, "--home", s.Home, "headless", text)
		}

		h, typeKeys := terminal(task)
		h.waitFor(t, 5*time.Minute, "the approval prompt", func(string) string {
			if strings.Contains(strings.ToLower(plainText(h.Stderr.String())), "allow once") {
				return "prompt"
			}
			return ""
		})
		typeKeys("y\r")
		if !h.await(10*time.Minute) || h.code() != 0 {
			t.Fatalf("(1) the approved run exited %d:\n%s", h.code(), h.output())
		}
		if data, _ := os.ReadFile(outside); !strings.Contains(string(data), "written") {
			t.Errorf("(1) the approved write did not happen: %q", data)
		}
		if strings.TrimSpace(h.Stdout.String()) == "" || strings.Contains(strings.ToLower(h.Stdout.String()), "allow") {
			t.Errorf("(1) the result stream holds %q, want the answer only", h.Stdout.String())
		}
		first := newestSession(t, s, s.Home)
		if !anyHolds(storedHistory(t, s, s.Home, first), `"cli"`) {
			t.Error("(1) the log names no channel cli")
		}

		if err := os.Remove(outside); err != nil {
			t.Fatal(err)
		}
		r := dshIn(t, s, 10*time.Minute, env, "headless", task)
		if _, err := os.Stat(outside); err == nil {
			t.Error("(2) the write happened with no terminal to approve it")
		}
		if strings.Contains(strings.ToLower(r.stderr), "allow once") || strings.TrimSpace(r.stderr) == "" {
			t.Errorf("(2) the diagnostic stream shows a prompt or no progress:\n%s", r.stderr)
		}
		if !anyHolds(storedHistory(t, s, s.Home, newestSession(t, s, s.Home)), "unavailable") {
			t.Error("(2) the denied call is not recorded as unavailable")
		}

		r = dshIn(t, s, 10*time.Minute, env, "headless", "--json", "Read note.txt and say what it holds.")
		if lines := exportLines(t, r.stdout); len(lines) == 0 || lines[len(lines)-1]["type"] != "run/result" {
			t.Errorf("(3) the last --json result line is not run/result:\n%s", r.stdout)
		}

		h, typeKeys = terminal(need(t, "DSH_SCENARIO_CLI2_LONG_PROMPT", "a task whose turn runs for minutes"))
		time.Sleep(20 * time.Second)
		if runtime.GOOS == "windows" {
			operatorStep(t, "DSH_SCENARIO_TERMINAL_KEYS", "types keys into a terminal", map[string]string{"pid": strconv.Itoa(h.Process.Pid()), "keys": "ctrl-c"})
		} else {
			typeKeys("\x03")
		}
		if !h.await(time.Minute) || h.code() != 130 {
			t.Errorf("(4) Ctrl-C mid-turn left exit %d, want 130", h.code())
		}
		long := newestSession(t, s, s.Home)
		before := len(ofType(storedHistory(t, s, s.Home, long), "turn/end"))
		if before == 0 {
			t.Error("(4) the cancelled turn was not closed")
		}

		if r := dshIn(t, s, 10*time.Minute, env, "headless", "--session", long, "continue"); r.code != 0 {
			t.Errorf("(5) the resumed run exited %d:\n%s", r.code, r.stderr)
		}
		if after := len(ofType(storedHistory(t, s, s.Home, long), "turn/end")); after <= before {
			t.Errorf("(5) the resumed session holds %d turns, want more than %d", after, before)
		}
	})
}
