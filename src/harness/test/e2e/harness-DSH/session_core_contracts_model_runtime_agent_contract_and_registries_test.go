//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

var mrarBG = context.Background()

func mrarMust[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

type mrarAsk struct {
	Hint string `json:"hint"`
}

// mrarSummaryAsk is a plugin type describing an auxiliary request, so Complete
// has a non-loop request to run.
var mrarSummaryAsk = func() *event.Type[mrarAsk] {
	typ := event.MustDefine(event.Default(), event.Spec[mrarAsk]{Name: e2ePluginTypePrefix + "mrar-summary", Version: 1})
	if err := surface.DeclareBuilder(event.Default(), typ.Name(), func(ev event.Event, at surface.View, b *surface.AuxBuilder) error {
		b.Purpose("summary")
		b.History(at.History())
		m, err := vocab.RestoreMessage("summary-ask", vocab.RoleUser, nil, vocab.TextBlock("summarise"))
		if err != nil {
			return err
		}
		b.Message(m)
		return nil
	}); err != nil {
		panic(err)
	}
	return typ
}()

// ---------------------------------------------------------------- llm runtime

// mrarActor names who is appending: the store attributes every batch to it.
type mrarActor struct{ v atomic.Value }

func (a *mrarActor) set(s string) { a.v.Store(s) }
func (a *mrarActor) get() string {
	if s, ok := a.v.Load().(string); ok {
		return s
	}
	return "runtime"
}

type mrarAppend struct {
	actor   string
	records int
	first   store.Seq
}

// mrarRecordingStore wraps memstore and records every append with the actor
// that was active when it reached the store.
type mrarRecordingStore struct {
	store.Store
	actor *mrarActor
	mu    sync.Mutex
	log   []mrarAppend
}

func (s *mrarRecordingStore) wrap(w store.Writer, err error) (store.Writer, error) {
	if err != nil {
		return nil, err
	}
	return &mrarRecordingWriter{Writer: w, s: s}, nil
}

func (s *mrarRecordingStore) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	return s.wrap(s.Store.Create(ctx, h, o))
}

func (s *mrarRecordingStore) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	return s.wrap(s.Store.OpenWrite(ctx, id, o))
}

func (s *mrarRecordingStore) appends() []mrarAppend {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]mrarAppend(nil), s.log...)
}

type mrarRecordingWriter struct {
	store.Writer
	s *mrarRecordingStore
}

func (w *mrarRecordingWriter) Append(ctx context.Context, b store.Batch) error {
	if err := w.Writer.Append(ctx, b); err != nil {
		return err
	}
	w.s.mu.Lock()
	w.s.log = append(w.s.log, mrarAppend{actor: w.s.actor.get(), records: len(b.Records), first: b.First})
	w.s.mu.Unlock()
	return nil
}

type mrarScript func(ctx context.Context, yield func(vocab.Chunk, error) bool)

// mrarFake is an in-memory route adapter: each Attempt runs the next script.
type mrarFake struct {
	mu       sync.Mutex
	scripts  []mrarScript
	attempts atomic.Int32
}

func (f *mrarFake) Prepare(context.Context, vocab.RouteID, string, vocab.CallConfig) (llm.Call, error) {
	return f, nil
}

func (f *mrarFake) Attempt(ctx context.Context, _ session.Request) iter.Seq2[vocab.Chunk, error] {
	f.attempts.Add(1)
	f.mu.Lock()
	s := f.scripts[0]
	f.scripts = f.scripts[1:]
	f.mu.Unlock()
	return func(yield func(vocab.Chunk, error) bool) { s(ctx, yield) }
}

func mrarChunks(cs ...vocab.Chunk) mrarScript {
	return func(_ context.Context, yield func(vocab.Chunk, error) bool) {
		for _, c := range cs {
			if !yield(c, nil) {
				return
			}
		}
	}
}

func mrarText(s string) []vocab.Chunk {
	return []vocab.Chunk{vocab.BlockStart{Index: 0, Kind: vocab.KindText}, vocab.TextDelta{Index: 0, Text: s},
		vocab.BlockEnd{Index: 0, Block: vocab.TextBlock(s)}}
}

