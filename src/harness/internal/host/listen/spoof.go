package listen

import "net"

// spoofSource makes every connection the listeners started after it accept report
// addr as its remote address, so a test on loopback can be a source from another
// machine. A test never binds a non-loopback address for that: on Windows the
// first such bind of a new test binary raises a firewall prompt, and opening the
// port without one needs an administrator. restore puts the listener back. Only
// tests call it: SpoofSource (export_test.go) and E2ESpoofSource (export_e2e.go).
func spoofSource(addr *net.TCPAddr) (restore func()) {
	wrapAccepted = func(ln net.Listener) net.Listener { return spoofed{Listener: ln, addr: addr} }
	return func() { wrapAccepted = nil }
}

type spoofed struct {
	net.Listener
	addr *net.TCPAddr
}

func (s spoofed) Accept() (net.Conn, error) {
	c, err := s.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return spoofedConn{Conn: c, addr: s.addr}, nil
}

type spoofedConn struct {
	net.Conn
	addr *net.TCPAddr
}

func (c spoofedConn) RemoteAddr() net.Addr { return c.addr }
