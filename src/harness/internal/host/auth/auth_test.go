package auth_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/access"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/auth"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
)

var bg = context.Background()

func openDB(t *testing.T) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(bg, filepath.Join(t.TempDir(), "dsh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(bg) })
	return s
}

func use(t *testing.T, r *cordis.Context, c *cordis.Component, cfg any) {
	t.Helper()
	f, err := r.Use(c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Dispose)
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
}

func apply(t *testing.T, r *cordis.Context, name string, body func(ctx *cordis.Context) error, inject ...string) {
	t.Helper()
	f, err := r.Apply(name, body, inject...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.Dispose)
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
}

// host is a listener on 127.0.0.1:0 with host.auth over a SQLite home and a Required
// mount at /api that records the peers it sees.
type host struct {
	l     listen.Listener
	svc   auth.Service
	db    *sqlite.Store
	mu    sync.Mutex
	peers []listen.Peer
}

func (h *host) calls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.peers)
}

func (h *host) last() listen.Peer {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.peers[len(h.peers)-1]
}

func newHost(t *testing.T) *host {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	h := &host{db: openDB(t)}
	if _, err := kv.DBKey.Provide(r, kv.DB(h.db)); err != nil {
		t.Fatal(err)
	}
	use(t, r, listen.Component, listen.Config{Port: new(uint16(0))})
	use(t, r, auth.Component, auth.Config{})
	apply(t, r, "mount api", func(ctx *cordis.Context) (err error) {
		if h.l, err = listen.Key.Get(ctx); err != nil {
			return err
		}
		if h.svc, err = auth.Key.Get(ctx); err != nil {
			return err
		}
		return h.l.Mount(ctx, listen.Mount{Prefix: "/api", Serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p, _ := listen.PeerFrom(r.Context())
			h.mu.Lock()
			h.peers = append(h.peers, p)
			h.mu.Unlock()
			w.Write([]byte(p.Principal().Kind))
		})})
	}, listen.Key.Name(), auth.Key.Name())
	return h
}

