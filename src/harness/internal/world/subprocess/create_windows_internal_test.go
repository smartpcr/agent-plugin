//go:build windows

package subprocess

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"testing"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// pauseHostEnv makes the re-executed test binary run TestNoProcessLeftWhenTheHostDiesMidStart
// as the host: it starts a child with a pause injected after creation.
const pauseHostEnv = "SUBPROCESS_PAUSE_HOST"

var trustedFixture = sandbox.Trusted("subprocess internal test: the test binary re-executed")

// stubbornSpec re-executes this test binary as subprocesstest's RoleStubborn (by
// its environment name: this package cannot import subprocesstest).
func stubbornSpec(t *testing.T) Spec {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return Spec{Argv: []string{exe}, Env: Env{"SUBPROCESSTEST_ROLE=stubborn"}, Launch: trustedFixture}
}

// The plan's scenario "A host killed between create and resume leaves nothing":
// a host whose start is held after the child was created, before its thread runs,
// is killed there; the child, created inside the host's job, dies with the job's
// last handle. The control runs the suspend-assign-resume fallback the same way:
// there the child is still outside the host's job at that point and is left
// behind, suspended, until this test's own job ends it.
//
//dsh:test DH-T-NODEPROC-8
func TestNoProcessLeftWhenTheHostDiesMidStart(t *testing.T) {
	if mode := os.Getenv(pauseHostEnv); mode != "" {
		pausedHost(t, mode)
		return
	}
	if left := killHostMidStart(t, "in-job"); left {
		t.Fatal("the child created inside the job outlived its host killed mid-start")
	}
	if left := killHostMidStart(t, "fallback"); !left {
		t.Log("control: the fallback's child did not outlive its host (the window was not hit)")
	} else {
		t.Log("control: the fallback's child outlived its host killed mid-start, as documented")
	}
}

// killHostMidStart starts a host in mode, kills it alone while its child is
// paused after creation, and reports whether the child was still there 5s later.
// The host's tree, the control's child included, is this test's job's to end.
func killHostMidStart(t *testing.T, mode string) (left bool) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := Env{pauseHostEnv + "=" + mode}
	if dir := os.Getenv("GOCOVERDIR"); dir != "" {
		env = append(env, "GOCOVERDIR="+dir)
	}
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pr.Close() }) // ends the drain goroutine below
	host, err := Start(context.Background(), Spec{
		Argv: []string{exe, "-test.run=^TestNoProcessLeftWhenTheHostDiesMidStart$", "-test.count=1"},
		Env:  env, Stdout: pw, Launch: trustedFixture,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = pr.Close()
		if err := host.StopTree(context.Background(), 0); err != nil {
			t.Errorf("%s: stop the host's tree: %v", mode, err)
		}
	}()
	child := -1
	sc := bufio.NewScanner(pr)
	for sc.Scan() {
		if _, err := fmt.Sscanf(sc.Text(), "created %d", &child); err == nil {
			break
		}
	}
	if child <= 0 {
		t.Fatalf("%s: the host never reported the paused child (scan: %v)", mode, sc.Err())
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	if !alive(child) {
		t.Fatalf("%s: paused child %d does not exist", mode, child)
	}
	// Kill the host alone: its tree stays owned by this test's job, so only the
	// host's own job handle closing can end the child.
	h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(host.Pid()))
	if err != nil {
		t.Fatal(err)
	}
	err = windows.TerminateProcess(h, 1)
	windows.CloseHandle(h)
	if err != nil {
		t.Fatal(err)
	}
	_ = host.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return procwait.Poll(ctx, func() bool { return !alive(child) }) != nil
}

// pausedHost is the re-executed host: it reports the created child and holds the
// start there until it is killed.
func pausedHost(t *testing.T, mode string) {
	forceSuspended = mode == "fallback"
	afterCreate = func(pid int) {
		fmt.Printf("created %d\n", pid)
		select {}
	}
	_, err := Start(context.Background(), stubbornSpec(t))
	t.Fatalf("Start returned (%v) past the injected pause", err)
}

func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	return windows.GetExitCodeProcess(h, &code) == nil && code == 259 // STILL_ACTIVE
}

// A child the job owner starts is created inside the job by createInJob, not by
// the fallback; the fallback still starts a child in the job when it is used.
func TestTheChildIsCreatedInTheJob(t *testing.T) {
	owner, err := newOwner()
	if err != nil {
		t.Fatal(err)
	}
	o := owner.(*jobOwner)
	defer o.release()
	spec := stubbornSpec(t)
	build := func() *exec.Cmd {
		return &exec.Cmd{Path: spec.Argv[0], Args: spec.Argv, Env: spec.Env.block(), Stdout: io.Discard}
	}
	for name, start := range map[string]func(func() *exec.Cmd) (*exec.Cmd, error){
		"in-job creation":  o.start,
		"suspend fallback": o.startSuspended,
	} {
		o.streams = nil
		cmd, err := start(build)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if n, err := o.active(); err != nil || n != 1 {
			t.Errorf("%s: the job holds %d processes (%v); want the child", name, n, err)
		}
		if (o.streams != nil) != (name == "in-job creation") {
			t.Errorf("%s: in-job streams %v", name, o.streams != nil)
		}
		_ = o.kill()
		_ = cmd.Wait()
		_ = o.drain()
	}
}

// The environment block and the command line are what os/exec would build.
func TestCreateInJobBlocks(t *testing.T) {
	if got, err := envBlock(nil); len(got) != 2 || err != nil {
		t.Errorf("empty block = %v, %v", got, err)
	}
	b, err := envBlock([]string{"A=1", "B=é"})
	if got := string(utf16.Decode(b)); err != nil || got != "A=1\x00B=é\x00\x00" {
		t.Errorf("block = %q, %v", got, err)
	}
	if _, err := envBlock([]string{"A=\x00"}); err == nil {
		t.Error("a NUL in the environment was accepted")
	}
	if got := composeLine([]string{`C:\a b\x.exe`, "", `q"`}); got != `"C:\a b\x.exe" "" q\"` {
		t.Errorf("composeLine = %s", got)
	}
}
