//go:build scenario

package scenarios

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRecordThenReplayWithoutAKey: record against the real model on the
// signed-in client route, then replay with no client, no sign-in and no network
// (upstream/toolchain.md DH-E-TOOLCHAIN-1; DH-ADR-TOOLCHAIN-5,
// DH-ADR-TOOLCHAIN-6, DH-ADR-TOOLCHAIN-12; the route of areas/llm.md
// DH-D-LLM-12). Authored in DH-S-11, once its subjects (the model runtime and
// the replay helpers) exist, and retargeted onto the signed-in client routes by
// gap-closure-plan 17.4.4 (M1-D-CLOSE-4 A); executed in DH-S-17. Under OWNER-2 no
// API key exists, so the record run needs the user's own signed-in client: it
// runs on the operator's workstation, not a hosted runner, until the
// credential-field preflight of gap-closure-plan 19.2 (M1-G-TOOL-16) says how a
// signed-in session reaches a runner.
//
// Given a clean checkout, a dsh binary built from it, no API key anywhere, the
// user's own installed official client signed in as themselves, and one
// scenario directory that holds only a manifest and a workspace seed, when, per
// route, the replay package is run in record mode for that scenario on that
// route (on a copy, so the checkout is never written), and then in replay mode
// twice with every client's configuration home pointed at an empty directory, a
// stand-in for each client first on the PATH, and every proxy variable naming a
// listener that counts and refuses, then record writes a session log (a new,
// nonempty session[.vN].jsonl of JSON lines at the top of the copy, which held
// none before) and, for a file-changing scenario, the product's workspace equals
// workspace.expected/; both replays pass with no client started (no stand-in
// ran), no sign-in in reach and no request reaching the network; the fixture
// directory has no differences after the replays; and each run report lists the
// scenario as executed, not skipped. Cleanup: the copy, the homes, the
// workspaces, the empty configuration homes and the stand-ins are under
// t.TempDir(); nothing of the user's sign-in is touched. On failure the
// commands' output, the child's standard error among it, is in the failure
// message and the unnormalised log stays in the copy, whose path is logged.
//
// The legs: claude-code must pass; copilot-cli is run and recorded either way
// (M1-D-LOOP-6 A; forEachRoute).
//
// Inputs: DSH_SCENARIO_<route>_MODEL (<route> is CLAUDE_CODE or COPILOT_CLI)
// the model; DSH_SCENARIO_TOOLCHAIN1_DIR the scenario directory relative to the
// repository root; DSH_SCENARIO_TOOLCHAIN1_RECORD and
// DSH_SCENARIO_TOOLCHAIN1_REPLAY command lines over {bin} (the built dsh),
// {scenario} (the copy), {home}, {workspace}, {route} (the route id) and
// {model} that run the replay package in record and replay mode and print its
// run report, a line "<name> executed" or "<name> skipped" per scenario. The
// client is the one on the PATH, signed in beforehand by the user.
//
//dsh:scenario DH-E-TOOLCHAIN-1
func TestRecordThenReplayWithoutAKey(t *testing.T) {
	selfCheckSessionLog(t)
	rel := need(t, "DSH_SCENARIO_TOOLCHAIN1_DIR", "a scenario directory holding a manifest and a workspace seed")
	recordT := need(t, "DSH_SCENARIO_TOOLCHAIN1_RECORD", "the replay package's record command over {route} and {model}")
	replayT := need(t, "DSH_SCENARIO_TOOLCHAIN1_REPLAY", "the replay package's replay command")

	root := repositoryRoot(t)
	if status := strings.TrimSpace(run(t, root, nil, "git", "status", "--porcelain")); status != "" {
		t.Fatalf("the checkout is not clean:\n%s", status)
	}
	bin := dshBinary(t)
	forEachRoute(t, func(t *testing.T, r clientRoute) {
		recordThenReplay(t, root, bin, rel, recordT, replayT, r)
	})
}

// noKeys empties every credential variable a model route could read, so the
// runs hold no API key whatever the operator's shell holds; the client's own
// sign-in, in its configuration home, is the only credential in play.
var noKeys = []string{
	"ANTHROPIC_API_KEY=", "ANTHROPIC_AUTH_TOKEN=", "CLAUDE_CODE_OAUTH_TOKEN=",
	"GH_TOKEN=", "GITHUB_TOKEN=", "COPILOT_GITHUB_TOKEN=",
}

