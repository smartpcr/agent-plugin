//go:build e2e

// Stage 11.4 (context pipeline and M1 closure) scenarios: the context
// pipeline's contributions are logged, ordered and rebuilt from the log
// (DH-T-CONTEXT-1, -2, TC-11a); flow F2's bracketed log matches the committed
// golden on memstore (TC-11a); and on the embedded SQLite store the driver
// flushes before each provider attempt, before the tool body and at step end,
// and the reopened log equals the same golden (TC-11b). Every scenario runs
// the real session, inbox, driver, guarded tool pipeline and approval registry
// on the test kit's scripted fake model and virtual clock; nothing is skipped.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

// The flow F2 composition of internal/agent/f2_test.go: every value below
// reaches the golden, so it must match the impl's rig exactly.
const (
	cpmF2Session        = "s-f2"
	cpmF2Route          = vocab.RouteID("r/f2")
	cpmF2Epoch          = int64(1_700_000_000_000)
	cpmF2Tool           = "probe"
	cpmF2Channel        = human.Channel("ui")
	cpmF2Call           = "call-1"
	cpmF2Args           = `{"text":"ping"}`
	cpmF2Prompt         = "probe once, then answer"
	cpmF2Answer         = "probed: ping"
	cpmF2PolicyName     = "f2.ask"
	cpmF2ApprovalReason = "probe needs a person"

	cpmCtxRoute   = vocab.RouteID("r/ctx")
	cpmCtxSession = "s-ctx"

	cpmWait = 20 * time.Second
)

var cpmF2Ref = agent.Ref{Session: cpmF2Session, Incarnation: 1}

// cpmOuterT is the test running the suite; scenario TBs delegate to it.
var cpmOuterT *testing.T

// cpmTB is a testing.TB scoped to one scenario: the test kit's cleanups run
// when it is disposed and its errors fail the scenario.
type cpmTB struct {
	testing.TB
	mu       sync.Mutex
	cleanups []func()
	errs     []string
}

func (t *cpmTB) Helper() {}

func (t *cpmTB) Cleanup(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanups = append(t.cleanups, f)
}

func (t *cpmTB) Errorf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errs = append(t.errs, fmt.Sprintf(format, args...))
}

func (t *cpmTB) Error(args ...any) { t.Errorf("%s", fmt.Sprint(args...)) }

func (t *cpmTB) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	panic(cpmFailure(fmt.Sprintf(format, args...)))
}

func (t *cpmTB) Fatal(args ...any) { t.Fatalf("%s", fmt.Sprint(args...)) }

func (t *cpmTB) Failed() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.errs) > 0
}

func (t *cpmTB) TempDir() string { return cpmOuterT.TempDir() }

func (t *cpmTB) failures() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.errs) > 0 {
		return errors.New(strings.Join(t.errs, "; "))
	}
	return nil
}

func (t *cpmTB) dispose() error {
	t.mu.Lock()
	cs := t.cleanups
	t.cleanups = nil
	t.mu.Unlock()
	for i := len(cs) - 1; i >= 0; i-- {
		cs[i]()
	}
	return t.failures()
}

type cpmFailure string

// cpmMust panics on err; cpmGuard turns the panic back into the step's error.
func cpmMust[T any](v T, err error) T {
	if err != nil {
		panic(cpmFailure(err.Error()))
	}
	return v
}

func cpmMust2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(cpmFailure(err.Error()))
	}
	return a, b
}

func cpmGuard(err *error) {
	if r := recover(); r != nil {
		if f, ok := r.(cpmFailure); ok {
			*err = errors.New(string(f))
			return
		}
		*err = fmt.Errorf("panic: %v", r)
	}
}

// cpmRecorder is the driver's model runtime with every request it streamed
// recorded and handed to a hook first, and each chunk passed through.
type cpmRecorder struct {
	driver.Models
	mu   sync.Mutex
	n    int
	sent []session.Request
	hook func(int, session.Request)
}

