package subprocess_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// slack is how much longer than its bound a rung may take on a loaded machine.
const slack = 2 * time.Second

// stubbornTree starts a RoleTree child (its grandchild ignores the polite stop)
// with plan, fed by a pipe the host holds, and returns it once both are ready.
func stubbornTree(t *testing.T, plan subprocess.StopPlan) (p *subprocess.Process, child, grandchild int) {
	t.Helper()
	spec := subprocesstest.Spec(t, subprocesstest.RoleTree)
	in, feed := io.Pipe()
	t.Cleanup(func() { _ = feed.Close() })
	spec.Stdin = in
	spec.Stop = plan
	out, w := io.Pipe()
	spec.Stdout = w
	p = subprocesstest.Start(t, spec)
	t.Cleanup(func() { _ = out.Close() })
	line, err := bufio.NewReader(out).ReadString('\n')
	if _, serr := fmt.Sscanf(line, "tree %d %d", &child, &grandchild); err != nil || serr != nil {
		t.Fatalf("tree child said %q: %v %v", line, err, serr)
	}
	return p, child, grandchild
}

// Scenario "Ladder order and survivors" (19.4.3), order half: Stop runs revoke,
// ask, close input, terminate and kill in that order on a stubborn tree, each
// bounded by its grace, then the tree is gone. The rungs are logged as the
// ladder's evidence line.
//
//dsh:test DH-T-EXEC-5
func TestStopRunsTheLadderInOrder(t *testing.T) {
	const grace = 150 * time.Millisecond
	var mu sync.Mutex
	var hooks []string
	note := func(name string) func(context.Context) error {
		return func(context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			hooks = append(hooks, name)
			return nil
		}
	}
	p, child, grandchild := stubbornTree(t, subprocess.StopPlan{Revoke: note("revoke"), Ask: note("ask"), Grace: grace, Settle: 10 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	userStop := errors.New("the user pressed stop")
	if err := p.Stop(ctx, userStop); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	rec := p.StopRecord()
	for _, r := range rec.Rungs {
		t.Logf("ladder: %-11s ran=%v took=%s gone=%v note=%q err=%v", r.Rung, r.Ran, r.Took.Round(time.Millisecond), r.Gone, r.Note, r.Err)
	}
	var names []subprocess.Rung
	for _, r := range rec.Rungs {
		names = append(names, r.Rung)
	}
	if !slices.Equal(names, subprocess.Rungs) {
		t.Fatalf("rungs = %v; want %v", names, subprocess.Rungs)
	}
	mu.Lock()
	gotHooks := slices.Clone(hooks)
	mu.Unlock()
	if !slices.Equal(gotHooks, []string{"revoke", "ask"}) {
		t.Fatalf("hooks ran %v; want revoke, then ask", gotHooks)
	}
	for _, r := range rec.Rungs[:4] {
		if !r.Ran || r.Err != nil || r.Took > grace+slack {
			t.Errorf("rung %s: ran %v, err %v, took %s; want it run within %s", r.Rung, r.Ran, r.Err, r.Took, grace)
		}
	}
	if kill := rec.Rungs[4]; !kill.Ran {
		t.Errorf("the kill rung did not run on a tree whose grandchild ignores the polite stop: %+v", kill)
	}
	if !errors.Is(rec.Cause, userStop) || rec.Survivors != nil {
		t.Errorf("record cause %v, survivors %v; want the user's stop and none", rec.Cause, rec.Survivors)
	}
	if !subprocesstest.Exited(child) || !subprocesstest.Exited(grandchild) {
		t.Fatalf("after Stop: child exited %v, grandchild exited %v", subprocesstest.Exited(child), subprocesstest.Exited(grandchild))
	}
	if err := p.Stop(ctx, errors.New("again")); err != nil || !errors.Is(p.StopCause(), userStop) {
		t.Fatalf("a second Stop = %v, cause %v; want nil and the first cause kept", err, p.StopCause())
	}
}

// A hook that does not return is cut at its bound: the rung reports the bound's
// error and the ladder goes on to stop the tree.
//
//dsh:test DH-T-EXEC-5
func TestStopBoundsAStuckHook(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	stuck := func(context.Context) error { <-release; return nil }
	const grace = 100 * time.Millisecond
	p, _, _ := stubbornTree(t, subprocess.StopPlan{Ask: stuck, Grace: grace})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.Stop(ctx, nil); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	ask := p.StopRecord().Rungs[1]
	if ask.Rung != subprocess.RungAsk || !errors.Is(ask.Err, context.DeadlineExceeded) || ask.Took > 2*grace+slack {
		t.Fatalf("ask rung = %+v; want it cut at its %s bound", ask, grace)
	}
	if !errors.Is(p.StopCause(), subprocess.ErrStopped) {
		t.Fatalf("StopCause = %v; want ErrStopped for a stop with no cause", p.StopCause())
	}
}

// Each way a tree is stopped gives its own first cause (DH-T-EXEC-6): the start
// context's deadline, its cancellation with and without a cause, and a user's
// Stop; a later stop keeps the first. On Windows no polite signal exists for a
// pipe child, and the cause is recorded all the same.
//
//dsh:test DH-T-EXEC-6
func TestStopCausesAreTheirOwn(t *testing.T) {
	userCancel := errors.New("the user cancelled the turn")
	for _, c := range []struct {
		name  string
		start func() (context.Context, func())
		want  error
	}{
		{"deadline", func() (context.Context, func()) {
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			t.Cleanup(cancel)
			return ctx, func() {} // the deadline ends it
		}, context.DeadlineExceeded},
		{"cancel", func() (context.Context, func()) {
			ctx, cancel := context.WithCancel(context.Background())
			return ctx, cancel
		}, context.Canceled},
		{"cancel with cause", func() (context.Context, func()) {
			ctx, cancel := context.WithCancelCause(context.Background())
			return ctx, func() { cancel(userCancel) }
		}, userCancel},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx, end := c.start()
			t.Cleanup(end)
			p, err := subprocess.Start(ctx, subprocesstest.Spec(t, subprocesstest.RoleStubborn))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = p.Kill() })
			end()
			wait, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := p.TreeGone(wait); err != nil {
				t.Fatalf("TreeGone after the context ended: %v", err)
			}
			if err := p.StopTree(wait, 0); err != nil || !errors.Is(p.StopCause(), c.want) {
				t.Fatalf("StopCause = %v (StopTree %v); want %v", p.StopCause(), err, c.want)
			}
		})
	}
	t.Run("user stop", func(t *testing.T) {
		p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleStubborn))
		stop := errors.New("stop pressed")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := p.Stop(ctx, stop); err != nil {
			t.Fatal(err)
		}
		if err := p.StopTree(ctx, 0); err != nil || !errors.Is(p.StopCause(), stop) {
			t.Fatalf("StopCause = %v (%v); want the user's stop kept", p.StopCause(), err)
		}
	})
	t.Run("not stopped", func(t *testing.T) {
		p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleExit))
		if err := p.Wait(); err != nil || p.StopCause() != nil {
			t.Fatalf("Wait %v, StopCause %v; want no cause for a tree that ended on its own", err, p.StopCause())
		}
	})
}

// The revoke rung runs even for a tree that has already left, since the
// authority it was given outlives it; the other rungs have nothing to do.
//
//dsh:test DH-T-EXEC-5
func TestStopRevokesEvenWhenTheTreeIsGone(t *testing.T) {
	revoked := 0
	spec := subprocesstest.Spec(t, subprocesstest.RoleExit)
	spec.Stop = subprocess.StopPlan{Revoke: func(context.Context) error { revoked++; return nil }}
	p := subprocesstest.Start(t, spec)
	if err := p.Wait(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := p.Stop(ctx, nil); err != nil {
		t.Fatal(err)
	}
	rungs := p.StopRecord().Rungs
	if revoked != 1 || !rungs[0].Ran {
		t.Fatalf("revoked %d times, rungs %+v; want the revoke run once", revoked, rungs)
	}
	for _, r := range rungs[1:] {
		if r.Ran || !r.Gone {
			t.Errorf("rung %+v ran on a gone tree", r)
		}
	}
}
