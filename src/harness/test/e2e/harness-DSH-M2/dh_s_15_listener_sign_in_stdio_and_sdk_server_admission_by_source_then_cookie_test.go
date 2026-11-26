//go:build e2e

package e2e

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/auth"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/kv"
)

// abscHost is one listener with host.auth over its own SQLite file and a Required
// mount at /api that counts the requests reaching it and answers the peer's kind.
type abscHost struct {
	root   *cordis.Context
	fibers []*cordis.Fiber
	db     *sqlite.Store
	l      listen.Listener
	svc    auth.Service
	calls  atomic.Int64
}

func (h *abscHost) close() {
	for i := len(h.fibers) - 1; i >= 0; i-- {
		h.fibers[i].Dispose()
	}
	if h.root != nil {
		h.root.Dispose()
	}
	if h.db != nil {
		_ = h.db.Close(context.Background())
	}
}

func (h *abscHost) keep(f *cordis.Fiber, err error) error {
	if err != nil {
		return err
	}
	h.fibers = append(h.fibers, f)
	return f.Wait()
}

func abscStart(dir string, cfg listen.Config) (*abscHost, error) {
	h := &abscHost{root: cordis.New()}
	h.root.Loggers().ClearExporters()
	h.root.Loggers().AddExporter(&cordis.MemoryExporter{})
	var err error
	if h.db, err = sqlite.Open(context.Background(), filepath.Join(dir, "dsh.db")); err != nil {
		return h, fmt.Errorf("open the SQLite home: %w", err)
	}
	if _, err := kv.DBKey.Provide(h.root, kv.DB(h.db)); err != nil {
		return h, err
	}
	var port uint16
	cfg.Port = &port
	cfg.ShutdownGrace = 2 * time.Second
	if err := h.keep(h.root.Use(listen.Component, cfg)); err != nil {
		return h, fmt.Errorf("load host.listen: %w", err)
	}
	if err := h.keep(h.root.Use(auth.Component, auth.Config{})); err != nil {
		return h, fmt.Errorf("load host.auth: %w", err)
	}
	err = h.keep(h.root.Apply("mount api", func(ctx *cordis.Context) (err error) {
		if h.l, err = listen.Key.Get(ctx); err != nil {
			return err
		}
		if h.svc, err = auth.Key.Get(ctx); err != nil {
			return err
		}
		return h.l.Mount(ctx, listen.Mount{Prefix: "/api", Serve: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h.calls.Add(1)
			p, ok := listen.PeerFrom(r.Context())
			if !ok || p == nil {
				http.Error(w, "the mount ran without a peer", http.StatusInternalServerError)
				return
			}
			io.WriteString(w, p.Principal().Kind)
		})})
	}, listen.Key.Name(), auth.Key.Name()))
	return h, err
}

// rows reads the session table's raw key/value pairs straight from SQLite.
func (h *abscHost) rows() ([][2]string, error) {
	var out [][2]string
	err := h.db.ReadTx(context.Background(), func(tx *sql.Tx) error {
		rs, err := tx.QueryContext(context.Background(), `SELECT key, value FROM kv WHERE namespace = ?`, auth.Namespace)
		if err != nil {
			return err
		}
		defer rs.Close()
		for rs.Next() {
			var k, v string
			if err := rs.Scan(&k, &v); err != nil {
				return err
			}
			out = append(out, [2]string{k, v})
		}
		return rs.Err()
	})
	return out, err
}

// abscLocalIP is a non-loopback address of this machine: IPv4 first, then IPv6
// that is not link-local (a link-local one needs a zone the listener drops).
func abscLocalIP() (net.IP, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, err
	}
	var v6 net.IP
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok || n.IP.IsLoopback() || n.IP.IsLinkLocalUnicast() || n.IP.IsUnspecified() || n.IP.IsMulticast() {
			continue
		}
		if ip4 := n.IP.To4(); ip4 != nil {
			return ip4, nil
		}
		if v6 == nil {
			v6 = n.IP
		}
	}
	if v6 != nil {
		return v6, nil
	}
	return nil, errors.New("this machine has no non-loopback address to present a foreign source from")
}

type abscState struct {
	dir    string
	main   *abscHost
	second *abscHost
	third  *abscHost
	lan    net.IP

	link    string
	first   *http.Response
	again   *http.Response
	session *http.Cookie

	status int
	body   string
	cookie []*http.Cookie
}

