package blob_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/blob"
)

// The session store is the index ReferencedBy reads.
var _ blob.Index = (*sqlite.Store)(nil)

// A saver holds gc.lock shared: while the collector holds it, Put and Touch
// wait (and give up with their context), and they go on once it is released;
// savers do not exclude each other, and a saver excludes the collector.
func TestSaversWaitForTheCollector(t *testing.T) {
	s, _ := newStore(t)
	ref := put(t, s, "kept")
	gc, err := s.LockCollection(bg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(bg, 50*time.Millisecond)
	defer cancel()
	if _, err := s.Put(ctx, blob.KindFile, strings.NewReader("new"), 0); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Put under the collection lock: %v, want the deadline", err)
	}
	if err := s.Touch(ctx, ref); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Touch under the collection lock: %v, want the deadline", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.Put(bg, blob.KindFile, strings.NewReader("new"), 0)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("Put returned while the collector held the lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := gc.Unlock(); err != nil {
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
	pr, pw := io.Pipe()
	go func() {
		_, err := s.Put(bg, blob.KindData, pr, 0)
		done <- err
	}()
	waitFor(t, func() bool {
		c, cancel := context.WithTimeout(bg, 5*time.Millisecond)
		defer cancel()
		l, err := s.LockCollection(c)
		if err == nil {
			_ = l.Unlock()
		}
		return err != nil
	})
	put(t, s, "concurrent saver")
	_ = pw.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	gc, err = s.LockCollection(bg)
	if err != nil {
		t.Fatalf("collector after the savers: %v", err)
	}
	_ = gc.Unlock()
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

// waitFor polls cond until it holds, failing the test after five seconds.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for end := time.Now().Add(5 * time.Second); !cond(); {
		if time.Now().After(end) {
			t.Fatal("condition not reached")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