// recordThenReplay is DH-E-TOOLCHAIN-1 on route r.
func recordThenReplay(t *testing.T, root, bin, rel, recordT, replayT string, r clientRoute) {
	t.Helper()
	model := r.model(t)
	scenario := filepath.Join(t.TempDir(), filepath.Base(rel))
	copyTree(t, filepath.Join(root, filepath.FromSlash(rel)), scenario)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the scenario copy, with the recorded log, is at %s", scenario)
		}
	})
	name := filepath.Base(scenario)
	vars := func() map[string]string {
		return map[string]string{"bin": bin, "scenario": scenario, "home": t.TempDir(), "workspace": t.TempDir(), "route": r.ID, "model": model}
	}

	// Record against the real model through the user's signed-in client.
	if logs := sessionLogs(t, scenario); len(logs) != 0 {
		t.Fatalf("the scenario directory already holds session logs %v; it must hold only a manifest and a workspace seed", logs)
	}
	rv := vars()
	report := shellEnv(t, expand(recordT, rv), noKeys...)
	assertExecuted(t, "record", report, name)
	assertRecordedLog(t, scenario)
	if expected := filepath.Join(scenario, "workspace.expected"); exists(expected) {
		if got, want := treeDigest(t, rv["workspace"]), treeDigest(t, expected); got != want {
			t.Fatalf("the product's workspace differs from workspace.expected/ (digest %s, want %s)", got, want)
		}
	}
	fixture := treeDigest(t, scenario)

	// Replay twice with no client, no sign-in and no network.
	var dialled atomic.Int64
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialled.Add(1)
		http.Error(w, "the replay must not reach the network", http.StatusForbidden)
	}))
	t.Cleanup(refuse.Close)
	standIns, started := clientStandIns(t)
	offline := append(slices.Clone(noKeys),
		"PATH="+standIns+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HTTP_PROXY="+refuse.URL, "HTTPS_PROXY="+refuse.URL, "ALL_PROXY="+refuse.URL, "NO_PROXY=",
		"http_proxy="+refuse.URL, "https_proxy="+refuse.URL, "all_proxy="+refuse.URL, "no_proxy=",
	)
	for _, c := range clientRoutes {
		offline = append(offline, c.signedOut(t))
	}
	// The stand-ins are found first and record that they ran.
	shellEnv(t, "claude", offline...)
	if n := started(); n != 1 {
		t.Fatalf("self-check: the claude stand-in recorded %d starts, want 1", n)
	}
	before := started()
	for i := range 2 {
		assertExecuted(t, "replay", shellEnv(t, expand(replayT, vars()), offline...), name)
		if n := dialled.Load(); n != 0 {
			t.Fatalf("replay %d made %d network requests", i+1, n)
		}
		if n := started() - before; n != 0 {
			t.Fatalf("replay %d started a client %d times; a replay needs none", i+1, n)
		}
		if got := treeDigest(t, scenario); got != fixture {
			t.Fatalf("replay %d changed the fixture directory", i+1)
		}
	}
}

