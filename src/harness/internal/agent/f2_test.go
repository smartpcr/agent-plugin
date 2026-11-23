package agent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/retry"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	spillpolicy "github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

// Flow F2 (architecture.md §6.2): one M1 turn with one test tool call and one
// approval, on the scripted model and the scripted answerer.

const (
	f2Session        = "s-f2"
	f2Route          = vocab.RouteID("r/f2")
	f2Epoch          = int64(1_700_000_000_000)
	f2Golden         = "f2_log"
	f2Tool           = "probe"
	f2Channel        = human.Channel("ui")
	f2Call           = "call-1"
	f2Args           = `{"text":"ping"}`
	f2Prompt         = "probe once, then answer"
	f2Answer         = "probed: ping"
	f2PolicyName     = "f2.ask"
	f2ApprovalReason = "probe needs a person"
)

var f2Ref = agent.Ref{Session: f2Session, Incarnation: 1}

type f2In struct {
	Text string `json:"text" dsh:"required"`
}

// f2Hooks observe the run from outside: before each model attempt is sent, on
// each chunk it streams, and at the start of the tool body. wallClock runs the
// driver on the real clock (the M1 check's retries wait real time).
type f2Hooks struct {
	attempt   func(n int, req session.Request)
	chunk     func(n int, c vocab.Chunk)
	body      func()
	wallClock bool
	// noApproval scripts the answerer with no reply, for a variant whose turn
	// calls no tool and so asks no approval.
	noApproval bool
}

// f2Models is the driver's model runtime with each request handed to a hook
// before it is streamed.
type f2Models struct {
	driver.Models
	mu    sync.Mutex
	n     int
	sent  []session.Request
	hook  func(int, session.Request)
	chunk func(int, vocab.Chunk)
}

func (m *f2Models) Prepare(ctx context.Context, rt vocab.RouteID, model string, c vocab.Controls) (llm.Prepared, error) {
	p, err := m.Models.Prepare(ctx, rt, model, c)
	if err != nil {
		return nil, err
	}
	return f2Prepared{Prepared: p, m: m}, nil
}

type f2Prepared struct {
	llm.Prepared
	m *f2Models
}

func (p f2Prepared) Stream(ctx context.Context, req session.Request, sink llm.AttemptSink) iter.Seq[vocab.Chunk] {
	p.m.mu.Lock()
	p.m.n++
	n := p.m.n
	p.m.sent = append(p.m.sent, req)
	p.m.mu.Unlock()
	if p.m.hook != nil {
		p.m.hook(n, req)
	}
	chunks := p.Prepared.Stream(ctx, req, sink)
	if p.m.chunk == nil {
		return chunks
	}
	return func(yield func(vocab.Chunk) bool) {
		for c := range chunks {
			p.m.chunk(n, c)
			if !yield(c) {
				return
			}
		}
	}
}

type f2Agent struct {
	agent.Agent
}

func (f2Agent) Ref() agent.Ref      { return f2Ref }
func (f2Agent) ScopeKey() scope.Key { return scope.Key{} }

// f2Rig is flow F2's composition: the real session, inbox, model selection,
// guarded tool pipeline with its spill policy, approval registry and grant
// table, the llm.retry component and the driver over one model runtime.
type f2Rig struct {
	sess   session.Handle
	inbox  *agent.Inbox
	models *f2Models
	drv    *driver.Driver
	ans    *answerer.Answerer
	stop   func()
	// clk is the driver's virtual clock (nil on the wall clock); waits are the
	// durations the driver waited on it.
	clk   *clock.Manual
	waits func() []time.Duration
}

// f2Run runs flow F2 on st and returns the live session's log, still open, and
// the requests the driver sent. The caller closes nothing: cleanups do.
func f2Run(t *testing.T, st store.Store, h f2Hooks) (session.Handle, []session.Request, func()) {
	t.Helper()
	r := f2Start(t, st, h, func(rt *llm.Runtime) agent.ModelSelection {
		fakemodel.Register(t, rt, f2Route, fakemodel.NewTransport(t,
			fakemodel.Calls(fakemodel.Tool(f2Call, f2Tool, f2Args)), fakemodel.Reply(f2Answer)))
		return agent.ModelSelection{Route: f2Route}
	})
	must(r.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: f2Prompt}))
	ctx, cancel := context.WithTimeout(bg, f2IdleBound)
	defer cancel()
	if err := r.drv.WaitIdle(ctx); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	if asked := r.ans.Asked(); len(asked) != 1 || asked[0].Kind != human.KindApproval {
		t.Fatalf("the answerer was asked %+v, want one approval", asked)
	}
	return r.sess, r.models.requests(), r.stop
}

