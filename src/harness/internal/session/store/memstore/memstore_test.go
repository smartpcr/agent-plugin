package memstore_test

import (
	"context"
	"errors"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/storetest"
)

// DH-T-STORE-1 / TC-05: the conformance suite passes on memstore.
//
//dsh:test DH-T-STORE-1
func TestConformance(t *testing.T) {
	storetest.Run(t, func(*testing.T) store.Store { return memstore.New() })
}

func rec(t *testing.T, seq event.Seq) event.Record {
	t.Helper()
	d, err := event.TurnStartType.Draft(event.TurnStart{Turn: int(seq)})
	if err != nil {
		t.Fatal(err)
	}
	r, err := d.Stamp(seq, int64(seq)+1)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// A crash between "accepted" and "durable" loses exactly the undurable records,
// releases the lease and kills the old handles.
func TestCrashLosesUndurable(t *testing.T) {
	ctx := context.Background()
	s := memstore.New()
	w, err := s.Create(ctx, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ctx, store.Batch{First: 0, Records: []event.Record{rec(t, 0), rec(t, 1)},
		Checkpoints: []store.Checkpoint{{Key: "core/a", Seq: 1, State: "1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := w.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	if err := w.Append(ctx, store.Batch{First: 2, Records: []event.Record{rec(t, 2)},
		Checkpoints: []store.Checkpoint{{Key: "core/a", Seq: 2, State: "2"}}}); err != nil {
		t.Fatal(err)
	}
	if sum, _ := s.Stat(ctx, "s"); sum.NextSeq != 3 || sum.Durable != 2 {
		t.Fatalf("before crash: %+v", sum)
	}
	s.Crash()
	if err := w.Append(ctx, store.Batch{First: 2, Records: []event.Record{rec(t, 2)}}); !errors.Is(err, store.ErrClosed) {
		t.Fatalf("Append after crash = %v", err)
	}
	sum, _ := s.Stat(ctx, "s")
	if sum.NextSeq != 2 || sum.Durable != 2 || sum.Holder != nil {
		t.Fatalf("after crash: %+v", sum)
	}
	w2, err := s.OpenWrite(ctx, "s", store.OpenOptions{})
	if err != nil {
		t.Fatalf("OpenWrite after crash: %v", err)
	}
	cks, _ := w2.Checkpoints(ctx)
	if len(cks) != 1 || cks[0].State != "1" {
		t.Fatalf("checkpoints after crash = %+v", cks)
	}
	if w2.Epoch() <= w.Epoch() {
		t.Fatalf("epoch did not rise: %d -> %d", w.Epoch(), w2.Epoch())
	}
}

// WithRegistry judges appends and opens against another registry.
func TestWithRegistry(t *testing.T) {
	ctx := context.Background()
	s := memstore.New(memstore.WithRegistry(event.NewRegistry()))
	w, _ := s.Create(ctx, store.Header{ID: "s"}, store.CreateOptions{})
	if err := w.Append(ctx, store.Batch{Records: []event.Record{rec(t, 0)}}); !errors.Is(err, event.ErrUnknownType) {
		t.Fatalf("Append against an empty registry = %v", err)
	}
	_ = w.Close(ctx)
	if err := s.Seed(ctx, store.Header{ID: "seeded"}, []event.Record{rec(t, 0)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.OpenRead(ctx, "seeded"); !errors.Is(err, event.ErrUnknownType) {
		t.Fatalf("OpenRead against an empty registry = %v", err)
	}
}