// browser is a client that follows no redirect and holds no cookie jar: each test
// sends exactly the cookies it means to.
func browser(t *testing.T) *http.Client {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

func send(t *testing.T, c *http.Client, method, url string, cookies []*http.Cookie, header ...string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, ck := range cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func (h *host) signIn(t *testing.T, c *http.Client) *http.Cookie {
	t.Helper()
	link, err := h.svc.Mint()
	if err != nil {
		t.Fatal(err)
	}
	resp := send(t, c, "GET", link, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("exchange: %d, want 303", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name == auth.CookieName(h.l.Origin()) {
			return ck
		}
	}
	t.Fatalf("exchange set no session cookie: %v", resp.Header.Values("Set-Cookie"))
	return nil
}

// TC-10b: the launch token is one-time. The first exchange answers 303 to ./ with
// no-store, no-referrer and a cookie HttpOnly, SameSite=Strict, Path=/ and not
// Secure on plain HTTP, and expires a stale harness cookie of another audience; the
// second exchange of the same token is 401. The cookie then admits as the owner, and
// the session table holds only its hash.
//
//dsh:test DH-T-HOST-10
//dsh:test DH-T-HOST-11
func TestLaunchTokenIsOneTime(t *testing.T) {
	h := newHost(t)
	c := browser(t)
	link, err := h.svc.Mint()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, h.l.Origin()+"/?token=") {
		t.Fatalf("Mint = %q, want the origin with a token", link)
	}
	stale := &http.Cookie{Name: auth.CookiePrefix + "0123456789abcdef", Value: "old"}
	first := send(t, c, "GET", link, []*http.Cookie{stale})
	if first.StatusCode != http.StatusSeeOther || first.Header.Get("Location") != "./" {
		t.Fatalf("first exchange: %d to %q, want 303 to ./", first.StatusCode, first.Header.Get("Location"))
	}
	if first.Header.Get("Cache-Control") != "no-store" || first.Header.Get("Referrer-Policy") != "no-referrer" {
		t.Errorf("first exchange headers: %v", first.Header)
	}
	var session *http.Cookie
	expired := false
	for _, ck := range first.Cookies() {
		switch ck.Name {
		case auth.CookieName(h.l.Origin()):
			session = ck
		case stale.Name:
			expired = ck.MaxAge < 0
		}
	}
	if session == nil || !session.HttpOnly || session.SameSite != http.SameSiteStrictMode || session.Path != "/" || session.Secure {
		t.Fatalf("session cookie %+v: want HttpOnly, SameSite=Strict, Path=/, not Secure", session)
	}
	if !expired {
		t.Errorf("the stale cookie of another audience was not expired: %v", first.Header.Values("Set-Cookie"))
	}

	if second := send(t, c, "GET", link, nil); second.StatusCode != http.StatusUnauthorized {
		t.Errorf("second exchange: %d, want 401", second.StatusCode)
	}

	if resp := send(t, c, "GET", h.l.Origin()+"/api", []*http.Cookie{session}); resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api with the cookie: %d, want 200", resp.StatusCode)
	}
	if p := h.last(); !p.Principal().IsOwner() || p.ID() == "" {
		t.Errorf("peer %q principal %+v, want the owner", p.ID(), p.Principal())
	}

	var stored []string
	if err := h.db.ReadTx(bg, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(bg, `SELECT key, value FROM kv WHERE namespace = ?`, auth.Namespace)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var k, v string
			if err := rows.Scan(&k, &v); err != nil {
				return err
			}
			stored = append(stored, k, v)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	if len(stored) != 2 {
		t.Fatalf("session table holds %d rows, want 1", len(stored)/2)
	}
	for _, s := range stored {
		if strings.Contains(s, session.Value) {
			t.Errorf("the session table holds the cookie value itself")
		}
	}
	if resp := send(t, c, "GET", h.l.Origin()+"/api", []*http.Cookie{{Name: session.Name, Value: stored[0]}}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("the stored hash used as the cookie: %d, want 401", resp.StatusCode)
	}
}

// TC-10b: without a cookie, with a cookie that matches no row, and with a valid
// cookie but a foreign Origin, the request is refused 401 or 403 and reaches no mount.
//
//dsh:test DH-T-HOST-11
func TestRefusalsReachNoMount(t *testing.T) {
	h := newHost(t)
	c := browser(t)
	session := h.signIn(t, c)
	api := h.l.Origin() + "/api"
	cases := []struct {
		name    string
		cookies []*http.Cookie
		header  []string
		want    int
	}{
		{"no cookie", nil, nil, http.StatusUnauthorized},
		{"unknown cookie", []*http.Cookie{{Name: session.Name, Value: strings.Repeat("A", 43)}}, nil, http.StatusUnauthorized},
		{"garbage cookie", []*http.Cookie{{Name: session.Name, Value: "x"}}, nil, http.StatusUnauthorized},
		{"foreign origin", []*http.Cookie{session}, []string{"Origin", "http://evil.example"}, http.StatusForbidden},
		{"cross-site", []*http.Cookie{session}, []string{"Sec-Fetch-Site", "cross-site"}, http.StatusForbidden},
	}
	for _, tc := range cases {
		if resp := send(t, c, "GET", api, tc.cookies, tc.header...); resp.StatusCode != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, resp.StatusCode, tc.want)
		}
	}
	if n := h.calls(); n != 0 {
		t.Errorf("refused requests reached the mount %d times", n)
	}
	if resp := send(t, c, "GET", api, []*http.Cookie{session}); resp.StatusCode != http.StatusOK {
		t.Errorf("the valid cookie alone: %d, want 200", resp.StatusCode)
	}
}

