package session_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// A prepared session is invisible and leased: Discard releases and deletes it,
// so neither Publish nor a second Discard brings it back; a published one cannot
// be published again.
func TestPreparedSessionDiscardAndPublishOnce(t *testing.T) {
	e := newEnv(t, session.Config{})
	p := must(e.svc.Prepare(bg, nil, session.CreateOptions{Header: session.Header{ID: "prep"}}))
	if p.ID() != "prep" {
		t.Fatalf("ID = %q", p.ID())
	}
	if _, ok := e.svc.Get("prep"); ok {
		t.Fatal("a prepared session is visible before Publish")
	}
	if err := p.Discard(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Stat(bg, "prep"); !errors.Is(err, store.ErrNotFound) && !errors.Is(err, store.ErrDeleted) {
		t.Fatalf("a discarded session is still stored: %v", err)
	}
	if err := p.Discard(bg); err != nil {
		t.Fatalf("a second Discard = %v", err)
	}
	if _, err := p.Publish(bg); !errors.Is(err, session.ErrClosed) {
		t.Fatalf("Publish after Discard = %v, want session/closed", err)
	}

	q := must(e.svc.Prepare(bg, nil, session.CreateOptions{}))
	if !strings.HasPrefix(string(q.ID()), "ses_") {
		t.Fatalf("a minted id = %q", q.ID())
	}
	h := must(q.Publish(bg))
	t.Cleanup(func() { _ = h.Close(bg) })
	if _, err := q.Publish(bg); !errors.Is(err, session.ErrClosed) {
		t.Fatalf("a second Publish = %v, want session/closed", err)
	}
	if err := q.Discard(bg); err != nil {
		t.Fatalf("Discard after Publish = %v, want a no-op", err)
	}
	if got, ok := e.svc.Get(q.ID()); !ok || got.ID() != h.ID() {
		t.Fatal("Discard after Publish removed the published session")
	}
}

// A closed session refuses reads, checkpoints, flushes and request rebuilds with
// session/closed naming it, and still reports its identity.
func TestClosedSessionRefusesUse(t *testing.T) {
	e := newEnv(t, session.Config{})
	key := must(e.tab.New(e.root, "owner")).Key()
	h := e.create(t, key)
	must(h.Append(bg, ctxDraft(t, "v")))
	if h.Scope() != key || fmt.Sprint(h) != fmt.Sprintf("session %q", h.ID()) {
		t.Fatalf("scope %v, string %q", h.Scope(), fmt.Sprint(h))
	}
	if at, err := h.Flush(bg); err != nil || at != 1 {
		t.Fatalf("Flush = %d, %v", at, err)
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	var ce *session.ClosedError
	if _, err := h.Read(bg, 0, 0); !errors.As(err, &ce) || ce.ID != h.ID() || !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Read after Close = %v", err)
	}
	if _, err := h.(session.Checkpointed).Checkpoints(bg); !errors.Is(err, session.ErrClosed) {
		t.Fatalf("Checkpoints after Close = %v", err)
	}
	if at, err := h.Flush(bg); !errors.Is(err, session.ErrClosed) || at != 1 {
		t.Fatalf("Flush after Close = %d, %v; want the durable 1 and session/closed", at, err)
	}
	if _, err := h.RequestFrom(0); !errors.Is(err, session.ErrClosed) {
		t.Fatalf("RequestFrom after Close = %v", err)
	}
	if msg := ce.Error(); msg != fmt.Sprintf("%s: session %q is closed", session.CodeClosed, h.ID()) {
		t.Fatalf("ClosedError = %q", msg)
	}
}

// The append path refuses an empty group, an event above the size limit and a
// type that is not a plugin record's, and appends a plugin record as an
// ignorable event of its own type.
func TestAppendRefusalsAndPluginRecords(t *testing.T) {
	e := newEnv(t, session.Config{MaxEventBytes: 256})
	h := e.create(t, scope.Key{})
	if _, err := h.AppendGroup(bg, nil); !errors.Is(err, session.ErrEmptyGroup) {
		t.Fatalf("an empty group = %v", err)
	}
	if _, err := h.Append(bg, ctxDraft(t, strings.Repeat("x", 300))); !errors.Is(err, session.ErrTooLarge) {
		t.Fatalf("an event above the limit = %v", err)
	}
	for _, typ := range []string{"ext/title", "plugin:Bad/x", "plugin:owner"} {
		if _, err := h.AppendPluginRecord(bg, typ, json.RawMessage(`{}`)); !errors.Is(err, event.ErrInvalidEvent) {
			t.Errorf("AppendPluginRecord(%q) = %v", typ, err)
		}
	}
	if _, err := h.AppendPluginRecord(bg, "plugin:owner/rec", json.RawMessage(`{not json`)); !errors.Is(err, event.ErrInvalidEvent) {
		t.Fatalf("a plugin record that is not JSON = %v", err)
	}
	if h.Len() != 0 {
		t.Fatalf("refused appends changed the log: len %d", h.Len())
	}
	r := must(h.AppendPluginRecord(bg, "plugin:owner/rec", json.RawMessage(`{"b":1, "a":2}`)))
	evs := must(h.Read(bg, r.Seq, 0))
	if len(evs) != 1 || evs[0].Type() != "plugin:owner/rec" || evs[0].Raw() != `{"a":2,"b":1}` {
		t.Fatalf("the plugin record: %v %q", types(evs), evs[0].Raw())
	}
	if rs, err := h.CloseOpen(bg, surface.CauseFailed); err != nil || rs != nil {
		t.Fatalf("CloseOpen with nothing open = %v, %v", rs, err)
	}
}

// A read-only open of a session left inside a turn serves the closers it folds
// in memory past the stored end: from a later offset, within a limit, and with
// the folds and header of the stored session.
func TestReadOnlyOpenServesItsClosers(t *testing.T) {
	e := newEnv(t, session.Config{})
	h := e.create(t, scope.Key{})
	id := h.ID()
	search := vocab.ToolSchema{Name: "search", Description: "search", Parameters: json.RawMessage(`{"type":"object"}`)}
	must(h.AppendGroup(bg, []event.Draft{turnStart(t, 1), stepStart(t, 1, 1), headerDraft(t, "r/a", search), userDraft(t, "u", "go")}))
	must(h.Append(bg, assistantDraft(t, event.AssistantMessagePayload{Message: msg(t, "a", vocab.RoleAssistant, callBlock("c1", "search"))})))
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}

	rs, release := must2(e.svc.OpenRead(bg, id))
	defer release()
	if rs.ID() != id || rs.Header().ID != string(id) || rs.Len() != 8 || rs.Durable() != 5 || rs.Open().IsOpen() {
		t.Fatalf("read-open: id %q len %d durable %d open %+v", rs.ID(), rs.Len(), rs.Durable(), rs.Open())
	}
	if hdr, ok := rs.RequestHeader(); !ok || hdr.Route != "r/a" {
		t.Fatalf("request header = %+v %v", hdr, ok)
	}
	if hist := rs.History(); hist.Len() == 0 {
		t.Fatal("the read-open has no history")
	}
	if th := rs.ToolHistory(); !th.HasBase || len(th.Active()) != 1 || th.Active()[0].Name != "search" {
		t.Fatalf("tool history = %+v", th)
	}
	if rs.Scope() != (scope.Key{}) {
		t.Fatalf("a read-open's scope = %v", rs.Scope())
	}
	if cps, err := rs.(session.Checkpointed).Checkpoints(bg); err != nil {
		t.Fatal(err)
	} else if slices.ContainsFunc(cps, func(c store.Checkpoint) bool { return c.Seq >= 5 }) {
		t.Fatalf("a checkpoint past the stored end: %+v", cps)
	}
	all := must(rs.Read(bg, 0, 0))
	if got := types(all[5:]); !slices.Equal(got, []string{"tool/result", "step/end", "turn/end"}) || len(all) != 8 {
		t.Fatalf("closers served = %v of %d", got, len(all))
	}
	if got := types(must(rs.Read(bg, 6, 0))); !slices.Equal(got, []string{"step/end", "turn/end"}) {
		t.Fatalf("from 6 = %v", got)
	}
	if got := must(rs.Read(bg, 3, 3)); len(got) != 3 || got[2].Type() != "tool/result" {
		t.Fatalf("a limit of 3 from 3 = %v", types(got))
	}
	if sum := must(e.st.Stat(bg, string(id))); sum.NextSeq != 5 {
		t.Fatalf("the read-open wrote: next %d", sum.NextSeq)
	}
}
