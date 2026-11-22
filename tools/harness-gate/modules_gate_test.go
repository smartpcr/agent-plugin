package main

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles writes files (slash-separated paths relative to root) under root.
func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, src := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// copyTree copies the directory src into dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
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
		t.Fatalf("copy %s: %v", src, err)
	}
}

// goSumLines returns the lines of this module's go.sum that name module.
func goSumLines(t *testing.T, module string) string {
	t.Helper()
	data, err := os.ReadFile("go.sum")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if strings.HasPrefix(line, module+" ") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "")
}

// TestModulesGatePassesOnRepository runs the modules gate on the checkout: every
// product, test and tools module is tidy and verified, the module rules hold and every
// pin has a reason (M1-G-TOOL-2, M1-G-TOOL-10; DH-T-TOOLCHAIN-11).
func TestModulesGatePassesOnRepository(t *testing.T) {
	cfg := newTestConfig(t, filepath.Join("..", ".."))
	var out bytes.Buffer
	if err := cfg.modulesGate(context.Background(), &out); err != nil {
		t.Fatalf("modules gate: %v\n%s", err, out.String())
	}
	for _, want := range []string{
		"ok   src/go/test/e2e: go mod tidy -diff", "ok   tools/harness-gate: go mod verify",
		"modules: 3 product, 1 test and 2 tools modules checked",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

// TestModulesGateNamesRM4 is the "broken module rule fails the main gate" scenario of
// plan stage 13.4, first half: a scratch copy of the product modules whose
// src/harness/go.mod gains a replace (reasoned and version-for-version, so tidy and
// verify still pass) fails the modules gate naming RM-4 (M1-G-TOOL-2;
// DH-T-TOOLCHAIN-11).
func TestModulesGateNamesRM4(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join("..", "..")
	for _, dir := range []string{"src/go", "src/sdk", "src/harness"} {
		copyTree(t, filepath.Join(repo, filepath.FromSlash(dir)), filepath.Join(root, filepath.FromSlash(dir)))
	}
	work, err := os.ReadFile(filepath.Join(repo, "go.work"))
	if err != nil {
		t.Fatal(err)
	}
	writeFiles(t, root, map[string]string{"go.work": string(work)})

	cfg := newTestConfig(t, root)
	var out bytes.Buffer
	if err := cfg.modulesGate(context.Background(), &out); err != nil {
		t.Fatalf("unchanged copy: modules gate = %v\n%s", err, out.String())
	}

	harnessMod := filepath.Join(root, "src", "harness", "go.mod")
	data, err := os.ReadFile(harnessMod)
	if err != nil {
		t.Fatal(err)
	}
	pin := ""
	for _, line := range strings.Split(string(data), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[0] == "github.com/smartpcr/agent-plugin/src/go" {
			pin = f[1]
		}
	}
	if pin == "" {
		t.Fatalf("src/harness/go.mod has no src/go pin:\n%s", data)
	}
	replace := "replace github.com/smartpcr/agent-plugin/src/go => github.com/smartpcr/agent-plugin/src/go " + pin
	data = append(data, []byte("\n// Seeded: a reasoned replace is still not allowed.\n"+replace+"\n")...)
	if err := os.WriteFile(harnessMod, data, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	err = cfg.modulesGate(context.Background(), &out)
	if err == nil || !strings.Contains(err.Error(), replace+": replace directive is not allowed (RM-4)") {
		t.Fatalf("with a replace: modules gate = %v, want RM-4 named\n%s", err, out.String())
	}
	if strings.Contains(out.String(), "FAIL ") {
		t.Errorf("a go command step failed too; only the RM-4 rule should:\n%s", out.String())
	}
}

// TestModulesGateNamesUnreasonedPin: a tidy, verified module whose direct require has
// no reason comment fails the modules gate, naming the file and line (M1-G-TOOL-2;
// DH-T-TOOLCHAIN-11).
func TestModulesGateNamesUnreasonedPin(t *testing.T) {
	root := t.TempDir()
	mod := "module example.com/unreasoned\n\ngo 1.27.4\n\nrequire golang.org/x/mod v0.43.0\n"
	writeFiles(t, root, map[string]string{
		"go.mod": mod,
		"go.sum": goSumLines(t, "golang.org/x/mod"),
		"p.go":   "// Package p uses x/mod.\npackage p\n\nimport \"golang.org/x/mod/semver\"\n\n// Valid reports whether v is a semantic version.\nfunc Valid(v string) bool { return semver.IsValid(v) }\n",
	})
	cfg := newTestConfig(t, root)
	var out bytes.Buffer
	err := cfg.modulesGate(context.Background(), &out)
	if err == nil || !strings.Contains(err.Error(), "go.mod:5: require golang.org/x/mod v0.43.0: no reason comment") {
		t.Fatalf("modules gate = %v, want the unreasoned require named\n%s", err, out.String())
	}

	writeFiles(t, root, map[string]string{"go.mod": strings.Replace(mod, "v0.43.0\n", "v0.43.0 // semantic versions for p\n", 1)})
	out.Reset()
	if err := cfg.modulesGate(context.Background(), &out); err != nil {
		t.Errorf("with a reason: modules gate = %v\n%s", err, out.String())
	}
}

// selfRoot writes a scratch root whose tools/harness-gate module has the given test
// file, and returns a configuration for it.
func selfRoot(t *testing.T, test string) *Config {
	t.Helper()
	cfg := newTestConfig(t, scratch(t, "race-seeded"))
	cfg.Root = t.TempDir()
	writeFiles(t, cfg.Root, map[string]string{
		SelfModule + "/go.mod":    "module example.com/selfseed\n\ngo 1.27.4\n",
		SelfModule + "/s.go":      "// Package s is seeded for the self gate.\npackage s\n",
		SelfModule + "/s_test.go": test,
	})
	return cfg
}

// TestSelfGateRunsTheGateTests is the "broken module rule fails the main gate"
// scenario of plan stage 13.4, second half: the self gate passes on gate tests that
// pass, never runs TestRepositoryPassesEveryGate (it would recurse), and fails naming
// a gate test that fails (M1-G-TOOL-3; DH-T-TOOLCHAIN-9).
func TestSelfGateRunsTheGateTests(t *testing.T) {
	const head = "package s\n\nimport \"testing\"\n\nfunc TestRepositoryPassesEveryGate(t *testing.T) { t.Fatal(\"recursed\") }\n\nfunc TestRule(t *testing.T) {}\n"
	cfg := selfRoot(t, head)
	var out bytes.Buffer
	if err := cfg.selfGate(context.Background(), &out); err != nil {
		t.Fatalf("passing gate tests: self gate = %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "-skip "+selfSkip) {
		t.Errorf("self gate does not skip %s:\n%s", selfSkip, out.String())
	}

	cfg = selfRoot(t, head+"\nfunc TestBrokenRule(t *testing.T) { t.Fatal(\"seeded failure\") }\n")
	out.Reset()
	err := cfg.selfGate(context.Background(), &out)
	if err == nil || !strings.Contains(err.Error(), "failing tests: TestBrokenRule") {
		t.Errorf("failing gate test: self gate = %v, want TestBrokenRule named\n%s", err, out.String())
	}
}

// TestSelfGateFailsWithoutModule: a root without tools/harness-gate fails the self
// gate; the check cannot be skipped (M1-G-TOOL-3; DH-T-TOOLCHAIN-9).
func TestSelfGateFailsWithoutModule(t *testing.T) {
	cfg := newTestConfig(t, scratch(t, "race-seeded"))
	err := cfg.selfGate(context.Background(), &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), SelfModule+" not found") {
		t.Errorf("self gate = %v, want a failure naming the missing module", err)
	}
}

// TestToolPinsResolveOffline is the "tools resolve from the tools module" scenario of
// plan stage 13.4: with the module cache warm and GOPROXY=off, the pinned linters
// build from this module on the go floor, and staticcheck reports its pinned version
// (M1-G-TOOL-5; DH-T-TOOLCHAIN-11). govulncheck is only built: its -version reads
// the vulnerability database over the network.
func TestToolPinsResolveOffline(t *testing.T) {
	cfg := newTestConfig(t, filepath.Join("..", ".."))
	env := cfg.env("GOPROXY=off", "GOFLAGS=-mod=readonly")
	out, err := cfg.command(context.Background(), ".", env, cfg.Go, "tool", "staticcheck", "-version")
	if err != nil || !strings.Contains(out, "(0.8.2)") {
		t.Errorf("go tool staticcheck -version = %v, want v0.8.2:\n%s", err, out)
	}
	bin := filepath.Join(t.TempDir(), "govulncheck")
	out, err = cfg.command(context.Background(), ".", env, cfg.Go, "build", "-o", bin, "golang.org/x/vuln/cmd/govulncheck")
	if err != nil {
		t.Errorf("go build govulncheck offline: %v\n%s", err, out)
	}
	out, err = cfg.command(context.Background(), ".", env, cfg.Go, "list", "-m", "golang.org/x/vuln")
	if err != nil || strings.TrimSpace(out) != "golang.org/x/vuln v1.10.0" {
		t.Errorf("go list -m golang.org/x/vuln = %v, %q; want v1.10.0", err, out)
	}
}
