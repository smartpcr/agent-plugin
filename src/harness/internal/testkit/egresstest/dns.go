package egresstest

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The DNS values the in-memory server uses (RFC 1035 §4.1).
const (
	typeA    = 1
	typeAAAA = 28

	rcodeOK       = 0
	rcodeFormErr  = 1
	rcodeServFail = 2
	rcodeNXDomain = 3
)

// answer decides one DNS question and records it. A Host name gets its
// addresses of the asked family. An allowed name with no Host mapping, and an
// unlisted name, get SERVFAIL, which the resolver (StrictErrors) treats as final,
// so the search list is not walked. A name that is a listed name plus a search
// suffix the resolver appended gets NXDOMAIN silently, so the resolver goes on
// to the name itself; it is not recorded.
func (r *Recorder) answer(name string, qtype uint16) (int, []net.IP) {
	r.mu.Lock()
	ips, mapped := r.hosts[name]
	allowed := mapped || r.allowedName(name)
	if !allowed && r.searchVariant(name) {
		r.mu.Unlock()
		return rcodeNXDomain, nil
	}
	r.lookups = append(r.lookups, Lookup{Name: name, Type: typeName(qtype), Allowed: allowed})
	report := !allowed && !r.ended && !r.reported[name]
	if report {
		r.reported[name] = true
	}
	r.mu.Unlock()
	if report {
		r.t.Errorf("egresstest: unlisted name lookup %q", name)
	}
	if !mapped {
		return rcodeServFail, nil
	}
	var out []net.IP
	for _, ip := range ips {
		if v4 := ip.To4() != nil; v4 == (qtype == typeA) && (qtype == typeA || qtype == typeAAAA) {
			out = append(out, ip)
		}
	}
	return rcodeOK, out
}

// searchVariant reports whether name is a listed name with a suffix appended.
// Called with r.mu held.
func (r *Recorder) searchVariant(name string) bool {
	listed := make([]string, 0, len(r.hosts)+len(r.allow))
	for h := range r.hosts {
		listed = append(listed, h)
	}
	for _, d := range r.allow {
		listed = append(listed, canonical(destHost(d)))
	}
	for _, l := range listed {
		if l != "" && strings.HasPrefix(name, l+".") {
			return true
		}
	}
	return false
}

func typeName(t uint16) string {
	switch t {
	case typeA:
		return "A"
	case typeAAAA:
		return "AAAA"
	}
	return strconv.Itoa(int(t))
}

// dnsConn is one in-memory DNS exchange. It is not a net.PacketConn, so the
// resolver frames each message with a two-byte length, as over TCP; each whole
// query written is answered at once, and Read returns the answers.
type dnsConn struct {
	answer func(name string, qtype uint16) (int, []net.IP)

	mu      sync.Mutex
	in, out []byte
	closed  bool
}

func (c *dnsConn) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return 0, net.ErrClosed
	}
	c.in = append(c.in, p...)
	for len(c.in) >= 2 {
		n := int(binary.BigEndian.Uint16(c.in))
		if len(c.in) < 2+n {
			break
		}
		resp := c.reply(c.in[2 : 2+n])
		c.in = c.in[2+n:]
		c.out = binary.BigEndian.AppendUint16(c.out, uint16(len(resp)))
		c.out = append(c.out, resp...)
	}
	return len(p), nil
}

func (c *dnsConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.out) == 0 {
		return 0, io.EOF
	}
	n := copy(p, c.out)
	c.out = c.out[n:]
	return n, nil
}

func (c *dnsConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *dnsConn) LocalAddr() net.Addr              { return hostAddr("egresstest:0") }
func (c *dnsConn) RemoteAddr() net.Addr             { return hostAddr("egresstest:53") }
func (c *dnsConn) SetDeadline(time.Time) error      { return nil }
func (c *dnsConn) SetReadDeadline(time.Time) error  { return nil }
func (c *dnsConn) SetWriteDeadline(time.Time) error { return nil }

// reply answers one query message: its header and question echoed, then one
// A or AAAA record per address.
func (c *dnsConn) reply(q []byte) []byte {
	if len(q) < 12 {
		return header(nil, 0, rcodeFormErr, 0, false)
	}
	off := 12
	var labels []string
	for {
		if off >= len(q) {
			return header(q[:2], 0, rcodeFormErr, 0, false)
		}
		l := int(q[off])
		if l == 0 {
			off++
			break
		}
		if l&0xC0 != 0 || off+1+l > len(q) {
			return header(q[:2], 0, rcodeFormErr, 0, false)
		}
		labels = append(labels, string(q[off+1:off+1+l]))
		off += 1 + l
	}
	if off+4 > len(q) {
		return header(q[:2], 0, rcodeFormErr, 0, false)
	}
	qtype := binary.BigEndian.Uint16(q[off:])
	qend := off + 4
	rcode, ips := c.answer(canonical(strings.Join(labels, ".")), qtype)
	rd := q[2]&0x01 != 0
	out := header(q[:2], 1, rcode, len(ips), rd)
	out = append(out, q[12:qend]...)
	for _, ip := range ips {
		data := []byte(ip.To4())
		if qtype == typeAAAA {
			data = []byte(ip.To16())
		}
		out = append(out, 0xC0, 0x0C) // the name: a pointer to the question's
		out = binary.BigEndian.AppendUint16(out, qtype)
		out = binary.BigEndian.AppendUint16(out, 1) // class IN
		out = binary.BigEndian.AppendUint32(out, 60)
		out = binary.BigEndian.AppendUint16(out, uint16(len(data)))
		out = append(out, data...)
	}
	return out
}

// header is a response header: id, QR, AA, RA, RD as asked, rcode and counts.
func header(id []byte, qd, rcode, an int, rd bool) []byte {
	h := make([]byte, 12)
	copy(h, id)
	flags := uint16(0x8000 | 0x0400 | 0x0080 | rcode)
	if rd {
		flags |= 0x0100
	}
	binary.BigEndian.PutUint16(h[2:], flags)
	binary.BigEndian.PutUint16(h[4:], uint16(qd))
	binary.BigEndian.PutUint16(h[6:], uint16(an))
	return h
}