func mrarFailOnceThenSucceed() []mrarScript {
	overloaded := mrarMust(vocab.NewFailure(vocab.CodeServer, "overloaded"))
	return []mrarScript{
		mrarChunks(vocab.BlockStart{Index: 0, Kind: vocab.KindText}, vocab.TextDelta{Index: 0, Text: "par"},
			vocab.Finish{Reason: vocab.FinishError, Failure: overloaded}),
		mrarChunks(append(mrarText("hi there"), vocab.UsageChunk{Usage: vocab.Usage{Input: 3, Output: 2}}, vocab.Finish{Reason: vocab.FinishStop})...),
	}
}

// mrarSink is the caller's AttemptSink: it logs each record as one settlement
// event under the "caller" actor and notes the log length on arrival.
type mrarSink struct {
	h       session.Handle
	actor   *mrarActor
	records []llm.Attempt
	before  []event.Offset
	seqs    []event.Seq
}

func (s *mrarSink) Settle(a llm.Attempt) error {
	s.records = append(s.records, a)
	s.before = append(s.before, s.h.Len())
	d, err := a.Draft()
	if err != nil {
		return err
	}
	s.actor.set("caller")
	defer s.actor.set("runtime")
	r, err := s.h.Append(mrarBG, d)
	if err != nil {
		return err
	}
	s.seqs = append(s.seqs, r.Seq)
	return nil
}

type mrarSettlement struct {
	RequestAsOf   *event.Seq `json:"request_as_of"`
	RequestDigest string     `json:"request_digest"`
}

type mrarRequestSeen struct {
	asOf   event.Seq
	digest string
}

// ---------------------------------------------------------------- agent

// mrarStub is a stub agent: a real session and scope, no loop.
type mrarStub struct {
	ref agent.Ref
	h   session.Handle
	sc  *scope.Scope
	off atomic.Bool
}

func (s *mrarStub) Ref() agent.Ref               { return s.ref }
func (s *mrarStub) Session() session.ReadSession { return s.h }
func (s *mrarStub) Scope() *cordis.Context       { return s.sc.Ctx() }
func (s *mrarStub) ScopeKey() scope.Key          { return s.sc.Key() }
func (s *mrarStub) Status() agent.Status         { return agent.StatusIdle }
func (s *mrarStub) State() agent.State {
	if s.off.Load() {
		return agent.StateDisposed
	}
	return agent.StateLive
}
func (s *mrarStub) Close(ctx context.Context) error {
	s.off.Store(true)
	_ = s.sc.Close()
	return s.h.Close(ctx)
}

type mrarPreStep = func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error)

// ---------------------------------------------------------------- human

type mrarCarrier struct {
	got chan human.Item
}

func (c *mrarCarrier) Deliver(_ context.Context, it human.Item) error {
	c.got <- it
	return nil
}
func (c *mrarCarrier) Withdraw(human.ID, human.Reason) {}

type mrarAsked struct {
	ans human.Answer
	by  human.Actor
	err error
}

// ---------------------------------------------------------------- state

type mrarState struct {
	root    *cordis.Context
	logs    *cordis.MemoryExporter
	cleanup []func()

	// llm
	actor     *mrarActor
	rec       *mrarRecordingStore
	h         session.Handle
	rt        *llm.Runtime
	fake      *mrarFake
	sink      *mrarSink
	start     event.Offset
	nAppends  int // store appends before the phase under test
	attempts  int
	finishes  []int
	requests  []mrarRequestSeen
	completes []error
	firstMsg  vocab.Message

	// agent
	ag        agent.Handle
	agh       session.Handle
	observed  []agent.StepNotice
	rejectReg *cordis.EffectHandle
	stepStart event.Offset
	outcome   agent.StepOutcome

	// views
	vreg      *views.Registry
	cnt       views.Handle[int]
	nxt       views.Handle[event.Seq]
	stop      atomic.Bool
	wg        sync.WaitGroup
	appendErr atomic.Value
	cuts      map[event.Seq]bool
	cutErr    error

	// human
	hreg    human.Registry
	ui      *mrarCarrier
	refA    agent.Ref
	refB    agent.Ref
	askA    <-chan mrarAsked
	askB    <-chan mrarAsked
	pending []human.Item
}

