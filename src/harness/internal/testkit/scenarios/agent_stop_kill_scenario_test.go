//go:build scenario

package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// TestStopAndKillLeaveAnHonestLog: stopping and killing the host leave an honest
// log (areas/agent-loop.md DH-E-AGENT-3; DH-D-AGENT-6). Authored in DH-S-11;
// executed in DH-S-17, which requires the agent loop (DH-S-11), the shell tool
// (DH-S-13) and the headless launcher (DH-S-16).
//
// Given an empty temporary home on the real SQLite store, profile headless, one
// real provider and a small model, its key supplied at run time, a temporary
// workspace, the shell tool under the platform's sandbox and a prompt that starts a
// shell command sleeping 60 seconds, when, while the command runs, run A asks the
// host to stop (interrupt signal; console control event on Windows) and run B kills
// it, and in both a second dsh then opens the home and resumes the session, then in
// run A, before any resume, the database already holds a result for the call,
// step/end and turn/end{aborted, disposed} and no child process remains; in run B
// resume appends an outcome-unknown result and the closers and the turn is not
// continued; in both the sequence numbers have no gap. Cleanup: the launcher
// stops every tree it started and the home lies under t.TempDir(); a leftover
// sleeping process is named by the pid it recorded. On failure the database
// file's path is logged.
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key, handed to the program as
// DEEPSEEK_API_KEY; DSH_SCENARIO_AGENT3_MODEL a small model on route
// deepseek-official; DSH_SCENARIO_AGENT3_LAYER a layer, a template over
// {workspace}, selecting the headless profile, the SQLite store and the sandboxed
// shell tool; DSH_SCENARIO_AGENT3_PROMPT a prompt, a template over {workspace},
// whose one shell command writes its process id to {workspace}/pid.txt and then
// sleeps 60 seconds; DSH_SCENARIO_AGENT3_STOP a command line over {pid} that
// delivers the platform's interrupt to that process; DSH_SCENARIO_AGENT3_LOG a
// command line over {home} and {session} that prints the session's stored events,
// read from the database without opening the session, as a JSON array of
// {seq, type, data}; DSH_SCENARIO_AGENT3_ALIVE a command line over {pid} that
// exits 0 only while that process exists.
//
//dsh:scenario DH-E-AGENT-3
func TestStopAndKillLeaveAnHonestLog(t *testing.T) {
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	model := need(t, "DSH_SCENARIO_AGENT3_MODEL", "a small model on deepseek-official")
	layerT := need(t, "DSH_SCENARIO_AGENT3_LAYER", "a headless layer with the SQLite store and the sandboxed shell tool")
	promptT := need(t, "DSH_SCENARIO_AGENT3_PROMPT", "a prompt that records its pid and sleeps 60 seconds")
	stopT := need(t, "DSH_SCENARIO_AGENT3_STOP", "a command that interrupts {pid}")
	logT := need(t, "DSH_SCENARIO_AGENT3_LOG", "a command that prints {session}'s stored events")
	aliveT := need(t, "DSH_SCENARIO_AGENT3_ALIVE", "a command that exits 0 while {pid} exists")
	bin := dshBinary(t)

	for _, killed := range []bool{false, true} {
		name := map[bool]string{false: "A stop", true: "B kill"}[killed]
		t.Run(name, func(t *testing.T) {
			ws := t.TempDir()
			home := filepath.Join(t.TempDir(), "home")
			vars := map[string]string{"workspace": filepath.ToSlash(ws), "home": home}
			env := []string{"DEEPSEEK_API_KEY=" + key}
			args := []string{"--home", home, "--layer", writeLayer(t, expand(layerT, vars))}
			route := &protocol.ModelRoute{Provider: "deepseek-official", Model: model}
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("the database is under %s", home)
				}
			})

			h1 := startSDK(t, bin, env, args)
			var created protocol.SessionCreateResult
			h1.call(t, "session/create", protocol.SessionCreateParams{Cwd: ws, Model: route, PermissionMode: "workspace-write"}, &created)
			session := created.SessionID
			vars["session"] = session
			h1.prompt(t, session, expand(promptT, vars))
			var child string
			waitUntil(t, 5*time.Minute, "the command's recorded pid", func() bool {
				data, err := os.ReadFile(filepath.Join(ws, "pid.txt"))
				child = strings.TrimSpace(string(data))
				return err == nil && child != ""
			})
			t.Logf("the sleeping command is pid %s", child)

			if !killed {
				mustShell(t, expand(stopT, map[string]string{"pid": strconv.Itoa(h1.Process.Pid())}))
				select {
				case <-h1.Process.Exited():
				case <-time.After(time.Minute):
					t.Fatalf("the host did not stop within a minute of the interrupt\nstderr:\n%s", h1.Stderr.String())
				}
				// Before any resume: the stopping host closed everything itself.
				stored := agent3Log(t, expand(logT, vars))
				tail := agent3Types(stored)
				last := slices.Index(tail, "turn/end")
				if !slices.Contains(tail, "tool/result") || !slices.Contains(tail, "step/end") || last < 0 {
					t.Fatalf("after the stop the database holds %v, want a tool/result, step/end and turn/end", tail)
				}
				var end wireTurnEnd
				if err := json.Unmarshal(stored[last].Data, &end); err != nil || end.Reason.Kind != "aborted" || end.Reason.Cause != "disposed" {
					t.Fatalf("the stopping host wrote turn/end %s, want aborted with cause disposed", stored[last].Data)
				}
				if _, err := shell(t, expand(aliveT, map[string]string{"pid": child})); err == nil {
					t.Fatalf("the sleeping command (pid %s) outlived the stopped host", child)
				}
			} else {
				kill(t, h1)
			}
			before := len(agent3Log(t, expand(logT, vars)))

			h2 := startSDK(t, bin, env, args)
			resume(t, h2, session, route, killed)
			stored := agent3Log(t, expand(logT, vars))
			for i, e := range stored {
				if e.Seq != stored[0].Seq+int64(i) {
					t.Fatalf("sequence gap at %d: seq %d after %d", i, e.Seq, stored[i-1].Seq)
				}
			}
			added := agent3Types(stored[before:])
			if !killed {
				if slices.ContainsFunc(added, func(typ string) bool { return typ == "tool/result" || typ == "step/end" || typ == "turn/end" }) {
					t.Fatalf("resuming a stopped session appended closers %v; the stop had closed it", added)
				}
				return
			}
			if !slices.Equal(added[:min(3, len(added))], []string{"tool/result", "step/end", "turn/end"}) {
				t.Fatalf("resuming a killed session appended %v, want tool/result, step/end, turn/end first", added)
			}
			if !strings.Contains(string(stored[before].Data), "tools/outcome-unknown") {
				t.Fatalf("the closing tool/result is not outcome-unknown: %s", stored[before].Data)
			}
			if slices.ContainsFunc(added, func(typ string) bool { return typ == "step/start" || typ == "turn/start" }) {
				t.Fatalf("resume continued the killed turn: %v", added)
			}
		})
	}
}

// agent3Event is one stored event as the log hook prints it.
type agent3Event struct {
	Seq  int64           `json:"seq"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func agent3Log(t *testing.T, line string) []agent3Event {
	t.Helper()
	var evs []agent3Event
	if err := json.Unmarshal([]byte(mustShell(t, line)), &evs); err != nil || len(evs) == 0 {
		t.Fatalf("%s printed no events: %v", line, err)
	}
	return evs
}

func agent3Types(evs []agent3Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.Type
	}
	return out
}
