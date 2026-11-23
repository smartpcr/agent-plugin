//go:build e2e

// Stage 11.3 (agent lifecycle and delegation seam) scenarios: the creation
// transaction's rollback (DH-T-AGENT-19, TC-11a), a delegated child torn down
// with its one owner (DH-D-SUBAGENT-1, TC-11a) and the host's shutdown
// keeping the last events on the SQLite store (DH-T-AGENT-21, TC-11b). Every
// scenario runs the real registry, the driver's factory and the scripted fake
// model; nothing is skipped.

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/delegate/subagent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

const alsRoute vocab.RouteID = "r/als"

// alsWait bounds every real-time wait on the driver.
const alsWait = 20 * time.Second

// alsOuterT is the test running the suite; scenario TBs delegate to it.
var alsOuterT *testing.T

// alsTB is a testing.TB scoped to one scenario: the test kit's cleanups run
// when it is disposed and its errors fail the scenario.
type alsTB struct {
	testing.TB
	mu       sync.Mutex
	cleanups []func()
	errs     []string
}

func (t *alsTB) Helper() {}

func (t *alsTB) Cleanup(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanups = append(t.cleanups, f)
}

func (t *alsTB) Errorf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errs = append(t.errs, fmt.Sprintf(format, args...))
}

func (t *alsTB) Error(args ...any) { t.Errorf("%s", fmt.Sprint(args...)) }

func (t *alsTB) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	panic(fmt.Sprintf(format, args...))
}

func (t *alsTB) Fatal(args ...any) { t.Fatalf("%s", fmt.Sprint(args...)) }

func (t *alsTB) Failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.errs) > 0
}

func (t *alsTB) dispose() error {
	t.mu.Lock()
	cs := t.cleanups
	t.cleanups = nil
	t.mu.Unlock()
	for i := len(cs) - 1; i >= 0; i-- {
		cs[i]()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.errs) > 0 {
		return errors.New(strings.Join(t.errs, "; "))
	}
	return nil
}

type alsState struct {
	tb   *alsTB
	root *cordis.Context
	svc  session.Service
	reg  agent.Registry
	tr   *fakemodel.Transport
	tap  *alsTap

	mu       sync.Mutex
	created  []string
	disposed []string

	// Failed create.
	setup     agent.SetupFunc
	undone    []string
	createErr error

	// Delegation seam.
	rt         subagent.Runtime
	components map[string]*alsComponent
	act        subagent.Activation
	child      agent.Agent
	doneAtWait bool

	// Shutdown.
	path   string
	st     *sqlite.Store
	report session.OpenReport
	reopen session.Handle
	events []event.Event
}

type alsComponent struct {
	fiber *cordis.Fiber
	ctx   *cordis.Context
}

