package egresstest_test

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/egresstest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/faultserver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/permit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/egress"
)

// guarded is a client of r's service for kind, under a permit for dest, guarded
// by DenyPrivate with loopback allowed (so a faultserver stays reachable). A
// guarded client has a transport of its own and no Proxy hook, so these tests
// prove the recorder does not depend on one.
func guarded(t *testing.T, r *egresstest.Recorder, kind, dest string) *http.Client {
	t.Helper()
	c, err := r.Service().Client(kind, permit.New(t, kind, dest), egress.Options{
		Label: t.Name(),
		Guard: egress.DenyPrivate(egress.PrivatePolicy{AllowLoopback: true}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Service().Release(c) })
	return c
}

// DH-T-UTIL-15 (gap-closure 19.5.2): a guarded client is recorded and refused
// like any other. Its request to the allowed loopback endpoint is served and
// recorded; its request to a host no Allow entry names fails the test naming
// the host, returns ErrUnlisted and is neither looked up nor dialled.
//
//dsh:test DH-T-UTIL-15
func TestAGuardedClientsUnlistedRequestFailsTheTest(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{})
	srv.Script("", faultserver.Reply("hi"))
	rec := &recorder{TB: t}
	t.Cleanup(rec.cleanup)
	r := egresstest.New(rec, srv.URL())

	if code, _, err := get(t, guarded(t, r, "web-fetch", srv.URL()), srv.URL()+"/v1"); err != nil || code != http.StatusOK {
		t.Fatalf("the allowed endpoint through a guarded client: %d %v", code, err)
	}
	const other = "https://telemetry.elsewhere.test"
	_, _, err := get(t, guarded(t, r, "telemetry", other), other+"/collect?token=secret")
	if !errors.Is(err, egresstest.ErrUnlisted) {
		t.Fatalf("the guarded client's unlisted request returned %v, want ErrUnlisted", err)
	}
	f := rec.failed()
	if len(f) != 1 || !strings.Contains(f[0], "unlisted request") || !strings.Contains(f[0], "telemetry.elsewhere.test") {
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
	if n := srv.Served(""); n != 1 {
		t.Fatalf("the server served %d requests, want the allowed one only", n)
	}
}

// DH-T-UTIL-15 (gap-closure 19.5.2): a scripted dial failure reaches a guarded
// client. The first attempt fails as a refused dial and never reaches the
// server; the second is served. Both are recorded, the first with its fault, so
// the script is used and the test does not fail at its end.
//
//dsh:test DH-T-UTIL-15
func TestAGuardedClientSeesAScriptedDialFailure(t *testing.T) {
	srv := faultserver.New(t, faultserver.Options{})
	srv.Script("", faultserver.Reply("second"))
	r := egresstest.New(t)
	r.Fail(srv.URL(), 1, egresstest.Refused)
	c := guarded(t, r, "web-fetch", srv.URL())

	if _, _, err := get(t, c, srv.URL()+"/v1"); !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("the scripted attempt returned %v, want a refused dial", err)
	}
	if n := srv.Served(""); n != 0 {
		t.Fatalf("the refused attempt reached the server (%d served)", n)
	}
	if code, body, err := get(t, c, srv.URL()+"/v1"); err != nil || code != http.StatusOK || !strings.Contains(body, "second") {
		t.Fatalf("the attempt after the scripted one: %d %q %v", code, body, err)
	}
	if reqs := r.Requests(); len(reqs) != 2 || reqs[0].Fault != egresstest.Refused || reqs[1].Fault != egresstest.None {
		t.Fatalf("recorded %+v", reqs)
	}
}

// DH-T-UTIL-15 (gap-closure 19.5.2): the guard runs on both sides of the
// recording. A private literal (10.0.0.1), allowed by the test and named by the
// permit, is refused by the guard with egress/not-permitted before the recorder
// sees it: it is not recorded, nothing is looked up, and the test does not fail.
// A name the recorder resolves to that address is recorded first (it is a dial
// attempt) and then refused by the guard on the resolved address.
//
//dsh:test DH-T-UTIL-15
func TestTheGuardRefusesAPrivateLiteralBeforeItIsRecorded(t *testing.T) {
	rec := &recorder{TB: t}
	t.Cleanup(rec.cleanup)
	r := egresstest.New(rec, "http://10.0.0.1")

	_, _, err := get(t, guarded(t, r, "web-fetch", "http://10.0.0.1"), "http://10.0.0.1/inside")
	if !egress.IsNotPermitted(err) || !strings.Contains(err.Error(), "private address") {
		t.Fatalf("a private literal returned %v, want the guard's egress/not-permitted refusal", err)
	}
	if reqs, l := r.Requests(), r.Lookups(); len(reqs) != 0 || len(l) != 0 {
		t.Fatalf("the guard's refusal was recorded: requests %+v, lookups %+v", reqs, l)
	}

	r.Host("inside.model.test", "10.0.0.1")
	named := "http://inside.model.test"
	r.Allow(named)
	_, _, err = get(t, guarded(t, r, "web-fetch", named), named+"/inside")
	if !egress.IsNotPermitted(err) || !strings.Contains(err.Error(), "10.0.0.1") {
		t.Fatalf("a name resolving to a private address returned %v, want the guard's refusal", err)
	}
	reqs := r.Requests()
	if len(reqs) != 1 || !reqs[0].Allowed || must(url.Parse(reqs[0].URL)).Hostname() != "inside.model.test" {
		t.Fatalf("recorded %+v, want the one request by name", reqs)
	}
	var looked bool
	for _, l := range r.Lookups() {
		looked = looked || (l.Name == "inside.model.test" && l.Allowed)
	}
	if !looked {
		t.Fatalf("lookups %+v do not record the name", r.Lookups())
	}
	if f := rec.failed(); len(f) != 0 {
		t.Fatalf("the guard's refusals failed the test: %q", f)
	}
}
