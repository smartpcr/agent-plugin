package egress_test

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

func TestMain(m *testing.M) { subprocesstest.Main(m) }

// namedSecrets is a secret store that names its variables exactly.
type namedSecrets []string

func (n namedSecrets) IsSecretName(name string) bool {
	for _, s := range n {
		if strings.EqualFold(s, name) {
			return true
		}
	}
	return false
}

// proxyNames are the names every child overlay sets.
var proxyNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy", egress.NodeEnvProxy}

// Scenario "A child inherits the proxy, not secrets" (gap-closure plan 19.3.7): a
// launch lookup holds HTTPS_PROXY and a named secret, which the host process
// environment also holds, beside a stale HTTP_PROXY the launch did not resolve. A
// re-executed child built by subprocess.BuildEnv with ChildEnv(ModelRun) as
// Managed prints its whole environment: the proxy is present in both cases with
// the loopback bypass, the stale HTTP_PROXY is cleared, and neither the secret's
// name nor its value arrives.
//
//dsh:test DH-T-UNDICI-9, DH-T-UTIL-5
func TestChildInheritsTheProxyNotSecrets(t *testing.T) {
	const (
		secretName  = "EGRESS_NAMED_PAT"
		secretValue = "egress-never-leaks-7f3a"
		proxy       = "http://proxy.example:3128"
	)
	t.Setenv(secretName, secretValue)
	t.Setenv("HTTP_PROXY", "http://stale.example:1")
	svc, diags, err := egress.Resolve(lookup(map[string]string{
		"HTTPS_PROXY": proxy,
		secretName:    secretValue,
	}), egress.Config{})
	if err != nil || len(diags) != 0 {
		t.Fatalf("Resolve = %v, %v", diags, err)
	}
	overlay, diags := svc.ChildEnv(egress.ModelRun)
	if len(diags) != 0 {
		t.Fatalf("ChildEnv(ModelRun) diagnostics %v; the proxy carries no credentials", diags)
	}

	spec := subprocesstest.Spec(t, subprocesstest.RoleEnviron)
	spec.Env = subprocess.BuildEnv(os.Environ(), subprocess.EnvOptions{
		Secrets: namedSecrets{secretName},
		Entries: spec.Env,
		Managed: overlay,
	})
	out := strings.ReplaceAll(subprocesstest.Output(t, spec), "\r\n", "\n")
	child := subprocess.Env(strings.Split(strings.TrimSuffix(out, "\n"), "\n"))
	if !child.Has(subprocesstest.RoleEnv) {
		t.Fatalf("the child did not print the built environment:\n%s", out)
	}
	for _, name := range []string{"HTTPS_PROXY", "https_proxy"} {
		if got := child.Get(name); got != proxy {
			t.Errorf("child %s = %q, want %q", name, got, proxy)
		}
	}
	if got := child.Get("NO_PROXY"); got != "localhost,127.0.0.1,::1" {
		t.Errorf("child NO_PROXY = %q, want the loopback bypass", got)
	}
	if got := child.Get(egress.NodeEnvProxy); got != "1" {
		t.Errorf("child %s = %q, want 1", egress.NodeEnvProxy, got)
	}
	if got := child.Get("HTTP_PROXY"); got != "" {
		t.Errorf("child HTTP_PROXY = %q, want the stale host value cleared", got)
	}
	if child.Has(secretName) {
		t.Errorf("the child inherited %s", secretName)
	}
	if strings.Contains(out, secretValue) {
		t.Errorf("the child's environment holds the secret's value")
	}
}

