// This file is the driver goroutine (DH-D-AGENT-3): one per agent, parked on
// the inbox's wake, running the sixteen points of agent-loop.md §6.2 as plain
// sequential Go. turn.go is points 1 to 3 and 14 to 16, step.go points 4 to 13,
// cancel.go DH-D-AGENT-5's cancel.

package driver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// Models binds a step's model call (point 7); *llm.Runtime is one.
type Models interface {
	Prepare(ctx context.Context, route vocab.RouteID, model string, c vocab.Controls) (llm.Prepared, error)
}

// Hooks are test seams; each runs on the driver goroutine and must return
// promptly. A nil hook is skipped.
type Hooks struct {
	// ContinueOrStop runs each time the driver reaches point 14 (continue or
	// stop) of turn, after step has closed.
	ContinueOrStop func(turn, step int)
}

// Options are what one agent's driver runs with.
type Options struct {
	// Context dispatches the steering points and the notices; required.
	Context *cordis.Context
	// Agent is the identity the driver runs for (Ref, ScopeKey); required.
	Agent agent.Agent
	// Session is the agent's write handle; only the driver appends turn events.
	Session session.Session
	// Inbox and Model are the agent's folds over Session; required.
	Inbox *agent.Inbox
	Model *agent.Model
	// Models prepares model calls; Tools resolves and runs step tool sets.
	Models Models
	Tools  tools.Service
	// Budget is the turn budget; nil is DefaultBudget.
	Budget *Budget
	// Now and After are the clock; nil is the wall clock.
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
	// CleanupTimeout bounds each log write the driver makes, the closers a
	// cancel owes above all (architecture.md F4's cleanupBudget): a write runs
	// under context.WithTimeout(context.WithoutCancel(turnCtx), CleanupTimeout),
	// so a cancel never stops it and a stalled store cannot hold the driver.
	// 0 is DefaultCleanupTimeout.
	CleanupTimeout time.Duration
	// FinalFlushTimeout bounds the flush that settles a turn (point 16,
	// tech-spec.md §5.5 finalFlushTimeout); 0 is DefaultFinalFlushTimeout.
	FinalFlushTimeout time.Duration
	Hooks             Hooks
}

// The cleanup bounds' defaults.
const (
	DefaultCleanupTimeout    = 10 * time.Second
	DefaultFinalFlushTimeout = 10 * time.Second
)

// Driver runs one agent's turns on its own goroutine.
type Driver struct {
	o      Options
	budget Budget

	life     context.Context
	stopLife context.CancelCauseFunc
	done     chan struct{}

	mu      sync.Mutex
	status  agent.Status
	waiting bool            // parked on the wake
	held    map[string]bool // next-turn input a cancel kept waits for a new wake
	gates   []chan struct{} // Discards a Cancel is making; closed when settled
	changed chan struct{}
	cur     *turnState
	err     error
}

