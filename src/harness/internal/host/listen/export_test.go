package listen

import "net"

// SpoofSource makes every connection the listeners started after it accept report
// addr as its remote address, so a test on loopback can be a source from another
// machine. restore puts the listener back.
func SpoofSource(addr *net.TCPAddr) (restore func()) {
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
