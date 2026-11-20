package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
)

var bg = context.Background()

func newRoot(t *testing.T) *cordis.Context {
	t.Helper()
	r := cordis.New()
	r.Loggers().ClearExporters()
	r.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(r.Dispose)
	return r
}

type env struct {
	root *cordis.Context
	tab  scope.Table
	ms   *memstore.Store
	st   store.Store
	svc  session.Service
}

func newEnv(t *testing.T, cfg session.Config, wrap ...func(store.Store) store.Store) *env {
	t.Helper()
	e := &env{root: newRoot(t), ms: memstore.New()}
	e.tab = scope.NewTable(e.root)
	e.st = e.ms
	for _, w := range wrap {
		e.st = w(e.st)
	}
	e.svc = session.NewService(e.root, e.st, cfg)
	return e
}

func (e *env) create(t *testing.T, key scope.Key) session.Handle {
	t.Helper()
	h, err := e.svc.Create(bg, nil, session.CreateOptions{Scope: key})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(bg) })
	return h
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func msg(t testing.TB, id string, role vocab.Role, blocks ...vocab.Block) vocab.Message {
	return must(vocab.RestoreMessage(id, role, nil, blocks...))
}

func ctxDraft(t testing.TB, v string) event.Draft {
	return must(event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: v}))
}

func userDraft(t testing.TB, id, s string) event.Draft {
	return must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: msg(t, id, vocab.RoleUser, vocab.TextBlock(s))}))
}

func assistantDraft(t testing.TB, p event.AssistantMessagePayload) event.Draft {
	return must(event.AssistantMessage.Draft(event.Append(), p))
}

func callBlock(id, name string) vocab.Block {
	return vocab.ToolUseBlock(id, name, json.RawMessage(`{"q":1}`))
}

func resultDraft(t testing.TB, id, callID, s string) event.Draft {
	return must(event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: callID,
		Message: msg(t, id, vocab.RoleTool, vocab.ToolResultBlock(callID, false, vocab.TextPart(s)))}))
}

func headerDraft(t testing.TB, route string, tools ...vocab.ToolSchema) event.Draft {
	return must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: vocab.RouteID(route), Tools: tools}))
}

func turnStart(t testing.TB, n int) event.Draft {
	return must(event.TurnStartType.Draft(event.TurnStart{Turn: n}))
}

func turnEnd(t testing.TB, n int) event.Draft {
	return must(event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: event.Reason{Kind: event.ReasonCompleted}}))
}

func stepStart(t testing.TB, turn, step int) event.Draft {
	return must(event.StepStartType.Draft(event.StepStart{Turn: turn, Step: step}))
}

func stepEnd(t testing.TB, turn, step int) event.Draft {
	return must(event.StepEndType.Draft(event.StepEnd{Turn: turn, Step: step}))
}

// recorder collects Committed and Closed for one listener.
type recorder struct {
	mu     sync.Mutex
	seqs   map[session.ID][]event.Seq
	closed map[session.ID]session.ClosedArgs
	// atClose is how many events each session had delivered when Closed came.
	atClose map[session.ID]int
	done    chan session.ID
}

func listen(t *testing.T, ctx *cordis.Context, opts ...cordis.ListenOption) *recorder {
	t.Helper()
	r := &recorder{seqs: map[session.ID][]event.Seq{}, closed: map[session.ID]session.ClosedArgs{},
		atClose: map[session.ID]int{}, done: make(chan session.ID, 64)}
	must(session.Committed.On(ctx, func(_ *cordis.Context, a session.CommittedArgs) error {
		r.mu.Lock()
		r.seqs[a.Session.ID()] = append(r.seqs[a.Session.ID()], a.Event.Seq())
		r.mu.Unlock()
		return nil
	}, opts...))
	must(session.Closed.On(ctx, func(_ *cordis.Context, a session.ClosedArgs) error {
		r.mu.Lock()
		r.closed[a.ID] = a
		r.atClose[a.ID] = len(r.seqs[a.ID])
		r.mu.Unlock()
		r.done <- a.ID
		return nil
	}, opts...))
	return r
}

func (r *recorder) waitClosed(t *testing.T, id session.ID) session.ClosedArgs {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		r.mu.Lock()
		a, ok := r.closed[id]
		r.mu.Unlock()
		if ok {
			return a
		}
		select {
		case <-r.done:
		case <-deadline:
			t.Fatalf("session %q never closed", id)
		}
	}
}

func (r *recorder) got(id session.ID) []event.Seq {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]event.Seq(nil), r.seqs[id]...)
}

func dense(seqs []event.Seq, n int) bool {
	if len(seqs) != n {
		return false
	}
	for i, s := range seqs {
		if s != event.Seq(i) {
			return false
		}
	}
	return true
}

