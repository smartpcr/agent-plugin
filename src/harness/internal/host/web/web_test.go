package web_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/web"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func root(t *testing.T) *cordis.Context {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	return r
}

func load(t *testing.T, r *cordis.Context, name string, body func(ctx *cordis.Context) error, inject ...string) (*cordis.Fiber, error) {
	t.Helper()
	f, err := r.Apply(name, body, inject...)
	if err != nil {
		return nil, err
	}
	t.Cleanup(f.Dispose)
	return f, f.Wait()
}

func ok(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, "ok") }

func route(name, method, pattern string, effect web.Effect, params ...string) web.Route {
	return web.Route{Name: name, Method: method, Pattern: pattern, Effect: effect, Params: params, Serve: http.HandlerFunc(ok)}
}

// A declaration with no effect, and every other refused declaration, fails
// registration with its reason; the table is unchanged, also for the declarations of
// the same call that were valid.
//
//dsh:test DH-T-API-1
func TestUndeclaredRouteRefused(t *testing.T) {
	r := root(t)
	routes := web.New(web.Config{}, nil)
	owner := func(name string, decls ...web.Route) error {
		_, err := load(t, r, name, func(ctx *cordis.Context) error {
			_, err := routes.Handle(ctx, decls...)
			return err
		})
		return err
	}
	if err := owner("first", route("session.show", http.MethodGet, "/sessions/{sid}", web.Read, "sid")); err != nil {
		t.Fatal(err)
	}
	before := routes.List()

	undeclared := route("session.prompt", http.MethodPost, "/sessions/{sid}/prompt", "", "sid")
	err := owner("undeclared", route("home", http.MethodGet, "/", web.Read), undeclared)
	if !herr.Has(err, web.ReasonUndeclared) {
		t.Fatalf("undeclared effect: %v, want %s", err, web.ReasonUndeclared)
	}
	if got := routes.List(); !reflect.DeepEqual(got, before) {
		t.Fatalf("table changed by a refused call: %v, want %v", got, before)
	}

	noServe := route("x.no-serve", http.MethodGet, "/x", web.Read)
	noServe.Serve = nil
	for _, c := range []struct {
		name   string
		decls  []web.Route
		reason herr.Reason
	}{
		{"unknown effect", []web.Route{route("x.a", http.MethodGet, "/x", "write")}, web.ReasonInvalid},
		{"method outside the effect", []web.Route{route("x.a", http.MethodPost, "/x", web.Read)}, web.ReasonInvalid},
		{"GET that changes", []web.Route{route("x.a", http.MethodGet, "/x", web.Change)}, web.ReasonInvalid},
		{"unbound path parameter", []web.Route{route("x.a", http.MethodGet, "/x/{id}", web.Read)}, web.ReasonInvalid},
		{"parameter not in the pattern", []web.Route{route("x.a", http.MethodGet, "/x", web.Read, "id")}, web.ReasonInvalid},
		{"bad name", []web.Route{route("X A", http.MethodGet, "/x", web.Read)}, web.ReasonInvalid},
		{"no handler", []web.Route{noServe}, web.ReasonInvalid},
		{"relative pattern", []web.Route{route("x.a", http.MethodGet, "x", web.Read)}, web.ReasonInvalid},
		{"trailing slash", []web.Route{route("x.a", http.MethodGet, "/x/", web.Read)}, web.ReasonInvalid},
		{"duplicate name", []web.Route{route("session.show", http.MethodGet, "/x", web.Read)}, web.ReasonTaken},
		{"duplicate pattern", []web.Route{route("x.a", http.MethodGet, "/sessions/{sid}", web.Read, "sid")}, web.ReasonTaken},
		{"duplicate in one call", []web.Route{route("x.a", http.MethodGet, "/x", web.Read), route("x.a", http.MethodGet, "/y", web.Read)}, web.ReasonTaken},
		{"conflicting pattern", []web.Route{route("x.a", http.MethodGet, "/sessions/{id}", web.Read, "id")}, web.ReasonInvalid},
	} {
		if err := owner(c.name, c.decls...); !herr.Has(err, c.reason) {
			t.Errorf("%s: %v, want %s", c.name, err, c.reason)
		}
		if got := routes.List(); !reflect.DeepEqual(got, before) {
			t.Errorf("%s: table changed: %v", c.name, got)
		}
	}
	if _, err := routes.Handle(nil, route("x.a", http.MethodGet, "/x", web.Read)); err == nil {
		t.Error("a registration with no owner was accepted")
	}
}

