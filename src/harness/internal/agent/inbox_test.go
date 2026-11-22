package agent_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

func inboxSession(t *testing.T, st store.Store, id string) (session.Service, session.Handle) {
	t.Helper()
	e := newEnv(t)
	svc := session.NewService(e.root, st, session.Config{})
	h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}}))
	return svc, h
}

func count(t *testing.T, h session.Handle, typ string) int {
	t.Helper()
	n := 0
	for _, ty := range types(t, h, 0) {
		if ty == typ {
			n++
		}
	}
	return n
}

func userText(id, text string) agent.InboxMessage {
	return agent.InboxMessage{ID: id, Source: agent.SourceUser, By: "alice", Text: text}
}

// TC-10b: on the SQLite store, an inbox item is in the database, visible from a
// second connection, as soon as Send returns.
func TestInboxSendIsDurableBeforeItReturns(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "sessions.db")
	st := must(sqlite.Open(ctx, path))
	t.Cleanup(func() { st.Close(ctx) })
	_, h := inboxSession(t, st, "s-inbox")
	t.Cleanup(func() { h.Close(ctx) })
	in := must(agent.OpenInbox(ctx, h))
	rec := must(in.Send(ctx, agent.ListNextTurn, userText("m1", "hello")))

	other := must(sqlite.Open(ctx, path))
	defer other.Close(ctx)
	r := must(other.OpenRead(ctx, "s-inbox"))
	defer r.Close(ctx)
	recs := must(r.Read(ctx, 0, 0, store.ReadOptions{}))
	i := slices.IndexFunc(recs, func(x event.Record) bool { return x.Type == agent.SplicedType.Name() })
	if i < 0 || recs[i].Seq != rec.Seq || !strings.Contains(recs[i].Payload, `"m1"`) {
		t.Fatalf("second connection saw %+v, want the splice of m1 at seq %d", recs, rec.Seq)
	}
}

// DH-T-AGENT-6, TC-10a: a driver that crashes after claiming an item leaves it
// claimed; the resumed agent does not run it again and the log has one turn for it.
func TestClaimedItemRunsAtMostOnceAcrossACrash(t *testing.T) {
	st := memstore.New()
	svc, h := inboxSession(t, st, "s-crash")
	in := must(agent.OpenInbox(bg, h))
	must(in.Send(bg, agent.ListNextTurn, userText("m1", "do it")))
	if m, ok, err := in.ClaimTurn(bg, 1); err != nil || !ok || m.ID != "m1" {
		t.Fatalf("claim = %+v %v %v", m, ok, err)
	}
	// The crash: the driver stops before turn/end.
	must(0, h.Close(bg))

	e := newEnv(t)
	h2, report := must2(svc.Open(bg, e.root, "s-crash", session.OpenOptions{}))
	t.Cleanup(func() { h2.Close(bg) })
	if !report.Interrupted {
		t.Fatal("resume did not close the open turn")
	}
	in2 := must(agent.OpenInbox(bg, h2))
	if p := in2.Pending(agent.ListNextTurn); len(p) != 0 {
		t.Fatalf("pending after resume %+v", p)
	}
	if _, ok, err := in2.ClaimTurn(bg, 2); ok || err != nil {
		t.Fatalf("resumed agent claimed again: %v %v", ok, err)
	}
	if n := count(t, h2, event.TurnStartType.Name()); n != 1 {
		t.Fatalf("%d turns, want 1", n)
	}
	// A retry of the lost send is refused with the first receipt, after the resume.
	first, _ := in2.Seen("m1")
	if _, err := in2.Send(bg, agent.ListNextTurn, userText("m1", "do it")); !errors.Is(err, agent.ErrDuplicateMessage) {
		t.Fatalf("retry = %v", err)
	} else if d := new(agent.DuplicateError); !errors.As(err, &d) || d.First != first {
		t.Fatalf("retry receipt %+v, want %+v", d.First, first)
	}
}

func must2[A, B any](a A, b B, err error) (A, B) {
	if err != nil {
		panic(err)
	}
	return a, b
}

