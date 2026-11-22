package watch_test

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// TestFSNotifier: the real notifier refuses a directory that does not exist,
// names the changed path inside an added directory, and closes without hanging
// when nobody reads its events: Close returns and both channels end.
func TestFSNotifier(t *testing.T) {
	n, err := watch.NewFSNotifier()
	if err != nil {
		t.Skipf("no change notification here: %v", err)
	}
	// Released by the bounded Close below once it has started; before that, here.
	closing := false
	t.Cleanup(func() {
		if !closing {
			n.Close()
		}
	})
	dir := canon(t, t.TempDir())
	if err := n.Add(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("Add of a missing directory succeeded")
	}
	if err := n.Add(dir); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(dir, "f")
	write(t, f, "1")
	for got := ""; got != f; {
		select {
		case got = <-n.Events():
			got = filepath.Clean(got)
		case <-time.After(deadline):
			t.Fatalf("no event for %s within %v", f, deadline)
		}
	}
	if err := n.Remove(filepath.Join(dir, "never-added")); err == nil {
		t.Fatal("Remove of a directory never added succeeded")
	}

	// Leave more events unread than the notifier buffers, so its pump is blocked
	// handing one over when Close is called.
	for i := range 300 {
		write(t, filepath.Join(dir, "e"+strconv.Itoa(i)), "x")
	}
	done := make(chan error, 1)
	closing = true
	go func() { done <- n.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Close: %v", err)
		}
	case <-time.After(deadline):
		t.Fatalf("Close blocked for %v with events unread", deadline)
	}
	drain(t, n.Events(), "events")
	drain(t, n.Errors(), "errors")
}

// drain reads ch until it is closed, bounded by deadline.
func drain[T any](t *testing.T, ch <-chan T, name string) {
	t.Helper()
	stop := time.After(deadline)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-stop:
			t.Fatalf("%s channel not closed within %v of Close", name, deadline)
		}
	}
}
