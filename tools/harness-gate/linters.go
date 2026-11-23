package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// The gates of the pinned linters (DH-ADR-TOOLCHAIN-8, -9; M1-D-TOOL-1): staticcheck
// and govulncheck, both run from the binaries the tools module builds for its "tool"
// lines (go.mod), so every lane runs the same versions. staticcheck runs right
// after vet; vulncheck runs after cross-compile, last before self: it is the one
// gate that needs the network (the vulnerability database), so every local check
// has passed before it is reached.

// SelfModulePath is the module path of the tools module, SelfModule.
const SelfModulePath = "github.com/smartpcr/agent-plugin/tools/harness-gate"

// toolsModuleDir is the tools module whose tool lines the linter gates run:
// Config.ToolsDir; else SelfModule under a repository root; else, for a single-module
// root such as a scratch module, the SelfModulePath module enclosing the working
// directory (the gate is run from it). Without one the gate fails: it never skips.
func (c *Config) toolsModuleDir() (string, error) {
	if c.ToolsDir != "" {
		return c.ToolsDir, nil
	}
	if dir := filepath.Join(c.Root, filepath.FromSlash(SelfModule)); isFile(filepath.Join(dir, "go.mod")) {
		return dir, nil
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for dir := wd; ; dir = filepath.Dir(dir) {
		if modulePath(filepath.Join(dir, "go.mod")) == SelfModulePath {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return "", fmt.Errorf("the tools module %s was found neither under %s nor above %s: the pinned linters cannot run", SelfModule, c.Root, wd)
		}
	}
}

// modulePath is the module path a go.mod declares, or "".
func modulePath(gomod string) string {
	f, err := os.Open(gomod)
	if err != nil {
		return ""
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(s.Text()), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// toolBinary builds the tools module's tool name on the floor and returns its path
// ("go tool -n name").
func (c *Config) toolBinary(ctx context.Context, name string) (string, error) {
	dir, err := c.toolsModuleDir()
	if err != nil {
		return "", err
	}
	out, err := c.command(ctx, dir, c.env(), c.Go, "tool", "-n", name)
	if err != nil {
		return "", fmt.Errorf("go tool -n %s in %s: %w\n%s", name, c.rel(dir), err, out)
	}
	bin := lastLine(out)
	if !isFile(bin) {
		return "", fmt.Errorf("go tool -n %s in %s printed %q, which is not a file", name, c.rel(dir), bin)
	}
	return bin, nil
}

// staticcheckGate runs the pinned staticcheck with its default checks in every module,
// twice like vet: with "-tags=" and with every LayerTags tag and BenchTag
// (layerTags; M1-G-TOOL-1). Any finding fails it, and the output names each
// finding's check (for example SA4000).
// A finding is accepted only by a reasoned //lint:ignore at its site, counted with
// the own analyzers' by the vet gate's "exceptions: N".
func (c *Config) staticcheckGate(ctx context.Context, w io.Writer) error {
	bin, err := c.toolBinary(ctx, "staticcheck")
	if err != nil {
		return err
	}
	var errs []error
	for _, mod := range c.Modules {
		for _, tags := range []string{noTags, layerTags} {
			if err := c.step(ctx, w, mod, c.env(), "staticcheck "+tags+" ./...", bin, tags, "./..."); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

// vulncheckGate runs the pinned govulncheck on every module, "govulncheck ./..."
// against the database of Config.VulnDB (M1-G-TOOL-4, M1-D-TOOL-1 (a)). A reachable
// vulnerability fails it (govulncheck exits 3), and so does a database it cannot
// read or reach (exit 1): the gate fails and is re-run, it never passes or skips
// (build-test-toolchain.md, "Failure and retry").
func (c *Config) vulncheckGate(ctx context.Context, w io.Writer) error {
	bin, err := c.toolBinary(ctx, "govulncheck")
	if err != nil {
		return err
	}
	var args []string
	if c.VulnDB != "" {
		args = append(args, "-db", c.VulnDB)
	}
	args = append(args, "./...")
	var errs []error
	for _, mod := range c.Modules {
		if err := c.step(ctx, w, mod, c.env(), "govulncheck "+strings.Join(args, " "), bin, args...); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// fileURL is the file:// URL of the absolute path p, as govulncheck -db accepts it on
// every host ("file:///C:/db" on Windows, "file:///db" elsewhere).
func fileURL(p string) string {
	s := filepath.ToSlash(p)
	if !strings.HasPrefix(s, "/") {
		s = "/" + s
	}
	return "file://" + s
}
