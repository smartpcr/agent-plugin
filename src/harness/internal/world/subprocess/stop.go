package subprocess

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// The stop ladder (DH-D-EXEC-2, DH-ADR-EXEC-2, step 19.4.3): Stop runs five
// rungs in this order, each bounded, and then waits for the tree up to the
// settle deadline.
const (
	// RungRevoke is the consumer's hook that takes back the authority the command
	// was given (a hosting tool revokes its script environment).
	RungRevoke Rung = "revoke"
	// RungAsk is the consumer's protocol request to stop, then up to Grace for the
	// tree to leave.
	RungAsk Rung = "ask"
	// RungCloseInput closes the child's standard input when the host feeds it,
	// then waits up to Grace. On Windows it is the polite stop of a pipe child.
	RungCloseInput Rung = "close-input"
	// RungTerminate asks every member to stop (SIGTERM on Unix; nothing to send
	// on Windows), then waits up to Grace.
	RungTerminate Rung = "terminate"
	// RungKill is the forced stop: the job object or control group is killed, or
	// SIGKILL goes to the process group; the settle wait follows.
	RungKill Rung = "kill"
)

// Rung names one step of the stop ladder.
type Rung string

// Rungs is the ladder in the order Stop runs it.
var Rungs = []Rung{RungRevoke, RungAsk, RungCloseInput, RungTerminate, RungKill}

// DefaultHookBound bounds the Revoke and Ask hooks when the plan's Grace is zero.
const DefaultHookBound = time.Second

// ErrStopped is the stop cause of a stop asked with no cause of its own: a
// StopTree call, Kill, or Stop(ctx, nil).
var ErrStopped = errors.New("stopped by the host")

// StopPlan is how a tree is stopped (Spec.Stop). Every field is optional.
type StopPlan struct {
	// Revoke takes back the command's authority; it runs first, even for a tree
	// that has already left, bounded by Grace
	// (DefaultHookBound when Grace is zero) and by Stop's context.
	Revoke func(ctx context.Context) error
	// Ask is the protocol request to stop, bounded the same way; the tree then
	// has up to Grace to leave.
	Ask func(ctx context.Context) error
	// Grace is the wait after each of ask, close input and terminate. Zero runs
	// those rungs without waiting, so the kill follows at once. When it is zero,
	// Spec.Grace is used.
	Grace time.Duration
	// Settle bounds the wait for the tree after the kill. Zero waits until Stop's
	// context ends, or DefaultSettle when it has no deadline.
	Settle time.Duration
}

// RungResult is what one rung of a stop did.
type RungResult struct {
	Rung Rung
	// Ran is false when the rung had nothing to do: the tree was already gone,
	// there was no hook, or no input to close; Note says which.
	Ran  bool
	Note string
	// Took is how long the rung ran, its wait included.
	Took time.Duration
	// Err is the hook's or the owner's failure; a hook past its bound reports
	// the bound's error.
	Err error
	// Gone says the tree was gone when the rung ended.
	Gone bool
}

// StopRecord is what the stops of a tree did: the first cause and the rungs of
// each attempt, in order. Survivors is the error of the last attempt that did
// not see the tree gone; nil once a stop saw it gone.
type StopRecord struct {
	Cause     error
	Rungs     []RungResult
	Survivors error
}

// Stop runs the stop ladder with the tree's StopPlan and returns once every
// member has exited. cause is recorded as the stop cause unless an earlier stop
// recorded one (nil records ErrStopped). Past the settle deadline it returns
// subprocess/tree-survivors naming what is left, the handle stays registered and
// a later call runs the ladder again. Once the tree is gone it is idempotent.
func (p *Process) Stop(ctx context.Context, cause error) error {
	if p.detached {
		return errDetached("Stop")
	}
	return p.stop(ctx, cause, p.plan)
}

// StopTree is Stop with the plan's Grace replaced by grace and no cause of its
// own (ErrStopped), for the callers of the two-rung stop it replaced.
func (p *Process) StopTree(ctx context.Context, grace time.Duration) error {
	if p.detached {
		return errDetached("StopTree")
	}
	plan := p.plan
	plan.Grace = grace
	return p.stop(ctx, nil, plan)
}

// StopCause is the cause of the first stop: the context's cause when the start
// context ended (context.DeadlineExceeded for a deadline), the cause given to
// Stop, or ErrStopped; nil while nothing stopped the tree.
func (p *Process) StopCause() error {
	if c := p.cause.Load(); c != nil {
		return c.err
	}
	return nil
}

// StopRecord returns what the stops did so far.
func (p *Process) StopRecord() StopRecord {
	p.recordMu.Lock()
	defer p.recordMu.Unlock()
	return StopRecord{Cause: p.StopCause(), Rungs: append([]RungResult(nil), p.rungs...), Survivors: p.lastSurvivors}
}

type causeBox struct{ err error }

