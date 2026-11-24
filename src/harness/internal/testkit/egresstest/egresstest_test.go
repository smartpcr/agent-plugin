package egresstest_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/egresstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/faultserver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// client is a client of r's service for kind, under a permit for dest.
func client(t *testing.T, r *egresstest.Recorder, kind, dest string) *http.Client {
	t.Helper()
	c, err := r.Service().Client(kind, permit.New(t, kind, dest), egress.Options{Label: t.Name()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Service().Release(c) })
	return c
}

// get sends GET target through c and returns the status and body, or the error.
func get(t *testing.T, c *http.Client, target string) (int, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), err
}

// Scenario (gap-closure 19.5.2) "An unlisted request fails the test": given
// egresstest allowing only the model endpoint, when a component dials another
// host, then the test fails naming it. The component holds a permit for the
// other host, so egress itself lets the request through; the recorder refuses it
// before any dial or lookup, and the model endpoint still answers.
//
//dsh:test DH-T-UTIL-15
func TestAnUnlistedRequestFailsTheTestNamingTheHost(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{})
	srv.Script("", faultserver.Reply("hi"))
	rec := &recorder{TB: t}
	t.Cleanup(rec.cleanup)
	r := egresstest.New(rec, srv.URL())

	if code, _, err := get(t, client(t, r, "model", srv.URL()), srv.URL()+"/v1/stream"); err != nil || code != http.StatusOK {
		t.Fatalf("the allowed model endpoint: %d %v", code, err)
	}
	const other = "https://telemetry.elsewhere.test"
	_, _, err := get(t, client(t, r, "telemetry", other), other+"/collect?token=secret")
	if !errors.Is(err, egresstest.ErrUnlisted) {
		t.Fatalf("the unlisted request returned %v, want ErrUnlisted", err)
	}
	f := rec.failed()
	if len(f) != 1 || !strings.Contains(f[0], "telemetry.elsewhere.test") || !strings.Contains(f[0], "unlisted request") {
		t.Fatalf("the test failed with %q, want one failure naming the host", f)
	}
	if strings.Contains(f[0], "secret") {
		t.Fatalf("the failure prints the query: %q", f[0])
	}
	if l := r.Lookups(); len(l) != 0 {
		t.Fatalf("the refused request was looked up: %+v", l)
	}
	reqs := r.Requests()
	if len(reqs) != 2 || !reqs[0].Allowed || reqs[1].Allowed || reqs[0].Header.Get("User-Agent") != egress.UserAgent {
		t.Fatalf("recorded %+v", reqs)
	}
}

// DH-T-UTIL-15: a lookup of a name the test did not allow fails the test; a
// Host name resolves to its address, through the service's own dialler too, so
// a request to the name reaches the loopback server; and nothing is reported
// for the resolver's search-list variants of a listed name.
//
//dsh:test DH-T-UTIL-15
func TestTheResolverAnswersHostsAndFailsAnUnlistedLookup(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{})
	srv.Script("", faultserver.Reply("by name"))
	rec := &recorder{TB: t}
	t.Cleanup(rec.cleanup)
	r := egresstest.New(rec)
	r.Host("api.model.test", "127.0.0.1")
	port := must(url.Parse(srv.URL())).Port()
	named := "http://api.model.test:" + port
	r.Allow(named)

	addrs, err := r.Resolver().LookupHost(t.Context(), "API.model.test")
	if err != nil || len(addrs) != 1 || addrs[0] != "127.0.0.1" {
		t.Fatalf("LookupHost(api.model.test) = %v, %v", addrs, err)
	}
	if code, body, err := get(t, client(t, r, "model", named), named+"/v1"); err != nil || code != http.StatusOK || !strings.Contains(body, "by name") {
		t.Fatalf("a request by name: %d %q %v", code, body, err)
	}
	if f := rec.failed(); len(f) != 0 {
		t.Fatalf("allowed lookups failed the test: %q", f)
	}

	if _, err := r.Resolver().LookupHost(t.Context(), "tracker.example.test"); err == nil {
		t.Fatal("an unlisted name resolved")
	}
	f := rec.failed()
	if len(f) != 1 || !strings.Contains(f[0], `unlisted name lookup "tracker.example.test"`) {
		t.Fatalf("an unlisted lookup reported %q, want one failure naming it", f)
	}
	var saw bool
	for _, l := range r.Lookups() {
		if l.Name == "tracker.example.test" {
			saw = !l.Allowed
		}
	}
	if !saw {
		t.Fatalf("lookups %+v do not record the unlisted name", r.Lookups())
	}
}