func (m *cpmRecorder) Prepare(ctx context.Context, rt vocab.RouteID, model string, c vocab.Controls) (llm.Prepared, error) {
	p, err := m.Models.Prepare(ctx, rt, model, c)
	if err != nil {
		return nil, err
	}
	return cpmPrepared{Prepared: p, m: m}, nil
}

func (m *cpmRecorder) requests() []session.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sent)
}

type cpmPrepared struct {
	llm.Prepared
	m *cpmRecorder
}

func (p cpmPrepared) Stream(ctx context.Context, req session.Request, sink llm.AttemptSink) iter.Seq[vocab.Chunk] {
	p.m.mu.Lock()
	p.m.n++
	n := p.m.n
	p.m.sent = append(p.m.sent, req)
	p.m.mu.Unlock()
	if p.m.hook != nil {
		p.m.hook(n, req)
	}
	return p.Prepared.Stream(ctx, req, sink)
}

type cpmAgent struct {
	agent.Agent
	ref agent.Ref
}

func (a cpmAgent) Ref() agent.Ref      { return a.ref }
func (a cpmAgent) ScopeKey() scope.Key { return scope.Key{} }

// cpmContributor is a context contributor adding one message naming the step.
type cpmContributor struct {
	name     string
	required bool
	mu       sync.Mutex
	calls    int
}

func (c *cpmContributor) Name() string   { return c.name }
func (c *cpmContributor) Required() bool { return c.required }
func (c *cpmContributor) Contribute(_ context.Context, in request.Input) (request.Output, error) {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	m, err := request.Message(c.name, "e2e."+c.name, c.name+" reading", map[string]int{"step": in.Step})
	return request.Output{Messages: []vocab.Message{m}}, err
}

// cpmF2In is the probe tool's input; its schema reaches the golden.
type cpmF2In struct {
	Text string `json:"text" dsh:"required"`
}

type cpmState struct {
	tb   *cpmTB
	root *cordis.Context
	tab  scope.Table

	// Context pipeline.
	st       store.Store
	sess     session.Handle
	inbox    *agent.Inbox
	drv      *driver.Driver
	models   *cpmRecorder
	pipe     *request.Pipeline
	contribs []*cpmContributor
	req      session.Request

	// Flow F2.
	f2Turns  []fakemodel.Turn
	approve  bool
	ans      *answerer.Answerer
	stop     func()
	barriers *cpmBarriers
	dbPath   string
	problems []string
	order    []string
	events   []event.Event
}

func (s *cpmState) newRoot() {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.tb.Cleanup(root.Dispose)
	cpmMust(0, cpmMust(root.Use(scope.Component, cordis.None{})).Wait())
	g := cpmMust(root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) (err error) {
		s.tab, err = scope.TableKey.Get(c)
		return err
	}))
	cpmMust(0, g.Wait())
	s.root = root
}

func (s *cpmState) startDriver(id string, initial agent.ModelSelection, rt *llm.Runtime, svc tools.Service, now bool, hook func(int, session.Request)) {
	s.models = &cpmRecorder{Models: rt, hook: hook}
	s.inbox = cpmMust(agent.OpenInbox(context.Background(), s.sess))
	mod := cpmMust(agent.OpenModel(context.Background(), s.sess, agent.ModelOptions{Initial: initial}))
	opts := driver.Options{
		Context: s.root, Agent: cpmAgent{ref: agent.Ref{Session: session.ID(id), Incarnation: 1}},
		Session: s.sess, Inbox: s.inbox, Model: mod, Models: s.models, Tools: svc,
	}
	if now {
		clk := clock.NewManual(time.UnixMilli(cpmF2Epoch))
		opts.Now, opts.After = clk.Now, clk.After
	}
	drv := cpmMust(driver.Start(opts))
	var once sync.Once
	s.stop = func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), cpmWait)
			defer cancel()
			if err := drv.Close(ctx); err != nil {
				s.tb.Errorf("close the driver: %v", err)
			}
		})
	}
	s.tb.Cleanup(s.stop)
	s.drv = drv
}

