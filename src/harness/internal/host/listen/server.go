package listen

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/klauspost/compress/gzhttp"
	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Config is host.listen's entry config (host-telemetry.md sec. 6.2). Zero fields take
// the defaults below.
type Config struct {
	// Bind is one IP literal, loopback by default, never unspecified.
	Bind string `json:"bind,omitempty"`
	// Port is the TCP port; nil means DefaultPort and 0 asks the system for a free one.
	Port *uint16 `json:"port,omitempty"`
	// TLS names a PEM pair; both empty means no TLS.
	TLS TLS `json:"tls,omitzero"`
	// AllowInsecureRemote lets a non-loopback bind start without TLS, with a warning.
	AllowInsecureRemote bool `json:"allowInsecureRemote,omitempty"`
	// TrustedHosts are the further names a browser may use for this host; one that is
	// not loopback makes the listener remote. The Host check itself is DH-D-HOST-2's.
	TrustedHosts []string `json:"trustedHosts,omitempty"`
	// PublicURL is the address a proxy publishes this host under; set, it is remote.
	PublicURL   string      `json:"publicURL,omitempty"`
	Compression Compression `json:"compression,omitzero"`

	ReadHeaderTimeout time.Duration `json:"readHeaderTimeout,omitempty"`
	IdleTimeout       time.Duration `json:"idleTimeout,omitempty"`
	MaxHeaderBytes    int           `json:"maxHeaderBytes,omitempty"`
	// ShutdownGrace bounds how long Close waits for open requests.
	ShutdownGrace time.Duration `json:"shutdownGrace,omitempty"`
}

// TLS is the certificate pair. The files are re-read when either changes.
type TLS struct {
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
}

// Compression is opt-in gzip of responses (DH-ADR-COMPRESSION-1). A Stream request,
// a Range request and an Uncompressed mount are never compressed.
type Compression struct {
	// Mode is CompressionNone (the default) or CompressionGzip.
	Mode string `json:"mode,omitempty"`
	// Level is the gzip level, 1 (fastest, the default) to 9.
	Level int `json:"level,omitempty"`
	// MinBytes is the smallest body that is compressed.
	MinBytes int `json:"minBytes,omitempty"`
}

// Compression modes.
const (
	CompressionNone = "none"
	CompressionGzip = "gzip"
)

// Defaults for the zero fields of Config.
const (
	DefaultBind                     = "127.0.0.1"
	DefaultPort              uint16 = 3080
	DefaultLevel                    = 1
	DefaultMinBytes                 = 1024
	DefaultReadHeaderTimeout        = 10 * time.Second
	DefaultIdleTimeout              = 2 * time.Minute
	DefaultMaxHeaderBytes           = 64 << 10
	DefaultShutdownGrace            = 5 * time.Second
)

// Address is the bind address cfg resolves to: Bind and Port with their defaults,
// parsed once by ParseBind. The listener listens on exactly this value.
func (c Config) Address() (netip.AddrPort, error) {
	port := DefaultPort
	if c.Port != nil {
		port = *c.Port
	}
	return ParseBind(net.JoinHostPort(orDefault(c.Bind, DefaultBind), strconv.Itoa(int(port))))
}

// remote reports whether a trusted host or a public URL lets other machines reach
// this listener.
func (c Config) remote() bool {
	if c.PublicURL != "" {
		return true
	}
	for _, h := range c.TrustedHosts {
		if a, err := netip.ParseAddr(h); err == nil {
			if !isLoopback(a) {
				return true
			}
		} else if !strings.EqualFold(h, "localhost") {
			return true
		}
	}
	return false
}

// compressor builds the gzip wrapper, or nil when compression is off.
func (c Compression) compressor() (func(http.Handler) http.HandlerFunc, error) {
	switch c.Mode {
	case "", CompressionNone:
		return nil, nil
	case CompressionGzip:
	default:
		return nil, herr.Newf(ReasonCompression, "compression mode %q: want %q or %q", c.Mode, CompressionNone, CompressionGzip)
	}
	level := orDefault(c.Level, DefaultLevel)
	if level < 1 || level > 9 || c.MinBytes < 0 {
		return nil, herr.Newf(ReasonCompression, "compression level %d (1 to 9) or minBytes %d (at least 0) out of range", level, c.MinBytes)
	}
	wrap, err := gzhttp.NewWrapper(
		gzhttp.EnableZstd(false),
		gzhttp.CompressionLevel(level),
		gzhttp.MinSize(orDefault(c.MinBytes, DefaultMinBytes)),
		gzhttp.ExceptContentTypes([]string{"text/event-stream"}),
	)
	if err != nil {
		return nil, herr.Wrap(ReasonCompression, err, "compression")
	}
	return wrap, nil
}