// Two browsers are two peers with different form tokens; sign-out ends one
// browser's peer context and refuses its cookie; an issued cookie is a peer of its
// principal; revoke-all refuses every cookie; revoke-all is the owner's only.
//
//dsh:test DH-T-HOST-12
func TestSignOutAndRevokeAll(t *testing.T) {
	h := newHost(t)
	c := browser(t)
	a, b := h.signIn(t, c), h.signIn(t, c)
	api, origin := h.l.Origin()+"/api", []string{"Origin", h.l.Origin()}
	send(t, c, "GET", api, []*http.Cookie{a})
	pa := h.last()
	send(t, c, "GET", api, []*http.Cookie{a})
	if h.last().FormToken() != pa.FormToken() {
		t.Error("one peer's form token changed between requests")
	}
	send(t, c, "GET", api, []*http.Cookie{b})
	pb := h.last()
	if pa.ID() == pb.ID() || pa.FormToken() == pb.FormToken() {
		t.Fatalf("two browsers share peer %q or a form token", pa.ID())
	}

	guest, err := h.svc.Issue(bg, access.Principal{Kind: "guest", ID: "g1"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.svc.Issue(bg, access.Owner(), time.Hour); err == nil {
		t.Error("Issue made an owner session")
	}
	if resp := send(t, c, "GET", api, []*http.Cookie{guest}); resp.StatusCode != http.StatusOK || h.last().Principal().Kind != "guest" {
		t.Errorf("issued cookie: %d as %+v, want 200 as guest", resp.StatusCode, h.last().Principal())
	}
	if send(t, c, "GET", api, []*http.Cookie{guest, b}); !h.last().Principal().IsOwner() {
		t.Error("a browser holding both cookies was not admitted as the owner")
	}
	if resp := send(t, c, "POST", h.l.Origin()+auth.RevokeAllPath, []*http.Cookie{guest}, origin...); resp.StatusCode != http.StatusForbidden {
		t.Errorf("revoke-all by a guest: %d, want 403", resp.StatusCode)
	}

	if resp := send(t, c, "POST", h.l.Origin()+auth.SignOutPath, []*http.Cookie{a}); resp.StatusCode != http.StatusForbidden {
		t.Errorf("sign-out without a same-origin proof: %d, want 403", resp.StatusCode)
	}
	if resp := send(t, c, "POST", h.l.Origin()+auth.SignOutPath, []*http.Cookie{a}, origin...); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("sign-out: %d, want 204", resp.StatusCode)
	}
	select {
	case <-pa.Context().Done():
	case <-time.After(5 * time.Second):
		t.Error("the signed-out peer's context did not end")
	}
	if pb.Context().Err() != nil {
		t.Error("signing one browser out ended the other's peer")
	}
	if resp := send(t, c, "GET", api, []*http.Cookie{a}); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("signed-out cookie: %d, want 401", resp.StatusCode)
	}
	if resp := send(t, c, "GET", api, []*http.Cookie{b}); resp.StatusCode != http.StatusOK {
		t.Errorf("the other browser after sign-out: %d, want 200", resp.StatusCode)
	}

	if resp := send(t, c, "POST", h.l.Origin()+auth.RevokeAllPath, []*http.Cookie{b}, "Sec-Fetch-Site", "same-origin"); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("revoke-all: %d, want 204", resp.StatusCode)
	}
	if pb.Context().Err() == nil {
		t.Error("revoke-all left a peer's context open")
	}
	for _, ck := range []*http.Cookie{b, guest} {
		if resp := send(t, c, "GET", api, []*http.Cookie{ck}); resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("cookie %s after revoke-all: %d, want 401", ck.Name, resp.StatusCode)
		}
	}
}

// A token is refused after its TTL; a sign-in is announced on the terminal and to
// listeners; a cookie issued before a restart (a new Auth over the same database)
// still admits.
//
//dsh:test DH-T-HOST-10
//dsh:test DH-T-HOST-12
func TestTokenTTLNoticeAndRestart(t *testing.T) {
	db := openDB(t)
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	var term strings.Builder
	o := auth.Options{Audience: "http://127.0.0.1:3080", Notice: &term, Now: func() time.Time { return now }}
	a, err := auth.New(bg, db, o)
	if err != nil {
		t.Fatal(err)
	}
	var seen []auth.SignIn
	cancel := a.OnSignIn(func(s auth.SignIn) { seen = append(seen, s) })
	defer cancel()
	token := func() string {
		link, err := a.Mint()
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimPrefix(link, o.Audience+"/?token=")
	}
	creds := func(tok string, cookies ...*http.Cookie) *listen.Credentials {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("User-Agent", "TestBrowser/1.0")
		for _, ck := range cookies {
			r.AddCookie(ck)
		}
		return &listen.Credentials{Request: r, Token: tok, Audience: o.Audience}
	}

	late := token()
	now = now.Add(auth.DefaultLaunchTokenTTL)
	if _, ok, err := a.Exchange(creds(late)); ok || err != nil {
		t.Errorf("exchange after the TTL: ok %v, err %v; want refused", ok, err)
	}
	cookies, ok, err := a.Exchange(creds(token()))
	if !ok || err != nil {
		t.Fatalf("exchange: ok %v, err %v", ok, err)
	}
	if len(seen) != 1 || !seen[0].Principal.IsOwner() || seen[0].Label != "TestBrowser/1.0" {
		t.Errorf("sign-in notices %+v, want one for the owner from TestBrowser/1.0", seen)
	}
	if !strings.Contains(term.String(), "signed in as owner (TestBrowser/1.0)") || !strings.Contains(term.String(), auth.RevokeAllPath) {
		t.Errorf("terminal notice %q, want the sign-in beside the revoke-all action", term.String())
	}
	if _, ok, _ := a.Authenticate(creds("", cookies[0])); !ok {
		t.Fatal("the new cookie did not admit")
	}
	other := creds("", cookies[0])
	other.Audience = "http://127.0.0.1:3081"
	if _, ok, _ := a.Authenticate(other); ok {
		t.Error("a cookie admitted for another audience")
	}
	a.Close()

	restarted, err := auth.New(bg, db, o)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if _, ok, err := restarted.Authenticate(creds("", cookies[0])); !ok || err != nil {
		t.Errorf("a cookie issued before the restart: ok %v, err %v; want admitted", ok, err)
	}
	now = now.Add(auth.DefaultCookieMaxAge)
	if _, ok, _ := restarted.Authenticate(creds("", cookies[0])); ok {
		t.Error("an expired session admitted")
	}
}
