package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/smartpcr/agent-plugin/tools/harness-gate/modcheck"
)

// FloorModule is the go.mod whose go line is the toolchain floor of a repository
// root (M1-G-TOOL-14). A single-module root (the seeded modules of testdata/) uses
// its own go.mod instead.
const FloorModule = "src/harness/go.mod"

// Toolchain is the go toolchain every child of the gate runs on: exactly the go
// floor, selected with GOTOOLCHAIN=go<floor> (Go fetches it when the default go on
// PATH is another release) and then run directly from its own GOROOT, so a child
// that sets GOTOOLCHAIN=local (the budget gate) still runs on the floor.
type Toolchain struct {
	Floor   string // the go line, e.g. "1.27.4"
	Source  string // the go.mod the floor was read from, slash-separated, relative to the root
	Version string // what "go version" reported, e.g. "go1.27.4"
	Line    string // the whole "go version" line
	GOROOT  string // the floor toolchain's GOROOT
	Go      string // GOROOT/bin/go
}

// Name is the toolchain name of the floor, e.g. "go1.27.4".
func (t Toolchain) Name() string { return "go" + t.Floor }

// String is the line the gate prints before its first gate.
func (t Toolchain) String() string {
	return fmt.Sprintf("toolchain %s (the go floor of %s; children run with GOTOOLCHAIN=%s, GOROOT=%s)", t.Version, t.Source, t.Name(), t.GOROOT)
}

// FloorError is the setup error of a toolchain that is not the floor. The gate exits
// 2 on it; there is no flag that accepts another toolchain (QG-2).
type FloorError struct {
	Floor  string // e.g. "go1.27.4"
	Got    string // e.g. "go1.26.0"
	Source string // the go.mod the floor was read from
	Go     string // the go command that reported Got
}

func (e *FloorError) Error() string {
	return fmt.Sprintf("toolchain %s is not the go floor %s of %s (%s version, with GOTOOLCHAIN=%s); install %s or let Go fetch it",
		e.Got, e.Floor, e.Source, e.Go, e.Floor, e.Floor)
}

// floorSource is the go.mod the floor of root is read from.
func floorSource(root string, modules []string) string {
	if len(modules) == 1 && modules[0] == root {
		return filepath.Join(root, "go.mod")
	}
	return filepath.Join(root, filepath.FromSlash(FloorModule))
}

// ReadFloor returns the version of the single go line of the go.mod at path.
func ReadFloor(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read the go floor: %w", err)
	}
	f, err := modcheck.Parse(path, data)
	if err != nil {
		return "", err
	}
	lines := f.All("go")
	if len(lines) != 1 {
		return "", fmt.Errorf("%s: want exactly one go line (the toolchain floor), found %d", path, len(lines))
	}
	return lines[0].Args, nil
}

// ResolveToolchain selects the floor toolchain through gobin, the go command on
// PATH, and verifies it: "go version" under GOTOOLCHAIN=go<floor> must report the
// floor, and so must the floor's own go binary run from its GOROOT. Any other
// version is a *FloorError naming both.
func ResolveToolchain(ctx context.Context, gobin, dir string, environ []string, floor, source string) (Toolchain, error) {
	tc := Toolchain{Floor: floor, Source: source}
	env := setEnv(environ, "GOTOOLCHAIN="+tc.Name(), "GOWORK=off")
	_, version, err := goVersion(ctx, gobin, dir, env)
	if err != nil {
		return tc, err
	}
	if version != tc.Name() {
		return tc, &FloorError{Floor: tc.Name(), Got: version, Source: source, Go: gobin}
	}
	out, err := runCommand(ctx, dir, env, gobin, "env", "GOROOT")
	if err != nil {
		return tc, fmt.Errorf("%s env GOROOT (GOTOOLCHAIN=%s): %w\n%s", gobin, tc.Name(), err, out)
	}
	tc.GOROOT = strings.TrimSpace(out)
	tc.Go = filepath.Join(tc.GOROOT, "bin", "go")
	if runtime.GOOS == "windows" {
		tc.Go += ".exe"
	}
	tc.Line, version, err = goVersion(ctx, tc.Go, dir, tc.Environ(environ))
	if err != nil {
		return tc, err
	}
	if version != tc.Name() {
		return tc, &FloorError{Floor: tc.Name(), Got: version, Source: source, Go: tc.Go}
	}
	tc.Version = version
	return tc, nil
}

// Environ is environ with the floor selected for every child: GOTOOLCHAIN=go<floor>,
// GOROOT the floor's, and its bin directory first on PATH, so a test that starts
// "go" itself gets the floor too.
func (t Toolchain) Environ(environ []string) []string {
	path := filepath.Join(t.GOROOT, "bin")
	if old := getEnv(environ, "PATH"); old != "" {
		path += string(os.PathListSeparator) + old
	}
	return setEnv(environ, "GOTOOLCHAIN="+t.Name(), "GOROOT="+t.GOROOT, "PATH="+path)
}

// goVersion runs "gobin version" and returns its line and the version field.
func goVersion(ctx context.Context, gobin, dir string, env []string) (line, version string, err error) {
	out, err := runCommand(ctx, dir, env, gobin, "version")
	if err != nil {
		return "", "", fmt.Errorf("%s version: %w\n%s", gobin, err, out)
	}
	line = strings.TrimSpace(out)
	if i := strings.LastIndex(line, "\n"); i >= 0 {
		line = strings.TrimSpace(line[i+1:]) // a download notice may precede the version line
	}
	return line, VersionOf(line), nil
}

// VersionOf returns the toolchain version of a "go version" line, e.g. "go1.27.4"
// of "go version go1.27.4 windows/amd64"; for a devel toolchain, the whole remainder.
func VersionOf(line string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), "go version ")
	if !ok {
		return strings.TrimSpace(line)
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return rest
	}
	if fields[0] == "devel" {
		return rest
	}
	return fields[0]
}

// printToolchain writes the toolchain line the gate starts with.
func printToolchain(w io.Writer, c *Config) {
	fmt.Fprintf(w, "harness-gate: %s\n", c.Toolchain)
}

// runCommand runs name with args in dir and env and returns its combined output.
func runCommand(ctx context.Context, dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// getEnv is the value of key in env; on Windows names are case-insensitive.
func getEnv(env []string, key string) string {
	want := envKey(key + "=")
	for i := len(env) - 1; i >= 0; i-- {
		if envKey(env[i]) == want {
			_, v, _ := strings.Cut(env[i], "=")
			return v
		}
	}
	return ""
}
