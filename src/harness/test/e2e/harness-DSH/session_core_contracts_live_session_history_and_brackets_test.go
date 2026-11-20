//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

var lshbBG = context.Background()

type lshbTitleAsk struct {
	Hint string `json:"hint"`
}

// lshbTitleRequest is a plugin event type whose builder describes an auxiliary
// request; its name is unique to this stage so sibling suites cannot collide.
var lshbTitleRequest = func() *event.Type[lshbTitleAsk] {
	typ := event.MustDefine(event.Default(), event.Spec[lshbTitleAsk]{Name: e2ePluginTypePrefix + "lshb-title-request", Version: 1})
	if err := surface.DeclareBuilder(event.Default(), typ.Name(), func(ev event.Event, at surface.View, b *surface.AuxBuilder) error {
		var p lshbTitleAsk
		if err := json.Unmarshal([]byte(ev.Raw()), &p); err != nil {
			return err
		}
		b.Purpose("title")
		b.History(at.History())
		m, err := vocab.RestoreMessage("title-ask", vocab.RoleUser, nil, vocab.TextBlock("a title, "+p.Hint))
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

// lshbSubscriber records Committed seqs and Closed for one listener.
type lshbSubscriber struct {
	mu     sync.Mutex
	seqs   map[session.ID][]event.Seq
	closed map[session.ID]session.ClosedArgs
	done   chan struct{}
}

func (r *lshbSubscriber) waitClosed(id session.ID) (session.ClosedArgs, error) {
	deadline := time.After(10 * time.Second)
	for {
		r.mu.Lock()
		a, ok := r.closed[id]
		r.mu.Unlock()
		if ok {
			return a, nil
		}
		select {
		case <-r.done:
		case <-deadline:
			return session.ClosedArgs{}, fmt.Errorf("session %q never reported Closed", id)
		}
	}
}

type lshbState struct {
	root *cordis.Context
	st   store.Store
	svc  session.Service
	h    session.Handle
	subs []*lshbSubscriber

	receipts []event.Receipt

	req     session.Request
	rebuilt surface.Built
	ask     event.Receipt
	aux     session.Request

	lenBefore  session.Offset
	appendErr  error
	closerErrs []error
}

func lshbMust[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func lshbMsg(id string, role vocab.Role, blocks ...vocab.Block) vocab.Message {
	return lshbMust(vocab.RestoreMessage(id, role, nil, blocks...))
}

func lshbCtx(v string) event.Draft {
	return lshbMust(event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: v}))
}

func lshbTurnStart(n int) event.Draft {
	return lshbMust(event.TurnStartType.Draft(event.TurnStart{Turn: n}))
}

func lshbTurnEnd(n int) event.Draft {
	return lshbMust(event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: event.Reason{Kind: event.ReasonCompleted}}))
}

func lshbStepStart(turn, step int) event.Draft {
	return lshbMust(event.StepStartType.Draft(event.StepStart{Turn: turn, Step: step}))
}

func lshbStepEnd(turn, step int) event.Draft {
	return lshbMust(event.StepEndType.Draft(event.StepEnd{Turn: turn, Step: step}))
}

func (s *lshbState) reset() {
	if s.h != nil {
		_ = s.h.Close(lshbBG)
	}
	if s.root != nil {
		s.root.Dispose()
	}
	*s = lshbState{}
}

func (s *lshbState) newService() {
	s.root = cordis.New()
	s.root.Loggers().ClearExporters()
	s.root.Loggers().AddExporter(&cordis.MemoryExporter{})
	_ = scope.NewTable(s.root)
	s.st = memstore.New()
	s.svc = session.NewService(s.root, s.st, session.Config{})
}

func (s *lshbState) create() (session.Handle, error) {
	return s.svc.Create(lshbBG, nil, session.CreateOptions{Scope: scope.Key{}})
}

func (s *lshbState) subscribe() error {
	r := &lshbSubscriber{seqs: map[session.ID][]event.Seq{}, closed: map[session.ID]session.ClosedArgs{}, done: make(chan struct{}, 64)}
	if _, err := session.Committed.On(s.root, func(_ *cordis.Context, a session.CommittedArgs) error {
		r.mu.Lock()
		r.seqs[a.Session.ID()] = append(r.seqs[a.Session.ID()], a.Event.Seq())
		r.mu.Unlock()
		return nil
	}); err != nil {
		return err
	}
	if _, err := session.Closed.On(s.root, func(_ *cordis.Context, a session.ClosedArgs) error {
		r.mu.Lock()
		r.closed[a.ID] = a
		r.mu.Unlock()
		select {
		case r.done <- struct{}{}:
		default:
		}
		return nil
	}); err != nil {
		return err
	}
	s.subs = append(s.subs, r)
	return nil
}