// clientStandIns writes, into a new directory, a stand-in for each client
// program (claude, copilot) that records each start in a marker file and exits
// 0 with no output, and returns the directory and a count of the starts.
func clientStandIns(t *testing.T) (string, func() int) {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "started.txt")
	for _, prog := range []string{"claude", "copilot"} {
		path, body, mode := filepath.Join(dir, prog), "#!/bin/sh\necho started >> '"+marker+"'\n", os.FileMode(0o755)
		if runtime.GOOS == "windows" {
			path, body, mode = path+".cmd", "@echo started>>\""+marker+"\"\r\n", 0o644
		}
		if err := os.WriteFile(path, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	return dir, func() int {
		data, err := os.ReadFile(marker)
		if err != nil {
			return 0
		}
		return strings.Count(string(data), "started")
	}
}

// assertExecuted checks that a run report lists name as executed, not skipped.
func assertExecuted(t *testing.T, mode, report, name string) {
	t.Helper()
	if strings.Contains(report, name+" skipped") || !strings.Contains(report, name+" executed") {
		t.Fatalf("the %s report does not list %s as executed:\n%s", mode, name, report)
	}
}

// sessionLogName matches a scenario's session logs (upstream/toolchain.md
// sec. 2): session[.vN].jsonl for the parent, session.<ordinal>[.vN].jsonl for
// a child agent.
var sessionLogName = regexp.MustCompile(`^session(\.\d+)?(\.v\d+)?\.jsonl$`)

var parentLogName = regexp.MustCompile(`^session(\.v\d+)?\.jsonl$`)

// sessionLogs lists the session logs at the top of a scenario directory.
func sessionLogs(t *testing.T, dir string) []string {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("listing %s: %v", dir, err)
	}
	var out []string
	for _, e := range ents {
		if e.Type().IsRegular() && sessionLogName.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// assertRecordedLog checks that record wrote the parent's session log into a
// directory that held none: present, nonempty, and every line one JSON object.
func assertRecordedLog(t *testing.T, dir string) {
	t.Helper()
	logs := sessionLogs(t, dir)
	parent := ""
	for _, l := range logs {
		if parentLogName.MatchString(l) {
			parent = l
		}
	}
	if parent == "" {
		t.Fatalf("record wrote no parent session log (session[.vN].jsonl) into %s; found %v", dir, logs)
	}
	for _, l := range logs {
		data, err := os.ReadFile(filepath.Join(dir, l))
		if err != nil {
			t.Fatalf("reading the recorded log %s: %v", l, err)
		}
		if err := checkSessionLog(data); err != nil {
			t.Fatalf("the recorded log %s: %v", l, err)
		}
	}
}

// checkSessionLog accepts a JSON Lines log with at least one line, each line a
// nonempty JSON object. A JSON null decodes into a map without error and leaves
// it nil, so it is refused explicitly, as are arrays, scalars and {}.
func checkSessionLog(data []byte) error {
	lines := 0
	for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			return fmt.Errorf("line %d is not a JSON object: %v\n%s", i+1, err, line)
		}
		if len(obj) == 0 {
			return fmt.Errorf("line %d is not a nonempty JSON object: %s", i+1, line)
		}
		lines++
	}
	if lines == 0 {
		return errors.New("the log is empty")
	}
	return nil
}

// selfCheckSessionLog proves the log oracle refuses what record must not pass
// off as a session log, and accepts a real line. It needs no key, so it runs
// before the scenario's inputs are read.
func selfCheckSessionLog(t *testing.T) {
	t.Helper()
	for name, data := range map[string]string{
		"empty": "", "blank lines": "\n\r\n  \n", "null": "null\n", "null after a line": "{\"seq\":1}\nnull\n",
		"empty object": "{}\n", "array": "[{\"seq\":1}]\n", "scalar": "1\n", "string": "\"x\"\n", "torn": "{\"seq\":1}\n{\"seq\":\n",
	} {
		if err := checkSessionLog([]byte(data)); err == nil {
			t.Fatalf("the session-log oracle accepted %s: %q", name, data)
		}
	}
	if err := checkSessionLog([]byte("{\"seq\":1,\"type\":\"turn/start\"}\r\n{\"seq\":2,\"type\":\"turn/end\"}\n")); err != nil {
		t.Fatalf("the session-log oracle refused a valid log: %v", err)
	}
}

// shellEnv runs line through the platform shell with env set over the parent
// environment (a later entry wins) and returns its combined output; a failure
// fails the scenario with that output.
func shellEnv(t *testing.T, line string, env ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "sh", "-c", line)
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(t.Context(), "cmd", "/C", line)
	}
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", line, err, out)
	}
	return string(out)
}

// copyTree copies the regular files under src to dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copying %s: %v", src, err)
	}
}

// treeDigest is a SHA-256 over the relative paths and contents of the regular
// files under dir, in walk order; an empty tree digests to "".
func treeDigest(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		h.Write([]byte(filepath.ToSlash(rel) + "\x00"))
		h.Write(bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n")))
		n++
		return nil
	})
	if err != nil {
		t.Fatalf("digesting %s: %v", dir, err)
	}
	if n == 0 {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
