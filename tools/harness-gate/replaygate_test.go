package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// replayRoot is a repository root whose one module holds cmd/dsh and, unless
// body is empty, a replay-tagged test with body.
func replayRoot(t *testing.T, body string) *Config {
	t.Helper()
	root := t.TempDir()
	mod := filepath.Join(root, "src", "harness")
	writeFile(t, filepath.Join(mod, "go.mod"), "module example.com/harness\n\ngo 1.26\n")
	writeFile(t, filepath.Join(mod, "cmd", "dsh", "main.go"), "package main\n\nfunc main() {}\n")
	if body != "" {
		writeFile(t, filepath.Join(mod, "test", "replay", "r_test.go"),
			"//go:build replay\n\npackage replay\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nvar _ = os.Getenv\n\nfunc TestR(t *testing.T) {"+body+"}\n")
	}
	return &Config{Root: root, Modules: []string{mod}, Go: "go", Environ: setEnv(os.Environ(), "GOFLAGS=", "GOTOOLCHAIN=local", replayRefreshEnv+"=1")}
}

// The harness-replay gate runs the replay-tagged tests of the dsh module, never
// passes on the refresh switch, fails when a replay fails, and fails when the
// layer is missing rather than passing on nothing (decision
// replay-gate-step-dh-s-16).
func TestReplayGateRunsTheReplayLayer(t *testing.T) {
	ctx := context.Background()
	var out bytes.Buffer
	c := replayRoot(t, `if os.Getenv("`+replayRefreshEnv+`") != "" { t.Fatal("refresh reached the replay") }`)
	if err := c.replayGate(ctx, &out); err != nil {
		t.Fatalf("replay gate: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "ok   src/harness: go test -count=1 -tags=replay ./test/replay/...") {
		t.Errorf("output lacks the replay step:\n%s", out.String())
	}

	out.Reset()
	if err := replayRoot(t, `t.Fatal("the replay differs")`).replayGate(ctx, &out); err == nil {
		t.Errorf("a failing replay passed the gate:\n%s", out.String())
	}

	if err := replayRoot(t, "").replayGate(ctx, &out); err == nil || !strings.Contains(err.Error(), "the replay layer is missing") {
		t.Errorf("a missing replay layer: %v", err)
	}
	if err := (&Config{Root: t.TempDir()}).replayGate(ctx, &out); err == nil || !strings.Contains(err.Error(), "cmd/dsh") {
		t.Errorf("no dsh module: %v", err)
	}
}
