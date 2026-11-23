//go:build linux

package subprocess_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// scopeEnv marks the run of this test binary inside a delegated systemd user
// scope. It has no DSH_ prefix because BuildEnv removes every DSH_* name.
const scopeEnv = "SUBPROCESS_TEST_IN_SCOPE"

// The tests that need a writable control group. Each skips outside the scope.
var inScopeTests = []string{
	"TestInScopeOwnerIsAControlGroup",
	"TestInScopeStopTreeStopsTheTreeAndRemovesItsGroup",
	"TestInScopeSurvivorsNameTheGroup",
	"TestInScopeStartFailureLeavesNoGroup",
}

// A host whose own control group is writable puts each tree in a control group
// of its own (DH-D-EXEC-2). A login shell's group is not writable, so this test
// re-runs the in-scope tests in this binary inside a systemd user scope with
// delegation, where the process's own group belongs to the user; the coverage
// directory is passed on so their counters are merged (QG-3). Without a user
// manager there is no delegated scope to run in, and the test skips saying so.
//
//dsh:test DH-T-EXEC-4
func TestControlGroupOwnerInADelegatedScope(t *testing.T) {
	if os.Getenv(scopeEnv) != "" {
		t.Skip("inside the scope: the in-scope tests run directly")
	}
	host := subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{Entries: []string{scopeEnv + "=1"}})
	run, err := subprocess.Lookup("systemd-run", host)
	if err != nil {
		t.Skipf("no systemd-run on PATH, so no delegated scope: %v", err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	argv := []string{run, "--user", "--scope", "--quiet", "-p", "Delegate=yes", "--", exe,
		"-test.run=^(" + strings.Join(inScopeTests, "|") + ")$", "-test.count=1", "-test.v"}
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		argv = append(argv, "-test.gocoverdir="+dir)
	}
	var out strings.Builder
	p := subprocesstest.Start(t, subprocess.Spec{Argv: argv, Env: host, Stdout: &out, Stderr: &out})
	werr := p.Wait()
	log := out.String()
	if werr != nil && !strings.Contains(log, "=== RUN") {
		t.Skipf("systemd-run could not open a delegated user scope (%v): %s", werr, log)
	}
	if werr != nil {
		t.Fatalf("the in-scope tests failed: %v\n%s", werr, log)
	}
	for _, name := range inScopeTests {
		if !strings.Contains(log, "--- PASS: "+name) {
			t.Errorf("%s did not pass inside the scope:\n%s", name, log)
		}
	}
}

// inScope skips a test outside the delegated scope.
func inScope(t *testing.T) {
	t.Helper()
	if os.Getenv(scopeEnv) == "" {
		t.Skip("runs inside a delegated scope, from TestControlGroupOwnerInADelegatedScope")
	}
}

// ownGroup is this process's control group directory.
func ownGroup(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(b), "\n") {
		if rel, ok := strings.CutPrefix(line, "0::"); ok {
			return filepath.Join("/sys/fs/cgroup", filepath.Clean("/"+rel))
		}
	}
	t.Fatalf("no cgroup v2 entry in %q", b)
	return ""
}

// treeGroups lists the per-tree control groups this process made and has not
// removed.
func treeGroups(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(ownGroup(t))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	prefix := "dsh-" + strconv.Itoa(os.Getpid()) + "-"
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			out = append(out, e.Name())
		}
	}
	return out
}

// With its own group writable, the host reports an exact control-group owner,
// with no fallback.
//
//dsh:test DH-T-EXEC-4
func TestInScopeOwnerIsAControlGroup(t *testing.T) {
	inScope(t)
	o := subprocess.TreeOwner()
	if o.Kind != subprocess.ControlGroup || !o.Exact || o.Fallback != "" || o.String() != "control-group" {
		t.Fatalf("TreeOwner = %+v (%s); want an exact control group with no fallback", o, o)
	}
}

// TC-06c on the control-group rung: StopTree asks the tree to stop, then kills
// the group, so the stubborn grandchild goes too; the tree's group is removed
// once it is empty.
//
//dsh:test DH-T-NODEPROC-5
func TestInScopeStopTreeStopsTheTreeAndRemovesItsGroup(t *testing.T) {
	inScope(t)
	p, child, grandchild := subprocesstest.Tree(t)
	if got := p.Owner(); got.Kind != subprocess.ControlGroup || !got.Exact {
		t.Fatalf("Owner = %+v; want an exact control group", got)
	}
	if len(treeGroups(t)) != 1 {
		t.Fatalf("tree groups while the tree runs = %v; want one", treeGroups(t))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := p.StopTree(ctx, 200*time.Millisecond); err != nil {
		t.Fatalf("StopTree: %v", err)
	}
	if err := p.TreeGone(ctx); err != nil {
		t.Fatalf("TreeGone after StopTree: %v", err)
	}
	if g := treeGroups(t); len(g) != 0 {
		t.Fatalf("tree groups after StopTree = %v; want none", g)
	}
	// The child is reaped by StopTree's wait; the grandchild, orphaned by the
	// kill, by the scope's reaper on its own time, so it is checked as exited.
	if !subprocesstest.Exited(child) || !subprocesstest.Exited(grandchild) {
		t.Fatalf("after StopTree: child exited %v, grandchild exited %v", subprocesstest.Exited(child), subprocesstest.Exited(grandchild))
	}
}

// A tree still running when TreeGone's context ends is reported with the group
// and the pids it still holds; Kill writes cgroup.kill, after which the tree is
// gone.
//
//dsh:test DH-T-EXEC-5
func TestInScopeSurvivorsNameTheGroup(t *testing.T) {
	inScope(t)
	p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleStubborn))
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	err := p.TreeGone(ended)
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if herr.Kind(err) != subprocess.ReasonSurvivors || !strings.Contains(msg, "control group dsh-") ||
		!strings.Contains(msg, "still holds ["+strconv.Itoa(p.Pid())+"]") {
		t.Fatalf("TreeGone with an ended context = %v; want survivors naming the group and pid %d", err, p.Pid())
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	ctx, cancel2 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel2()
	if err := p.TreeGone(ctx); err != nil {
		t.Fatalf("TreeGone after Kill: %v", err)
	}
	if err := p.Wait(); err == nil {
		t.Fatal("Wait after Kill: no error for a killed child")
	}
	if g := treeGroups(t); len(g) != 0 {
		t.Fatalf("tree groups after the tree left = %v; want none", g)
	}
}

// A program that cannot start is the caller's failure, not the control group's:
// it is reported as a start failure, no process-group fallback is tried, and its
// group is removed.
//
//dsh:test DH-T-EXEC-4
func TestInScopeStartFailureLeavesNoGroup(t *testing.T) {
	inScope(t)
	dir := t.TempDir()
	plain := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(plain, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for name, spec := range map[string]subprocess.Spec{
		"missing program":    {Argv: []string{filepath.Join(dir, "missing")}},
		"not executable":     {Argv: []string{plain}},
		"missing work dir":   {Argv: []string{exe}, Dir: filepath.Join(dir, "no-such-dir")},
		"directory as a dir": {Argv: []string{dir}},
	} {
		if _, err := subprocess.Start(context.Background(), spec); herr.Kind(err) != subprocess.ReasonStartFailed {
			t.Errorf("%s: Start = %v; want %s", name, err, subprocess.ReasonStartFailed)
		}
	}
	if g := treeGroups(t); len(g) != 0 {
		t.Fatalf("tree groups after failed starts = %v; want none", g)
	}
}
