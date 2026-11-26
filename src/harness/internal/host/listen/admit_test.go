package listen_test

import (
	"net"
	"net/http"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
)

// do sends method path to l with the given headers ("Host" sets the request's Host)
// and returns the status.
func do(t *testing.T, c *http.Client, l listen.Listener, method, path string, header ...string) int {
	t.Helper()
	req, err := http.NewRequest(method, l.Origin()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(header); i += 2 {
		if header[i] == "Host" {
			req.Host = header[i+1]
			continue
		}
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TC-10b: the source table. Loopback names, the bind literal on any port and a
// trusted host pass; a rebinding name, 127.1, Origin null, a mismatched Origin,
// cross-site, and a non-GET that is not same-origin are 403 before any credential is
// read, so neither the authenticator nor the mount runs.
//
//dsh:test DH-T-HOST-9
func TestSourceAndOriginRefusals(t *testing.T) {
	r := cordisRoot(t)
	l, err := start(t, r, listen.Config{Port: anyPort(), TrustedHosts: []string{"dsh.test"}})
	if err != nil {
		t.Fatal(err)
	}
	admit(t, r, true)
	h := &recorder{name: "api"}
	if err := mount(t, r, l, "api", listen.Mount{Prefix: "/api", Serve: h}); err != nil {
		t.Fatal(err)
	}
	c := client(t)
	self := strings.TrimPrefix(l.Origin(), "http://")
	_, port, _ := net.SplitHostPort(self)
	pass := [][]string{
		{"GET"},
		{"GET", "Host", "localhost:" + port},
		{"GET", "Host", "LOCALHOST.:" + port},
		{"GET", "Host", "127.0.0.1:1"},
		{"GET", "Host", "dsh.test"},
		{"GET", "Sec-Fetch-Site", "same-site"},
		{"GET", "Origin", l.Origin()},
		{"POST", "Sec-Fetch-Site", "same-origin"},
		{"POST", "Origin", l.Origin()},
	}
	for _, p := range pass {
		if code := do(t, c, l, p[0], "/api", p[1:]...); code != http.StatusOK {
			t.Errorf("%v: %d, want 200", p, code)
		}
	}
	refused := [][]string{
		{"GET", "Host", "evil.example:" + port},
		{"GET", "Host", "127.1:" + port},
		{"POST", "Host", "evil.example:" + port},
		{"GET", "Origin", "null"},
		{"POST", "Origin", "null"},
		{"GET", "Origin", "http://evil.example"},
		{"POST", "Origin", "http://evil.example"},
		{"GET", "Sec-Fetch-Site", "cross-site"},
		{"POST", "Sec-Fetch-Site", "cross-site"},
		// Same-site is not enough for a state change: another localhost port.
		{"POST", "Sec-Fetch-Site", "same-site"},
		{"POST"},
		{"GET", "Upgrade", "websocket", "Connection", "Upgrade"},
	}
	before := h.calls.Load()
	for _, p := range refused {
		if code := do(t, c, l, p[0], "/api", p[1:]...); code != http.StatusForbidden {
			t.Errorf("%v: %d, want 403", p, code)
		}
	}
	if n := h.calls.Load() - before; n != 0 {
		t.Errorf("refused requests reached the mount %d times", n)
	}
}

// TC-10b: a connection from a source that is neither loopback nor an allowed peer is
// refused 403 and reaches no mount, whatever it carries; the same source inside
// AllowedPeers is admitted.
//
//dsh:test DH-T-HOST-9
func TestForeignSourceRefused(t *testing.T) {
	restore := listen.SpoofSource(&net.TCPAddr{IP: net.ParseIP("192.0.2.7"), Port: 40000})
	defer restore()
	for _, allowed := range [][]string{nil, {"10.0.0.0/8"}, {"192.0.2.0/24"}, {"192.0.2.7"}} {
		r := cordisRoot(t)
		l, err := start(t, r, listen.Config{Port: anyPort(), AllowedPeers: allowed})
		if err != nil {
			t.Fatal(err)
		}
		admit(t, r, true)
		h := &recorder{name: "api"}
		if err := mount(t, r, l, "api", listen.Mount{Prefix: "/api", Serve: h}); err != nil {
			t.Fatal(err)
		}
		want := http.StatusForbidden
		if len(allowed) > 0 && allowed[0] != "10.0.0.0/8" {
			want = http.StatusOK
		}
		if code := do(t, client(t), l, "GET", "/api"); code != want {
			t.Errorf("allowed peers %v: %d, want %d", allowed, code, want)
		}
		if want == http.StatusForbidden && h.calls.Load() != 0 {
			t.Errorf("allowed peers %v: a refused source reached the mount", allowed)
		}
	}
	if _, err := start(t, cordisRoot(t), listen.Config{Port: anyPort(), AllowedPeers: []string{"not-an-ip"}}); err == nil {
		t.Error("an allowed peer that is not an IP or prefix started the listener")
	}
}

// TC-10b: a token parameter anywhere but `GET /` is 400 and reaches no mount.
//
//dsh:test DH-T-HOST-10
func TestTokenParameterRule(t *testing.T) {
	r := cordisRoot(t)
	l, err := start(t, r, listen.Config{Port: anyPort()})
	if err != nil {
		t.Fatal(err)
	}
	admit(t, r, true)
	h := &recorder{name: "api"}
	if err := mount(t, r, l, "api", listen.Mount{Prefix: "/api", Serve: h}); err != nil {
		t.Fatal(err)
	}
	c := client(t)
	for _, p := range []string{"/api?token=x", "/?token=a&token=b", "/?token=", "/api?a=1&token=x"} {
		if code := do(t, c, l, "GET", p); code != http.StatusBadRequest {
			t.Errorf("GET %s: %d, want 400", p, code)
		}
	}
	if code := do(t, c, l, "POST", "/?token=x", "Origin", l.Origin()); code != http.StatusBadRequest {
		t.Errorf("POST /?token=x: %d, want 400", code)
	}
	// No answerer of listen/exchange: the token is not accepted.
	if code := do(t, c, l, "GET", "/?token=x"); code != http.StatusUnauthorized {
		t.Errorf("GET /?token=x with no exchange: %d, want 401", code)
	}
	if h.calls.Load() != 0 {
		t.Errorf("a token request reached the mount")
	}
}