// DH-T-UTIL-5 and DH-T-UNDICI-9 (DH-LIB-UNDICI-R7), the overlay half.
// ChildEnv(Trusted) keeps every value the user set as they set it, a SOCKS value
// and one with a password included, and adds the resolved value for a name they
// did not set; ChildEnv(ModelRun) is the same except that the value with a
// password is cleared and reported once, without the password; ClearedEnv clears
// every name. Resolve sees only its lookup, also when the process environment
// names another proxy.
//
//dsh:test DH-T-UTIL-5, DH-T-UNDICI-9
func TestChildEnvAudiences(t *testing.T) {
	const withPassword = "http://user:pa55word@corp.example:8080"
	svc, _, err := egress.Resolve(lookup(map[string]string{
		"all_proxy":  "socks5h://socks.example:1080",
		"ALL_PROXY":  "socks5://ignored.example:1",
		"HTTP_PROXY": withPassword,
		"NO_PROXY":   "intra.example, 127.0.0.1",
	}), egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"HTTP_PROXY":        withPassword,
		"HTTPS_PROXY":       "socks5h://socks.example:1080", // unset by the user: the resolved value
		"ALL_PROXY":         "socks5h://socks.example:1080", // the user's SOCKS value, lower case first
		"NO_PROXY":          "intra.example,127.0.0.1,localhost,::1",
		egress.NodeEnvProxy: "1",
	}
	trusted, diags := svc.ChildEnv(egress.Trusted)
	if len(diags) != 0 {
		t.Fatalf("ChildEnv(Trusted) diagnostics %v, want none", diags)
	}
	checkOverlay(t, "Trusted", trusted, want)

	want["HTTP_PROXY"] = ""
	for i := range 2 {
		run, diags := svc.ChildEnv(egress.ModelRun)
		checkOverlay(t, "ModelRun", run, want)
		switch {
		case i == 0 && (len(diags) != 1 || diags[0].Name != "HTTP_PROXY" || diags[0].Reason != egress.ReasonRefusedProxyValue):
			t.Fatalf("first ChildEnv(ModelRun) diagnostics %v, want one for HTTP_PROXY", diags)
		case i == 0 && strings.Contains(diags[0].String(), "pa55word"):
			t.Fatalf("the diagnostic %q quotes the password", diags[0])
		case i == 1 && len(diags) != 0:
			t.Fatalf("second ChildEnv(ModelRun) diagnostics %v; the omission is reported once", diags)
		}
	}

	cleared := map[string]string{}
	for _, n := range proxyNames {
		cleared[strings.ToUpper(n)] = ""
	}
	checkOverlay(t, "ClearedEnv", egress.ClearedEnv(), cleared)

	// The process environment names a proxy the lookup does not: neither the
	// service nor the overlay sees it.
	t.Setenv("HTTPS_PROXY", "http://process.example:1")
	t.Setenv("https_proxy", "http://process.example:1")
	bare, _, err := egress.Resolve(lookup(nil), egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	none, _ := bare.ChildEnv(egress.Trusted)
	checkOverlay(t, "an empty lookup", none, cleared)
	if p := egress.ProxyFor(lookup(nil), &url.URL{Scheme: "https", Host: "api.example"}); p != nil {
		t.Fatalf("ProxyFor = %v with an empty lookup, want direct", p)
	}
}

// checkOverlay checks that overlay sets every proxy name, in both cases, to the
// value want gives its upper-case name, and nothing else.
func checkOverlay(t *testing.T, label string, overlay []string, want map[string]string) {
	t.Helper()
	got := map[string]string{}
	for _, kv := range overlay {
		n, v, ok := strings.Cut(kv, "=")
		if !ok {
			t.Fatalf("%s: entry %q is not NAME=value", label, kv)
		}
		got[n] = v
	}
	if len(got) != len(proxyNames) {
		t.Fatalf("%s: overlay %v, want exactly the names %v", label, overlay, proxyNames)
	}
	for _, n := range proxyNames {
		if v, ok := got[n]; !ok || v != want[strings.ToUpper(n)] {
			t.Errorf("%s: %s = %q (set %v), want %q", label, n, v, ok, want[strings.ToUpper(n)])
		}
	}
}

// DH-T-UNDICI-9 (DH-LIB-UNDICI-R12), the release half: releasing the last client
// of a transport closes its idle connection, while a request in flight on another
// connection completes and its body is read in full.
//
//dsh:test DH-T-UNDICI-9
func TestReleaseClosesIdleWhileInFlightCompletes(t *testing.T) {
	entered, unblock := make(chan struct{}), make(chan struct{})
	closed := make(chan struct{}, 4)
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow" {
			close(entered)
			<-unblock
			_, _ = io.WriteString(w, "done")
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		switch s {
		case http.StateNew:
			conns.Add(1)
		case http.StateClosed:
			closed <- struct{}{}
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-unblock:
		default:
			close(unblock)
		}
	})
	svc := egress.New(egress.Config{})
	t.Cleanup(svc.CloseIdleConnections)
	c, err := svc.Client("model-request", permit.New(t, "model-request", srv.URL), egress.Options{})
	if err != nil {
		t.Fatal(err)
	}

	type result struct {
		body string
		err  error
	}
	slow := make(chan result, 1)
	go func() {
		resp, err := c.Get(srv.URL + "/slow")
		if err != nil {
			slow <- result{err: err}
			return
		}
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		slow <- result{string(b), err}
	}()
	<-entered
	resp, err := c.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if n := conns.Load(); n != 2 {
		t.Fatalf("%d connections, want two: one in flight, one idle", n)
	}

	svc.Release(c)
	select {
	case <-closed:
	case <-time.After(10 * time.Second):
		t.Fatal("the idle connection was not closed when its last client was released")
	}
	close(unblock)
	select {
	case r := <-slow:
		if r.err != nil || r.body != "done" {
			t.Fatalf("the in-flight request ended with %q, %v; want it to complete", r.body, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the in-flight request did not complete")
	}
	if len(svc.Handed()) != 0 {
		t.Fatalf("Handed() = %v after Release", svc.Handed())
	}
}