// DH-T-SESSION-3 (TC-05): concurrent appenders get dense seqs and
// non-decreasing times; three listeners see each event once, in order.
func TestOrderedOnceDelivery(t *testing.T) {
	e := newEnv(t, session.Config{})
	rs := []*recorder{listen(t, e.root), listen(t, e.root), listen(t, e.root)}
	h := e.create(t, scope.Key{})
	const goroutines, per = 8, 125
	var wg sync.WaitGroup
	receipts := make(chan event.Receipt, goroutines*per)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < per; i++ {
				r, err := h.Append(bg, ctxDraft(t, "v"))
				if err != nil {
					t.Error(err)
					return
				}
				receipts <- r
			}
		}()
	}
	wg.Wait()
	close(receipts)
	times := make([]int64, goroutines*per)
	seen := make([]bool, goroutines*per)
	for r := range receipts {
		if seen[r.Seq] {
			t.Fatalf("seq %d assigned twice", r.Seq)
		}
		seen[r.Seq], times[r.Seq] = true, r.Time
	}
	for i := 1; i < len(times); i++ {
		if times[i] < times[i-1] {
			t.Fatalf("time goes back at seq %d", i)
		}
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	for i, r := range rs {
		r.waitClosed(t, h.ID())
		if got := r.got(h.ID()); !dense(got, goroutines*per) {
			t.Fatalf("listener %d saw %d events, not 0..%d once in order", i, len(got), goroutines*per-1)
		}
	}
}

// faultStore wraps a store's writers.
type faultStore struct {
	store.Store
	mu      sync.Mutex
	writers []*faultWriter
	mode    string
	// foreignAt is the seq "foreign-late" corrupts from its second read on;
	// reads counts the reads that returned it.
	foreignAt store.Seq
	reads     int
	// cancel is what "cancel-after-append" and "cancel-in-append" call.
	cancel context.CancelFunc
}

func (f *faultStore) wrap(w store.Writer, err error) (store.Writer, error) {
	if err != nil {
		return nil, err
	}
	fw := &faultWriter{Writer: w, f: f}
	f.mu.Lock()
	f.writers = append(f.writers, fw)
	f.mu.Unlock()
	return fw, nil
}

func (f *faultStore) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	return f.wrap(f.Store.Create(ctx, h, o))
}

func (f *faultStore) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	return f.wrap(f.Store.OpenWrite(ctx, id, o))
}

func (f *faultStore) setMode(m string) {
	f.mu.Lock()
	f.mode = m
	f.mu.Unlock()
}

func (f *faultStore) getMode() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.mode
}

type faultWriter struct {
	store.Writer
	f       *faultStore
	mu      sync.Mutex
	appends int
}

func (w *faultWriter) Append(ctx context.Context, b store.Batch) error {
	switch w.f.getMode() {
	case "append-fails":
		return errors.New("injected: disk full")
	case "cancel-in-append":
		w.f.cancel()
		return ctx.Err()
	}
	w.mu.Lock()
	w.appends++
	w.mu.Unlock()
	err := w.Writer.Append(ctx, b)
	if err == nil && w.f.getMode() == "cancel-after-append" {
		w.f.cancel()
	}
	return err
}

func (w *faultWriter) Close(ctx context.Context) error {
	err := w.Writer.Close(ctx)
	if w.f.getMode() == "close-fails" {
		return errors.New("injected: flush on close failed")
	}
	return err
}

func (w *faultWriter) Read(ctx context.Context, from store.Seq, limit int, o store.ReadOptions) ([]store.Record, error) {
	recs, err := w.Writer.Read(ctx, from, limit, o)
	if err != nil || len(recs) == 0 {
		return recs, err
	}
	switch w.f.getMode() {
	case "stale":
		return nil, nil
	case "gap":
		if len(recs) > 1 {
			return append(recs[:1:1], recs[2:]...), nil
		}
		return w.Writer.Read(ctx, from+1, limit, o)
	case "first":
		return w.Writer.Read(ctx, from+1, limit, o)
	case "foreign":
		recs[0].Payload = `{"key":"k","value":"from another session"}`
	case "foreign-late":
		for i, r := range recs {
			if r.Seq != w.f.foreignAt {
				continue
			}
			w.f.mu.Lock()
			w.f.reads++
			late := w.f.reads > 1
			w.f.mu.Unlock()
			if late {
				recs = append([]store.Record(nil), recs...)
				recs[i].Payload = `{"key":"k","value":"from another session"}`
			}
		}
	}
	return recs, nil
}

