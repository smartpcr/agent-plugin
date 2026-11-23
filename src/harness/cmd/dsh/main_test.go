package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runMain runs main as `dsh <args...>` with os.Stdout redirected to a file and
// returns what it printed and the status it exited with.
func runMain(t *testing.T, args ...string) (string, int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	stdout, savedArgs, savedExit := os.Stdout, os.Args, exit
	status := -1
	os.Stdout, os.Args = file, append([]string{"dsh"}, args...)
	exit = func(code int) { status = code }
	defer func() { os.Stdout, os.Args, exit = stdout, savedArgs, savedExit }()
	main()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), status
}

func TestMainPrintsReleaseVersion(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3"
	got, status := runMain(t, "--version")
	if status != 0 || !strings.HasPrefix(got, "dsh v1.2.3 (") {
		t.Errorf("main printed %q with status %d, want \"dsh v1.2.3 (...)\" and 0", got, status)
	}
}

func TestMainFallsBackToBuildVersion(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = ""
	got, status := runMain(t, "version")
	v, ok := strings.CutPrefix(strings.TrimSuffix(got, "\n"), "dsh ")
	if status != 0 || !ok || v == "" || strings.Contains(v, "\n") || strings.HasPrefix(v, " ") {
		t.Errorf("main printed %q with status %d, want \"dsh <module version> (...)\"", got, status)
	}
}

// TestMainServesTheListingMode: the hidden mode word reaches the listing-only
// MCP server of the signed-in client routes before the command tree; without
// its flags it refuses with status 2 and prints nothing on standard output.
//
//dsh:test DH-T-LLM-19
func TestMainServesTheListingMode(t *testing.T) {
	got, status := runMain(t, "mcp-listing")
	if status != 2 || got != "" {
		t.Errorf("main printed %q with status %d, want nothing and 2", got, status)
	}
}
