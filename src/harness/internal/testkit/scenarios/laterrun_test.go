//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/scenario"
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

// storedHistory reads every event of session in home, from seq 0, through the Go
// client on a host of its own; the session is resumed, so no other host may hold
// it.
func storedHistory(t *testing.T, s *scenario.S, home, session string) []protocol.Event {
	t.Helper()
	ctx := s.Context()
	h, err := sdk.Start(ctx, sdk.Options{Bin: s.Bin, Home: home, Dir: s.Workspace})
	if err != nil {
		t.Fatalf("a host on %s: %v", home, err)
	}
	defer func() {
		if err := h.Close(context.WithoutCancel(ctx)); err != nil {
			t.Errorf("close the host on %s: %v", home, err)
		}
	}()
	sess, err := h.Resume(ctx, session, sdk.ResumeOptions{})
	if err != nil {
		t.Fatalf("resume %s: %v", session, err)
	}
	evs, err := sess.History(ctx, -1, 5000)
	if err != nil {
		t.Fatalf("history of %s: %v", session, err)
	}
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
