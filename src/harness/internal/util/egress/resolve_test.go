package egress_test

import (
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func lookup(vars map[string]string) egress.Lookup {
	return func(name string) (string, bool) {
		v, ok := vars[name]
		return v, ok
	}
}

// proxyServer is a loopback forward proxy that counts the requests it receives
// and answers each itself.
func proxyServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var seen atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.Add(1)
		_, _ = io.WriteString(w, "via proxy "+r.URL.Host)
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

// The proxy names are read through the launch lookup: HTTP_PROXY carries http
// requests, NO_PROXY bypasses (and loopback is never proxied), HTTPS_PROXY wins
// over ALL_PROXY, HTTPS falls back to the HTTP proxy last, and a refused value is
// a diagnostic that never quotes the password, with that scheme going direct.
//
//dsh:test DH-T-UNDICI-1, DH-T-UNDICI-2, DH-T-UNDICI-3, DH-T-UNDICI-4, DH-T-UTIL-5
func TestResolveProxyNames(t *testing.T) {
	proxy, seen := proxyServer(t)
	svc, diags, err := egress.Resolve(lookup(map[string]string{
		"HTTP_PROXY": proxy.URL,
		"NO_PROXY":   "bypass.example, .internal.example",
	}), egress.Config{})
	if err != nil || len(diags) != 0 {
		t.Fatalf("Resolve = %v, %v", diags, err)
	}
	t.Cleanup(svc.CloseIdleConnections)
	c, err := svc.Client("model-request", permit.New(t, "model-request", "http://api.example"), egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get("http://api.example/v1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "via proxy api.example" || seen.Load() != 1 {
		t.Fatalf("response %q, proxy saw %d; want the request through the proxy", b, seen.Load())
	}

	for _, tc := range []struct {
		name   string
		vars   map[string]string
		target string
		want   string // "" is direct
		diag   string // the name of the refused value, if any
	}{
		{"https name", map[string]string{"HTTPS_PROXY": "http://p1:8080", "ALL_PROXY": "http://p2:8080"}, "https://api.example/", "p1:8080", ""},
		{"all as fallback", map[string]string{"ALL_PROXY": "http://p2:1080"}, "https://api.example/", "p2:1080", ""},
		{"lower case", map[string]string{"https_proxy": "p3:3128"}, "https://api.example/", "p3:3128", ""},
		{"lower case first", map[string]string{"https_proxy": "p3:3128", "HTTPS_PROXY": "http://p1:8080"}, "https://api.example/", "p3:3128", ""},
		{"blank is unset", map[string]string{"https_proxy": "  ", "HTTPS_PROXY": "http://p1:8080"}, "https://api.example/", "p1:8080", ""},
		{"https falls back to http", map[string]string{"HTTP_PROXY": "http://p1:8080"}, "https://api.example/", "p1:8080", ""},
		{"all before the http fallback", map[string]string{"HTTP_PROXY": "http://p1:8080", "ALL_PROXY": "http://p2:8080"}, "https://api.example/", "p2:8080", ""},
		{"refused https stays direct", map[string]string{"HTTPS_PROXY": "ftp://p9:21", "HTTP_PROXY": "http://p1:8080"}, "https://api.example/", "", "HTTPS_PROXY"},
		{"socks5 https", map[string]string{"HTTPS_PROXY": "socks5://p4:1080"}, "https://api.example/", "p4:1080", ""},
		{"socks5h all", map[string]string{"ALL_PROXY": "socks5h://p5:1080"}, "http://api.example/", "p5:1080", ""},
		{"refused all stays direct", map[string]string{"ALL_PROXY": "pac://p9", "HTTP_PROXY": "http://p1:8080"}, "https://api.example/", "", "ALL_PROXY"},
		{"bypass exact", map[string]string{"HTTPS_PROXY": "http://p1:8080", "NO_PROXY": "api.example"}, "https://api.example/", "", ""},
		{"bypass subdomain", map[string]string{"HTTPS_PROXY": "http://p1:8080", "NO_PROXY": "example"}, "https://api.example/", "", ""},
		{"bypass dot is subdomains only", map[string]string{"HTTPS_PROXY": "http://p1:8080", "NO_PROXY": ".api.example"}, "https://api.example/", "p1:8080", ""},
		{"bypass port", map[string]string{"HTTPS_PROXY": "http://p1:8080", "NO_PROXY": "api.example:8443"}, "https://api.example/", "p1:8080", ""},
		{"bypass cidr", map[string]string{"HTTPS_PROXY": "http://p1:8080", "NO_PROXY": "10.0.0.0/8"}, "https://10.1.2.3/", "", ""},
		{"bypass all", map[string]string{"HTTPS_PROXY": "http://p1:8080", "NO_PROXY": "*"}, "https://api.example/", "", ""},
		{"loopback", map[string]string{"HTTPS_PROXY": "http://p1:8080"}, "https://127.0.0.1:9/", "", ""},
		{"refused scheme", map[string]string{"HTTPS_PROXY": "ftp://user:hunter2@p1:21", "ALL_PROXY": "http://p2:8080"}, "https://api.example/", "", "HTTPS_PROXY"},
		{"refused path", map[string]string{"HTTP_PROXY": "http://p1:8080/path"}, "http://api.example/", "", "HTTP_PROXY"},
		{"refused port", map[string]string{"HTTP_PROXY": "http://p1:99999"}, "http://api.example/", "", "HTTP_PROXY"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, diags, err := egress.Resolve(lookup(tc.vars), egress.Config{})
			if err != nil {
				t.Fatal(err)
			}
			got := egress.ProxyFor(lookup(tc.vars), mustParse(t, tc.target))
			if host := hostOf(got); host != tc.want {
				t.Errorf("proxy for %s = %q, want %q", tc.target, host, tc.want)
			}
			if tc.diag == "" {
				if len(diags) != 0 {
					t.Errorf("diagnostics %v, want none", diags)
				}
				return
			}
			if len(diags) != 1 || diags[0].Name != tc.diag || diags[0].Reason != egress.ReasonRefusedProxyValue {
				t.Fatalf("diagnostics %v, want one %s for %s", diags, egress.ReasonRefusedProxyValue, tc.diag)
			}
			if strings.Contains(diags[0].String(), "hunter2") {
				t.Fatalf("the diagnostic quotes the password: %s", diags[0])
			}
		})
	}
}

func mustParse(t *testing.T, s string) *url.URL {
	t.Helper()
	u, err := url.Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func hostOf(u *url.URL) string {
	if u == nil {
		return ""
	}
	return u.Host
}

// ExtraCACerts: a server signed by a private authority is refused by default and
// accepted once the authority's file is configured; an unreadable file, or one
// with no certificate, fails Resolve with egress/ca-unreadable.
//
//dsh:test DH-T-UNDICI-5
func TestExtraCACerts(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "private")
	}))
	t.Cleanup(srv.Close)
	grant := permit.New(t, "model-request", srv.URL)
	get := func(c egress.Config) (string, error) {
		svc, _, err := egress.Resolve(nil, c)
		if err != nil {
			t.Fatal(err)
		}
		defer svc.CloseIdleConnections()
		cl, err := svc.Client("model-request", grant, egress.Options{})
		if err != nil {
			t.Fatal(err)
		}
		resp, err := cl.Get(srv.URL)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b), nil
	}
	if _, err := get(egress.Config{}); err == nil {
		t.Fatal("a private authority was trusted by default")
	}
	dir := t.TempDir()
	ca := filepath.Join(dir, "proxy-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	if body, err := get(egress.Config{ExtraCACerts: []string{ca}}); err != nil || body != "private" {
		t.Fatalf("with the authority configured: %q, %v", body, err)
	}
	junk := filepath.Join(dir, "junk.pem")
	if err := os.WriteFile(junk, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{filepath.Join(dir, "missing.pem"), junk} {
		if _, _, err := egress.Resolve(nil, egress.Config{ExtraCACerts: []string{f}}); herr.Kind(err) != egress.ReasonCAUnreadable {
			t.Errorf("Resolve with %s: %v, want %s", filepath.Base(f), err, egress.ReasonCAUnreadable)
		}
	}
}