func (s *mrarState) newRoot() {
	s.root = cordis.New()
	s.logs = &cordis.MemoryExporter{}
	s.root.Loggers().ClearExporters()
	s.root.Loggers().AddExporter(s.logs)
	root := s.root
	s.cleanup = append(s.cleanup, root.Dispose)
}

func (s *mrarState) close() {
	s.stop.Store(true)
	s.wg.Wait()
	if s.hreg != nil {
		s.hreg.Close()
	}
	for i := len(s.cleanup) - 1; i >= 0; i-- {
		s.cleanup[i]()
	}
}

// ---------------------------------------------------------------- llm steps

func (s *mrarState) givenRuntimeFailsOnceThenSucceeds() error {
	s.fake = &mrarFake{scripts: mrarFailOnceThenSucceed()}
	var now atomic.Int64
	s.rt = llm.New(llm.WithClock(func() int64 { return now.Add(1) }))
	_, err := s.rt.Register("fake", llm.Route{ID: "r/a", Models: []llm.Model{{Name: "m1", Efforts: []string{"low", "high"}}}, Adapter: s.fake})
	return err
}

func (s *mrarState) givenRecordingStore() error {
	s.newRoot()
	s.actor = &mrarActor{}
	s.actor.set("setup")
	s.rec = &mrarRecordingStore{Store: memstore.New(), actor: s.actor}
	svc := session.NewService(s.root, s.rec, session.Config{})
	h, err := svc.Create(mrarBG, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		return err
	}
	s.h = h
	s.cleanup = append(s.cleanup, func() { _ = h.Close(mrarBG) })
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello"))
	if err != nil {
		return err
	}
	_, err = h.AppendGroup(mrarBG, []event.Draft{
		mrarMust(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		mrarMust(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		mrarMust(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "r/a"})),
		mrarMust(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})),
	})
	if err != nil {
		return err
	}
	if _, err := h.Append(mrarBG, mrarMust(mrarSummaryAsk.Draft(mrarAsk{Hint: "x"}))); err != nil {
		return err
	}
	if _, err := h.Flush(mrarBG); err != nil {
		return err
	}
	if len(s.rec.appends()) == 0 {
		return errors.New("the recording store saw no setup appends")
	}
	return nil
}

func (s *mrarState) givenCallerSink() error {
	s.sink = &mrarSink{h: s.h, actor: s.actor}
	return nil
}

func (s *mrarState) beginPhase() {
	s.actor.set("runtime")
	s.sink.records, s.sink.before, s.sink.seqs = nil, nil, nil
	s.finishes, s.requests = nil, nil
	s.start = s.h.Len()
	s.nAppends = len(s.rec.appends())
}

func (s *mrarState) whenStreamAttempts() error {
	s.beginPhase()
	s.attempts = 2
	for i := 0; i < s.attempts; i++ {
		p, err := s.rt.Prepare(mrarBG, "r/a", "", vocab.Controls{})
		if err != nil {
			return err
		}
		req, err := s.h.NextRequest()
		if err != nil {
			return err
		}
		s.requests = append(s.requests, mrarRequestSeen{req.AsOf(), req.Digest()})
		n := 0
		for c := range p.Stream(mrarBG, req, s.sink) {
			if _, ok := c.(vocab.Finish); ok {
				n++
			}
		}
		s.finishes = append(s.finishes, n)
	}
	return nil
}

func (s *mrarState) whenCompleteCalls() error {
	s.fake.mu.Lock()
	s.fake.scripts = mrarFailOnceThenSucceed()
	s.fake.mu.Unlock()
	evs, err := s.h.Read(mrarBG, 0, 0)
	if err != nil {
		return err
	}
	var askSeq event.Seq
	found := false
	for _, ev := range evs {
		if ev.Type() == mrarSummaryAsk.Name() {
			askSeq, found = ev.Seq(), true
		}
	}
	if !found {
		return errors.New("no auxiliary ask in the log")
	}
	s.beginPhase()
	s.attempts = 2
	s.completes = nil
	for i := 0; i < s.attempts; i++ {
		req, err := s.h.RequestFrom(askSeq)
		if err != nil {
			return err
		}
		s.requests = append(s.requests, mrarRequestSeen{req.AsOf(), req.Digest()})
		m, err := s.rt.Complete(mrarBG, req, s.sink)
		s.completes = append(s.completes, err)
		if err == nil {
			s.firstMsg = m
		}
	}
	return nil
}

