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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// RoleEnv selects the role of a re-executed test binary. It has no DSH_ prefix
// because BuildEnv removes every DSH_* name.
const RoleEnv = "SUBPROCESSTEST_ROLE"

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
	// RoleExit exits with the code given as its first argument.
	RoleExit = "exit"
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
	case RoleStubborn:
		ignoreStop()
		fmt.Fprintf(out, "ready %d\n", os.Getpid())
		park()
	case RoleExit:
		code, _ := strconv.Atoi(firstOr(args, "0"))
		return code
	default:
		fmt.Fprintf(os.Stderr, "subprocesstest: unknown role %q\n", role)
		return 2
	}
	return 0
}

// tree starts the grandchild the way an arbitrary program would, so it inherits
// the child's process group, control group or job, with an explicit environment
// that holds only the role; then it reports both pids.
func tree(out io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	g, err := os.StartProcess(exe, []string{exe}, &os.ProcAttr{
		Env:   []string{RoleEnv + "=" + RoleStubborn},
		Files: []*os.File{nil, pw, nil},
	})
	_ = pw.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "ready ") {
		fmt.Fprintf(os.Stderr, "subprocesstest: grandchild said %q: %v\n", line, err)
		return 2
	}
	fmt.Fprintf(out, "tree %d %d\n", os.Getpid(), g.Pid)
	park()
	return 0
}

// park waits until the process is killed.
func park() {
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
// environment holds only RoleEnv; callers add to Env.
func Spec(t testing.TB, role string, args ...string) subprocess.Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("subprocesstest: %v", err)
	}
	return subprocess.Spec{
		Argv: append([]string{exe}, args...),
		Env:  subprocess.Env{RoleEnv + "=" + role},
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