type peer string

func (p peer) ID() string             { return string(p) }
func (peer) Context() context.Context { return context.Background() }
func (peer) FormToken() string        { return "" }

// host loads host.listen on 127.0.0.1:0, an admit-all fake and host.web, and
// returns the table and the listener's origin. Each pause, if any, runs inside the
// fake's authentication of every request, after the listener's stream decision.
func host(t *testing.T, r *cordis.Context, pause ...func()) (*web.Routes, string) {
	t.Helper()
	lf, err := r.Use(listen.Component, listen.Config{Port: new(uint16(0))})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(lf.Dispose)
	if err := lf.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := load(t, r, "fake auth", func(ctx *cordis.Context) error {
		_, err := listen.Authenticate.On(ctx, func(*cordis.Context, *listen.Credentials) (listen.Peer, bool, error) {
			for _, p := range pause {
				p()
			}
			return peer("p1"), true, nil
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wf, err := r.Use(web.Component, web.Config{UnloadGrace: 3 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(wf.Dispose)
	if err := wf.Wait(); err != nil {
		t.Fatal(err)
	}
	var routes *web.Routes
	var origin string
	if _, err := load(t, r, "get", func(ctx *cordis.Context) error {
		l, err := listen.Key.Get(ctx)
		if err != nil {
			return err
		}
		origin = l.Origin()
		routes, err = web.Key.Get(ctx)
		return err
	}, listen.Key.Name(), web.Key.Name()); err != nil {
		t.Fatal(err)
	}
	if routes == nil {
		t.Fatal("host.web provided no table")
	}
	return routes, origin
}

func client(t *testing.T) *http.Client {
	t.Helper()
	c := &http.Client{Transport: &http.Transport{}, Timeout: 10 * time.Second}
	t.Cleanup(c.CloseIdleConnections)
	return c
}

func status(t *testing.T, c *http.Client, url string) (int, string) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// A slow handler serving a request on the loopback listener has its context
// cancelled, with ErrUnloaded as the cause, when its component is disposed; the
// dispose returns once the handler has, and the next request is 404.
//
//dsh:test DH-T-API-1
func TestCleanupCancelsInFlight(t *testing.T) {
	r := root(t)
	routes, origin := host(t, r)

	started, cause := make(chan struct{}), make(chan error, 1)
	slow := web.Route{Name: "slow.show", Method: http.MethodGet, Pattern: "/slow/{id}", Effect: web.Read, Params: []string{"id"},
		Serve: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.PathValue("id") != "7" {
				t.Errorf("path value %q, want 7", req.PathValue("id"))
			}
			if _, ok := listen.PeerFrom(req.Context()); !ok {
				t.Error("no peer in the request context")
			}
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			close(started)
			select {
			case <-req.Context().Done():
				cause <- context.Cause(req.Context())
			case <-time.After(10 * time.Second):
				cause <- errors.New("not cancelled")
			}
		})}
	f, err := load(t, r, "slow routes", func(ctx *cordis.Context) error {
		k, err := web.Key.Get(ctx)
		if err != nil {
			return err
		}
		_, err = k.Handle(ctx, slow)
		return err
	}, web.Key.Name())
	if err != nil {
		t.Fatal(err)
	}
	if got := routes.List(); len(got) != 1 || got[0].Name != "slow.show" {
		t.Fatalf("table %v, want slow.show", got)
	}

	c := client(t)
	resp, err := c.Get(origin + "/slow/7")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	<-started

	f.Dispose()
	select {
	case err := <-cause:
		if !errors.Is(err, web.ErrUnloaded) {
			t.Fatalf("handler context cause %v, want %v", err, web.ErrUnloaded)
		}
	default:
		t.Fatal("dispose returned before the handler did")
	}
	io.Copy(io.Discard, resp.Body)

	if code, _ := status(t, c, origin+"/slow/7"); code != http.StatusNotFound {
		t.Fatalf("after dispose: %d, want 404", code)
	}
	if got := routes.List(); len(got) != 0 {
		t.Fatalf("table after dispose %v, want empty", got)
	}
}

// Disposing the returned effect removes only its own routes; the root path is the
// root alone, a GET route answers HEAD, and only Stream routes stream.
func TestEffectHandleAndDispatch(t *testing.T) {
	r := root(t)
	routes, origin := host(t, r)
	var home, events *cordis.EffectHandle
	if _, err := load(t, r, "routes", func(ctx *cordis.Context) (err error) {
		if home, err = routes.Handle(ctx, route("home", http.MethodGet, "/", web.Read)); err != nil {
			return err
		}
		events, err = routes.Handle(ctx, route("events", http.MethodGet, "/events", web.Stream))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c := client(t)
	if code, body := status(t, c, origin+"/"); code != http.StatusOK || body != "ok" {
		t.Fatalf("GET /: %d %q", code, body)
	}
	if code, _ := status(t, c, origin+"/other"); code != http.StatusNotFound {
		t.Fatalf("GET /other: %d, want 404", code)
	}
	if resp, err := c.Head(origin + "/"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD /: %v %v", resp, err)
	}
	ev, _ := http.NewRequest(http.MethodGet, origin+"/events", nil)
	hm, _ := http.NewRequest(http.MethodGet, origin+"/", nil)
	if !routes.Streams(ev) || routes.Streams(hm) {
		t.Fatal("Streams reports the wrong routes")
	}

	home.Dispose()
	if code, _ := status(t, c, origin+"/"); code != http.StatusNotFound {
		t.Fatalf("GET / after dispose: %d, want 404", code)
	}
	if got := routes.List(); len(got) != 1 || got[0].Name != "events" || !events.Alive() {
		t.Fatalf("table %v, want events alone", got)
	}
}

// The listener decides whether a request streams before it authenticates it. A Read
// route replaced by a Stream route at the same path while a request is being
// authenticated must not run the Stream handler under the old non-stream decision
// (ungated, compressible): it is refused 503 before the handler runs. The next
// request, decided over the new table, streams.
//
//dsh:test DH-T-API-1
func TestStreamDecisionPinnedAcrossSwap(t *testing.T) {
	r := root(t)
	var armed atomic.Bool
	entered, release := make(chan struct{}), make(chan struct{})
	routes, origin := host(t, r, func() {
		if armed.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
	})
	read, err := load(t, r, "read route", func(ctx *cordis.Context) error {
		_, err := routes.Handle(ctx, route("x.read", http.MethodGet, "/x", web.Read))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	ran := make(chan struct{}, 2)
	c := client(t)
	codes := make(chan int, 1)
	armed.Store(true)
	go func() {
		resp, err := c.Get(origin + "/x")
		if err != nil {
			codes <- -1
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		codes <- resp.StatusCode
	}()
	<-entered // the listener has decided "not a stream" over the table holding x.read

	read.Dispose()
	if _, err := load(t, r, "stream route", func(ctx *cordis.Context) error {
		_, err := routes.Handle(ctx, web.Route{Name: "x.stream", Method: http.MethodGet, Pattern: "/x", Effect: web.Stream,
			Serve: http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if s, ok := listen.Streaming(req.Context()); !ok || !s {
					t.Errorf("stream handler ran with stream decision %v, %v", s, ok)
				}
				ran <- struct{}{}
				io.WriteString(w, "stream")
			})})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if code := <-codes; code != http.StatusServiceUnavailable {
		t.Fatalf("request decided over the old table: %d, want 503", code)
	}
	select {
	case <-ran:
		t.Fatal("the stream handler ran under a non-stream decision")
	default:
	}
	if code, body := status(t, c, origin+"/x"); code != http.StatusOK || body != "stream" {
		t.Fatalf("request over the new table: %d %q, want 200 stream", code, body)
	}
}

// Concurrent registrations, dispatch and removals: a reader never sees a batch
// partially published, of two conflicting registrations exactly one succeeds, and
// an unrelated route keeps serving through every swap and cleanup.
func TestConcurrentPublication(t *testing.T) {
	r := root(t)
	routes := web.New(web.Config{}, nil)
	handle := func(name string, decls ...web.Route) (*cordis.Fiber, error) {
		var herrs error
		f, err := r.Apply(name, func(ctx *cordis.Context) error {
			_, herrs = routes.Handle(ctx, decls...)
			return herrs
		})
		if err != nil {
			return nil, err
		}
		f.Wait()
		return f, herrs
	}
	stable, err := handle("stable", route("stable", http.MethodGet, "/stable", web.Read))
	if err != nil {
		t.Fatal(err)
	}
	defer stable.Dispose()

	const batches, rounds, contested = 8, 15, 8
	stop := make(chan struct{})
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			select {
			case <-stop:
				return
			default:
			}
			names := map[string]bool{}
			for _, info := range routes.List() {
				names[info.Name] = true
			}
			if !names["stable"] {
				t.Error("the unrelated route left the table")
				return
			}
			for i := range batches {
				if a, b := names[fmt.Sprintf("b%d.a", i)], names[fmt.Sprintf("b%d.b", i)]; a != b {
					t.Errorf("batch %d partially visible: a=%v b=%v", i, a, b)
					return
				}
			}
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/stable", nil))
			if rec.Code != http.StatusOK {
				t.Errorf("GET /stable during swaps: %d", rec.Code)
				return
			}
		}
	}()

	var wg sync.WaitGroup
	for i := range batches {
		wg.Go(func() {
			for n := range rounds {
				f, err := handle(fmt.Sprintf("batch %d/%d", i, n),
					route(fmt.Sprintf("b%d.a", i), http.MethodGet, fmt.Sprintf("/b%d/a", i), web.Read),
					route(fmt.Sprintf("b%d.b", i), http.MethodPost, fmt.Sprintf("/b%d/b", i), web.Change))
				if err != nil {
					t.Errorf("batch %d round %d: %v", i, n, err)
					return
				}
				f.Dispose()
			}
		})
	}
	var won [contested]atomic.Int32
	var keep sync.Mutex
	var kept []*cordis.Fiber
	for k := range contested {
		for _, side := range []string{"x", "y"} {
			wg.Go(func() {
				f, err := handle(fmt.Sprintf("contest %d %s", k, side),
					route(fmt.Sprintf("c%d.%s", k, side), http.MethodGet, fmt.Sprintf("/c%d", k), web.Read))
				if err == nil {
					won[k].Add(1)
				} else if !herr.Has(err, web.ReasonTaken) {
					t.Errorf("contest %d %s: %v, want %s", k, side, err, web.ReasonTaken)
				}
				if f != nil {
					keep.Lock()
					kept = append(kept, f)
					keep.Unlock()
				}
			})
		}
	}
	wg.Wait()
	close(stop)
	<-readerDone

	for k := range contested {
		if n := won[k].Load(); n != 1 {
			t.Errorf("contest %d: %d registrations succeeded, want 1", k, n)
		}
	}
	got := map[string]bool{}
	for _, info := range routes.List() {
		got[info.Name] = true
	}
	if len(got) != 1+contested || !got["stable"] {
		t.Fatalf("table after the run %v, want stable and one route per contest", got)
	}
	for _, f := range kept {
		f.Dispose()
	}
	if got := routes.List(); len(got) != 1 || got[0].Name != "stable" {
		t.Fatalf("table after cleanup %v, want stable alone", got)
	}
}