func (s *mrarState) thenOneFinishPerStream() error {
	if len(s.finishes) != s.attempts {
		return fmt.Errorf("%d streams ran, want %d", len(s.finishes), s.attempts)
	}
	for i, n := range s.finishes {
		if n != 1 {
			return fmt.Errorf("stream %d ended with %d Finish chunks, want 1", i+1, n)
		}
	}
	return nil
}

func (s *mrarState) thenCompleteResults() error {
	if len(s.completes) != 2 {
		return fmt.Errorf("%d Complete calls", len(s.completes))
	}
	var fl *vocab.Failure
	if !errors.As(s.completes[0], &fl) || fl.Code != vocab.CodeServer {
		return fmt.Errorf("first Complete = %v, want the route's server failure", s.completes[0])
	}
	if s.completes[1] != nil {
		return fmt.Errorf("second Complete = %v, want success", s.completes[1])
	}
	if bs := s.firstMsg.Blocks(); len(bs) == 0 || bs[0].Text == nil || bs[0].Text.Text != "hi there" {
		return fmt.Errorf("second Complete message %+v", s.firstMsg)
	}
	return nil
}

func (s *mrarState) thenOneRecordPerAttempt() error {
	if len(s.sink.records) != s.attempts {
		return fmt.Errorf("the sink received %d records for %d attempts", len(s.sink.records), s.attempts)
	}
	for i, rec := range s.sink.records {
		want := s.requests[i]
		if rec.RequestDigest == "" || rec.RequestDigest != want.digest || rec.RequestAsOf != want.asOf {
			return fmt.Errorf("record %d carries asOf %d digest %q, request was %d %q", i+1, rec.RequestAsOf, rec.RequestDigest, want.asOf, want.digest)
		}
	}
	if s.sink.records[0].Succeeded() || !s.sink.records[1].Succeeded() {
		return fmt.Errorf("records %v/%v, want a failure then a success", s.sink.records[0].Succeeded(), s.sink.records[1].Succeeded())
	}
	return nil
}

func (s *mrarState) thenOneSettlementPerAttempt() error {
	if got := s.h.Len() - s.start; got != event.Offset(s.attempts) {
		return fmt.Errorf("the log grew by %d, want %d settlements", got, s.attempts)
	}
	evs, err := s.h.Read(mrarBG, s.start, 100)
	if err != nil {
		return err
	}
	for i, ev := range evs {
		if ev.Seq() != s.sink.seqs[i] {
			return fmt.Errorf("settlement %d at seq %d, the caller appended %d", i+1, ev.Seq(), s.sink.seqs[i])
		}
		var st mrarSettlement
		if err := json.Unmarshal([]byte(ev.Raw()), &st); err != nil {
			return err
		}
		rec := s.sink.records[i]
		if st.RequestAsOf == nil || *st.RequestAsOf != rec.RequestAsOf || st.RequestDigest != rec.RequestDigest {
			return fmt.Errorf("settlement %d (%s) does not attribute its request: %s", i+1, ev.Type(), ev.Raw())
		}
	}
	return nil
}

func (s *mrarState) thenAllAppendsByCaller() error {
	if _, err := s.h.Flush(mrarBG); err != nil {
		return err
	}
	phase := s.rec.appends()[s.nAppends:]
	total := 0
	for _, a := range phase {
		if a.actor != "caller" {
			return fmt.Errorf("an append of %d records at seq %d was made by %q, not the caller", a.records, a.first, a.actor)
		}
		total += a.records
	}
	if total != s.attempts {
		return fmt.Errorf("the store took %d records in this phase, want %d", total, s.attempts)
	}
	return nil
}

