package subprocess

import (
	"context"
	"io"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// heldOwner is a real owner whose tree is reported populated while hold is set,
// as a member that outlives the kill would keep it.
type heldOwner struct {
	treeOwner
	hold atomic.Bool
}

func (o *heldOwner) empty() (bool, error) {
	if o.hold.Load() {
		return false, nil
	}
	return o.treeOwner.empty()
}

func (o *heldOwner) describe() string { return "held: " + o.treeOwner.describe() }

// Scenario "Ladder order and survivors" (19.4.3), survivor half: a tree that is
// not gone at the settle deadline is named by Stop's error, its record and its
// registry, which keeps holding it; once it leaves, the next Stop succeeds and the
// registry lets it go.
//
//dsh:test DH-T-EXEC-5
func TestStopPastSettleNamesSurvivorsAndKeepsThemRegistered(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	p, err := Start(context.Background(), Spec{Argv: []string{exe}, Env: Env{"SUBPROCESSTEST_ROLE=stubborn"},
		Launch: sandbox.Trusted("subprocess test: the test binary re-executed"), Stop: StopPlan{Settle: 200 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	held := &heldOwner{}
	p.ownerMu.Lock()
	held.treeOwner = p.owner
	p.owner = held
	p.ownerMu.Unlock()
	held.hold.Store(true)
	t.Cleanup(func() {
		held.hold.Store(false)
		_ = p.StopTree(context.Background(), 0)
	})
	reg := NewRegistry()
	reg.Add(p)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	begin := time.Now()
	err = p.Stop(ctx, nil)
	if herr.Kind(err) != ReasonSurvivors || !strings.Contains(err.Error(), "held: ") {
		t.Fatalf("Stop past settle = %v; want subprocess/tree-survivors naming what is left", err)
	}
	if d := time.Since(begin); d > 10*time.Second {
		t.Fatalf("Stop took %s; the settle deadline is 200ms", d)
	}
	rec := p.StopRecord()
	if herr.Kind(rec.Survivors) != ReasonSurvivors || len(rec.Rungs) != len(Rungs) || !rec.Rungs[4].Ran {
		t.Fatalf("record = %+v; want the survivors and the five rungs", rec)
	}
	if err := reg.StopAll(ctx, 0); herr.Kind(err) != ReasonSurvivors || !slices.Contains(reg.Live(), p) {
		t.Fatalf("StopAll = %v, live %d; want the survivor reported and kept", err, len(reg.Live()))
	}

	held.hold.Store(false)
	if err := p.Stop(ctx, nil); err != nil {
		t.Fatalf("Stop once the tree left: %v", err)
	}
	if rec := p.StopRecord(); rec.Survivors != nil {
		t.Fatalf("survivors after the tree left = %v", rec.Survivors)
	}
	if err := reg.StopAll(ctx, 0); err != nil || len(reg.Live()) != 0 {
		t.Fatalf("StopAll = %v, live %d; want the tree let go", err, len(reg.Live()))
	}
}

// A fed input is closed by the close-input rung: the child reads end of file,
// and the caller's pipe is closed so a pending read ends.
//
//dsh:test DH-T-EXEC-5
func TestInputGateClosesTheFeed(t *testing.T) {
	if gateInput(nil) != nil || gateInput(os.Stdin) != nil {
		t.Fatal("the null device and a file are handed to the child, never gated")
	}
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	pending := gateInput(pr)
	got := make(chan error, 1)
	go func() {
		_, err := pending.Read(make([]byte, 1))
		got <- err
	}()
	t.Cleanup(func() { _ = pending.Close() })
	if err := pending.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-got; err != io.EOF {
		t.Fatalf("a read waiting on the caller's pipe ended with %v; want EOF", err)
	}
	if _, err := pw.Write([]byte("x")); err == nil {
		t.Fatal("the caller's pipe still takes writes after the input was closed")
	}

	g := gateInput(StdinData([]byte("abc")))
	b := make([]byte, 8)
	if n, err := g.Read(b); n != 3 || err != nil {
		t.Fatalf("Read = %d, %v", n, err)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if n, err := g.Read(b); n != 0 || err != io.EOF {
		t.Fatalf("Read after Close = %d, %v; want EOF", n, err)
	}
	if err := g.Close(); err != nil {
		t.Fatalf("a second Close = %v", err)
	}
}