func (m *f2Models) requests() []session.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.sent)
}

// f2Start composes flow F2 on st with the routes register adds to the model
// runtime, starting from the selection it returns, and starts the driver.
func f2Start(t *testing.T, st store.Store, h f2Hooks, register func(*llm.Runtime) agent.ModelSelection) *f2Rig {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	t.Cleanup(root.Dispose)
	must(0, must(root.Use(scope.Component, cordis.None{})).Wait())
	var tab scope.Table
	g := must(root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) (err error) {
		tab, err = scope.TableKey.Get(c)
		return err
	}))
	must(0, g.Wait())

	// The answerer approves the one request through the real registry; the
	// session grant table is bound to the agent lifecycle, as the product binds it.
	sessions := session.NewService(root, st, session.Config{Clock: func() int64 { return f2Epoch }})
	reg := human.New()
	t.Cleanup(reg.Close)
	replies := []answerer.Reply{answerer.AllowOnce()}
	if h.noApproval {
		replies = nil
	}
	ans := answerer.Register(t, root, reg, f2Channel, replies...)
	must(reg.Bind(root, f2Ref, human.Binding{Channel: f2Channel}))
	grants := approval.NewGrants()
	for _, h := range must(grants.Bind(root, sessions)) {
		t.Cleanup(h.Dispose)
	}
	svc := core.NewWith(root, tab, core.Config{Approval: approval.New(approval.Config{Registry: reg, Grants: grants})})
	// The spill policy keeps every tool result within the budget, over a spill
	// store in a private temp home.
	spills := must(local.New(temphome.New(t), local.DefaultConfig))
	_, spillSeat := must2(spillpolicy.Mount(root, svc, spills, spillpolicy.Config{Now: func() time.Time { return time.UnixMilli(f2Epoch) }}))
	t.Cleanup(spillSeat.Dispose)
	must(svc.Register(root, tools.Define(tools.Spec[f2In, string]{
		Name: f2Tool, Description: "Echoes its text.",
		Run: func(_ context.Context, in f2In, _ tools.Run) (string, error) {
			if h.body != nil {
				h.body()
			}
			return "probed: " + in.Text, nil
		},
	})))
	must(svc.Decide(root, f2PolicyName, func(_ context.Context, c tools.Call) tools.Decision {
		if c.Name() == f2Tool {
			return tools.Ask(f2ApprovalReason, "")
		}
		return tools.Allow()
	}))

	sess := must(sessions.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: f2Session}}))
	t.Cleanup(func() { _ = sess.Close(bg) })
	rt := llm.New()
	initial := register(rt)
	models := &f2Models{Models: rt, hook: h.attempt, chunk: h.chunk}
	inbox := must(agent.OpenInbox(bg, sess))
	mod := must(agent.OpenModel(bg, sess, agent.ModelOptions{Initial: initial}))
	opts := driver.Options{
		Context: root, Agent: f2Agent{}, Session: sess, Inbox: inbox, Model: mod, Models: models, Tools: svc,
	}
	var (
		clk    *clock.Manual
		waitMu sync.Mutex
		waited []time.Duration
	)
	if !h.wallClock {
		clk = clock.NewManual(time.UnixMilli(f2Epoch))
		opts.Now = clk.Now
		opts.After = func(d time.Duration) <-chan time.Time {
			waitMu.Lock()
			waited = append(waited, d)
			waitMu.Unlock()
			return clk.After(d)
		}
	}
	// The llm.retry component decides under the failed call's route policy and
	// logs into the agent's session; the driver waits on its own clock.
	rc := retry.Config{Record: func(ctx context.Context, _ agent.AttemptFailure, d event.Draft) error {
		_, err := sess.Append(ctx, d)
		return err
	}}
	if clk != nil {
		rc.Now = clk.Now
	}
	t.Cleanup(must(retry.Listen(root, rc)).Close)
	drv := must(driver.Start(opts))
	var once sync.Once
	stop := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(bg, f2IdleBound)
			defer cancel()
			if err := drv.Close(ctx); err != nil {
				t.Errorf("close the driver: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	waits := func() []time.Duration {
		waitMu.Lock()
		defer waitMu.Unlock()
		return slices.Clone(waited)
	}
	return &f2Rig{sess: sess, inbox: inbox, models: models, drv: drv, ans: ans, stop: stop, clk: clk, waits: waits}
}

