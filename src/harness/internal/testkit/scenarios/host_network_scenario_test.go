//go:build scenario

package scenarios

import (
	"bytes"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// TestNetworkListenerTLSPrefixAndRenewal is the DH-E-HOST-2 scenario
// (docs/harness/areas/host-telemetry.md; DH-D-HOST-1, DH-D-HOST-2; tech-spec sec.
// 4.5: authored in DH-S-15, run in DH-S-22 step 22.2): a network listener refuses
// plain HTTP, serves TLS, works behind a path prefix, and picks up a new
// certificate.
//
// Given two machines on one network (this one runs the host; the browser driver
// drives a browser on the second), a certificate the second machine trusts for
// this machine's name, a renewed pair for the same name, a real reverse proxy that
// strips a path prefix, and the binary built from this checkout on a fresh home,
// When (1) the host starts with a network bind and no TLS; (2) it starts with TLS
// and the name as a trusted host, and the printed URL is opened on the second
// machine; (3) seven tabs are opened; (4) the certificate files are replaced with
// the renewed pair while tabs are open; (5) the proxy is put in front and the UI
// is opened under the prefix,
// Then (1) start-up fails with a message naming the flag and nothing listens; (2)
// the UI loads, the cookie is Secure, directory browsing works, and the "open in"
// route answers 404; (3) all seven update, over HTTP/2; (4) a new connection
// presents the renewed certificate without a restart; (5) pages, assets, forms and
// the event stream work under the prefix and the token redirect stays inside it.
// Cleanup: the host and proxy stop and the home is deleted here; removing the
// certificate from the trust store is the runbook's. If step 1 listened, the layer
// and the start-up output are logged.
//
// Inputs: DSH_SCENARIO_WEB_ARGS (host_web_test.go); DSH_SCENARIO_HOST2_LAYER, a
// layer over {tls} (on or off), {cert}, {key} and {public} binding
// DSH_SCENARIO_HOST2_ADDR, trusting DSH_SCENARIO_HOST2_NAME, serving the pair when
// {tls} is on and printing links under {public} when it is not empty;
// DSH_SCENARIO_HOST2_PAIR and DSH_SCENARIO_HOST2_RENEWED, each "cert;key" PEM
// paths; DSH_SCENARIO_HOST2_DRIVER, the browser driver on the second machine (its
// {browser} is "remote"); DSH_SCENARIO_HOST2_DIRS and DSH_SCENARIO_HOST2_OPEN_IN,
// the paths of the directory browser and of the "open in" route under the origin;
// DSH_SCENARIO_HOST2_PROXY, a command over {upstream} (https:// and
// DSH_SCENARIO_HOST2_ADDR, the bound listener) that runs the reverse proxy in the
// foreground, and DSH_SCENARIO_HOST2_PROXY_URL, the URL with the prefix the
// second machine reaches it at, which the scenario waits on before browsing.
//
//dsh:scenario DH-E-HOST-2
func TestNetworkListenerTLSPrefixAndRenewal(t *testing.T) {
	layerT := need(t, "DSH_SCENARIO_HOST2_LAYER", "layer over {tls}, {cert}, {key} and {public} binding the network address")
	addr := need(t, "DSH_SCENARIO_HOST2_ADDR", "the network host:port the layer binds")
	name := need(t, "DSH_SCENARIO_HOST2_NAME", "the name the certificate is for, a trusted host")
	pair := needList(t, "DSH_SCENARIO_HOST2_PAIR", "cert;key of the trusted certificate")
	renewed := needList(t, "DSH_SCENARIO_HOST2_RENEWED", "cert;key of the renewed certificate")
	if len(pair) != 2 || len(renewed) != 2 {
		t.Fatalf("DSH_SCENARIO_HOST2_PAIR and _RENEWED are each cert;key, got %v and %v", pair, renewed)
	}
	drive := newBrowserDriver(t, "DSH_SCENARIO_HOST2_DRIVER")
	dirs := need(t, "DSH_SCENARIO_HOST2_DIRS", "path of the directory browser under the origin")
	openIn := need(t, "DSH_SCENARIO_HOST2_OPEN_IN", "path of the open-in route under the origin")
	proxy := need(t, "DSH_SCENARIO_HOST2_PROXY", "command over {upstream} running the prefix-stripping proxy")
	proxyURL := strings.TrimRight(need(t, "DSH_SCENARIO_HOST2_PROXY_URL", "the proxy's URL with the prefix"), "/")
	bin := dshBinary(t)
	dir := t.TempDir()
	root := filepath.Join(dir, "home")
	cert, key := filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	install := func(p []string) {
		copyFile(t, p[0], cert)
		copyFile(t, p[1], key)
	}
	install(pair)
	layer := func(tlsOn, public string) string {
		return writeLayer(t, expand(layerT, map[string]string{"tls": tlsOn, "cert": cert, "key": key, "public": public}))
	}
	step := func(name, u string) browserState { return drive.step(t, "remote", name, u) }

	// (1) A network bind without TLS or the explicit flag does not start.
	plain := layer("off", "")
	r := runDsh(t, time.Minute, []string{"DSH_HOME=" + root}, bin,
		append([]string{"--home", root, "--layer", plain}, needList(t, webArgsEnv, "arguments starting a listening host")...)...)
	if r.code == 0 || !strings.Contains(strings.ToLower(r.stdout+r.stderr), "allowinsecureremote") {
		t.Errorf("(1) a plain network bind exited %d without naming the AllowInsecureRemote flag:\n%s%s", r.code, r.stdout, r.stderr)
	}
	if c, err := net.DialTimeout("tcp", addr, 3*time.Second); err == nil {
		c.Close()
		t.Fatalf("(1) %s accepts connections after the refused start; layer:\n%s", addr, plain)
	}

	// (2) With TLS the UI loads on the second machine with a Secure cookie.
	h := startWebHost(t, bin, root, nil, layer("on", ""))
	if !strings.HasPrefix(h.link, "https://") {
		t.Fatalf("(2) the TLS host printed %s", h.link)
	}
	origin := h.origin()
	s := step("open", h.link)
	if s.Status != http.StatusOK {
		t.Fatalf("(2) the link answered %d on the second machine", s.Status)
	}
	noTokenShown(t, "(2)", s)
	if c, ok := s.sessionCookie(); !ok || !c.Secure {
		t.Errorf("(2) the session cookie is %+v (present %v), want Secure", c, ok)
	}
	if s := step("open", origin+dirs); s.Status != http.StatusOK {
		t.Errorf("(2) directory browsing answered %d", s.Status)
	}
	if s := step("open", origin+openIn); s.Status != http.StatusNotFound {
		t.Errorf("(2) the open-in route answered %d in remote mode, want 404", s.Status)
	}

	// (3) Seven tabs all update, each over HTTP/2.
	tabs := step("seven-tabs", origin+"/")
	if len(tabs.Tabs) != 7 || len(tabs.Protocols) != 7 {
		t.Errorf("(3) %d of 7 tabs showed the update, with %d protocol results; want 7 of each", len(tabs.Tabs), len(tabs.Protocols))
	}
	for i, p := range tabs.Protocols {
		if p != "h2" {
			t.Errorf("(3) tab %d used %q, want h2", i+1, p)
		}
	}

	// (4) Renewed files are picked up by the next handshake, without a restart.
	pid := h.pid()
	install(renewed)
	want := pemDER(t, renewed[0])
	deadline := time.Now().Add(time.Minute)
	renewTick := time.NewTicker(time.Second)
	for {
		got, err := presented(addr, name)
		if err == nil && bytes.Equal(got, want) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("(4) no handshake presented the renewed certificate within a minute (last error %v)", err)
		}
		<-renewTick.C
	}
	renewTick.Stop()
	if h.pid() != pid {
		t.Errorf("(4) the host restarted (pid %d to %d) to pick up the certificate", pid, h.pid())
	}
	stopHost(t, h.dshHost)

	// (5) Behind the prefix-stripping proxy every request stays under the prefix.
	// The proxy's upstream is the bound listener, never the public URL the links
	// carry (that is the proxy itself). The proxy runs in a tree of its own, which
	// cleanup stops whole.
	h = startWebHost(t, bin, root, nil, layer("on", proxyURL))
	proxyLine := expand(proxy, map[string]string{"upstream": "https://" + addr})
	proxied := startShellTree(t, proxyLine)
	if !strings.HasPrefix(h.link, proxyURL+"/") {
		t.Fatalf("(5) the host printed %s, want a link under %s", h.link, proxyURL)
	}
	awaitListening(t, proxyURL, proxyLine, proxied)
	s = step("open", h.link)
	if s.Status != http.StatusOK || !strings.HasPrefix(s.URL, proxyURL+"/") {
		t.Fatalf("(5) the link under the prefix ended at %s (%d), want a page under %s", s.URL, s.Status, proxyURL)
	}
	noTokenShown(t, "(5)", s)
	walk := step("walk", s.URL)
	if err := prefixCoverageError(walk.Responses, proxyURL); err != nil {
		t.Errorf("(5) %v: %+v", err, walk.Responses)
	}
	stopHost(t, h.dshHost)
}