// --- Scenario 1: ordered once-only delivery ---

func (s *lshbState) sessionWithSubscribers(n int) error {
	s.newService()
	for i := 0; i < n; i++ {
		if err := s.subscribe(); err != nil {
			return err
		}
	}
	h, err := s.create()
	if err != nil {
		return err
	}
	s.h = h
	return nil
}

func (s *lshbState) appendConcurrently(total, goroutines int) error {
	if total%goroutines != 0 {
		return fmt.Errorf("%d events do not split evenly over %d goroutines", total, goroutines)
	}
	per := total / goroutines
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	out := make(chan event.Receipt, total)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < per; i++ {
				r, err := s.h.Append(lshbBG, lshbCtx(fmt.Sprintf("g%d-%d", g, i)))
				if err != nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
					return
				}
				out <- r
			}
		}(g)
	}
	wg.Wait()
	close(out)
	if err := errors.Join(errs...); err != nil {
		return err
	}
	for r := range out {
		s.receipts = append(s.receipts, r)
	}
	if len(s.receipts) != total {
		return fmt.Errorf("got %d receipts, want %d", len(s.receipts), total)
	}
	return nil
}

func (s *lshbState) receiptsDense() error {
	n := len(s.receipts)
	times := make([]int64, n)
	seen := make([]bool, n)
	for _, r := range s.receipts {
		if int(r.Seq) < 0 || int(r.Seq) >= n {
			return fmt.Errorf("seq %d outside 0..%d", r.Seq, n-1)
		}
		if seen[r.Seq] {
			return fmt.Errorf("seq %d assigned twice", r.Seq)
		}
		seen[r.Seq], times[r.Seq] = true, r.Time
	}
	for i := 1; i < n; i++ {
		if times[i] < times[i-1] {
			return fmt.Errorf("commit time goes back at seq %d", i)
		}
	}
	if got := int(s.h.Len()); got != n {
		return fmt.Errorf("session length %d, want %d", got, n)
	}
	return nil
}

func (s *lshbState) subscribersGotAllOnceInOrder() error {
	id := s.h.ID()
	if err := s.h.Close(lshbBG); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	n := len(s.receipts)
	for i, r := range s.subs {
		c, err := r.waitClosed(id)
		if err != nil {
			return err
		}
		if c.Cause != nil {
			return fmt.Errorf("subscriber %d: session closed with %v", i, c.Cause)
		}
		r.mu.Lock()
		got := append([]event.Seq(nil), r.seqs[id]...)
		r.mu.Unlock()
		if len(got) != n {
			return fmt.Errorf("subscriber %d received %d events, want %d", i, len(got), n)
		}
		for j, sq := range got {
			if sq != event.Seq(j) {
				return fmt.Errorf("subscriber %d received seq %d at position %d", i, sq, j)
			}
		}
	}
	return nil
}

// --- Scenario 2: rebuilt request equals digest ---

func (s *lshbState) sessionWithHeader(route, tool string) error {
	s.newService()
	h, err := s.create()
	if err != nil {
		return err
	}
	s.h = h
	if _, err := h.NextRequest(); !errors.Is(err, session.ErrNoHeader) {
		return fmt.Errorf("a request with no header returned %v, want session.ErrNoHeader", err)
	}
	schema := vocab.ToolSchema{Name: tool, Description: tool, Parameters: json.RawMessage(`{"type":"object"}`)}
	header := lshbMust(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: vocab.RouteID(route), Tools: []vocab.ToolSchema{schema}}))
	_, err = h.AppendGroup(lshbBG, []event.Draft{lshbTurnStart(1), lshbStepStart(1, 1), header})
	return err
}