// The per-run values a golden cannot hold: generated ids and the digests over
// them, numbered in order of first appearance so two runs of one flow serialize
// the same.
var f2Volatile = regexp.MustCompile(`\b[a-z]+[_-][0-9a-f]{16,}\b|sha256:[0-9a-f]{64}`)

// f2Timing is an attempt's wall-clock timing, which the transport measures.
var f2Timing = regexp.MustCompile(`"(start|first_token|end)":\d+`)

// f2Serialize is the bracketed log as golden text: one line per event with its
// seq, type, operation, sources, time and payload, ids and digests numbered.
func f2Serialize(evs []event.Event) string {
	names := map[string]string{}
	norm := func(s string) string {
		return f2Volatile.ReplaceAllStringFunc(s, func(v string) string {
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
		fmt.Fprintf(&b, " t=%d %s\n", e.Time()-f2Epoch, f2Timing.ReplaceAllString(norm(e.Raw()), `"$1":"<time>"`))
	}
	return b.String()
}

// f2Check is what both runs of F2 assert of the log beyond the golden: the
// brackets balance, the turn completed, and every request sent equals the one
// rebuilt from the stored log at its requestAsOf (architecture.md §6.2).
func f2Check(t *testing.T, st store.Store, sess session.ReadSession, sent []session.Request) {
	t.Helper()
	if b := sess.Open(); b.IsOpen() {
		t.Fatalf("brackets still open: %+v", b)
	}
	if len(sent) != 2 {
		t.Fatalf("%d requests sent, want 2", len(sent))
	}
	rd := must(st.OpenRead(bg, f2Session))
	defer func() { _ = rd.Close(bg) }()
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return rd.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	for i, req := range sent {
		b := must(surface.Rebuild(bg, event.Default(), read, f2Session, req.AsOf()))
		if b.Digest() != req.Digest() {
			t.Fatalf("request %d: rebuilt digest %s, sent %s", i+1, b.Digest(), req.Digest())
		}
	}
}

func f2Types(evs []event.Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Type())
	}
	return out
}

func f2OfType(evs []event.Event, typ string) []event.Event {
	var out []event.Event
	for _, e := range evs {
		if e.Type() == typ {
			out = append(out, e)
		}
	}
	return out
}

func f2Decode[T any](e event.Event) (T, error) {
	var v T
	if err := json.Unmarshal([]byte(e.Raw()), &v); err != nil {
		return v, fmt.Errorf("decode %s: %w", e.Type(), err)
	}
	return v, nil
}

// f2Text is a message's text: its text blocks and the text parts of its tool
// results, where a tool-role message keeps what the tool returned.
func f2Text(m vocab.Message) string {
	var b strings.Builder
	for _, bl := range m.Blocks() {
		if bl.Text != nil {
			b.WriteString(bl.Text.Text)
		}
		if bl.ToolResult != nil {
			for _, p := range bl.ToolResult.Parts {
				if p.Text != nil {
					b.WriteString(p.Text.Text)
				}
			}
		}
	}
	return b.String()
}

func f2HasToolUse(m vocab.Message) bool {
	for _, bl := range m.Blocks() {
		if bl.ToolUse != nil {
			return true
		}
	}
	return false
}

func f2ToolResultFailed(p event.ToolResultPayload) bool {
	if p.IsError {
		return true
	}
	for _, bl := range p.Message.Blocks() {
		if bl.ToolResult != nil && bl.ToolResult.IsError {
			return true
		}
	}
	return false
}

