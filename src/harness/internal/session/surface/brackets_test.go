package surface_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// Every bracket rule refuses the event that would break it, and a refused event
// leaves the folds exactly as they were.
func TestBracketRefusalsLeaveTheFoldsUnchanged(t *testing.T) {
	type draft func() (event.Draft, error)
	cases := []struct {
		name  string
		setup []draft
		bad   draft
	}{
		{"turn end with no turn open", nil, func() (event.Draft, error) { return turnEnd(1, event.ReasonCompleted) }},
		{"turn end of another turn", []draft{func() (event.Draft, error) { return turnStart(1) }},
			func() (event.Draft, error) { return turnEnd(2, event.ReasonCompleted) }},
		{"step of another turn", []draft{func() (event.Draft, error) { return turnStart(1) }},
			func() (event.Draft, error) { return stepStart(2, 1) }},
		{"step inside a step", []draft{
			func() (event.Draft, error) { return turnStart(1) },
			func() (event.Draft, error) { return stepStart(1, 1) },
		}, func() (event.Draft, error) { return stepStart(1, 2) }},
		{"step end with no step open", []draft{func() (event.Draft, error) { return turnStart(1) }},
			func() (event.Draft, error) { return stepEnd(1, 1) }},
		{"step end of another step", []draft{
			func() (event.Draft, error) { return turnStart(1) },
			func() (event.Draft, error) { return stepStart(1, 1) },
		}, func() (event.Draft, error) { return stepEnd(1, 2) }},
		{"tool call for no unanswered call", []draft{
			func() (event.Draft, error) { return turnStart(1) },
			func() (event.Draft, error) { return stepStart(1, 1) },
		}, func() (event.Draft, error) { return toolCall("c9", "grep") }},
		{"tool call started twice", []draft{
			func() (event.Draft, error) { return turnStart(1) },
			func() (event.Draft, error) { return stepStart(1, 1) },
			func() (event.Draft, error) { return assistant(t, "m-a", call("c1", "grep")) },
			func() (event.Draft, error) { return toolCall("c1", "grep") },
		}, func() (event.Draft, error) { return toolCall("c1", "grep") }},
		{"tool call to another tool", []draft{
			func() (event.Draft, error) { return turnStart(1) },
			func() (event.Draft, error) { return stepStart(1, 1) },
			func() (event.Draft, error) { return assistant(t, "m-a", call("c1", "grep")) },
		}, func() (event.Draft, error) { return toolCall("c1", "edit") }},
		{"a call id still unanswered", []draft{
			func() (event.Draft, error) { return turnStart(1) },
			func() (event.Draft, error) { return stepStart(1, 1) },
			func() (event.Draft, error) { return assistant(t, "m-a", call("c1", "grep")) },
		}, func() (event.Draft, error) { return assistant(t, "m-b", call("c1", "edit")) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newLog(t)
			for _, d := range c.setup {
				l.add(d())
			}
			before, err := l.st.Checkpoint()
			if err != nil {
				t.Fatal(err)
			}
			n := l.st.Len()
			if _, err := l.try(c.bad()); !errors.Is(err, surface.ErrUnbalanced) {
				t.Fatalf("got %v, want session/unbalanced", err)
			}
			if after, _ := l.st.Checkpoint(); after != before || l.st.Len() != n {
				t.Fatal("a refused event changed the folds")
			}
		})
	}
}

// A checkpoint is restored only from the records it cites: unreadable state is
// session/checkpoint-version (the caller replays from 0), and a store that
// fails the read, returns too few records or other seqs is reported, never
// folded.
func TestRestoreRefusesABadCheckpointOrRead(t *testing.T) {
	l := genLog(t, rand.New(rand.NewPCG(3, 9)), 2)
	state, err := l.st.Checkpoint()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := surface.Restore(ctx, nil, surface.CheckpointVersion, `{not json`, readSeqs(l.recs)); !errors.Is(err, surface.ErrCheckpoint) {
		t.Fatalf("unreadable state = %v", err)
	}
	gone := errors.New("store gone")
	for name, c := range map[string]struct {
		read surface.ReadSeqsFunc
		want error
	}{
		"read fails": {func(context.Context, []event.Seq) ([]event.Record, error) { return nil, gone }, gone},
		"short read": {func(ctx context.Context, seqs []event.Seq) ([]event.Record, error) {
			recs, err := readSeqs(l.recs)(ctx, seqs)
			return recs[:len(recs)-1], err
		}, surface.ErrBrokenStore},
		"other seqs": {func(ctx context.Context, seqs []event.Seq) ([]event.Record, error) {
			recs, err := readSeqs(l.recs)(ctx, seqs)
			slices.Reverse(recs)
			return recs, err
		}, surface.ErrBrokenStore},
	} {
		if s, err := surface.Restore(ctx, nil, surface.CheckpointVersion, state, c.read); !errors.Is(err, c.want) || s != nil {
			t.Errorf("%s: Restore = %v, %v; want %v and no state", name, s, err, c.want)
		}
	}
	s, err := surface.Restore(ctx, nil, surface.CheckpointVersion, state, readSeqs(l.recs))
	if err != nil || s.Len() != l.st.Len() {
		t.Fatalf("a good restore = %v, len %d of %d", err, s.Len(), l.st.Len())
	}
}

// Closers need a cause.
func TestClosersNeedACause(t *testing.T) {
	l := newLog(t)
	l.add(turnStart(1))
	if _, err := surface.Closers(l.st.Open(), "", l.st.Len()); err == nil {
		t.Fatal("closers without a cause were built")
	}
}

// A history snapshot names each node's seq, type, role and revision; a content
// rewrite of one node by its own type bumps the revision; the messages and raw
// forms agree.
func TestHistoryNodes(t *testing.T) {
	l := newLog(t)
	l.add(system(t, "m-s", "be brief"))
	u := l.add(user(t, "m-u", "hi"))
	l.add(assistant(t, "m-a", text("hello")))
	h := l.st.History()
	if h.Len() != 3 || !slices.Equal(h.Seqs(), []event.Seq{0, 1, 2}) {
		t.Fatalf("history len %d seqs %v", h.Len(), h.Seqs())
	}
	n := h.At(1)
	if n.Seq() != u || n.Type() != event.UserMessage.Name() || n.Role() != vocab.RoleUser || n.Revision() != 0 {
		t.Fatalf("node 1 = seq %d type %q role %q rev %d", n.Seq(), n.Type(), n.Role(), n.Revision())
	}
	ms, err := h.Messages()
	if err != nil || len(ms) != 3 || ms[0].Role() != vocab.RoleSystem || ms[2].ID() != "m-a" {
		t.Fatalf("messages = %d, %v", len(ms), err)
	}
	raw := h.Raw()
	if len(raw) != 3 || string(raw[1]) != n.Raw() {
		t.Fatalf("raw[1] = %s, node raw %s", raw[1], n.Raw())
	}
	var m vocab.Message
	if err := json.Unmarshal(raw[1], &m); err != nil || m.ID() != "m-u" {
		t.Fatalf("raw[1] does not decode to the user message: %v", err)
	}

	r := l.add(userReplace(t, "m-u2", "hi again", u, u, u))
	h2 := l.st.History()
	if got := h2.At(1); got.Seq() != r || got.Revision() != 1 || got.Type() != event.UserMessage.Name() {
		t.Fatalf("rewritten node = seq %d rev %d type %q", got.Seq(), got.Revision(), got.Type())
	}
	if h.At(1).Revision() != 0 || h.At(1).Seq() != u {
		t.Fatal("a later rewrite changed an earlier snapshot")
	}
}
