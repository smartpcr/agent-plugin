package listen_test

import (
	"errors"
	"net"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
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
	for _, p := range []string{"/api?token=x", "/?token=a&token=b", "/?token=", "/api?a=1&token=x",
		"/api?%74oken=x", "/api?%54OKEN=x", "/?%74oken=a&token=b", "/?Token=x", "/api?a=1&%74%6f%6b%65%6e=x", "/api?%zz=1", "/?token=%zz"} {
		if code := do(t, c, l, "GET", p); code != http.StatusBadRequest {
			t.Errorf("GET %s: %d, want 400", p, code)
		}
	}
	if code := do(t, c, l, "GET", "/api?tokens=x&a=token"); code != http.StatusOK {
		t.Errorf("GET /api?tokens=x&a=token: %d, want 200: neither key is token", code)
	}
	before := h.calls.Load()
	if code := do(t, c, l, "POST", "/?token=x", "Origin", l.Origin()); code != http.StatusBadRequest {
		t.Errorf("POST /?token=x: %d, want 400", code)
	}
	// No answerer of listen/exchange: the token is not accepted.
	if code := do(t, c, l, "GET", "/?token=x"); code != http.StatusUnauthorized {
		t.Errorf("GET /?token=x with no exchange: %d, want 401", code)
	}
	if h.calls.Load() != before {
		t.Errorf("a token request reached the mount")
	}
}

// fakeTrees answers every lookup with tree (or err) and counts the lookups.
type fakeTrees struct {
	tree  subprocess.Tree
	err   error
	calls atomic.Int32
}

func (f *fakeTrees) PeerTree(net.Conn) (subprocess.Tree, error) {
	f.calls.Add(1)
	return f.tree, f.err
}