// f2Completed is the M1 one-turn success oracle, shared with the keyless F2
// run so it is checked against a real successful payload: exactly one turn,
// completed; no failed attempt; one tool/result, a success whose text carries
// answer; and the last settlement an assistant/message with text and no tool
// call. It returns the final reply's text.
func f2Completed(evs []event.Event, answer string) (string, error) {
	ends := f2OfType(evs, "turn/end")
	if len(ends) != 1 {
		return "", fmt.Errorf("%d turn/end events, want exactly one", len(ends))
	}
	te, err := f2Decode[event.TurnEnd](ends[0])
	if err != nil {
		return "", err
	}
	if te.Reason.Kind != event.ReasonCompleted {
		return "", fmt.Errorf("turn/end %+v, want completed", te.Reason)
	}
	if n := len(f2OfType(evs, "assistant/attempt")); n != 0 {
		return "", fmt.Errorf("%d failed attempts in the turn", n)
	}
	results := f2OfType(evs, "tool/result")
	if len(results) != 1 {
		return "", fmt.Errorf("%d tool/result events, want 1", len(results))
	}
	res, err := f2Decode[event.ToolResultPayload](results[0])
	if err != nil {
		return "", err
	}
	if f2ToolResultFailed(res) || !strings.Contains(f2Text(res.Message), answer) {
		return "", fmt.Errorf("tool result %s, want a success carrying %q", results[0].Raw(), answer)
	}
	var last event.Event
	found := false
	for _, e := range evs {
		switch e.Type() {
		case "assistant/message", "tool/result":
			last, found = e, true
		}
	}
	if !found || last.Type() != "assistant/message" {
		return "", errors.New("the turn's last settlement is not an assistant reply after the tool result")
	}
	reply, err := f2Decode[event.AssistantMessagePayload](last)
	if err != nil {
		return "", err
	}
	if f2Text(reply.Message) == "" || f2HasToolUse(reply.Message) {
		return "", fmt.Errorf("final reply %s, want text and no tool call", last.Raw())
	}
	return f2Text(reply.Message), nil
}

// The M1 one-turn oracle accepts the scripted F2 turn (its tool result holds
// the probe's text under tool_result.parts, not a text block) and refuses the
// same log cut before the reply, or with the tool result turned into an error.
func TestM1CompletionOracleOnTheF2Turn(t *testing.T) {
	sess, _, _ := f2Run(t, memstore.New(), f2Hooks{})
	evs := must(sess.Read(bg, 0, 0))
	if _, err := f2Completed(evs, f2Answer); err != nil {
		t.Fatalf("the oracle refused the successful F2 turn: %v", err)
	}
	cut := slices.IndexFunc(evs, func(e event.Event) bool { return e.Type() == "tool/result" })
	if _, err := f2Completed(evs[:cut+1], f2Answer); err == nil {
		t.Fatal("the oracle accepted a turn cut after the tool result")
	}
	res := must(f2Decode[event.ToolResultPayload](evs[cut]))
	res.IsError = true
	if !f2ToolResultFailed(res) {
		t.Fatal("an error tool result reads as a success")
	}
	if _, err := f2Completed(evs, "not the probe's output"); err == nil {
		t.Fatal("the oracle accepted a tool result without the expected text")
	}
}

// TC-11a, the Stage 11.4 scenario "Flow F2 bracketed log matches golden": one
// turn on memstore serializes to testdata/f2_log.golden.
func TestFlowF2LogMatchesGolden(t *testing.T) {
	st := memstore.New()
	sess, sent, _ := f2Run(t, st, f2Hooks{})
	evs := must(sess.Read(bg, 0, 0))
	for _, typ := range []string{"turn/start", "step/start", "request/header", "assistant/message", "tool/call", "approval/asked", "approval/decided", "tool/result", "step/end", "turn/end"} {
		if !slices.Contains(f2Types(evs), typ) {
			t.Fatalf("the F2 log has no %s: %v", typ, f2Types(evs))
		}
	}
	var te event.TurnEnd
	must(0, json.Unmarshal([]byte(evs[len(evs)-1].Raw()), &te))
	if te.Reason.Kind != event.ReasonCompleted {
		t.Fatalf("the turn ended %+v", te.Reason)
	}
	f2Check(t, st, sess, sent)
	golden.Assert(t, f2Golden, f2Serialize(evs))
}