func (s *abscState) close() {
	for _, h := range []*abscHost{s.third, s.second, s.main} {
		if h != nil {
			h.close()
		}
	}
	if s.dir != "" {
		os.RemoveAll(s.dir)
	}
}

func (s *abscState) subdir(name string) (string, error) {
	d := filepath.Join(s.dir, name)
	return d, os.MkdirAll(d, 0o700)
}

// client follows no redirect and keeps no cookie jar; from, when set, is the local
// address every connection is dialled from.
func abscClient(from net.IP) *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second}
	if from != nil {
		d.LocalAddr = &net.TCPAddr{IP: from}
	}
	return &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true, DialContext: d.DialContext},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (s *abscState) do(c *http.Client, method, url string, cookies []*http.Cookie, header ...string) error {
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		return err
	}
	for _, ck := range cookies {
		req.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	s.status, s.body, s.cookie = resp.StatusCode, strings.TrimSpace(string(b)), resp.Cookies()
	return nil
}

func (s *abscState) background(bind string) error {
	if ip := net.ParseIP(bind); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("bind %s: the main listener runs on loopback", bind)
	}
	var err error
	if s.dir, err = os.MkdirTemp("", "dsh-absc-e2e-"); err != nil {
		return err
	}
	d, err := s.subdir("main")
	if err != nil {
		return err
	}
	s.main, err = abscStart(d, listen.Config{Bind: bind})
	return err
}

func (s *abscState) mint() error {
	link, err := s.main.svc.Mint()
	if err != nil {
		return err
	}
	if !strings.HasPrefix(link, s.main.l.Origin()+"/?token=") {
		return fmt.Errorf("Mint = %q, want %s/?token=...", link, s.main.l.Origin())
	}
	s.link = link
	return nil
}

func (s *abscState) exchange() (*http.Response, error) {
	resp, err := abscClient(nil).Get(s.link)
	if err != nil {
		return nil, err
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp, nil
}

func (s *abscState) exchangeFirst() (err error) {
	s.first, err = s.exchange()
	if err != nil {
		return err
	}
	name := auth.CookieName(s.main.l.Origin())
	for _, ck := range s.first.Cookies() {
		if ck.Name == name {
			s.session = ck
		}
	}
	return nil
}

func (s *abscState) exchangeAgain() (err error) {
	s.again, err = s.exchange()
	return err
}

func (s *abscState) firstIs303(loc, cache string) error {
	if s.first.StatusCode != http.StatusSeeOther || s.first.Header.Get("Location") != loc {
		return fmt.Errorf("first exchange %d to %q, want 303 to %q", s.first.StatusCode, s.first.Header.Get("Location"), loc)
	}
	if got := s.first.Header.Get("Cache-Control"); got != cache {
		return fmt.Errorf("first exchange Cache-Control %q, want %q", got, cache)
	}
	return nil
}

func (s *abscState) cookieAttributes() error {
	c := s.session
	if c == nil {
		return fmt.Errorf("the first exchange set no %s cookie: %v", auth.CookieName(s.main.l.Origin()), s.first.Header.Values("Set-Cookie"))
	}
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Secure || c.MaxAge <= 0 {
		return fmt.Errorf("session cookie %+v: want HttpOnly, SameSite=Strict, Path=/, not Secure, a positive Max-Age", c)
	}
	if b, err := base64.RawURLEncoding.DecodeString(c.Value); err != nil || len(b) != 32 {
		return fmt.Errorf("session cookie value %q: want 32 random bytes, base64url", c.Value)
	}
	return nil
}

func (s *abscState) oneHashedRow() error {
	rows, err := s.main.rows()
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("the session table holds %d rows, want 1", len(rows))
	}
	sum := sha256.Sum256([]byte(s.session.Value))
	if want := hex.EncodeToString(sum[:]); rows[0][0] != want {
		return fmt.Errorf("row key %q, want the SHA-256 of the cookie %q", rows[0][0], want)
	}
	if strings.Contains(rows[0][1], s.session.Value) || strings.Contains(rows[0][0], s.session.Value) {
		return errors.New("the session table holds the cookie value itself")
	}
	return nil
}

