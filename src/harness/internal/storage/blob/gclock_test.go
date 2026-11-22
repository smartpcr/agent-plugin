package blob_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/blob"
)

// The session store is the index ReferencedBy reads.
var _ blob.Index = (*sqlite.Store)(nil)

// A saver holds gc.lock shared: while the collector holds it, Put and Touch
// wait (and give up with their context), and they go on once it is released;
// savers do not exclude each other, and a saver excludes the collector.
//
// P-V: gc.lock is taken by non-blocking tries with a timer between them, so in
// the bubble the waits are virtual and "Put is still waiting" is synctest.Wait.
func TestSaversWaitForTheCollector(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, _ := newStore(t)
		ref := put(t, s, "kept")
		gc, err := s.LockCollection(bg)
		if err != nil {
			t.Fatal(err)
		}
		unlockGC := sync.OnceValue(gc.Unlock)
		ctx, cancel := context.WithTimeout(bg, 50*time.Millisecond)
		defer cancel()
		if _, err := s.Put(ctx, blob.KindFile, strings.NewReader("new"), 0); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Put under the collection lock: %v, want the deadline", err)
		}
		if err := s.Touch(ctx, ref); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Touch under the collection lock: %v, want the deadline", err)
		}
		done := make(chan error, 1)
		waiter := make(chan struct{})
		// P-C: the saver waits for the collector, so the cleanup releases the
		// collection lock, then joins the saver.
		t.Cleanup(func() { _ = unlockGC(); <-waiter })
		go func() {
			defer close(waiter)
			_, err := s.Put(bg, blob.KindFile, strings.NewReader("new"), 0)
			done <- err
		}()
		synctest.Wait() // the saver is blocked between its tries of gc.lock
		select {
		case err := <-done:
			t.Fatalf("Put returned while the collector held the lock: %v", err)
		default:
		}
		if err := unlockGC(); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Put did not go on once the collector released the lock")
		}

		// A saver mid-Put (its source blocked) holds the lock: a second saver
		// proceeds, the collector waits.
		held := make(chan struct{}, 1)
		blob.SetAfterLockShared(t, func() {
			select {
			case held <- struct{}{}:
			default:
			}
		})
		pr, pw := io.Pipe()
		closeSource := sync.OnceValue(pw.Close)
		saver := make(chan struct{})
		// P-C: the saver blocks on its source, so the cleanup closes it, then
		// joins the saver.
		t.Cleanup(func() { _ = closeSource(); <-saver })
		go func() {
			defer close(saver)
			_, err := s.Put(bg, blob.KindData, pr, 0)
			done <- err
		}()
		// P-S: the lock says when the saver holds it shared.
		select {
		case <-held:
		case <-time.After(5 * time.Second):
			t.Fatal("the saver never took gc.lock shared")
		}
		c, cancelC := context.WithTimeout(bg, 5*time.Millisecond)
		defer cancelC()
		if l, err := s.LockCollection(c); !errors.Is(err, context.DeadlineExceeded) {
			if err == nil {
				_ = l.Unlock()
			}
			t.Fatalf("collector while a saver holds the lock: %v, want the deadline", err)
		}
		put(t, s, "concurrent saver")
		_ = closeSource()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		gc, err = s.LockCollection(bg)
		if err != nil {
			t.Fatalf("collector after the savers: %v", err)
		}
		_ = gc.Unlock()
	})
}

type fakeIndex map[string]bool

func (f fakeIndex) Cites(_ context.Context, session, ref string) (bool, error) {
	return f[session+" "+ref], nil
}

// ReferencedBy asks the index with the reference's blob_refs form; without an
// index it refuses, and a malformed reference is cited by nobody.
func TestReferencedByReadsTheIndex(t *testing.T) {
	s, l := newStore(t)
	ref := put(t, s, "cited")
	if _, err := s.ReferencedBy(bg, "s1", ref); !errors.Is(err, blob.ErrNoIndex) {
		t.Fatalf("ReferencedBy without an index: %v, want ErrNoIndex", err)
	}
	ix, err := blob.New(l, blob.WithIndex(fakeIndex{"s1 " + ref.String(): true}))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		session string
		ref     blob.Ref
		want    bool
	}{{"s1", ref, true}, {"s2", ref, false}, {"s1", blob.Ref{Kind: "bogus", Digest: ref.Digest}, false}} {
		if got, err := ix.ReferencedBy(bg, c.session, c.ref); err != nil || got != c.want {
			t.Fatalf("ReferencedBy(%s, %s) = %v, %v; want %v", c.session, c.ref, got, err, c.want)
		}
	}
}