func (s *mrarState) thenRuntimeMadeZeroAppends() error {
	for i, before := range s.sink.before {
		if want := s.start + event.Offset(i); before != want {
			return fmt.Errorf("attempt %d: the log was %d long when the record arrived, want %d", i+1, before, want)
		}
	}
	for _, a := range s.rec.appends()[s.nAppends:] {
		if a.actor == "runtime" {
			return fmt.Errorf("the runtime appended %d records at seq %d", a.records, a.first)
		}
	}
	return nil
}

// ---------------------------------------------------------------- agent steps

func (s *mrarState) ensureAgent() error {
	if s.ag != nil {
		return nil
	}
	s.newRoot()
	tab := scope.NewTable(s.root)
	svc := session.NewService(s.root, memstore.New(), session.Config{})
	reg := agent.NewRegistry(s.root)
	if _, err := reg.SetFactory(s.root, agent.FactoryFunc(func(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
		h, err := svc.Create(mrarBG, nil, session.CreateOptions{})
		if err != nil {
			return nil, err
		}
		sc, err := tab.New(owner, string(o.Session))
		if err != nil {
			return nil, err
		}
		return &mrarStub{ref: ref, h: h, sc: sc}, nil
	})); err != nil {
		return err
	}
	ag, err := reg.Create(s.root, agent.CreateOptions{Session: "mrar-a"})
	if err != nil {
		return err
	}
	s.ag = ag
	s.agh = ag.Agent().Session().(session.Handle)
	s.cleanup = append(s.cleanup, func() { _ = ag.Dispose(mrarBG) })
	return nil
}

func (s *mrarState) givenPreStepReject(reason string) error {
	if err := s.ensureAgent(); err != nil {
		return err
	}
	h, err := agent.PreStep.On(s.ag.Agent().Scope(), mrarPreStep(func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		return agent.Reject{By: "quota-guard", Reason: reason}, nil
	}))
	s.rejectReg = h
	return err
}

func (s *mrarState) givenPanickingObserver() error {
	if err := s.ensureAgent(); err != nil {
		return err
	}
	_, err := agent.StepDecided.On(s.root, func(_ *cordis.Context, n agent.StepNotice) error {
		s.observed = append(s.observed, n)
		panic("mrar observer bug")
	})
	return err
}

func (s *mrarState) proposeStep(turn int) error {
	if _, err := s.agh.Append(mrarBG, mrarMust(event.TurnStartType.Draft(event.TurnStart{Turn: turn}))); err != nil {
		return err
	}
	s.stepStart = s.agh.Len()
	user := mrarMust(vocab.RestoreMessage(fmt.Sprintf("u%d", turn), vocab.RoleUser, nil, vocab.TextBlock("hi")))
	out, err := agent.ProposeStep(s.root, s.ag.Agent(), s.agh, agent.PreStepRequest{Turn: turn, Step: 1, Messages: []vocab.Message{user}})
	s.outcome = out
	return err
}

func (s *mrarState) whenDriverReachesPoint4() error { return s.proposeStep(1) }

func (s *mrarState) appendedTypes() ([]string, error) {
	evs, err := s.agh.Read(mrarBG, s.stepStart, 100)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Type())
	}
	return out, nil
}

func (s *mrarState) lastTurnEnd() (event.TurnEnd, error) {
	var te event.TurnEnd
	evs, err := s.agh.Read(mrarBG, s.agh.Len()-1, 1)
	if err != nil {
		return te, err
	}
	if len(evs) != 1 || evs[0].Type() != event.TurnEndType.Name() {
		return te, fmt.Errorf("the last event is not turn/end")
	}
	return te, json.Unmarshal([]byte(evs[0].Raw()), &te)
}

func (s *mrarState) thenNoEventAppended(typ string) error {
	got, err := s.appendedTypes()
	if err != nil {
		return err
	}
	for _, t := range got {
		if t == typ {
			return fmt.Errorf("%s was appended: %v", typ, got)
		}
	}
	if s.outcome.Entered {
		return errors.New("the step was entered")
	}
	return nil
}

