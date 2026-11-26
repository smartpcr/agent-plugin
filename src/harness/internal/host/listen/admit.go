package listen

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// The admission steps before the credential (DH-D-HOST-2, host-telemetry.md §6.1
// steps 2 to 5): the source of the connection, the Host check, the token-parameter
// rule and the origin check. Each refuses before any credential is read.

// parsePeers parses AllowedPeers: each entry is an IP literal or a CIDR prefix.
func parsePeers(entries []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(entries))
	for _, e := range entries {
		if p, err := netip.ParsePrefix(e); err == nil {
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(e)
		if err != nil {
			return nil, herr.Newf(ReasonPeerInvalid, "allowed peer %q is not an IP literal or a CIDR prefix", e)
		}
		a = a.Unmap().WithZone("")
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

type sourceKey struct{}

// connContext decides the connection's source once, when it is accepted.
func (s *Server) connContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, sourceKey{}, s.sourceAllowed(c.RemoteAddr()))
}

// sourceAllowed reports whether a connection from a is admitted: loopback in any
// spelling, or inside one of the allowed peers.
func (s *Server) sourceAllowed(a net.Addr) bool {
	tcp, ok := a.(*net.TCPAddr)
	if !ok {
		return false
	}
	ip := tcp.AddrPort().Addr().Unmap().WithZone("")
	if ip.IsLoopback() {
		return true
	}
	for _, p := range s.peers {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

// fromAllowedSource reads the verdict connContext stored; a request without one
// (never through this server's connections) is refused.
func fromAllowedSource(r *http.Request) bool {
	ok, _ := r.Context().Value(sourceKey{}).(bool)
	return ok
}

// hostAllowed is the Host check: a loopback name or literal, the bind literal on
// any port, or a configured trusted host, parsed with net/netip. "127.1", a
// rebinding name and an empty Host fail.
func (s *Server) hostAllowed(h string) bool {
	if h == "" {
		return false
	}
	name := h
	if host, _, err := net.SplitHostPort(h); err == nil {
		name = host
	}
	name = strings.TrimSuffix(strings.TrimPrefix(name, "["), "]")
	if a, err := netip.ParseAddr(name); err == nil {
		if isLoopback(a) || a.Unmap().WithZone("") == s.bound.Addr().Unmap().WithZone("") {
			return true
		}
	} else if strings.EqualFold(strings.TrimSuffix(name, "."), "localhost") {
		return true
	}
	for _, t := range s.trusted {
		if _, _, err := net.SplitHostPort(t); err == nil {
			if strings.EqualFold(t, h) {
				return true
			}
		} else if strings.EqualFold(strings.TrimSuffix(strings.Trim(t, "[]"), "."), strings.TrimSuffix(name, ".")) {
			return true
		}
	}
	return false
}

// ownOrigin reports whether o is one of the listener's own origins: its scheme, an
// allowed host and its port; a trusted host written with a port; or PublicURL.
func (s *Server) ownOrigin(o string) bool {
	if s.public != "" && strings.EqualFold(o, s.public) {
		return true
	}
	u, err := url.Parse(o)
	if err != nil || u.Host == "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return false
	}
	scheme := "http"
	if s.secure {
		scheme = "https"
	}
	if !strings.EqualFold(u.Scheme, scheme) || !s.hostAllowed(u.Host) {
		return false
	}
	port := u.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	if port == strconv.Itoa(int(s.bound.Port())) {
		return true
	}
	for _, t := range s.trusted {
		if strings.EqualFold(t, u.Host) {
			return true
		}
	}
	return false
}

// originAllowed is the origin check of every class but Callback.
// `Sec-Fetch-Site: cross-site` and `Origin: null` fail, and an Origin must match the
// Host. A method other than GET and HEAD, and every upgrade, must be same-origin --
// `Sec-Fetch-Site: same-origin` or an Origin that is one of the listener's own -- and
// pass http.CrossOriginProtection; same-site (another port on localhost) is not enough.
func (s *Server) originAllowed(r *http.Request) bool {
	site := strings.ToLower(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")))
	if site == "cross-site" {
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "null" {
		return false
	}
	if origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
			return false
		}
	}
	safe := (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.Header.Get("Upgrade") == ""
	if safe {
		return true
	}
	if site != "same-origin" && (origin == "" || !s.ownOrigin(origin)) {
		return false
	}
	return s.csrf.Check(r) == nil
}

// tokenParameter applies the token-parameter rule: it returns the token of the one
// exchange (`GET /` with exactly one non-empty token), and refuse is true for a
// token parameter on any other request. A query that does not parse cannot hide one.
func tokenParameter(r *http.Request) (token string, refuse bool) {
	if !strings.Contains(r.URL.RawQuery, "token") {
		return "", false
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return "", true
	}
	toks, has := q["token"]
	if !has {
		return "", false
	}
	if r.Method != http.MethodGet || r.URL.Path != "/" || len(toks) != 1 || toks[0] == "" {
		return "", true
	}
	return toks[0], false
}

// refuse answers a pipeline refusal: the reason is the whole body.
func refuse(w http.ResponseWriter, code int, reason herr.Reason) {
	http.Error(w, string(reason), code)
}