// A client belongs to the fiber that asked: util.egress provides the service on
// the root, a consumer fiber asks for clients with its Lifetime as owner, and
// unloading the consumer releases every one of them -- they leave Handed and
// refuse requests without dialling -- while the permit stays live. Release does
// the same for one client, so asking repeatedly accumulates nothing.
//
//dsh:test DH-T-UTIL-3
func TestClientsAreReleasedWithTheirOwner(t *testing.T) {
	srv := newCountingServer(t, nil)
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	if _, err := egress.LaunchKey.Provide(r, egress.Lookup(nil)); err != nil {
		t.Fatal(err)
	}
	ef, err := r.Use(egress.Component, egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := ef.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ef.Dispose)
	svc, err := egress.Key.Get(r)
	if err != nil {
		t.Fatal(err)
	}
	grant := permit.New(t, "telemetry", srv.URL)

	for range 100 {
		c, err := svc.Client("telemetry", grant, egress.Options{})
		if err != nil {
			t.Fatal(err)
		}
		svc.Release(c)
		svc.Release(c)
	}
	if h := svc.Handed(); len(h) != 0 {
		t.Fatalf("Handed() = %d clients after Release, want 0", len(h))
	}

	var clients []*http.Client
	consumer, err := r.Use(cordis.Define("test.egress-consumer", func(ctx *cordis.Context, _ cordis.None) error {
		s, err := egress.Key.Get(ctx)
		if err != nil {
			return err
		}
		for range 3 {
			c, err := s.Client("telemetry", grant, egress.Options{Owner: ctx.Lifetime(), Label: "consumer"})
			if err != nil {
				return err
			}
			clients = append(clients, c)
		}
		return nil
	}, cordis.WithInject(egress.Key.Name())), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := consumer.Wait(); err != nil {
		t.Fatal(err)
	}
	if h := svc.Handed(); len(h) != 3 {
		t.Fatalf("Handed() = %d clients while the consumer is loaded, want 3", len(h))
	}
	consumer.Dispose()
	if h := svc.Handed(); len(h) != 0 {
		t.Fatalf("Handed() = %+v after the consumer unloaded, want none", h)
	}
	for _, c := range clients {
		if _, err := post(t, c, srv.URL, "late"); !egress.IsNotPermitted(err) {
			t.Fatalf("a released client sent: %v", err)
		}
	}
	if n := srv.conns.Load(); n != 0 {
		t.Fatalf("released clients dialled %d connections", n)
	}
	if grant.Context().Err() != nil {
		t.Fatal("the permit ended with the client")
	}
}

// socksServer is a loopback SOCKS5 proxy (no authentication, CONNECT only) that
// counts the tunnels it opens and connects every one to target, whatever name the
// client asked for: a request reaching target proves it went through the proxy.
func socksServer(t *testing.T, target string) (string, *atomic.Int64) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var tunnels atomic.Int64
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 262)
				// greeting: VER NMETHODS METHODS...
				if _, err := io.ReadFull(c, buf[:2]); err != nil {
					return
				}
				if _, err := io.ReadFull(c, buf[:buf[1]]); err != nil {
					return
				}
				_, _ = c.Write([]byte{5, 0})
				// request: VER CMD RSV ATYP ADDR PORT
				if _, err := io.ReadFull(c, buf[:4]); err != nil || buf[1] != 1 {
					return
				}
				var n int
				switch buf[3] {
				case 1:
					n = 4
				case 4:
					n = 16
				case 3:
					if _, err := io.ReadFull(c, buf[:1]); err != nil {
						return
					}
					n = int(buf[0])
				default:
					return
				}
				if _, err := io.ReadFull(c, buf[:n+2]); err != nil {
					return
				}
				up, err := net.Dial("tcp", target)
				if err != nil {
					_, _ = c.Write([]byte{5, 1, 0, 1, 0, 0, 0, 0, 0, 0})
					return
				}
				defer up.Close()
				tunnels.Add(1)
				_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				go func() { _, _ = io.Copy(up, c) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	return ln.Addr().String(), &tunnels
}

