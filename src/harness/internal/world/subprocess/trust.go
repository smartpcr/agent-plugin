package subprocess

import (
	"net"
	"strings"
	"sync/atomic"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Trust is the mark recorded for every tree from its launch plan (step 19.4.4):
// a tree started with sandbox.Trusted is Trusted; every other plan, confined or
// not, makes an Untrusted tree. A process in no tree of the host is Outside.
type Trust string

// The trust marks.
const (
	Trusted   Trust = "trusted"
	Untrusted Trust = "untrusted"
	Outside   Trust = "outside"
)

func trustOf(l sandbox.Launch) Trust {
	if l.IsTrusted() {
		return Trusted
	}
	return Untrusted
}

// Trust is the mark of p's tree; Outside for a detached launch, which belongs to
// no tree.
func (p *Process) Trust() Trust {
	if p.detached {
		return Outside
	}
	return p.trust
}

// Tree is PeerTree's answer: which tree the process behind a connection is in.
type Tree struct {
	// Trust is the tree's mark, or Outside when the process is in no tree the
	// registry holds.
	Trust Trust
	// Exact is true when the answer comes from exact membership (job object,
	// control group). On the process-group rung it is best effort: a member can
	// leave its group, so an Untrusted process may be answered Outside.
	Exact bool
	// Peer is the pid of the process behind the connection.
	Peer int
	// Root is the direct child of the tree it is in; 0 when Outside.
	Root int
	// Owner is that tree's owner kind; "" when Outside.
	Owner OwnerKind
}

// peerPid names the process behind a local connection: the peer credentials of
// a Unix socket, the owner of the other end of a loopback TCP connection. It is
// per system (peer_*.go); the trust tests replace it with a fake lookup.
var peerPid = connPeerPid

// PeerTree answers whether the local process behind c is in an Untrusted or a
// Trusted tree of this registry, or Outside (DH-D-EXEC-2; A18 refuses a process
// started for the model at the control endpoint). Membership is the job's on
// Windows and the control group's on Linux, which is exact, and the process
// group's and parent chain's on the process-group rung, which is best effort.
// It fails with subprocess/peer-unknown when the peer cannot be named on this
// system or for this kind of connection.
func (r *Registry) PeerTree(c net.Conn) (Tree, error) {
	pid, err := peerPid(c)
	if err != nil {
		return Tree{}, herr.Wrap(ReasonPeerUnknown, err, "name the process behind the connection")
	}
	return r.treeOf(pid), nil
}

// treeOf finds the registered tree pid is in.
func (r *Registry) treeOf(pid int) Tree {
	exact := true
	for _, p := range r.Live() {
		in := false
		_ = p.withOwner(func(o treeOwner) error { in = o.member(pid); return nil })
		if in {
			return Tree{Trust: p.Trust(), Exact: p.report.Exact, Peer: pid, Root: p.Pid(), Owner: p.report.Kind}
		}
		exact = exact && p.report.Exact
	}
	return Tree{Trust: Outside, Exact: exact, Peer: pid}
}

// controlSocket is the host's control-socket path, which no child environment
// may hold (DH-ADR-EXEC-8).
var controlSocket atomic.Pointer[string]

// SetControlSocket records the host's control-socket path; from then on Start
// refuses a Spec whose environment holds it in any value, so no child is ever
// given the way to the host's control endpoint. An empty path clears it.
func SetControlSocket(path string) {
	if path == "" {
		controlSocket.Store(nil)
		return
	}
	controlSocket.Store(&path)
}

// checkControlSocket refuses an environment that holds the control-socket path.
func checkControlSocket(env Env) error {
	path := controlSocket.Load()
	if path == nil {
		return nil
	}
	for _, kv := range env {
		name, value, _ := strings.Cut(kv, "=")
		if strings.Contains(value, *path) {
			return herr.Newf(ReasonBadArgument, "the child environment's %s holds the host's control-socket path; no child is given it", name)
		}
	}
	return nil
}
