// Package subprocesstest lets a test binary re-execute itself as a child, or as a
// child that starts a grandchild, so process tests need no external program
// (TC-06c, TC-07). A test package calls Main from its TestMain; Spec builds the
// argument list and environment that select a role in the re-executed binary.
// Only tests import this package (archtest).
package subprocesstest

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// RoleEnv selects the role of a re-executed test binary. It has no DSH_ prefix
// because BuildEnv removes every DSH_* name.
const RoleEnv = "SUBPROCESSTEST_ROLE"

// CoverDirEnv is the directory a binary built with -cover writes its coverage
// data to. It is the one host variable a re-executed child inherits: the gate
// merges coverage from the test process and every child (QG-3), and the name
// holds a directory, never a secret (SP-11).
const CoverDirEnv = "GOCOVERDIR"

// Roles a re-executed binary plays.
const (
	// RoleArgs prints its arguments as one JSON array line and exits.
	RoleArgs = "args"
	// RoleEnvProbe prints NAME=present or NAME=absent for each argument and exits.
	RoleEnvProbe = "env"
	// RoleTree starts a RoleStubborn grandchild, prints "tree <child pid>
	// <grandchild pid>" and waits to be stopped.
	RoleTree = "tree"
	// RoleStubborn ignores the polite stop signal, prints "ready <pid>" and waits
	// to be killed.
	RoleStubborn = "stubborn"
	// RoleOrphan starts a RoleStubborn grandchild with standard streams of its own,
	// prints "orphan <grandchild pid>" and exits, leaving the grandchild running.
	RoleOrphan = "orphan"
	// RoleExit exits with the code given as its first argument.
	RoleExit = "exit"
	// RoleEnviron prints its whole environment, one NAME=value per line, sorted,
	// and exits.
	RoleEnviron = "environ"
	// RoleHost plays a host: it starts a RoleStubborn child through
	// subprocess.Start, so the child gets this process's tree owner, prints "host
	// <child pid> <owner kind>" once the child is ready and waits to be killed.
	RoleHost = "host"
)

// Main runs the role named by RoleEnv and exits; without one it runs the tests.
func Main(m *testing.M) {
	role, ok := os.LookupEnv(RoleEnv)
	if !ok {
		os.Exit(m.Run())
	}
	os.Exit(run(role, os.Args[1:], os.Stdout))
}

func run(role string, args []string, out io.Writer) int {
	switch role {
	case RoleArgs:
		b, _ := json.Marshal(args)
		fmt.Fprintln(out, string(b))
	case RoleEnvProbe:
		for _, name := range args {
			state := "absent"
			if _, ok := os.LookupEnv(name); ok {
				state = "present"
			}
			fmt.Fprintf(out, "%s=%s\n", name, state)
		}
	case RoleTree:
		return tree(out)
	case RoleOrphan:
		return orphan(out)
	case RoleStubborn:
		ignoreStop()
		fmt.Fprintf(out, "ready %d\n", os.Getpid())
		park()
	case RoleExit:
		code, _ := strconv.Atoi(firstOr(args, "0"))
		return code
	case RoleEnviron:
		env := os.Environ()
		slices.Sort(env)
		for _, kv := range env {
			fmt.Fprintln(out, kv)
		}
	case RoleHost:
		return host(out)
	default:
		return fail(fmt.Errorf("unknown role %q", role))
	}
	return 0
}

// fail reports err on standard error and is the exit code of a role that could
// not do its part.
func fail(err error) int {
	fmt.Fprintln(os.Stderr, "subprocesstest:", err)
	return 2
}

// startStubborn starts a RoleStubborn grandchild the way an arbitrary program
// would, with stdout as its standard output and an explicit environment that
// holds only the role (and the coverage directory, childEnv).
func startStubborn(stdout *os.File) (*os.Process, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return os.StartProcess(exe, []string{exe}, &os.ProcAttr{
		Env:   childEnv(RoleStubborn),
		Files: []*os.File{nil, stdout, nil},
	})
}