// f2Barriers is a store whose writers record every successful Flush: the
// highest seq accepted before the call, which the barrier made durable. hide
// drops the record (the flush still runs), standing in for a driver that never
// flushed, so the oracle's own failure can be shown.
type f2Barriers struct {
	store.Store
	mu       sync.Mutex
	accepted event.Seq
	cuts     []event.Seq
	types    map[event.Seq]string
	hide     bool
}

func newF2Barriers(st store.Store) *f2Barriers {
	return &f2Barriers{Store: st, types: map[event.Seq]string{}}
}

func (b *f2Barriers) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	w, err := b.Store.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return &f2Writer{Writer: w, b: b}, nil
}

func (b *f2Barriers) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	w, err := b.Store.OpenWrite(ctx, id, o)
	if err != nil {
		return nil, err
	}
	return &f2Writer{Writer: w, b: b}, nil
}

// flushed is the highest seq a successful barrier has covered so far.
func (b *f2Barriers) flushed() event.Seq {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.cuts) == 0 {
		return 0
	}
	return b.cuts[len(b.cuts)-1]
}

// last is the highest accepted seq of type typ, 0 for none.
func (b *f2Barriers) last(typ string) event.Seq {
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

type f2Writer struct {
	store.Writer
	b *f2Barriers
}

func (w *f2Writer) Append(ctx context.Context, bt store.Batch) error {
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

func (w *f2Writer) Flush(ctx context.Context) error {
	w.b.mu.Lock()
	at := w.b.accepted
	w.b.mu.Unlock()
	if err := w.Writer.Flush(ctx); err != nil {
		return err
	}
	w.b.mu.Lock()
	if !w.b.hide {
		w.b.cuts = append(w.b.cuts, at)
	}
	w.b.mu.Unlock()
	return nil
}

// f2Durability runs flow F2 on the SQLite store in a temp home behind a
// barrier recorder and returns what failed of TC-11b's checks. At each cut,
// before the provider attempt, at the start of the tool body and once the turn
// is idle, a successful Flush must already cover the records that cut needs,
// and a second connection must read them from the database.
func f2Durability(t *testing.T, hide bool) (problems []string, seen string, sess session.Handle, sent []session.Request, st store.Store, stop func()) {
	t.Helper()
	layout := temphome.New(t)
	db := must(sqlite.OpenHome(bg, layout))
	t.Cleanup(func() { _ = db.Close(bg) })
	bar := newF2Barriers(db)
	bar.hide = hide
	durable := func() []store.Record {
		other := must(sqlite.Open(bg, layout.DB()))
		defer func() { _ = other.Close(bg) }()
		r := must(other.OpenRead(bg, f2Session))
		defer func() { _ = r.Close(bg) }()
		return must(r.Read(bg, 0, 0, store.ReadOptions{}))
	}
	var (
		mu    sync.Mutex
		order []string
	)
	fail := func(format string, args ...any) {
		mu.Lock()
		problems = append(problems, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	// covered checks one cut: a barrier covers through, and the database holds it.
	covered := func(cut string, through event.Seq) {
		if f := bar.flushed(); f < through {
			fail("%s: no flush covers seq %d (flushed through %d)", cut, through, f)
		}
		rows := durable()
		if len(rows) == 0 || rows[len(rows)-1].Seq < through {
			fail("%s: the database holds %d rows, want through seq %d", cut, len(rows), through)
		}
		mu.Lock()
		order = append(order, cut)
		mu.Unlock()
	}
	hooks := f2Hooks{
		attempt: func(n int, req session.Request) {
			covered("attempt", req.AsOf())
			if n == 2 {
				covered("attempt", bar.last("step/end"))
			}
		},
		body: func() {
			for _, typ := range []string{"assistant/message", "tool/call", "approval/decided"} {
				s := bar.last(typ)
				if s == 0 {
					fail("before the tool body: no %s was accepted", typ)
				}
				covered("body", s)
			}
		},
	}
	sess, sent, stop = f2Run(t, bar, hooks)
	// At the end, before the driver closes (which flushes again), the step/end
	// and turn/end are the last durable rows and a barrier covered them.
	covered("end", bar.last("turn/end"))
	rows := durable()
	if n := len(rows); n < 2 || rows[n-1].Type != "turn/end" || rows[n-2].Type != "step/end" || event.Offset(n) != sess.Len() {
		fail("after the turn the database holds %d of %d rows, ending %v", n, sess.Len(), rows[max(0, n-2):])
	}
	return problems, strings.Join(slices.Compact(order), ","), sess, sent, bar, stop
}

// TC-11b, the Stage 11.4 scenario "Flow F2 persisted with checkpoints before
// I/O": on the SQLite store in a temp home a successful Flush, recorded at the
// writer, covers, before each provider attempt, every record the request
// reflects (and before attempt 2, step 1's step/end); before the tool body the
// call's assistant/message, tool/call and approval/decided; and before the turn
// is idle, the step/end and turn/end. A second connection reads each of them
// from the database at the same cut. The log reopened from the database equals
// the in-memory golden.
func TestFlowF2CheckpointsBeforeIOOnSQLite(t *testing.T) {
	problems, seen, sess, sent, st, stop := f2Durability(t, false)
	if len(problems) > 0 {
		t.Fatal(strings.Join(problems, "\n"))
	}
	if seen != "attempt,body,attempt,end" {
		t.Fatalf("checkpoints observed %s", seen)
	}
	f2Check(t, st, sess, sent)

	// The log reopened from the database equals the in-memory golden.
	stop()
	must(0, sess.Close(bg))
	rs, release := must2(session.NewService(cordis.New(), st, session.Config{}).OpenRead(bg, f2Session))
	defer release()
	golden.Assert(t, f2Golden, f2Serialize(must(rs.Read(bg, 0, 0))))
}

// f2IdleBound bounds every F2 wait for the driver to go idle. It only catches a
// hang: a run normally idles in well under a second, but the gate's -race run of
// the whole module on a loaded Windows host once took longer than an earlier 10 s
// bound (TestFlowF2CheckpointOracleCatchesAMissingFlush, win lane at d67d483), so
// the bound is a minute, still far inside the gate's test timeout.
const f2IdleBound = time.Minute

// The TC-11b oracle fails without the barriers: the same run with every flush
// hidden from the recorder (the rows are still durable, because the SQLite
// store commits each append) is reported at every cut.
func TestFlowF2CheckpointOracleCatchesAMissingFlush(t *testing.T) {
	problems, _, _, _, _, _ := f2Durability(t, true)
	for _, cut := range []string{"attempt", "body", "end"} {
		if !slices.ContainsFunc(problems, func(p string) bool { return strings.HasPrefix(p, cut+": no flush covers") }) {
			t.Errorf("a missing flush at the %s cut was not reported: %q", cut, problems)
		}
	}
}

// The F2 retry variant (plan Stage 16.4): one keyless turn on a scripted route
// whose first attempt fails, with the llm.retry component that f2Start mounts.
const (
	f2RetryGolden = "f2_retry_log"
	f2OtherRoute  = vocab.RouteID("r/f2-other")
	f2RetryAfter  = 7 * time.Second
	f2RouteCap    = 5 * time.Second
	f2RetryPrompt = "answer once"
	f2RetryAnswer = "answered after the retry"
)

// f2Failing is a keyless scripted route adapter: each attempt first plays the
// next of its scripted failures, as the provider adapter would classify it
// (Retry-After parsed into the failure), and then the testkit's scripted turns.
type f2Failing struct {
	llm.Adapter
	mu       sync.Mutex
	fails    []*vocab.Failure
	prepared int
}

func (a *f2Failing) Prepare(ctx context.Context, rt vocab.RouteID, model string, cfg vocab.CallConfig) (llm.Call, error) {
	a.mu.Lock()
	a.prepared++
	a.mu.Unlock()
	c, err := a.Adapter.Prepare(ctx, rt, model, cfg)
	if err != nil {
		return nil, err
	}
	return f2FailingCall{Call: c, a: a}, nil
}

func (a *f2Failing) calls() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.prepared
}

type f2FailingCall struct {
	llm.Call
	a *f2Failing
}

func (c f2FailingCall) Attempt(ctx context.Context, req session.Request) iter.Seq2[vocab.Chunk, error] {
	c.a.mu.Lock()
	if len(c.a.fails) == 0 {
		c.a.mu.Unlock()
		return c.Call.Attempt(ctx, req)
	}
	f := c.a.fails[0]
	c.a.fails = c.a.fails[1:]
	c.a.mu.Unlock()
	return func(yield func(vocab.Chunk, error) bool) {
		yield(vocab.Finish{Reason: vocab.FinishError, Failure: f}, nil)
	}
}

// f2Fail is a classified provider failure with an HTTP status and Retry-After.
func f2Fail(code vocab.Code, msg string, status int, after time.Duration) *vocab.Failure {
	f := must(vocab.NewFailure(code, msg))
	f.Status, f.RetryAfter = status, after
	return f
}

// f2RetryRoutes registers the F2 route, failing first with fails and then
// serving turns, under a bounded policy whose Retry-After cap is f2RouteCap, and
// a second route that no failure may be taken to (SP-2).
func f2RetryRoutes(t *testing.T, fails []*vocab.Failure, turns ...fakemodel.Turn) (register func(*llm.Runtime) agent.ModelSelection, main, other *f2Failing) {
	t.Helper()
	main = &f2Failing{Adapter: fakemodel.NewAdapter(fakemodel.NewTransport(t, turns...)), fails: fails}
	other = &f2Failing{Adapter: fakemodel.NewAdapter(fakemodel.NewTransport(t))}
	policy := llm.RetryPolicy{Mode: llm.RetryBounded, MaxRetries: 2, RetryAfterCap: f2RouteCap, Jitter: llm.NoJitter}
	return func(rt *llm.Runtime) agent.ModelSelection {
		reg := must(rt.Register("f2", llm.Route{ID: f2Route, Models: []llm.Model{{Name: fakemodel.ModelName}}, Adapter: main, Retry: policy}))
		t.Cleanup(reg.Close)
		alt := must(rt.Register("f2-other", llm.Route{ID: f2OtherRoute, Models: []llm.Model{{Name: fakemodel.ModelName}}, Adapter: other, Retry: llm.DefaultRetry()}))
		t.Cleanup(alt.Close)
		return agent.ModelSelection{Route: f2Route}
	}, main, other
}

// f2Ordered reports whether want occurs in types in order (other types between).
func f2Ordered(types, want []string) bool {
	i := 0
	for _, typ := range types {
		if i < len(want) && typ == want[i] {
			i++
		}
	}
	return i == len(want)
}

// Plan 16.4 scenario "A streamed turn is retried within the route's cap": the
// F2 composition with retry mounted, a scripted model returning overloaded
// with Retry-After 7s under a route cap of 5s, then success, on the virtual
// clock. The log holds assistant/attempt, llm/retry{delay_ms:5000},
// llm/retry-started and assistant/message in that order; the driver waited
// exactly 5s; the bracketed log equals testdata/f2_retry_log.golden.
//
//dsh:test DH-T-LLM-7
func TestFlowF2RetriedWithinTheRouteCap(t *testing.T) {
	st := memstore.New()
	register, main, other := f2RetryRoutes(t,
		[]*vocab.Failure{f2Fail(vocab.CodeServer, "overloaded", 529, f2RetryAfter)}, fakemodel.Reply(f2RetryAnswer))
	r := f2Start(t, st, f2Hooks{noApproval: true}, register)
	must(r.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: f2RetryPrompt}))
	if !r.clk.BlockUntil(1, 10*time.Second) {
		t.Fatal("the driver never waited to retry")
	}
	r.clk.Advance(f2RouteCap)
	ctx, cancel := context.WithTimeout(bg, f2IdleBound)
	defer cancel()
	if err := r.drv.WaitIdle(ctx); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	evs := must(r.sess.Read(bg, 0, 0))
	if want := []string{"assistant/attempt", "llm/retry", "llm/retry-started", "assistant/message", "turn/end"}; !f2Ordered(f2Types(evs), want) {
		t.Fatalf("the log %v does not hold %v in order", f2Types(evs), want)
	}
	if n := len(f2OfType(evs, "assistant/attempt")); n != 1 {
		t.Fatalf("%d failed attempts, want 1", n)
	}
	sched := must(f2Decode[retry.Scheduled](f2OfType(evs, "llm/retry")[0]))
	if sched.DelayMs != 5000 || sched.Policy.RetryAfterCap != f2RouteCap || sched.Failure.RetryAfter != f2RetryAfter {
		t.Fatalf("llm/retry %+v, want 5000ms under the route's 5s cap", sched)
	}
	if w := r.waits(); !slices.Equal(w, []time.Duration{f2RouteCap}) {
		t.Fatalf("the driver waited %v, want exactly %v", w, f2RouteCap)
	}
	te := must(f2Decode[event.TurnEnd](f2OfType(evs, "turn/end")[0]))
	if te.Reason.Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v", te.Reason)
	}
	if main.calls() != 2 || other.calls() != 0 {
		t.Fatalf("prepared %d calls on the route and %d on the other, want 2 and 0", main.calls(), other.calls())
	}
	f2Check(t, st, r.sess, r.models.requests())
	golden.Assert(t, f2RetryGolden, f2Serialize(evs))
}

