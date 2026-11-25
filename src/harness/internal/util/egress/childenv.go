package egress

import (
	"net/url"
	"strings"
)

// Audience says who a child environment is for (DH-LIB-UNDICI-R7, DH-ADR-8).
type Audience int

const (
	// Trusted is a program the harness itself starts (a language server, an MCP
	// server): it receives the user's proxy values as they set them.
	Trusted Audience = iota
	// ModelRun is a command the model runs: an ordinary sandboxed command that
	// gets the same proxy values except any that carries credentials, which is
	// omitted (cleared) and reported once.
	ModelRun
)

func (a Audience) String() string {
	if a == ModelRun {
		return "model-run"
	}
	return "trusted"
}

// childNames are the proxy names a child overlay sets, each in upper and lower
// case. NodeEnvProxy is set as well.
var childNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"}

// NodeEnvProxy tells a Node child (an MCP server) to honour the proxy names
// (UP-DEBT-limit.packages.util.http-proxy.3); the overlay sets it to "1" when any
// proxy value is set.
const NodeEnvProxy = "NODE_USE_ENV_PROXY"

// loopbackBypass is added to a child's NO_PROXY whenever a proxy is set, so a
// child never sends its loopback traffic to the proxy.
var loopbackBypass = []string{"localhost", "127.0.0.1", "::1"}

// childProxy is what ChildEnv needs from the launch: the user's own values of the
// proxy names (keyed by the upper-case name) and the resolved proxy of each scheme.
type childProxy struct {
	user          map[string]string
	resolvedHTTP  string
	resolvedHTTPS string
}

// readChild reads the user's proxy values through lookup (lower case first, then
// upper case; blank is unset) and keeps the resolved ones of p.
func readChild(lookup Lookup, p *proxyPolicy) childProxy {
	c := childProxy{user: map[string]string{}}
	if lookup != nil {
		for _, n := range childNames {
			for _, name := range []string{strings.ToLower(n), n} {
				if v, ok := lookup(name); ok && strings.TrimSpace(v) != "" {
					c.user[n] = strings.TrimSpace(v)
					break
				}
			}
		}
	}
	if p != nil {
		if p.http != nil {
			c.resolvedHTTP = p.http.String()
		}
		if p.https != nil {
			c.resolvedHTTPS = p.https.String()
		}
	}
	return c
}

// ChildEnv is the proxy overlay of a child environment for audience a, as
// "NAME=value" entries for subprocess.EnvOptions.Managed (applied after every
// other entry). It sets every proxy name, in upper and lower case: the user's own
// value where they set the name (a SOCKS value included), else the resolved value
// of that scheme, else empty, which every client reads as unset and which hides a
// host value the launch did not resolve. NO_PROXY gains the loopback bypass and
// NODE_USE_ENV_PROXY is "1" when any proxy is set.
//
// For ModelRun a value that carries credentials (a user name or password) is
// omitted: the name is set empty. Each omitted name is reported in the returned
// diagnostics the first time this service omits it, and never again; the
// diagnostic never quotes the value.
func (s *Service) ChildEnv(a Audience) ([]string, []Diagnostic) {
	c := s.cfg.child
	values := map[string]string{}
	var diags []Diagnostic
	anyProxy := false
	for _, n := range childNames[:3] {
		v := c.user[n]
		if v == "" {
			switch n {
			case "HTTP_PROXY":
				v = c.resolvedHTTP
			case "HTTPS_PROXY":
				v = c.resolvedHTTPS
			}
		}
		if v != "" && a == ModelRun && carriesCredentials(v) {
			if s.firstOmission(n) {
				diags = append(diags, Diagnostic{Reason: ReasonRefusedProxyValue, Name: n,
					Message: "the value carries credentials; a command the model runs does not receive it"})
			}
			v = ""
		}
		values[n] = v
		anyProxy = anyProxy || v != ""
	}
	values["NO_PROXY"] = c.user["NO_PROXY"]
	values[NodeEnvProxy] = ""
	if anyProxy {
		values["NO_PROXY"] = withLoopback(values["NO_PROXY"])
		values[NodeEnvProxy] = "1"
	}
	out := make([]string, 0, 2*len(childNames)+1)
	for _, n := range childNames {
		out = append(out, n+"="+values[n], strings.ToLower(n)+"="+values[n])
	}
	out = append(out, NodeEnvProxy+"="+values[NodeEnvProxy])
	return out, diags
}

// ClearedEnv is the overlay that clears every proxy name (a test fixture's
// child): each is set empty, in upper and lower case, as is NODE_USE_ENV_PROXY.
func ClearedEnv() []string {
	out := make([]string, 0, 2*len(childNames)+1)
	for _, n := range childNames {
		out = append(out, n+"=", strings.ToLower(n)+"=")
	}
	return append(out, NodeEnvProxy+"=")
}

// firstOmission records that name was omitted for a ModelRun child and reports
// whether it is the first time.
func (s *Service) firstOmission(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.omitted[name] {
		return false
	}
	s.omitted[name] = true
	return true
}

// carriesCredentials reports whether a proxy value holds a user name or a
// password. A value that cannot be read as a URL but holds an "@" counts, so the
// check fails closed.
func carriesCredentials(v string) bool {
	raw := v
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return strings.Contains(v, "@")
	}
	return u.User != nil
}

// withLoopback adds every loopback bypass entry that bypass lacks.
func withLoopback(bypass string) string {
	have := map[string]bool{}
	var parts []string
	for _, f := range strings.Split(bypass, ",") {
		if f = strings.TrimSpace(f); f != "" {
			parts = append(parts, f)
			have[strings.ToLower(strings.Trim(f, "[]"))] = true
		}
	}
	for _, l := range loopbackBypass {
		if !have[l] {
			parts = append(parts, l)
		}
	}
	return strings.Join(parts, ",")
}