// prefixKinds are the request categories a walk under the prefix must show
// working: a page, an asset, a form submission and the event stream.
var prefixKinds = []string{"document", "asset", "form", "event-stream"}

// prefixCoverageError checks a walk's responses: every request stays under
// prefix and none fails, and each of prefixKinds has a successful response (a
// form submission may redirect, if the redirect stays under the prefix).
func prefixCoverageError(responses []browserResponse, prefix string) error {
	var problems []string
	ok := map[string]bool{}
	for _, r := range responses {
		if _, err := url.Parse(r.URL); err != nil || !strings.HasPrefix(r.URL, prefix+"/") {
			problems = append(problems, fmt.Sprintf("a request left the prefix: %s", r.URL))
			continue
		}
		if r.Status >= 400 || r.Status < 200 {
			problems = append(problems, fmt.Sprintf("%s answered %d under the prefix", r.URL, r.Status))
			continue
		}
		kind := r.Kind
		if strings.HasPrefix(r.ContentType, "text/event-stream") {
			kind = "event-stream"
		}
		switch {
		case kind == "form" && r.Method != http.MethodPost:
			problems = append(problems, fmt.Sprintf("the form request to %s is %q, want a POST submission", r.URL, r.Method))
		case kind == "form" || r.Status < 300:
			ok[kind] = true
		}
	}
	for _, k := range prefixKinds {
		if !ok[k] {
			problems = append(problems, fmt.Sprintf("no %s request succeeded under the prefix", k))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// awaitListening waits up to a minute until rawURL's host and port accept a
// connection, failing the scenario when the process p running line exits first.
func awaitListening(t *testing.T, rawURL, line string, p *launcher.Host) {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	hostPort := u.Host
	if u.Port() == "" {
		hostPort = net.JoinHostPort(u.Hostname(), map[string]string{"https": "443", "http": "80"}[u.Scheme])
	}
	deadline := time.Now().Add(time.Minute)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		if c, err := net.DialTimeout("tcp", hostPort, 3*time.Second); err == nil {
			c.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("nothing listens at %s within a minute of starting %s", hostPort, line)
		}
		select {
		case <-p.Process.Exited():
			t.Fatalf("%s exited before %s listened:\n%s%s", line, hostPort, p.Stdout.String(), p.Stderr.String())
		case <-tick.C:
		}
	}
}

// copyFile replaces dst with src's bytes.
func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// pemDER is the DER of the first certificate in a PEM file.
func pemDER(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := pem.Decode(data)
	if b == nil || b.Type != "CERTIFICATE" {
		t.Fatalf("%s holds no PEM certificate", path)
	}
	return b.Bytes
}

// presented is the DER of the leaf certificate a new TLS connection to addr
// presents for name.
func presented(addr, name string) ([]byte, error) {
	// The bytes are compared, so the chain is not verified here.
	c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr,
		&tls.Config{ServerName: name, InsecureSkipVerify: true})
	if err != nil {
		return nil, err
	}
	defer c.Close()
	certs := c.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return nil, errors.New("no certificate presented")
	}
	return certs[0].Raw, nil
}
