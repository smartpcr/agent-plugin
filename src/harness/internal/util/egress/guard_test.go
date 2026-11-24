package egress_test

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// fakeResolver is a pure-Go resolver whose only server is in memory: it answers
// an A or AAAA query for a name of names with that address, an empty answer for
// the other family, and "no such host" for every other name. It never touches the
// network, so a test decides what a name resolves to.
func fakeResolver(names map[string]netip.Addr) *net.Resolver {
	return &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		client, server := net.Pipe()
		go serveDNS(server, names)
		return client, nil
	}}
}

// serveDNS answers length-prefixed DNS queries on c (the stream form, which the
// Go resolver uses on a connection that is not a PacketConn) until it closes.
func serveDNS(c net.Conn, names map[string]netip.Addr) {
	defer c.Close()
	for {
		var n [2]byte
		if _, err := io.ReadFull(c, n[:]); err != nil {
			return
		}
		q := make([]byte, binary.BigEndian.Uint16(n[:]))
		if _, err := io.ReadFull(c, q); err != nil {
			return
		}
		resp := dnsAnswer(q, names)
		if resp == nil {
			return
		}
		out := binary.BigEndian.AppendUint16(nil, uint16(len(resp)))
		if _, err := c.Write(append(out, resp...)); err != nil {
			return
		}
	}
}

// dnsAnswer builds the response to the one-question query q, or nil when q cannot
// be read.
func dnsAnswer(q []byte, names map[string]netip.Addr) []byte {
	if len(q) < 12 || binary.BigEndian.Uint16(q[4:]) != 1 {
		return nil
	}
	var labels []string
	i := 12
	for i < len(q) && q[i] != 0 {
		l := int(q[i])
		if i+1+l > len(q) {
			return nil
		}
		labels = append(labels, string(q[i+1:i+1+l]))
		i += 1 + l
	}
	if i+5 > len(q) {
		return nil
	}
	qtype := binary.BigEndian.Uint16(q[i+1:])
	question := q[12 : i+5]
	addr, known := names[strings.ToLower(strings.Join(labels, "."))]

	const typeA, typeAAAA = 1, 28
	var answer []byte
	if known && ((qtype == typeA && addr.Is4()) || (qtype == typeAAAA && addr.Is6())) {
		ip := addr.AsSlice()
		answer = append(answer, 0xC0, 12) // the name: a pointer to the question's
		answer = binary.BigEndian.AppendUint16(answer, qtype)
		answer = binary.BigEndian.AppendUint16(answer, 1) // class IN
		answer = binary.BigEndian.AppendUint32(answer, 60)
		answer = binary.BigEndian.AppendUint16(answer, uint16(len(ip)))
		answer = append(answer, ip...)
	}
	flags := uint16(0x8580) // response, authoritative, recursion desired and available
	if !known {
		flags |= 3 // NXDOMAIN
	}
	out := append([]byte{}, q[0:2]...)
	out = binary.BigEndian.AppendUint16(out, flags)
	out = binary.BigEndian.AppendUint16(out, 1)
	ancount := uint16(0)
	if answer != nil {
		ancount = 1
	}
	out = binary.BigEndian.AppendUint16(out, ancount)
	out = binary.BigEndian.AppendUint32(out, 0) // no authority, no additional
	out = append(out, question...)
	return append(out, answer...)
}

// recordingGuard delegates to g and records every address it was asked about.
type recordingGuard struct {
	g    egress.Guard
	mu   sync.Mutex
	seen []netip.AddrPort
}

func (r *recordingGuard) check(a netip.AddrPort) error {
	r.mu.Lock()
	r.seen = append(r.seen, a)
	r.mu.Unlock()
	return r.g(a)
}

func (r *recordingGuard) asked(a netip.AddrPort) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, s := range r.seen {
		if s == a {
			return true
		}
	}
	return false
}

