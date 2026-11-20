package watchtest

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/watch"
)

// TestManualCancelDuringRegistration: a context cancelled while its watch is
// being registered leaves no watch behind and never races (run with -race).
func TestManualCancelDuringRegistration(t *testing.T) {
	m := NewManual()
	path := filepath.Join(t.TempDir(), "f")
	var wg sync.WaitGroup
	for range 500 {
		ctx, cancel := context.WithCancel(t.Context())
		wg.Go(cancel)
		wg.Go(func() {
			if w, err := (engine{m}).Watch(ctx, watch.Target{Path: path}, func(error) {}); err == nil {
				w.Mode()
			}
		})
	}
	wg.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for len(m.Watching()) != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("%d watches outlived their cancelled contexts", len(m.Watching()))
		}
		<-time.After(time.Millisecond)
	}
	if n := m.Fire(path); n != 0 {
		t.Fatalf("cancelled watches told: %d", n)
	}
}
