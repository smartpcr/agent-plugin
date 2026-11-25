package subprocess

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// memberOwner is a fake owner whose members are the pids listed.
type memberOwner struct {
	stuckOwner
	pids []int
}

func (o *memberOwner) member(pid int) bool { return slices.Contains(o.pids, pid) }

// fakeTree is a registered tree with a fake owner of the kind given.
func fakeTree(root int, kind OwnerKind, exact bool, trust Trust, members ...int) *Process {
	return &Process{
		cmd:   &exec.Cmd{Process: &os.Process{Pid: root}},
		owner: &memberOwner{pids: append([]int{root}, members...)}, report: Owner{Kind: kind, Exact: exact}, trust: trust,
		exited: make(chan struct{}), releasedC: make(chan struct{}),
	}
}

// With fake owners and a fake peer lookup (the peer half of DH-T-EXEC-17): a
// process in a tree started under an unconfined plan is Untrusted, the host's
// own helper Trusted, a detached launch and an unrelated process Outside; the
// job and control-group owners answer Exact and the process-group rung best
// effort; a peer that cannot be named is subprocess/peer-unknown.
//
//dsh:test DH-T-EXEC-17
func TestPeerTreeAnswersFromTheTrustMark(t *testing.T) {
	if trustOf(sandbox.Untrusted("a helper that reads the workspace")) != Untrusted || trustOf(sandbox.Trusted("the ssh client")) != Trusted {
		t.Fatal("the mark does not follow the launch plan")
	}
	reg := NewRegistry()
	reg.Add(fakeTree(100, JobObject, true, Untrusted, 101))
	reg.Add(fakeTree(200, ControlGroup, true, Trusted, 201))
	reg.Add(fakeTree(300, ProcessGroup, false, Untrusted, 301))
	detached := fakeTree(400, Detached, false, Untrusted, 401)
	detached.detached = true
	reg.Add(detached)
	if detached.Trust() != Outside {
		t.Fatalf("a detached launch's mark = %s; want outside", detached.Trust())
	}

	conns := map[net.Conn]int{}
	saved := peerPid
	t.Cleanup(func() { peerPid = saved })
	peerPid = func(c net.Conn) (int, error) {
		if pid, ok := conns[c]; ok {
			return pid, nil
		}
		return 0, errors.New("unnamed")
	}
	ask := func(pid int) Tree {
		t.Helper()
		a, b := net.Pipe()
		t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
		conns[a] = pid
		tree, err := reg.PeerTree(a)
		if err != nil {
			t.Fatalf("PeerTree(%d): %v", pid, err)
		}
		return tree
	}
	for _, c := range []struct {
		pid   int
		want  Tree
		about string
	}{
		{101, Tree{Trust: Untrusted, Exact: true, Peer: 101, Root: 100, Owner: JobObject}, "a member of an untrusted job"},
		{201, Tree{Trust: Trusted, Exact: true, Peer: 201, Root: 200, Owner: ControlGroup}, "the host's own helper, in a control group"},
		{301, Tree{Trust: Untrusted, Exact: false, Peer: 301, Root: 300, Owner: ProcessGroup}, "a member of an untrusted process group"},
		{401, Tree{Trust: Outside, Exact: false, Peer: 401}, "a detached launch's child"},
		{999, Tree{Trust: Outside, Exact: false, Peer: 999}, "an unrelated process, beside a best-effort rung"},
	} {
		if got := ask(c.pid); got != c.want {
			t.Errorf("%s: PeerTree = %+v; want %+v", c.about, got, c.want)
		}
	}
	exactOnly := NewRegistry()
	exactOnly.Add(fakeTree(100, JobObject, true, Untrusted))
	if got := exactOnly.treeOf(999); got.Trust != Outside || !got.Exact {
		t.Errorf("an unrelated process beside exact owners = %+v; want an exact outside", got)
	}

	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	if _, err := reg.PeerTree(a); herr.Kind(err) != ReasonPeerUnknown {
		t.Fatalf("PeerTree of an unnamed peer = %v; want %s", err, ReasonPeerUnknown)
	}
	if _, err := saved(a); err == nil {
		t.Fatal("the real lookup named the peer of an in-memory pipe")
	}
}

// No child environment holds the control socket's path: Start refuses a Spec
// whose environment carries it in any value.
//
//dsh:test DH-T-EXEC-17
func TestStartKeepsTheControlSocketOutOfTheChild(t *testing.T) {
	const path = "/run/dsh/control-7f3a.sock"
	SetControlSocket(path)
	t.Cleanup(func() { SetControlSocket("") })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	spec := Spec{Argv: []string{exe}, Env: Env{"SUBPROCESSTEST_ROLE=exit", "HELPER_ENDPOINT=unix://" + path}, Launch: sandbox.Untrusted("subprocess test")}
	if _, err := Start(context.Background(), spec); herr.Kind(err) != ReasonBadArgument {
		t.Fatalf("Start with the control socket in the environment = %v; want %s", err, ReasonBadArgument)
	}
	spec.Env = Env{"SUBPROCESSTEST_ROLE=exit"}
	p, err := Start(context.Background(), spec)
	if err != nil {
		t.Fatalf("Start without it: %v", err)
	}
	t.Cleanup(func() { _ = p.Kill() })
	if err := p.Wait(); err != nil || p.Trust() != Untrusted {
		t.Fatalf("Wait = %v, trust %s; want an untrusted tree that ran", err, p.Trust())
	}
}