func (s *mrarState) thenTurnEndWith(typ, kind, detail string) error {
	got, err := s.appendedTypes()
	if err != nil {
		return err
	}
	if strings.Join(got, ",") != typ {
		return fmt.Errorf("appended %v, want only %s", got, typ)
	}
	te, err := s.lastTurnEnd()
	if err != nil {
		return err
	}
	if te.Reason.Kind != kind || !strings.Contains(te.Reason.Detail, detail) {
		return fmt.Errorf("turn/end reason %+v, want %s carrying %q", te.Reason, kind, detail)
	}
	switch kind {
	case event.ReasonBlocked:
		if te.Reason.By != "quota-guard" || s.outcome.Seq != s.agh.Len()-1 {
			return fmt.Errorf("blocked turn/end %+v, outcome %+v", te, s.outcome)
		}
	case event.ReasonError:
		if te.Reason.By != agent.PreStep.Name() || !errors.Is(s.outcome.Err, agent.ErrExtensionFailed) {
			return fmt.Errorf("error turn/end %+v, outcome err %v", te, s.outcome.Err)
		}
	}
	return nil
}

func (s *mrarState) thenObserverPanicRecorded() error {
	if len(s.observed) != 1 || s.observed[0].Entered || s.observed[0].Reason.Kind != event.ReasonBlocked {
		return fmt.Errorf("observed %+v, want one blocked notice", s.observed)
	}
	logged := false
	for _, m := range s.logs.Messages() {
		if strings.Contains(fmt.Sprint(m), "mrar observer bug") {
			logged = true
		}
	}
	if !logged {
		return errors.New("the observer's panic was not recorded")
	}
	te, err := s.lastTurnEnd()
	if err != nil {
		return err
	}
	if te.Reason.Kind != event.ReasonBlocked {
		return fmt.Errorf("the observer changed the outcome to %s", te.Reason.Kind)
	}
	return nil
}

func (s *mrarState) whenPreStepPanics() error {
	s.rejectReg.Dispose()
	if _, err := agent.PreStep.On(s.ag.Agent().Scope(), mrarPreStep(func(*cordis.Context, agent.PreStepRequest, func(agent.PreStepRequest) (agent.PreStepDecision, error)) (agent.PreStepDecision, error) {
		panic("mrar listener bug")
	})); err != nil {
		return err
	}
	return s.proposeStep(2)
}

// ---------------------------------------------------------------- views steps

func (s *mrarState) givenTwoFolds() error {
	s.newRoot()
	s.vreg = views.New()
	var err error
	if s.cnt, err = views.Register(s.vreg, views.Definition[int]{Key: "e2e/mrar-count", Version: 1,
		Init:  func(session.Header) int { return 0 },
		Apply: func(n int, _ event.Event) (int, bool) { return n + 1, true }}); err != nil {
		return err
	}
	s.nxt, err = views.Register(s.vreg, views.Definition[event.Seq]{Key: "e2e/mrar-next", Version: 1,
		Init:  func(session.Header) event.Seq { return 0 },
		Apply: func(_ event.Seq, ev event.Event) (event.Seq, bool) { return ev.Seq() + 1, true }})
	return err
}

func (s *mrarState) givenConcurrentAppender() error {
	svc := session.NewService(s.root, memstore.New(), session.Config{Folder: s.vreg})
	h, err := svc.Create(mrarBG, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		return err
	}
	s.h = h
	s.cleanup = append(s.cleanup, func() { _ = h.Close(mrarBG) })
	for range 4 {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			for !s.stop.Load() {
				d := mrarMust(event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: "v"}))
				if _, err := h.Append(mrarBG, d); err != nil {
					s.appendErr.Store(err)
					return
				}
			}
		}()
	}
	return nil
}