// Plan 16.4 scenario "A bad credential on a streamed turn": the same
// composition and a scripted llm/auth failure. Exactly one assistant/attempt is
// logged, then llm/retry-abandoned{not-retryable}, with no llm/retry, no wait,
// no second attempt and no call on another route (SP-2).
//
//dsh:test DH-T-LLM-7
func TestFlowF2BadCredentialIsNeverRetried(t *testing.T) {
	register, main, other := f2RetryRoutes(t,
		[]*vocab.Failure{f2Fail(vocab.CodeAuth, "invalid credential", 401, time.Second)})
	r := f2Start(t, memstore.New(), f2Hooks{noApproval: true}, register)
	must(r.inbox.Send(bg, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: f2RetryPrompt}))
	ctx, cancel := context.WithTimeout(bg, f2IdleBound)
	defer cancel()
	if err := r.drv.WaitIdle(ctx); err != nil {
		t.Fatalf("wait idle: %v", err)
	}
	evs := must(r.sess.Read(bg, 0, 0))
	if n := len(f2OfType(evs, "assistant/attempt")); n != 1 {
		t.Fatalf("%d assistant/attempt, want exactly 1", n)
	}
	if !f2Ordered(f2Types(evs), []string{"assistant/attempt", "llm/retry-abandoned", "turn/end"}) {
		t.Fatalf("log %v", f2Types(evs))
	}
	for _, typ := range []string{"llm/retry", "llm/retry-started", "assistant/message"} {
		if n := len(f2OfType(evs, typ)); n != 0 {
			t.Fatalf("%d %s in a turn whose credential failed", n, typ)
		}
	}
	ab := must(f2Decode[retry.Abandoned](f2OfType(evs, "llm/retry-abandoned")[0]))
	if ab.Reason != llm.AbandonNotRetryable || ab.Failure == nil || ab.Failure.Code != vocab.CodeAuth {
		t.Fatalf("llm/retry-abandoned %+v, want not-retryable for llm/auth", ab)
	}
	te := must(f2Decode[event.TurnEnd](f2OfType(evs, "turn/end")[0]))
	if te.Reason.Kind != event.ReasonError || te.Reason.Failure == nil || te.Reason.Failure.Code != vocab.CodeAuth {
		t.Fatalf("turn/end %+v, want error with llm/auth", te.Reason)
	}
	if w := r.waits(); len(w) != 0 {
		t.Fatalf("the driver waited %v for a failure that is never retried", w)
	}
	if main.calls() != 1 || other.calls() != 0 || len(r.models.requests()) != 1 {
		t.Fatalf("prepared %d on the route and %d on the other, %d requests; want 1, 0, 1", main.calls(), other.calls(), len(r.models.requests()))
	}
}
