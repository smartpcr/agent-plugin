package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck"
)

// gitIn runs git in dir with no global or system configuration, so the scratch
// repository does not depend on the host's git settings.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", append([]string{"-c", "user.name=harness-gate", "-c", "user.email=harness-gate@example.invalid", "-c", "core.autocrlf=false"}, args...)...)
	cmd.Dir = dir
	cmd.Env = setEnv(os.Environ(), "GIT_CONFIG_GLOBAL="+global, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// gitScratch copies testdata/<name> into a fresh git repository with one commit and
// returns its path and HEAD.
func gitScratch(t *testing.T, name string) (string, string) {
	t.Helper()
	root := scratch(t, name)
	gitIn(t, root, "init", "-q")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "commit", "-q", "-m", "seed "+name)
	return root, gitIn(t, root, "rev-parse", "HEAD")
}

// TestEvidenceRefusesDirtyTree is the scenario "Dirty tree refuses evidence":
// harness-gate --evidence on a scratch repository with an untracked and a modified
// file exits 2 naming both paths, writes no file and starts no gate (M1-G-TOOL-12;
// DH-T-TOOLCHAIN-9).
func TestEvidenceRefusesDirtyTree(t *testing.T) {
	root, _ := gitScratch(t, "unformatted-file")
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("draft\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "add_test.go"), []byte("package add\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(t.TempDir(), "out.txt")
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--root", root, "--evidence", record}, &stdout, &stderr)
	if code != 2 {
		t.Errorf("exit = %d, want 2\n%s%s", code, stdout.String(), stderr.String())
	}
	for _, want := range []string{"is not clean", "notes.txt", "add_test.go"} {
		if !strings.Contains(stderr.String(), want) {
			t.Errorf("stderr lacks %q:\n%s", want, stderr.String())
		}
	}
	if strings.Contains(stdout.String(), "PASS") || strings.Contains(stdout.String(), "gate 1/") {
		t.Errorf("a gate ran on a dirty tree:\n%s", stdout.String())
	}
	if _, err := os.Stat(record); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("an evidence file was written on a dirty tree: %v", err)
	}
}

// TestEvidenceRecordsHeaderOutputAndDigest runs harness-gate --evidence in-process on
// a clean scratch repository whose module fails gofmt: the record holds the header,
// the teed output, the exit status and a SHA-256 of every byte before it
// (M1-G-TOOL-12, M1-G-TOOL-18; DH-T-TOOLCHAIN-9).
func TestEvidenceRecordsHeaderOutputAndDigest(t *testing.T) {
	root, head := gitScratch(t, "unformatted-file")
	record := filepath.Join(t.TempDir(), "run.txt")
	var stdout, stderr bytes.Buffer
	before := time.Now().UTC().Truncate(time.Second)
	code := run(context.Background(), []string{"--root", root, "--evidence", record}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (gofmt)\n%s%s", code, stdout.String(), stderr.String())
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(stdout.String(), text) {
		t.Errorf("stdout is not the record teed:\nstdout:\n%s\nrecord:\n%s", stdout.String(), text)
	}
	fields := checkRecord(t, text)
	wantHeader := map[string]string{
		"commit":      head,
		"goos/goarch": runtime.GOOS + "/" + runtime.GOARCH,
		"children":    "CI=1",
		"exit":        "1",
	}
	for key, want := range wantHeader {
		if fields[key] != want {
			t.Errorf("%s = %q, want %q", key, fields[key], want)
		}
	}
	prefixes := map[string]string{
		"tree":       "clean",
		"go version": "go version go" + modcheck.Floor + " ",
		"go floor":   "go" + modcheck.Floor + " (go.mod)",
		"command":    "harness-gate --root ",
	}
	for key, want := range prefixes {
		if !strings.HasPrefix(fields[key], want) {
			t.Errorf("%s = %q, want prefix %q", key, fields[key], want)
		}
	}
	keys := []string{"goroot", "cc", "host os"}
	if runtime.GOOS == "linux" {
		keys = append(keys, "wsl distro", "os-release", "kernel")
	}
	for _, key := range keys {
		if fields[key] == "" || strings.HasPrefix(fields[key], "unavailable") {
			t.Errorf("%s = %q, want a recorded value", key, fields[key])
		}
	}
	started, err1 := time.Parse(time.RFC3339, fields["started"])
	ended, err2 := time.Parse(time.RFC3339, fields["ended"])
	if err := errors.Join(err1, err2); err != nil || started.Before(before) || ended.Before(started) || started.Location() != time.UTC {
		t.Errorf("started %q, ended %q (%v), want UTC times from %s on", fields["started"], fields["ended"], err, before)
	}
	for _, want := range []string{
		"\n--- output ---\nharness-gate: toolchain go" + modcheck.Floor,
		"harness-gate: FAILED at gate gofmt\n--- end of output ---\nended: ",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("record lacks %q:\n%s", want, text)
		}
	}
}

