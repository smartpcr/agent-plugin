//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"reflect"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/listen"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/web"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

type drtPeer struct{ id string }

func (p drtPeer) ID() string               { return p.id }
func (p drtPeer) Context() context.Context { return context.Background() }
func (p drtPeer) FormToken() string        { return "" }

var drtWildcard = regexp.MustCompile(`\{([^{}]*)\}`)

// drtRoute declares name as "METHOD PATTERN" with effect, its Params being the
// pattern's wildcards.
func drtRoute(name, request string, effect web.Effect, serve http.Handler) (web.Route, error) {
	method, pattern, ok := strings.Cut(request, " ")
	if !ok {
		return web.Route{}, fmt.Errorf("route %q: want METHOD PATTERN", request)
	}
	var params []string
	for _, m := range drtWildcard.FindAllStringSubmatch(pattern, -1) {
		if p := strings.TrimSuffix(m[1], "..."); p != "$" {
			params = append(params, p)
		}
	}
	if serve == nil {
		serve = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, name) })
	}
	return web.Route{Name: name, Method: method, Pattern: pattern, Effect: effect, Params: params, Serve: serve}, nil
}

type drtState struct {
	root   *cordis.Context
	fibers []*cordis.Fiber
	routes *web.Routes
	origin string

	before []web.Info
	err    error

	owner   *cordis.Fiber
	started chan struct{}
	cause   chan error
	// handlerDoneAtDispose records whether the slow handler had already
	// returned at the instant Dispose returned.
	handlerDoneAtDispose bool
	pathID               atomic.Value
	peerID               atomic.Value
	resp                 *http.Response

	status int
	body   string
}

func (s *drtState) newRoot() {
	s.root = cordis.New()
	s.root.Loggers().ClearExporters()
	s.root.Loggers().AddExporter(&cordis.MemoryExporter{})
}

func (s *drtState) keep(f *cordis.Fiber, err error) (*cordis.Fiber, error) {
	if err != nil {
		return nil, err
	}
	s.fibers = append(s.fibers, f)
	return f, f.Wait()
}

func (s *drtState) close() {
	if s.resp != nil {
		s.resp.Body.Close()
	}
	for i := len(s.fibers) - 1; i >= 0; i-- {
		s.fibers[i].Dispose()
	}
	if s.root != nil {
		s.root.Dispose()
	}
}

func (s *drtState) emptyTable() error {
	s.newRoot()
	s.routes = web.New(web.Config{}, nil)
	if got := s.routes.List(); len(got) != 0 {
		return fmt.Errorf("new table lists %v, want none", got)
	}
	return nil
}

// register runs Routes.Handle with decls in a new component and returns its error.
func (s *drtState) register(decls ...web.Route) (*cordis.Fiber, error) {
	return s.keep(s.root.Apply("routes", func(ctx *cordis.Context) error {
		_, err := s.routes.Handle(ctx, decls...)
		return err
	}))
}

func (s *drtState) registered(name, request, effect string) error {
	r, err := drtRoute(name, request, web.Effect(effect), nil)
	if err != nil {
		return err
	}
	if _, err := s.register(r); err != nil {
		return fmt.Errorf("register %s: %w", name, err)
	}
	s.before = s.routes.List()
	return nil
}

func (s *drtState) registersMixed(n1, r1, e1, n2, r2 string) error {
	a, err := drtRoute(n1, r1, web.Effect(e1), nil)
	if err != nil {
		return err
	}
	b, err := drtRoute(n2, r2, "", nil)
	if err != nil {
		return err
	}
	_, s.err = s.register(a, b)
	return nil
}

func (s *drtState) registersUndeclared(name, request string) error {
	r, err := drtRoute(name, request, "", nil)
	if err != nil {
		return err
	}
	_, s.err = s.register(r)
	return nil
}

func (s *drtState) refusedWith(reason string) error {
	if s.err == nil {
		return errors.New("the registration was accepted")
	}
	if !herr.Has(s.err, herr.Reason(reason)) {
		return fmt.Errorf("refused with %v, want reason %s", s.err, reason)
	}
	return nil
}