// Server is the listener. Start returns one that is serving.
type Server struct {
	ctx    *cordis.Context
	bound  netip.AddrPort
	secure bool
	mode   Mode
	grace  time.Duration
	ready  <-chan struct{}

	mu     sync.Mutex
	mounts atomic.Pointer[[]*Mount]

	gzip func(http.Handler) http.HandlerFunc // nil when compression is off

	// conns guards the hijacked connections (WebSockets) and the open handlers,
	// which http.Server.Shutdown neither tracks nor waits for.
	conns    sync.Mutex
	hijacked map[*hijackedConn]struct{}
	open     int           // handlers running
	drained  chan struct{} // closed when open reaches 0, once Close asks

	http      *http.Server
	served    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	cancel    context.CancelFunc
}

var _ Listener = (*Server)(nil)

// Start resolves cfg's address, loads the TLS pair, binds the address and serves.
// Bad configuration or TLS material fails here, and nothing listens. ctx is the
// context the pipeline asks listen/authenticate in and logs to; ready gates Stream
// requests.
func Start(ctx *cordis.Context, cfg Config, ready <-chan struct{}) (*Server, error) {
	ap, err := cfg.Address()
	if err != nil {
		return nil, err
	}
	if (cfg.TLS.CertFile == "") != (cfg.TLS.KeyFile == "") {
		return nil, herr.New(ReasonTLS, "tls.certFile and tls.keyFile are set together or not at all")
	}
	gz, err := cfg.Compression.compressor()
	if err != nil {
		return nil, err
	}
	s := &Server{ctx: ctx, secure: cfg.TLS.CertFile != "", ready: ready, grace: orDefault(cfg.ShutdownGrace, DefaultShutdownGrace),
		gzip: gz, hijacked: map[*hijackedConn]struct{}{}, served: make(chan struct{}), done: make(chan struct{})}
	if cfg.remote() {
		s.mode = Remote
	}
	if !isLoopback(ap.Addr()) {
		s.mode = Remote
		if !s.secure && !cfg.AllowInsecureRemote {
			return nil, herr.Newf(ReasonInsecureRemote, "bind address %s is reachable from other machines; configure TLS or set allowInsecureRemote", ap)
		}
		if !s.secure {
			ctx.Logger().Warnf("listen: %s serves other machines without TLS (allowInsecureRemote)", ap)
		}
	}
	empty := []*Mount{}
	s.mounts.Store(&empty)
	base, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	s.http = &http.Server{
		Handler:           s,
		ReadHeaderTimeout: orDefault(cfg.ReadHeaderTimeout, DefaultReadHeaderTimeout),
		IdleTimeout:       orDefault(cfg.IdleTimeout, DefaultIdleTimeout),
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
		BaseContext:       func(net.Listener) context.Context { return base },
	}
	if s.http.MaxHeaderBytes == 0 {
		s.http.MaxHeaderBytes = DefaultMaxHeaderBytes
	}
	var ln net.Listener
	if s.secure {
		certs, err := newCertSource(cfg.TLS.CertFile, cfg.TLS.KeyFile, func(err error) { ctx.Logger().Warnf("listen: %v", err) })
		if err != nil {
			cancel()
			return nil, err
		}
		s.http.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: certs.getCertificate}
	}
	tcp, err := net.ListenTCP("tcp", net.TCPAddrFromAddrPort(ap))
	if err != nil {
		cancel()
		return nil, err
	}
	// The bound address is the parsed value, spelled as parsed, with the chosen port.
	s.bound = ap
	if a, ok := tcp.Addr().(*net.TCPAddr); ok {
		s.bound = netip.AddrPortFrom(ap.Addr(), uint16(a.Port))
	}
	ln = tcp
	if s.secure {
		ln = tls.NewListener(tcp, s.http.TLSConfig)
	}
	go func() {
		defer close(s.served)
		if err := s.http.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			ctx.Logger().Errorf("listen: serve %s: %v", s.bound, err)
		}
	}()
	return s, nil
}