func (s *abscState) stillOneRow() error {
	rows, err := s.main.rows()
	if err != nil {
		return err
	}
	if len(rows) != 1 {
		return fmt.Errorf("the session table holds %d rows after the second exchange, want 1", len(rows))
	}
	return nil
}

func (s *abscState) againIs401(reason string) error {
	if s.again.StatusCode != http.StatusUnauthorized {
		return fmt.Errorf("second exchange %d, want 401", s.again.StatusCode)
	}
	if n := len(s.again.Cookies()); n != 0 {
		return fmt.Errorf("second exchange set %d cookies: %v", n, s.again.Header.Values("Set-Cookie"))
	}
	if loc := s.again.Header.Get("Location"); loc != "" {
		return fmt.Errorf("second exchange redirected to %q", loc)
	}
	// The body was drained in exchange; the reason is checked by sending it again.
	if err := s.do(abscClient(nil), "GET", s.link, nil); err != nil {
		return err
	}
	return s.responseIs(http.StatusUnauthorized, reason)
}

func (s *abscState) withCookieReaches(request, kind string) error {
	method, path, _ := strings.Cut(request, " ")
	if err := s.do(abscClient(nil), method, s.main.l.Origin()+path, []*http.Cookie{s.session}); err != nil {
		return err
	}
	if s.status != http.StatusOK || s.body != kind {
		return fmt.Errorf("%s with the session cookie: %d %q, want 200 %q", request, s.status, s.body, kind)
	}
	return nil
}

func (s *abscState) signedIn() error {
	if err := s.mint(); err != nil {
		return err
	}
	if err := s.exchangeFirst(); err != nil {
		return err
	}
	if err := s.firstIs303("./", "no-store"); err != nil {
		return err
	}
	return s.cookieAttributes()
}

func (s *abscState) send(request string, cookies []*http.Cookie, header ...string) error {
	method, path, ok := strings.Cut(request, " ")
	if !ok {
		return fmt.Errorf("request %q: want METHOD PATH", request)
	}
	return s.do(abscClient(nil), method, s.main.l.Origin()+path, cookies, header...)
}

func (s *abscState) sendNoCookie(request string) error { return s.send(request, nil) }

func (s *abscState) sendUnknownCookie(request string) error {
	forged := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	return s.send(request, []*http.Cookie{{Name: auth.CookieName(s.main.l.Origin()), Value: forged}})
}

func (s *abscState) sendWithOrigin(request, origin string) error {
	return s.send(request, []*http.Cookie{s.session}, "Origin", origin)
}

func (s *abscState) sendWithFetchSite(request, site string) error {
	return s.send(request, []*http.Cookie{s.session}, "Sec-Fetch-Site", site)
}

func (s *abscState) sendWithSession(request string) error {
	return s.send(request, []*http.Cookie{s.session})
}

func (s *abscState) responseIs(code int, reason string) error {
	if s.status != code || s.body != reason {
		return fmt.Errorf("response %d %q, want %d %q", s.status, s.body, code, reason)
	}
	return nil
}

func (s *abscState) responseBody(code int, body string) error { return s.responseIs(code, body) }

func abscReached(h *abscHost, want int) error {
	if h == nil {
		return errors.New("that listener was not started")
	}
	if got := h.calls.Load(); got != int64(want) {
		return fmt.Errorf("the mount was reached %d times, want %d", got, want)
	}
	return nil
}

func (s *abscState) startLAN(name string, peers []string) (*abscHost, error) {
	if s.lan == nil {
		ip, err := abscLocalIP()
		if err != nil {
			return nil, err
		}
		s.lan = ip
	}
	d, err := s.subdir(name)
	if err != nil {
		return nil, err
	}
	return abscStart(d, listen.Config{Bind: s.lan.String(), AllowInsecureRemote: true, AllowedPeers: peers})
}

func (s *abscState) secondListener(peers string) (err error) {
	list := strings.Split(peers, ",")
	if s.lan == nil {
		if s.lan, err = abscLocalIP(); err != nil {
			return err
		}
	}
	for _, p := range list {
		if _, n, err := net.ParseCIDR(p); err == nil && n.Contains(s.lan) {
			return fmt.Errorf("this machine's address %s is inside the allowed peers %s; the source would not be foreign", s.lan, p)
		}
	}
	s.second, err = s.startLAN("second", list)
	return err
}