// boot builds the host: the scope and tool components, a session service on
// st, a registry seated with the driver's factory and the scripted model.
func (s *alsState) boot(st store.Store, turns ...fakemodel.Turn) error {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.tb.Cleanup(root.Dispose)
	for _, c := range []*cordis.Component{scope.Component, core.Component} {
		f, err := root.Use(c, cordis.None{})
		if err != nil {
			return err
		}
		if err := f.Wait(); err != nil {
			return err
		}
	}
	// The prompt component assembles each step's system prompt and tool list,
	// as the F2 rig composes it (plan 17.1.9).
	pf, err := root.Use(prompt.Component, prompt.Defaults())
	if err != nil {
		return err
	}
	if err := pf.Wait(); err != nil {
		return err
	}
	var (
		tsvc tools.Service
		ps   *prompt.Service
	)
	g, err := root.Inject([]string{tools.Key.Name(), prompt.Key.Name()}, func(c *cordis.Context) (err error) {
		if tsvc, err = tools.Key.Get(c); err != nil {
			return err
		}
		ps, err = prompt.Key.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		return err
	}
	s.root = root
	s.svc = session.NewService(root, st, session.Config{})
	s.reg = agent.NewRegistry(root)
	models := llm.New()
	s.tr = fakemodel.NewTransport(s.tb, turns...)
	s.tap = newALSTap(s.tr, turns)
	fakemodel.Register(s.tb, models, alsRoute, s.tap)
	f, err := driver.NewFactory(driver.FactoryOptions{
		Context: root, Sessions: s.svc, Scopes: scope.NewTable(root), Models: models, Tools: tsvc, Prompt: ps,
	})
	if err != nil {
		return err
	}
	if _, err := s.reg.SetFactory(root, f); err != nil {
		return err
	}
	if _, err := agent.Created.On(root, func(_ *cordis.Context, n agent.CreatedNotice) error {
		s.mu.Lock()
		s.created = append(s.created, string(n.Agent.Ref().Session))
		s.mu.Unlock()
		return nil
	}); err != nil {
		return err
	}
	_, err = agent.Disposed.On(root, func(_ *cordis.Context, n agent.DisposedNotice) error {
		s.mu.Lock()
		s.disposed = append(s.disposed, string(n.Ref.Session))
		s.mu.Unlock()
		return nil
	})
	return err
}

// waitHangPoint waits until the driver has read hanging model request n's
// whole scripted prefix and is blocked reading past it. The adapter reads the
// body again only after the normaliser has received every chunk decoded so far,
// and the normaliser folds a received chunk in before it can see an abort, so
// the prefix is then part of the attempt whatever cancels it next.
func (s *alsState) waitHangPoint(n int) error {
	select {
	case <-s.tap.hung(n - 1):
		return nil
	case <-time.After(alsWait):
		return fmt.Errorf("the driver never read model request %d up to its hang point (%d requests sent)", n, len(s.tr.Requests()))
	}
}

// alsTap wraps the fake transport so each hanging turn's body reports when it
// is read past its scripted events.
type alsTap struct {
	next  http.RoundTripper
	turns []fakemodel.Turn

	mu    sync.Mutex
	calls int
	chans map[int]chan struct{}
}

func newALSTap(next http.RoundTripper, turns []fakemodel.Turn) *alsTap {
	return &alsTap{next: next, turns: turns, chans: map[int]chan struct{}{}}
}

// hung is closed once request i's hanging body is read past its events.
func (t *alsTap) hung(i int) chan struct{} {
	t.mu.Lock()
	defer t.mu.Unlock()
	c, ok := t.chans[i]
	if !ok {
		c = make(chan struct{})
		t.chans[i] = c
	}
	return c
}

func (t *alsTap) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	i := t.calls
	t.calls++
	t.mu.Unlock()
	resp, err := t.next.RoundTrip(req)
	if err != nil || i >= len(t.turns) || !t.turns[i].Hang || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	// The SSE framing matches the fake transport's encoding of a turn.
	left := 0
	for _, e := range t.turns[i].Events {
		left += len("event: " + e.Name + "\ndata: " + e.Data + "\n\n")
	}
	resp.Body = &alsHangBody{ReadCloser: resp.Body, left: left, hung: t.hung(i)}
	return resp, nil
}

// alsHangBody counts the prefix bytes still unread; a Read issued with none
// left is the reader reaching the hang point.
type alsHangBody struct {
	io.ReadCloser
	left int
	once sync.Once
	hung chan struct{}
}

func (b *alsHangBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		b.once.Do(func() { close(b.hung) })
	}
	n, err := b.ReadCloser.Read(p)
	b.left -= n
	return n, err
}

func alsSplit(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *alsState) addressable(id string) error {
	if _, ok := s.reg.Get(session.ID(id)); ok {
		return fmt.Errorf("%s is still addressable", id)
	}
	for _, a := range s.reg.List() {
		if a.Ref().Session == session.ID(id) {
			return fmt.Errorf("%s is still listed", id)
		}
	}
	return nil
}

// --- Failed create leaves nothing ---

func (s *alsState) memRegistry() error { return s.boot(memstore.New()) }

