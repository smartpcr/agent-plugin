//go:build scenario

package scenarios

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// The helpers of the listening-host scenarios authored in DH-S-15 (DH-E-HOST-1,
// DH-E-HOST-2 run in DH-S-22; DH-E-HOST-7, -8, -9 run in DH-S-39). They reach the
// product only at its boundary: the built binary through dshBinary and startDsh
// (binary_test.go), the launch link it prints, the runtime record under
// <home>/run/, the control endpoint as raw newline-delimited JSON-RPC through
// src/sdk/jsonrpc, and plain HTTP. None imports src/sdk/sdk or an internal
// package (M2-DOD-5). What the web UI adds after DH-S-15 (pages, the conversation
// API, sign-out) is reached through run-time inputs the DH-S-22 and DH-S-39
// operators set, as DH-E-HOST-4 does.

// webArgsEnv are the ";"-separated arguments after the global options that start
// a listening host (for example "web").
const webArgsEnv = "DSH_SCENARIO_WEB_ARGS"

// launchLink matches a launch link the host prints: its audience, "/?token=" and
// the one-time token (host.auth).
var launchLink = regexp.MustCompile(`https?://[^\s"'<>]+/\?token=[A-Za-z0-9_\-]+`)

// The marks of a harness credential (host.auth): a launch token in a URL and the
// session cookie's name prefix. No control or tool reply may carry either.
const (
	tokenMark  = "?token="
	cookieMark = "dsh-session-"
)

// holdsCredential reports whether s carries a launch token or a session cookie.
func holdsCredential(s string) bool {
	return strings.Contains(s, tokenMark) || strings.Contains(s, cookieMark)
}

// webHost is a running listening host on a scratch home.
type webHost struct {
	*dshHost
	home string
	link string // the first launch link it printed
}

// origin is the host's audience: the link without "/?token=...".
func (w *webHost) origin() string { return originOf(w.link) }

// originOf cuts the token query off a launch link.
func originOf(link string) string {
	o, _, _ := strings.Cut(link, "/"+tokenMark)
	return o
}

// startWebHost starts bin on home with the layers given and the arguments of
// webArgsEnv, and waits until it has printed a launch link on its own terminal.
func startWebHost(t *testing.T, bin, home string, env []string, layers ...string) *webHost {
	t.Helper()
	args := []string{"--home", home}
	for _, l := range layers {
		args = append(args, "--layer", l)
	}
	args = append(args, needList(t, webArgsEnv, "arguments starting a listening host, ';'-separated")...)
	h := startDsh(t, append([]string{"DSH_HOME=" + home}, env...), bin, args...)
	link := h.waitFor(t, 2*time.Minute, "a printed launch link", func(out string) string { return launchLink.FindString(out) })
	return &webHost{dshHost: h, home: home, link: link}
}