func (s *cpmState) runTurn(text string) {
	cpmMust(s.inbox.Send(context.Background(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: text}))
	ctx, cancel := context.WithTimeout(context.Background(), cpmWait)
	defer cancel()
	if err := s.drv.WaitIdle(ctx); err != nil {
		panic(cpmFailure("wait idle: " + err.Error()))
	}
	if err := s.tb.failures(); err != nil {
		panic(cpmFailure(err.Error()))
	}
}

// rebuild is surface.Rebuild over the stored log prefix of id ending at asOf.
func cpmRebuild(st store.Store, id string, asOf event.Seq) (surface.Built, error) {
	rd, err := st.OpenRead(context.Background(), id)
	if err != nil {
		return surface.Built{}, err
	}
	defer func() { _ = rd.Close(context.Background()) }()
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return rd.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	return surface.Rebuild(context.Background(), event.Default(), read, id, asOf)
}

func cpmSplit(list string) []string {
	var out []string
	for _, p := range strings.Split(list, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// --- Context pipeline is logged and ordered ---

func (s *cpmState) contributorsRegistered(list string) (err error) {
	defer cpmGuard(&err)
	names := cpmSplit(list)
	if len(names) != 3 {
		return fmt.Errorf("want three contributors, got %q", names)
	}
	s.newRoot()
	cpmMust(0, cpmMust(s.root.Use(core.Component, cordis.None{})).Wait())
	var svc tools.Service
	g := cpmMust(s.root.Inject([]string{tools.Key.Name()}, func(c *cordis.Context) (err error) {
		svc, err = tools.Key.Get(c)
		return err
	}))
	cpmMust(0, g.Wait())

	st := memstore.New()
	s.st = st
	s.sess = cpmMust(session.NewService(s.root, st, session.Config{}).Create(context.Background(), nil,
		session.CreateOptions{Header: session.Header{ID: cpmCtxSession}}))
	s.tb.Cleanup(func() { _ = s.sess.Close(context.Background()) })
	sess := s.sess
	s.pipe, _ = cpmMust2(request.New(s.root, request.Options{
		Table:    s.tab,
		Sessions: func(agent.Ref) (request.Log, error) { return sess, nil },
	}))
	// Register in reverse of the listed order so registration order never
	// matches the order table: the request must still follow the table.
	for i := len(names) - 1; i >= 0; i-- {
		c := &cpmContributor{name: names[i], required: i == 0}
		cpmMust(s.pipe.Register(s.root, c))
		s.contribs = append(s.contribs, c)
	}

	rt := llm.New()
	fakemodel.Register(s.tb, rt, cpmCtxRoute, fakemodel.NewTransport(s.tb, fakemodel.Reply("done")))
	s.startDriver(cpmCtxSession, agent.ModelSelection{Route: cpmCtxRoute}, rt, svc, false, nil)
	return nil
}

func (s *cpmState) requestBuilt() (err error) {
	defer cpmGuard(&err)
	s.runTurn("hi")
	sent := s.models.requests()
	if len(sent) != 1 {
		return fmt.Errorf("%d requests sent, want 1", len(sent))
	}
	s.req = sent[0]
	s.events = cpmMust(s.sess.Read(context.Background(), 0, 0))
	return nil
}

func (s *cpmState) contributionsInOrder(list string) (err error) {
	defer cpmGuard(&err)
	want := cpmSplit(list)
	for _, k := range want {
		if !slices.Contains(request.Order(), k) {
			return fmt.Errorf("%q is not a kind of the order table %v", k, request.Order())
		}
	}
	var tableOrder []string
	for _, k := range request.Order() {
		if slices.Contains(want, k) {
			tableOrder = append(tableOrder, k)
		}
	}
	if !slices.Equal(tableOrder, want) {
		return fmt.Errorf("%q is not the order table's order %q", want, tableOrder)
	}
	var registered []string
	for _, c := range s.contribs {
		registered = append(registered, c.name)
	}
	if slices.Equal(registered, want) {
		return fmt.Errorf("contributors were registered in the table order %q; the check cannot tell table order from registration order", registered)
	}
	msgs := cpmMust(s.req.Messages())
	var kinds []string
	for _, m := range msgs {
		src, _ := m.Source()
		kinds = append(kinds, src.Kind)
	}
	if len(kinds) < len(want)+1 || !slices.Equal(kinds[len(kinds)-len(want):], want) {
		return fmt.Errorf("request message kinds %q, want the contributions %q last", kinds, want)
	}
	if kinds[len(kinds)-len(want)-1] != "user" {
		return fmt.Errorf("request message kinds %q: the contributions do not follow the claimed user message", kinds)
	}
	for _, c := range s.contribs {
		c.mu.Lock()
		n := c.calls
		c.mu.Unlock()
		if n != 1 {
			return fmt.Errorf("contributor %s observed %d times, want once", c.name, n)
		}
	}
	// The log admits them between step/start and the request header.
	var types []string
	for _, e := range s.events {
		types = append(types, e.Type())
	}
	joined := strings.Join(types, ",")
	at := strings.Index(joined, "step/start")
	if at < 0 || !strings.HasPrefix(joined[at:], "step/start,model/selection,user/message,user/message,user/message,request/header") {
		return fmt.Errorf("the contributions are not admitted between step/start and request/header: %s", joined)
	}
	return nil
}

func (s *cpmState) contributionsLogged() (err error) {
	defer cpmGuard(&err)
	msgs := cpmMust(s.req.Messages())
	for _, m := range msgs[len(msgs)-len(s.contribs):] {
		var found *event.Event
		for i, e := range s.events {
			if e.Type() != event.UserMessage.Name() {
				continue
			}
			var p event.UserMessagePayload
			cpmMust(0, json.Unmarshal([]byte(e.Raw()), &p))
			if p.Message.ID() == m.ID() {
				found = &s.events[i]
			}
		}
		if found == nil {
			return fmt.Errorf("contribution %s is not a logged user/message", m.ID())
		}
		if found.Seq() > s.req.AsOf() {
			return fmt.Errorf("contribution %s logged at seq %d, after requestAsOf %d", m.ID(), found.Seq(), s.req.AsOf())
		}
	}
	return nil
}

func (s *cpmState) digestRebuilds() (err error) {
	defer cpmGuard(&err)
	b := cpmMust(cpmRebuild(s.st, cpmCtxSession, s.req.AsOf()))
	if b.Digest() != s.req.Digest() {
		return fmt.Errorf("rebuilt digest %s, sent %s", b.Digest(), s.req.Digest())
	}
	if b.Canonical() != s.req.Canonical() {
		return fmt.Errorf("the rebuilt request differs\nsent:    %s\nrebuilt: %s", s.req.Canonical(), b.Canonical())
	}
	for _, c := range s.contribs {
		c.mu.Lock()
		n := c.calls
		c.mu.Unlock()
		if n != 1 {
			return fmt.Errorf("contributor %s observed again during the rebuild (%d calls)", c.name, n)
		}
	}
	return nil
}

func (s *cpmState) turnCompleted() (err error) {
	defer cpmGuard(&err)
	for i := len(s.events) - 1; i >= 0; i-- {
		if s.events[i].Type() == event.TurnEndType.Name() {
			var te event.TurnEnd
			cpmMust(0, json.Unmarshal([]byte(s.events[i].Raw()), &te))
			if te.Reason.Kind != event.ReasonCompleted {
				return fmt.Errorf("the turn ended %+v", te.Reason)
			}
			return nil
		}
	}
	return errors.New("no turn/end in the log")
}

// --- Flow F2 ---

func (s *cpmState) scriptedModel() error {
	s.f2Turns = []fakemodel.Turn{fakemodel.Calls(fakemodel.Tool(cpmF2Call, cpmF2Tool, cpmF2Args)), fakemodel.Reply(cpmF2Answer)}
	return nil
}

func (s *cpmState) answererApproving() error {
	s.approve = true
	return nil
}

// f2Start composes flow F2 on st as internal/agent/f2_test.go does: the real
// session, inbox, model selection, guarded tool pipeline, approval registry
// and driver, on the virtual clock.
func (s *cpmState) f2Start(st store.Store, attempt func(int, session.Request), body func()) {
	if len(s.f2Turns) == 0 || !s.approve {
		panic(cpmFailure("flow F2 needs the scripted model and the approving answerer"))
	}
	s.newRoot()
	reg := human.New()
	s.tb.Cleanup(reg.Close)
	s.ans = answerer.Register(s.tb, s.root, reg, cpmF2Channel, answerer.AllowOnce())
	cpmMust(reg.Bind(s.root, cpmF2Ref, human.Binding{Channel: cpmF2Channel}))
	svc := core.NewWith(s.root, s.tab, core.Config{Approval: approval.New(approval.Config{Registry: reg})})
	cpmMust(svc.Register(s.root, tools.Define(tools.Spec[cpmF2In, string]{
		Name: cpmF2Tool, Description: "Echoes its text.",
		Run: func(_ context.Context, in cpmF2In, _ tools.Run) (string, error) {
			if body != nil {
				body()
			}
			return "probed: " + in.Text, nil
		},
	})))
	cpmMust(svc.Decide(s.root, cpmF2PolicyName, func(_ context.Context, c tools.Call) tools.Decision {
		if c.Name() == cpmF2Tool {
			return tools.Ask(cpmF2ApprovalReason, "")
		}
		return tools.Allow()
	}))
	s.st = st
	s.sess = cpmMust(session.NewService(s.root, st, session.Config{Clock: func() int64 { return cpmF2Epoch }}).Create(context.Background(), nil,
		session.CreateOptions{Header: session.Header{ID: cpmF2Session}}))
	sess := s.sess
	s.tb.Cleanup(func() { _ = sess.Close(context.Background()) })
	rt := llm.New()
	fakemodel.Register(s.tb, rt, cpmF2Route, fakemodel.NewTransport(s.tb, s.f2Turns...))
	s.startDriver(cpmF2Session, agent.ModelSelection{Route: cpmF2Route}, rt, svc, true, attempt)
}

func (s *cpmState) turnOnMemstore() (err error) {
	defer cpmGuard(&err)
	s.f2Start(memstore.New(), nil, nil)
	s.runTurn(cpmF2Prompt)
	s.events = cpmMust(s.sess.Read(context.Background(), 0, 0))
	return nil
}

// cpmBarriers is a store whose writers record every successful Flush: the
// highest seq accepted before the call, which the barrier made durable.
type cpmBarriers struct {
	store.Store
	mu       sync.Mutex
	accepted event.Seq
	cuts     []event.Seq
	types    map[event.Seq]string
}

func (b *cpmBarriers) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	w, err := b.Store.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return &cpmWriter{Writer: w, b: b}, nil
}

func (b *cpmBarriers) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	w, err := b.Store.OpenWrite(ctx, id, o)
	if err != nil {
		return nil, err
	}
	return &cpmWriter{Writer: w, b: b}, nil
}