func (s *alsState) thirdFails(one, two, three string) error {
	s.setup = func(sc *cordis.Context, _ agent.Agent) (func() error, error) {
		for _, n := range []string{one, two} {
			if _, err := sc.Effect(n, func() (cordis.Disposer, error) {
				return func() error {
					s.mu.Lock()
					s.undone = append(s.undone, n)
					s.mu.Unlock()
					return nil
				}, nil
			}); err != nil {
				return nil, err
			}
		}
		_, err := sc.Effect(three, func() (cordis.Disposer, error) {
			return nil, fmt.Errorf("%s fails", "third")
		})
		return nil, err
	}
	return nil
}

func (s *alsState) createAgent(id string) error {
	_, s.createErr = s.reg.Create(s.root, agent.CreateOptions{
		Session: session.ID(id), Model: agent.ModelSelection{Route: alsRoute}, Setup: s.setup,
	})
	return nil
}

func (s *alsState) creationFails(msg string) error {
	if s.createErr == nil || !strings.Contains(s.createErr.Error(), msg) {
		return fmt.Errorf("Create returned %v, want an error containing %q", s.createErr, msg)
	}
	return nil
}

func (s *alsState) notAddressable(id string) error { return s.addressable(id) }

func (s *alsState) noNotices() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.created) != 0 || len(s.disposed) != 0 {
		return fmt.Errorf("notices sent: created %v, disposed %v", s.created, s.disposed)
	}
	return nil
}

func (s *alsState) noPartialEvents(id string) error {
	rs, done, err := s.svc.OpenRead(context.Background(), session.ID(id))
	if err != nil {
		return fmt.Errorf("read session %s: %w", id, err)
	}
	defer done()
	evs, err := rs.Read(context.Background(), 0, 0)
	if err != nil {
		return err
	}
	for _, e := range evs {
		t := e.Type()
		if strings.HasPrefix(t, "turn/") || strings.HasPrefix(t, "step/") ||
			strings.HasPrefix(t, "tool/") || t == event.AssistantMessage.Name() {
			return fmt.Errorf("a partial agent wrote %s", t)
		}
	}
	return nil
}

func (s *alsState) rolledBack(order string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if want := alsSplit(order); !slices.Equal(s.undone, want) {
		return fmt.Errorf("rolled back %v, want %v", s.undone, want)
	}
	return nil
}

func (s *alsState) leaseFree(id string) error {
	h, _, err := s.svc.Open(context.Background(), nil, session.ID(id), session.OpenOptions{})
	if err != nil {
		return fmt.Errorf("the lease of %s was kept: %w", id, err)
	}
	_ = h.Close(context.Background())
	if n := len(s.tr.Requests()); n != 0 {
		return fmt.Errorf("a rolled-back agent called the model %d times", n)
	}
	return nil
}

// --- Child torn down with its owner ---

func (s *alsState) seam() error {
	if err := s.boot(memstore.New(), fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("par")}, Hang: true}); err != nil {
		return err
	}
	s.rt = subagent.New(s.reg)
	s.components = map[string]*alsComponent{}
	return nil
}

func (s *alsState) componentsLoaded(a, b string) error {
	for _, n := range []string{a, b} {
		c := &alsComponent{}
		f, err := s.root.Apply(n, func(ctx *cordis.Context) error { c.ctx = ctx; return nil })
		if err != nil {
			return err
		}
		if err := f.Wait(); err != nil {
			return err
		}
		c.fiber = f
		s.components[n] = c
	}
	return nil
}

func (s *alsState) component(n string) (*alsComponent, error) {
	c, ok := s.components[n]
	if !ok {
		return nil, fmt.Errorf("component %q was not loaded", n)
	}
	return c, nil
}

func (s *alsState) spec(child string) subagent.Spec {
	return subagent.Spec{Session: session.ID(child), Prompt: "do it", Model: &agent.ModelSelection{Route: alsRoute}}
}