func (s *mrarState) whenReaderRequestsBothFolds() error {
	s.cuts = map[event.Seq]bool{}
	deadline := time.Now().Add(10 * time.Second)
	for reads := 0; reads < 300 || len(s.cuts) < 20; reads++ {
		if time.Now().After(deadline) {
			break
		}
		snap, err := s.vreg.Read(mrarBG, s.h)
		if err != nil {
			return err
		}
		c, err := s.cnt.Get(snap)
		if err != nil {
			return err
		}
		n, err := s.nxt.Get(snap)
		if err != nil {
			return err
		}
		if event.Seq(c) != snap.AsOf || n != snap.AsOf {
			s.cutErr = fmt.Errorf("one read, two cuts: count %d, next %d, AsOf %d", c, n, snap.AsOf)
			break
		}
		s.cuts[snap.AsOf] = true
	}
	s.stop.Store(true)
	s.wg.Wait()
	return nil
}

func (s *mrarState) thenSameSeq() error {
	if v := s.appendErr.Load(); v != nil {
		return fmt.Errorf("the appender failed: %v", v)
	}
	if s.cutErr != nil {
		return s.cutErr
	}
	if len(s.cuts) < 20 {
		return fmt.Errorf("the reader saw %d cuts; the appender did not run concurrently", len(s.cuts))
	}
	final, err := s.vreg.Read(mrarBG, s.h)
	if err != nil {
		return err
	}
	c, err := s.cnt.Get(final)
	if err != nil {
		return err
	}
	if final.AsOf != s.h.Len() || c != int(s.h.Len()) {
		return fmt.Errorf("final read at %d count %d, session len %d", final.AsOf, c, s.h.Len())
	}
	return nil
}

// ---------------------------------------------------------------- human steps

func (s *mrarState) ask(it human.Item) <-chan mrarAsked {
	out := make(chan mrarAsked, 1)
	go func() {
		a, by, err := s.hreg.Ask(mrarBG, it)
		out <- mrarAsked{a, by, err}
	}()
	return out
}

func (s *mrarState) awaitDelivery() error {
	select {
	case <-s.ui.got:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("nothing was delivered")
	}
}

func (s *mrarState) givenTwoPendingApprovals() error {
	s.newRoot()
	s.hreg = human.New()
	s.ui = &mrarCarrier{got: make(chan human.Item, 16)}
	if _, err := s.hreg.RegisterAnswerer(s.root, "ui", s.ui); err != nil {
		return err
	}
	s.refA = agent.Ref{Session: "mrar-ses-a", Incarnation: 1}
	s.refB = agent.Ref{Session: "mrar-ses-b", Incarnation: 1}
	for _, r := range []agent.Ref{s.refA, s.refB} {
		if _, err := s.hreg.Bind(s.root, r, human.Binding{Channel: "ui"}); err != nil {
			return err
		}
	}
	s.askA = s.ask(human.Item{Kind: human.KindApproval, To: human.Audience{Agent: s.refA}, Payload: "rm -rf build"})
	if err := s.awaitDelivery(); err != nil {
		return err
	}
	s.askB = s.ask(human.Item{Kind: human.KindApproval, To: human.Audience{Agent: s.refB}, Payload: "git push"})
	if err := s.awaitDelivery(); err != nil {
		return err
	}
	if all := s.hreg.PendingAll(); len(all) != 2 {
		return fmt.Errorf("PendingAll = %+v, want two approvals", all)
	}
	return nil
}

func (s *mrarState) whenAListed() error {
	s.pending = s.hreg.Pending(s.refA)
	return nil
}

func (s *mrarState) thenOnlyA() error {
	if len(s.pending) != 1 || s.pending[0].To.Agent != s.refA || s.pending[0].Kind != human.KindApproval || s.pending[0].Payload != "rm -rf build" {
		return fmt.Errorf("Pending(A) = %+v, want only A's approval", s.pending)
	}
	return nil
}

func (s *mrarState) thenAnswerResolvesOne() error {
	by := human.Actor{Channel: "ui", Name: "alice"}
	if err := s.hreg.Answer(mrarBG, by, s.pending[0].ID, human.Answer{Outcome: human.OutcomeAllowedOnce}); err != nil {
		return err
	}
	select {
	case r := <-s.askA:
		if r.err != nil || r.ans.Outcome != human.OutcomeAllowedOnce || r.by != by {
			return fmt.Errorf("A's ask = %+v, want allowed-once by alice", r)
		}
	case <-time.After(5 * time.Second):
		return errors.New("A's ask did not resolve")
	}
	if p := s.hreg.Pending(s.refA); len(p) != 0 {
		return fmt.Errorf("Pending(A) after the answer = %+v", p)
	}
	if p := s.hreg.Pending(s.refB); len(p) != 1 || p[0].Payload != "git push" {
		return fmt.Errorf("Pending(B) = %+v, want B's approval untouched", p)
	}
	select {
	case r := <-s.askB:
		return fmt.Errorf("B's ask settled by A's answer: %+v", r)
	case <-time.After(50 * time.Millisecond):
	}
	return nil
}

