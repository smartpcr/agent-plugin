package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck"
)

// TestOffFloorToolchainFailsSetup: with a go on PATH whose version is not the floor
// (testdata/fake-go answers go1.99.0 and cannot switch toolchains), the gate fails
// setup with exit 2, naming both versions, before any gate starts (M1-G-TOOL-14;
// DH-T-TOOLCHAIN-9).
func TestOffFloorToolchainFailsSetup(t *testing.T) {
	bin := t.TempDir()
	fake := filepath.Join(bin, "go")
	if runtime.GOOS == "windows" {
		fake += ".exe"
	}
	build := exec.Command("go", "build", "-o", fake, ".")
	build.Dir = filepath.Join("testdata", "fake-go")
	build.Env = setEnv(os.Environ(), "GOWORK=off")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build the fake go: %v\n%s", err, out)
	}
	gate := buildGate(t)
	root := scratch(t, "unformatted-file")
	for _, args := range [][]string{
		{"--root", root},
		{"rehearse", "--root", root},
	} {
		cmd := exec.Command(gate, args...)
		cmd.Env = setEnv(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+getEnv(os.Environ(), "PATH"))
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 2 {
			t.Fatalf("harness-gate %v = %v, want exit 2\n%s%s", args, err, stdout.String(), stderr.String())
		}
		for _, want := range []string{"toolchain go1.99.0 is not the go floor go" + modcheck.Floor, "go.mod"} {
			if !strings.Contains(stderr.String(), want) {
				t.Errorf("harness-gate %v: stderr lacks %q:\n%s", args, want, stderr.String())
			}
		}
		if strings.Contains(stdout.String(), "gate 1/") || strings.Contains(stdout.String(), "rehearse: cmd/dsh") {
			t.Errorf("harness-gate %v started work on an off-floor toolchain:\n%s", args, stdout.String())
		}
	}
}

// TestGateRunsChildrenOnTheFloor: on any host default toolchain the gate prints the
// floor it selected and every child -- the gates' and the budget gate's
// GOTOOLCHAIN=local one -- reports the floor (M1-G-TOOL-14; DH-T-TOOLCHAIN-9).
func TestGateRunsChildrenOnTheFloor(t *testing.T) {
	want := "go" + modcheck.Floor
	for root, source := range map[string]string{
		scratch(t, "race-seeded"):      "go.mod",
		filepath.Join("..", ".."):      "src/harness/go.mod",
		scratch(t, "unformatted-file"): "go.mod",
	} {
		cfg := newTestConfig(t, root)
		tc := cfg.Toolchain
		if tc.Version != want || tc.Floor != modcheck.Floor || tc.Source != source || cfg.Go != tc.Go {
			t.Errorf("%s: toolchain = %+v, Go = %s; want %s read from %s", root, tc, cfg.Go, want, source)
		}
		for name, env := range map[string][]string{"gate": cfg.env(), "budget": BudgetEnv(cfg.Environ)} {
			out, err := cfg.command(context.Background(), cfg.Root, env, cfg.Go, "version")
			if err != nil || VersionOf(out) != want {
				t.Errorf("%s: %s child: go version = %q (%v), want %s", root, name, out, err, want)
			}
			out, err = cfg.command(context.Background(), cfg.Root, env, cfg.Go, "env", "GOROOT")
			if err != nil || strings.TrimSpace(out) != tc.GOROOT {
				t.Errorf("%s: %s child: GOROOT = %q (%v), want %s", root, name, out, err, tc.GOROOT)
			}
		}
		if path := getEnv(cfg.env(), "PATH"); !strings.HasPrefix(path, filepath.Join(tc.GOROOT, "bin")+string(os.PathListSeparator)) {
			t.Errorf("%s: the floor's bin is not first on PATH: %s", root, path)
		}
		if got := getEnv(cfg.env(), "GOTOOLCHAIN"); got != want {
			t.Errorf("%s: children's GOTOOLCHAIN = %q, want %s", root, got, want)
		}
	}

	out, code := runGate(t, scratch(t, "unformatted-file"))
	assertStoppedAt(t, out, code, "gofmt")
	if line := "harness-gate: toolchain " + want + " (the go floor of go.mod;"; !strings.HasPrefix(out, line) {
		t.Errorf("output does not start with %q:\n%s", line, out)
	}
}

// TestFloorErrorsNameBothVersions covers the setup errors in-process: a go.mod
// without exactly one go line, and the off-floor message (DH-T-TOOLCHAIN-9).
func TestFloorErrorsNameBothVersions(t *testing.T) {
	dir := t.TempDir()
	for name, src := range map[string]string{
		"none.mod": "module example.com/x\n",
		"two.mod":  "module example.com/x\n\ngo 1.27.4\ngo 1.28.0\n",
		"one.mod":  "module example.com/x\n\ngo 1.27.4 // the floor\n",
	} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		floor, err := ReadFloor(path)
		switch {
		case name == "one.mod" && (err != nil || floor != "1.27.4"):
			t.Errorf("ReadFloor(%s) = %q, %v; want 1.27.4", name, floor, err)
		case name != "one.mod" && (err == nil || !strings.Contains(err.Error(), "want exactly one go line")):
			t.Errorf("ReadFloor(%s) = %q, %v; want an error", name, floor, err)
		}
	}
	if _, err := ReadFloor(filepath.Join(dir, "missing.mod")); err == nil {
		t.Error("ReadFloor of a missing file passed")
	}

	msg := (&FloorError{Floor: "go1.27.4", Got: "go1.26.0", Source: "src/harness/go.mod", Go: "go"}).Error()
	for _, want := range []string{"go1.27.4", "go1.26.0", "src/harness/go.mod", "GOTOOLCHAIN=go1.27.4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("FloorError %q lacks %q", msg, want)
		}
	}
	for line, want := range map[string]string{
		"go version go1.26.0 windows/amd64\n":     "go1.26.0",
		"go version go1.27.4 linux/amd64":         "go1.27.4",
		"go version devel go1.28-abc linux/amd64": "devel go1.28-abc linux/amd64",
		"not a version":                           "not a version",
	} {
		if got := VersionOf(line); got != want {
			t.Errorf("VersionOf(%q) = %q, want %q", line, got, want)
		}
	}
}