func (s *alsState) delegates(owner, child, _ string) error {
	c, err := s.component(owner)
	if err != nil {
		return err
	}
	s.act, err = s.rt.Start(c.ctx, s.spec(child))
	if err != nil {
		return fmt.Errorf("start %s: %w", child, err)
	}
	s.child = s.act.Agent()
	return nil
}

func (s *alsState) inFlight() error { return s.waitHangPoint(1) }

func (s *alsState) oneOwner(owner, children string) error {
	c, err := s.component(owner)
	if err != nil {
		return err
	}
	child := s.act.Child()
	if o, ok := s.rt.OwnerOf(child); !ok || o != c.ctx {
		return fmt.Errorf("owner of %s is %v (%v), want %s", child, o, ok, owner)
	}
	var want []session.ID
	for _, n := range alsSplit(children) {
		want = append(want, session.ID(n))
	}
	if got := s.rt.Children(c.ctx); !slices.Equal(got, want) {
		return fmt.Errorf("children of %s: %v, want %v", owner, got, want)
	}
	return nil
}

func (s *alsState) secondOwnerRefused(other, child string) error {
	d, err := s.component(other)
	if err != nil {
		return err
	}
	if _, err := s.rt.Start(d.ctx, s.spec(child)); !errors.Is(err, subagent.ErrNameTaken) {
		return fmt.Errorf("a second owner for %s: %v, want %s", child, err, subagent.CodeNameTaken)
	}
	if got := s.rt.Children(d.ctx); len(got) != 0 {
		return fmt.Errorf("%s gained children %v", other, got)
	}
	return nil
}

func (s *alsState) unload(owner string) error {
	c, err := s.component(owner)
	if err != nil {
		return err
	}
	done := agent.Done(s.child)
	if done == nil {
		return errors.New("the child's driver never started")
	}
	c.fiber.Dispose()
	werr := c.fiber.Wait()
	select {
	case <-done:
		s.doneAtWait = true
	default:
	}
	if werr != nil && !errors.Is(werr, context.Canceled) {
		return fmt.Errorf("wait for %s: %w", owner, werr)
	}
	return nil
}

func (s *alsState) exitedAtWait(owner string) error {
	if !s.doneAtWait {
		return fmt.Errorf("the child's driver goroutine outlived the Wait of %s", owner)
	}
	return nil
}

func (s *alsState) noOwner(child string) error {
	if err := s.addressable(child); err != nil {
		return err
	}
	if o, ok := s.rt.OwnerOf(session.ID(child)); ok {
		return fmt.Errorf("%s kept the owner %v", child, o)
	}
	return nil
}

func (s *alsState) settledWith(stop, output, cleanup, diag string) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	r, err := s.act.Result(ctx)
	if err != nil {
		return fmt.Errorf("the result was not settled with the owner: %w", err)
	}
	if string(r.Stop) != stop || r.Output != output || string(r.Cleanup) != cleanup || r.Diagnostic != diag {
		return fmt.Errorf("result %+v, want stop %q output %q cleanup %q diagnostic %q", r, stop, output, cleanup, diag)
	}
	return nil
}

func (s *alsState) childDisposed() error {
	if st := s.child.State(); st != agent.StateDisposed {
		return fmt.Errorf("the child is %s", st)
	}
	return nil
}

// --- Shutdown keeps the last events ---

// sqliteHost opens the embedded store under DSH_E2E_STORE_HOME when the
// compose run mounts one, else in a test-kit temp dir.
func (s *alsState) sqliteHost() error {
	var dir string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		d, err := os.MkdirTemp(v, "e2e-als-")
		if err != nil {
			return err
		}
		s.tb.Cleanup(func() { _ = os.RemoveAll(d) })
		dir = d
	} else {
		dir = alsOuterT.TempDir()
	}
	s.path = filepath.Join(dir, "dsh.db")
	st, err := sqlite.Open(context.Background(), s.path)
	if err != nil {
		return fmt.Errorf("open the SQLite store: %w", err)
	}
	s.st = st
	s.tb.Cleanup(func() { _ = st.Close(context.Background()) })
	return s.boot(st, fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("par")}, Hang: true})
}