func (s *drtState) unchanged() error {
	if got := s.routes.List(); !reflect.DeepEqual(got, s.before) {
		return fmt.Errorf("table %v, want %v", got, s.before)
	}
	return nil
}

func (s *drtState) listsOnly(name string) error {
	got := s.routes.List()
	if len(got) != 1 || got[0].Name != name {
		return fmt.Errorf("table %v, want only %s", got, name)
	}
	return nil
}

func (s *drtState) empty() error {
	if got := s.routes.List(); len(got) != 0 {
		return fmt.Errorf("table %v, want empty", got)
	}
	return nil
}

func (s *drtState) loadHost(bind string) error {
	ap, err := netip.ParseAddrPort(bind)
	if err != nil {
		return err
	}
	if !ap.Addr().IsLoopback() {
		return fmt.Errorf("bind %s: the scenario runs on loopback only", bind)
	}
	s.newRoot()
	port := ap.Port()
	if _, err := s.keep(s.root.Use(listen.Component, listen.Config{Bind: ap.Addr().String(), Port: &port, ShutdownGrace: 2 * time.Second})); err != nil {
		return fmt.Errorf("load host.listen: %w", err)
	}
	if _, err := s.keep(s.root.Apply("fake authenticate", func(ctx *cordis.Context) error {
		_, err := listen.Authenticate.On(ctx, func(_ *cordis.Context, c *listen.Credentials) (listen.Peer, bool, error) {
			return drtPeer{"p1"}, c.Request != nil, nil
		})
		return err
	})); err != nil {
		return err
	}
	if _, err := s.keep(s.root.Use(web.Component, web.Config{UnloadGrace: 3 * time.Second})); err != nil {
		return fmt.Errorf("load host.web: %w", err)
	}
	_, err = s.keep(s.root.Apply("get", func(ctx *cordis.Context) error {
		l, err := listen.Key.Get(ctx)
		if err != nil {
			return err
		}
		s.origin = l.Origin()
		s.routes, err = web.Key.Get(ctx)
		return err
	}, listen.Key.Name(), web.Key.Name()))
	return err
}

func (s *drtState) registeredSlow(name, request, effect string) error {
	s.started, s.cause = make(chan struct{}), make(chan error, 1)
	slow := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		s.pathID.Store(req.PathValue("id"))
		if p, ok := listen.PeerFrom(req.Context()); ok && p != nil {
			s.peerID.Store(p.ID())
		}
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(s.started)
		select {
		case <-req.Context().Done():
			s.cause <- context.Cause(req.Context())
		case <-time.After(10 * time.Second):
			s.cause <- errors.New("not cancelled")
		}
	})
	r, err := drtRoute(name, request, web.Effect(effect), slow)
	if err != nil {
		return err
	}
	f, err := s.keep(s.root.Apply("slow routes", func(ctx *cordis.Context) error {
		k, err := web.Key.Get(ctx)
		if err != nil {
			return err
		}
		_, err = k.Handle(ctx, r)
		return err
	}, web.Key.Name()))
	if err != nil {
		return fmt.Errorf("register %s: %w", name, err)
	}
	s.owner = f
	return s.listsOnly(name)
}

func (s *drtState) client() *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{DisableKeepAlives: true}}
}

func (s *drtState) request(request string) (*http.Response, error) {
	method, path, ok := strings.Cut(request, " ")
	if !ok {
		return nil, fmt.Errorf("request %q: want METHOD PATH", request)
	}
	req, err := http.NewRequest(method, s.origin+path, nil)
	if err != nil {
		return nil, err
	}
	return s.client().Do(req)
}

func (s *drtState) sendSlow(request string) error {
	resp, err := s.request(request)
	if err != nil {
		return err
	}
	s.resp = resp
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slow request status %d, want 200", resp.StatusCode)
	}
	select {
	case <-s.started:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("the slow handler did not start")
	}
}

func (s *drtState) disposeOwner() error {
	if s.owner == nil {
		return errors.New("no component registered the slow route")
	}
	s.owner.Dispose()
	select {
	case err := <-s.cause:
		s.cause <- err
		s.handlerDoneAtDispose = true
	default:
		s.handlerDoneAtDispose = false
	}
	return nil
}

