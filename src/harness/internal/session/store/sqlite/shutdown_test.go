package sqlite_test

import (
	"context"
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
	defer other.Close()
	if _, err := other.ExecContext(bg, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	appended := make(chan error, 1)
	go func() { appended <- w.Append(bg, store.Batch{First: 0, Records: []store.Record{call(t, 0)}}) }()
	for deadline := time.Now().Add(10 * time.Second); !sqlite.WriteHeld(s); {
		if time.Now().After(deadline) {
			t.Fatal("the append never took the writer mutex")
		}
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond) // let it reach the busy wait on the other connection

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

	if _, err := other.ExecContext(bg, `ROLLBACK`); err != nil {
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