func (s *alsState) midStep(id, _ string) error {
	h, err := s.reg.Create(s.root, agent.CreateOptions{Session: session.ID(id), Model: agent.ModelSelection{Route: alsRoute}})
	if err != nil {
		return fmt.Errorf("create %s: %w", id, err)
	}
	if _, err := agent.Send(context.Background(), h.Agent(), agent.ListNextTurn,
		agent.InboxMessage{Source: agent.SourceUser, Text: "go"}); err != nil {
		return err
	}
	return s.waitHangPoint(1)
}

func (s *alsState) shutdown() error {
	s.root.Dispose()
	return s.st.Close(context.Background())
}

func (s *alsState) reopened() error {
	st, err := sqlite.Open(context.Background(), s.path)
	if err != nil {
		return fmt.Errorf("reopen the SQLite store: %w", err)
	}
	s.tb.Cleanup(func() { _ = st.Close(context.Background()) })
	root := cordis.New()
	s.tb.Cleanup(root.Dispose)
	h, rep, err := session.NewService(root, st, session.Config{}).Open(context.Background(), nil, "s", session.OpenOptions{})
	if err != nil {
		return fmt.Errorf("reopen the session: %w", err)
	}
	s.tb.Cleanup(func() { _ = h.Close(context.Background()) })
	s.reopen, s.report = h, rep
	s.events, err = h.Read(context.Background(), 0, 0)
	return err
}

func (s *alsState) nothingToClose() error {
	if s.report.Interrupted || len(s.report.Closers) != 0 {
		return fmt.Errorf("the reopen had to close the log: %+v", s.report)
	}
	return nil
}

func (s *alsState) closedBrackets() error {
	if b := s.reopen.Open(); b.IsOpen() {
		return fmt.Errorf("brackets still open: %+v", b)
	}
	if ts, te := len(alsOfType(s.events, "turn/start")), len(alsOfType(s.events, "turn/end")); ts == 0 || ts != te {
		return fmt.Errorf("%d turn/start, %d turn/end", ts, te)
	}
	if ss, se := len(alsOfType(s.events, "step/start")), len(alsOfType(s.events, "step/end")); ss == 0 || ss != se {
		return fmt.Errorf("%d step/start, %d step/end", ss, se)
	}
	return nil
}

func (s *alsState) lastTurnEnd(kind, cause string) error {
	ends := alsOfType(s.events, "turn/end")
	if len(ends) == 0 {
		return errors.New("no turn/end persisted")
	}
	var te event.TurnEnd
	if err := json.Unmarshal([]byte(ends[len(ends)-1].Raw()), &te); err != nil {
		return fmt.Errorf("decode turn/end: %w", err)
	}
	if string(te.Reason.Kind) != kind || te.Reason.Cause != cause {
		return fmt.Errorf("last turn/end %+v, want %s{%s}", te.Reason, kind, cause)
	}
	return nil
}

func (s *alsState) lastSettlement(text string) error {
	last, at, end := "", -1, -1
	for i, e := range s.events {
		switch e.Type() {
		case "turn/end":
			end = i
		case event.AssistantMessage.Name():
			p, err := event.AssistantMessage.Decode(e.Record())
			if err != nil {
				return err
			}
			for _, b := range p.Message.Blocks() {
				if b.Text != nil {
					last, at = b.Text.Text, i
				}
			}
		}
	}
	if last != text {
		return fmt.Errorf("the last settlement is %q, want %q", last, text)
	}
	if at > end {
		return fmt.Errorf("the settlement (event %d) follows the last turn/end (event %d)", at, end)
	}
	return nil
}

func alsOfType(evs []event.Event, typ string) []event.Event {
	var out []event.Event
	for _, e := range evs {
		if e.Type() == typ {
			out = append(out, e)
		}
	}
	return out
}

