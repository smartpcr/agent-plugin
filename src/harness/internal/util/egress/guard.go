package egress

import (
	"net/netip"
	"net/url"
	"strconv"
	"syscall"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Guard decides whether a client may connect to addr (DH-LIB-UNDICI-R9). It runs
// in the dialler's Control hook, so it sees the address actually being connected
// to, after name resolution and before the connection is opened, for every
// attempt; it also runs on a literal address in a request's URL (the first
// request and every redirect) before the request is sent. A non-nil error refuses
// the address. Options.Guard nil allows every address.
type Guard func(addr netip.AddrPort) error

// PrivatePolicy configures DenyPrivate.
type PrivatePolicy struct {
	// AllowLoopback lets loopback addresses (127.0.0.0/8, ::1) through; a test's
	// own listener is one. Every other private range stays refused.
	AllowLoopback bool
}

// cgnat is the shared address space of carrier-grade NAT (RFC 6598), which
// netip.Addr.IsPrivate does not count.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// thisNetwork is 0.0.0.0/8 (RFC 1122), "this host on this network".
var thisNetwork = netip.MustParsePrefix("0.0.0.0/8")

// DenyPrivate is the guard that refuses every address that is not on the public
// internet: private ranges (10/8, 172.16/12, 192.168/16, fc00::/7), carrier-grade
// NAT (100.64/10), link-local (169.254/16, fe80::/10, which holds the cloud
// metadata address), multicast, unspecified and 0.0.0.0/8, and loopback unless
// p.AllowLoopback. An IPv4 address written as IPv4-mapped IPv6 is judged as the
// IPv4 address. A refusal is egress/not-permitted.
func DenyPrivate(p PrivatePolicy) Guard {
	return func(addr netip.AddrPort) error {
		a := addr.Addr().Unmap()
		switch {
		case !a.IsValid():
			return refused(addr, "it is not an IP address")
		case a.IsLoopback():
			if p.AllowLoopback {
				return nil
			}
			return refused(addr, "it is a loopback address")
		case a.IsPrivate(), cgnat.Contains(a):
			return refused(addr, "it is a private address")
		case a.IsLinkLocalUnicast(), a.IsLinkLocalMulticast():
			return refused(addr, "it is a link-local address")
		case a.IsUnspecified(), thisNetwork.Contains(a):
			return refused(addr, "it is an unspecified address")
		case a.IsMulticast(), a.IsInterfaceLocalMulticast():
			return refused(addr, "it is a multicast address")
		}
		return nil
	}
}

func refused(addr netip.AddrPort, why string) error {
	return herr.Newf(ReasonNotPermitted, "the guard refuses %s: %s", addr, why)
}

// control is the dialler's Control hook for guard: address is the resolved
// "ip:port" about to be connected to.
func control(guard Guard) func(network, address string, _ syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		ap, err := netip.ParseAddrPort(address)
		if err != nil {
			return herr.Newf(ReasonNotPermitted, "the guard cannot read the %s address %q", network, address)
		}
		return guard(ap)
	}
}

// guardLiteral applies guard to a literal IP host in u before the request is
// sent; a host name is left to the dialler's hook, which sees what it resolves to.
func guardLiteral(guard Guard, u *url.URL) error {
	if guard == nil || u == nil {
		return nil
	}
	a, err := netip.ParseAddr(u.Hostname())
	if err != nil {
		return nil
	}
	port, _ := strconv.ParseUint(portOf(u), 10, 16)
	return guard(netip.AddrPortFrom(a.WithZone(""), uint16(port)))
}
