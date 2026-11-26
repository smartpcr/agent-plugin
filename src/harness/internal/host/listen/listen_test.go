package listen_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func cordisRoot(t *testing.T) *cordis.Context {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	return r
}

func load(t *testing.T, r *cordis.Context, name string, body func(ctx *cordis.Context) error, inject ...string) error {
	t.Helper()
	f, err := r.Apply(name, body, inject...)
	if err != nil {
		return err
	}
	t.Cleanup(f.Dispose)
	return f.Wait()
}

// start loads host.listen with cfg and returns the listener it provides.
func start(t *testing.T, r *cordis.Context, cfg listen.Config) (listen.Listener, error) {
	t.Helper()
	f, err := r.Use(listen.Component, cfg)
	if err != nil {
		return nil, err
	}
	t.Cleanup(f.Dispose)
	if err := f.Wait(); err != nil {
		return nil, err
	}
	var l listen.Listener
	err = load(t, r, "get listener", func(ctx *cordis.Context) error {
		l, err = listen.Key.Get(ctx)
		return err
	}, listen.Key.Name())
	return l, err
}

type peer struct{ id string }

func (p peer) ID() string               { return p.id }
func (p peer) Context() context.Context { return context.Background() }
func (p peer) FormToken() string        { return "form-" + p.id }
func admit(t *testing.T, r *cordis.Context, yes bool) {
	t.Helper()
	if err := load(t, r, "fake auth", func(ctx *cordis.Context) error {
		_, err := listen.Authenticate.On(ctx, func(_ *cordis.Context, c *listen.Credentials) (listen.Peer, bool, error) {
			if !yes || c.Request == nil {
				return nil, false, nil
			}
			return peer{"p1"}, true, nil
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// recorder is a handler that counts its calls and answers its name and the peer.
type recorder struct {
	name  string
	calls atomic.Int32
}

func (h *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.calls.Add(1)
	p, _ := listen.PeerFrom(r.Context())
	id := "none"
	if p != nil {
		id = p.ID()
	}
	io.WriteString(w, h.name+" "+id)
}

func mount(t *testing.T, r *cordis.Context, l listen.Listener, name string, m listen.Mount) error {
	t.Helper()
	return load(t, r, name, func(ctx *cordis.Context) error { return l.Mount(ctx, m) })
}

func client(t *testing.T) *http.Client {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

func get(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

//dsh:test DH-T-HOST-1
func TestBindTable(t *testing.T) {
	for _, in := range []string{"127.0.0.1:0", "[::1]:8080", "[::ffff:127.0.0.1]:8080", "[fe80::1%eth0]:80", "192.0.2.7:443"} {
		ap, err := listen.ParseBind(in)
		if err != nil || ap.String() != in {
			t.Errorf("ParseBind(%q) = %v, %v; want the value as written", in, ap, err)
		}
	}
	refused := map[string]herr.Reason{
		"0.0.0.0:80": listen.ReasonBindUnspecified, "[::]:80": listen.ReasonBindUnspecified,
		"[::ffff:0.0.0.0]:80": listen.ReasonBindUnspecified, "localhost:80": listen.ReasonBindInvalid,
		"example.com:443": listen.ReasonBindInvalid, "127.0.0.1": listen.ReasonBindInvalid, "": listen.ReasonBindInvalid,
	}
	for in, want := range refused {
		_, err := listen.ParseBind(in)
		if !herr.Has(err, want) || !strings.Contains(err.Error(), `"`+in+`"`) {
			t.Errorf("ParseBind(%q) err = %v; want %s quoting the value", in, err, want)
		}
	}
	l, err := start(t, cordisRoot(t), listen.Config{Address: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(l.Origin(), "http://127.0.0.1:") || l.Origin() == "http://127.0.0.1:0" || l.Mode() != listen.Local || l.Secure() {
		t.Errorf("origin %s mode %s secure %v; want the parsed address with its bound port, local, plain", l.Origin(), l.Mode(), l.Secure())
	}
}

// A non-loopback bind without TLS fails start-up before anything listens.
//
//dsh:test DH-T-HOST-3
func TestRemoteBindWithoutTLSRefused(t *testing.T) {
	_, err := start(t, cordisRoot(t), listen.Config{Address: "192.0.2.7:0"})
	if !herr.Has(err, listen.ReasonInsecureRemote) {
		t.Fatalf("err = %v, want %s", err, listen.ReasonInsecureRemote)
	}
}

// TC-10b: mounts /api and /api/sdk; the longest prefix answers, and only after
// admission. With an admit-none fake the answer is 401 and no handler runs.
//
//dsh:test DH-T-HOST-4
func TestLongestPrefixMountAfterAdmission(t *testing.T) {
	for _, yes := range []bool{true, false} {
		r := cordisRoot(t)
		l, err := start(t, r, listen.Config{Address: "127.0.0.1:0"})
		if err != nil {
			t.Fatal(err)
		}
		admit(t, r, yes)
		api, sdk := &recorder{name: "api"}, &recorder{name: "sdk"}
		if err := mount(t, r, l, "api", listen.Mount{Prefix: "/api", Serve: api}); err != nil {
			t.Fatal(err)
		}
		if err := mount(t, r, l, "sdk", listen.Mount{Prefix: "/api/sdk", Serve: sdk}); err != nil {
			t.Fatal(err)
		}
		c := client(t)
		code, body := get(t, c, l.Origin()+"/api/sdk/x")
		if yes {
			if code != http.StatusOK || body != "sdk p1" {
				t.Errorf("admit-all: GET /api/sdk/x = %d %q, want 200 from /api/sdk with the peer", code, body)
			}
			if code, body := get(t, c, l.Origin()+"/api/sdkx"); code != http.StatusOK || body != "api p1" {
				t.Errorf("admit-all: GET /api/sdkx = %d %q, want /api: a prefix matches whole segments", code, body)
			}
			if code, _ := get(t, c, l.Origin()+"/other"); code != http.StatusNotFound {
				t.Errorf("GET /other = %d, want 404 with no mount", code)
			}
			continue
		}
		if code != http.StatusUnauthorized || api.calls.Load()+sdk.calls.Load() != 0 {
			t.Errorf("admit-none: GET /api/sdk/x = %d, calls %d; want 401 and no handler run", code, api.calls.Load()+sdk.calls.Load())
		}
	}
}

//dsh:test DH-T-HOST-4
func TestMountRules(t *testing.T) {
	r := cordisRoot(t)
	ready := make(chan struct{})
	s, err := listen.Start(r, listen.Config{Address: "127.0.0.1:0"}, ready)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	c := client(t)
	h := &recorder{name: "h"}
	if err := mount(t, r, s, "first", listen.Mount{Prefix: "/x", Serve: h}); err != nil {
		t.Fatal(err)
	}
	if err := mount(t, r, s, "second", listen.Mount{Prefix: "/x", Serve: h}); !herr.Has(err, listen.ReasonMountTaken) {
		t.Errorf("same prefix twice: err = %v, want %s", err, listen.ReasonMountTaken)
	}
	if err := mount(t, r, s, "bad", listen.Mount{Prefix: "x", Serve: h}); !herr.Has(err, listen.ReasonMountInvalid) {
		t.Errorf("prefix without /: err = %v, want %s", err, listen.ReasonMountInvalid)
	}
	deny := func(*http.Request) listen.Class { return listen.Deny }
	boom := func(*http.Request) listen.Class { panic("classify") }
	anon := func(*http.Request) listen.Class { return listen.Anonymous }
	for prefix, m := range map[string]listen.Mount{
		"/deny":   {Classify: deny, Serve: h},
		"/panic":  {Classify: boom, Serve: h},
		"/stream": {Classify: anon, Stream: func(*http.Request) bool { return true }, Serve: h},
		"/fault":  {Classify: anon, Serve: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("handler") })},
	} {
		m.Prefix = prefix
		if err := mount(t, r, s, prefix, m); err != nil {
			t.Fatal(err)
		}
	}
	for path, want := range map[string]int{"/deny": 403, "/panic": 403, "/stream": 503, "/fault": 500, "/x": 401} {
		if code, _ := get(t, c, s.Origin()+path); code != want {
			t.Errorf("GET %s = %d, want %d", path, code, want)
		}
	}
	if h.calls.Load() != 0 {
		t.Errorf("handler ran %d times before admission", h.calls.Load())
	}
	close(ready)
	if code, body := get(t, c, s.Origin()+"/stream"); code != 200 || body != "h none" {
		t.Errorf("GET /stream after Ready = %d %q, want 200 with no peer", code, body)
	}

	// The owner unloading removes its mount.
	f, err := r.Apply("owner", func(ctx *cordis.Context) error {
		return s.Mount(ctx, listen.Mount{Prefix: "/gone", Classify: anon, Serve: h})
	})
	if err != nil || f.Wait() != nil {
		t.Fatal(err)
	}
	if code, _ := get(t, c, s.Origin()+"/gone"); code != 200 {
		t.Fatalf("GET /gone = %d before unload", code)
	}
	f.Dispose()
	if code, _ := get(t, c, s.Origin()+"/gone"); code != 404 {
		t.Errorf("GET /gone = %d after its owner unloaded, want 404", code)
	}
}