func InitializeScenario_agent_loop_milestone_m1_agent_lifecycle_and_delegation_seam(ctx *godog.ScenarioContext) {
	s := &alsState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = alsState{tb: &alsTB{TB: alsOuterT}}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if derr := s.tb.dispose(); derr != nil && err == nil {
			return c, fmt.Errorf("scenario cleanup: %w", derr)
		}
		return c, nil
	})

	ctx.Step(`^an agent registry seated with the driver's factory on the scripted fake model and the memory store$`, s.memRegistry)
	ctx.Step(`^a creation setup whose registrations "([^"]+)" and "([^"]+)" succeed and whose third registration "([^"]+)" fails$`, s.thirdFails)
	ctx.Step(`^agent "([^"]+)" is created$`, s.createAgent)
	ctx.Step(`^the creation fails with "([^"]+)"$`, s.creationFails)
	ctx.Step(`^agent "([^"]+)" is neither addressable nor listed$`, s.notAddressable)
	ctx.Step(`^no Created or Disposed notice was sent$`, s.noNotices)
	ctx.Step(`^session "([^"]+)" holds no partial agent events$`, s.noPartialEvents)
	ctx.Step(`^the registrations were rolled back in the order "([^"]+)"$`, s.rolledBack)
	ctx.Step(`^session "([^"]+)"'s lease is free and the model was never called$`, s.leaseFree)

	ctx.Step(`^a delegation seam over the driver's factory on the scripted fake model and the memory store$`, s.seam)
	ctx.Step(`^components "([^"]+)" and "([^"]+)" are loaded$`, s.componentsLoaded)
	ctx.Step(`^component "([^"]+)" delegates to child agent "([^"]+)" whose model streams "([^"]+)" and then hangs$`, s.delegates)
	ctx.Step(`^K's model request is in flight$`, s.inFlight)
	ctx.Step(`^K's one owner is "([^"]+)" and the children of "[^"]+" are "([^"]+)"$`, s.oneOwner)
	ctx.Step(`^component "([^"]+)" cannot also own "([^"]+)"$`, s.secondOwnerRefused)
	ctx.Step(`^component "([^"]+)" is unloaded$`, s.unload)
	ctx.Step(`^K's driver goroutine had exited when the Wait of "([^"]+)" returned$`, s.exitedAtWait)
	ctx.Step(`^"([^"]+)" is not addressable and has no owner$`, s.noOwner)
	ctx.Step(`^K's result was already settled "([^"]+)" with output "([^"]+)", cleanup "([^"]+)" and diagnostic "([^"]+)"$`, s.settledWith)
	ctx.Step(`^K is disposed$`, s.childDisposed)

	ctx.Step(`^a host on the embedded SQLite store in a temp home$`, s.sqliteHost)
	ctx.Step(`^agent "([^"]+)" is mid-step with its model streaming "([^"]+)" and then hanging$`, s.midStep)
	ctx.Step(`^the host shuts down$`, s.shutdown)
	ctx.Step(`^the SQLite store is reopened from its database file$`, s.reopened)
	ctx.Step(`^the reopen had nothing to close$`, s.nothingToClose)
	ctx.Step(`^the persisted log ends with closed brackets$`, s.closedBrackets)
	ctx.Step(`^the last turn/end records reason "([^"]+)" with cause "([^"]+)"$`, s.lastTurnEnd)
	ctx.Step(`^the last assistant settlement "([^"]+)" is persisted before the last turn/end$`, s.lastSettlement)
}

func TestE2E_agent_loop_milestone_m1_agent_lifecycle_and_delegation_seam(t *testing.T) {
	alsOuterT = t
	t.Cleanup(func() { alsOuterT = nil })
	suite := godog.TestSuite{
		Name:                "agent_loop_milestone_m1_agent_lifecycle_and_delegation_seam",
		ScenarioInitializer: InitializeScenario_agent_loop_milestone_m1_agent_lifecycle_and_delegation_seam,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"agent_loop_milestone_m1_agent_lifecycle_and_delegation_seam.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
