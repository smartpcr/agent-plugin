// Package corpussqlite seeds the heavy-tailed corpus (testkit/corpus) into a
// SQLite session store for the budget benchmarks (DH-T-STORE-18, DH-T-VIEW-25).
// Each session is written as rows another build wrote, through
// sqlite.Store.Seed, and then given the checkpoints a closed session of the
// product holds: the core folds' checkpoint at its head (what Close writes) and
// the stored rows of the built-in views (what Open's restore writes), so an
// open, a listing or a snapshot takes them up instead of folding the whole log.
//
// It is a package of its own so testkit/corpus stays free of the SQLite store:
// the agent driver's benchmark imports the ladder without it. Only tests and
// the kit's own packages import it (archtest, DH-T-UTIL-11).
package corpussqlite

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/corpus"
)

// Epoch is the Unix millisecond the first corpus session starts at.
const Epoch int64 = 1_700_000_000_000

// Slot is the time between the starts of two corpus sessions: longer than the
// longest session lasts (one millisecond per record), so the sessions' prompt
// order is their start order.
const Slot = 10 * time.Minute

// Seed stores session h with the log of shape sh starting at start (Unix ms)
// through st.Seed, then its checkpoints: the core folds' checkpoint at the head
// and the built-in views' stored rows. It returns the number of records.
func Seed(t testing.TB, st *sqlite.Store, h store.Header, sh corpus.Shape, start int64) store.Seq {
	t.Helper()
	n, err := seed(testkit.Tree(t), st, h, sh, start)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func seed(root *cordis.Context, st *sqlite.Store, h store.Header, sh corpus.Shape, start int64) (store.Seq, error) {
	ctx := context.Background()
	recs, err := corpus.Log(sh, start)
	if err != nil {
		return 0, err
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.UnixMilli(start).UTC()
	}
	for _, step := range []struct {
		what string
		do   func() error
	}{
		{"seeding", func() error { return st.Seed(ctx, h, recs) }},
		{"the core checkpoint of", func() error { return coreCheckpoint(ctx, st, h.ID, recs) }},
		{"the view rows of", func() error { return viewRows(ctx, root, st, h.ID) }},
	} {
		if err := step.do(); err != nil {
			return 0, fmt.Errorf("corpussqlite: %s %s: %w", step.what, h.ID, err)
		}
	}
	return store.Seq(len(recs)), nil
}

// coreCheckpoint commits the core folds' checkpoint of recs at its last seq, as
// a checkpoint-only batch under the session's lease.
func coreCheckpoint(ctx context.Context, st *sqlite.Store, id string, recs []event.Record) error {
	var (
		state string
		w     store.Writer
	)
	folds, err := surface.Replay(event.Default(), recs)
	if err == nil {
		state, err = folds.Checkpoint()
	}
	if err == nil {
		w, err = st.OpenWrite(ctx, id, store.OpenOptions{})
	}
	if err != nil {
		return err
	}
	n := store.Seq(len(recs))
	err = w.Append(ctx, store.Batch{First: n, Checkpoints: []store.Checkpoint{
		{Key: surface.CheckpointKey, StateVersion: surface.CheckpointVersion, Seq: n - 1, State: state}}})
	if cerr := w.Close(ctx); err == nil {
		err = cerr
	}
	return err
}

// viewRows opens session id with the built-in views, which restores them and
// commits their rows at the head, and closes it, which writes nothing more,
// since nothing was appended. The open must have restored the core folds from
// their checkpoint and found no open turn.
func viewRows(ctx context.Context, root *cordis.Context, st *sqlite.Store, id string) error {
	reg := views.New()
	_, err := views.RegisterBuiltins(reg)
	var (
		s   session.Handle
		rep session.OpenReport
	)
	if err == nil {
		s, rep, err = session.NewService(root, st, session.Config{Folder: reg}).Open(ctx, nil, session.ID(id), session.OpenOptions{})
	}
	if err != nil {
		return err
	}
	if err := s.Close(ctx); err != nil || !rep.FromCheckpoint || rep.Interrupted {
		return fmt.Errorf("closed with %v, report %+v", err, rep)
	}
	return nil
}

// Home seeds a corpus, shapes ascending by rank (corpus.Corpus), into st and
// returns the ids by listing order, the newest prompt first. Session rank r is
// "corpus-<r>-a<anchor>". The last (longest) session is the newest, the worst
// case of a first page (a user's longest session is the one they are working
// in); the others' order is a fixed shuffle, so length and recency are
// unrelated.
func Home(t testing.TB, st *sqlite.Store, shapes []corpus.Shape) []string {
	t.Helper()
	count := len(shapes)
	if count == 0 {
		t.Fatal("corpussqlite: a home of no sessions")
	}
	root := testkit.Tree(t)
	slots := append(rand.New(rand.NewPCG(2026, 928)).Perm(count-1), count-1)
	ids := make([]string, count)
	for r, sh := range shapes {
		id := fmt.Sprintf("corpus-%04d-a%02d", r, sh.Anchor)
		if _, err := seed(root, st, store.Header{ID: id}, sh, Epoch+int64(slots[r])*Slot.Milliseconds()); err != nil {
			t.Fatal(err)
		}
		ids[count-1-slots[r]] = id
	}
	return ids
}
