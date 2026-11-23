package sqlite

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

var obg = context.Background()

// openBoundStore is a fresh store in a temporary home, closed with the test.
func openBoundStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(obg, filepath.Join(t.TempDir(), "dsh.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(obg); err != nil {
			t.Error(err)
		}
	})
	return s
}

// boundCall is a tool/call record at seq.
func boundCall(t *testing.T, seq store.Seq) store.Record {
	t.Helper()
	d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", seq), Name: "grep", Arguments: `{"q":"x"}`})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := d.Stamp(seq, 1_000+int64(seq))
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

// countOpens counts the records each open reads and judges, until the test
// ends.
func countOpens(t *testing.T) *[]int {
	t.Helper()
	var got []int
	old := onOpenCheck
	onOpenCheck = func(n int) { got = append(got, n) }
	t.Cleanup(func() { onOpenCheck = old })
	return &got
}

// An open is bounded by what restoring reads (DH-T-STORE-18, "opening one
// session ... from its checkpoints"): with a checkpoint near the head, OpenRead
// and OpenWrite read and judge the records from the newest checkpoint's seq to
// the head, the same number for 100, 1,000 and 10,000 events, and a reader
// still returns the whole log. A session with no checkpoint is judged whole at
// the open, as before.
//
//dsh:test DH-T-STORE-18
func TestOpenIsBoundedByTheNewestCheckpoint(t *testing.T) {
	got := countOpens(t)
	for _, n := range []int{100, 1_000, 10_000} {
		s := openBoundStore(t)
		w, err := s.Create(obg, store.Header{ID: "s"}, store.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		const batch = 1_000
		for first := 0; first < n; first += batch {
			b := store.Batch{First: store.Seq(first)}
			for i := first; i < min(first+batch, n); i++ {
				b.Records = append(b.Records, boundCall(t, store.Seq(i)))
			}
			if first+batch >= n {
				// The newest checkpoint reflects the log through seq n-2.
				b.Checkpoints = []store.Checkpoint{{Key: "core/x", Seq: store.Seq(n - 2), State: "{}"},
					{Key: "core/y", Seq: store.Seq(n / 2), State: "{}"}}
			}
			if err := w.Append(obg, b); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(obg); err != nil {
			t.Fatal(err)
		}

		*got = nil
		r, err := s.OpenRead(obg, "s")
		if err != nil {
			t.Fatal(err)
		}
		ow, err := s.OpenWrite(obg, "s", store.OpenOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if want := []int{2, 2}; len(*got) != 2 || (*got)[0] != want[0] || (*got)[1] != want[1] {
			t.Fatalf("%d events: OpenRead and OpenWrite judged %v records, want %v (seq n-2 and the head)", n, *got, want)
		}
		for name, h := range map[string]store.Reader{"reader": r, "writer": ow} {
			recs, err := h.Read(obg, 0, 0, store.ReadOptions{})
			if err != nil || len(recs) != n {
				t.Fatalf("%d events: the %s read %d records, %v", n, name, len(recs), err)
			}
		}
		// The writer's own appends need no judging again.
		if err := ow.Append(obg, store.Batch{First: store.Seq(n), Records: []store.Record{boundCall(t, store.Seq(n))}}); err != nil {
			t.Fatal(err)
		}
		if recs, err := ow.ReadSeqs(obg, []store.Seq{store.Seq(n)}, store.ReadOptions{}); err != nil || len(recs) != 1 {
			t.Fatalf("%d events: the writer read its append: %d, %v", n, len(recs), err)
		}
		_ = r.Close(obg)
		if err := ow.Close(obg); err != nil {
			t.Fatal(err)
		}
	}

	// No checkpoint: the open judges the whole log.
	s := openBoundStore(t)
	recs := []store.Record{boundCall(t, 0), boundCall(t, 1), boundCall(t, 2)}
	if err := s.Seed(obg, store.Header{ID: "plain"}, recs); err != nil {
		t.Fatal(err)
	}
	*got = nil
	r, err := s.OpenRead(obg, "plain")
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Close(obg)
	if len(*got) != 1 || (*got)[0] != len(recs) {
		t.Fatalf("an open with no checkpoint judged %v records, want [%d]", *got, len(recs))
	}
}

// The read rule moved, not weakened (DH-T-STORE-10: an unknown required type
// and a newer payload version refuse with their direction): a refused record
// at or past the newest checkpoint's seq refuses the open itself, by its first
// position; one below it, which restoring does not read, lets the open succeed
// and refuses every read that would return it, Read and ReadSeqs, on a reader
// and on a writer, while the records around it still read.
//
//dsh:test DH-T-STORE-10
func TestReadsJudgeWhatTheOpenDidNot(t *testing.T) {
	s := openBoundStore(t)
	unknown := store.Record{Seq: 1, Time: 1_001, Type: "future/thing", Version: 1, Payload: `{}`}
	newer := boundCall(t, 3)
	newer.Version = event.ToolCall.Version() + 1

	// A refused record below the checkpoint (seq 2).
	below := []store.Record{boundCall(t, 0), unknown, boundCall(t, 2), boundCall(t, 3)}
	// A refused record past it.
	past := []store.Record{boundCall(t, 0), boundCall(t, 1), boundCall(t, 2), newer}
	for id, recs := range map[string][]store.Record{"below": below, "past": past} {
		if err := s.Seed(obg, store.Header{ID: id}, recs); err != nil {
			t.Fatal(err)
		}
		if n, err := s.UpsertDerived(obg, id, []store.Checkpoint{{Key: "core/x", Seq: 2, State: "{}"}}); err != nil || n != 1 {
			t.Fatalf("UpsertDerived(%s) = %d, %v", id, n, err)
		}
	}

	_, err := s.OpenRead(obg, "past")
	var np *event.NewerPayloadError
	if !errors.As(err, &np) || np.Seq != 3 {
		t.Fatalf("OpenRead(past) = %v, want *NewerPayloadError at seq 3", err)
	}
	if _, err := s.OpenWrite(obg, "past", store.OpenOptions{}); !errors.Is(err, event.ErrNewerPayload) {
		t.Fatalf("OpenWrite(past) = %v, want %v", err, event.ErrNewerPayload)
	}

	r, err := s.OpenRead(obg, "below")
	if err != nil {
		t.Fatalf("OpenRead(below): %v", err)
	}
	defer func() { _ = r.Close(obg) }()
	w, err := s.OpenWrite(obg, "below", store.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenWrite(below): %v", err)
	}
	defer func() { _ = w.Close(obg) }()
	for name, h := range map[string]store.Reader{"reader": r, "writer": w} {
		for _, o := range []store.ReadOptions{{}, {WithAux: true}} {
			_, err := h.Read(obg, 0, 0, o)
			var ut *event.UnknownTypeError
			if !errors.As(err, &ut) || ut.Type != "future/thing" || ut.Seq != 1 {
				t.Fatalf("%s: Read(0) %+v = %v, want *UnknownTypeError at seq 1", name, o, err)
			}
			if _, err := h.ReadSeqs(obg, []store.Seq{2, 1}, o); !errors.Is(err, event.ErrUnknownType) {
				t.Fatalf("%s: ReadSeqs(2, 1) %+v = %v, want %v", name, o, err, event.ErrUnknownType)
			}
			if recs, err := h.Read(obg, 2, 0, o); err != nil || len(recs) != 2 {
				t.Fatalf("%s: Read(2) %+v = %d records, %v", name, o, len(recs), err)
			}
			if recs, err := h.ReadSeqs(obg, []store.Seq{0, 3}, o); err != nil || len(recs) != 2 {
				t.Fatalf("%s: ReadSeqs(0, 3) %+v = %d records, %v", name, o, len(recs), err)
			}
		}
	}
}
