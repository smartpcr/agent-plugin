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
// no proxy is used until the launch's proxy policy is resolved into Config.
package egress

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
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

// Config configures the service once, before any component asks for a client.
type Config struct {
	// Proxy chooses the proxy for a request; nil connects directly.
	Proxy func(*http.Request) (*url.URL, error)
	// Resolver is the service's name resolver; nil is a fresh pure-Go resolver.
	Resolver *net.Resolver
	// DialTimeout bounds one connection attempt; zero is 30 s.
	DialTimeout time.Duration
}

// Options configure one client.
type Options struct {
	// Label names the client in Handed.
	Label string
	// Redirect decides on a redirect; nil follows none and returns the 3xx response.
	Redirect func(req *http.Request, via []*http.Request) error
	// HeaderWait bounds the wait for response headers; zero waits for the context.
	HeaderWait time.Duration
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
	handed     map[*permitted]ClientInfo
}

// New returns the service.
func New(c Config) *Service {
	if c.Resolver == nil {
		c.Resolver = &net.Resolver{PreferGo: true}
	}
	if c.DialTimeout <= 0 {
		c.DialTimeout = 30 * time.Second
	}
	return &Service{cfg: c, transports: map[time.Duration]*http.Transport{}, handed: map[*permitted]ClientInfo{}}
}

// Client returns a client for kind under p. Without a permit, with a permit for
// another kind, or with one already withdrawn it fails with egress/not-permitted.
// The client is listed by Handed until the permit's context ends.
func (s *Service) Client(kind string, p Permit, o Options) (*http.Client, error) {
	if err := usable(kind, p); err != nil {
		return nil, err
	}
	rt := &permitted{kind: kind, permit: p, next: s.transport(o.HeaderWait)}
	redirect := o.Redirect
	if redirect == nil {
		redirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	s.mu.Lock()
	s.handed[rt] = ClientInfo{Kind: kind, Destination: p.Destination(), Label: o.Label}
	s.mu.Unlock()
	context.AfterFunc(p.Context(), func() {
		s.mu.Lock()
		delete(s.handed, rt)
		s.mu.Unlock()
	})
	return &http.Client{Transport: rt, CheckRedirect: redirect}, nil
}

// Handed lists every live client: its kind, destination and label. A client whose
// permit was withdrawn is not live.
func (s *Service) Handed() []ClientInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]ClientInfo, 0, len(s.handed))
	for rt, info := range s.handed {
		if rt.permit.Context().Err() != nil {
			delete(s.handed, rt)
			continue
		}
		out = append(out, info)
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
}

func (s *Service) transport(headerWait time.Duration) *http.Transport {
	s.mu.Lock()
	defer s.mu.Unlock()
	if t, ok := s.transports[headerWait]; ok {
		return t
	}
	d := &net.Dialer{Timeout: s.cfg.DialTimeout, Resolver: s.cfg.Resolver}
	t := &http.Transport{
		Proxy:                 s.cfg.Proxy,
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
		ResponseHeaderTimeout: headerWait,
	}
	s.transports[headerWait] = t
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
	kind   string
	permit Permit
	next   http.RoundTripper
}

func (t *permitted) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := usable(t.kind, t.permit); err != nil {
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
			return nil, herr.Wrap(ReasonNotPermitted, err, "the permit for "+t.kind+" was withdrawn")
		}
		return nil, err
	}
	resp.Body = &releaseBody{ReadCloser: resp.Body, release: func() { stop(); cancel(nil) }}
	return resp, nil
}

// releaseBody ends the request's joined context when the body is closed.
type releaseBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
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
