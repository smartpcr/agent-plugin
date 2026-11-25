package launch

import (
	"context"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// DefaultLimit bounds a staged start from Confirm's call until the command has
// started: the wait for ready, the decision and the wait after go.
const DefaultLimit = 30 * time.Second

// Confirm is the host's decision on a stage's ready (C37's contract,
// architecture.md sec. 4.2 "Host decision"): it returns the Confinement the
// command may run under, or the error that refuses it. The plan p is the one the
// stage was started with, Argv and channel numbers filled in.
type Confirm func(ctx context.Context, p Plan, r Ready) (sandbox.Confinement, error)

// Hooks are the seam's parts of a staged plan.
type Hooks struct {
	// Confirm decides on the stage's ready; it is required.
	Confirm Confirm
	// Release frees the host state the seam prepared for this start (the canary,
	// a scratch directory); nil when there is none. It runs once, after Confirm
	// or after a failed start.
	Release func(ctx context.Context) error
	// Exe is the stage's program, the harness binary; empty is os.Executable().
	Exe string
	// Limit bounds Confirm (DefaultLimit when zero).
	Limit time.Duration
}

// NewLaunch is the staged plan of kind, backend and canary for policy, which the
// one starter executes (DH-D-EXEC-4): its Wrap starts `<exe> sandbox-launch`
// with the plan's arguments in place of the command, its Confirm reads ready,
// calls h.Confirm and sends go or closes the pair, and its Release closes the
// host's ends and calls h.Release. Argv, Control and Status of p are filled in by
// Wrap and must be empty. A plan executes one start.
func NewLaunch(policy sandbox.Policy, p Plan, h Hooks) (sandbox.Launch, error) {
	if err := policy.Validate(); err != nil {
		return sandbox.Launch{}, err
	}
	if h.Confirm == nil {
		return sandbox.Launch{}, failed("a staged plan needs a Confirm decision")
	}
	if p.Argv != nil || p.Control != 0 || p.Status != 0 {
		return sandbox.Launch{}, failed("the command and the channel of a staged plan are filled in by Wrap")
	}
	if h.Exe == "" {
		exe, err := os.Executable()
		if err != nil {
			return sandbox.Launch{}, herr.Wrap(sandbox.CodeLaunchFailed, err, "find the harness binary for the launch stage")
		}
		h.Exe = exe
	}
	if h.Limit <= 0 {
		h.Limit = DefaultLimit
	}
	return sandbox.Staged(policy, &staged{plan: p, hooks: h}, p.String()), nil
}

// staged is the sandbox.Stage of a NewLaunch plan.
type staged struct {
	hooks Hooks

	mu       sync.Mutex
	plan     Plan
	ch       *Channel
	wrapped  bool
	released bool
}

var _ sandbox.Stage = (*staged)(nil)

// Wrap opens the pair, hands the child its ends and returns the stage's argv.
func (s *staged) Wrap(argv []string, keep []uintptr, pass sandbox.Pass) (sandbox.Wrapped, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wrapped {
		return sandbox.Wrapped{}, failed("a staged plan starts one command")
	}
	s.wrapped = true
	ch, err := NewChannel()
	if err != nil {
		return sandbox.Wrapped{}, err
	}
	s.ch = ch
	p := s.plan
	p.Argv = slices.Clone(argv)
	if len(keep) > 0 {
		p.Keep = slices.Clone(keep)
	}
	p.Control = pass(ch.child[0].File)
	p.Status = pass(ch.child[1].File)
	if err := p.Validate(); err != nil {
		return sandbox.Wrapped{}, err
	}
	s.plan = p
	return sandbox.Wrapped{Argv: append([]string{s.hooks.Exe, Mode}, p.Args()...)}, nil
}

// Confirm is the exchange: ready, the decision, then go or a rejection.
func (s *staged) Confirm(ctx context.Context) (sandbox.Confinement, error) {
	s.mu.Lock()
	ch, p := s.ch, s.plan
	s.mu.Unlock()
	if ch == nil {
		return sandbox.Confinement{}, failed("Confirm before Wrap")
	}
	ch.CloseChildEnds()
	ctx, cancel := context.WithTimeout(ctx, s.hooks.Limit)
	defer cancel()
	r, err := ch.ReadReady(ctx)
	if err != nil {
		ch.Reject()
		return sandbox.Confinement{}, err
	}
	c, err := s.hooks.Confirm(ctx, p, r)
	if err != nil {
		ch.Reject()
		return sandbox.Confinement{}, err
	}
	if !c.Valid() {
		ch.Reject()
		return sandbox.Confinement{}, failed("the decision gave no valid confinement (%q, backend %q)", c.State, c.Backend)
	}
	// A decision that came after the limit, or after the caller gave up, is no
	// decision: the stage is rejected and the command never runs.
	if err := ctx.Err(); err != nil {
		ch.Reject()
		return sandbox.Confinement{}, herr.Wrap(sandbox.CodeLaunchFailed, context.Cause(ctx), "the decision came after the launch limit or the caller's end")
	}
	if err := ch.Go(); err != nil {
		ch.Close()
		return sandbox.Confinement{}, err
	}
	if err := ch.AwaitExec(ctx); err != nil {
		return sandbox.Confinement{}, err
	}
	return c, nil
}

// Release closes what is left of the pair and runs the seam's release, once.
func (s *staged) Release(ctx context.Context) error {
	s.mu.Lock()
	if s.released {
		s.mu.Unlock()
		return nil
	}
	s.released = true
	ch := s.ch
	s.mu.Unlock()
	if ch != nil {
		ch.CloseChildEnds()
		ch.Close()
	}
	if s.hooks.Release != nil {
		return s.hooks.Release(ctx)
	}
	return nil
}