// TestEvidenceChildrenRunWithCI: every child of an evidence run sees CI=1, a passing
// run records exit 0, and a failed write is a setup error (DH-T-TOOLCHAIN-9).
func TestEvidenceChildrenRunWithCI(t *testing.T) {
	root, head := gitScratch(t, "race-seeded")
	cfg := newTestConfig(t, root)
	cfg.Environ = setEnv(cfg.Environ, "CI=")
	record := filepath.Join(t.TempDir(), "run.txt")
	gates := func(c *Config) []Gate {
		return []Gate{{Name: "env", Title: "reports the children's CI", Run: func(ctx context.Context, w io.Writer) error {
			_, err := fmt.Fprintf(w, "child CI=%s\n", getEnv(c.env(), "CI"))
			return err
		}}}
	}
	var stdout bytes.Buffer
	if code := cfg.RunEvidence(context.Background(), record, head, []string{"--root", root}, gates, &stdout, io.Discard); code != 0 {
		t.Fatalf("exit = %d, want 0\n%s", code, stdout.String())
	}
	data, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	fields := checkRecord(t, string(data))
	if fields["exit"] != "0" || !strings.Contains(string(data), "child CI=1\nharness-gate: PASS env") {
		t.Errorf("record:\n%s", data)
	}

	var stderr bytes.Buffer
	missing := filepath.Join(t.TempDir(), "no-such-dir", "run.txt")
	if code := cfg.RunEvidence(context.Background(), missing, head, nil, gates, io.Discard, &stderr); code != 2 || !strings.Contains(stderr.String(), "--evidence") {
		t.Errorf("unwritable record: exit %d, stderr %q; want 2", code, stderr.String())
	}
}

// checkRecord verifies the final sha256 line of an evidence record and returns its
// "key: value" lines outside the output section.
func checkRecord(t *testing.T, text string) map[string]string {
	t.Helper()
	body, last, ok := cutLastLine(text)
	sum := sha256.Sum256([]byte(body))
	if !ok || last != "sha256: "+hex.EncodeToString(sum[:]) {
		t.Errorf("last line %q is not the SHA-256 %x of the record before it", last, sum)
	}
	if !strings.HasPrefix(text, "harness-gate evidence record\n") {
		t.Errorf("record does not start with its title:\n%s", text)
	}
	fields := map[string]string{}
	inOutput := false
	for _, line := range strings.Split(body, "\n") {
		switch {
		case line == "--- output ---":
			inOutput = true
		case line == "--- end of output ---":
			inOutput = false
		case !inOutput:
			if k, v, ok := strings.Cut(line, ": "); ok {
				fields[k] = v
			}
		}
	}
	return fields
}

func cutLastLine(text string) (body, last string, ok bool) {
	trimmed := strings.TrimSuffix(text, "\n")
	i := strings.LastIndex(trimmed, "\n")
	if i < 0 || trimmed == text {
		return text, "", false
	}
	return text[:i+1], trimmed[i+1:], true
}
