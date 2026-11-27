//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
	"github.com/smartpcr/agent-plugin/src/sdk/sdk"
)

// The helpers of the later-run scenarios authored in DH-S-16 (implementation-plan
// Stage 6.11; tech-spec sec. 4.5): DH-E-SDK-1, DH-E-SDK-2, DH-E-UTIL-2, DH-E-CLI-1,
// DH-E-CLI-2 and DH-E-BUNDLE-1 to -4 and DH-E-API-5. Each runs in scenario.Run and
// reaches the product only through the built binary, src/sdk/... and the test
// kit's launcher. What the real environment adds and the scenario cannot drive
// itself -- a real browser, keystrokes on a terminal, a recording proxy, the
// settings page, a hosted workflow -- is an operator command template the run's
// operator sets, as DH-E-HOST-1's browser driver is.

// operatorStep runs the operator command template that input names, with vars
// expanded, and returns its output; a non-zero exit fails the scenario.
func operatorStep(t *testing.T, input, what string, vars map[string]string) string {
	t.Helper()
	return mustShell(t, expand(need(t, input, what), vars))
}

// operatorJSON is operatorStep for a command that writes one JSON object to
// {out}; the object is decoded into v.
func operatorJSON(t *testing.T, input, what string, vars map[string]string, v any) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "step.json")
	all := map[string]string{"out": out}
	for k, x := range vars {
		all[k] = x
	}
	operatorStep(t, input, what, all)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("%s wrote no report: %v", input, err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s's report: %v\n%s", input, err, data)
	}
}

// profileLayer writes text as the profile layer of profile in home
// (<home>/config/profiles/<profile>/layer.yml, the one writable layer of a launch)
// and returns its path. The Go client cannot pass --layer, so this is how a
// scenario gives a client-started host its configuration.
func profileLayer(t *testing.T, home, profile, text string) string {
	t.Helper()
	path := filepath.Join(home, "config", "profiles", profile, "layer.yml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// layerInput reads the layer template file that input names and expands vars in
// it.
func layerInput(t *testing.T, input, what string, vars map[string]string) string {
	t.Helper()
	data, err := os.ReadFile(need(t, input, what))
	if err != nil {
		t.Fatalf("%s: %v", input, err)
	}
	return expand(string(data), vars)
}

// pidAlive reports whether a process with pid runs, through the platform's own
// listing (tasklist on Windows, kill -0 elsewhere).
func pidAlive(t *testing.T, pid int) bool {
	t.Helper()
	if pid <= 0 {
		return false
	}
	id := strconv.Itoa(pid)
	if runtime.GOOS == "windows" {
		out, _ := shell(t, `tasklist /FI "PID eq `+id+`" /NH`)
		return strings.Contains(out, " "+id+" ")
	}
	_, err := shell(t, "kill -0 "+id)
	return err == nil
}

// goneWithin fails the scenario when pid still runs after within.
func goneWithin(t *testing.T, pid int, what string, within time.Duration) {
	t.Helper()
	for end := time.Now().Add(within); pidAlive(t, pid); time.Sleep(200 * time.Millisecond) {
		if time.Now().After(end) {
			t.Errorf("%s (pid %d) still runs %s later", what, pid, within)
			return
		}
	}
}

// readPid reads the process id a fixture command wrote to path, waiting up to
// within for it.
func readPid(t *testing.T, path string, within time.Duration) int {
	t.Helper()
	for end := time.Now().Add(within); ; time.Sleep(100 * time.Millisecond) {
		if data, err := os.ReadFile(path); err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 {
				return pid
			}
		}
		if time.Now().After(end) {
			t.Fatalf("no process id in %s within %s", path, within)
		}
	}
}

// dshIn runs the built binary in the scenario's home and workspace (S.Program)
// with args and waits up to within; a run that outlives it fails the scenario.
func dshIn(t *testing.T, s *scenario.S, within time.Duration, env []string, args ...string) dshRun {
	t.Helper()
	h, err := launcher.Run(t, within, s.Program(env, append([]string{"--home", s.Home}, args...)...))
	if err != nil {
		t.Fatalf("dsh %v: %v within %s\nstdout:\n%s\nstderr:\n%s", args, err, within, h.Stdout.String(), h.Stderr.String())
	}
	return dshRun{stdout: h.Stdout.String(), stderr: h.Stderr.String(), code: h.Process.ExitCode(), took: time.Since(h.Started)}
}

// historyCall is one JSON-RPC call; *jsonrpc.Conn's Call satisfies it.
type historyCall func(ctx context.Context, method string, params, result any) error

// historyPages reads every event of session from seq 0 through session/history,
// following More until the host says the history is complete: a page is capped
// by count and by bytes (sdkserver session/history), so one call is not enough.
func historyPages(ctx context.Context, call historyCall, session string) ([]protocol.Event, error) {
	var evs []protocol.Event
	for after := int64(0); ; {
		var page protocol.SessionHistoryResult
		if err := call(ctx, "session/history", protocol.SessionHistoryParams{SessionID: session, AfterSeq: after, Limit: 1000}, &page); err != nil {
			return evs, err
		}
		evs = append(evs, page.Events...)
		if !page.More {
			return evs, nil
		}
		if len(page.Events) == 0 {
			return evs, fmt.Errorf("session/history of %s after seq %d said more but returned no event", session, after)
		}
		after = page.Events[len(page.Events)-1].Seq
	}
}

// storedHistory reads every event of session in home, from seq 0, on an sdk
// host of its own over raw JSON-RPC. session/history needs no lease, so it reads
// a session another host (a running dsh web) owns, and it neither resumes nor
// changes the session.
func storedHistory(t *testing.T, s *scenario.S, home, session string) []protocol.Event {
	t.Helper()
	ctx := s.Context()
	cmd := exec.Command(s.Bin, "--home", home, "--profile", "sdk")
	cmd.Dir, cmd.Env = s.Workspace, append(os.Environ(), "DSH_HOME="+home)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("a history host on %s: %v", home, err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	c := jsonrpc.Serve(ctx, stdout, stdin, jsonrpc.Options{})
	var init protocol.InitializeResult
	if err := c.Call(ctx, protocol.InitializeMethod, protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "dsh-scenario-history", Version: "1"}}, &init); err != nil {
		t.Fatalf("initialize the history host on %s: %v", home, err)
	}
	evs, err := historyPages(ctx, c.Call, session)
	if err != nil {
		t.Fatalf("history of %s: %v", session, err)
	}
	_ = c.Call(ctx, "shutdown", protocol.Empty{}, nil)
	_ = stdin.Close()
	_ = cmd.Wait()
	return evs
}

