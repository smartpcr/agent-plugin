package subprocesstest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

func TestMain(m *testing.M) { Main(m) }

// The roles that answer and exit do what their documentation says, run in this
// process; an unknown role fails with code 2, and RoleExit without an argument
// exits 0.
func TestRolesThatExit(t *testing.T) {
	var b strings.Builder
	if code := run(RoleArgs, []string{"a b", "", `"q"`}, &b); code != 0 {
		t.Fatalf("args role exited %d", code)
	}
	var got []string
	if err := json.Unmarshal([]byte(b.String()), &got); err != nil || len(got) != 3 || got[0] != "a b" || got[1] != "" || got[2] != `"q"` {
		t.Fatalf("args role printed %q (%v)", b.String(), err)
	}

	t.Setenv("SUBPROCESSTEST_PROBE_SET", "x")
	b.Reset()
	if code := run(RoleEnvProbe, []string{"SUBPROCESSTEST_PROBE_SET", "SUBPROCESSTEST_PROBE_UNSET"}, &b); code != 0 ||
		b.String() != "SUBPROCESSTEST_PROBE_SET=present\nSUBPROCESSTEST_PROBE_UNSET=absent\n" {
		t.Fatalf("env role = %d, %q", code, b.String())
	}

	for _, c := range []struct {
		args []string
		want int
	}{{nil, 0}, {[]string{"5"}, 5}} {
		if code := run(RoleExit, c.args, &b); code != c.want {
			t.Errorf("exit role with %q exited %d; want %d", c.args, code, c.want)
		}
	}
	b.Reset()
	if code := run("no-such-role", nil, &b); code != 2 || b.Len() != 0 {
		t.Fatalf("unknown role = %d, printed %q; want 2 and nothing on stdout", code, b.String())
	}
}

// A child's environment is the role and, when the test process has one, the
// coverage directory; no other host variable reaches it (SP-11).
func TestSpecForwardsOnlyTheCoverageDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(CoverDirEnv, dir)
	t.Setenv("SUBPROCESSTEST_HOST_TOKEN", "dummy")
	spec := Spec(t, RoleArgs, "x")
	want := subprocess.Env{RoleEnv + "=" + RoleArgs, CoverDirEnv + "=" + dir}
	if strings.Join(spec.Env, "\n") != strings.Join(want, "\n") {
		t.Fatalf("Env = %q; want %q", spec.Env, want)
	}
	if len(spec.Argv) != 2 || !filepath.IsAbs(spec.Argv[0]) || spec.Argv[1] != "x" {
		t.Fatalf("Argv = %q; want this binary and x", spec.Argv)
	}

	t.Setenv(CoverDirEnv, "")
	if got := Spec(t, RoleArgs).Env; len(got) != 1 || got[0] != RoleEnv+"="+RoleArgs {
		t.Fatalf("Env without a coverage directory = %q; want the role alone", got)
	}
}

// A child really receives the forwarded directory and nothing else of the host.
func TestChildSeesTheCoverageDirectoryAndNoHostVariable(t *testing.T) {
	if os.Getenv(CoverDirEnv) == "" {
		t.Setenv(CoverDirEnv, t.TempDir())
	}
	t.Setenv("SUBPROCESSTEST_HOST_TOKEN", "dummy")
	out := Output(t, Spec(t, RoleEnvProbe, CoverDirEnv, "SUBPROCESSTEST_HOST_TOKEN", RoleEnv))
	want := CoverDirEnv + "=present\nSUBPROCESSTEST_HOST_TOKEN=absent\n" + RoleEnv + "=present\n"
	if got := strings.ReplaceAll(out, "\r\n", "\n"); got != want {
		t.Fatalf("child reported %q; want %q", got, want)
	}
}

// fatalTB records what a helper reports and ends the helper's goroutine on
// Fatalf, as testing does; its cleanups run when the test asks.
type fatalTB struct {
	testing.TB
	mu       sync.Mutex
	fatals   []string
	errors   []string
	cleanups []func()
}