func orDefault[T comparable](v, def T) T {
	var zero T
	if v == zero {
		return def
	}
	return v
}

// Addr is the bound address: the parsed address with the port the system chose.
func (s *Server) Addr() netip.AddrPort { return s.bound }

// Origin is the listener's own origin.
func (s *Server) Origin() string {
	if s.secure {
		return "https://" + s.bound.String()
	}
	return "http://" + s.bound.String()
}

// Secure reports whether the listener speaks TLS.
func (s *Server) Secure() bool { return s.secure }

// Mode is Remote for a non-loopback bind.
func (s *Server) Mode() Mode { return s.mode }

// Ready closes when boot reports the required entries loaded.
func (s *Server) Ready() <-chan struct{} { return s.ready }

// Done closes when Close starts.
func (s *Server) Done() <-chan struct{} { return s.done }

// Mount registers m as an effect of owner's fiber.
func (s *Server) Mount(owner *cordis.Context, m Mount) error {
	if !validPrefix(m.Prefix) || m.Serve == nil {
		return herr.Newf(ReasonMountInvalid, "mount %q: the prefix starts with / and a handler is set", m.Prefix)
	}
	mount := &m
	_, err := owner.Effect("listen mount "+m.Prefix, func() (cordis.Disposer, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		cur := *s.mounts.Load()
		for _, have := range cur {
			if have.Prefix == m.Prefix {
				return nil, herr.Newf(ReasonMountTaken, "mount %q is already mounted", m.Prefix)
			}
		}
		next := append(append([]*Mount{}, cur...), mount)
		s.mounts.Store(&next)
		return func() error {
			s.mu.Lock()
			defer s.mu.Unlock()
			var kept []*Mount
			for _, have := range *s.mounts.Load() {
				if have != mount {
					kept = append(kept, have)
				}
			}
			s.mounts.Store(&kept)
			return nil
		}, nil
	})
	return err
}

// ServeHTTP is the admission pipeline.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.enter()
	defer s.leave()
	m := longest(*s.mounts.Load(), r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	class := classify(m, r)
	switch class {
	case Required, Anonymous, Callback:
	default:
		// Deny, and any value that is not a class: admission fails closed.
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	stream := streams(m, r)
	if stream && !closed(s.ready) {
		http.Error(w, "starting", http.StatusServiceUnavailable)
		return
	}
	if class == Required {
		peer, ok, err := Authenticate.Bail(s.ctx, &Credentials{Request: r})
		if err != nil {
			s.ctx.Logger().Errorf("listen: authenticate %s: %v", m.Prefix, err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		if !ok || peer == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		r = r.WithContext(context.WithValue(r.Context(), peerKey{}, peer))
	}
	var h http.Handler = m.Serve
	if s.gzip != nil && !stream && !m.Uncompressed && r.Header.Get("Range") == "" {
		h = s.gzip(keepNoTransform(h))
	}
	s.serve(&trackedWriter{ResponseWriter: w, s: s}, r, m, h)
}

// serve calls the handler; a panic is a server fault, answered 500 and logged.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, m *Mount, h http.Handler) {
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				panic(v)
			}
			s.ctx.Logger().Errorf("listen: handler %s panicked: %v", m.Prefix, v)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}()
	h.ServeHTTP(w, r)
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// keepNoTransform runs inside the gzip wrapper: a response whose Cache-Control
// carries no-transform is marked so gzhttp leaves it as written
// (DH-LIB-COMPRESSION-R3). gzhttp decides when the headers are committed, so the mark
// is set on the handler's first WriteHeader, Write or Flush.
func keepNoTransform(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(&noTransformWriter{ResponseWriter: w}, r)
	})
}

type noTransformWriter struct {
	http.ResponseWriter
	checked bool
}

func (n *noTransformWriter) check() {
	if n.checked {
		return
	}
	n.checked = true
	if noTransform(n.Header()) {
		n.Header().Set(gzhttp.HeaderNoCompression, "1")
	}
}

func (n *noTransformWriter) WriteHeader(code int) { n.check(); n.ResponseWriter.WriteHeader(code) }

func (n *noTransformWriter) Write(b []byte) (int, error) { n.check(); return n.ResponseWriter.Write(b) }