// ---------------------------------------------------------------- wiring

func InitializeScenario_session_core_contracts_model_runtime_agent_contract_and_registries(ctx *godog.ScenarioContext) {
	s := &mrarState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, nil
	})

	ctx.Step(`^the runtime with an in-memory fake route that fails once then succeeds$`, s.givenRuntimeFailsOnceThenSucceeds)
	ctx.Step(`^an in-memory session store that records every append$`, s.givenRecordingStore)
	ctx.Step(`^a caller-side recording AttemptSink$`, s.givenCallerSink)
	ctx.Step(`^the caller runs one Prepared\.Stream attempt per scripted attempt and logs the record it receives$`, s.whenStreamAttempts)
	ctx.Step(`^the caller runs one Complete call per scripted attempt and logs the record it receives$`, s.whenCompleteCalls)
	ctx.Step(`^every stream ends with exactly one Finish chunk$`, s.thenOneFinishPerStream)
	ctx.Step(`^the first Complete returns the route failure and the second returns the message$`, s.thenCompleteResults)
	ctx.Step(`^the runtime delivers exactly one attempt record per attempt carrying requestAsOf and requestDigest$`, s.thenOneRecordPerAttempt)
	ctx.Step(`^the store holds exactly one settlement event per attempt$`, s.thenOneSettlementPerAttempt)
	ctx.Step(`^every append is attributed to the caller$`, s.thenAllAppendsByCaller)
	ctx.Step(`^the runtime itself made zero session appends$`, s.thenRuntimeMadeZeroAppends)

	ctx.Step(`^an agent\.PreStep listener returning Reject with reason "([^"]*)"$`, s.givenPreStepReject)
	ctx.Step(`^a Notify observer that panics$`, s.givenPanickingObserver)
	ctx.Step(`^the driver reaches point 4 of a step$`, s.whenDriverReachesPoint4)
	ctx.Step(`^no "([^"]*)" event is appended$`, s.thenNoEventAppended)
	ctx.Step(`^"([^"]*)" is appended with outcome "([^"]*)" carrying the reason "([^"]*)"$`, s.thenTurnEndWith)
	ctx.Step(`^"([^"]*)" is appended with outcome "([^"]*)" carrying "([^"]*)"$`, s.thenTurnEndWith)
	ctx.Step(`^the observer panic is recorded without changing that outcome$`, s.thenObserverPanicRecorded)
	ctx.Step(`^the PreStep listener itself panics and the driver reaches point 4 of the next turn$`, s.whenPreStepPanics)

	ctx.Step(`^two registered folds$`, s.givenTwoFolds)
	ctx.Step(`^a concurrent appender$`, s.givenConcurrentAppender)
	ctx.Step(`^a reader requests both folds$`, s.whenReaderRequestsBothFolds)
	ctx.Step(`^both results are computed at the same sequence number$`, s.thenSameSeq)

	ctx.Step(`^two agents each with a pending approval$`, s.givenTwoPendingApprovals)
	ctx.Step(`^agent A's interactions are listed$`, s.whenAListed)
	ctx.Step(`^only A's approval is returned$`, s.thenOnlyA)
	ctx.Step(`^answering it resolves exactly that one$`, s.thenAnswerResolvesOne)
}

func TestE2E_session_core_contracts_model_runtime_agent_contract_and_registries(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "session_core_contracts_model_runtime_agent_contract_and_registries",
		ScenarioInitializer: InitializeScenario_session_core_contracts_model_runtime_agent_contract_and_registries,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"session_core_contracts_model_runtime_agent_contract_and_registries.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
