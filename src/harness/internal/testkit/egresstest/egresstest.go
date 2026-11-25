// Package egresstest is the test kit's recording egress (DH-D-UTIL-6,
// util-test-support.md §6.1 "egresstest"): a real util/egress service, provided
// under egress.Key like the production one, whose every outbound request and
// every name lookup is recorded and checked against what the test allowed.
//
// The service is egress.New over two hooks the egress Config already has, so
// the permit check, the one header, the release rules and the real
// net/http.Transport all stay the production ones:
//
//   - Config.Observe is called by every client the service hands out, guarded
//     (Options.Guard) or not, for every request after the permit check and
//     before any lookup or dial. The recorder records the request there, refuses
//     one no Allow entry names (the test fails, naming the origin, and nothing
//     is dialled), and returns a scripted dial failure (Fail) for a chosen
//     attempt -- refused, reset or timeout -- deterministically and with no
//     listener.
//   - Config.Resolver is a pure-Go resolver whose DNS exchange is answered in
//     memory: a name given by Host resolves to its addresses, a name the test
//     did not allow fails the test, and no packet leaves the process.
//
// A test allows destinations in the syntax of egress.Allows
// ("https://api.example.test", "127.0.0.1:8080", "example.test"); a loopback
// server such as faultserver is allowed by its URL. Every refusal and every
// scripted failure that never happened is reported through the test's TB, so
// Errorf is the whole failure surface; nothing is reported after the test ends.
//
// A guarded client's guard runs on both sides of the recording. A literal
// address in the URL is judged by the guard before Observe, so a refused
// private literal fails with egress/not-permitted and is not recorded: it was
// never a dial attempt. A host name is recorded, and given its scripted
// failure, before it is resolved; the guard then judges the address it resolves
// to in the dialler, so a name the test resolves (Host) to a refused address is
// recorded and then refused by the guard.
//
// What it does not see: a client built outside util/egress (the archtest rule
// and DH-T-UTIL-22 cover that), and a request a test never exercises.
package egresstest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// Fault is a scripted dial failure.
type Fault int

// The scripted dial failures.
const (
	// None is no failure: the request goes on to the real transport.
	None Fault = iota
	// Refused is a connection refused: no listener at the address.
	Refused
	// Reset is a connection reset by the peer.
	Reset
	// Timeout is a dial that timed out; the error's Timeout() is true.
	Timeout
)

func (f Fault) String() string {
	switch f {
	case None:
		return "none"
	case Refused:
		return "refused"
	case Reset:
		return "reset"
	case Timeout:
		return "timeout"
	}
	return fmt.Sprintf("fault(%d)", int(f))
}

// ErrUnlisted matches, with errors.Is, the refusal of a request or a name lookup
// the test did not allow.
var ErrUnlisted = errors.New("egresstest: not allowed by the test")

// Request is one request the transport was about to send.
type Request struct {
	Method string
	URL    string
	Header http.Header
	// Allowed is whether an Allow entry named its origin.
	Allowed bool
	// Fault is the scripted failure it was given, None when it went on.
	Fault Fault
}

// Lookup is one DNS question the resolver was asked.
type Lookup struct {
	Name    string // lower case, without the trailing dot
	Type    string // "A", "AAAA" or the numeric type
	Allowed bool
}

// rule is one scripted failure: the attempt-th request dest allows gets fault.
type rule struct {
	dest    string
	attempt int
	fault   Fault
	seen    int
	used    bool
}

// Recorder records and checks one test's egress.
type Recorder struct {
	t testing.TB

	mu       sync.Mutex
	ended    bool // the test has ended: nothing more is reported
	allow    []string
	hosts    map[string][]net.IP
	rules    []*rule
	requests []Request
	lookups  []Lookup
	reported map[string]bool // unlisted names already reported
	service  *egress.Service
	resolver *net.Resolver
}