func (s *lshbState) handWrittenSequence(callID string) error {
	user := lshbMust(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{
		Message: lshbMsg("u1", vocab.RoleUser, vocab.TextBlock("find x"))}))
	if _, err := s.h.Append(lshbBG, user); err != nil {
		return err
	}
	asst := lshbMust(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
		Message: lshbMsg("a1", vocab.RoleAssistant, vocab.ToolUseBlock(callID, "search", json.RawMessage(`{"q":1}`)))}))
	if _, err := s.h.Append(lshbBG, asst); err != nil {
		return err
	}
	if _, err := s.h.NextRequest(); err == nil {
		return errors.New("a request was built while a tool call was pending")
	}
	result := lshbMust(event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: callID,
		Message: lshbMsg("r1", vocab.RoleTool, vocab.ToolResultBlock(callID, false, vocab.TextPart("x is here")))}))
	_, err := s.h.Append(lshbBG, result)
	return err
}

func (s *lshbState) buildNextRequest() error {
	req, err := s.h.NextRequest()
	if err != nil {
		return err
	}
	if req.IsZero() || req.Meta().Auxiliary() {
		return fmt.Errorf("loop request: zero %v meta %+v", req.IsZero(), req.Meta())
	}
	if req.AsOf() != event.Seq(s.h.Len()-1) {
		return fmt.Errorf("requestAsOf %d, want %d", req.AsOf(), s.h.Len()-1)
	}
	ms, err := req.Messages()
	if err != nil {
		return err
	}
	if len(ms) != 3 || ms[0].Role() != vocab.RoleUser || ms[1].Role() != vocab.RoleAssistant || ms[2].Role() != vocab.RoleTool {
		return fmt.Errorf("request carries %d messages, want user/assistant/tool", len(ms))
	}
	if req.Canonical() == "" || req.Digest() == "" {
		return errors.New("request has empty canonical bytes or digest")
	}
	s.req = req
	return nil
}

func (s *lshbState) appendAfterRequest() error {
	for i := 0; i < 3; i++ {
		if _, err := s.h.Append(lshbBG, lshbCtx(fmt.Sprintf("later-%d", i))); err != nil {
			return err
		}
	}
	return nil
}

func (s *lshbState) rebuildFromPrefix() error {
	r, err := s.st.OpenRead(lshbBG, string(s.h.ID()))
	if err != nil {
		return err
	}
	defer r.Close(lshbBG)
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return r.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	s.rebuilt, err = surface.Rebuild(lshbBG, event.Default(), read, string(s.h.ID()), s.req.AsOf())
	return err
}

func (s *lshbState) rebuiltEqualsLive() error {
	if s.rebuilt.Canonical() != s.req.Canonical() {
		return fmt.Errorf("rebuilt canonical bytes differ:\nrebuilt %s\nlive    %s", s.rebuilt.Canonical(), s.req.Canonical())
	}
	if s.rebuilt.Digest() != s.req.Digest() {
		return fmt.Errorf("rebuilt digest %s, live %s", s.rebuilt.Digest(), s.req.Digest())
	}
	return nil
}

func (s *lshbState) builderEventRequestFrom() error {
	d, err := lshbTitleRequest.Draft(lshbTitleAsk{Hint: "short"})
	if err != nil {
		return err
	}
	if s.ask, err = s.h.Append(lshbBG, d); err != nil {
		return err
	}
	s.aux, err = s.h.RequestFrom(s.ask.Seq)
	return err
}

func (s *lshbState) auxMetaNamesEvent(purpose string) error {
	m := s.aux.Meta()
	if !m.Auxiliary() || m.Event != s.ask.Seq || m.EventType != lshbTitleRequest.Name() || m.Purpose != purpose {
		return fmt.Errorf("auxiliary meta %+v, want event %d of type %q with purpose %q", m, s.ask.Seq, lshbTitleRequest.Name(), purpose)
	}
	if m.Session != string(s.h.ID()) {
		return fmt.Errorf("auxiliary meta session %q, want %q", m.Session, s.h.ID())
	}
	ms, err := s.aux.Messages()
	if err != nil {
		return err
	}
	if len(ms) != 4 {
		return fmt.Errorf("auxiliary request carries %d messages, want the 3 of history plus the ask", len(ms))
	}
	if s.aux.Digest() == s.req.Digest() {
		return errors.New("auxiliary request has the loop request's digest")
	}
	return nil
}