func (s *abscState) thirdListener() (err error) {
	if s.lan == nil {
		if s.lan, err = abscLocalIP(); err != nil {
			return err
		}
	}
	s.third, err = s.startLAN("third", []string{s.lan.String()})
	return err
}

func (s *abscState) fromLAN(h *abscHost, request string, cookies []*http.Cookie) error {
	if h == nil {
		return errors.New("that listener was not started")
	}
	method, path, _ := strings.Cut(request, " ")
	return s.do(abscClient(s.lan), method, h.l.Origin()+path, cookies)
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_admission_by_source_then_cookie(ctx *godog.ScenarioContext) {
	var s *abscState
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &abscState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, nil
	})
	ctx.Step(`^host\.listen on "([^"]*)" with host\.auth over a SQLite session table and a Required mount at "/api"$`, func(b string) error { return s.background(b) })
	ctx.Step(`^a launch token minted by host\.auth$`, func() error { return s.mint() })
	ctx.Step(`^the launch token is exchanged$`, func() error { return s.exchangeFirst() })
	ctx.Step(`^the launch token is exchanged again$`, func() error { return s.exchangeAgain() })
	ctx.Step(`^the exchange answers 303 to "([^"]*)" with Cache-Control "([^"]*)"$`, func(l, c string) error { return s.firstIs303(l, c) })
	ctx.Step(`^the exchange sets the session cookie HttpOnly, SameSite=Strict, Path=/ and not Secure$`, func() error { return s.cookieAttributes() })
	ctx.Step(`^the SQLite session table holds exactly one row, keyed by the SHA-256 of the cookie and not holding the cookie itself$`, func() error { return s.oneHashedRow() })
	ctx.Step(`^the exchange answers 401 with reason "([^"]*)" and sets no cookie$`, func(r string) error { return s.againIs401(r) })
	ctx.Step(`^the SQLite session table still holds exactly one row$`, func() error { return s.stillOneRow() })
	ctx.Step(`^"([^"]*)" with the session cookie reaches the mount as the "([^"]*)"$`, func(r, k string) error { return s.withCookieReaches(r, k) })
	ctx.Step(`^a browser signed in with a launch token$`, func() error { return s.signedIn() })
	ctx.Step(`^"([^"]*)" is sent without a cookie$`, func(r string) error { return s.sendNoCookie(r) })
	ctx.Step(`^"([^"]*)" is sent with a cookie that matches no session row$`, func(r string) error { return s.sendUnknownCookie(r) })
	ctx.Step(`^"([^"]*)" is sent with the session cookie and Origin "([^"]*)"$`, func(r, o string) error { return s.sendWithOrigin(r, o) })
	ctx.Step(`^"([^"]*)" is sent with the session cookie and Sec-Fetch-Site "([^"]*)"$`, func(r, f string) error { return s.sendWithFetchSite(r, f) })
	ctx.Step(`^"([^"]*)" is sent with the session cookie$`, func(r string) error { return s.sendWithSession(r) })
	ctx.Step(`^the response is (\d+) with reason "([^"]*)"$`, func(c int, r string) error { return s.responseIs(c, r) })
	ctx.Step(`^the response is (\d+) with body "([^"]*)"$`, func(c int, b string) error { return s.responseBody(c, b) })
	ctx.Step(`^the mount at "/api" was reached (\d+) times$`, func(n int) error { return abscReached(s.main, n) })
	ctx.Step(`^the mount at "/api" of the second listener was reached (\d+) times$`, func(n int) error { return abscReached(s.second, n) })
	ctx.Step(`^a second host\.listen bound on this machine's non-loopback address with allowed peers "([^"]*)"$`, func(p string) error { return s.secondListener(p) })
	ctx.Step(`^"([^"]*)" is sent with the session cookie from this machine's non-loopback address$`, func(r string) error {
		return s.fromLAN(s.second, r, []*http.Cookie{s.session})
	})
	ctx.Step(`^a third host\.listen bound on this machine's non-loopback address that allows that address as a peer$`, func() error { return s.thirdListener() })
	ctx.Step(`^"([^"]*)" is sent to the third listener from this machine's non-loopback address$`, func(r string) error { return s.fromLAN(s.third, r, nil) })
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_admission_by_source_then_cookie(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_admission_by_source_then_cookie,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_admission_by_source_then_cookie.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the admission-by-source-then-cookie e2e scenarios failed")
	}
}
