package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runMain runs main with os.Stdout redirected to a file and returns what it printed.
func runMain(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdout")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = file
	defer func() { os.Stdout = stdout }()
	main()
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestMainPrintsReleaseVersion(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = "v1.2.3"
	if got := runMain(t); got != "dsh v1.2.3\n" {
		t.Errorf("main printed %q, want %q", got, "dsh v1.2.3\n")
	}
}

func TestMainFallsBackToBuildVersion(t *testing.T) {
	saved := version
	t.Cleanup(func() { version = saved })
	version = ""
	got := runMain(t)
	v, ok := strings.CutPrefix(strings.TrimSuffix(got, "\n"), "dsh ")
	if !ok || v == "" || strings.ContainsAny(v, " \n") {
		t.Errorf("main printed %q, want \"dsh <module version>\"", got)
	}
}