// peerRuleHost starts a listener whose peer rule asks trees, with a mount at /api,
// an admit-all authenticator and an exchange answerer, each counting its calls.
func peerRuleHost(t *testing.T, trees listen.Trees) (l listen.Listener, mounted, authed, exchanged *atomic.Int32) {
	t.Helper()
	if trees != nil {
		defer listen.UseTrees(trees)()
	}
	r := cordisRoot(t)
	l, err := start(t, r, listen.Config{Port: anyPort()})
	if err != nil {
		t.Fatal(err)
	}
	authed, exchanged = &atomic.Int32{}, &atomic.Int32{}
	if err := load(t, r, "counting auth", func(ctx *cordis.Context) error {
		if _, err := listen.Authenticate.On(ctx, func(*cordis.Context, *listen.Credentials) (listen.Peer, bool, error) {
			authed.Add(1)
			return peer{id: "p1"}, true, nil
		}); err != nil {
			return err
		}
		_, err := listen.Exchange.On(ctx, func(*cordis.Context, *listen.Credentials) ([]*http.Cookie, bool, error) {
			exchanged.Add(1)
			return []*http.Cookie{{Name: "c", Value: "v"}}, true, nil
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	h := &recorder{name: "api"}
	if err := mount(t, r, l, "api", listen.Mount{Prefix: "/api", Serve: h}); err != nil {
		t.Fatal(err)
	}
	return l, &h.calls, authed, exchanged
}

// TC-10b, DH-ADR-HOST-12: with a fake tree lookup, a local peer inside an untrusted
// (model-started) tree is refused 403 auth/untrusted-peer on the token exchange and
// on a Required request, before any credential is read: neither the exchange, the
// authenticator nor the mount runs. The tree is asked once per accepted connection.
// A trusted helper and a process outside every tree pass; a best-effort answer makes
// PeerRule BestEffort; a peer that cannot be named is served and makes it NotApplied.
// Each case runs as on an exact tree owner (job object, control group) and as on the
// process-group fallback: the reported rule is the weaker of the owner and the answers.
//
//dsh:test DH-T-HOST-31
func TestPeerRule(t *testing.T) {
	for _, owner := range []struct {
		name  string
		exact bool
		rule  listen.PeerRule
	}{
		{"exact owner", true, listen.Exact},
		{"process-group fallback", false, listen.BestEffort},
	} {
		t.Run(owner.name, func(t *testing.T) {
			defer listen.UseOwnerExact(owner.exact)()
			peerRuleCases(t, owner.rule)
		})
	}
}

// weaker returns the weaker of two peer rules.
func weaker(a, b listen.PeerRule) listen.PeerRule {
	order := map[listen.PeerRule]int{listen.NotApplied: 0, listen.BestEffort: 1, listen.Exact: 2}
	if order[a] < order[b] {
		return a
	}
	return b
}

// peerRuleCases runs the fake-tree cases on a host whose tree owner allows at most
// ownerRule.
func peerRuleCases(t *testing.T, ownerRule listen.PeerRule) {
	untrusted := &fakeTrees{tree: subprocess.Tree{Trust: subprocess.Untrusted, Exact: true, Peer: 4242, Root: 4240, Owner: subprocess.JobObject}}
	l, mounted, authed, exchanged := peerRuleHost(t, untrusted)
	c := client(t)
	for _, p := range []string{"/api", "/?token=x", "/api"} {
		code, body := get(t, c, l.Origin()+p)
		if code != http.StatusForbidden || strings.TrimSpace(body) != string(listen.ReasonUntrustedPeer) {
			t.Errorf("untrusted peer GET %s: %d %q, want 403 %s", p, code, body, listen.ReasonUntrustedPeer)
		}
	}
	if n := mounted.Load() + authed.Load() + exchanged.Load(); n != 0 {
		t.Errorf("an untrusted peer reached the mount, authenticator or exchange %d times", n)
	}
	if n := untrusted.calls.Load(); n != 1 {
		t.Errorf("the tree was asked %d times over one connection, want once", n)
	}
	if want := weaker(ownerRule, listen.Exact); l.PeerRule() != want {
		t.Errorf("PeerRule = %s after exact answers, want %s", l.PeerRule(), want)
	}

	for _, tc := range []struct {
		name   string
		trees  *fakeTrees
		answer listen.PeerRule
	}{
		{"trusted helper", &fakeTrees{tree: subprocess.Tree{Trust: subprocess.Trusted, Exact: true}}, listen.Exact},
		{"outside every tree", &fakeTrees{tree: subprocess.Tree{Trust: subprocess.Outside, Exact: true}}, listen.Exact},
		{"best effort", &fakeTrees{tree: subprocess.Tree{Trust: subprocess.Outside, Exact: false}}, listen.BestEffort},
		{"cannot be named", &fakeTrees{err: errors.New("subprocess/peer-unknown")}, listen.NotApplied},
	} {
		l, mounted, _, exchanged := peerRuleHost(t, tc.trees)
		c := client(t)
		c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if code, _ := get(t, c, l.Origin()+"/api"); code != http.StatusOK || mounted.Load() != 1 {
			t.Errorf("%s: GET /api = %d, want 200 from the mount", tc.name, code)
		}
		if code, _ := get(t, c, l.Origin()+"/?token=x"); code != http.StatusSeeOther || exchanged.Load() != 1 {
			t.Errorf("%s: exchange = %d, want 303", tc.name, code)
		}
		if want := weaker(ownerRule, tc.answer); l.PeerRule() != want {
			t.Errorf("%s: PeerRule = %s, want %s", tc.name, l.PeerRule(), want)
		}
	}

	if l, _, _, _ := peerRuleHost(t, nil); l.PeerRule() != listen.NotApplied {
		t.Errorf("no tree registry: PeerRule = %s, want %s", l.PeerRule(), listen.NotApplied)
	}
}

// With the real process registry, the test's own connection is outside every tree
// and served; PeerRule reports what this machine can tell, never more than its tree
// owner allows: exact only on an exact owner, at most best effort on the fallback.
//
//dsh:test DH-T-HOST-31
func TestPeerRuleWithTheProcessRegistry(t *testing.T) {
	l, mounted, _, _ := peerRuleHost(t, subprocess.NewRegistry())
	if code, _ := get(t, client(t), l.Origin()+"/api"); code != http.StatusOK || mounted.Load() != 1 {
		t.Errorf("own connection: GET /api = %d, want 200", code)
	}
	owner := subprocess.TreeOwner()
	t.Logf("peer rule on %s: %s (tree owner %s)", runtime.GOOS, l.PeerRule(), owner)
	if !owner.Exact && l.PeerRule() == listen.Exact {
		t.Errorf("PeerRule = %s on a best-effort tree owner %s", l.PeerRule(), owner)
	}
	if runtime.GOOS == "windows" && l.PeerRule() != listen.Exact {
		t.Errorf("PeerRule on Windows = %s, want %s: a job object is exact and loopback TCP peers are named", l.PeerRule(), listen.Exact)
	}
}
