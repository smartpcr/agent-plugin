package query_test

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// homeSlot is the time between the prompts of two seeded sessions.
const homeSlot = 10 * time.Millisecond

// template is the log and stored rows of one closed product session with one
// prompt, written through the session service with the built-in views: what a
// seeded session is a copy of.
type template struct {
	recs []store.Record
	cps  []store.Checkpoint
}

func newTemplate(t *testing.T) template {
	t.Helper()
	mem := memstore.New()
	e := newEnvOn(t, mem)
	h := must(e.svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "tmpl"}}))
	if _, err := h.AppendGroup(bg, []event.Draft{promptDraft(t, "hello")}); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	r := must(mem.OpenRead(bg, "tmpl"))
	defer func() { _ = r.Close(bg) }()
	tp := template{recs: must(r.Read(bg, 0, 0, store.ReadOptions{WithAux: true})), cps: must(r.Checkpoints(bg))}
	if len(tp.recs) == 0 || len(tp.cps) == 0 {
		t.Fatalf("template: %d records, %d rows", len(tp.recs), len(tp.cps))
	}
	return tp
}

// seedHome stores n copies of tp in st, "h00000" the oldest: copy i is created
// at base + i slots and its records are moved to that time, so the copies'
// prompt order is the reverse of their index. Each copy is one Seed and one
// UpsertDerived of the template's rows, as the session service would have left
// it. It returns the ids, newest prompt first.
func seedHome(t *testing.T, st *sqlite.Store, tp template, n int, base time.Time) []string {
	t.Helper()
	shift := base.UnixMilli() - tp.recs[0].Time
	ids := make([]string, n)
	for i := range n {
		id := fmt.Sprintf("h%05d", i)
		at := base.Add(time.Duration(i) * homeSlot)
		recs := slices.Clone(tp.recs)
		for j := range recs {
			recs[j].Time += shift + at.UnixMilli() - base.UnixMilli()
		}
		if err := st.Seed(bg, store.Header{ID: id, CreatedAt: at}, recs); err != nil {
			t.Fatal(err)
		}
		if _, err := st.UpsertDerived(bg, id, tp.cps); err != nil {
			t.Fatal(err)
		}
		ids[n-1-i] = id
	}
	return ids
}

func openSQLite(t *testing.T) *sqlite.Store {
	t.Helper()
	st := must(sqlite.Open(bg, filepath.Join(t.TempDir(), "dsh.db")))
	t.Cleanup(func() { _ = st.Close(bg) })
	return st
}

// cutOf is the cut a page's cursor carries.
func cutOf(t *testing.T, next string) int64 {
	t.Helper()
	c, err := store.ParsePromptCursor(next)
	if err != nil {
		t.Fatal(err)
	}
	return c.AsOf
}

