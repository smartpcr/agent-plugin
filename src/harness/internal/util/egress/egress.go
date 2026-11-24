// Package egress is the one client every outbound connection is made through
// (DH-D-UTIL-2, docs/harness/areas/util-test-support.md §6.1 "egress").
//
// A client is handed out only to the holder of a permit for one data kind
// (internal/host/sharing gives them; this package names only the one-interface
// shape, so util imports no host package). Every request of a client is checked
// against its permit before anything is dialled: no permit, a permit for another
// kind, a destination the permit does not name, or a permit whose consent was
// withdrawn fails with egress/not-permitted and opens no connection. A request in
// flight runs under a context joined with the permit's, so withdrawing consent
// cancels it.
//
// What a client sends is the request its caller built plus one header,
// `User-Agent: dsh`, set after every other header. It keeps no cookie jar, follows
// no redirect unless its caller says so, and never reads the process environment:
// Resolve reads the proxy names through the launch lookup boot gives it (the
// trusted view, so a project file never supplies a proxy) and the extra
// certificate authorities of Config.ExtraCACerts. ChildEnv hands the same proxy
// names to a child process, by audience; Options.Guard (DenyPrivate) refuses
// destination addresses before anything is dialled.
//
// A client belongs to its asker: it is registered until the asker releases it
// (Service.Release), the asker's Options.Owner context ends (the fiber that asked
// unloads), or its permit is withdrawn -- whichever comes first.
package egress

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// ReasonNotPermitted is the failure of a request made without a matching permit.
const ReasonNotPermitted herr.Reason = "egress/not-permitted"

// ErrNotPermitted matches every egress/not-permitted error with errors.Is.
var ErrNotPermitted = herr.New(ReasonNotPermitted, "")

// UserAgent is the one header a client adds to every request.
const UserAgent = "dsh"

// Permit is a permit for one data kind to one destination; sharing.Permit
// satisfies it. Its context ends when consent is withdrawn.
type Permit interface {
	Kind() string
	Destination() string
	Context() context.Context
}

// Config configures the service once, at launch (`egress.Config`, not live).
type Config struct {
	// ExtraCACerts are PEM files of certificate authorities trusted in addition to
	// the system's (an intercepting proxy's authority). An unreadable file, or one
	// holding no certificate, stops the launch with egress/ca-unreadable.
	ExtraCACerts []string `json:"extraCACerts,omitempty"`
	// Proxy chooses the proxy for a request; nil connects directly. Resolve sets
	// it from the launch's proxy names.
	Proxy func(*http.Request) (*url.URL, error) `json:"-"`
	// Resolver is the service's name resolver; nil is a fresh pure-Go resolver.
	Resolver *net.Resolver `json:"-"`
	// DialTimeout bounds one connection attempt; zero is 30 s.
	DialTimeout time.Duration `json:"-"`

	// roots is the pool Resolve builds from ExtraCACerts; nil uses the system's.
	roots *x509.CertPool
	// child is the launch's proxy names as ChildEnv overlays them; Resolve reads it.
	child childProxy
}

// Options configure one client.
type Options struct {
	// Label names the client in Handed.
	Label string
	// Redirect decides on a redirect; nil follows none and returns the 3xx response.
	Redirect func(req *http.Request, via []*http.Request) error
	// HeaderWait bounds the wait for response headers; zero waits for the context.
	HeaderWait time.Duration
	// Owner is the asker's lifetime (its fiber's Lifetime): when it ends the client
	// is released. nil leaves the release to Service.Release or the permit.
	Owner context.Context
	// Guard refuses destination addresses (DenyPrivate is the usual one); nil
	// allows every address. It is checked on a literal address in the URL of each
	// request and redirect before anything is sent, and in the dialler's Control
	// hook on every resolved address before it is connected to. A guarded client
	// connects directly, never through the launch's proxy, so that the guard sees
	// the final address; whether a guarded client may use a proxy at all is the
	// owning tool's decision (docs/harness/libs/undici.md §7). A guarded client has
	// a transport of its own.
	Guard Guard
}

// ClientInfo is one live client as Handed lists it.
type ClientInfo struct {
	Kind        string `json:"kind"`
	Destination string `json:"destination"`
	Label       string `json:"label,omitempty"`
}

// Service builds every outbound client. Clients with equal options share a
// transport.
type Service struct {
	cfg Config

	mu         sync.Mutex
	transports map[time.Duration]*http.Transport
	handed     map[*permitted]*record
	// users counts the live clients of each transport; when the last one is
	// released the transport's idle connections are closed (DH-LIB-UNDICI-R12).
	users map[*http.Transport]int
	// omitted are the proxy names ChildEnv has reported omitting for ModelRun.
	omitted map[string]bool
}

// record is one registered client: what Handed lists, and the callbacks on the
// permit and the owner that release it, stopped once it is released.
type record struct {
	info  ClientInfo
	stops []func() bool
}

// New returns the service.
func New(c Config) *Service {
	if c.Resolver == nil {
		c.Resolver = &net.Resolver{PreferGo: true}
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 30 * time.Second
	}
	return &Service{cfg: c, transports: map[time.Duration]*http.Transport{}, handed: map[*permitted]*record{},
		users: map[*http.Transport]int{}, omitted: map[string]bool{}}
}

