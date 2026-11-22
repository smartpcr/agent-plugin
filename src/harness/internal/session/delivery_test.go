package session_test

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// blockFirst registers a listener that blocks on the session's first event
// until release is called, and reports when it is blocked.
func blockFirst(t *testing.T, e *env) (blocked <-chan struct{}, release func()) {
	t.Helper()
	b, r := make(chan struct{}), make(chan struct{})
	var once sync.Once
	release = func() { once.Do(func() { close(r) }) }
	first := true
	must(session.Committed.On(e.root, func(*cordis.Context, session.CommittedArgs) error {
		if first {
			first = false
			close(b)
			<-r
		}
		return nil
	}))
	return b, release
}

func faulty(fs **faultStore) func(store.Store) store.Store {
	return func(s store.Store) store.Store { *fs = &faultStore{Store: s}; return *fs }
}

func drafts(t *testing.T, n int) []event.Draft {
	ds := make([]event.Draft, n)
	for i := range ds {
		ds[i] = ctxDraft(t, "g")
	}
	return ds
}

// DH-T-SESSION-8 (bounded state): with a listener blocked, the session's
// delivery state never exceeds its fixed limit, and every event is still
// delivered once, in order, when the listener resumes.
//
//dsh:test DH-T-SESSION-8
func TestDeliveryStateBounded(t *testing.T) {
	defer session.SetAnchorLimit(8)()
	e := newEnv(t, session.Config{CheckpointEvery: -1, FeedPage: 16})
	blocked, release := blockFirst(t, e)
	defer release()
	rec := listen(t, e.root)
	h := e.create(t, scope.Key{})
	must(h.Append(bg, ctxDraft(t, "v")))
	<-blocked
	const n = 1000
	for i := 1; i < n; i++ {
		must(h.Append(bg, ctxDraft(t, "v")))
		if a, _, limit := session.DeliveryState(h); a > limit {
			t.Fatalf("after %d appends the session holds %d anchors, above %d", i+1, a, limit)
		}
	}
	must(h.AppendGroup(bg, drafts(t, 150)))
	release()
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	rec.waitClosed(t, h.ID())
	if got := rec.got(h.ID()); !dense(got, n+150) {
		t.Fatalf("delivered %d events, want %d once in order", len(got), n+150)
	}
}

// DH-T-SESSION-19 behind a lagging listener: the store reads a record back
// correctly while the deliverer splits a long segment, then returns a record
// of another session in its place on the read the deliverer delivers from.
// The session closes with session/broken-store and delivers nothing from that
// segment on: an earlier good read is never taken as proof of a later one.
//
//dsh:test DH-T-SESSION-19
func TestForeignRecordOnLaterRead(t *testing.T) {
	defer session.SetAnchorLimit(8)()
	for _, mode := range []string{"foreign", "foreign-late"} {
		t.Run(mode, func(t *testing.T) {
			var fs *faultStore
			e := newEnv(t, session.Config{CheckpointEvery: -1, FeedPage: 16}, faulty(&fs))
			blocked, release := blockFirst(t, e)
			defer release()
			rec := listen(t, e.root)
			h := e.create(t, scope.Key{})
			must(h.Append(bg, ctxDraft(t, "v")))
			<-blocked
			for i := 1; i < 1000; i++ {
				must(h.Append(bg, ctxDraft(t, "v")))
			}
			const bad = 500
			fs.mu.Lock()
			fs.foreignAt = bad
			fs.mu.Unlock()
			fs.setMode(mode)
			release()
			c := rec.waitClosed(t, h.ID())
			if !errors.Is(c.Cause, session.ErrBrokenStore) {
				t.Fatalf("closed with %v", c.Cause)
			}
			got := rec.got(h.ID())
			if !dense(got, len(got)) || len(got) > bad {
				t.Fatalf("delivered %d events (dense %v); seq %d is bad", len(got), dense(got, len(got)), bad)
			}
			if mode == "foreign-late" {
				fs.mu.Lock()
				reads := fs.reads
				fs.mu.Unlock()
				if reads < 2 {
					t.Fatalf("seq %d was read %d times; the scenario needs a good read before the bad one", bad, reads)
				}
			}
		})
	}
}

// A context that ends right after the store accepted a group larger than the
// delivery limit does not undo the acknowledgement: the receipts come back,
// the session stays open and every event is delivered.
func TestCancelAfterCommit(t *testing.T) {
	defer session.SetAnchorLimit(8)()
	var fs *faultStore
	e := newEnv(t, session.Config{CheckpointEvery: -1, FeedPage: 16}, faulty(&fs))
	rec := listen(t, e.root)
	h := e.create(t, scope.Key{})
	ctx, cancel := context.WithCancel(bg)
	fs.cancel = cancel
	fs.setMode("cancel-after-append")
	rs, err := h.AppendGroup(ctx, drafts(t, 150))
	if err != nil || len(rs) != 150 {
		t.Fatalf("a committed group returned %d receipts and %v", len(rs), err)
	}
	fs.setMode("")
	must(h.Append(bg, ctxDraft(t, "after")))
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	rec.waitClosed(t, h.ID())
	if got := rec.got(h.ID()); !dense(got, 151) {
		t.Fatalf("delivered %d events, want 151", len(got))
	}
}

