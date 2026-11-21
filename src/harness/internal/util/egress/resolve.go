package egress

import (
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Reasons Resolve reports.
const (
	// ReasonRefusedProxyValue is a proxy name whose value is not a proxy address;
	// it is a diagnostic, and the scheme it would serve connects directly.
	ReasonRefusedProxyValue herr.Reason = "egress/refused-proxy-value"
	// ReasonCAUnreadable is a certificate file of ExtraCACerts that cannot be read
	// or holds no certificate; it stops the launch.
	ReasonCAUnreadable herr.Reason = "egress/ca-unreadable"
)

// Lookup reads one launch name: the trusted view of the launch snapshot, which
// boot gives the service (launch.Snapshot.Trusted().Func()).
type Lookup func(name string) (string, bool)

// Diagnostic is one launch line about the proxy names. It never holds a proxy
// password.
type Diagnostic struct {
	Reason  herr.Reason `json:"reason"`
	Name    string      `json:"name"`
	Message string      `json:"message"`
}

func (d Diagnostic) String() string { return fmt.Sprintf("%s: %s [%s]", d.Name, d.Message, d.Reason) }

// Resolve builds the service of the launch: the proxy policy read through lookup
// (lower case first, then upper case; blank is unset): HTTP_PROXY for http,
// HTTPS_PROXY for https, ALL_PROXY for a scheme whose own name is unset, and
// HTTPS falling back to the HTTP proxy last; NO_PROXY bypasses and loopback is
// always direct. Only http, https, socks5 and socks5h proxy addresses are
// accepted (DH-ADR-UNDICI-2; net/http dials all four): any other value (PAC, ftp,
// garbage)
// is a diagnostic and that scheme goes direct, never borrowing another scheme's
// proxy. The certificate authorities are the system's plus c.ExtraCACerts; an
// unreadable certificate file is an error. A nil lookup reads nothing. A Proxy
// already set in c is kept.
func Resolve(lookup Lookup, c Config) (*Service, []Diagnostic, error) {
	roots, err := loadRoots(c.ExtraCACerts)
	if err != nil {
		return nil, nil, err
	}
	c.roots = roots
	var diags []Diagnostic
	if c.Proxy == nil {
		var p *proxyPolicy
		p, diags = readProxy(lookup)
		c.Proxy = p.proxy
	}
	return New(c), diags, nil
}

func loadRoots(files []string) (*x509.CertPool, error) {
	if len(files) == 0 {
		return nil, nil
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, herr.Newf(ReasonCAUnreadable, "certificate file %s: %v", f, err)
		}
		if !pool.AppendCertsFromPEM(data) {
			return nil, herr.Newf(ReasonCAUnreadable, "certificate file %s holds no PEM certificate", f)
		}
	}
	return pool, nil
}

// proxyPolicy is the resolved proxy names.
type proxyPolicy struct {
	http, https *url.URL
	bypassAll   bool
	bypass      []bypassRule
}

type bypassRule struct {
	ipnet  *net.IPNet
	ip     net.IP
	domain string // lower case, no leading dot
	sub    bool   // the rule matched subdomains only (".example.com", "*.example.com")
	port   string
}

