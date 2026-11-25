//go:build linux

package subprocess

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// connPeerPid names the process behind c: SO_PEERCRED for a Unix socket; for a
// loopback TCP connection, the socket whose local end is c's remote end in
// /proc/net/tcp or tcp6, and the process holding that socket's inode.
func connPeerPid(c net.Conn) (int, error) {
	switch c := c.(type) {
	case *net.UnixConn:
		raw, err := c.SyscallConn()
		if err != nil {
			return 0, err
		}
		var cred *syscall.Ucred
		var cerr error
		if err := raw.Control(func(fd uintptr) {
			cred, cerr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		}); err != nil {
			return 0, err
		}
		if cerr != nil {
			return 0, cerr
		}
		return int(cred.Pid), nil
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
	for _, table := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		inode, ok := socketInode(table, r, l)
		if ok {
			return inodeOwner(inode)
		}
	}
	return 0, errors.New("no local socket has the connection's other end")
}

// socketInode finds the socket whose local address is from and remote is to.
func socketInode(table string, from, to netip.AddrPort) (string, bool) {
	b, err := os.ReadFile(table)
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) < 10 {
			continue
		}
		lo, ok1 := procAddr(f[1])
		re, ok2 := procAddr(f[2])
		if ok1 && ok2 && sameEnd(lo, from) && sameEnd(re, to) {
			return f[9], true
		}
	}
	return "", false
}

func sameEnd(a, b netip.AddrPort) bool {
	return a.Port() == b.Port() && a.Addr().Unmap() == b.Addr().Unmap()
}

// procAddr decodes an address of /proc/net/tcp: the address as 32-bit words in
// host (little-endian) order, a colon, the port in hexadecimal.
func procAddr(s string) (netip.AddrPort, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return netip.AddrPort{}, false
	}
	raw, err := hex.DecodeString(a)
	if err != nil || (len(raw) != 4 && len(raw) != 16) {
		return netip.AddrPort{}, false
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return netip.AddrPort{}, false
	}
	addr, _ := netip.AddrFromSlice(raw)
	return netip.AddrPortFrom(addr, uint16(port)), true
}

// inodeOwner finds a process holding the socket inode.
func inodeOwner(inode string) (int, error) {
	want := "socket:[" + inode + "]"
	procs, err := os.ReadDir("/proc")
	if err != nil {
		return 0, err
	}
	for _, d := range procs {
		pid, err := strconv.Atoi(d.Name())
		if err != nil {
			continue
		}
		dir := "/proc/" + d.Name() + "/fd"
		fds, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, fd := range fds {
			if link, err := os.Readlink(dir + "/" + fd.Name()); err == nil && link == want {
				return pid, nil
			}
		}
	}
	return 0, fmt.Errorf("no readable process holds socket inode %s", inode)
}
