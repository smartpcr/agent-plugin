package sqlite_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

// DH-ADR-STORE-3: Close is bounded by the shutdown flush timeout even while a
// write in flight holds the writer mutex, waiting (up to a longer busy timeout)
// on another connection's write lock. It tears nothing down under that write,
// keeps the lease, and a later Close releases it.
func TestCloseIsBoundedWhileAWriteWaitsOnAnotherConnection(t *testing.T) {
	const bound = 200 * time.Millisecond
	path := dbPath(t)
	s, err := sqlite.Open(bg, path, sqlite.WithBusyTimeout(30*time.Second), sqlite.WithShutdownFlushTimeout(bound))
	if err != nil {
		t.Fatal(err)
	}
	w, err := s.Create(bg, store.Header{ID: "s"}, store.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	other, err := fresh(t, path).Conn(bg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Close() })
	if _, err := other.ExecContext(bg, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	rollback := sync.OnceValue(func() error {
		_, err := other.ExecContext(bg, `ROLLBACK`)
		return err
	})
	held := sqlite.WriteHeldSignal(s)
	ctx, busy := sqlite.BusyWaitSignal(t, bg)
	rec := call(t, 0)
	appended, done := make(chan error, 1), make(chan struct{})
	// P-C: the append waits on other's lock, so the cleanup rolls that back
	// (unblock), then joins the append, bounded.
	t.Cleanup(func() {
		_ = rollback()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("the append in flight did not return after the cleanup rolled the other connection back")
		}
	})
	go func() {
		defer close(done)
		appended <- w.Append(ctx, store.Batch{First: 0, Records: []store.Record{rec}})
	}()
	// P-S: the append took the writer mutex, then entered the driver's busy wait
	// on the other connection's lock.
	for _, sig := range []struct {
		what string
		ch   <-chan struct{}
	}{{"took the writer mutex", held}, {"entered the busy wait", busy}} {
		select {
		case <-sig.ch:
		case <-time.After(10 * time.Second):
			t.Fatalf("the append never %s", sig.what)
		}
	}

	start := time.Now()
	err = s.Close(context.Background())
	if took := time.Since(start); took > bound+2*time.Second {
		t.Fatalf("Close took %s with a %s bound", took, bound)
	}
	if codeOf(err) != sqlite.CodeBusy {
		t.Fatalf("Close = %v, want %s naming the write in flight", err, sqlite.CodeBusy)
	}
	if !sqlite.WriteHeld(s) {
		t.Fatal("Close took the writer mutex from the write in flight")
	}

	if err := rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-appended; err != nil {
		t.Fatalf("the append in flight failed: %v", err)
	}
	if err := s.Close(bg); err != nil {
		t.Fatalf("the second Close: %v", err)
	}
	if !s.TornDown() {
		t.Fatal("the second Close did not tear the store down")
	}
	r := open(t, path)
	sum, err := r.Stat(bg, "s")
	if err != nil {
		t.Fatal(err)
	}
	if sum.Holder != nil || sum.NextSeq != 1 {
		t.Fatalf("after the second Close: holder %+v, next seq %d; want released with the append kept", sum.Holder, sum.NextSeq)
	}
}