// readProxy reads the proxy names through lookup.
func readProxy(lookup Lookup) (*proxyPolicy, []Diagnostic) {
	p := &proxyPolicy{}
	if lookup == nil {
		return p, nil
	}
	var diags []Diagnostic
	read := func(names ...string) (string, string) {
		for _, n := range names {
			if v, ok := lookup(n); ok && strings.TrimSpace(v) != "" {
				return n, strings.TrimSpace(v)
			}
		}
		return "", ""
	}
	pick := func(names ...string) *url.URL {
		name, v := read(names...)
		if name == "" {
			return nil
		}
		u, why := parseProxy(v)
		if why != "" {
			diags = append(diags, Diagnostic{Reason: ReasonRefusedProxyValue, Name: name, Message: why + "; its requests go direct"})
			return nil
		}
		return u
	}
	// ALL_PROXY is read (and reported) once, only when a scheme needs it.
	var all *url.URL
	allRead := false
	fallback := func() *url.URL {
		if !allRead {
			allRead = true
			all = pick("all_proxy", "ALL_PROXY")
		}
		return all
	}
	// Each scheme: its own name, else ALL_PROXY. A name that is set but refused
	// keeps the scheme direct; it never borrows another scheme's proxy.
	httpName, _ := read("http_proxy", "HTTP_PROXY")
	if httpName != "" {
		p.http = pick(httpName)
	} else {
		p.http = fallback()
	}
	httpsName, _ := read("https_proxy", "HTTPS_PROXY")
	switch {
	case httpsName != "":
		p.https = pick(httpsName)
	case fallback() != nil:
		p.https = all
	case func() bool { n, _ := read("all_proxy", "ALL_PROXY"); return n == "" }():
		// HTTPS falls back to the HTTP proxy last (DH-ADR-UNDICI-2); a refused
		// ALL_PROXY keeps HTTPS direct instead.
		p.https = p.http
	}
	if _, v := read("no_proxy", "NO_PROXY"); v != "" {
		p.parseBypass(v)
	}
	return p, diags
}

// parseProxy validates one proxy value; why is "" when it is a proxy address. The
// reason never quotes the value, which may hold a password.
func parseProxy(v string) (*url.URL, string) {
	raw := v
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "the value is not a URL"
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Sprintf("the scheme %q is not a proxy scheme (http, https, socks5, socks5h)", u.Scheme)
	}
	if u.Hostname() == "" {
		return nil, "the value names no host"
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return nil, "the value's port is not a port"
		}
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, "a proxy address has no path, query or fragment"
	}
	return u, ""
}

func (p *proxyPolicy) parseBypass(v string) {
	for _, f := range strings.Split(v, ",") {
		f = strings.ToLower(strings.TrimSpace(f))
		if f == "" {
			continue
		}
		if f == "*" {
			p.bypassAll = true
			return
		}
		if _, n, err := net.ParseCIDR(f); err == nil {
			p.bypass = append(p.bypass, bypassRule{ipnet: n})
			continue
		}
		host, port := f, ""
		if h, pt, err := net.SplitHostPort(f); err == nil {
			host, port = h, pt
		}
		host = strings.Trim(host, "[]")
		if ip := net.ParseIP(host); ip != nil {
			p.bypass = append(p.bypass, bypassRule{ip: ip, port: port})
			continue
		}
		r := bypassRule{port: port}
		switch {
		case strings.HasPrefix(host, "*."):
			r.domain, r.sub = host[2:], true
		case strings.HasPrefix(host, "."):
			r.domain, r.sub = host[1:], true
		default:
			r.domain = host
		}
		if r.domain != "" {
			p.bypass = append(p.bypass, r)
		}
	}
}

// proxy is the transport's Proxy hook.
func (p *proxyPolicy) proxy(req *http.Request) (*url.URL, error) {
	return p.forURL(req.URL), nil
}

func (p *proxyPolicy) forURL(u *url.URL) *url.URL {
	if u == nil {
		return nil
	}
	var proxy *url.URL
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		proxy = p.https
	case "http", "ws":
		proxy = p.http
	}
	if proxy == nil || p.bypassAll {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip != nil && (ip.IsLoopback() || ip.IsUnspecified()) {
		return nil
	}
	port := portOf(u)
	for _, r := range p.bypass {
		if r.matches(host, ip, port) {
			return nil
		}
	}
	return proxy
}

func (r bypassRule) matches(host string, ip net.IP, port string) bool {
	if r.ipnet != nil {
		return ip != nil && r.ipnet.Contains(ip)
	}
	if r.port != "" && r.port != port {
		return false
	}
	if r.ip != nil {
		return ip != nil && r.ip.Equal(ip)
	}
	if host == r.domain {
		return !r.sub
	}
	return strings.HasSuffix(host, "."+r.domain)
}