func (p *Process) setCause(cause error) {
	if cause == nil {
		cause = ErrStopped
	}
	p.cause.CompareAndSwap(nil, &causeBox{cause})
}

func (p *Process) stop(ctx context.Context, cause error, plan StopPlan) error {
	p.setCause(cause)
	p.stopMu.Lock()
	defer p.stopMu.Unlock()
	if p.stopped {
		return nil
	}
	hookBound := plan.Grace
	if hookBound <= 0 {
		hookBound = DefaultHookBound
	}
	p.rung(ctx, RungRevoke, hookAct(plan.Revoke, hookBound), 0)
	p.rung(ctx, RungAsk, hookAct(plan.Ask, hookBound), plan.Grace)
	p.rung(ctx, RungCloseInput, p.closeInput, plan.Grace)
	p.rung(ctx, RungTerminate, func(context.Context) (bool, string, error) {
		return true, "", p.withOwner(treeOwner.polite)
	}, plan.Grace)
	p.rung(ctx, RungKill, func(context.Context) (bool, string, error) {
		return true, "", p.withOwner(treeOwner.kill)
	}, 0)
	sctx, cancel := settle(ctx, plan.Settle)
	defer cancel()
	if err := p.await(sctx); err != nil {
		// One more forced stop: a member may have started after the first.
		_ = p.withOwner(treeOwner.kill)
		serr := p.survivors(err)
		p.recordMu.Lock()
		p.lastSurvivors = serr
		p.recordMu.Unlock()
		return serr
	}
	p.recordMu.Lock()
	p.lastSurvivors = nil
	p.recordMu.Unlock()
	p.stopped = true
	p.releaseOwner()
	return nil
}

// rungAct performs a rung; ran is false when it had nothing to do, with a note.
type rungAct func(ctx context.Context) (ran bool, note string, err error)

// rung runs act unless the tree is gone, then waits up to wait for the tree to
// leave, and records the result. The revoke rung runs even then: authority the
// command was given is taken back whether or not the command is still running.
func (p *Process) rung(ctx context.Context, r Rung, act rungAct, wait time.Duration) {
	res := RungResult{Rung: r}
	begin := time.Now()
	if r != RungRevoke && p.gone() {
		res.Note, res.Gone = "the tree was already gone", true
	} else {
		res.Ran, res.Note, res.Err = act(ctx)
		if res.Ran && wait > 0 {
			wctx, cancel := context.WithTimeout(ctx, wait)
			_ = p.await(wctx)
			cancel()
		}
		res.Gone = p.gone()
	}
	res.Took = time.Since(begin)
	p.recordMu.Lock()
	p.rungs = append(p.rungs, res)
	p.recordMu.Unlock()
}

// hookAct runs a consumer hook bounded by bound: past it the rung ends with the
// bound's error and the hook is left to return on its own.
func hookAct(hook func(context.Context) error, bound time.Duration) rungAct {
	return func(ctx context.Context) (bool, string, error) {
		if hook == nil {
			return false, "no hook", nil
		}
		hctx, cancel := context.WithTimeout(ctx, bound)
		defer cancel()
		done := make(chan error, 1)
		go func() { done <- hook(hctx) }()
		select {
		case err := <-done:
			return true, "", err
		case <-hctx.Done():
			return true, "the hook passed its bound", context.Cause(hctx)
		}
	}
}

// closeInput closes the input the host feeds the child, so it reads end of file.
func (p *Process) closeInput(context.Context) (bool, string, error) {
	if p.input == nil {
		return false, "no input fed by the host", nil
	}
	return true, "", p.input.Close()
}

// inputGate stands between a Spec's standard input and the copy to the child:
// once closed it reads end of file, so the child sees its input end, and it
// closes the caller's reader when that can be closed (io.Pipe's), which ends a
// read that is waiting. A file is handed to the child as it is and is not gated.
type inputGate struct {
	r      io.Reader
	closed atomic.Bool
	once   sync.Once
	err    error
}

// gateInput returns the gate for m, or nil when the host copies nothing.
func gateInput(m StdinMode) *inputGate {
	switch m.(type) {
	case nil, *os.File:
		return nil
	}
	return &inputGate{r: m}
}

func (g *inputGate) Read(b []byte) (int, error) {
	if g.closed.Load() {
		return 0, io.EOF
	}
	n, err := g.r.Read(b)
	if g.closed.Load() {
		return n, io.EOF
	}
	return n, err
}

func (g *inputGate) Close() error {
	g.once.Do(func() {
		g.closed.Store(true)
		if c, ok := g.r.(io.Closer); ok {
			g.err = c.Close()
		}
	})
	return g.err
}

// settle bounds the wait after the kill: by d when set, else by ctx's deadline,
// else by DefaultSettle.
func settle(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	if d > 0 {
		return context.WithTimeout(ctx, d)
	}
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultSettle)
}
