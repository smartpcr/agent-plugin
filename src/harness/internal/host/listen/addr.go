package listen

import (
	"net/netip"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Mode says who can reach the listener: only this machine, or others too.
type Mode uint8

// The modes.
const (
	Local Mode = iota
	Remote
)

func (m Mode) String() string {
	if m == Remote {
		return "remote"
	}
	return "local"
}

// ParseBind parses a bind address once, with net/netip, into the value the listener
// listens on. Only an IP literal with a port is accepted (the port may be 0): a host
// name is refused because its resolution can change what is reachable, and every
// spelling of the unspecified address is refused because it binds every interface
// (DH-ADR-HOST-2). Mapped and zoned spellings are accepted as written.
func ParseBind(s string) (netip.AddrPort, error) {
	ap, err := netip.ParseAddrPort(s)
	if err != nil {
		return netip.AddrPort{}, herr.Newf(ReasonBindInvalid, "bind address %q is not an IP literal with a port", s)
	}
	if ap.Addr().Unmap().IsUnspecified() {
		return netip.AddrPort{}, herr.Newf(ReasonBindUnspecified, "bind address %q is the unspecified address; name one concrete address", s)
	}
	return ap, nil
}

// isLoopback reports whether a is a loopback address in any spelling.
func isLoopback(a netip.Addr) bool { return a.Unmap().IsLoopback() }