func (n *noTransformWriter) Flush() {
	n.check()
	if f, ok := n.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (n *noTransformWriter) Unwrap() http.ResponseWriter { return n.ResponseWriter }

// noTransform reports whether any Cache-Control directive, over every header line
// and comma-separated list, is no-transform.
func noTransform(h http.Header) bool {
	for _, line := range h.Values("Cache-Control") {
		for d := range strings.SplitSeq(line, ",") {
			if strings.EqualFold(strings.TrimSpace(d), "no-transform") {
				return true
			}
		}
	}
	return false
}

// closeBudget splits the grace period of Close into its phases so that the whole
// unload ends inside it: Shutdown gets the grace less forceShare, forced closing and
// the wait for handlers end returnShare before the grace ends, and that last share
// is left for Close itself to return.
func closeBudget(grace time.Duration) (shutdown, handlers time.Duration) {
	forceShare := min(grace/4, 250*time.Millisecond)
	returnShare := min(grace/10, 100*time.Millisecond)
	return grace - forceShare, grace - returnShare
}

// Close is unload (sec. 6.3): stop accepting, close Done so Stream handlers return,
// Shutdown, then close what remains -- every connection, hijacked ones included --
// and wait for the handlers to return. Every phase ends inside one grace period; a
// handler still running when it ends is logged and left behind.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		began := time.Now()
		shutdownBy, handlersBy := closeBudget(s.grace)
		ctx, cancel := context.WithDeadline(context.Background(), began.Add(shutdownBy))
		defer cancel()
		if shutdownErr := s.http.Shutdown(ctx); shutdownErr != nil && !errors.Is(shutdownErr, context.DeadlineExceeded) {
			err = shutdownErr
		}
		s.cancel()
		if closeErr := s.http.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		if closeErr := s.closeHijacked(); closeErr != nil && err == nil {
			err = closeErr
		}
		timer := time.NewTimer(time.Until(began.Add(handlersBy)))
		defer timer.Stop()
		select {
		case <-s.idle():
		case <-timer.C:
			s.ctx.Logger().Errorf("listen: handlers still running when the %s grace period ended", s.grace)
			return
		}
		select {
		case <-s.served:
		case <-timer.C:
		}
	})
	return err
}

func (s *Server) enter() {
	s.conns.Lock()
	defer s.conns.Unlock()
	s.open++
}

func (s *Server) leave() {
	s.conns.Lock()
	defer s.conns.Unlock()
	s.open--
	if s.open == 0 && s.drained != nil {
		close(s.drained)
		s.drained = nil
	}
}

// idle closes when no handler runs.
func (s *Server) idle() <-chan struct{} {
	s.conns.Lock()
	defer s.conns.Unlock()
	ch := make(chan struct{})
	if s.open == 0 {
		close(ch)
	} else {
		s.drained = ch
	}
	return ch
}

// closeHijacked closes every hijacked connection still open; one that is already
// closed is not an error.
func (s *Server) closeHijacked() error {
	s.conns.Lock()
	open := make([]*hijackedConn, 0, len(s.hijacked))
	for c := range s.hijacked {
		open = append(open, c)
	}
	s.conns.Unlock()
	var errs []error
	for _, c := range open {
		if err := c.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// trackedWriter is the ResponseWriter a handler sees: hijacking through it records
// the connection so Close can close it. Unwrap keeps http.ResponseController working.
type trackedWriter struct {
	http.ResponseWriter
	s *Server
}

func (t *trackedWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

func (t *trackedWriter) Flush() {
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (t *trackedWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	conn, rw, err := http.NewResponseController(t.ResponseWriter).Hijack()
	if err != nil {
		return nil, nil, err
	}
	h := &hijackedConn{Conn: conn, s: t.s}
	t.s.conns.Lock()
	defer t.s.conns.Unlock()
	if closed(t.s.done) {
		return nil, nil, errors.Join(errors.New("listen: closing"), conn.Close())
	}
	t.s.hijacked[h] = struct{}{}
	return h, rw, nil
}

// hijackedConn leaves the server's set when it closes.
type hijackedConn struct {
	net.Conn
	s *Server
}

func (h *hijackedConn) Close() error {
	h.s.conns.Lock()
	delete(h.s.hijacked, h)
	h.s.conns.Unlock()
	return h.Conn.Close()
}