// newestSession is the id of the newest root session in home, read through the
// Go client on a host of its own.
func newestSession(t *testing.T, s *scenario.S, home string) string {
	t.Helper()
	ctx := s.Context()
	h, err := sdk.Start(ctx, sdk.Options{Bin: s.Bin, Home: home, Dir: s.Workspace})
	if err != nil {
		t.Fatalf("a host on %s: %v", home, err)
	}
	defer func() { _ = h.Close(context.WithoutCancel(ctx)) }()
	page, err := h.List(ctx, sdk.ListOptions{})
	if err != nil || len(page.Sessions) == 0 {
		t.Fatalf("no session in %s: %v", home, err)
	}
	return page.Sessions[0].SessionID
}

// lastLine is the last non-blank line of out, trimmed.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// headlessResult is the run/result line dsh headless --json writes last
// (internal/cli/headless runResult).
type headlessResult struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Turn    int    `json:"turn"`
	Reason  string `json:"reason"`
	Text    string `json:"text"`
}

// runResultLine decodes the last line of a --json run's stdout as its run/result
// and fails unless it is one naming a session.
func runResultLine(t *testing.T, stdout string) headlessResult {
	t.Helper()
	var r headlessResult
	if strings.TrimSpace(stdout) == "" {
		t.Fatal("the --json run wrote no result line")
	}
	if err := json.Unmarshal([]byte(lastLine(stdout)), &r); err != nil || r.Type != "run/result" || r.Session == "" {
		t.Fatalf("the last --json line is not a run/result naming its session (%v):\n%s", err, stdout)
	}
	return r
}

// exportLines parses every non-blank line of out as one JSON object and returns
// them; a line that does not parse fails the scenario.
func exportLines(t *testing.T, out string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for i, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("result line %d is not an export line: %v\n%s", i+1, err, l)
		}
		lines = append(lines, m)
	}
	return lines
}
