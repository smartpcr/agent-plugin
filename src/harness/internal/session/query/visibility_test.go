package query_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

// sameAsMissing fails unless err is exactly what the caller gets for a session
// that does not exist (only the id differs).
func sameAsMissing(t *testing.T, what string, id session.ID, err, missing error) {
	t.Helper()
	if !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("%s of %s: %v, want session/not-found", what, id, err)
	}
	if got, want := strings.ReplaceAll(err.Error(), string(id), "<id>"), strings.ReplaceAll(missing.Error(), "missing", "<id>"); got != want {
		t.Fatalf("%s of %s: %q, a missing session gives %q", what, id, got, want)
	}
}

// DH-D-VIEW-5 "within what the caller may see": a caller limited to one project
// lists only that project's visible sessions, and for a session of another
// project or a hidden one every read (observe, trace, lineage, follow) answers
// exactly as for a missing session; the lineage and the fork list stop at what
// it may not see; a follower stops as the session is hidden.
func TestCallerSeesOnlyWhatItMay(t *testing.T) {
	home := t.TempDir()
	st := must(sqlite.Open(bg, filepath.Join(home, "dsh.db")))
	t.Cleanup(func() { _ = st.Close(bg) })
	e := newEnvOn(t, st)
	ws := must(workspace.Open(bg, st))
	proj := map[string]workspace.Workspace{}
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(home, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		proj[name] = must(ws.Add(bg, dir))
	}
	alpha, beta := proj["alpha"], proj["beta"]
	e.createIn(t, "a1", alpha.Path, 6)
	e.createIn(t, "a2", alpha.Path, 2)
	e.createIn(t, "b1", beta.Path, 4)
	e.forkIn(t, "f", alpha.Path, "a1", 2, 1)  // visible fork of a1
	e.forkIn(t, "fh", alpha.Path, "a1", 2, 0) // hidden fork of a1
	e.forkIn(t, "x", alpha.Path, "b1", 1, 1)  // alpha fork of a beta session
	for id, w := range map[session.ID]workspace.Workspace{"a1": alpha, "a2": alpha, "b1": beta, "f": alpha, "fh": alpha, "x": alpha} {
		if err := ws.Attach(bg, w.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []session.ID{"a2", "fh"} {
		if err := ws.SetArchive(bg, id, workspace.ArchiveArchived); err != nil {
			t.Fatal(err)
		}
	}
	q := query.New(st, query.WithWorkspaces(ws), query.WithFollowInterval(5*time.Millisecond),
		query.WithCaller(query.Caller{Projects: []string{alpha.ID}}))

	p := must(q.List(bg, query.ListOptions{IncludeBlank: true}))
	var ids []string
	for _, en := range p.Entries {
		ids = append(ids, string(en.ID))
	}
	slices.Sort(ids)
	if fmt.Sprint(ids) != "[a1 f x]" {
		t.Fatalf("listed %v, want [a1 f x]", ids)
	}
	l := must(q.ListByProject(bg, query.ListOptions{IncludeBlank: true}))
	if len(l.Groups) != 1 || l.Groups[0].Workspace.ID != alpha.ID || len(l.Groups[0].Sessions) != 3 {
		t.Fatalf("by project = %+v", l.Groups)
	}

	_, missing := q.Observe(bg, "missing", query.Window{})
	if !errors.Is(missing, store.ErrNotFound) {
		t.Fatalf("observe missing: %v", missing)
	}
	for _, id := range []session.ID{"missing", "b1", "a2"} {
		_, err := q.Observe(bg, id, query.Window{})
		sameAsMissing(t, "observe", id, err, missing)
		_, err = q.Trace(bg, id, query.Window{})
		sameAsMissing(t, "trace", id, err, missing)
		_, err = q.Lineage(bg, id)
		sameAsMissing(t, "lineage", id, err, missing)
		_, err = q.Follow(bg, id, 0)
		sameAsMissing(t, "follow", id, err, missing)
	}

	// The fork list leaves out the hidden fork; a lineage stops at an ancestor
	// of another project, marked as gone (as a deleted one is).
	if fl := must(q.Lineage(bg, "a1")); fmt.Sprint(fl.Forks) != "[f]" || fl.Truncated {
		t.Fatalf("forks of a1 = %v truncated %v", fl.Forks, fl.Truncated)
	}
	xl := must(q.Lineage(bg, "x"))
	if len(xl.Chain) != 2 || xl.Chain[0].ID != "b1" || !xl.Chain[0].Gone {
		t.Fatalf("lineage of x = %+v", xl.Chain)
	}

	// An operator sees the hidden session.
	if _, err := q.As(query.Operator()).Observe(bg, "a2", query.Window{}); err != nil {
		t.Fatalf("operator observe of a hidden session: %v", err)
	}

	// A follower stops, as for a missing session, once its session is hidden.
	f := must(q.Follow(bg, "a1", 0))
	defer f.Close()
	for n := 0; n < 6; n++ {
		select {
		case _, ok := <-f.Events():
			if !ok {
				t.Fatalf("follower stopped early: %v", f.Err())
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("followed %d records", n)
		}
	}
	if err := ws.SetArchive(bg, "a1", workspace.ArchiveArchived); err != nil {
		t.Fatal(err)
	}
	select {
	case _, ok := <-f.Events():
		if ok {
			t.Fatal("a record after the session was hidden")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the follower of a hidden session kept going")
	}
	sameAsMissing(t, "follower", "a1", f.Err(), missing)
}

// racingStore appends to a session whenever a reader of it reads.
type racingStore struct {
	store.Store
	hook func()
}

func (s racingStore) OpenRead(ctx context.Context, id string) (store.Reader, error) {
	r, err := s.Store.OpenRead(ctx, id)
	if err != nil {
		return nil, err
	}
	return racingReader{r, s.hook}, nil
}

type racingReader struct {
	store.Reader
	hook func()
}

func (r racingReader) Read(ctx context.Context, from event.Seq, limit int, o store.ReadOptions) ([]store.Record, error) {
	r.hook()
	return r.Reader.Read(ctx, from, limit, o)
}

func (r racingReader) Checkpoints(ctx context.Context) ([]store.Checkpoint, error) {
	r.hook()
	return r.Reader.Checkpoints(ctx)
}

// An observation is one committed cut: records appended between its reads (of
// the window, the checkpoints, the folds) are in none of them, so the window
// and all four views agree with the head.
func TestObserveIsOneCut(t *testing.T) {
	e := newEnv(t)
	e.create(t, "s", 10)
	h, _, err := e.svc.Open(bg, nil, "s", session.OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close(bg)
	var appended atomic.Int32
	q := query.New(racingStore{e.st, func() {
		if _, err := h.AppendGroup(bg, ctxDrafts("late", 1)); err != nil {
			t.Error(err)
		}
		if _, err := h.Flush(bg); err != nil {
			t.Error(err)
		}
		appended.Add(1)
	}})

	o := must(q.Observe(bg, "s", query.Window{}))
	if appended.Load() < 2 {
		t.Fatalf("only %d appends raced the observation", appended.Load())
	}
	if now := must(e.st.Stat(bg, "s")).NextSeq; now <= 10 {
		t.Fatalf("the store did not grow: %d", now)
	}
	if o.Head != 10 || len(o.Events) != 10 || o.Next != 10 || o.More {
		t.Fatalf("observe = head %d, %d events, next %d, more %v", o.Head, len(o.Events), o.Next, o.More)
	}
	if o.Views.Stats.Events != 10 || len(o.Views.Index.Events) != 10 {
		t.Fatalf("views past the head: stats %d, index %d", o.Views.Stats.Events, len(o.Views.Index.Events))
	}
}

// countingStore counts the candidates its List hands out.
type countingStore struct {
	store.Store
	n *atomic.Int64
}

func (s countingStore) List(ctx context.Context, q store.Query) (store.Page, error) {
	p, err := s.Store.List(ctx, q)
	s.n.Add(int64(len(p.Summaries)))
	return p, err
}

// The listing's bound is store candidates examined, filtered ones included: on
// mostly hidden pages it reads no more than the bound and says the result is
// truncated; with room for all it is complete.
func TestListingBoundCountsCandidates(t *testing.T) {
	e := newEnv(t)
	marks := workspace.NewMemoryMarks()
	for i := range 30 {
		id := fmt.Sprintf("s%02d", i)
		e.create(t, id, 1)
		if i%10 != 0 {
			if err := marks.SetArchive(bg, session.ID(id), workspace.ArchiveArchived); err != nil {
				t.Fatal(err)
			}
		}
	}
	var n atomic.Int64
	cs := countingStore{e.st, &n}

	l := must(query.New(cs, query.WithMarks(marks), query.WithMaxListed(10)).ListByProject(bg, query.ListOptions{IncludeBlank: true}))
	if !l.Truncated || l.Examined != 10 || n.Load() != 10 {
		t.Fatalf("bounded listing: truncated %v, examined %d, store handed out %d", l.Truncated, l.Examined, n.Load())
	}
	listed := 0
	for _, g := range l.Groups {
		listed += len(g.Sessions)
	}
	if listed > 1 {
		t.Fatalf("listed %d of the first 10 candidates, at most 1 is visible", listed)
	}

	n.Store(0)
	full := must(query.New(cs, query.WithMarks(marks)).ListByProject(bg, query.ListOptions{IncludeBlank: true}))
	if full.Truncated || full.Examined != 30 || n.Load() != 30 || len(full.Groups) != 1 || len(full.Groups[0].Sessions) != 3 {
		t.Fatalf("full listing: truncated %v, examined %d, handed out %d, groups %+v", full.Truncated, full.Examined, n.Load(), full.Groups)
	}
}

func promptDraft(t testing.TB, text string) event.Draft {
	m, err := vocab.NewMessage(vocab.RoleUser, vocab.TextBlock(text))
	if err != nil {
		t.Fatal(err)
	}
	return must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: m}))
}

// appendTo opens session id, appends ds and closes it.
func (e *env) appendTo(id string, ds ...event.Draft) error {
	h, _, err := e.svc.Open(bg, nil, session.ID(id), session.OpenOptions{})
	if err != nil {
		return err
	}
	_, err = h.AppendGroup(bg, ds)
	return errors.Join(err, h.Close(bg))
}

// DH-D-VIEW-5 "the keyset list on last_prompt_at with as-of ordering": List
// pages by the last prompt, not the last event, and while prompts are appended
// concurrently to every session (each would move it to the top) a paging
// caller lists every session exactly once, in the order as of its first page.
func TestListPagesByLastPromptAsOf(t *testing.T) {
	// P-V: the session clock reads time.Now, so in the bubble the prompt
	// milliseconds are virtual and two prompts are 2 ms apart without a sleep.
	synctest.Test(t, func(t *testing.T) {
		st := must(sqlite.Open(bg, filepath.Join(t.TempDir(), "dsh.db")))
		t.Cleanup(func() { _ = st.Close(bg) })
		e := newEnvOn(t, st)
		ids := []string{"s1", "s2", "s3", "s4", "s5", "s6"}
		for _, id := range ids {
			h := must(e.svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}}))
			if _, err := h.AppendGroup(bg, []event.Draft{promptDraft(t, id)}); err != nil {
				t.Fatal(err)
			}
			if err := h.Close(bg); err != nil {
				t.Fatal(err)
			}
			<-time.After(2 * time.Millisecond) // distinct prompt milliseconds (virtual)
		}
		// s1 has the oldest prompt but the newest event.
		if err := e.appendTo("s1", ctxDrafts("s1", 3)...); err != nil {
			t.Fatal(err)
		}
		<-time.After(2 * time.Millisecond)
		q := query.New(st)
		again := promptDraft(t, "again")

		stop := make(chan struct{})
		halt := sync.OnceFunc(func() { close(stop) })
		done := make(chan error, 1)
		// P-S: the writer sends on appended after each prompt it lands, so the
		// reader waits for two prompts between pages instead of polling a counter.
		appended := make(chan struct{}, 2*len(ids))
		appendAll := func() {
			defer close(done)
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				// Each prompt in a later (virtual) millisecond than the one before,
				// as on the wall clock.
				<-time.After(time.Millisecond)
				// The oldest prompts first: each jumps ahead of the cursor.
				if err := e.appendTo(ids[i%len(ids)], again); err != nil {
					done <- err
					return
				}
				select {
				case appended <- struct{}{}:
				case <-stop:
					return
				}
			}
		}
		var seen []string
		o := query.ListOptions{Limit: 2}
		for pages := 0; ; pages++ {
			if pages > len(ids) {
				t.Fatal("List does not terminate")
			}
			for n := 0; pages > 0 && n < 2; n++ { // prompts land between pages
				select {
				case <-appended:
				case err := <-done:
					t.Fatalf("the writer stopped before two prompts landed: %v", err)
				}
			}
			p := must(q.List(bg, o))
			for _, en := range p.Entries {
				seen = append(seen, string(en.ID))
			}
			if pages == 0 {
				// P-C: the cleanup stops the writer, then joins it.
				t.Cleanup(func() { halt(); <-done })
				go appendAll()
			}
			if p.Next == "" {
				break
			}
			o.Cursor = p.Next
		}
		halt()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if want := []string{"s6", "s5", "s4", "s3", "s2", "s1"}; !slices.Equal(seen, want) {
			t.Fatalf("listed %v while prompts were appended, want %v", seen, want)
		}
		// A new listing's cut is its own now: let the last prompt's millisecond pass.
		<-time.After(2 * time.Millisecond)
		fresh := must(q.List(bg, query.ListOptions{}))
		if len(fresh.Entries) != len(ids) || fresh.Entries[0].Summary.LastPromptAt < fresh.Entries[1].Summary.LastPromptAt {
			t.Fatalf("a new listing is not by the latest prompts: %+v", fresh.Entries)
		}
	})
}