// DH-D-VIEW-5 "paging returns each visible session once while others gain
// prompts or are created": a caller pages query.List through its cursors over
// 1,000 sessions while a writer, between every two pages, prompts a session not
// yet listed (its live key would jump ahead of the cursor), prompts one already
// listed and creates a new session with a prompt.
//
// Visible is defined by the cursor's AsOf (store.PromptCursor, store.OrderPrompt):
// the cut C is the AsOf of the first page's cursor, which every later cursor
// carries. A session is visible when it is active, was committed before the
// first page was read, was created at or before C (CreatedAt <= C) and the
// caller may see it (the default Viewer, no marks: every session; IncludeBlank,
// so the summary's blank flag does not decide). Each visible session must be
// listed exactly once, in the order of its last prompt before C's millisecond
// (store.PromptCut). A session created after the first page is not visible and
// must not be listed, even when a clock coarser than a nanosecond stamps it
// with C itself.
//
//dsh:test DH-T-VIEW-15
func TestListPagingIsStableWhileSessionsChange(t *testing.T) {
	const n, limit = 1_000, 50
	st := openSQLite(t)
	e := newEnvOn(t, st)
	seeded := seedHome(t, st, newTemplate(t), n, time.Now().Add(-time.Hour))
	q := query.New(st)
	o := query.ListOptions{Limit: limit, IncludeBlank: true}

	stop := make(chan struct{})
	halt := sync.OnceFunc(func() { close(stop) })
	done := make(chan error, 1)
	// The writer sends on landed after each change, so the reader waits for
	// three changes between pages instead of polling.
	landed := make(chan struct{}, 3)
	var created []string
	write := func() {
		defer close(done)
		send := func() bool {
			select {
			case landed <- struct{}{}:
				return true
			case <-stop:
				return false
			}
		}
		for i := 0; ; i++ {
			// The oldest unlisted session first (seeded is newest first), then
			// the newest, already listed, then a new session.
			if err := e.appendTo(seeded[n-1-i%n], promptDraft(t, "again")); err != nil {
				done <- err
				return
			}
			if !send() {
				return
			}
			if err := e.appendTo(seeded[i%n], promptDraft(t, "again")); err != nil {
				done <- err
				return
			}
			if !send() {
				return
			}
			id := fmt.Sprintf("new%04d", i)
			h, err := e.svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: id}})
			if err == nil {
				_, err = h.AppendGroup(bg, []event.Draft{promptDraft(t, id)})
				if cerr := h.Close(bg); err == nil {
					err = cerr
				}
			}
			if err != nil {
				done <- err
				return
			}
			created = append(created, id)
			if !send() {
				return
			}
		}
	}

	var (
		seen []string
		cut  int64
	)
	for pages := 0; ; pages++ {
		if pages > n/limit+1 {
			t.Fatal("List does not terminate")
		}
		for k := 0; pages > 0 && k < 3; k++ { // three changes land between pages
			select {
			case <-landed:
			case err := <-done:
				t.Fatalf("the writer stopped: %v", err)
			}
		}
		p := must(q.List(bg, o))
		for _, en := range p.Entries {
			seen = append(seen, string(en.ID))
		}
		if pages == 0 {
			cut = cutOf(t, p.Next)
			// The cleanup stops the writer, then joins it.
			t.Cleanup(func() { halt(); <-done })
			go write()
		} else if p.Next != "" && cutOf(t, p.Next) != cut {
			t.Fatalf("page %d carries the cut %d, the first page's is %d", pages, cutOf(t, p.Next), cut)
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

	// Visible: every seeded session, created an hour before the cut and
	// committed before the first page. The writer started after the first page,
	// so none of its sessions is, and none can be stamped before the cut.
	visible := seeded
	for _, id := range created {
		if sum := must(st.Stat(bg, id)); sum.Header.CreatedAt.UnixNano() < cut {
			t.Fatalf("session %s, created after the first page, is stamped %d, before its cut %d", id, sum.Header.CreatedAt.UnixNano(), cut)
		}
	}
	// One session per page after the first, the one in flight at the halt
	// aside.
	if len(created) < n/limit-2 {
		t.Fatalf("the writer created %d sessions over %d pages", len(created), n/limit)
	}
	once := map[string]int{}
	for _, id := range seen {
		once[id]++
	}
	for _, id := range visible {
		if once[id] != 1 {
			t.Errorf("visible session %s listed %d times", id, once[id])
		}
	}
	if len(seen) != len(visible) {
		t.Fatalf("listed %d sessions, %d are visible", len(seen), len(visible))
	}
	// The seeded sessions' prompts before the cut are their seeded ones, so the
	// listing is their seeded order.
	if !slices.Equal(seen, seeded) {
		t.Fatalf("the listing is not in the order as of the cut: first %v, want %v", seen[:5], seeded[:5])
	}
}

// rowCount counts what crosses the store's interface: the summaries List
// hands out, the reader opens, the stored rows Checkpoints returns and the
// records Read and ReadSeqs return.
type rowCount struct{ summaries, opens, rows, records atomic.Int64 }

func (c *rowCount) total() [4]int64 {
	return [4]int64{c.summaries.Load(), c.opens.Load(), c.rows.Load(), c.records.Load()}
}

type rowCounting struct {
	store.Store
	c *rowCount
}

func (s rowCounting) List(ctx context.Context, q store.Query) (store.Page, error) {
	p, err := s.Store.List(ctx, q)
	s.c.summaries.Add(int64(len(p.Summaries)))
	return p, err
}

func (s rowCounting) OpenRead(ctx context.Context, id string) (store.Reader, error) {
	r, err := s.Store.OpenRead(ctx, id)
	if err != nil {
		return nil, err
	}
	s.c.opens.Add(1)
	return rowCountingReader{r, s.c}, nil
}

type rowCountingReader struct {
	store.Reader
	c *rowCount
}

func (r rowCountingReader) Checkpoints(ctx context.Context) ([]store.Checkpoint, error) {
	cps, err := r.Reader.Checkpoints(ctx)
	r.c.rows.Add(int64(len(cps)))
	return cps, err
}

func (r rowCountingReader) Read(ctx context.Context, from event.Seq, limit int, o store.ReadOptions) ([]store.Record, error) {
	recs, err := r.Reader.Read(ctx, from, limit, o)
	r.c.records.Add(int64(len(recs)))
	return recs, err
}

func (r rowCountingReader) ReadSeqs(ctx context.Context, seqs []event.Seq, o store.ReadOptions) ([]store.Record, error) {
	recs, err := r.Reader.ReadSeqs(ctx, seqs, o)
	r.c.records.Add(int64(len(recs)))
	return recs, err
}

// DH-D-VIEW-5 "rows read per page do not grow with the corpus": the first
// page and a page two pages from the end read the same rows at 100 and at
// 10,000 sessions (summaries, reader opens, stored rows and log records
// through the store's interface; no log record at all, the summaries being
// Stored). The store's own reads below the interface are bounded by its
// tests: each page statement is a range search of its index
// (sqlite.TestListPagesSearchTheirIndex) and an open judges only the records
// from the newest checkpoint (sqlite.TestOpenIsBoundedByTheNewestCheckpoint).
//
//dsh:test DH-T-VIEW-15
func TestListRowsPerPageDoNotGrow(t *testing.T) {
	const limit = query.DefaultLimit
	tp := newTemplate(t)
	counts := map[int][2][4]int64{}
	for _, n := range []int{100, 10_000} {
		st := openSQLite(t)
		start := time.Now()
		ids := seedHome(t, st, tp, n, time.Now().Add(-2*time.Hour))
		t.Logf("seeded %d sessions in %v", n, time.Since(start))
		var c rowCount
		q := query.New(rowCounting{st, &c})

		first := must(q.List(bg, query.ListOptions{}))
		if len(first.Entries) != limit || string(first.Entries[0].ID) != ids[0] || first.Next == "" {
			t.Fatalf("%d sessions, first page: %d entries, next %q", n, len(first.Entries), first.Next)
		}
		atFirst := c.total()

		// A cursor after the session 2*limit+1 from the end: the page two
		// pages from the end.
		last := must(st.Stat(bg, ids[n-2*limit-1]))
		cur := store.PromptCursor{AsOf: cutOf(t, first.Next), Key: last.LastPromptAt.UnixNano(), ID: last.Header.ID}
		deep := must(q.List(bg, query.ListOptions{Cursor: cur.String()}))
		if len(deep.Entries) != limit || string(deep.Entries[0].ID) != ids[n-2*limit] || deep.Next == "" {
			t.Fatalf("%d sessions, deep page: %d entries from %v, next %q", n, len(deep.Entries), deep.Entries, deep.Next)
		}
		atDeep := c.total()
		for i := range atDeep {
			atDeep[i] -= atFirst[i]
		}
		counts[n] = [2][4]int64{atFirst, atDeep}
		t.Logf("%d sessions: [summaries opens rows records] first page %v, deep page %v", n, atFirst, atDeep)
	}
	if counts[100] != counts[10_000] {
		t.Fatalf("rows read per page grew with the store: 100 sessions %v, 10,000 sessions %v", counts[100], counts[10_000])
	}
	if got := counts[100][0]; got[0] != limit || got[1] != limit || got[2] < limit || got[3] != 0 {
		t.Fatalf("a page read %v, want %d summaries, %d opens, a row each and no log record", got, limit, limit)
	}
}