// New is a recorder that allows the destinations allow, checked when the test
// ends.
func New(t testing.TB, allow ...string) *Recorder {
	t.Helper()
	r := &Recorder{t: t, allow: append([]string(nil), allow...), hosts: map[string][]net.IP{}, reported: map[string]bool{}}
	r.resolver = &net.Resolver{PreferGo: true, StrictErrors: true, Dial: r.dialDNS}
	t.Cleanup(r.end)
	return r
}

// Allow allows more destinations, in the syntax of egress.Allows.
func (r *Recorder) Allow(dest ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allow = append(r.allow, dest...)
}

// Host makes name resolve to addrs (IP literals) and allows its lookup. It does
// not allow a request to it: Allow does.
func (r *Recorder) Host(name string, addrs ...string) {
	r.t.Helper()
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			r.t.Fatalf("egresstest: Host(%q): %q is not an IP address", name, a)
		}
		ips = append(ips, ip)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hosts[canonical(name)] = ips
}

// Fail scripts fault for the attempt-th request (counted from 1) that dest
// allows, and allows dest. A scripted failure that never happens fails the test
// when it ends, as an unused script does.
func (r *Recorder) Fail(dest string, attempt int, fault Fault) {
	r.t.Helper()
	if attempt < 1 || fault == None {
		r.t.Fatalf("egresstest: Fail(%q, %d, %s): want an attempt from 1 and a fault", dest, attempt, fault)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.allow = append(r.allow, dest)
	r.rules = append(r.rules, &rule{dest: dest, attempt: attempt, fault: fault})
}

// Requests is every request recorded so far, in order.
func (r *Recorder) Requests() []Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Request, len(r.requests))
	for i, q := range r.requests {
		q.Header = q.Header.Clone()
		out[i] = q
	}
	return out
}

// Lookups is every DNS question recorded so far, in order. The resolver asks
// for A and AAAA separately, so one name lookup is usually two entries.
func (r *Recorder) Lookups() []Lookup {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Lookup(nil), r.lookups...)
}

// Resolver is the recording resolver, for a component that resolves names
// itself rather than through a client.
func (r *Recorder) Resolver() *net.Resolver { return r.resolver }

// Config is base with the recorder's hooks: its Observe records and checks each
// request, guarded clients' included, before base's Observe (if any), and its
// Resolver is the recording one. Other fields, Proxy among them, are base's.
// Extra certificate authorities are loaded only by egress.Resolve, so a service
// built from this Config trusts the system's.
func (r *Recorder) Config(base egress.Config) egress.Config {
	next := base.Observe
	base.Observe = func(req *http.Request) error {
		if err := r.check(req); err != nil {
			return err
		}
		if next != nil {
			return next(req)
		}
		return nil
	}
	base.Resolver = r.resolver
	return base
}

// Service is the recording egress service, built once: egress.New over Config
// of a zero egress.Config.
func (r *Recorder) Service() *egress.Service {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.service == nil {
		r.service = egress.New(r.Config(egress.Config{}))
	}
	return r.service
}

// Provide provides Service under egress.Key on ctx, for a test that builds its
// tree by hand.
func (r *Recorder) Provide(ctx *cordis.Context) error {
	_, err := egress.Key.Provide(ctx, r.Service())
	return err
}

// Component is a stand-in for egress.Component under the same catalogue name:
// each load builds a recording service from the egress.Config it is given (its
// Observe is chained after the recorder's check), provides it under egress.Key,
// and closes its idle connections when the fiber unloads. A composition test
// registers it in place of the production entry.
func (r *Recorder) Component() *cordis.Component {
	return cordis.Define(egress.Name, func(ctx *cordis.Context, c egress.Config) error {
		s := egress.New(r.Config(c))
		if _, err := ctx.Effect("egresstest service", func() (cordis.Disposer, error) {
			return func() error { s.CloseIdleConnections(); return nil }, nil
		}); err != nil {
			return err
		}
		_, err := egress.Key.Provide(ctx, s)
		return err
	})
}