// tree starts the grandchild so it inherits the child's process group, control
// group or job; once it is ready it reports both pids.
func tree(out io.Writer) int {
	pr, pw, err := os.Pipe()
	if err != nil {
		return fail(err)
	}
	defer pr.Close()
	g, err := startStubborn(pw)
	_ = pw.Close()
	if err != nil {
		return fail(err)
	}
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "ready ") {
		return fail(fmt.Errorf("grandchild said %q: %v", line, err))
	}
	fmt.Fprintf(out, "tree %d %d\n", os.Getpid(), g.Pid)
	park()
	return 0
}

// host starts a RoleStubborn child through the spawn package and reports it once
// it is ready; the child's tree is never stopped here, so a test can kill the host
// and watch what happens to the child.
func host(out io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		return fail(err)
	}
	pr, pw := io.Pipe()
	p, err := subprocess.Start(context.Background(), subprocess.Spec{Argv: []string{exe}, Env: childEnv(RoleStubborn), Stdout: pw})
	if err != nil {
		return fail(err)
	}
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "ready ") {
		return fail(fmt.Errorf("child said %q: %v", line, err))
	}
	fmt.Fprintf(out, "host %d %s\n", p.Pid(), p.Owner().Kind)
	park()
	return 0
}

// orphan starts the grandchild without sharing the child's streams, so nothing
// ties the child's exit to it, then exits.
func orphan(out io.Writer) int {
	g, err := startStubborn(nil)
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(out, "orphan %d\n", g.Pid)
	_ = g.Release()
	return 0
}

// childEnv is the whole environment of a re-executed child in role: the role,
// and CoverDirEnv when this process has it. Nothing else of the host reaches it.
func childEnv(role string) []string {
	env := []string{RoleEnv + "=" + role}
	if dir := os.Getenv(CoverDirEnv); dir != "" {
		env = append(env, CoverDirEnv+"="+dir)
	}
	return env
}

// park waits until the process is killed. A killed process writes no coverage
// data, so this package's own tests replace it to run the parking roles in
// their own process, where the counters are kept.
var park = func() {
	for {
		time.Sleep(time.Hour)
	}
}

func firstOr(args []string, def string) string {
	if len(args) == 0 {
		return def
	}
	return args[0]
}

// Spec is the spec that re-executes the test binary in role with args. Its
// environment holds only RoleEnv and, when set, CoverDirEnv; callers add to Env.
func Spec(t testing.TB, role string, args ...string) subprocess.Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("subprocesstest: %v", err)
	}
	return subprocess.Spec{
		Argv: append([]string{exe}, args...),
		Env:  subprocess.Env(childEnv(role)),
	}
}

// Start starts spec and registers a cleanup that stops the tree (QG-5).
func Start(t testing.TB, spec subprocess.Spec) *subprocess.Process {
	t.Helper()
	p, err := subprocess.Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("subprocesstest: start: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := p.StopTree(ctx, 0); err != nil {
			t.Errorf("subprocesstest: cleanup: %v", err)
		}
	})
	return p
}

// Output runs spec to completion and returns its standard output.
func Output(t testing.TB, spec subprocess.Spec) string {
	t.Helper()
	var b strings.Builder
	spec.Stdout = &b
	spec.Stderr = &prefixWriter{t: t}
	p := Start(t, spec)
	if err := p.Wait(); err != nil {
		t.Fatalf("subprocesstest: child failed: %v", err)
	}
	return b.String()
}

// Tree starts a RoleTree child and returns it with the child and grandchild pids,
// once the grandchild is ready.
func Tree(t testing.TB) (p *subprocess.Process, child, grandchild int) {
	t.Helper()
	spec := Spec(t, RoleTree)
	pr, pw := io.Pipe()
	spec.Stdout = pw
	spec.Stderr = &prefixWriter{t: t}
	p = Start(t, spec)
	t.Cleanup(func() { _ = pr.Close() })
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("subprocesstest: tree child said %q: %v", line, err)
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	if _, err := fmt.Sscanf(line, "tree %d %d", &child, &grandchild); err != nil {
		t.Fatalf("subprocesstest: tree child said %q: %v", line, err)
	}
	return p, child, grandchild
}

// prefixWriter logs a child's standard error through the test.
type prefixWriter struct{ t testing.TB }

func (w *prefixWriter) Write(b []byte) (int, error) {
	w.t.Logf("child stderr: %s", strings.TrimRight(string(b), "\n"))
	return len(b), nil
}