func (b *cpmBarriers) flushed() event.Seq {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.cuts) == 0 {
		return 0
	}
	return b.cuts[len(b.cuts)-1]
}

func (b *cpmBarriers) last(typ string) event.Seq {
	b.mu.Lock()
	defer b.mu.Unlock()
	var s event.Seq
	for seq, t := range b.types {
		if t == typ && seq > s {
			s = seq
		}
	}
	return s
}

type cpmWriter struct {
	store.Writer
	b *cpmBarriers
}

func (w *cpmWriter) Append(ctx context.Context, bt store.Batch) error {
	if err := w.Writer.Append(ctx, bt); err != nil {
		return err
	}
	w.b.mu.Lock()
	for _, r := range bt.Records {
		w.b.types[r.Seq] = r.Type
		w.b.accepted = max(w.b.accepted, r.Seq)
	}
	w.b.mu.Unlock()
	return nil
}

func (w *cpmWriter) Flush(ctx context.Context) error {
	w.b.mu.Lock()
	at := w.b.accepted
	w.b.mu.Unlock()
	if err := w.Writer.Flush(ctx); err != nil {
		return err
	}
	w.b.mu.Lock()
	w.b.cuts = append(w.b.cuts, at)
	w.b.mu.Unlock()
	return nil
}

// storeDir is DSH_E2E_STORE_HOME when the compose run mounts one, else a
// test-kit temp dir.
func (s *cpmState) storeDir() string {
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		d := cpmMust(os.MkdirTemp(v, "e2e-cpm-"))
		s.tb.Cleanup(func() { _ = os.RemoveAll(d) })
		return d
	}
	return cpmOuterT.TempDir()
}

