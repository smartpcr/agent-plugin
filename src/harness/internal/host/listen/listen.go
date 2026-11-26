// Package listen is host.listen, the one HTTP listener of the host (DH-D-HOST-1,
// docs/harness/areas/host-telemetry.md §6.1).
//
// One http.Server listens on one address parsed once by ParseBind, optionally over
// TLS whose certificate pair is re-read when the files change. It runs one handler,
// the admission pipeline: limits, then the mount chosen by longest prefix, then the
// mount's class, then (for a Required mount) the listen/authenticate bail key, and
// only then the mount's handler, with the Peer in the request context. No component
// can insert a step or reach a handler around it. The source, Host, token-parameter
// and origin steps (DH-D-HOST-2) are added here by host.auth's stage.
package listen

import (
	"context"
	"net/http"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
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

// Credentials is what listen/authenticate reads from an admitted request.
type Credentials struct {
	Request *http.Request
}

// Peer is the one browser a Required request came from.
type Peer interface {
	ID() string
	// Context ends when the peer is revoked.
	Context() context.Context
	FormToken() string
}

// Authenticate is the bail key the pipeline asks for a Required request. No answer
// is 401; an error is 500. host.auth answers it (DH-D-HOST-2).
var Authenticate = cordis.NewBail[*Credentials, Peer]("listen/authenticate")

type peerKey struct{}

// PeerFrom returns the peer the pipeline admitted the request as.
func PeerFrom(ctx context.Context) (Peer, bool) {
	p, ok := ctx.Value(peerKey{}).(Peer)
	return p, ok
}

// Listener is the service under Key.
type Listener interface {
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