func (s *lshbState) auxStable() error {
	if _, err := s.h.Append(lshbBG, lshbCtx("after the ask")); err != nil {
		return err
	}
	again, err := s.h.RequestFrom(s.ask.Seq)
	if err != nil {
		return err
	}
	if again.Digest() != s.aux.Digest() || again.AsOf() != s.ask.Seq {
		return fmt.Errorf("auxiliary request rebuilt from the log differs: digest %s vs %s, asOf %d", again.Digest(), s.aux.Digest(), again.AsOf())
	}
	if _, err := s.h.RequestFrom(event.Seq(s.h.Len())); !errors.Is(err, session.ErrNotCommitted) {
		return fmt.Errorf("RequestFrom past the end returned %v, want session.ErrNotCommitted", err)
	}
	return nil
}

// --- Scenario 3: unbalanced bracket refused ---

func (s *lshbState) sessionWithOpenStep() error {
	s.newService()
	h, err := s.create()
	if err != nil {
		return err
	}
	s.h = h
	if _, err := h.AppendGroup(lshbBG, []event.Draft{lshbTurnStart(1), lshbStepStart(1, 1)}); err != nil {
		return err
	}
	if !h.Open().IsOpen() {
		return errors.New("no bracket is open after turn/start, step/start")
	}
	return nil
}

func (s *lshbState) appendTurnEndEarly() error {
	s.lenBefore = s.h.Len()
	_, s.appendErr = s.h.Append(lshbBG, lshbTurnEnd(1))
	return nil
}

func (s *lshbState) refusedWithBracketError() error {
	if s.appendErr == nil {
		return errors.New("turn/end inside an open step was accepted")
	}
	if !errors.Is(s.appendErr, session.ErrUnbalanced) {
		return fmt.Errorf("turn/end inside an open step returned %v, want session.ErrUnbalanced", s.appendErr)
	}
	return nil
}

func lshbLogUnchanged(h session.Handle, want session.Offset) error {
	if h.Len() != want {
		return fmt.Errorf("a refused append changed the log length: %d, want %d", h.Len(), want)
	}
	evs, err := h.Read(lshbBG, 0, 0)
	if err != nil {
		return err
	}
	if session.Offset(len(evs)) != want {
		return fmt.Errorf("the log reads %d events, want %d", len(evs), want)
	}
	for _, ev := range evs {
		if ev.Type() == "turn/end" {
			return fmt.Errorf("a refused turn/end is in the log at seq %d", ev.Seq())
		}
	}
	return nil
}

func (s *lshbState) logUnchanged() error {
	return lshbLogUnchanged(s.h, s.lenBefore)
}

func (s *lshbState) failurePath() error {
	s.closerErrs = nil
	_, err := s.h.Append(lshbBG, lshbStepEnd(1, 1))
	s.closerErrs = append(s.closerErrs, err)
	_, err = s.h.Append(lshbBG, lshbTurnEnd(1))
	s.closerErrs = append(s.closerErrs, err)
	return nil
}

func (s *lshbState) closersSucceeded() error {
	for i, err := range s.closerErrs {
		if err != nil {
			return fmt.Errorf("closer %d failed: %w", i, err)
		}
	}
	if s.h.Open().IsOpen() {
		return fmt.Errorf("a bracket is still open: %+v", s.h.Open())
	}
	evs, err := s.h.Read(lshbBG, event.Offset(s.lenBefore), 0)
	if err != nil {
		return err
	}
	if len(evs) != 2 || evs[0].Type() != "step/end" || evs[1].Type() != "turn/end" {
		got := make([]string, len(evs))
		for i, ev := range evs {
			got[i] = ev.Type()
		}
		return fmt.Errorf("log tail %v, want [step/end turn/end]", got)
	}
	return nil
}

