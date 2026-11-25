package subprocess_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// modelStarted stands for a command started for the model: an unconfined,
// untrusted plan.
var modelStarted = sandbox.Untrusted("subprocess test: a fixture standing for a model-started command")

// dialTree starts a RoleDial child under launch, registers it, and returns it
// with the host's end of its connection.
func dialTree(t *testing.T, reg *subprocess.Registry, ln net.Listener, launch sandbox.Launch) (*subprocess.Process, net.Conn, int) {
	t.Helper()
	spec := subprocesstest.Spec(t, subprocesstest.RoleDial, ln.Addr().Network(), ln.Addr().String())
	spec.Launch = launch
	out, w := io.Pipe()
	spec.Stdout = w
	p := subprocesstest.Start(t, spec)
	t.Cleanup(func() { _ = out.Close() })
	reg.Add(p)
	line, err := bufio.NewReader(out).ReadString('\n')
	var pid int
	if _, serr := fmt.Sscanf(line, "dialed %d", &pid); err != nil || serr != nil {
		t.Fatalf("dialer said %q: %v %v", line, err, serr)
	}
	if d, ok := ln.(interface{ SetDeadline(time.Time) error }); ok {
		_ = d.SetDeadline(time.Now().Add(20 * time.Second))
	}
	c, err := ln.Accept()
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	if got, err := bufio.NewReader(c).ReadString('\n'); err != nil || got != line {
		t.Fatalf("connection said %q (%v); want the dialer's %q", got, err, line)
	}
	return p, c, pid
}

// listeners are a Unix socket in a short temporary directory and a loopback TCP
// port.
func listeners(t *testing.T) []net.Listener {
	t.Helper()
	dir, err := os.MkdirTemp("", "dshpeer")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	var out []net.Listener
	for _, a := range [][2]string{{"unix", filepath.Join(dir, "c.sock")}, {"tcp", "127.0.0.1:0"}} {
		ln, err := net.Listen(a[0], a[1])
		if err != nil {
			t.Fatalf("listen %s %s: %v", a[0], a[1], err)
		}
		t.Cleanup(func() { _ = ln.Close() })
		out = append(out, ln)
	}
	return out
}

// Scenario "The peer of a model-started process is Untrusted" (19.4.4): a tree
// started untrusted dials the host over a Unix socket and over loopback TCP;
// PeerTree names its tree Untrusted, Exact on the job and control-group rungs
// and best effort on the group rung. A tree of the host's own helper is
// Trusted, and the host's own connection is Outside.
//
//dsh:test DH-T-EXEC-17
func TestPeerTreeOfADialingTree(t *testing.T) {
	for _, ln := range listeners(t) {
		t.Run(ln.Addr().Network(), func(t *testing.T) {
			reg := subprocess.NewRegistry()
			p, c, pid := dialTree(t, reg, ln, modelStarted)
			tree, err := reg.PeerTree(c)
			t.Logf("peer %d over %s: %+v (owner %s)", pid, ln.Addr().Network(), tree, p.Owner())
			if err != nil {
				t.Fatalf("PeerTree: %v", err)
			}
			if tree.Trust != subprocess.Untrusted || tree.Root != p.Pid() || tree.Peer != pid || tree.Exact != p.Owner().Exact || tree.Owner != p.Owner().Kind {
				t.Fatalf("PeerTree = %+v; want untrusted, root %d, exact %v", tree, p.Pid(), p.Owner().Exact)
			}
			if p.Owner().Kind != subprocess.ProcessGroup && !tree.Exact {
				t.Fatalf("owner %s answered best effort; job and control-group answers are exact", p.Owner())
			}

			helper, hc, _ := dialTree(t, reg, ln, subprocesstest.Launch)
			if tree, err := reg.PeerTree(hc); err != nil || tree.Trust != subprocess.Trusted || tree.Root != helper.Pid() {
				t.Fatalf("PeerTree of the host's helper = %+v, %v; want trusted", tree, err)
			}

			own, err := net.Dial(ln.Addr().Network(), ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = own.Close() })
			back, err := ln.Accept()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = back.Close() })
			if tree, err := reg.PeerTree(back); err != nil || tree.Trust != subprocess.Outside || tree.Peer != os.Getpid() {
				t.Fatalf("PeerTree of the host's own connection = %+v, %v; want outside", tree, err)
			}
		})
	}
}

// A connection that is neither a Unix socket nor loopback TCP is not answered.
func TestPeerTreeRefusesAConnectionItCannotName(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	if _, err := subprocess.NewRegistry().PeerTree(a); err == nil || !strings.Contains(err.Error(), "peer-unknown") {
		t.Fatalf("PeerTree of an in-memory pipe = %v; want subprocess/peer-unknown", err)
	}
}