// DH-T-UTIL-15: a scripted refusal at the egress seam is deterministic: the
// chosen attempt fails as a refused dial and never reaches the server, the
// attempts around it are served, and every run gives the same sequence. Reset
// and timeout have the dialler's error shapes. A scripted failure that never
// happens fails the test when it ends.
//
//dsh:test DH-T-UTIL-15
func TestAScriptedDialFailureIsDeterministic(t *testing.T) {
	for run := range 3 {
		srv := faultserver.New(t, faultserver.Options{})
		srv.Script("", faultserver.Reply("one"), faultserver.Reply("three"))
		r := egresstest.New(t)
		r.Fail(srv.URL(), 2, egresstest.Refused)
		c := client(t, r, "model", srv.URL())
		var got []string
		for i := range 3 {
			code, body, err := get(t, c, srv.URL()+"/v1")
			switch {
			case err != nil && errors.Is(err, syscall.ECONNREFUSED):
				got = append(got, "refused")
				if n := srv.Served(""); n != i {
					t.Fatalf("run %d: the refused attempt reached the server (%d served)", run, n)
				}
			case err != nil:
				t.Fatalf("run %d attempt %d: %v", run, i+1, err)
			case code != http.StatusOK || body == "":
				t.Fatalf("run %d attempt %d: %d %q", run, i+1, code, body)
			default:
				got = append(got, "ok")
			}
		}
		if strings.Join(got, ",") != "ok,refused,ok" {
			t.Fatalf("run %d gave %v, want ok,refused,ok", run, got)
		}
		if reqs := r.Requests(); len(reqs) != 3 || reqs[1].Fault != egresstest.Refused || reqs[0].Fault != egresstest.None {
			t.Fatalf("run %d recorded %+v", run, reqs)
		}
	}

	r := egresstest.New(t)
	r.Fail("https://api.model.test", 1, egresstest.Reset)
	r.Fail("https://slow.model.test", 1, egresstest.Timeout)
	if _, _, err := get(t, client(t, r, "model", "https://api.model.test"), "https://api.model.test/v1"); !errors.Is(err, syscall.ECONNRESET) {
		t.Fatalf("a scripted reset returned %v", err)
	}
	_, _, err := get(t, client(t, r, "model", "https://slow.model.test"), "https://slow.model.test/v1")
	var ne net.Error
	if !errors.As(err, &ne) || !ne.Timeout() {
		t.Fatalf("a scripted timeout returned %v, want a timeout", err)
	}

	rec := &recorder{TB: t}
	unused := egresstest.New(rec)
	unused.Fail("https://api.model.test", 2, egresstest.Refused)
	rec.cleanup()
	if f := rec.failed(); len(f) != 1 || !strings.Contains(f[0], "refused on attempt 2 to https://api.model.test") {
		t.Fatalf("an unused scripted failure reported %q", f)
	}
}

// The recorder's component stands in for util.egress: it provides a recording
// service under egress.Key, built from the config it is given.
func TestComponentProvidesTheRecordingServiceUnderTheEgressKey(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{})
	srv.Script("", faultserver.Reply("via the tree"))
	r := egresstest.New(t, srv.URL())
	root := testkit.Tree(t)
	f, err := root.Use(r.Component(), egress.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Wait(); err != nil {
		t.Fatal(err)
	}
	var s *egress.Service
	g, err := root.Inject([]string{egress.Key.Name()}, func(c *cordis.Context) error {
		s, err = egress.Key.Get(c)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	c, err := s.Client("model", permit.New(t, "model", srv.URL()), egress.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Release(c) })
	if code, _, err := get(t, c, srv.URL()+"/v1"); err != nil || code != http.StatusOK {
		t.Fatalf("a request through the provided service: %d %v", code, err)
	}
	if reqs := r.Requests(); len(reqs) != 1 || !reqs[0].Allowed {
		t.Fatalf("the provided service did not record: %+v", reqs)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