func (f *fatalTB) Helper() {}

func (f *fatalTB) Logf(string, ...any) {}

func (f *fatalTB) Errorf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errors = append(f.errors, fmt.Sprintf(format, args...))
}

func (f *fatalTB) Fatalf(format string, args ...any) {
	f.mu.Lock()
	f.fatals = append(f.fatals, fmt.Sprintf(format, args...))
	f.mu.Unlock()
	runtime.Goexit()
}

func (f *fatalTB) Cleanup(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cleanups = append(f.cleanups, fn)
}

// call runs fn against a fatalTB on its own goroutine, joins it, runs the
// cleanups fn registered, last first, and returns what was reported.
func call(t *testing.T, fn func(tb testing.TB)) (fatals, errs []string) {
	t.Helper()
	f := &fatalTB{TB: t}
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		fn(f)
	}()
	<-done
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	return f.fatals, f.errors
}

// Start fails its test when the program cannot start, and Output fails its test
// when the child exits with an error; the tree started is still stopped.
func TestHelpersFailTheirTest(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-program")
	fatals, errs := call(t, func(tb testing.TB) { Start(tb, subprocess.Spec{Argv: []string{missing}, Launch: Launch}) })
	if len(fatals) != 1 || !strings.Contains(fatals[0], "subprocesstest: start:") || len(errs) != 0 {
		t.Fatalf("Start of a missing program reported fatals %q, errors %q; want one start failure", fatals, errs)
	}

	fatals, errs = call(t, func(tb testing.TB) { Output(tb, Spec(t, RoleExit, "3")) })
	if len(fatals) != 1 || !strings.Contains(fatals[0], "subprocesstest: child failed:") || len(errs) != 0 {
		t.Fatalf("Output of a failing child reported fatals %q, errors %q; want one child failure and a clean stop", fatals, errs)
	}
}

// The roles that park report ready only once the tree is up: Tree returns the
// child and the grandchild, both running, and its cleanup stops both.
func TestTreeReportsBothPids(t *testing.T) {
	var child, grandchild int
	var running bool
	fatals, errs := call(t, func(tb testing.TB) {
		_, child, grandchild = Tree(tb)
		running = Alive(child) && Alive(grandchild)
	})
	if len(fatals) != 0 || len(errs) != 0 {
		t.Fatalf("Tree reported fatals %q, cleanup errors %q", fatals, errs)
	}
	if child <= 0 || grandchild <= 0 || child == grandchild || !running {
		t.Fatalf("Tree pids %d, %d (both running %v); want two distinct running processes", child, grandchild, running)
	}
	if !Exited(child) || !Exited(grandchild) {
		t.Fatalf("after the cleanup: child exited %v, grandchild exited %v", Exited(child), Exited(grandchild))
	}
}

