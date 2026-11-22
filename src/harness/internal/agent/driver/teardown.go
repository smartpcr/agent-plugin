// This file is the one teardown order (DH-D-AGENT-6, DH-ADR-AGENT-6,
// agent-loop.md §6.3): T1 to T8, memoised, shared by both owners and by a
// creation's rollback, which enters it at the point Build reached.

package driver

import (
	"context"
	"errors"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
)

// Teardown implements agent.Teardowner. The first caller runs T1 to T8 with
// its hooks; every other caller waits for that run and gets its result.
func (in *instance) Teardown(ctx context.Context, h agent.TeardownHooks) error {
	in.td.Do(func() { in.tdErr = in.teardown(ctx, h) })
	return in.tdErr
}

// Close is Teardown without the registry's hooks: a rollback, or an
// instance used outside a registry.
func (in *instance) Close(ctx context.Context) error {
	return in.Teardown(ctx, agent.TeardownHooks{})
}

func (in *instance) teardown(ctx context.Context, h agent.TeardownHooks) error {
	if ctx == nil {
		ctx = context.Background()
	}
	o := in.f.o
	point := func(p string) {
		if o.Observe != nil {
			o.Observe(in.ref, p)
		}
	}
	var errs []error

	// T1: stop admitting.
	in.admit.Lock()
	in.closing = true
	in.admit.Unlock()
	in.setState(agent.StateClosing)
	point("T1")

	// T2: join a Created dispatch still running.
	if h.JoinCreation != nil {
		h.JoinCreation()
	}
	point("T2")

	// T3: cancel the active turn with cause disposed and discard the pending
	// input of an agent that went live. An agent that never went live ran
	// nothing, and its queued input is kept (it is not undone).
	d := in.driver()
	if d != nil {
		if err := d.Cancel(agent.CauseDisposed, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxDiscard}); err != nil {
			errs = append(errs, err)
		}
	}
	point("T3")

	// T4: wait, without a limit (DH-ADR-AGENT-8), for the driver goroutine;
	// it writes the turn's closers before it exits.
	if d != nil {
		if err := d.Close(context.Background()); err != nil {
			errs = append(errs, err)
		}
	}
	point("T4")

	// T5: close the scope, before the flush, so what its cleanups write is
	// flushed too.
	if in.sc != nil {
		if err := in.sc.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	point("T5")

	// T6: flush within finalFlushTimeout; a failure names the last durable
	// position and does not keep the lease.
	if in.sess != nil {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.FinalFlushTimeout)
		_, err := in.sess.Flush(fctx)
		cancel()
		if err != nil {
			errs = append(errs, &agent.FlushError{Durable: in.sess.Durable(), Err: err})
		}
	}
	point("T6")

	// T7: leave the registry; Disposed now means nothing more is written.
	if h.Leave != nil {
		h.Leave()
	}
	point("T7")

	// T8: close the session handle, which releases the lease.
	if in.sess != nil {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), o.CleanupTimeout)
		err := in.sess.Close(cctx)
		cancel()
		if err != nil && !flushFailed(errs) {
			errs = append(errs, err)
		}
	}
	in.setState(agent.StateDisposed)
	point("T8")
	return errors.Join(errs...)
}

// flushFailed reports whether errs already hold T6's failure, which a
// failing close would only repeat.
func flushFailed(errs []error) bool {
	for _, err := range errs {
		if errors.Is(err, agent.ErrFlushFailed) {
			return true
		}
	}
	return false
}
