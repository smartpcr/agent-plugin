package subprocess

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// StopAll returns by its context's end even when a tree's stop cannot finish:
// the tree still stopping is killed once more, named by pid, and left
// registered, and its stop goes on in the background. Holding the tree's stop
// lock stands in for a StopTree that another caller is still running.
//
//dsh:test DH-T-EXEC-5
func TestStopAllReturnsAtTheContextEnd(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Start(context.Background(), Spec{Argv: []string{exe}, Env: Env{"SUBPROCESSTEST_ROLE=stubborn"}, Launch: sandbox.Trusted("test fixture")})
	if err != nil {
		t.Fatal(err)
	}
	locked := true
	p.stopMu.Lock()
	t.Cleanup(func() {
		if locked {
			p.stopMu.Unlock()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := p.StopTree(ctx, 0); err != nil {
			t.Errorf("cleanup StopTree: %v", err)
		}
	})
	r := NewRegistry()
	remove := r.Add(p)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = r.StopAll(ctx, time.Minute)
	if err == nil || !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "process trees "+strconv.Itoa(p.Pid())+" still stopping") {
		t.Fatalf("StopAll with an ended context = %v; want the pid still stopping and context.Canceled", err)
	}
	if live := r.Live(); len(live) != 1 || live[0] != p {
		t.Fatalf("Live after StopAll = %v; want the tree still registered", live)
	}
	// The forced stop went out although the stop lock was held.
	wctx, wcancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer wcancel()
	if err := p.TreeGone(wctx); err != nil {
		t.Fatalf("TreeGone after StopAll's kill: %v", err)
	}
	p.stopMu.Unlock()
	locked = false
	remove()
	remove()
	if live := r.Live(); len(live) != 0 {
		t.Fatalf("Live after remove = %v; want none", live)
	}
}
