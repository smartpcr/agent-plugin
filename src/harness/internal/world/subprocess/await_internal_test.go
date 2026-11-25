package subprocess

import (
	"context"
	"os/exec"
	"sync/atomic"
	"testing"
	"time"
)

// stuckOwner is a tree owner whose members never leave; it counts empty() calls.
type stuckOwner struct{ polls atomic.Int64 }

func (o *stuckOwner) start(func() *exec.Cmd) (*exec.Cmd, error) { return nil, nil }
func (o *stuckOwner) report() Owner                             { return Owner{Kind: ProcessGroup} }
func (o *stuckOwner) polite() error                             { return nil }
func (o *stuckOwner) kill() error                               { return nil }
func (o *stuckOwner) empty() (bool, error)                      { o.polls.Add(1); return false, nil }
func (o *stuckOwner) describe() string                          { return "stuck" }
func (o *stuckOwner) release()                                  {}
func (o *stuckOwner) limitsReport() LimitsReport                { return LimitsReport{State: LimitsNone} }
func (o *stuckOwner) member(int) bool                           { return false }

// With the direct child gone and a descendant left, await polls at pollEvery
// rather than spinning on the closed exited channel.
//
//dsh:test DH-T-NODEPROC-5
func TestAwaitDoesNotSpinAfterChildExit(t *testing.T) {
	o := &stuckOwner{}
	p := &Process{owner: o, exited: make(chan struct{}), releasedC: make(chan struct{})}
	close(p.exited)
	const wait = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	if err := p.await(ctx); err == nil {
		t.Fatal("await returned nil for a tree that never empties")
	}
	if max := int64(wait/pollEvery) * 3; o.polls.Load() > max {
		t.Fatalf("await polled the owner %d times in %v; want at most %d", o.polls.Load(), wait, max)
	}
}