// check records req and decides it: nil lets it go on to the transport.
func (r *Recorder) check(req *http.Request) error {
	u := req.URL
	r.mu.Lock()
	rec := Request{Method: req.Method, URL: u.String(), Header: req.Header.Clone()}
	for _, d := range r.allow {
		if egress.Allows(d, u) {
			rec.Allowed = true
			break
		}
	}
	if rec.Allowed {
		for _, ru := range r.rules {
			if !egress.Allows(ru.dest, u) {
				continue
			}
			ru.seen++
			if ru.seen == ru.attempt && rec.Fault == None {
				ru.used = true
				rec.Fault = ru.fault
			}
		}
	}
	r.requests = append(r.requests, rec)
	report := !rec.Allowed && !r.ended
	r.mu.Unlock()
	if !rec.Allowed {
		if report {
			r.t.Errorf("egresstest: unlisted request %s %s to %s", req.Method, redacted(u), origin(u))
		}
		return fmt.Errorf("egresstest: %s %s: %w", req.Method, origin(u), ErrUnlisted)
	}
	return faultError(rec.Fault, u)
}

// end is the recorder's cleanup: every scripted failure must have happened.
func (r *Recorder) end() {
	r.mu.Lock()
	r.ended = true
	var left []string
	for _, ru := range r.rules {
		if !ru.used {
			left = append(left, fmt.Sprintf("%s on attempt %d to %s (saw %d)", ru.fault, ru.attempt, ru.dest, ru.seen))
		}
	}
	r.mu.Unlock()
	if len(left) > 0 {
		r.t.Errorf("egresstest: scripted failure(s) never happened: %s", strings.Join(left, "; "))
	}
}

// faultError is the error the transport returns for f, shaped as the dialler's
// own: a *net.OpError wrapping the system error.
func faultError(f Fault, u *url.URL) error {
	addr := hostAddr(net.JoinHostPort(u.Hostname(), portOf(u)))
	switch f {
	case Refused:
		return &net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: os.NewSyscallError("connect", syscall.ECONNREFUSED)}
	case Reset:
		return &net.OpError{Op: "read", Net: "tcp", Addr: addr, Err: os.NewSyscallError("read", syscall.ECONNRESET)}
	case Timeout:
		return &net.OpError{Op: "dial", Net: "tcp", Addr: addr, Err: os.ErrDeadlineExceeded}
	}
	return nil
}

// hostAddr is a net.Addr naming the host and port a request was for.
type hostAddr string

func (a hostAddr) Network() string { return "tcp" }
func (a hostAddr) String() string  { return string(a) }

func origin(u *url.URL) string { return u.Scheme + "://" + u.Host }

// redacted is u without its user information or query, which may hold secrets.
func redacted(u *url.URL) string {
	c := *u
	c.User, c.RawQuery, c.Fragment = nil, "", ""
	return c.String()
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if strings.EqualFold(u.Scheme, "https") {
		return "443"
	}
	return "80"
}

// canonical is a DNS name in lower case without its trailing dot.
func canonical(name string) string { return strings.TrimSuffix(strings.ToLower(name), ".") }

// allowedName reports whether name is the host of an Allow entry. Called with
// r.mu held.
func (r *Recorder) allowedName(name string) bool {
	for _, d := range r.allow {
		if canonical(destHost(d)) == name {
			return true
		}
	}
	return false
}

// destHost is the host an egress.Allows destination names.
func destHost(d string) string {
	if strings.Contains(d, "://") {
		if u, err := url.Parse(d); err == nil {
			return u.Hostname()
		}
		return ""
	}
	if h, _, err := net.SplitHostPort(d); err == nil {
		return h
	}
	return strings.Trim(d, "[]")
}

// dialDNS is the resolver's dialler: an in-memory DNS server, never a socket.
func (r *Recorder) dialDNS(context.Context, string, string) (net.Conn, error) {
	return &dnsConn{answer: r.answer}, nil
}
