package sqlite_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// seedFold creates session id with n records and the derived rows cps in one
// batch, and closes its writer.
func seedFold(t *testing.T, s store.Store, id string, n int, cps ...store.Checkpoint) {
	t.Helper()
	w, err := s.Create(bg, store.Header{ID: id}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		recs := make([]store.Record, n)
		for i := range recs {
			recs[i] = call(t, store.Seq(i))
		}
		if err := w.Append(bg, store.Batch{First: 0, Records: recs, Checkpoints: cps}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(bg); err != nil {
		t.Fatal(err)
	}
}

// The fold status query (M1-G-STORE-17): each session with an event is counted
// once per key, current, behind or pending against next_seq; an empty session
// and a deleted one are not counted; a row of another version is pending.
// FoldPending pages the pending sessions in id order.
func TestFoldCountsAndPending(t *testing.T) {
	s := open(t, dbPath(t))
	const k = "test/k"
	seedFold(t, s, "a", 0)
	seedFold(t, s, "b", 3, store.Checkpoint{Key: k, StateVersion: 1, Seq: 2, State: "3"})
	seedFold(t, s, "c", 3, store.Checkpoint{Key: k, StateVersion: 1, Seq: 0, State: "1"})
	seedFold(t, s, "d", 2)
	seedFold(t, s, "e", 2, store.Checkpoint{Key: k, StateVersion: 2, Seq: 1, State: "4"})
	seedFold(t, s, "f", 2)
	if err := s.Delete(bg, "f", store.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}

	got, err := s.FoldCounts(bg, map[string]int{k: 1, "test/other": 1})
	if err != nil {
		t.Fatal(err)
	}
	want := []store.FoldCount{
		{Key: k, StateVersion: 1, Current: 1, Behind: 1, Pending: 2},
		{Key: "test/other", StateVersion: 1, Pending: 4},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("FoldCounts = %+v, want %+v", got, want)
	}
	if got, err := s.FoldCounts(bg, map[string]int{k: 2}); err != nil || !slices.Equal(got, []store.FoldCount{{Key: k, StateVersion: 2, Current: 1, Pending: 3}}) {
		t.Fatalf("FoldCounts at version 2 = %+v, %v", got, err)
	}
	if got, err := s.FoldCounts(bg, nil); err != nil || len(got) != 0 {
		t.Fatalf("FoldCounts of nothing = %+v, %v", got, err)
	}

	var pages [][]string
	after := ""
	for {
		ids, err := s.FoldPending(bg, k, 1, after, 1)
		if err != nil {
			t.Fatal(err)
		}
		if len(ids) == 0 {
			break
		}
		pages = append(pages, ids)
		after = ids[len(ids)-1]
	}
	if len(pages) != 2 || pages[0][0] != "d" || pages[1][0] != "e" {
		t.Fatalf("FoldPending pages = %v, want [d] [e]", pages)
	}
	if ids, err := s.FoldPending(bg, k, 1, "", 0); err != nil || ids != nil {
		t.Fatalf("FoldPending with limit 0 = %v, %v", ids, err)
	}

	done, cancel := context.WithCancel(bg)
	cancel()
	if _, err := s.FoldCounts(done, map[string]int{k: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("FoldCounts on an ended context = %v", err)
	}
	if _, err := s.FoldPending(done, k, 1, "", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("FoldPending on an ended context = %v", err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FoldCounts(bg, map[string]int{k: 1}); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("FoldCounts on a closed store = %v", err)
	}
	if _, err := s.FoldPending(bg, k, 1, "", 1); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("FoldPending on a closed store = %v", err)
	}
}