// DH-T-AGENT-6: an injection while idle logs a splice and opens no turn; it is
// claimed at the next step boundary, with its user message, in one group.
func TestIdleInjectionOpensNoTurnAndIsClaimedAtTheNextStep(t *testing.T) {
	_, h := inboxSession(t, memstore.New(), "s-idle")
	t.Cleanup(func() { h.Close(bg) })
	in := must(agent.OpenInbox(bg, h))
	must(in.Send(bg, agent.ListNextStep, userText("s1", "also this")))
	if got := strings.Join(types(t, h, 0), ","); got != "agent/inbox/spliced" {
		t.Fatalf("log %s", got)
	}
	if _, ok, _ := in.ClaimTurn(bg, 1); ok {
		t.Fatal("a next-step item opened a turn")
	}
	if h.Len() != 1 {
		t.Fatal("a wake with nothing pending appended")
	}
	must(h.Append(bg, must(event.TurnStartType.Draft(event.TurnStart{Turn: 1}))))
	got := must(in.ClaimStep(bg, 1))
	if len(got) != 1 || got[0].ID != "s1" || len(in.Pending(agent.ListNextStep)) != 0 {
		t.Fatalf("claimed %+v", got)
	}
	if got := strings.Join(types(t, h, 0), ","); got != "agent/inbox/spliced,turn/start,agent/inbox/spliced,user/message" {
		t.Fatalf("log %s", got)
	}
	if must(in.ClaimStep(bg, 1)) != nil {
		t.Fatal("claimed twice")
	}
}

// DH-T-AGENT-5: two queued follow-ups give two turns; a discarded item opens none.
func TestQueuedFollowUpsAreOneTurnEach(t *testing.T) {
	_, h := inboxSession(t, memstore.New(), "s-two")
	t.Cleanup(func() { h.Close(bg) })
	in := must(agent.OpenInbox(bg, h))
	for _, id := range []string{"a", "b", "c"} {
		must(in.Send(bg, agent.ListNextTurn, userText(id, id)))
	}
	if ok := must(in.Discard(bg, agent.ListNextTurn, "b", "cancelled")); !ok {
		t.Fatal("discard b")
	}
	var ran []string
	for turn := 1; ; turn++ {
		m, ok, err := in.ClaimTurn(bg, turn)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			break
		}
		ran = append(ran, m.ID)
		must(h.Append(bg, must(event.TurnEndType.Draft(event.TurnEnd{Turn: turn, Reason: event.Reason{Kind: event.ReasonCompleted}}))))
		must(h.Flush(bg))
	}
	if !slices.Equal(ran, []string{"a", "c"}) || count(t, h, event.TurnStartType.Name()) != 2 {
		t.Fatalf("ran %v", ran)
	}
	// The fold of the log equals the live lists.
	again := must(agent.OpenInbox(bg, h))
	if len(again.Pending(agent.ListNextTurn)) != 0 {
		t.Fatal("refold disagrees")
	}
	if _, err := in.Send(bg, agent.ListNextTurn, userText("b", "b")); !errors.Is(err, agent.ErrDuplicateMessage) {
		t.Fatalf("a discarded id was admitted again: %v", err)
	}
}

// DH-T-AGENT-4: many goroutines send; receipts follow splice order, each id is
// admitted once, and the fold equals the live list.
func TestConcurrentSendsAreOrderedAndUnique(t *testing.T) {
	_, h := inboxSession(t, memstore.New(), "s-many")
	t.Cleanup(func() { h.Close(bg) })
	in := must(agent.OpenInbox(bg, h))
	var wg sync.WaitGroup
	var mu sync.Mutex
	var receipts []agent.Receipt
	dups := 0
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 10 {
				// Every id is sent twice, by two goroutines.
				r, err := in.Send(bg, agent.ListNextTurn, userText(fmt.Sprintf("m%d", (g/2)*10+i), "x"))
				mu.Lock()
				if err != nil {
					dups++
				} else {
					receipts = append(receipts, r)
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(receipts) != 40 || dups != 40 {
		t.Fatalf("%d admitted, %d refused", len(receipts), dups)
	}
	slices.SortFunc(receipts, func(a, b agent.Receipt) int { return int(a.Seq) - int(b.Seq) })
	live := in.Pending(agent.ListNextTurn)
	for i, r := range receipts {
		if live[i].ID != r.MessageID {
			t.Fatalf("position %d holds %s, receipt order says %s", i, live[i].ID, r.MessageID)
		}
	}
	if again := must(agent.OpenInbox(bg, h)); !slices.Equal(again.Pending(agent.ListNextTurn), live) {
		t.Fatal("the fold differs from the live list")
	}
}
