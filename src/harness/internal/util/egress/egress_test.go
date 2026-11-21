package egress_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// fakePermit stands in for sharing.Permit, which util may not import.
type fakePermit struct {
	kind, dest string
	ctx        context.Context
}

func (p fakePermit) Kind() string             { return p.kind }
func (p fakePermit) Destination() string      { return p.dest }
func (p fakePermit) Context() context.Context { return p.ctx }

// countingServer is an httptest server on 127.0.0.1:0 that counts connections and
// requests.
type countingServer struct {
	*httptest.Server
	conns, requests atomic.Int64
	mu              sync.Mutex
	userAgent, body string
}

func newCountingServer(t *testing.T, h http.HandlerFunc) *countingServer {
	t.Helper()
	cs := &countingServer{}
	cs.Server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.requests.Add(1)
		b, _ := io.ReadAll(r.Body)
		cs.mu.Lock()
		cs.userAgent, cs.body = r.Header.Get("User-Agent"), string(b)
		cs.mu.Unlock()
		if h != nil {
			h(w, r)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	cs.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			cs.conns.Add(1)
		}
	}
	cs.Start()
	t.Cleanup(cs.Close)
	return cs
}

func post(t *testing.T, c *http.Client, url, body string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("User-Agent", "someone-else")
	return c.Do(req)
}

func wantNotPermitted(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("err = nil, want egress/not-permitted")
	}
	if !egress.IsNotPermitted(err) || herr.Kind(err) != egress.ReasonNotPermitted {
		t.Fatalf("err = %v (kind %q), want %s", err, herr.Kind(err), egress.ReasonNotPermitted)
	}
}

// TC-06d: without a permit nothing is dialled; with a model-request permit for the
// server's origin the request arrives once, carrying `User-Agent: dsh`.
func TestNoPermitNoDial(t *testing.T) {
	srv := newCountingServer(t, nil)
	if !strings.HasPrefix(srv.Listener.Addr().String(), "127.0.0.1:") {
		t.Fatalf("server on %s, want 127.0.0.1", srv.Listener.Addr())
	}
	svc := egress.New(egress.Config{})
	t.Cleanup(svc.CloseIdleConnections)

	if _, err := svc.Client("model-request", nil, egress.Options{}); err == nil {
		t.Fatal("a client was handed out without a permit")
	} else {
		wantNotPermitted(t, err)
	}

	// A permit for another kind is refused the same way.
	other := fakePermit{kind: "telemetry", dest: srv.URL, ctx: context.Background()}
	if _, err := svc.Client("model-request", other, egress.Options{}); err == nil {
		t.Fatal("a client was handed out for a permit of another kind")
	} else {
		wantNotPermitted(t, err)
	}

	// A permit for another destination: the client exists, but the POST fails
	// before any dial.
	elsewhere := fakePermit{kind: "model-request", dest: "https://api.example.invalid", ctx: context.Background()}
	c, err := svc.Client("model-request", elsewhere, egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := post(t, c, srv.URL+"/v1/chat", "{}"); err == nil {
		t.Fatal("POST to an unpermitted destination succeeded")
	} else {
		wantNotPermitted(t, err)
	}
	if n := srv.conns.Load(); n != 0 {
		t.Fatalf("server saw %d connections without a permit, want 0", n)
	}

	// With a model-request permit for the server, the request arrives once.
	ok := fakePermit{kind: "model-request", dest: srv.URL, ctx: context.Background()}
	c, err = svc.Client("model-request", ok, egress.Options{Label: "deepseek"})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := post(t, c, srv.URL+"/v1/chat", `{"hello":1}`)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || string(b) != "ok" {
		t.Fatalf("response = %d %q", resp.StatusCode, b)
	}
	srv.mu.Lock()
	ua, body := srv.userAgent, srv.body
	srv.mu.Unlock()
	if srv.requests.Load() != 1 || body != `{"hello":1}` || ua != egress.UserAgent {
		t.Fatalf("requests=%d body=%q user-agent=%q", srv.requests.Load(), body, ua)
	}
	if srv.conns.Load() != 1 {
		t.Fatalf("connections = %d, want 1", srv.conns.Load())
	}
	handed := svc.Handed()
	if len(handed) != 2 || !containsLabel(handed, "deepseek") {
		t.Fatalf("Handed() = %+v, want the two live clients", handed)
	}
}

func containsLabel(list []egress.ClientInfo, label string) bool {
	for _, c := range list {
		if c.Label == label {
			return true
		}
	}
	return false
}

// Withdrawing consent cancels a request in flight, the next request fails before
// any dial, and the client leaves Handed.
func TestWithdrawnPermitCancelsAndRefuses(t *testing.T) {
	entered := make(chan struct{})
	srv := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-r.Context().Done()
	})
	svc := egress.New(egress.Config{})
	t.Cleanup(svc.CloseIdleConnections)
	ctx, withdraw := context.WithCancel(context.Background())
	c, err := svc.Client("telemetry", fakePermit{kind: "telemetry", dest: srv.URL, ctx: ctx}, egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		resp, err := post(t, c, srv.URL, "batch")
		if resp != nil {
			_ = resp.Body.Close()
		}
		done <- err
	}()
	<-entered
	withdraw()
	select {
	case err := <-done:
		wantNotPermitted(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("the request in flight was not cancelled")
	}
	conns := srv.conns.Load()
	if _, err := post(t, c, srv.URL, "next"); err == nil {
		t.Fatal("a request after withdrawal succeeded")
	} else {
		wantNotPermitted(t, err)
	}
	if srv.conns.Load() != conns || srv.requests.Load() != 1 {
		t.Fatalf("connections %d -> %d, requests %d: the refused request dialled", conns, srv.conns.Load(), srv.requests.Load())
	}
	if h := svc.Handed(); len(h) != 0 {
		t.Fatalf("Handed() = %+v after withdrawal, want none", h)
	}
}

// A redirect is not followed by default.
func TestRedirectNotFollowed(t *testing.T) {
	srv := newCountingServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://elsewhere.invalid/", http.StatusFound)
	})
	svc := egress.New(egress.Config{})
	t.Cleanup(svc.CloseIdleConnections)
	c, err := svc.Client("model-request", fakePermit{kind: "model-request", dest: srv.URL, ctx: context.Background()}, egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := post(t, c, srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusFound || c.Jar != nil {
		t.Fatalf("status = %d, jar = %v; want 302 returned and no cookie jar", resp.StatusCode, c.Jar)
	}
}

func TestAllows(t *testing.T) {
	for _, tc := range []struct {
		dest, target string
		want         bool
	}{
		{"https://api.deepseek.com", "https://API.deepseek.com/v1/chat", true},
		{"https://api.deepseek.com:443/v1", "https://api.deepseek.com/chat", true},
		{"https://api.deepseek.com", "http://api.deepseek.com/", false},
		{"https://api.deepseek.com", "https://api.deepseek.com:8443/", false},
		{"https://api.deepseek.com", "https://evil.example/", false},
		{"127.0.0.1:8080", "http://127.0.0.1:8080/x", true},
		{"127.0.0.1:8080", "http://127.0.0.1:9090/x", false},
		{"api.deepseek.com", "https://api.deepseek.com:8443/", true},
		{"[::1]", "http://[::1]:80/", true},
		{"", "https://api.deepseek.com/", false},
	} {
		u, err := url.Parse(tc.target)
		if err != nil {
			t.Fatal(err)
		}
		if got := egress.Allows(tc.dest, u); got != tc.want {
			t.Errorf("Allows(%q, %q) = %v, want %v", tc.dest, tc.target, got, tc.want)
		}
	}
}