// nextLink waits for a launch link the host prints after the ones in seen.
func (w *webHost) nextLink(t *testing.T, seen ...string) string {
	t.Helper()
	return w.waitFor(t, time.Minute, "a new launch link", func(out string) string {
		for _, l := range launchLink.FindAllString(out, -1) {
			if !contains(seen, l) {
				return l
			}
		}
		return ""
	})
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// runtimeRecord is the runtime record a listening host writes (host.launch Record).
type runtimeRecord struct {
	Origin string `json:"origin"`
	PID    int    `json:"pid"`
	Socket string `json:"socket"`
	Raw    string `json:"-"`
}

// runtimeRecords reads every runtime record under <home>/run/. A record that holds
// a credential fails the scenario: a second dsh reads it, and so can the model.
func runtimeRecords(t *testing.T, home string) []runtimeRecord {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(home, "run", "web-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	var out []runtimeRecord
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var r runtimeRecord
		if err := json.Unmarshal(data, &r); err != nil {
			t.Fatalf("runtime record %s: %v\n%s", p, err, data)
		}
		r.Raw = string(data)
		if holdsCredential(r.Raw) {
			t.Errorf("runtime record %s holds a credential:\n%s", p, data)
		}
		out = append(out, r)
	}
	return out
}

// oneRecord is the one runtime record of home.
func oneRecord(t *testing.T, home string) runtimeRecord {
	t.Helper()
	recs := runtimeRecords(t, home)
	if len(recs) != 1 {
		t.Fatalf("%s/run holds %d runtime records, want exactly one: %+v", home, len(recs), recs)
	}
	return recs[0]
}

// controlCall makes one call on the control endpoint at socket: newline-delimited
// JSON-RPC over the same-user local socket, through src/sdk/jsonrpc. It returns
// the raw result, or the error the host answered or the dial met.
func controlCall(t *testing.T, socket, method string, params any) (json.RawMessage, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", socket)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c := jsonrpc.Serve(ctx, conn, conn, jsonrpc.Options{})
	defer c.Close()
	var raw json.RawMessage
	err = c.Call(ctx, method, params, &raw)
	return raw, err
}

// mustControl is controlCall that fails the scenario on an error and on a reply
// that holds a credential.
func mustControl(t *testing.T, socket, method string, params any) json.RawMessage {
	t.Helper()
	raw, err := controlCall(t, socket, method, params)
	if err != nil {
		t.Fatalf("%s on %s: %v", method, socket, err)
	}
	if holdsCredential(string(raw)) {
		t.Errorf("%s returned a credential: %s", method, raw)
	}
	return raw
}

// browserDriverEnv names, per scenario, the command line that drives a real
// browser through one step and reports what it saw (browserState).
//
// The command is a template over {browser} (a name the scenario's browser list
// gives), {step}, {url} and {out}. It performs step in browser and writes one JSON
// object to {out}. Steps:
//
//	current         report the active tab of the browser's normal window
//	open            load {url} in a new tab of the normal window
//	reload          reload the active tab of the normal window
//	private         load {url} in a private window, opening one if needed
//	reload-private  reload the private window's tab
//	tabs            report the normal window's tabs
//	visit           load {url} in a new tab and wait until its scripts and forms ran
//	walk            from {url}, open a page, load its assets, submit one form and
//	                hold the event stream for ten seconds
//	seven-tabs      open {url} in seven tabs, cause one update and wait for it
//	sign-out        sign the normal window out through the UI
//
// url, status, history and cookies describe the tab the step ended on;
// responses list every response the browser received during the step; tabs list
// the tabs open after it, and for seven-tabs only those that showed the update,
// with the protocol of each in protocols.
type browserState struct {
	URL       string            `json:"url"`
	Status    int               `json:"status"`
	History   []string          `json:"history"`
	Cookies   []browserCookie   `json:"cookies"`
	Responses []browserResponse `json:"responses"`
	Tabs      []string          `json:"tabs"`
	Protocols []string          `json:"protocols"`
}

type browserCookie struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secure bool   `json:"secure"`
}

type browserResponse struct {
	URL         string `json:"url"`
	Status      int    `json:"status"`
	ContentType string `json:"contentType"`
}

// sessionCookie is the harness session cookie of the state, if any.
func (s browserState) sessionCookie() (browserCookie, bool) {
	for _, c := range s.Cookies {
		if strings.HasPrefix(c.Name, cookieMark) && c.Value != "" {
			return c, true
		}
	}
	return browserCookie{}, false
}

// browserDriver runs one scenario's driver command.
type browserDriver struct{ line string }

func newBrowserDriver(t *testing.T, env string) browserDriver {
	t.Helper()
	return browserDriver{line: need(t, env, "command over {browser}, {step}, {url} and {out} driving a real browser (host_web_test.go browserState)")}
}

// step runs one driver step and returns what the browser reported.
func (d browserDriver) step(t *testing.T, browser, step, url string) browserState {
	t.Helper()
	out := filepath.Join(t.TempDir(), "browser-"+step+".json")
	mustShell(t, expand(d.line, map[string]string{"browser": browser, "step": step, "url": url, "out": out}))
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the browser driver wrote no report for %s: %v", step, err)
	}
	var s browserState
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatalf("the browser driver's report for %s: %v\n%s", step, err, data)
	}
	t.Logf("%s %s %s: %s %d, %d tabs, %d responses", browser, step, url, s.URL, s.Status, len(s.Tabs), len(s.Responses))
	return s
}

// noTokenShown fails the scenario when the tab's address or history shows a
// launch token.
func noTokenShown(t *testing.T, what string, s browserState) {
	t.Helper()
	if strings.Contains(s.URL, tokenMark) {
		t.Errorf("%s: the address bar shows the token: %s", what, s.URL)
	}
	for _, h := range s.History {
		if strings.Contains(h, tokenMark) {
			t.Errorf("%s: the history holds the token: %s", what, h)
		}
	}
}
