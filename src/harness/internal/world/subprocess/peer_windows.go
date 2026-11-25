//go:build windows

package subprocess

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	//lint:ignore typeassert GetExtendedTcpTable fills a caller buffer passed as unsafe.Pointer, and WSAIoctl's output is a uint32 passed by its address
	"unsafe"

	"golang.org/x/sys/windows"
)

// sioAFUnixGetPeerPID is SIO_AF_UNIX_GETPEERPID (_WSAIOR(IOC_VENDOR, 256)): the
// pid at the other end of an AF_UNIX socket. x/sys v0.50.0 does not declare it.
const sioAFUnixGetPeerPID = 0x58000100

// tcpTableOwnerPIDAll is TCP_TABLE_OWNER_PID_ALL.
const tcpTableOwnerPIDAll = 5

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// connPeerPid names the process behind c: SIO_AF_UNIX_GETPEERPID for a Unix
// socket; for a loopback TCP connection, the owner of the row whose local end is
// c's remote end in the extended TCP table.
func connPeerPid(c net.Conn) (int, error) {
	switch c := c.(type) {
	case *net.UnixConn:
		raw, err := c.SyscallConn()
		if err != nil {
			return 0, err
		}
		var pid uint32
		var ierr error
		if err := raw.Control(func(fd uintptr) {
			var n uint32
			ierr = windows.WSAIoctl(windows.Handle(fd), sioAFUnixGetPeerPID, nil, 0,
				(*byte)(unsafe.Pointer(&pid)), uint32(unsafe.Sizeof(pid)), &n, nil, 0)
		}); err != nil {
			return 0, err
		}
		if ierr != nil {
			return 0, ierr
		}
		return int(pid), nil
	case *net.TCPConn:
		return loopbackPeer(c.LocalAddr(), c.RemoteAddr())
	}
	return 0, fmt.Errorf("a %T is neither a Unix socket nor a loopback TCP connection", c)
}

func loopbackPeer(local, remote net.Addr) (int, error) {
	l, err := netip.ParseAddrPort(local.String())
	if err != nil {
		return 0, err
	}
	r, err := netip.ParseAddrPort(remote.String())
	if err != nil {
		return 0, err
	}
	if !r.Addr().IsLoopback() {
		return 0, errors.New("the connection is not from this machine's loopback")
	}
	af, row := uint32(windows.AF_INET), 24
	if r.Addr().Unmap().Is6() {
		af, row = windows.AF_INET6, 56
	}
	buf, err := tcpTable(af)
	if err != nil {
		return 0, err
	}
	if len(buf) < 4 {
		return 0, errors.New("short TCP table")
	}
	n := int(binary.LittleEndian.Uint32(buf))
	for i := range n {
		e := buf[4+i*row:]
		if len(e) < row {
			break
		}
		var lo, re netip.AddrPort
		var pid uint32
		if af == windows.AF_INET {
			// MIB_TCPROW_OWNER_PID: state, local addr, local port, remote addr,
			// remote port, owning pid; addresses and ports in network order.
			lo = netip.AddrPortFrom(netip.AddrFrom4([4]byte(e[4:8])), binary.BigEndian.Uint16(e[8:10]))
			re = netip.AddrPortFrom(netip.AddrFrom4([4]byte(e[12:16])), binary.BigEndian.Uint16(e[16:18]))
			pid = binary.LittleEndian.Uint32(e[20:24])
		} else {
			// MIB_TCP6ROW_OWNER_PID: local addr, scope, port, remote addr, scope,
			// port, state, owning pid.
			lo = netip.AddrPortFrom(netip.AddrFrom16([16]byte(e[0:16])), binary.BigEndian.Uint16(e[20:22]))
			re = netip.AddrPortFrom(netip.AddrFrom16([16]byte(e[24:40])), binary.BigEndian.Uint16(e[44:46]))
			pid = binary.LittleEndian.Uint32(e[52:56])
		}
		if sameEnd(lo, r) && sameEnd(re, l) {
			return int(pid), nil
		}
	}
	return 0, errors.New("no local socket has the connection's other end")
}

func sameEnd(a, b netip.AddrPort) bool {
	return a.Port() == b.Port() && a.Addr().Unmap() == b.Addr().Unmap()
}

// tcpTable reads the extended TCP table of the address family.
func tcpTable(af uint32) ([]byte, error) {
	size := uint32(16 << 10)
	for range 4 {
		buf := make([]byte, size)
		r, _, _ := procGetExtendedTcpTable.Call(uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&size)), 0,
			uintptr(af), tcpTableOwnerPIDAll, 0)
		switch windows.Errno(r) {
		case 0:
			return buf[:size], nil
		case windows.ERROR_INSUFFICIENT_BUFFER:
			size += 4 << 10
			continue
		default:
			return nil, fmt.Errorf("GetExtendedTcpTable: %w", windows.Errno(r))
		}
	}
	return nil, errors.New("GetExtendedTcpTable: the table keeps growing")
}