// bracketProperty: for random nestings (some closed steps, then one open step),
// a premature turn/end is always refused with the log unchanged, and closing the
// step first always lets the turn end.
func (s *lshbState) bracketProperty(n int) error {
	rng := rand.New(rand.NewPCG(0x5e55, 0x13))
	for i := 0; i < n; i++ {
		h, err := s.create()
		if err != nil {
			return err
		}
		err = func() error {
			defer h.Close(lshbBG)
			turns := 1 + rng.IntN(3)
			for turn := 1; turn <= turns; turn++ {
				if _, err := h.Append(lshbBG, lshbTurnStart(turn)); err != nil {
					return fmt.Errorf("case %d turn %d start: %w", i, turn, err)
				}
				closed := rng.IntN(4)
				for step := 1; step <= closed; step++ {
					if _, err := h.AppendGroup(lshbBG, []event.Draft{lshbStepStart(turn, step), lshbStepEnd(turn, step)}); err != nil {
						return fmt.Errorf("case %d turn %d step %d: %w", i, turn, step, err)
					}
				}
				open := closed + 1
				if _, err := h.Append(lshbBG, lshbStepStart(turn, open)); err != nil {
					return fmt.Errorf("case %d turn %d open step: %w", i, turn, err)
				}
				before := h.Len()
				if _, err := h.Append(lshbBG, lshbTurnEnd(turn)); !errors.Is(err, session.ErrUnbalanced) {
					return fmt.Errorf("case %d turn %d: premature turn/end returned %v, want session.ErrUnbalanced", i, turn, err)
				}
				if h.Len() != before {
					return fmt.Errorf("case %d turn %d: refused turn/end changed the log length %d -> %d", i, turn, before, h.Len())
				}
				evs, err := h.Read(lshbBG, 0, 0)
				if err != nil {
					return err
				}
				if session.Offset(len(evs)) != before || evs[len(evs)-1].Type() != "step/start" {
					return fmt.Errorf("case %d turn %d: log after refusal has %d events ending in %q", i, turn, len(evs), evs[len(evs)-1].Type())
				}
				if _, err := h.Append(lshbBG, lshbStepEnd(turn, open)); err != nil {
					return fmt.Errorf("case %d turn %d step/end: %w", i, turn, err)
				}
				if _, err := h.Append(lshbBG, lshbTurnEnd(turn)); err != nil {
					return fmt.Errorf("case %d turn %d turn/end after step/end: %w", i, turn, err)
				}
				if h.Open().IsOpen() {
					return fmt.Errorf("case %d turn %d: bracket still open %+v", i, turn, h.Open())
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

func InitializeScenario_session_core_contracts_live_session_history_and_brackets(ctx *godog.ScenarioContext) {
	s := &lshbState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		s.reset()
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.reset()
		return c, nil
	})

	ctx.Step(`^a live session on memstore and (\d+) subscribers$`, s.sessionWithSubscribers)
	ctx.Step(`^(\d+) events are appended from (\d+) goroutines$`, s.appendConcurrently)
	ctx.Step(`^every append got a distinct dense sequence number with non-decreasing times$`, s.receiptsDense)
	ctx.Step(`^each subscriber receives every event exactly once in sequence order$`, s.subscribersGotAllOnceInOrder)

	ctx.Step(`^a live session on memstore with a request header for route "([^"]*)" and tool "([^"]*)"$`, s.sessionWithHeader)
	ctx.Step(`^a hand-written sequence of a user message, an assistant tool call "([^"]*)" and its tool result$`, s.handWrittenSequence)
	ctx.Step(`^NextRequest is built$`, s.buildNextRequest)
	ctx.Step(`^more events are appended after the request$`, s.appendAfterRequest)
	ctx.Step(`^surface\.Rebuild is called over the log prefix ending at the request's requestAsOf$`, s.rebuildFromPrefix)
	ctx.Step(`^the rebuilt canonical bytes and Digest equal the live request's$`, s.rebuiltEqualsLive)
	ctx.Step(`^a builder event is committed and RequestFrom is called on its seq$`, s.builderEventRequestFrom)
	ctx.Step(`^the auxiliary request's Meta names that event and its purpose "([^"]*)"$`, s.auxMetaNamesEvent)
	ctx.Step(`^RequestFrom on the same seq after later appends yields the same digest$`, s.auxStable)

	ctx.Step(`^a live session on memstore with an open step/start inside turn/start$`, s.sessionWithOpenStep)
	ctx.Step(`^turn/end is appended before step/end$`, s.appendTurnEndEarly)
	ctx.Step(`^the append is refused with a bracket error$`, s.refusedWithBracketError)
	ctx.Step(`^the log is unchanged$`, s.logUnchanged)
	ctx.Step(`^the failure path appends step/end then turn/end$`, s.failurePath)
	ctx.Step(`^both appends succeed and no bracket is left open$`, s.closersSucceeded)
	ctx.Step(`^for (\d+) random bracket nestings every premature turn/end is refused and the log is unchanged$`, s.bracketProperty)
}

func TestE2E_session_core_contracts_live_session_history_and_brackets(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_session_core_contracts_live_session_history_and_brackets,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"session_core_contracts_live_session_history_and_brackets.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
