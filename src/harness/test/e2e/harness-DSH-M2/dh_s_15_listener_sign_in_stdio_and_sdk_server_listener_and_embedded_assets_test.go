//go:build e2e

package e2e

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
)

// leaPeer is the peer the admit-all fake admits every request as.
type leaPeer struct{ id string }

func (p leaPeer) ID() string               { return p.id }
func (p leaPeer) Context() context.Context { return context.Background() }
func (p leaPeer) FormToken() string        { return "form-" + p.id }

// leaRecorder counts its calls and answers its prefix and the admitted peer.
type leaRecorder struct {
	prefix string
	calls  atomic.Int32
}

func (h *leaRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	id := "none"
	if p, ok := listen.PeerFrom(r.Context()); ok && p != nil {
		id = p.ID()
	}
	io.WriteString(w, h.prefix+" "+id)
}

type leaState struct {
	root     *cordis.Context
	fibers   []*cordis.Fiber
	listener listen.Listener
	handlers map[string]*leaRecorder

	dir               string
	certFile, keyFile string
	stampAt           time.Time
	origin            string
	serial            int64

	status int
	body   string
}

func (s *leaState) keep(f *cordis.Fiber, err error) error {
	if err != nil {
		return err
	}
	s.fibers = append(s.fibers, f)
	return f.Wait()
}

func (s *leaState) close() {
	for i := len(s.fibers) - 1; i >= 0; i-- {
		s.fibers[i].Dispose()
	}
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
}

// load starts host.listen on bind (an IP literal with a port), optionally over the
// scenario's certificate pair, and installs the authenticate fake.
func (s *leaState) load(bind, admit string, secure bool) error {
	ap, err := netip.ParseAddrPort(bind)
	if err != nil {
		return err
	}
	if !ap.Addr().IsLoopback() {
		return fmt.Errorf("bind %s: the scenario runs on loopback only", bind)
	}
	var yes bool
	switch admit {
	case "admit-all":
		yes = true
	case "admit-none":
	default:
		return fmt.Errorf("unknown authenticate fake %q", admit)
	}
	s.root = cordis.New()
	s.root.Loggers().ClearExporters()
	s.root.Loggers().AddExporter(&cordis.MemoryExporter{})
	port := ap.Port()
	cfg := listen.Config{Bind: ap.Addr().String(), Port: &port, ShutdownGrace: 2 * time.Second}
	if secure {
		cfg.TLS = listen.TLS{CertFile: s.certFile, KeyFile: s.keyFile}
	}
	if err := s.keep(s.root.Use(listen.Component, cfg)); err != nil {
		return fmt.Errorf("load host.listen: %w", err)
	}
	if err := s.keep(s.root.Apply("get listener", func(ctx *cordis.Context) (err error) {
		s.listener, err = listen.Key.Get(ctx)
		return err
	}, listen.Key.Name())); err != nil {
		return err
	}
	if secure != s.listener.Secure() {
		return fmt.Errorf("listener secure = %v, want %v", s.listener.Secure(), secure)
	}
	s.origin = s.listener.Origin()
	return s.keep(s.root.Apply("fake authenticate", func(ctx *cordis.Context) error {
		_, err := listen.Authenticate.On(ctx, func(_ *cordis.Context, c *listen.Credentials) (listen.Peer, bool, error) {
			if !yes || c.Request == nil {
				return nil, false, nil
			}
			return leaPeer{"p1"}, true, nil
		})
		return err
	}))
}

func (s *leaState) mountBoth(a, b string) error {
	s.handlers = map[string]*leaRecorder{}
	for _, p := range []string{a, b} {
		h := &leaRecorder{prefix: p}
		s.handlers[p] = h
		m := listen.Mount{Prefix: p, Serve: h} // nil Classify is Required: admission asks the fake
		if err := s.keep(s.root.Apply("mount "+p, func(ctx *cordis.Context) error { return s.listener.Mount(ctx, m) })); err != nil {
			return err
		}
	}
	return nil
}

func (s *leaState) client() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // the per-test pair is self-signed
		},
	}
}

func (s *leaState) send(request string) error {
	method, path, ok := strings.Cut(request, " ")
	if !ok {
		return fmt.Errorf("request %q: want METHOD PATH", request)
	}
	req, err := http.NewRequest(method, s.listener.Origin()+path, nil)
	if err != nil {
		return err
	}
	resp, err := s.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	s.status, s.body = resp.StatusCode, string(b)
	return nil
}

func (s *leaState) sendHTTPS(request string) error {
	if !strings.HasPrefix(s.listener.Origin(), "https://") {
		return fmt.Errorf("origin %s is not https", s.listener.Origin())
	}
	return s.send(request)
}

