package egress

import "net/url"

// ProxyFor is the proxy the policy read through l chooses for u; nil is direct.
func ProxyFor(l Lookup, u *url.URL) *url.URL {
	p, _ := readProxy(l)
	return p.forURL(u)
}
