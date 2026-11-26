// Package listen is host.listen, the one HTTP listener of the host (DH-D-HOST-1,
// docs/harness/areas/host-telemetry.md §6.1).
//
// One http.Server listens on one address parsed once by ParseBind, optionally over
// TLS whose certificate pair is re-read when the files change. It runs one handler,
// the admission pipeline (DH-D-HOST-1, DH-D-HOST-2): limits; the source of the
// connection (loopback or an allowed peer, decided once per accepted connection);
// the Host check; the peer rule (a local peer inside an untrusted process tree,
// resolved once per accepted connection); the token-parameter rule, whose one exchange is the
// listen/exchange bail key; the mount chosen by longest prefix; the mount's class;
// the origin check (not for Callback); then (for a Required mount) the
// listen/authenticate bail key, and only then the mount's handler, with the Peer in
// the request context. No component can insert a step or reach a handler around it.
package listen

import (
	"context"
	"net"
	"net/http"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/access"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Reasons the listener refuses a configuration or a mount.
const (
	ReasonBindInvalid     herr.Reason = "listen/bind-invalid"
	ReasonBindUnspecified herr.Reason = "listen/bind-unspecified"
	ReasonInsecureRemote  herr.Reason = "listen/insecure-remote"
	ReasonTLS             herr.Reason = "listen/tls"
	ReasonMountInvalid    herr.Reason = "listen/mount-invalid"
	ReasonMountTaken      herr.Reason = "listen/mount-taken"
	ReasonCompression     herr.Reason = "listen/compression"
	ReasonPeerInvalid     herr.Reason = "listen/peer-invalid"
)

// Reasons the pipeline refuses a request with; each is the body of its response.
const (
	// ReasonSource is a connection from neither loopback nor an allowed peer (403).
	ReasonSource herr.Reason = "auth/source"
	// ReasonHost is a Host that is not loopback, the bind literal or a trusted host (403).
	ReasonHost herr.Reason = "auth/host"
	// ReasonUntrustedPeer is a local connection whose process is inside an untrusted
	// tree, whatever it carries (403, DH-ADR-HOST-12).
	ReasonUntrustedPeer herr.Reason = "auth/untrusted-peer"
	// ReasonTokenParameter is a token parameter anywhere but the exchange (400).
	ReasonTokenParameter herr.Reason = "auth/token-parameter"
	// ReasonOrigin is a cross-site or cross-origin request (403).
	ReasonOrigin herr.Reason = "auth/origin"
	// ReasonUnauthorized is a request with no credential that admits it (401).
	ReasonUnauthorized herr.Reason = "auth/unauthorized"
)

// Class is how a mount's request is admitted. The zero value is Required; a value
// that is none of the classes below is refused like Deny.
type Class uint8

// The classes.
const (
	// Required requests carry a credential that listen/authenticate answers.
	Required Class = iota
	// Anonymous requests skip the credential and get no peer.
	Anonymous
	// Callback requests skip the credential and, from DH-D-HOST-2, the origin check.
	Callback
	// Deny refuses the request with 403 before any credential is read.
	Deny
)

// Mount is one prefix of the URL space and the handler that serves it.
type Mount struct {
	// Prefix is "/" or a path starting with "/". A prefix ending in "/" matches every
	// path under it; any other matches itself and the paths below it ("/api" matches
	// "/api" and "/api/x", never "/apix").
	Prefix string
	// Classify is asked before any credential is read; nil is Required and a panic
	// counts as Deny.
	Classify func(r *http.Request) Class
	// Stream reports a long-lived request: it answers 503 until Ready closes and must
	// end when Done closes. nil means no request streams.
	Stream func(r *http.Request) bool
	// Serve is called only after admission.
	Serve http.Handler
	// Uncompressed keeps the mount's responses out of Config.Compression: embedded
	// assets, which carry their own caching and range semantics.
	Uncompressed bool
}

// Credentials is what listen/authenticate and listen/exchange read from a request
// that passed the source, Host and origin checks.
type Credentials struct {
	Request *http.Request
	// Token is the launch token of an exchange; empty for listen/authenticate.
	Token string
	// Audience is the listener's own origin, which a session belongs to.
	Audience string
	// Secure is whether the listener itself speaks TLS; a forwarded header never
	// sets it.
	Secure bool
}

// Peer is the one browser a Required request came from.
type Peer interface {
	ID() string
	Principal() access.Principal
	// Context ends when the peer is revoked.
	Context() context.Context
	FormToken() string
}

// Authenticate is the bail key the pipeline asks for a Required request. No answer
// is 401; an error is 500. host.auth answers it (DH-D-HOST-2).
var Authenticate = cordis.NewBail[*Credentials, Peer]("listen/authenticate")

// Exchange is the bail key the pipeline asks for `GET /?token=`: the answer is the
// cookies the pipeline sets with its 303 to "./". No answer (a token that is unknown,
// used or expired) is 401; an error is 500. host.auth answers it (DH-D-HOST-2).
var Exchange = cordis.NewBail[*Credentials, []*http.Cookie]("listen/exchange")

type peerKey struct{}

// PeerFrom returns the peer the pipeline admitted the request as.
func PeerFrom(ctx context.Context) (Peer, bool) {
	p, ok := ctx.Value(peerKey{}).(Peer)
	return p, ok
}

// PeerRule is how well the listener tells a local peer inside an untrusted process
// tree on this machine (DH-ADR-HOST-12).
type PeerRule string

// The peer rules.
const (
	// Exact: tree membership is the job object's or the control group's.
	Exact PeerRule = "exact"
	// BestEffort: membership is the process group's, which a member can leave.
	BestEffort PeerRule = "best-effort"
	// NotApplied: the connecting process cannot be identified, or there is no tree
	// registry; local peers are served on their credential alone.
	NotApplied PeerRule = "not-applied"
)

// Trees names the process tree behind a local connection (DH-D-EXEC-2); the boot
// tree's *subprocess.Registry is one.
type Trees interface {
	PeerTree(c net.Conn) (subprocess.Tree, error)
}

// Listener is the service under Key.
type Listener interface {
	// PeerRule says whether the peer rule is exact, best effort or not applied here.
	PeerRule() PeerRule
	// Mount registers m as an effect of owner's fiber: the mount leaves when owner
	// unloads. A prefix already mounted is an error.
	Mount(owner *cordis.Context, m Mount) error
	Origin() string
	Secure() bool
	Mode() Mode
	// Ready closes when boot reports the required entries loaded.
	Ready() <-chan struct{}
	// Done closes when the listener starts to shut down.
	Done() <-chan struct{}
}

// matches reports whether prefix covers path.
func matches(prefix, path string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	return len(path) == len(prefix) || strings.HasSuffix(prefix, "/") || path[len(prefix)] == '/'
}

// longest returns the mount with the longest prefix covering path.
func longest(mounts []*Mount, path string) *Mount {
	var best *Mount
	for _, m := range mounts {
		if matches(m.Prefix, path) && (best == nil || len(m.Prefix) > len(best.Prefix)) {
			best = m
		}
	}
	return best
}

func validPrefix(p string) bool {
	return strings.HasPrefix(p, "/") && !strings.Contains(p, "//") && !strings.ContainsAny(p, "?#\\")
}

// classify runs m.Classify; a panic is Deny.
func classify(m *Mount, r *http.Request) (c Class) {
	if m.Classify == nil {
		return Required
	}
	defer func() {
		if recover() != nil {
			c = Deny
		}
	}()
	return m.Classify(r)
}

// streams runs m.Stream; a panic counts as a stream, so it waits for Ready.
func streams(m *Mount, r *http.Request) (s bool) {
	if m.Stream == nil {
		return false
	}
	defer func() {
		if recover() != nil {
			s = true
		}
	}()
	return m.Stream(r)
}