// Cancelling one append while delivery lags far behind refuses that append
// only: the log is unchanged, later appends succeed and every accepted event
// is delivered once, in order.
func TestCancelledAppendKeepsSession(t *testing.T) {
	defer session.SetAnchorLimit(8)()
	var fs *faultStore
	e := newEnv(t, session.Config{CheckpointEvery: -1, FeedPage: 16}, faulty(&fs))
	blocked, release := blockFirst(t, e)
	defer release()
	rec := listen(t, e.root)
	h := e.create(t, scope.Key{})
	must(h.Append(bg, ctxDraft(t, "v")))
	<-blocked
	for i := 1; i < 200; i++ {
		must(h.Append(bg, ctxDraft(t, "v")))
	}
	ctx, cancel := context.WithCancel(bg)
	fs.cancel = cancel
	fs.setMode("cancel-in-append")
	if _, err := h.Append(ctx, ctxDraft(t, "cancelled")); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled append returned %v", err)
	}
	fs.setMode("")
	if h.Len() != 200 {
		t.Fatalf("the log has %d events after a cancelled append, want 200", h.Len())
	}
	if _, err := h.Append(ctx, ctxDraft(t, "late")); !errors.Is(err, context.Canceled) {
		t.Fatalf("an append with an ended context returned %v", err)
	}
	for i := 0; i < 50; i++ {
		must(h.Append(bg, ctxDraft(t, "v")))
	}
	release()
	if err := h.Close(bg); err != nil {
		t.Fatalf("the session did not survive a cancelled append: %v", err)
	}
	rec.waitClosed(t, h.ID())
	if got := rec.got(h.ID()); !dense(got, 250) {
		t.Fatalf("delivered %d events, want 250 once in order", len(got))
	}
}

// DH-T-SESSION-8: after 100,000 log-only appends behind a blocked listener the
// session's heap is within a fixed bound of the same appends with a listener
// that keeps up: the store's log is the same in both, so the difference is the
// session's own state.
//
//dsh:test DH-T-SESSION-8
func TestHeapBoundedBehindBlockedListener(t *testing.T) {
	if testing.Short() {
		t.Skip("100,000 appends")
	}
	const n = 100_000
	run := func(block bool) uint64 {
		e := newEnv(t, session.Config{CheckpointEvery: -1})
		release := func() {}
		if block {
			_, release = blockFirst(t, e)
		}
		defer release()
		h := e.create(t, scope.Key{})
		d := ctxDraft(t, "v")
		before := heap()
		for i := 0; i < n; i++ {
			must(h.Append(bg, d))
		}
		if !block {
			for {
				if a, r, _ := session.DeliveryState(h); a+r == 0 {
					break
				}
				runtime.Gosched()
			}
		}
		after := heap()
		runtime.KeepAlive(h)
		return after - min(before, after)
	}
	free, blocked := run(false), run(true)
	const bound = 2 << 20
	if blocked > free+bound {
		t.Fatalf("behind a blocked listener the heap grew %d bytes, %d more than with a live one (bound %d)", blocked, blocked-free, bound)
	}
}

func heap() uint64 {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return m.HeapAlloc
}

// DH-T-SESSION-7: a writer that fails to close (its flush or lease release)
// is reported by Close and carried by Closed, not swallowed.
//
//dsh:test DH-T-SESSION-7
func TestCloseReportsWriterFailure(t *testing.T) {
	var fs *faultStore
	e := newEnv(t, session.Config{}, faulty(&fs))
	rec := listen(t, e.root)
	h := e.create(t, scope.Key{})
	must(h.Append(bg, ctxDraft(t, "v")))
	fs.setMode("close-fails")
	err := h.Close(bg)
	if err == nil {
		t.Fatal("Close succeeded although the writer failed to close")
	}
	c := rec.waitClosed(t, h.ID())
	if c.Cause == nil || c.Cause.Error() != err.Error() {
		t.Fatalf("Close returned %v but Closed carried %v", err, c.Cause)
	}
	if again := h.Close(bg); again == nil || again.Error() != err.Error() {
		t.Fatalf("a second Close returned %v, want %v", again, err)
	}
	fs.setMode("")
	h2 := e.create(t, scope.Key{})
	if err := h2.Close(bg); err != nil {
		t.Fatalf("an ordinary close: %v", err)
	}
}
