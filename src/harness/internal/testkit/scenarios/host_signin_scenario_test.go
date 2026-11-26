//go:build scenario

package scenarios

import (
	"fmt"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBrowserSignInReuseAndOtherOrigins is the DH-E-HOST-1 scenario
// (docs/harness/areas/host-telemetry.md; DH-D-HOST-2, DH-D-HOST-3; tech-spec sec.
// 4.5: authored in DH-S-15, run in DH-S-22 step 22.2): a real browser is signed in
// by dsh, a second dsh reuses the host, and other origins are refused.
//
// Given the binary built from this checkout, a fresh home, each browser of the
// list as the default in turn, a second local web server on another port serving
// a page with a cross-site form and a script, and a host name resolving to
// 127.0.0.1,
// When (1) dsh runs; (2) the page is reloaded and the printed URL pasted into a
// private window; (3) dsh runs again; (4) the host restarts and the page reloads;
// (5) the second server's page submits its form and fetches a UI page, and the UI
// is loaded through the extra host name; (6) one browser signs out; (7) the second
// server at 127.0.0.1 records the cookies it receives,
// Then (1) the UI shows with no token in the address bar or history; (2) the reload
// works and the pasted URL is refused; (3) a new tab opens on the same host, no
// second listener exists and the second process exits zero; (4) the page loads
// without a new token; (5) all three are refused with 403; (6) only that browser is
// signed out; (7) the cookies are logged as the answer to host-telemetry.md sec. 10
// item 2, not a pass or fail.
// Cleanup: the hosts and servers stop and the home is deleted here; the browser's
// site data for the origin is the driver's ("clear" is not a step). If step 5 was
// not refused, the responses are logged.
//
// Inputs: DSH_SCENARIO_WEB_ARGS (host_web_test.go); DSH_SCENARIO_HOST1_BROWSERS,
// the ";"-separated browsers to run with; DSH_SCENARIO_HOST1_DEFAULT, a command over
// {browser} making it the system default; DSH_SCENARIO_HOST1_DRIVER, the browser
// driver (host_web_test.go browserState); DSH_SCENARIO_HOST1_ALIAS, a host name
// that resolves to 127.0.0.1 and is not a trusted host; DSH_SCENARIO_HOST1_NO_OPEN,
// a config layer that turns off the hand-off to the browser (host.launch open:
// false), used for the restart of step 4.
//
//dsh:scenario DH-E-HOST-1
func TestBrowserSignInReuseAndOtherOrigins(t *testing.T) {
	browsers := needList(t, "DSH_SCENARIO_HOST1_BROWSERS", "browsers to run with, ';'-separated")
	makeDefault := need(t, "DSH_SCENARIO_HOST1_DEFAULT", "command over {browser} making it the default browser")
	drive := newBrowserDriver(t, "DSH_SCENARIO_HOST1_DRIVER")
	alias := need(t, "DSH_SCENARIO_HOST1_ALIAS", "a host name resolving to 127.0.0.1")
	noOpen := need(t, "DSH_SCENARIO_HOST1_NO_OPEN", "a layer turning off the hand-off to the browser")
	bin := dshBinary(t)
	for _, browser := range browsers {
		t.Run(browser, func(t *testing.T) {
			mustShell(t, expand(makeDefault, map[string]string{"browser": browser}))
			root := filepath.Join(t.TempDir(), "home")
			step := func(name, url string) browserState { return drive.step(t, browser, name, url) }

			// (1) dsh hands the link to the default browser.
			h := startWebHost(t, bin, root, nil)
			origin := h.origin()
			first := step("current", "")
			if first.Status != http.StatusOK || !strings.HasPrefix(first.URL, origin) {
				t.Fatalf("(1) the browser shows %s (%d), want the UI at %s", first.URL, first.Status, origin)
			}
			noTokenShown(t, "(1)", first)

			// (2) Reload works; the used link pasted into a private window is refused.
			if s := step("reload", ""); s.Status != http.StatusOK {
				t.Errorf("(2) the reload answered %d, want 200", s.Status)
			}
			if s := step("private", h.link); s.Status != http.StatusUnauthorized && s.Status != http.StatusForbidden {
				t.Errorf("(2) the pasted link answered %d in a private window, want it refused", s.Status)
			}

			// (3) A second dsh asks the running host to open a new tab and exits zero.
			rec := oneRecord(t, root)
			before := step("tabs", "")
			second := runDsh(t, time.Minute, []string{"DSH_HOME=" + root}, bin,
				append([]string{"--home", root}, needList(t, webArgsEnv, "arguments starting a listening host")...)...)
			if second.code != 0 {
				t.Errorf("(3) the second dsh exited %d, want 0:\n%s%s", second.code, second.stdout, second.stderr)
			}
			after := step("tabs", "")
			if len(after.Tabs) != len(before.Tabs)+1 || !strings.HasPrefix(after.Tabs[len(after.Tabs)-1], origin) {
				t.Errorf("(3) tabs went from %v to %v, want one more on %s", before.Tabs, after.Tabs, origin)
			}
			if again := oneRecord(t, root); again.PID != rec.PID || again.Origin != rec.Origin {
				t.Errorf("(3) the runtime record changed from %+v to %+v: a second listener", rec, again)
			}

			// (4) A restart keeps the browser signed in: the session row is durable.
			stopHost(t, h.dshHost)
			h = startWebHost(t, bin, root, nil, writeLayer(t, noOpen))
			if s := step("reload", ""); s.Status != http.StatusOK || !strings.HasPrefix(s.URL, origin) {
				t.Errorf("(4) after the restart the reload answered %s %d, want the UI without a new token", s.URL, s.Status)
			} else {
				noTokenShown(t, "(4)", s)
			}

			// (5) Another origin's form and fetch, and the extra host name, are refused.
			other := startOtherOrigin(t, origin)
			visit := step("visit", other.url+"/")
			var toHost []browserResponse
			for _, r := range visit.Responses {
				if strings.HasPrefix(r.URL, origin) {
					toHost = append(toHost, r)
				}
			}
			if len(toHost) < 2 {
				t.Errorf("(5) the cross-site page reached the host %d times, want the form and the fetch: %+v", len(toHost), visit.Responses)
			}
			for _, r := range toHost {
				if r.Status != http.StatusForbidden {
					t.Errorf("(5) the cross-site request %s answered %d, want 403", r.URL, r.Status)
				}
			}
			_, port, _ := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://"))
			if s := step("open", "http://"+net.JoinHostPort(alias, port)+"/"); s.Status != http.StatusForbidden {
				t.Errorf("(5) the UI through %s answered %d, want 403", alias, s.Status)
			}

			// (6) Sign one browser out; the other, signed in with a fresh link, stays in.
			mustControl(t, oneRecord(t, root).Socket, "launch.open", map[string]any{"print": true})
			fresh := h.nextLink(t, h.link)
			if s := step("private", fresh); s.Status != http.StatusOK {
				t.Fatalf("(6) the fresh link answered %d in a private window, want the UI", s.Status)
			}
			held, ok := step("current", "").sessionCookie()
			if !ok {
				t.Fatalf("(6) the normal window holds no session cookie before sign-out")
			}
			if code := replayCookie(t, origin+"/", held); code != http.StatusOK {
				t.Fatalf("(6) the normal window's cookie replayed before sign-out answered %d, want 200", code)
			}
			step("sign-out", "")
			// What the UI reaches after sign-out is decided by the host, not by the
			// cookie jar: the reload must be refused, and so must the session cookie
			// the browser held before, replayed outside it (checked to work first).
			if s := step("reload", ""); s.Status != http.StatusUnauthorized && s.Status != http.StatusForbidden {
				t.Errorf("(6) after sign-out the reload answered %d at %s, want it refused", s.Status, s.URL)
			}
			if code := replayCookie(t, origin+"/", held); code != http.StatusUnauthorized && code != http.StatusForbidden {
				t.Errorf("(6) the signed-out session's cookie %s still answers %d, want it refused", held.Name, code)
			}
			if s := step("reload-private", ""); s.Status != http.StatusOK {
				t.Errorf("(6) signing one browser out signed the other out too (%d)", s.Status)
			} else if _, ok := s.sessionCookie(); !ok {
				t.Errorf("(6) the private window lost its session cookie when the other browser signed out")
			}

			// (7) Observation, not a verdict: what the browser sends to another port.
			step("open", "http://"+net.JoinHostPort("127.0.0.1", other.port)+"/cookies")
			t.Logf("(7) cookies the second server on 127.0.0.1:%s received: %q (host-telemetry.md sec. 10 item 2)", other.port, other.cookies())
			stopHost(t, h.dshHost)
		})
	}
}

// replayCookie requests target as a same-origin navigation carrying only c and
// returns the status, without following a redirect.
func replayCookie(t *testing.T, target string, c browserCookie) int {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("replaying %s to %s: %v", c.Name, target, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// otherOrigin is the second local web server of DH-E-HOST-1.
type otherOrigin struct {
	url, port string
	mu        sync.Mutex
	seen      []string
}

func (o *otherOrigin) cookies() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.seen...)
}

// startOtherOrigin serves, on a loopback port of its own until cleanup, a page
// whose form posts to target and whose script fetches target's root with
// credentials, and /cookies, which records the Cookie header it receives.
func startOtherOrigin(t *testing.T, target string) *otherOrigin {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	o := &otherOrigin{url: "http://" + ln.Addr().String(), port: port}
	page := fmt.Sprintf(`<!doctype html><html><body>
<iframe name="sink"></iframe>
<form id="f" method="post" action="%[1]s/" target="sink"><input name="x" value="1"></form>
<script>
document.getElementById("f").submit();
fetch("%[1]s/", {credentials: "include"}).catch(function () {});
</script></body></html>`, target)
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write([]byte(page))
	})
	mux.HandleFunc("/cookies", func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		o.seen = append(o.seen, r.Header.Values("Cookie")...)
		o.mu.Unlock()
		_, _ = w.Write([]byte("recorded"))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return o
}
