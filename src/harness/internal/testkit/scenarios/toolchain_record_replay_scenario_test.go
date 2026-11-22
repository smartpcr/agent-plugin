//go:build scenario

package scenarios

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRecordThenReplayWithoutAKey: record against the real model, then replay
// without a key (upstream/toolchain.md DH-E-TOOLCHAIN-1; DH-ADR-TOOLCHAIN-5,
// DH-ADR-TOOLCHAIN-6, DH-ADR-TOOLCHAIN-12). Authored in DH-S-11, once its subjects
// (the model runtime and the replay helpers) exist; executed in DH-S-17 on a
// hosted Linux runner.
//
// Given a clean checkout, a dsh binary built from it, a model API key supplied at
// run time and one scenario directory that holds only a manifest and a workspace
// seed, when the replay package is run in record mode for that scenario (on a
// copy, so the checkout is never written), the key is removed from the
// environment and the package is run in replay mode twice, then record writes a
// session log and, for a file-changing scenario, the product's workspace equals
// workspace.expected/; both replays pass with no request reaching the network
// (every proxy variable names a listener that counts and refuses); the fixture
// directory has no differences after the replays; and each run report lists the
// scenario as executed, not skipped. Cleanup: the copy, the homes and the
// workspaces are under t.TempDir(). On failure the commands' output, the child's
// standard error among it, is in the failure message and the unnormalised log
// stays in the copy, whose path is logged.
//
// Inputs: DSH_DEEPSEEK_API_KEY is the real key, handed to the record run as
// DEEPSEEK_API_KEY; DSH_SCENARIO_TOOLCHAIN1_DIR the scenario directory relative to
// the repository root; DSH_SCENARIO_TOOLCHAIN1_RECORD and
// DSH_SCENARIO_TOOLCHAIN1_REPLAY command lines over {bin} (the built dsh),
// {scenario} (the copy), {home} and {workspace} that run the replay package in
// record and replay mode and print its run report, a line "<name> executed" or
// "<name> skipped" per scenario.
//
//dsh:scenario DH-E-TOOLCHAIN-1
func TestRecordThenReplayWithoutAKey(t *testing.T) {
	key := need(t, "DSH_DEEPSEEK_API_KEY", "a real DeepSeek API key")
	rel := need(t, "DSH_SCENARIO_TOOLCHAIN1_DIR", "a scenario directory holding a manifest and a workspace seed")
	recordT := need(t, "DSH_SCENARIO_TOOLCHAIN1_RECORD", "the replay package's record command")
	replayT := need(t, "DSH_SCENARIO_TOOLCHAIN1_REPLAY", "the replay package's replay command")

	root := repositoryRoot(t)
	if status := strings.TrimSpace(run(t, root, nil, "git", "status", "--porcelain")); status != "" {
		t.Fatalf("the checkout is not clean:\n%s", status)
	}
	bin := dshBinary(t)
	scenario := filepath.Join(t.TempDir(), filepath.Base(rel))
	copyTree(t, filepath.Join(root, filepath.FromSlash(rel)), scenario)
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the scenario copy, with the recorded log, is at %s", scenario)
		}
	})
	name := filepath.Base(scenario)
	vars := func() map[string]string {
		return map[string]string{"bin": bin, "scenario": scenario, "home": t.TempDir(), "workspace": t.TempDir()}
	}

	// Record against the real model.
	rv := vars()
	report := shellEnv(t, expand(recordT, rv), "DEEPSEEK_API_KEY="+key)
	assertExecuted(t, "record", report, name)
	if treeDigest(t, scenario) == "" {
		t.Fatal("the scenario copy is empty after record")
	}
	if expected := filepath.Join(scenario, "workspace.expected"); exists(expected) {
		if got, want := treeDigest(t, rv["workspace"]), treeDigest(t, expected); got != want {
			t.Fatalf("the product's workspace differs from workspace.expected/ (digest %s, want %s)", got, want)
		}
	}
	fixture := treeDigest(t, scenario)

	// Replay twice with no key and no network.
	var dialled atomic.Int64
	refuse := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dialled.Add(1)
		http.Error(w, "the replay must not reach the network", http.StatusForbidden)
	}))
	t.Cleanup(refuse.Close)
	offline := []string{
		"DEEPSEEK_API_KEY=", "DSH_DEEPSEEK_API_KEY=",
		"HTTP_PROXY=" + refuse.URL, "HTTPS_PROXY=" + refuse.URL, "ALL_PROXY=" + refuse.URL, "NO_PROXY=",
		"http_proxy=" + refuse.URL, "https_proxy=" + refuse.URL, "all_proxy=" + refuse.URL, "no_proxy=",
	}
	for i := range 2 {
		assertExecuted(t, "replay", shellEnv(t, expand(replayT, vars()), offline...), name)
		if n := dialled.Load(); n != 0 {
			t.Fatalf("replay %d made %d network requests", i+1, n)
		}
		if got := treeDigest(t, scenario); got != fixture {
			t.Fatalf("replay %d changed the fixture directory", i+1)
		}
	}
}

// assertExecuted checks that a run report lists name as executed, not skipped.
func assertExecuted(t *testing.T, mode, report, name string) {
	t.Helper()
	if strings.Contains(report, name+" skipped") || !strings.Contains(report, name+" executed") {
		t.Fatalf("the %s report does not list %s as executed:\n%s", mode, name, report)
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
