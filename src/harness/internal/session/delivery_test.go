package session_test

import (
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
// until release is closed, and reports when it is blocked.
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

// DH-T-SESSION-8 (bounded state): with a listener blocked, the session's
// verification state never exceeds its fixed ring, and every event is still
// delivered once, in order, when the listener resumes.
func TestDeliveryStateBounded(t *testing.T) {
	defer session.SetFingerprintWindow(64)()
	e := newEnv(t, session.Config{CheckpointEvery: -1})
	blocked, release := blockFirst(t, e)
	defer release()
	rec := listen(t, e.root)
	h := e.create(t, scope.Key{})
	must(h.Append(bg, ctxDraft(t, "v")))
	<-blocked
	const n = 1000
	peak := 0
	for i := 1; i < n; i++ {
		must(h.Append(bg, ctxDraft(t, "v")))
		held, size := session.HeldFingerprints(h)
		if held > size || size != 64 {
			t.Fatalf("after %d appends the session holds %d fingerprints in a ring of %d", i+1, held, size)
		}
		peak = max(peak, held)
	}
	if peak < 32 {
		t.Fatalf("the ring never filled behind the blocked listener (peak %d)", peak)
	}
	// A group larger than the ring is verified at once and held only in part.
	group := make([]event.Draft, 150)
	for i := range group {
		group[i] = ctxDraft(t, "g")
	}
	must(h.AppendGroup(bg, group))
	if held, size := session.HeldFingerprints(h); held > size {
		t.Fatalf("after a large group the session holds %d of %d", held, size)
	}
	release()
	if err := h.Close(bg); err != nil {
		t.Fatal(err)
	}
	rec.waitClosed(t, h.ID())
	if got := rec.got(h.ID()); !dense(got, n+150) {
		t.Fatalf("delivered %d events, want %d once in order", len(got), n+150)
	}
}

// DH-T-SESSION-19 beyond the ring: a record of another session among the
// records the appender verifies (delivery lagging a full ring) closes the
// session with session/broken-store, and the record is not delivered.
func TestBrokenStoreBeyondWindow(t *testing.T) {
	defer session.SetFingerprintWindow(16)()
	var fs *faultStore
	e := newEnv(t, session.Config{CheckpointEvery: -1}, func(s store.Store) store.Store { fs = &faultStore{Store: s}; return fs })
	blocked, release := blockFirst(t, e)
	defer release()
	rec := listen(t, e.root)
	h := e.create(t, scope.Key{})
	must(h.Append(bg, ctxDraft(t, "v")))
	<-blocked
	fs.setMode("foreign")
	var err error
	for i := 0; i < 64 && err == nil; i++ {
		_, err = h.Append(bg, ctxDraft(t, "v"))
	}
	if !errors.Is(err, session.ErrBrokenStore) {
		t.Fatalf("appends past the ring over a foreign record: %v", err)
	}
	release()
	if c := rec.waitClosed(t, h.ID()); !errors.Is(c.Cause, session.ErrBrokenStore) {
		t.Fatalf("closed with %v", c.Cause)
	}
	if got := rec.got(h.ID()); len(got) > 1 {
		t.Fatalf("delivered %d events after the store broke", len(got))
	}
}

// DH-T-SESSION-8: after 100,000 log-only appends behind a blocked listener the
// session's heap is within a fixed bound of the same appends with a listener
// that keeps up: the store's log is the same in both, so the difference is the
// session's own state.
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
			for h.Durable() >= 0 {
				if held, _ := session.HeldFingerprints(h); held == 0 {
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
func TestCloseReportsWriterFailure(t *testing.T) {
	var fs *faultStore
	e := newEnv(t, session.Config{}, func(s store.Store) store.Store { fs = &faultStore{Store: s}; return fs })
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
