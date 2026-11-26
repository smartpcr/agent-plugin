package listen

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Config is host.listen's entry config. The zero durations take the defaults below.
type Config struct {
	// Address is the bind address, an IP literal with a port (ParseBind).
	Address string `json:"address"`
	// CertFile and KeyFile name a PEM pair; both empty means no TLS.
	CertFile string `json:"certFile,omitempty"`
	KeyFile  string `json:"keyFile,omitempty"`
	// AllowInsecureRemote lets a non-loopback bind start without TLS, with a warning.
	AllowInsecureRemote bool          `json:"allowInsecureRemote,omitempty"`
	ReadHeaderTimeout   time.Duration `json:"readHeaderTimeout,omitempty"`
	IdleTimeout         time.Duration `json:"idleTimeout,omitempty"`
	MaxHeaderBytes      int           `json:"maxHeaderBytes,omitempty"`
	// ShutdownGrace bounds how long Close waits for open requests.
	ShutdownGrace time.Duration `json:"shutdownGrace,omitempty"`
}

// Defaults for the zero fields of Config.
const (
	DefaultReadHeaderTimeout = 10 * time.Second
	DefaultIdleTimeout       = 2 * time.Minute
	DefaultMaxHeaderBytes    = 64 << 10
	DefaultShutdownGrace     = 5 * time.Second
)

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

	http      *http.Server
	served    chan struct{}
	done      chan struct{}
	closeOnce sync.Once
	cancel    context.CancelFunc
}

var _ Listener = (*Server)(nil)

// Start parses cfg, loads the TLS pair, binds the parsed address and serves. Bad
// configuration or TLS material fails here, and nothing listens. ctx is the context
// the pipeline asks listen/authenticate in and logs to; ready gates Stream requests.
func Start(ctx *cordis.Context, cfg Config, ready <-chan struct{}) (*Server, error) {
	ap, err := ParseBind(cfg.Address)
	if err != nil {
		return nil, err
	}
	if (cfg.CertFile == "") != (cfg.KeyFile == "") {
		return nil, herr.New(ReasonTLS, "certFile and keyFile are set together or not at all")
	}
	s := &Server{ctx: ctx, secure: cfg.CertFile != "", ready: ready, grace: orDefault(cfg.ShutdownGrace, DefaultShutdownGrace),
		served: make(chan struct{}), done: make(chan struct{})}
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
		certs, err := newCertSource(cfg.CertFile, cfg.KeyFile, func(err error) { ctx.Logger().Warnf("listen: %v", err) })
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
	m := longest(*s.mounts.Load(), r.URL.Path)
	if m == nil {
		http.NotFound(w, r)
		return
	}
	class := classify(m, r)
	if class == Deny {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if streams(m, r) && !closed(s.ready) {
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
	s.serve(w, r, m)
}

// serve calls the handler; a panic is a server fault, answered 500 and logged.
func (s *Server) serve(w http.ResponseWriter, r *http.Request, m *Mount) {
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				panic(v)
			}
			s.ctx.Logger().Errorf("listen: handler %s panicked: %v", m.Prefix, v)
			http.Error(w, "internal error", http.StatusInternalServerError)
		}
	}()
	m.Serve.ServeHTTP(w, r)
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// Close closes Done, waits up to the grace period for open requests, then closes
// every connection and waits for the serve loop to end.
func (s *Server) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.done)
		ctx, cancel := context.WithTimeout(context.Background(), s.grace)
		defer cancel()
		if shutdownErr := s.http.Shutdown(ctx); shutdownErr != nil && !errors.Is(shutdownErr, context.DeadlineExceeded) {
			err = shutdownErr
		}
		s.cancel()
		if closeErr := s.http.Close(); closeErr != nil && err == nil {
			err = closeErr
		}
		<-s.served
	})
	return err
}