// List pages are bounded: no limit is DefaultLimit, a larger one (up to the
// integer maximum, which must not overflow the store's query) is MaxLimit; the
// store clamps a direct query too.
func TestListPagesAreBounded(t *testing.T) {
	st := must(sqlite.Open(bg, filepath.Join(t.TempDir(), "dsh.db")))
	t.Cleanup(func() { _ = st.Close(bg) })
	e := newEnvOn(t, st)
	n := query.MaxLimit + 5
	for i := range n {
		h := must(e.svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: fmt.Sprintf("s%03d", i)}}))
		if _, err := h.AppendGroup(bg, []event.Draft{promptDraft(t, "p")}); err != nil {
			t.Fatal(err)
		}
		if err := h.Close(bg); err != nil {
			t.Fatal(err)
		}
	}
	q := query.New(st)
	for _, c := range []struct{ limit, want int }{
		{0, query.DefaultLimit}, {-1, query.DefaultLimit}, {7, 7},
		{query.MaxLimit, query.MaxLimit}, {query.MaxLimit + 1, query.MaxLimit}, {math.MaxInt, query.MaxLimit},
	} {
		p, err := q.List(bg, query.ListOptions{Limit: c.limit})
		if err != nil || len(p.Entries) != c.want || p.Next == "" {
			t.Fatalf("limit %d: %d entries, next %q, %v; want %d and more", c.limit, len(p.Entries), p.Next, err, c.want)
		}
	}
	for _, o := range []store.Order{store.OrderRecent, store.OrderPrompt} {
		p, err := st.List(bg, store.Query{Limit: math.MaxInt, Order: o})
		if err != nil || len(p.Summaries) != n || p.Next != "" {
			t.Fatalf("store order %d at the integer maximum: %d of %d, next %q, %v", o, len(p.Summaries), n, p.Next, err)
		}
	}
}
