package memstore_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
)

// seedFold creates session id with n records and the derived rows cps in one
// batch; closing the writer makes them durable.
func seedFold(t *testing.T, s *memstore.Store, id string, n int, closed bool, cps ...store.Checkpoint) {
	t.Helper()
	ctx := context.Background()
	w, err := s.Create(ctx, store.Header{ID: id}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		recs := make([]store.Record, n)
		for i := range recs {
			recs[i] = rec(t, store.Seq(i))
		}
		if err := w.Append(ctx, store.Batch{First: 0, Records: recs, Checkpoints: cps}); err != nil {
			t.Fatal(err)
		}
	}
	if closed {
		if err := w.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
}

// FoldCounts and FoldPending count what another process would see: durable
// records and rows. An empty, an undurable and a deleted session are not counted.
func TestFoldCountsAndPending(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	const k = "test/k"
	seedFold(t, s, "a", 0, true)
	seedFold(t, s, "b", 3, true, store.Checkpoint{Key: k, StateVersion: 1, Seq: 2, State: "3"})
	seedFold(t, s, "c", 3, true, store.Checkpoint{Key: k, StateVersion: 1, Seq: 0, State: "1"})
	seedFold(t, s, "d", 2, true)
	seedFold(t, s, "e", 2, true, store.Checkpoint{Key: k, StateVersion: 2, Seq: 1, State: "4"})
	seedFold(t, s, "u", 2, false)
	seedFold(t, s, "x", 2, true)
	if err := s.Delete(ctx, "x", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := s.FoldCounts(ctx, map[string]int{k: 1, "test/other": 1})
	want := []store.FoldCount{
		{Key: k, StateVersion: 1, Current: 1, Behind: 1, Pending: 2},
		{Key: "test/other", StateVersion: 1, Pending: 4},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Fatalf("FoldCounts = %+v, %v; want %+v", got, err, want)
	}
	if ids, err := s.FoldPending(ctx, k, 1, "", 10); err != nil || !slices.Equal(ids, []string{"d", "e"}) {
		t.Fatalf("FoldPending = %v, %v; want [d e]", ids, err)
	}
	if ids, err := s.FoldPending(ctx, k, 1, "d", 1); err != nil || !slices.Equal(ids, []string{"e"}) {
		t.Fatalf("FoldPending after d = %v, %v; want [e]", ids, err)
	}
	if ids, err := s.FoldPending(ctx, k, 1, "", 0); err != nil || ids != nil {
		t.Fatalf("FoldPending with limit 0 = %v, %v", ids, err)
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.FoldCounts(done, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("FoldCounts on an ended context = %v", err)
	}
	if _, err := s.FoldPending(done, k, 1, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("FoldPending on an ended context = %v", err)
	}
}