func (s *drtState) cancelledByUnload() error {
	select {
	case err := <-s.cause:
		s.cause <- err
		if !errors.Is(err, web.ErrUnloaded) {
			return fmt.Errorf("handler context cause %v, want %v", err, web.ErrUnloaded)
		}
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("the slow handler's context was not cancelled")
	}
}

// waited asserts on the snapshot disposeOwner took immediately after Dispose
// returned: the handler sends its cause before returning, so the cause must
// already have been there if Dispose waited for the handler. Checking later
// would be vacuous because cancelledByUnload blocks until the cause arrives.
func (s *drtState) waited() error {
	if !s.handlerDoneAtDispose {
		return errors.New("the dispose returned before the slow handler did")
	}
	return nil
}

func (s *drtState) sawPath(name, want, peer string) error {
	if name != "id" {
		return fmt.Errorf("the slow handler records only path value id, not %q", name)
	}
	if got, _ := s.pathID.Load().(string); got != want {
		return fmt.Errorf("path value %q, want %q", got, want)
	}
	if got, _ := s.peerID.Load().(string); got != peer {
		return fmt.Errorf("peer %q, want %q", got, peer)
	}
	return nil
}

func (s *drtState) send(request string) error {
	resp, err := s.request(request)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	s.status, s.body = resp.StatusCode, string(b)
	return nil
}

func (s *drtState) statusIs(want int) error {
	if s.status != want {
		return fmt.Errorf("status %d (body %q), want %d", s.status, s.body, want)
	}
	return nil
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_declared_effect_route_table(ctx *godog.ScenarioContext) {
	var s *drtState
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s = &drtState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, nil
	})
	ctx.Step(`^an empty host\.web route table$`, func() error { return s.emptyTable() })
	ctx.Step(`^a component has registered "([^"]*)" as "([^"]*)" with effect "([^"]*)"$`, func(n, r, e string) error { return s.registered(n, r, e) })
	ctx.Step(`^a component registers "([^"]*)" as "([^"]*)" with effect "([^"]*)" and "([^"]*)" as "([^"]*)" with no effect$`, func(n1, r1, e1, n2, r2 string) error { return s.registersMixed(n1, r1, e1, n2, r2) })
	ctx.Step(`^a component registers "([^"]*)" as "([^"]*)" with no effect$`, func(n, r string) error { return s.registersUndeclared(n, r) })
	ctx.Step(`^the registration is refused with reason "([^"]*)"$`, func(r string) error { return s.refusedWith(r) })
	ctx.Step(`^the route table is unchanged$`, func() error { return s.unchanged() })
	ctx.Step(`^the route table lists only "([^"]*)"$`, func(n string) error { return s.listsOnly(n) })
	ctx.Step(`^the route table is empty$`, func() error { return s.empty() })
	ctx.Step(`^host\.listen is loaded on "([^"]*)" with an admit-all authenticate fake and host\.web$`, func(b string) error { return s.loadHost(b) })
	ctx.Step(`^a component has registered the slow route "([^"]*)" as "([^"]*)" with effect "([^"]*)"$`, func(n, r, e string) error { return s.registeredSlow(n, r, e) })
	ctx.Step(`^"([^"]*)" is sent and the slow handler has started$`, func(r string) error { return s.sendSlow(r) })
	ctx.Step(`^the component is disposed$`, func() error { return s.disposeOwner() })
	ctx.Step(`^the slow handler's context was cancelled because its owner unloaded$`, func() error { return s.cancelledByUnload() })
	ctx.Step(`^the dispose returned only after the slow handler did$`, func() error { return s.waited() })
	ctx.Step(`^the slow handler saw path value "([^"]*)" = "([^"]*)" for peer "([^"]*)"$`, func(n, v, p string) error { return s.sawPath(n, v, p) })
	ctx.Step(`^"([^"]*)" is sent$`, func(r string) error { return s.send(r) })
	ctx.Step(`^the response status is (\d+)$`, func(n int) error { return s.statusIs(n) })
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_declared_effect_route_table(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_declared_effect_route_table,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_declared_effect_route_table.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("the declared-effect route table e2e scenarios failed")
	}
}