// Client returns a client for kind under p. Without a permit, with a permit for
// another kind, or with one already withdrawn it fails with egress/not-permitted.
// The client is listed by Handed until it is released: by Release, by the end of
// o.Owner, or by the end of the permit's context. A released client refuses every
// request with egress/not-permitted; releasing the last client of a transport
// closes its idle connections, while a request already in flight completes.
func (s *Service) Client(kind string, p Permit, o Options) (*http.Client, error) {
	if err := usable(kind, p); err != nil {
		return nil, err
	}
	if o.Owner != nil && o.Owner.Err() != nil {
		return nil, herr.Newf(ReasonNotPermitted, "the asker of a %s client has ended", kind)
	}
	tr := s.transport(o.HeaderWait, o.Guard)
	rt := &permitted{kind: kind, permit: p, owner: o.Owner, guard: o.Guard, next: tr, tr: tr}
	redirect := o.Redirect
	if redirect == nil {
		redirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	rec := &record{info: ClientInfo{Kind: kind, Destination: p.Destination(), Label: o.Label}}
	s.mu.Lock()
	s.handed[rt] = rec
	s.users[tr]++
	s.mu.Unlock()
	drop := func() { s.release(rt) }
	stops := []func() bool{context.AfterFunc(p.Context(), drop)}
	if o.Owner != nil {
		stops = append(stops, context.AfterFunc(o.Owner, drop))
	}
	s.mu.Lock()
	if _, live := s.handed[rt]; live {
		rec.stops, stops = stops, nil
	}
	s.mu.Unlock()
	// Released while registering: nothing holds the callbacks any more.
	for _, stop := range stops {
		stop()
	}
	return &http.Client{Transport: rt, CheckRedirect: redirect}, nil
}

// Over is a client over rt that follows no redirect and checks no permit: the
// seam under llm/transport.Over for the test kit's fake transport and recorded
// fixtures, so that no HTTP client is built outside this package (DH-T-UNDICI-8).
// It is not listed by Handed. The archtest seam rule allows its use only from
// _test.go files, internal/testkit and internal/llm/transport (DH-T-HOST-19).
func Over(rt http.RoundTripper) *http.Client {
	return &http.Client{Transport: rt, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

// Release releases c: it is no longer listed, its callbacks on the permit and the
// owner are stopped, and every later request fails with egress/not-permitted.
// When c was the last live client of its transport, the transport's idle
// connections are closed; a request already in flight completes.
// Releasing a client twice, or one this service did not build, does nothing.
func (s *Service) Release(c *http.Client) {
	if c == nil {
		return
	}
	if rt, ok := c.Transport.(*permitted); ok {
		s.release(rt)
	}
}

func (s *Service) release(rt *permitted) {
	rt.released.Store(true)
	s.mu.Lock()
	rec, ok := s.handed[rt]
	delete(s.handed, rt)
	var idle *http.Transport
	if ok {
		if s.users[rt.tr]--; s.users[rt.tr] <= 0 {
			delete(s.users, rt.tr)
			idle = rt.tr
		}
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	for _, stop := range rec.stops {
		stop()
	}
	if idle != nil {
		idle.CloseIdleConnections()
	}
}

// Handed lists every live client: its kind, destination and label. A client whose
// permit was withdrawn, or that was released, is not live.
func (s *Service) Handed() []ClientInfo {
	s.mu.Lock()
	var gone []*permitted
	out := make([]ClientInfo, 0, len(s.handed))
	for rt, rec := range s.handed {
		if rt.permit.Context().Err() != nil || rt.ownerEnded() {
			gone = append(gone, rt)
			continue
		}
		out = append(out, rec.info)
	}
	s.mu.Unlock()
	for _, rt := range gone {
		s.release(rt)
	}
	return out
}

// CloseIdleConnections closes the idle connections of every transport.
func (s *Service) CloseIdleConnections() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.transports {
		t.CloseIdleConnections()
	}
	for t := range s.users {
		t.CloseIdleConnections()
	}
}

// transport is the shared transport for headerWait, or, for a guarded client, a
// transport of its own that dials through the guard and uses no proxy.
func (s *Service) transport(headerWait time.Duration, guard Guard) *http.Transport {
	if guard != nil {
		return s.newTransport(headerWait, nil, guard)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.transports[headerWait]; ok {
		return t
	}
	t := s.newTransport(headerWait, s.cfg.Proxy, nil)
	s.transports[headerWait] = t
	return t
}

func (s *Service) newTransport(headerWait time.Duration, proxy func(*http.Request) (*url.URL, error), guard Guard) *http.Transport {
	d := &net.Dialer{Timeout: s.cfg.DialTimeout, Resolver: s.cfg.Resolver}
	if guard != nil {
		d.Control = control(guard)
	}
	t := &http.Transport{
		Proxy:                 proxy,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: headerWait,
	}
	if s.cfg.roots != nil {
		t.TLSClientConfig = &tls.Config{RootCAs: s.cfg.roots, MinVersion: tls.VersionTLS12}
	}
	return t
}

// usable checks that p is a live permit for kind.
func usable(kind string, p Permit) error {
	if p == nil {
		return herr.Newf(ReasonNotPermitted, "no permit for %s", kind)
	}
	if p.Kind() != kind {
		return herr.Newf(ReasonNotPermitted, "the permit is for %s, not %s", p.Kind(), kind)
	}
	if err := p.Context().Err(); err != nil {
		// The cause is printed, not chained, so the innermost reason stays ours.
		return herr.Newf(ReasonNotPermitted, "the permit for %s was withdrawn: %v", kind, context.Cause(p.Context()))
	}
	return nil
}

// permitted is the round tripper of one client: the permit check, the one header,
// and the permit's context joined with each request's.
type permitted struct {
	kind     string
	permit   Permit
	owner    context.Context
	guard    Guard
	next     http.RoundTripper
	tr       *http.Transport
	released atomic.Bool
}

func (t *permitted) ownerEnded() bool { return t.owner != nil && t.owner.Err() != nil }

func (t *permitted) RoundTrip(req *http.Request) (*http.Response, error) {
	if t.released.Load() || t.ownerEnded() {
		closeBody(req)
		return nil, herr.Newf(ReasonNotPermitted, "the %s client was released", t.kind)
	}
	if err := usable(t.kind, t.permit); err != nil {
		closeBody(req)
		return nil, err
	}
	// A literal address is guarded before the permit's destination is compared, so
	// a redirect to a refused address reads as the guard's refusal.
	if err := guardLiteral(t.guard, req.URL); err != nil {
		closeBody(req)
		return nil, err
	}
	if !Allows(t.permit.Destination(), req.URL) {
		closeBody(req)
		return nil, herr.Newf(ReasonNotPermitted, "the permit for %s does not name %s", t.kind, origin(req.URL))
	}
	ctx, cancel := context.WithCancelCause(req.Context())
	pctx := t.permit.Context()
	stop := context.AfterFunc(pctx, func() { cancel(context.Cause(pctx)) })
	out := req.Clone(ctx)
	out.Header.Set("User-Agent", UserAgent)
	resp, err := t.next.RoundTrip(out)
	if err != nil {
		stop()
		withdrawn := pctx.Err() != nil
		cancel(nil)
		if withdrawn {
			// The transport error chains the permit's cause (sharing/withdrawn); it is
			// printed, not chained, so herr.Kind and the exit status read ours.
			return nil, herr.Newf(ReasonNotPermitted, "the permit for %s was withdrawn in flight: %v", t.kind, err)
		}
		return nil, err
	}
	resp.Body = &releaseBody{ReadCloser: resp.Body, kind: t.kind, pctx: pctx, release: func() { stop(); cancel(nil) }}
	return resp, nil
}

// releaseBody ends the request's joined context when the body is closed, and
// reports a read cut short by a withdrawn permit as egress/not-permitted, the
// same as a withdrawal before the response arrived.
type releaseBody struct {
	io.ReadCloser
	kind    string
	pctx    context.Context
	once    sync.Once
	release func()
}

func (b *releaseBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if err != nil && err != io.EOF && b.pctx.Err() != nil {
		// As in RoundTrip: the cause is printed, not chained, so herr.Kind and the
		// exit status read egress/not-permitted rather than sharing/withdrawn.
		return n, herr.Newf(ReasonNotPermitted, "the permit for %s was withdrawn in flight: %v", b.kind, err)
	}
	return n, err
}

func (b *releaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func closeBody(req *http.Request) {
	if req.Body != nil {
		_ = req.Body.Close()
	}
}

// Allows reports whether destination names u's origin. A destination with a
// scheme ("https://api.example.com/v1") must match u's scheme, host and port;
// a bare "host:port" must match u's host and port; a bare host matches any port.
// Host names compare case-insensitively. An empty destination allows nothing.
func Allows(destination string, u *url.URL) bool {
	if destination == "" || u == nil || u.Host == "" {
		return false
	}
	if strings.Contains(destination, "://") {
		d, err := url.Parse(destination)
		if err != nil || d.Host == "" {
			return false
		}
		return strings.EqualFold(d.Scheme, u.Scheme) && hostPort(d) == hostPort(u)
	}
	if host, port, err := net.SplitHostPort(destination); err == nil {
		return strings.EqualFold(host, u.Hostname()) && port == portOf(u)
	}
	return strings.EqualFold(strings.Trim(destination, "[]"), u.Hostname())
}

func hostPort(u *url.URL) string {
	return strings.ToLower(net.JoinHostPort(u.Hostname(), portOf(u)))
}

func portOf(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "wss":
		return "443"
	case "http", "ws":
		return "80"
	}
	return ""
}

func origin(u *url.URL) string {
	if u == nil {
		return "<nil>"
	}
	return u.Scheme + "://" + u.Host
}

// IsNotPermitted reports whether err's chain holds egress/not-permitted.
func IsNotPermitted(err error) bool { return errors.Is(err, ErrNotPermitted) }