// noPark makes the parking roles return instead of waiting to be killed, so they
// run to their end in this process (a killed child writes no coverage data), and
// undoes what RoleStubborn does to this process's signal handling.
func noPark(t *testing.T) {
	t.Helper()
	saved := park
	parked := 0
	park = func() { parked++ }
	t.Cleanup(func() {
		park = saved
		signal.Reset(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	})
	t.Cleanup(func() {
		if parked == 0 {
			t.Error("no role parked")
		}
	})
}

// RoleStubborn reports its own pid once it is ready, then parks.
func TestStubbornReportsReadyAndParks(t *testing.T) {
	noPark(t)
	var b strings.Builder
	if code := run(RoleStubborn, nil, &b); code != 0 || b.String() != fmt.Sprintf("ready %d\n", os.Getpid()) {
		t.Fatalf("stubborn role = %d, %q; want 0 and ready with this pid", code, b.String())
	}
}

// RoleTree waits for its stubborn grandchild to be ready before it reports both
// pids, and then parks; the grandchild runs until it is killed.
func TestTreeRoleWaitsForTheGrandchild(t *testing.T) {
	noPark(t)
	var b strings.Builder
	if code := run(RoleTree, nil, &b); code != 0 {
		t.Fatalf("tree role exited %d", code)
	}
	var self, grandchild int
	if _, err := fmt.Sscanf(b.String(), "tree %d %d\n", &self, &grandchild); err != nil || self != os.Getpid() {
		t.Fatalf("tree role printed %q (%v); want this pid and the grandchild's", b.String(), err)
	}
	g, err := os.FindProcess(grandchild)
	if err != nil {
		t.Fatal(err)
	}
	// The tree ran in this process, so this process is the grandchild's parent. On
	// Unix a killed child stays a zombie, which kill(pid, 0) still finds, until its
	// parent reaps it, so the fact to wait for is the reaping: Wait returns once
	// the grandchild has exited and been reaped. The wait is bounded, and the
	// cleanup kills before it joins, so the waiter always ends.
	waited := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, err := g.Wait()
		waited <- err
	}()
	t.Cleanup(func() {
		_ = g.Kill()
		<-done
	})
	if !Alive(grandchild) {
		t.Fatalf("grandchild %d is not running after the tree reported it", grandchild)
	}
	if err := g.Kill(); err != nil {
		t.Fatalf("kill grandchild %d: %v", grandchild, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	select {
	case err := <-waited:
		if err != nil {
			t.Fatalf("wait for grandchild %d: %v", grandchild, err)
		}
	case <-ctx.Done():
		t.Fatalf("grandchild %d was not reaped within 15s of its kill", grandchild)
	}
	if Alive(grandchild) {
		t.Fatalf("grandchild %d is still found after it was reaped", grandchild)
	}
}

// A child that fails writes its reason to standard error, which Output logs
// through the test before failing it.
func TestOutputLogsTheChildsStandardError(t *testing.T) {
	f := &fatalTB{TB: t}
	var logged []string
	lt := &logTB{fatalTB: f, logs: &logged}
	done := make(chan struct{})
	t.Cleanup(func() { <-done })
	go func() {
		defer close(done)
		Output(lt, Spec(t, "no-such-role"))
	}()
	<-done
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
	if len(f.fatals) != 1 || !strings.Contains(f.fatals[0], "child failed") {
		t.Fatalf("fatals %q; want one child failure", f.fatals)
	}
	if !strings.Contains(strings.Join(logged, "\n"), `child stderr: subprocesstest: unknown role "no-such-role"`) {
		t.Fatalf("logged %q; want the child's reason", logged)
	}
}

// logTB is a fatalTB that keeps what is logged.
type logTB struct {
	*fatalTB
	logs *[]string
}

func (l *logTB) Logf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	*l.logs = append(*l.logs, fmt.Sprintf(format, args...))
}

// RoleEnviron prints the whole environment, one entry per line, in order.
func TestEnvironRolePrintsTheWholeEnvironment(t *testing.T) {
	t.Setenv("SUBPROCESSTEST_ENVIRON_PROBE", "seen")
	var b strings.Builder
	if code := run(RoleEnviron, nil, &b); code != 0 {
		t.Fatalf("environ role exited %d", code)
	}
	lines := strings.Split(strings.TrimSuffix(b.String(), "\n"), "\n")
	if !slices.IsSorted(lines) || !slices.Contains(lines, "SUBPROCESSTEST_ENVIRON_PROBE=seen") || len(lines) != len(os.Environ()) {
		t.Fatalf("environ role printed %q; want every entry of this process, sorted", lines)
	}
}

// RoleHost starts its child through the spawn package, reports it with its
// owner once it is ready, then parks; the child runs until it is killed.
func TestHostRoleReportsItsChild(t *testing.T) {
	noPark(t)
	var b strings.Builder
	if code := run(RoleHost, nil, &b); code != 0 {
		t.Fatalf("host role exited %d", code)
	}
	var child int
	var kind string
	if _, err := fmt.Sscanf(b.String(), "host %d %s\n", &child, &kind); err != nil || child <= 0 || kind != string(subprocess.TreeOwner().Kind) {
		t.Fatalf("host role printed %q (%v); want the child's pid and this host's owner %s", b.String(), err, subprocess.TreeOwner().Kind)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t.Cleanup(func() { killPid(child) })
	if !Alive(child) {
		t.Fatalf("child %d is not running after the host reported it", child)
	}
	killPid(child)
	// The spawn package's own waiter reaps the child, so it is gone soon after.
	procwait.Until(ctx, t, func() bool { return !Alive(child) })
}

func killPid(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}

// RoleWrite writes the decoded bytes to the stream it names and fails on a bad
// stream, bad hex or a missing argument.
func TestWriteRoleWritesTheBytes(t *testing.T) {
	var out, errOut strings.Builder
	saved := stderr
	stderr = &errOut
	t.Cleanup(func() { stderr = saved })
	if code := run(RoleWrite, []string{"stdout", "00ff0a"}, &out); code != 0 || out.String() != "\x00\xff\n" {
		t.Fatalf("write to stdout = %d, %q", code, out.String())
	}
	if code := run(RoleWrite, []string{"stderr", "6869"}, &out); code != 0 || errOut.String() != "hi" {
		t.Fatalf("write to stderr = %d, %q", code, errOut.String())
	}
	for _, args := range [][]string{nil, {"stdout", "zz"}, {"stdin", "00"}} {
		if code := run(RoleWrite, args, &out); code != 2 {
			t.Errorf("write %q exited %d; want 2", args, code)
		}
	}
}

// RoleFlood reports both pids on standard error once the grandchild is ready,
// then writes the bytes and parks; a bad count fails.
func TestFloodRoleReportsThenWrites(t *testing.T) {
	noPark(t)
	var out, errOut strings.Builder
	saved := stderr
	stderr = &errOut
	t.Cleanup(func() { stderr = saved })
	if code := run(RoleFlood, []string{"5"}, &out); code != 0 || out.String() != "xxxxx" {
		t.Fatalf("flood role = %d, %q; want 0 and five bytes", code, out.String())
	}
	var self, grandchild int
	if _, err := fmt.Sscanf(errOut.String(), "tree %d %d\n", &self, &grandchild); err != nil || self != os.Getpid() {
		t.Fatalf("flood role said %q on stderr (%v); want this pid and the grandchild's", errOut.String(), err)
	}
	g, err := os.FindProcess(grandchild)
	if err != nil {
		t.Fatal(err)
	}
	_ = g.Kill()
	_, _ = g.Wait()
	if code := run(RoleFlood, []string{"-1"}, &out); code != 2 {
		t.Fatalf("flood with a negative count exited %d; want 2", code)
	}
}

// RoleChannel fails without a usable number and a text; RoleConsole says one of
// its two answers; RoleDetachHost reports a detached child and parks.
func TestChannelConsoleAndDetachHostRoles(t *testing.T) {
	var out strings.Builder
	for _, args := range [][]string{nil, {"x", "text"}} {
		if code := run(RoleChannel, args, &out); code != 2 {
			t.Errorf("channel %q exited %d; want 2", args, code)
		}
	}
	out.Reset()
	if code := run(RoleConsole, nil, &out); code != 0 || (out.String() != "window\n" && out.String() != "no-window\n") {
		t.Fatalf("console role = %d, %q", code, out.String())
	}
	noPark(t)
	out.Reset()
	if code := run(RoleDetachHost, nil, &out); code != 0 {
		t.Fatalf("detach-host role exited %d", code)
	}
	var child int
	if _, err := fmt.Sscanf(out.String(), "detached %d\n", &child); err != nil || child <= 0 {
		t.Fatalf("detach-host role printed %q (%v)", out.String(), err)
	}
	t.Cleanup(func() { killPid(child) })
	if !Alive(child) {
		t.Fatalf("detached child %d is not running", child)
	}
	killPid(child)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	procwait.Until(ctx, t, func() bool { return Exited(child) })
}