// DH-T-UNDICI-7 (DH-LIB-UNDICI-R9): a guard denying private ranges refuses a
// literal 10.0.0.1, a name the injected resolver resolves to 10.0.0.1, and a
// redirect from a loopback listener to 10.0.0.1, each with egress/not-permitted
// before anything is sent; the loopback listener, which the policy allows, and a
// name resolving to it stay reachable, so the refusals are the guard's and not the
// resolver's. With loopback denied too, the same listener, by literal and by name,
// is refused and receives no connection at all: the guard runs before the dial.
//
//dsh:test DH-T-UNDICI-7
func TestGuardRefusesPrivateBeforeDial(t *testing.T) {
	srv := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			// Port 9, so that only the name case asks the guard about 10.0.0.1 on the
			// listener's port.
			http.Redirect(w, r, "http://10.0.0.1:9/inside", http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "ok")
	})
	srvPort := port(t, strings.TrimPrefix(srv.URL, "http://"))
	resolver := fakeResolver(map[string]netip.Addr{
		"inside.example": netip.MustParseAddr("10.0.0.1"),
		"loop.example":   netip.MustParseAddr("127.0.0.1"),
	})
	svc := egress.New(egress.Config{Resolver: resolver, DialTimeout: 5 * time.Second})
	t.Cleanup(svc.CloseIdleConnections)
	follow := func(*http.Request, []*http.Request) error { return nil }

	t.Run("loopback allowed", func(t *testing.T) {
		guard := &recordingGuard{g: egress.DenyPrivate(egress.PrivatePolicy{AllowLoopback: true})}
		client := func(dest string) *http.Client {
			c, err := svc.Client("web-fetch", permit.New(t, "web-fetch", dest), egress.Options{Guard: guard.check, Redirect: follow})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { svc.Release(c) })
			return c
		}

		for _, tc := range []struct{ name, dest, target string }{
			{"the listener", srv.URL, srv.URL + "/"},
			{"a name resolving to the listener", "loop.example", "http://loop.example:" + srvPort + "/"},
		} {
			resp, err := client(tc.dest).Get(tc.target)
			if err != nil {
				t.Fatalf("%s: %v; want it reachable under AllowLoopback", tc.name, err)
			}
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			if string(b) != "ok" {
				t.Fatalf("%s: body %q, want ok", tc.name, b)
			}
		}
		if !guard.asked(netip.MustParseAddrPort("127.0.0.1:" + srvPort)) {
			t.Fatalf("the guard saw %v; want the resolved 127.0.0.1:%s", guard.seen, srvPort)
		}

		requests := srv.requests.Load()
		for _, tc := range []struct{ name, dest, target string }{
			{"a literal address", "10.0.0.1", "http://10.0.0.1/x"},
			{"a name resolving to one", "inside.example", "http://inside.example:" + srvPort + "/x"},
			{"a redirect to one", srv.URL, srv.URL + "/redirect"},
		} {
			start := time.Now()
			_, err := client(tc.dest).Get(tc.target)
			wantNotPermitted(t, err)
			if !strings.Contains(err.Error(), "10.0.0.1") || !strings.Contains(err.Error(), "private address") {
				t.Fatalf("%s: %v; want the guard's refusal of 10.0.0.1", tc.name, err)
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("%s: refused after %v; want it refused before any connection attempt", tc.name, d)
			}
		}
		// The name was refused on its resolved address, in the dialler's hook; the
		// literal (port 80) and the redirect (port 9) were refused before it.
		for _, want := range []string{"10.0.0.1:" + srvPort, "10.0.0.1:80", "10.0.0.1:9"} {
			if !guard.asked(netip.MustParseAddrPort(want)) {
				t.Fatalf("the guard saw %v; want %s among them", guard.seen, want)
			}
		}
		if got := srv.requests.Load() - requests; got != 1 {
			t.Fatalf("the listener got %d requests; want only the one that redirected", got)
		}
	})

	t.Run("loopback denied", func(t *testing.T) {
		conns, requests := srv.conns.Load(), srv.requests.Load()
		for _, tc := range []struct{ name, dest, target string }{
			{"a literal loopback address", srv.URL, srv.URL + "/"},
			{"a name resolving to loopback", "loop.example", "http://loop.example:" + srvPort + "/"},
		} {
			c, err := svc.Client("web-fetch", permit.New(t, "web-fetch", tc.dest), egress.Options{Guard: egress.DenyPrivate(egress.PrivatePolicy{})})
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.Get(tc.target)
			svc.Release(c)
			wantNotPermitted(t, err)
			if !strings.Contains(err.Error(), "loopback address") {
				t.Fatalf("%s: %v; want the guard's loopback refusal", tc.name, err)
			}
		}
		if srv.conns.Load() != conns || srv.requests.Load() != requests {
			t.Fatalf("connections %d -> %d, requests %d -> %d: a refused address was dialled",
				conns, srv.conns.Load(), requests, srv.requests.Load())
		}
	})
}

// DenyPrivate judges each range as its comment says; an IPv4-mapped address is
// judged as the IPv4 address, and loopback follows the policy.
//
//dsh:test DH-T-UNDICI-7
func TestDenyPrivateRanges(t *testing.T) {
	deny, allowLoop := egress.DenyPrivate(egress.PrivatePolicy{}), egress.DenyPrivate(egress.PrivatePolicy{AllowLoopback: true})
	for _, tc := range []struct {
		addr             string
		refused, refLoop bool // refused by deny, by allowLoop
	}{
		{"10.0.0.1", true, true},
		{"172.16.5.4", true, true},
		{"192.168.1.1", true, true},
		{"100.64.0.1", true, true},
		{"169.254.169.254", true, true},
		{"0.0.0.0", true, true},
		{"0.1.2.3", true, true},
		{"224.0.0.1", true, true},
		{"fc00::1", true, true},
		{"fe80::1", true, true},
		{"::", true, true},
		{"::ffff:10.0.0.1", true, true},
		{"127.0.0.1", true, false},
		{"127.8.9.10", true, false},
		{"::1", true, false},
		{"8.8.8.8", false, false},
		{"172.32.0.1", false, false},
		{"2001:4860:4860::8888", false, false},
	} {
		ap := netip.AddrPortFrom(netip.MustParseAddr(tc.addr), 443)
		if err := deny(ap); (err != nil) != tc.refused {
			t.Errorf("DenyPrivate(%s) = %v, want refused %v", tc.addr, err, tc.refused)
		} else if err != nil && !egress.IsNotPermitted(err) {
			t.Errorf("DenyPrivate(%s) = %v, want egress/not-permitted", tc.addr, err)
		}
		if err := allowLoop(ap); (err != nil) != tc.refLoop {
			t.Errorf("DenyPrivate(AllowLoopback)(%s) = %v, want refused %v", tc.addr, err, tc.refLoop)
		}
	}
	if err := deny(netip.AddrPort{}); err == nil {
		t.Error("DenyPrivate(zero) = nil, want refused")
	}
	var guardErr interface{ Reason() string }
	if !errors.As(deny(netip.MustParseAddrPort("10.0.0.1:80")), &guardErr) {
		t.Error("the refusal is not a harness error")
	}
}

func port(t *testing.T, hostport string) string {
	t.Helper()
	u, err := url.Parse("http://" + hostport)
	if err != nil || u.Port() == "" {
		t.Fatalf("no port in %q", hostport)
	}
	return u.Port()
}