// Start validates o and starts the driver goroutine. The driver opens no turn
// until a waking send reaches it: a resumed agent starts idle.
func Start(o Options) (*Driver, error) {
	switch {
	case o.Context == nil, o.Agent == nil, o.Session == nil, o.Inbox == nil, o.Model == nil, o.Models == nil, o.Tools == nil:
		return nil, errors.New("driver: Start needs a context, the agent, its session, inbox and model, the model runtime and the tool service")
	}
	b := DefaultBudget()
	if o.Budget != nil {
		b = *o.Budget
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.After == nil {
		o.After = time.After
	}
	if o.CleanupTimeout < 0 || o.FinalFlushTimeout < 0 {
		return nil, errors.New("driver: a cleanup timeout cannot be negative")
	}
	if o.CleanupTimeout == 0 {
		o.CleanupTimeout = DefaultCleanupTimeout
	}
	if o.FinalFlushTimeout == 0 {
		o.FinalFlushTimeout = DefaultFinalFlushTimeout
	}
	life, stop := context.WithCancelCause(context.Background())
	d := &Driver{o: o, budget: b, life: life, stopLife: stop, done: make(chan struct{}),
		status: agent.StatusIdle, changed: make(chan struct{})}
	go d.run()
	return d, nil
}

// Budget is the budget the driver runs turns under.
func (d *Driver) Budget() Budget { return d.budget }

// Status is running while a turn runs, idle otherwise.
func (d *Driver) Status() agent.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// Err is the failure that stopped the driver (a failed append: lease lost,
// storage), nil while it runs or after a clean Close.
func (d *Driver) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// Done is closed when the driver goroutine has exited.
func (d *Driver) Done() <-chan struct{} { return d.done }

// WaitIdle returns once the driver is parked with no input it would run: no
// next-turn item pending, or only input kept across a cancel, which waits for
// a new wake. It returns the driver's failure when the driver has stopped.
func (d *Driver) WaitIdle(ctx context.Context) error {
	for {
		d.mu.Lock()
		idle, held, ch := d.status == agent.StatusIdle && d.waiting, d.held, d.changed
		d.mu.Unlock()
		select {
		case <-d.done:
			return d.Err()
		default:
		}
		if idle && allHeld(d.o.Inbox.Pending(agent.ListNextTurn), held) {
			select {
			case <-ch: // the driver moved on between the reads: look again
				continue
			default:
				return nil
			}
		}
		select {
		case <-ch:
		case <-d.done:
			return d.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close cancels the running turn with cause disposed, keeps parked input, stops
// the goroutine and waits for it within ctx: the closers of the turn are
// written before it returns. It returns the driver's failure, if any.
func (d *Driver) Close(ctx context.Context) error {
	d.halt(agent.CauseDisposed)
	select {
	case <-d.done:
		return d.Err()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stop implements agent.Stopper: the running turn ends aborted{disposed} and no
// new turn opens.
func (d *Driver) Stop(ctx context.Context, _ agent.StopCause) error { return d.Close(ctx) }

func (d *Driver) halt(c agent.Cause) {
	d.mu.Lock()
	if t := d.cur; t != nil {
		t.cancelTurn(c)
	}
	d.mu.Unlock()
	d.stopLife(&agent.CancelError{Cause: c})
}

func (d *Driver) setStatus(s agent.Status, waiting bool) {
	d.mu.Lock()
	changed := d.status != s
	d.status, d.waiting = s, waiting
	close(d.changed)
	d.changed = make(chan struct{})
	d.mu.Unlock()
	if changed {
		agent.Notify(d.o.Context, agent.StatusChanged, d.o.Agent, agent.StatusNotice{Agent: d.o.Agent.Ref(), Status: s})
	}
}

// run is the driver goroutine: point 1 (wake) and the turn loop.
func (d *Driver) run() {
	defer close(d.done)
	for {
		d.setStatus(agent.StatusIdle, true)
		select {
		case <-d.life.Done():
			d.setStatus(agent.StatusIdle, false)
			return
		case <-d.o.Inbox.Wake():
		}
		d.mu.Lock()
		d.held = nil
		d.mu.Unlock()
		for d.life.Err() == nil {
			ran, aborted, err := d.turn()
			if err != nil {
				d.fail(err)
				return
			}
			if !ran {
				break
			}
			if aborted {
				// The cancel spent the wakes of the input parked then (keep),
				// so that input stays parked until a new wake. A wake left now
				// is a Send that arrived during the cancel or its cleanup: it
				// opens the next turn at once. The snapshot is taken first, so
				// a send after it is never held.
				held := map[string]bool{}
				for _, m := range d.o.Inbox.Pending(agent.ListNextTurn) {
					held[m.ID] = true
				}
				select {
				case <-d.o.Inbox.Wake():
					continue
				default:
				}
				d.mu.Lock()
				d.held = held
				d.mu.Unlock()
				break
			}
		}
	}
}

// fail stops the driver on a failure that has no place in the log (an append
// or a flush failed): the turn may stay open, and the next write-open of the
// session appends its closers.
func (d *Driver) fail(err error) {
	d.mu.Lock()
	if d.err == nil {
		d.err = err
	}
	t := d.cur
	d.mu.Unlock()
	n := turnOf(t)
	agent.Notify(d.o.Context, agent.Failed, d.o.Agent, agent.FailedNotice{Agent: d.o.Agent.Ref(), Turn: n, Err: err})
	d.stopLife(fmt.Errorf("driver: stopped: %w", err))
	d.setStatus(agent.StatusIdle, false)
}

func turnOf(t *turnState) int {
	if t == nil {
		return 0
	}
	return t.turn
}

// allHeld reports whether every pending item is one kept across a cancel.
func allHeld(pending []agent.InboxMessage, held map[string]bool) bool {
	for _, m := range pending {
		if !held[m.ID] {
			return false
		}
	}
	return true
}

// writeCtx is the context of one log write made for parent (the turn's or
// step's context, nil for none): parent's values without its cancel, bounded
// by CleanupTimeout. A cancel never stops a closer; a stalled store does.
func (d *Driver) writeCtx(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = d.life
	}
	return context.WithTimeout(context.WithoutCancel(parent), d.o.CleanupTimeout)
}

// write runs fn under a fresh writeCtx for parent.
func (d *Driver) write(parent context.Context, fn func(ctx context.Context) error) error {
	ctx, cancel := d.writeCtx(parent)
	defer cancel()
	return fn(ctx)
}

// permanent reports a write failure no retry can mend: the session is closed
// or its lease went to another writer.
func permanent(err error) bool {
	return errors.Is(err, store.ErrLeaseLost) || errors.Is(err, session.ErrClosed)
}