// durable reads the F2 session's rows from the database on a second connection.
func (s *cpmState) durable() ([]store.Record, error) {
	other, err := sqlite.Open(context.Background(), s.dbPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = other.Close(context.Background()) }()
	r, err := other.OpenRead(context.Background(), cpmF2Session)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close(context.Background()) }()
	return r.Read(context.Background(), 0, 0, store.ReadOptions{})
}

func (s *cpmState) turnOnSQLite() (err error) {
	defer cpmGuard(&err)
	s.dbPath = filepath.Join(s.storeDir(), "dsh.db")
	db := cpmMust(sqlite.Open(context.Background(), s.dbPath))
	s.tb.Cleanup(func() { _ = db.Close(context.Background()) })
	bar := &cpmBarriers{Store: db, types: map[event.Seq]string{}}
	s.barriers = bar

	var mu sync.Mutex
	fail := func(format string, args ...any) {
		mu.Lock()
		s.problems = append(s.problems, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	// covered checks one cut: a barrier covers through, and the database holds it.
	covered := func(cut string, through event.Seq) {
		if f := bar.flushed(); f < through {
			fail("%s: no flush covers seq %d (flushed through %d)", cut, through, f)
		}
		rows, err := s.durable()
		if err != nil {
			fail("%s: read the database: %v", cut, err)
		} else if len(rows) == 0 || rows[len(rows)-1].Seq < through {
			fail("%s: the database holds %d rows, want through seq %d", cut, len(rows), through)
		}
		mu.Lock()
		s.order = append(s.order, cut)
		mu.Unlock()
	}
	attempt := func(n int, req session.Request) {
		covered("attempt", req.AsOf())
		if n == 2 {
			covered("attempt", bar.last("step/end"))
		}
	}
	body := func() {
		for _, typ := range []string{"assistant/message", "tool/call", "approval/decided"} {
			sq := bar.last(typ)
			if sq == 0 {
				fail("before the tool body: no %s was accepted", typ)
			}
			covered("body", sq)
		}
	}
	s.f2Start(bar, attempt, body)
	s.runTurn(cpmF2Prompt)
	// At the end, before the driver closes (which flushes again), the step/end
	// and turn/end are the last durable rows and a barrier covered them.
	covered("end", bar.last("turn/end"))
	rows := cpmMust(s.durable())
	if n := len(rows); n < 2 || rows[n-1].Type != "turn/end" || rows[n-2].Type != "step/end" || event.Offset(n) != s.sess.Len() {
		fail("after the turn the database holds %d of %d rows, ending %v", n, s.sess.Len(), rows[max(0, n-2):])
	}
	s.events = cpmMust(s.sess.Read(context.Background(), 0, 0))
	return nil
}

func (s *cpmState) askedOneApproval() error {
	asked := s.ans.Asked()
	if len(asked) != 1 || asked[0].Kind != human.KindApproval {
		return fmt.Errorf("the answerer was asked %+v, want one approval", asked)
	}
	return nil
}

func (s *cpmState) flushesBeforeIO() error {
	if len(s.problems) > 0 {
		return errors.New(strings.Join(s.problems, "\n"))
	}
	if len(s.barriers.cuts) == 0 {
		return errors.New("no flush was recorded")
	}
	return nil
}

func (s *cpmState) checkpointOrder(want string) error {
	if got := strings.Join(slices.Compact(slices.Clone(s.order)), ","); got != want {
		return fmt.Errorf("checkpoints observed %s, want %s", got, want)
	}
	return nil
}

func (s *cpmState) f2Rebuilds() (err error) {
	defer cpmGuard(&err)
	if b := s.sess.Open(); b.IsOpen() {
		return fmt.Errorf("brackets still open: %+v", b)
	}
	types := map[string]bool{}
	for _, e := range s.events {
		types[e.Type()] = true
	}
	for _, typ := range []string{"turn/start", "step/start", "request/header", "assistant/message", "tool/call", "approval/asked", "approval/decided", "tool/result", "step/end", "turn/end"} {
		if !types[typ] {
			return fmt.Errorf("the F2 log has no %s", typ)
		}
	}
	var te event.TurnEnd
	cpmMust(0, json.Unmarshal([]byte(s.events[len(s.events)-1].Raw()), &te))
	if te.Reason.Kind != event.ReasonCompleted {
		return fmt.Errorf("the turn ended %+v", te.Reason)
	}
	sent := s.models.requests()
	if len(sent) != 2 {
		return fmt.Errorf("%d requests sent, want 2", len(sent))
	}
	for i, req := range sent {
		b := cpmMust(cpmRebuild(s.st, cpmF2Session, req.AsOf()))
		if b.Digest() != req.Digest() {
			return fmt.Errorf("request %d: rebuilt digest %s, sent %s", i+1, b.Digest(), req.Digest())
		}
	}
	return nil
}

// The per-run values a golden cannot hold: generated ids and the digests over
// them, numbered in order of first appearance; and an attempt's wall-clock
// timing (the f2Serialize of internal/agent/f2_test.go).
var (
	cpmVolatile = regexp.MustCompile(`\b[a-z]+[_-][0-9a-f]{16,}\b|sha256:[0-9a-f]{64}`)
	cpmTiming   = regexp.MustCompile(`"(start|first_token|end)":\d+`)
)

func cpmSerialize(evs []event.Event) string {
	names := map[string]string{}
	norm := func(s string) string {
		return cpmVolatile.ReplaceAllStringFunc(s, func(v string) string {
			if n, ok := names[v]; ok {
				return n
			}
			kind := strings.FieldsFunc(v, func(r rune) bool { return r == '_' || r == '-' || r == ':' })[0]
			n := fmt.Sprintf("<%s-%d>", kind, len(names)+1)
			names[v] = n
			return n
		})
	}
	var b strings.Builder
	for _, e := range evs {
		op := e.Op()
		fmt.Fprintf(&b, "%d %s op=%s", e.Seq(), e.Type(), op.Kind)
		if src := e.Sources(); len(src) > 0 {
			fmt.Fprintf(&b, " sources=%v", src)
		}
		fmt.Fprintf(&b, " t=%d %s\n", e.Time()-cpmF2Epoch, cpmTiming.ReplaceAllString(norm(e.Raw()), `"$1":"<time>"`))
	}
	return b.String()
}

// matchGolden compares the serialized log with the committed golden, a path
// relative to the module root (this package is test/e2e/harness-DSH).
func cpmMatchGolden(evs []event.Event, rel string) error {
	file := filepath.Join("..", "..", "..", filepath.FromSlash(rel))
	want, err := os.ReadFile(file)
	if err != nil {
		return fmt.Errorf("read the committed golden: %w", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	got := cpmSerialize(evs)
	if string(want) != got {
		return fmt.Errorf("the log differs from %s\n%s", rel, golden.Diff(string(want), got))
	}
	return nil
}

func (s *cpmState) logEqualsGolden(rel string) error {
	return cpmMatchGolden(s.events, rel)
}

func (s *cpmState) reopenedEqualsGolden(rel string) (err error) {
	defer cpmGuard(&err)
	s.stop()
	cpmMust(0, s.sess.Close(context.Background()))
	db := cpmMust(sqlite.Open(context.Background(), s.dbPath))
	s.tb.Cleanup(func() { _ = db.Close(context.Background()) })
	root := cordis.New()
	s.tb.Cleanup(root.Dispose)
	rs, release := cpmMust2(session.NewService(root, db, session.Config{}).OpenRead(context.Background(), cpmF2Session))
	defer release()
	return cpmMatchGolden(cpmMust(rs.Read(context.Background(), 0, 0)), rel)
}

func InitializeScenario_agent_loop_milestone_m1_context_pipeline_and_m1_closure(ctx *godog.ScenarioContext) {
	s := &cpmState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = cpmState{tb: &cpmTB{TB: cpmOuterT}}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if derr := s.tb.dispose(); derr != nil && err == nil {
			return c, derr
		}
		return c, nil
	})

	ctx.Step(`^three context contributors "([^"]*)" registered (?:in that order|in reverse order)$`, s.contributorsRegistered)
	ctx.Step(`^the loop request for a step is built with NextRequest$`, s.requestBuilt)
	ctx.Step(`^the request's contributions appear in the order "([^"]*)"$`, s.contributionsInOrder)
	ctx.Step(`^each contribution is backed by a logged user/message at or before the request's requestAsOf$`, s.contributionsLogged)
	ctx.Step(`^the request's digest equals the digest of surface\.Rebuild over the log prefix ending at requestAsOf$`, s.digestRebuilds)
	ctx.Step(`^the turn ended completed$`, s.turnCompleted)

	ctx.Step(`^the scripted model returning one tool call then a final reply$`, s.scriptedModel)
	ctx.Step(`^the fake answerer approving$`, s.answererApproving)
	ctx.Step(`^one turn runs on memstore$`, s.turnOnMemstore)
	ctx.Step(`^one turn runs on the SQLite store in a temp home$`, s.turnOnSQLite)
	ctx.Step(`^the answerer was asked exactly one approval$`, s.askedOneApproval)
	ctx.Step(`^the F2 brackets are closed and every request sent rebuilds from the log$`, s.f2Rebuilds)
	ctx.Step(`^the serialized log equals "([^"]*)"$`, s.logEqualsGolden)
	ctx.Step(`^database rows show a flush before the provider attempt, before the tool body and at step end$`, s.flushesBeforeIO)
	ctx.Step(`^the checkpoints were observed in the order "([^"]*)"$`, s.checkpointOrder)
	ctx.Step(`^the log reopened from the database equals "([^"]*)"$`, s.reopenedEqualsGolden)
}

func TestE2E_agent_loop_milestone_m1_context_pipeline_and_m1_closure(t *testing.T) {
	cpmOuterT = t
	t.Cleanup(func() { cpmOuterT = nil })
	suite := godog.TestSuite{
		Name:                "agent_loop_milestone_m1_context_pipeline_and_m1_closure",
		ScenarioInitializer: InitializeScenario_agent_loop_milestone_m1_context_pipeline_and_m1_closure,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"agent_loop_milestone_m1_context_pipeline_and_m1_closure.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
