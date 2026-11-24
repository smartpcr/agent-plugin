package subprocess_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// A private channel (Spec.Extra) reaches the child under the number ChildFD gives,
// apart from its standard output; without Extra the same number is not open in
// the child, since nothing else is inherited. (The private-channel row of
// gap-closure 19.3.2; DH-T-NODEPROC-9's collection, spill and long-lived piped
// child rows are not proven here.)
//
//dsh:test DH-T-NODEPROC-9
func TestChannelReachesTheChild(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	extra := []subprocess.Channel{{File: w}}
	fd := fmt.Sprint(subprocess.ChildFD(extra, 0))
	spec := subprocesstest.Spec(t, subprocesstest.RoleChannel, fd, "status: ready")
	spec.Extra = extra
	var out strings.Builder
	spec.Stdout = &out
	p := subprocesstest.Start(t, spec)
	_ = w.Close() // the child's copy is the only writer left
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Wait(); err != nil {
		t.Fatalf("channel child: %v", err)
	}
	if string(got) != "status: ready" || out.Len() != 0 {
		t.Fatalf("channel carried %q and stdout %q; want the status on the channel alone", got, out.String())
	}

	without := subprocesstest.Spec(t, subprocesstest.RoleChannel, fd, "leak")
	if res, err := subprocess.Run(context.Background(), without, subprocess.RunOptions{}); err != nil || res.ExitCode == 0 {
		t.Fatalf("a child without the channel wrote to number %s: %+v, %v; want a failed write", fd, res, err)
	}
	bad := subprocesstest.Spec(t, subprocesstest.RoleExit)
	bad.Extra = []subprocess.Channel{{}}
	if _, err := subprocess.Start(context.Background(), bad); herr.Kind(err) != subprocess.ReasonBadArgument {
		t.Fatalf("a channel without a file: Start = %v; want %s", err, subprocess.ReasonBadArgument)
	}
}

// The plan's scenario "A detached launch outlives the host and is never
// stopped": with a tree and a detached child started, the registry holds only
// the tree; the end of the start context and the registry's stop end the tree,
// and the detached child is still alive, has no tree owner, and refuses every
// stop and tree wait. The test then stops it itself.
//
//dsh:test DH-T-NODEPROC-9
func TestDetachedLaunchIsNeverStopped(t *testing.T) {
	reg := subprocess.NewRegistry()
	tree, child, grandchild := subprocesstest.Tree(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	spec := subprocesstest.Spec(t, subprocesstest.RoleStubborn)
	spec.Detach = true
	det, err := subprocess.Start(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopDetached(t, det) })
	reg.Add(tree)
	remove := reg.Add(det)
	remove()
	reg.Add(det)
	if live := reg.Live(); len(live) != 1 || live[0] != tree {
		t.Fatalf("registry holds %d processes; want the tree alone", len(live))
	}
	cancel()
	sctx, scancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer scancel()
	if err := reg.StopAll(sctx, 0); err != nil {
		t.Fatalf("StopAll: %v", err)
	}
	if !subprocesstest.Exited(child) || !subprocesstest.Exited(grandchild) {
		t.Fatalf("the tree survived the registry's stop: child exited %v, grandchild exited %v",
			subprocesstest.Exited(child), subprocesstest.Exited(grandchild))
	}
	if !subprocesstest.Alive(det.Pid()) {
		t.Fatalf("detached child %d did not outlive the host's stop", det.Pid())
	}
	if o := det.Owner(); o.Kind != subprocess.Detached || !det.Detached() {
		t.Fatalf("detached child reports owner %+v (detached %v); want %s", o, det.Detached(), subprocess.Detached)
	}
	for name, err := range map[string]error{
		"StopTree": det.StopTree(sctx, 0),
		"Kill":     det.Kill(),
		"TreeGone": det.TreeGone(sctx),
	} {
		if herr.Kind(err) != subprocess.ReasonDetached {
			t.Errorf("%s on a detached launch = %v; want %s", name, err, subprocess.ReasonDetached)
		}
	}
	if !subprocesstest.Alive(det.Pid()) {
		t.Fatalf("detached child %d stopped by a refused call", det.Pid())
	}
}

// A detached child outlives the process that started it: a host that started one
// is killed, and the child is still running.
//
//dsh:test DH-T-NODEPROC-9
func TestDetachedChildOutlivesItsHost(t *testing.T) {
	spec := subprocesstest.Spec(t, subprocesstest.RoleDetachHost)
	pr, pw := io.Pipe()
	spec.Stdout = pw
	host := subprocesstest.Start(t, spec)
	t.Cleanup(func() { _ = pr.Close() })
	line, err := bufio.NewReader(pr).ReadString('\n')
	if err != nil {
		t.Fatalf("host said %q: %v", line, err)
	}
	go func() { _, _ = io.Copy(io.Discard, pr) }()
	var pid int
	if _, err := fmt.Sscanf(line, "detached %d", &pid); err != nil {
		t.Fatalf("host said %q: %v", line, err)
	}
	t.Cleanup(func() { killPid(pid) })
	killPid(host.Pid()) // the host alone, not its tree
	if err := host.Wait(); err == nil {
		t.Fatal("the killed host exited cleanly")
	}
	// Watched for 200ms after the host is reaped: the child must not end in that window.
	watch, stop := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer stop()
	if procwait.Poll(watch, func() bool { return !subprocesstest.Alive(pid) }) == nil {
		t.Fatalf("detached child %d died with its host", pid)
	}
}

// Run waits for its program, so it refuses a detached launch; a detached launch
// refuses a stream the host would have to keep copying.
func TestDetachRefusals(t *testing.T) {
	spec := subprocesstest.Spec(t, subprocesstest.RoleExit)
	spec.Detach = true
	if _, err := subprocess.Run(context.Background(), spec, subprocess.RunOptions{}); herr.Kind(err) != subprocess.ReasonDetached {
		t.Errorf("Run(detached) = %v; want %s", err, subprocess.ReasonDetached)
	}
	for name, set := range map[string]func(*subprocess.Spec){
		"stdin reader":  func(s *subprocess.Spec) { s.Stdin = subprocess.StdinData([]byte("x")) },
		"stdout writer": func(s *subprocess.Spec) { s.Stdout = &strings.Builder{} },
		"stderr writer": func(s *subprocess.Spec) { s.Stderr = &strings.Builder{} },
	} {
		s := spec
		set(&s)
		if _, err := subprocess.Start(context.Background(), s); herr.Kind(err) != subprocess.ReasonDetached {
			t.Errorf("%s: Start(detached) = %v; want %s", name, err, subprocess.ReasonDetached)
		}
	}
}

// stopDetached is the test stopping a detached child itself, and waiting until
// the reaper saw it exit.
func stopDetached(t *testing.T, p *subprocess.Process) {
	t.Helper()
	killPid(p.Pid())
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	select {
	case <-p.Exited():
	case <-ctx.Done():
		t.Errorf("detached child %d did not exit after its kill", p.Pid())
	}
	procwait.Until(ctx, t, func() bool { return subprocesstest.Exited(p.Pid()) })
}

func killPid(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