func (s *leaState) statusIs(want int) error {
	if s.status != want {
		return fmt.Errorf("status %d (body %q), want %d", s.status, s.body, want)
	}
	return nil
}

func (s *leaState) answeredBy(prefix, peer string) error {
	if want := prefix + " " + peer; s.body != want {
		return fmt.Errorf("body %q, want %q", s.body, want)
	}
	return nil
}

func (s *leaState) handlerRan(prefix string, n int) error {
	h, ok := s.handlers[prefix]
	if !ok {
		return fmt.Errorf("no handler mounted at %q", prefix)
	}
	if got := int(h.calls.Load()); got != n {
		return fmt.Errorf("handler %q ran %d times, want %d", prefix, got, n)
	}
	return nil
}

// leaPair makes a self-signed P-256 certificate for 127.0.0.1 with the serial.
func leaPair(serial int64) (certPEM, keyPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "dsh e2e"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), nil
}

// writePair writes both files and moves their modification time on, as a renewal
// tool replacing the pair would; coarse file-system clocks cannot hide the change.
func (s *leaState) writePair(serial int64) error {
	c, k, err := leaPair(serial)
	if err != nil {
		return err
	}
	s.stampAt = s.stampAt.Add(time.Minute)
	for file, b := range map[string][]byte{s.certFile: c, s.keyFile: k} {
		if err := os.WriteFile(file, b, 0o600); err != nil {
			return err
		}
		if err := os.Chtimes(file, s.stampAt, s.stampAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *leaState) givenPair(serial int64) error {
	dir, err := os.MkdirTemp("", "dsh-e2e-tls-")
	if err != nil {
		return err
	}
	s.dir = dir
	s.certFile, s.keyFile = filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	s.stampAt = time.Now()
	return s.writePair(serial)
}

func (s *leaState) dialTLS() error {
	addr := strings.TrimPrefix(s.listener.Origin(), "https://")
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true}) //nolint:gosec // the step reads the presented certificate, not its trust
	if err != nil {
		return err
	}
	defer conn.Close()
	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return errors.New("no certificate presented")
	}
	s.serial = certs[0].SerialNumber.Int64()
	return nil
}

func (s *leaState) serialIs(want int64) error {
	if s.serial != want {
		return fmt.Errorf("presented certificate serial %d, want %d", s.serial, want)
	}
	return nil
}

func (s *leaState) originUnchanged() error {
	if got := s.listener.Origin(); got != s.origin {
		return fmt.Errorf("origin %s, want %s (the listener restarted)", got, s.origin)
	}
	return nil
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_listener_and_embedded_assets(ctx *godog.ScenarioContext) {
	var s *leaState
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &leaState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, nil
	})
	ctx.Step(`^host\.listen is loaded on "([^"]*)" with an? (admit-all|admit-none) authenticate fake$`, func(bind, admit string) error { return s.load(bind, admit, false) })
	ctx.Step(`^host\.listen is loaded on "([^"]*)" over TLS from those files with an? (admit-all|admit-none) authenticate fake$`, func(bind, admit string) error { return s.load(bind, admit, true) })
	ctx.Step(`^handlers are mounted at "([^"]*)" and "([^"]*)"$`, func(a, b string) error { return s.mountBoth(a, b) })
	ctx.Step(`^"([^"]*)" is sent$`, func(r string) error { return s.send(r) })
	ctx.Step(`^"([^"]*)" is sent over HTTPS$`, func(r string) error { return s.sendHTTPS(r) })
	ctx.Step(`^the response status is (\d+)$`, func(n int) error { return s.statusIs(n) })
	ctx.Step(`^the response was answered by the "([^"]*)" handler for peer "([^"]*)"$`, func(p, peer string) error { return s.answeredBy(p, peer) })
	ctx.Step(`^the "([^"]*)" handler ran (\d+) times$`, func(p string, n int) error { return s.handlerRan(p, n) })
	ctx.Step(`^a per-test certificate pair with serial (\d+) written to a temp directory$`, func(n int64) error { return s.givenPair(n) })
	ctx.Step(`^a new TLS connection is opened$`, func() error { return s.dialTLS() })
	ctx.Step(`^the presented certificate has serial (\d+)$`, func(n int64) error { return s.serialIs(n) })
	ctx.Step(`^the files are replaced with a new pair with serial (\d+)$`, func(n int64) error { return s.writePair(n) })
	ctx.Step(`^the listener origin is unchanged$`, func() error { return s.originUnchanged() })
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_listener_and_embedded_assets(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_listener_and_embedded_assets,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_listener_and_embedded_assets.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the listener and embedded assets e2e scenarios failed")
	}
}
