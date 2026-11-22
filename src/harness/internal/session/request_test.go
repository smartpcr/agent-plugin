package session_test

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

type titleAsk struct {
	Hint string `json:"hint"`
}

// titleRequest is a test plugin type that describes an auxiliary request.
var titleRequest = func() *event.Type[titleAsk] {
	typ := event.MustDefine(event.Default(), event.Spec[titleAsk]{Name: "ext/title-request", Version: 1})
	if err := surface.DeclareBuilder(event.Default(), typ.Name(), func(ev event.Event, at surface.View, b *surface.AuxBuilder) error {
		var p titleAsk
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

func storeRead(t *testing.T, st store.Store, id session.ID) surface.ReadFunc {
	t.Helper()
	r := must(st.OpenRead(bg, string(id)))
	t.Cleanup(func() { _ = r.Close(bg) })
	return func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return r.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
}

// DH-T-SESSION-12 (TC-05): a loop request rebuilt from the log prefix ending at
// its requestAsOf has the same canonical bytes and digest; an auxiliary request
// names its builder event, and is the same whether built live or rebuilt.
//
//dsh:test DH-T-SESSION-12
func TestRequestRebuild(t *testing.T) {
	e := newEnv(t, session.Config{})
	h := e.create(t, scope.Key{})
	if _, err := h.NextRequest(); !errors.Is(err, session.ErrNoHeader) {
		t.Fatalf("a request with no header: %v", err)
	}
	search := vocab.ToolSchema{Name: "search", Description: "search", Parameters: json.RawMessage(`{"type":"object"}`)}
	must(h.AppendGroup(bg, []event.Draft{turnStart(t, 1), stepStart(t, 1, 1), headerDraft(t, "r/a", search), userDraft(t, "u1", "find x")}))
	must(h.Append(bg, assistantDraft(t, event.AssistantMessagePayload{Message: msg(t, "a1", vocab.RoleAssistant, callBlock("c1", "search"))})))
	if _, err := h.NextRequest(); err == nil {
		t.Fatal("a request with a pending call was built")
	}
	must(h.Append(bg, resultDraft(t, "r1", "c1", "x is here")))

	req := must(h.NextRequest())
	if req.IsZero() || req.Meta().Auxiliary() || req.AsOf() != h.Len()-1 || req.Route() != "r/a" {
		t.Fatalf("request: zero %v meta %+v asOf %d route %q", req.IsZero(), req.Meta(), req.AsOf(), req.Route())
	}
	ms := must(req.Messages())
	if len(ms) != 3 || ms[0].Role() != vocab.RoleUser || ms[1].Role() != vocab.RoleAssistant || ms[2].Role() != vocab.RoleTool {
		t.Fatalf("messages: %d", len(ms))
	}
	must(h.Append(bg, ctxDraft(t, "later")))
	re := must(surface.Rebuild(bg, event.Default(), storeRead(t, e.st, h.ID()), string(h.ID()), req.AsOf()))
	if re.Canonical() != req.Canonical() || re.Digest() != req.Digest() {
		t.Fatalf("rebuilt request differs:\n%s\n%s", re.Canonical(), req.Canonical())
	}

	ask := must(h.Append(bg, must(titleRequest.Draft(titleAsk{Hint: "short"}))))
	live := must(h.RequestFrom(ask.Seq))
	if m := live.Meta(); !m.Auxiliary() || m.Event != ask.Seq || m.EventType != titleRequest.Name() || m.Purpose != "title" {
		t.Fatalf("auxiliary meta %+v", m)
	}
	if ms := must(live.Messages()); len(ms) != 4 {
		t.Fatalf("auxiliary messages: %d", len(ms))
	}
	must(h.Append(bg, ctxDraft(t, "after the ask")))
	again := must(h.RequestFrom(ask.Seq))
	if again.Digest() != live.Digest() || again.AsOf() != ask.Seq {
		t.Fatal("an auxiliary request rebuilt from the log differs from the live one")
	}
	if _, err := h.RequestFrom(h.Len()); !errors.Is(err, session.ErrNotCommitted) {
		t.Fatalf("RequestFrom past the end: %v", err)
	}
	if _, err := h.RequestFrom(3); !errors.Is(err, session.ErrNoBuilder) {
		t.Fatalf("RequestFrom a user message: %v", err)
	}
}

// DH-T-SESSION-13 (scenario): turn/end before step/end is refused and the log is
// unchanged; the failure path appends step/end, then turn/end; a turn/end of kind
// interrupted is the core's alone.
//
//dsh:test DH-T-SESSION-13
func TestBracketsOnAppend(t *testing.T) {
	e := newEnv(t, session.Config{})
	h := e.create(t, scope.Key{})
	must(h.AppendGroup(bg, []event.Draft{turnStart(t, 1), stepStart(t, 1, 1)}))
	if _, err := h.Append(bg, turnEnd(t, 1)); !errors.Is(err, session.ErrUnbalanced) {
		t.Fatalf("turn/end inside a step: %v", err)
	}
	if h.Len() != 2 || len(must(h.Read(bg, 0, 0))) != 2 {
		t.Fatalf("a refused append changed the log: len %d", h.Len())
	}
	interrupted := must(event.TurnEndType.Draft(event.TurnEnd{Turn: 1, Reason: event.Reason{Kind: event.ReasonInterrupted}}))
	must(h.Append(bg, stepEnd(t, 1, 1)))
	if _, err := h.Append(bg, interrupted); !errors.Is(err, session.ErrUnbalanced) {
		t.Fatalf("a caller wrote an interrupted turn/end: %v", err)
	}
	must(h.Append(bg, stepStart(t, 1, 2)))
	rs := must(h.CloseOpen(bg, surface.CauseFailed))
	evs := must(h.Read(bg, rs[0].Seq, 0))
	if len(evs) != 2 || evs[0].Type() != "step/end" || evs[1].Type() != "turn/end" || h.Open().IsOpen() {
		t.Fatalf("closers: %v", types(evs))
	}
	var end event.TurnEnd
	if err := json.Unmarshal([]byte(evs[1].Raw()), &end); err != nil || end.Reason.Kind != event.ReasonInterrupted {
		t.Fatalf("closing turn/end %s", evs[1].Raw())
	}
}

func types(evs []event.Event) []string {
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type()
	}
	return out
}

// DH-T-SESSION-13 (open): opening a session left inside a turn with a pending
// call appends the closers and an end seed once; a read-only open shows them
// and writes nothing; a second open appends nothing.
//
//dsh:test DH-T-SESSION-13
func TestOpenClosesBrackets(t *testing.T) {
	e := newEnv(t, session.Config{})
	h := e.create(t, scope.Key{})
	id := h.ID()
	must(h.AppendGroup(bg, []event.Draft{turnStart(t, 1), stepStart(t, 1, 1), userDraft(t, "u", "go")}))
	must(h.Append(bg, assistantDraft(t, event.AssistantMessagePayload{Message: msg(t, "a", vocab.RoleAssistant, callBlock("c1", "search"))})))
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}

	rs, release := must2(e.svc.OpenRead(bg, id))
	if rs.Open().IsOpen() || rs.Len() != 7 {
		t.Fatalf("a read-only open: open %+v len %d", rs.Open(), rs.Len())
	}
	release()
	if sum := must(e.st.Stat(bg, string(id))); sum.NextSeq != 4 {
		t.Fatalf("a read-only open wrote: next %d", sum.NextSeq)
	}

	h2, rep := must2(e.svc.Open(bg, nil, id, session.OpenOptions{}))
	if !rep.Interrupted || len(rep.Closers) != 4 || h2.Open().IsOpen() {
		t.Fatalf("open report %+v", rep)
	}
	evs := must(h2.Read(bg, 4, 0))
	if got := types(evs); !slices.Equal(got, []string{"tool/result", "step/end", "turn/end", event.EndSeed.Name()}) {
		t.Fatalf("closers %v", got)
	}
	if err := h2.Close(bg); err != nil {
		t.Fatal(err)
	}
	h3, rep := must2(e.svc.Open(bg, nil, id, session.OpenOptions{}))
	defer h3.Close(bg)
	if rep.Interrupted || len(rep.Closers) != 0 || h3.Len() != 8 {
		t.Fatalf("second open: %+v len %d", rep, h3.Len())
	}
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

// DH-T-STORE-9 (publication): the closers Open appends are flushed before the
// session is published; when that flush fails, Open fails, nothing is published
// and the lease is released, so a later Open succeeds.
func TestOpenFlushesClosersBeforePublishing(t *testing.T) {
	var fs *faultStore
	e := newEnv(t, session.Config{}, func(s store.Store) store.Store { fs = &faultStore{Store: s}; return fs })
	h := e.create(t, scope.Key{})
	id := h.ID()
	must(h.AppendGroup(bg, []event.Draft{turnStart(t, 1), stepStart(t, 1, 1), userDraft(t, "u", "go")}))
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	fs.setMode("flush-fails")
	if h2, _, err := e.svc.Open(bg, nil, id, session.OpenOptions{}); err == nil {
		h2.Close(bg)
		t.Fatal("Open published a session whose closers did not flush")
	}
	if _, ok := e.svc.Get(id); ok {
		t.Fatal("a session whose closers did not flush is live")
	}
	if sum := must(e.st.Stat(bg, string(id))); sum.Holder != nil {
		t.Fatalf("the failed Open kept the lease: %+v", sum.Holder)
	}
	fs.setMode("")
	h3, _ := must2(e.svc.Open(bg, nil, id, session.OpenOptions{}))
	defer h3.Close(bg)
	if h3.Open().IsOpen() {
		t.Fatalf("brackets after the retried Open: %+v", h3.Open())
	}
}

// DH-T-SESSION-14: an open restored from the core checkpoint has the same folds
// as a full replay.
//
//dsh:test DH-T-SESSION-14
func TestOpenFromCheckpoint(t *testing.T) {
	e := newEnv(t, session.Config{CheckpointEvery: 3})
	h := e.create(t, scope.Key{})
	id := h.ID()
	must(h.AppendGroup(bg, []event.Draft{headerDraft(t, "r/a"), turnStart(t, 1), userDraft(t, "u1", "one")}))
	for i := 0; i < 4; i++ {
		must(h.Append(bg, ctxDraft(t, "v")))
	}
	must(h.Append(bg, assistantDraft(t, event.AssistantMessagePayload{Message: msg(t, "a1", vocab.RoleAssistant, vocab.TextBlock("done"))})))
	must(h.Append(bg, turnEnd(t, 1)))
	want := must(h.NextRequest())
	wantJSON := must(json.Marshal(h.History()))
	wantTools := must(json.Marshal(h.ToolHistory()))
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}

	h2, rep := must2(e.svc.Open(bg, nil, id, session.OpenOptions{}))
	defer h2.Close(bg)
	if !rep.FromCheckpoint || rep.Interrupted || h2.Len() != 9 {
		t.Fatalf("open report %+v len %d", rep, h2.Len())
	}
	if got := must(json.Marshal(h2.History())); string(got) != string(wantJSON) {
		t.Fatalf("history from the checkpoint:\n%s\nwant\n%s", got, wantJSON)
	}
	if got := must(json.Marshal(h2.ToolHistory())); string(got) != string(wantTools) {
		t.Fatalf("tool history from the checkpoint:\n%s\nwant\n%s", got, wantTools)
	}
	if got := must(h2.NextRequest()); got.Canonical() != want.Canonical() {
		t.Fatalf("the request from the checkpoint differs:\n%s\n%s", got.Canonical(), want.Canonical())
	}
	if h2.Open().IsOpen() || h2.Open().LastTurn != 1 {
		t.Fatalf("brackets from the checkpoint %+v", h2.Open())
	}
}

// DH-T-SESSION-19: a store read that breaks a clause (stale tail, a wrong first
// seq, a gap, a record this session did not commit) closes the session with
// session/broken-store, and nothing of the bad page is delivered.
//
//dsh:test DH-T-SESSION-19
func TestBrokenStoreRead(t *testing.T) {
	for _, mode := range []string{"stale", "first", "gap", "foreign"} {
		t.Run(mode, func(t *testing.T) {
			var fs *faultStore
			e := newEnv(t, session.Config{}, func(s store.Store) store.Store { fs = &faultStore{Store: s}; return fs })
			rec := listen(t, e.root)
			h := e.create(t, scope.Key{})
			fs.setMode(mode)
			must(h.AppendGroup(bg, []event.Draft{ctxDraft(t, "a"), ctxDraft(t, "b"), ctxDraft(t, "c")}))
			closed := rec.waitClosed(t, h.ID())
			if !errors.Is(closed.Cause, session.ErrBrokenStore) {
				t.Fatalf("closed with %v", closed.Cause)
			}
			if n := len(rec.got(h.ID())); n != 0 {
				t.Fatalf("%d events of a bad page delivered", n)
			}
			if _, err := h.Append(bg, ctxDraft(t, "c")); !errors.Is(err, session.ErrClosed) {
				t.Fatalf("append after a broken store: %v", err)
			}
		})
	}
}

// DH-T-SESSION-7: a vetoed Created publishes nothing; Close delivers every event
// before Closed and is idempotent; Append after close is session/closed; a lost
// lease closes the session with that cause.
//
//dsh:test DH-T-SESSION-7
func TestLifecycle(t *testing.T) {
	e := newEnv(t, session.Config{})
	rec := listen(t, e.root)
	veto := true
	must(session.Created.On(e.root, func(*cordis.Context, session.CreatedArgs) error {
		if veto {
			return errors.New("not now")
		}
		return nil
	}))
	if _, err := e.svc.Create(bg, nil, session.CreateOptions{}); err == nil {
		t.Fatal("a vetoed create succeeded")
	}
	if len(e.svc.Live()) != 0 {
		t.Fatal("a vetoed session is live")
	}
	veto = false

	h := e.create(t, scope.Key{})
	for i := 0; i < 20; i++ {
		must(h.Append(bg, ctxDraft(t, "v")))
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(bg); err != nil {
		t.Fatalf("a second close: %v", err)
	}
	if c := rec.waitClosed(t, h.ID()); c.Cause != nil || rec.atClose[h.ID()] != 20 {
		t.Fatalf("closed with %v after %d events", c.Cause, rec.atClose[h.ID()])
	}
	if _, err := h.Append(bg, ctxDraft(t, "late")); !errors.Is(err, session.ErrClosed) || !errors.Is(err, store.ErrClosed) {
		t.Fatalf("append after close: %v", err)
	}
	if _, ok := e.svc.Get(h.ID()); ok {
		t.Fatal("a closed session is still live")
	}

	h2 := e.create(t, scope.Key{})
	must(h2.Append(bg, ctxDraft(t, "v")))
	w := must(e.st.OpenWrite(bg, string(h2.ID()), store.OpenOptions{Takeover: store.Force}))
	defer w.Close(bg)
	if _, err := h2.Append(bg, ctxDraft(t, "v")); err == nil {
		t.Fatal("an append after the lease was taken succeeded")
	}
	if c := rec.waitClosed(t, h2.ID()); !errors.Is(c.Cause, store.ErrLeaseLost) {
		t.Fatalf("closed with %v, want lease lost", c.Cause)
	}
}

// DH-T-SESSION-8: Read honours from and limit, and returns cold parts only when
// asked.
//
//dsh:test DH-T-SESSION-8
func TestRead(t *testing.T) {
	e := newEnv(t, session.Config{})
	h := e.create(t, scope.Key{})
	for i := 0; i < 5; i++ {
		must(h.Append(bg, ctxDraft(t, "v")))
	}
	a := must(h.Append(bg, assistantDraft(t, event.AssistantMessagePayload{
		Message: msg(t, "a", vocab.RoleAssistant, vocab.TextBlock("hi")), Stream: json.RawMessage(`[{"delta":"hi"}]`)})))
	evs := must(h.Read(bg, 1, 2))
	if len(evs) != 2 || evs[0].Seq() != 1 || evs[1].Seq() != 2 {
		t.Fatalf("Read(1, 2): %v", types(evs))
	}
	if evs := must(h.Read(bg, 10, 0)); len(evs) != 0 {
		t.Fatal("a read past the end returned events")
	}
	if _, err := h.Read(bg, 0, -1); err == nil {
		t.Fatal("a negative limit was accepted")
	}
	if ev := must(h.Read(bg, a.Seq, 1))[0]; ev.ColdRaw() != "" {
		t.Fatal("a plain read returned the cold part")
	}
	if ev := must(h.Read(bg, a.Seq, 1, session.WithCold()))[0]; ev.ColdRaw() != `[{"delta":"hi"}]` {
		t.Fatalf("a cold read: %q", ev.ColdRaw())
	}
}

// DH-T-SESSION-5: what a caller gets back cannot change what the session holds.
//
//dsh:test DH-T-SESSION-5
func TestImmutableResults(t *testing.T) {
	e := newEnv(t, session.Config{})
	h := e.create(t, scope.Key{})
	search := vocab.ToolSchema{Name: "search", Description: "search", Parameters: json.RawMessage(`{"type":"object"}`)}
	must(h.AppendGroup(bg, []event.Draft{headerDraft(t, "r/a", search), userDraft(t, "u1", "one"), userDraft(t, "u2", "two")}))
	before := must(json.Marshal(h.History()))

	seqs := h.History().Seqs()
	seqs[0] = 99
	req := must(h.NextRequest())
	tools := req.Tools()
	tools[0].Name = "mutated"
	tools[0].Parameters[2] = 'X'
	hdr, _ := h.RequestHeader()
	hdr.Tools[0].Name = "mutated"
	evs := must(h.Read(bg, 1, 0))
	evs[0] = event.Event{}

	if after := must(json.Marshal(h.History())); string(after) != string(before) {
		t.Fatal("mutating returned seqs changed the history")
	}
	if again := must(h.NextRequest()); again.Digest() != req.Digest() || again.Tools()[0].Name != "search" {
		t.Fatal("mutating returned tools changed the request")
	}
	if hdr2, _ := h.RequestHeader(); hdr2.Tools[0].Name != "search" || string(hdr2.Tools[0].Parameters) != `{"type":"object"}` {
		t.Fatal("mutating the returned header changed the session")
	}
	if evs := must(h.Read(bg, 1, 0)); len(evs) != 2 || evs[0].Type() != event.UserMessage.Name() {
		t.Fatal("mutating a read changed the log")
	}
}