// SOCKS proxies are accepted (DH-ADR-UNDICI-2): socks5 and socks5h values resolve
// without a diagnostic, and with socks5h the request -- for a name only the proxy
// can resolve -- reaches the server through the proxy's tunnel.
//
//dsh:test DH-T-UNDICI-1
func TestSocksProxy(t *testing.T) {
	var arrived atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		arrived.Add(1)
		_, _ = io.WriteString(w, "tunnelled "+r.Host)
	}))
	t.Cleanup(srv.Close)
	addr, tunnels := socksServer(t, srv.Listener.Addr().String())

	for _, scheme := range []string{"socks5", "socks5h"} {
		vars := map[string]string{"ALL_PROXY": scheme + "://" + addr}
		if _, diags, err := egress.Resolve(lookup(vars), egress.Config{}); err != nil || len(diags) != 0 {
			t.Fatalf("%s: Resolve = %v, %v; want no diagnostic", scheme, diags, err)
		}
		got := egress.ProxyFor(lookup(vars), mustParse(t, "https://api.example/"))
		if got == nil || got.Scheme != scheme || got.Host != addr {
			t.Fatalf("%s: proxy for https = %v, want %s://%s", scheme, got, scheme, addr)
		}
	}

	svc, _, err := egress.Resolve(lookup(map[string]string{"ALL_PROXY": "socks5h://" + addr}), egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(svc.CloseIdleConnections)
	const dest = "http://only-the-proxy-knows.invalid"
	c, err := svc.Client("model-request", permit.New(t, "model-request", dest), egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Get(dest + "/v1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "tunnelled only-the-proxy-knows.invalid" || tunnels.Load() != 1 || arrived.Load() != 1 {
		t.Fatalf("response %q, %d tunnels, %d arrivals; want one request through the SOCKS proxy", b, tunnels.Load(), arrived.Load())
	}
}