// DH-T-SESSION-4: a group with one invalid member changes nothing; a valid group
// reaches storage as one batch; with a fault injected in the store none of it is
// readable and the folds are unchanged.
func TestGroupAtomic(t *testing.T) {
	var fs *faultStore
	e := newEnv(t, session.Config{CheckpointEvery: -1}, func(s store.Store) store.Store { fs = &faultStore{Store: s}; return fs })
	h := e.create(t, scope.Key{})

	_, err := h.AppendGroup(bg, []event.Draft{turnStart(t, 1), userDraft(t, "u", "x"), stepEnd(t, 1, 1)})
	if !errors.Is(err, session.ErrUnbalanced) || h.Len() != 0 || h.History().Len() != 0 {
		t.Fatalf("invalid group: err %v, len %d", err, h.Len())
	}
	if evs := must(h.Read(bg, 0, 0)); len(evs) != 0 {
		t.Fatalf("invalid group left %d events", len(evs))
	}

	if _, err := h.AppendGroup(bg, []event.Draft{turnStart(t, 1), userDraft(t, "u", "x"), stepStart(t, 1, 1)}); err != nil {
		t.Fatal(err)
	}
	if n := fs.writers[0].appends; n != 1 {
		t.Fatalf("a group of 3 took %d storage appends, want 1", n)
	}

	fs.setMode("append-fails")
	if _, err := h.AppendGroup(bg, []event.Draft{stepEnd(t, 1, 1), turnEnd(t, 1)}); err == nil {
		t.Fatal("an append the store refused succeeded")
	}
	fs.setMode("")
	if h.Len() != 3 || !h.Open().StepOpen {
		t.Fatalf("a refused batch changed the folds: len %d, open %+v", h.Len(), h.Open())
	}
	if evs := must(h.Read(bg, 0, 0)); len(evs) != 3 {
		t.Fatalf("%d events readable after a refused batch, want 3", len(evs))
	}
	if _, err := h.AppendGroup(bg, []event.Draft{stepEnd(t, 1, 1), turnEnd(t, 1)}); err != nil {
		t.Fatalf("the retry: %v", err)
	}
}

// DH-T-SESSION-6: a failing, panicking or blocked listener does not fail or
// delay Append; a listener may append; listeners see only the sessions their
// scope reaches.
func TestListeners(t *testing.T) {
	e := newEnv(t, session.Config{})
	release := make(chan struct{})
	var once sync.Once
	must(session.Committed.On(e.root, func(*cordis.Context, session.CommittedArgs) error { return errors.New("listener fails") }))
	must(session.Committed.On(e.root, func(*cordis.Context, session.CommittedArgs) error { panic("listener panics") }))
	must(session.Committed.On(e.root, func(_ *cordis.Context, a session.CommittedArgs) error {
		if a.Event.Type() == event.RequestContext.Name() {
			<-release // blocks the deliverer, never Append
		}
		return nil
	}))
	rec := listen(t, e.root)
	appended := make(chan event.Receipt, 1)
	must(session.Committed.On(e.root, func(_ *cordis.Context, a session.CommittedArgs) error {
		if a.Event.Type() == event.UserMessage.Name() {
			once.Do(func() {
				s, _ := e.svc.Get(a.Session.ID())
				r, err := s.Append(bg, ctxDraft(t, "from a listener"))
				if err != nil {
					t.Error(err)
				}
				appended <- r
			})
		}
		return nil
	}))

	h := e.create(t, scope.Key{})
	start := time.Now()
	for i := 0; i < 50; i++ {
		if _, err := h.Append(bg, ctxDraft(t, "v")); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("appends behind a blocked listener took %v", d)
	}
	close(release)
	if _, err := h.Append(bg, userDraft(t, "u", "hi")); err != nil {
		t.Fatal(err)
	}
	r := <-appended
	if r.Seq != 51 {
		t.Fatalf("the listener's append got seq %d, want 51", r.Seq)
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	rec.waitClosed(t, h.ID())
	if got := rec.got(h.ID()); !dense(got, 52) {
		t.Fatalf("delivered %d events, want 52 in order", len(got))
	}
}

// DH-T-SESSION-6 (scope clauses): a listener in agent A's scope does not see
// agent B's session; an enclosing scope's and a global one do.
func TestListenerScopes(t *testing.T) {
	e := newEnv(t, session.Config{})
	preset := must(e.tab.New(e.root, "preset"))
	a := must(e.tab.New(e.root, "a", preset.Key()))
	b := must(e.tab.New(e.root, "b"))
	inA, inPreset, inB, global := listen(t, a.Ctx()), listen(t, preset.Ctx()), listen(t, b.Ctx()), listen(t, b.Ctx(), cordis.Global())

	ha, hb := e.create(t, a.Key()), e.create(t, b.Key())
	for _, h := range []session.Handle{ha, hb} {
		must(h.Append(bg, ctxDraft(t, "v")))
		if err := h.Close(bg); err != nil {
			t.Fatal(err)
		}
	}
	global.waitClosed(t, ha.ID())
	global.waitClosed(t, hb.ID())
	check := func(name string, r *recorder, id session.ID, want int) {
		if got := len(r.got(id)); got != want {
			t.Errorf("%s saw %d events of %s, want %d", name, got, id, want)
		}
	}
	check("a", inA, ha.ID(), 1)
	check("a", inA, hb.ID(), 0)
	check("preset", inPreset, ha.ID(), 1)
	check("preset", inPreset, hb.ID(), 0)
	check("b", inB, ha.ID(), 0)
	check("b", inB, hb.ID(), 1)
	check("global", global, ha.ID(), 1)
	check("global", global, hb.ID(), 1)
}
