// This file is the creation and resume transaction (DH-D-AGENT-6,
// agent-loop.md §6.3): the factory the driver seats in the registry. Build runs
// creation steps 1 to 4 and records how far it got in the instance; a failure
// enters the teardown (teardown.go) at that point. The registry runs steps 5
// to 7 and calls Activate, which starts the driver goroutine.

package driver

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// FactoryOptions are what the driver's factory builds agents with.
type FactoryOptions struct {
	// Context is the driver's fiber: it dispatches the agents' notices and
	// steering points, owns their scopes and co-owns every agent built
	// (agent.CoOwned), so its unload tears them all down. Required.
	Context *cordis.Context
	// Sessions creates and opens the agents' sessions; Scopes mints their
	// scopes. Required.
	Sessions session.Service
	Scopes   scope.Table
	// Models and Tools are as in Options. Required.
	Models Models
	Tools  tools.Service
	// Prompt is as in Options. nil injects the prompt service provided under
	// prompt.Key on Context (the tools.prompt component); with neither, NewFactory
	// refuses the composition.
	Prompt Prompter
	// Defaults is the default-model service, nil for none.
	Defaults agent.DefaultModels
	// Budget, Now, After, CleanupTimeout, FinalFlushTimeout and Hooks are as
	// in Options. FinalFlushTimeout also bounds teardown's flush (T6), and
	// CleanupTimeout its session close (T8).
	Budget            *Budget
	Now               func() time.Time
	After             func(time.Duration) <-chan time.Time
	CleanupTimeout    time.Duration
	FinalFlushTimeout time.Duration
	Hooks             Hooks
	// Observe is a test seam: it is called at each teardown point, "T1" to
	// "T8", after the point's action. nil is none.
	Observe func(ref agent.Ref, point string)
	// Workdir is the working-directory component: each agent's session is
	// attached to it at creation step 3 (a resume's check), its tool calls are
	// guarded by it (WorkdirGuard), its notices reach the agent's inbox, its
	// requests carry the prompt context line, and T5 detaches it. nil is none.
	Workdir Workdir
}

// NewFactory is the driver's agent.Factory.
func NewFactory(o FactoryOptions) (agent.Factory, error) {
	switch {
	case o.Context == nil, o.Sessions == nil, o.Scopes == nil, o.Models == nil, o.Tools == nil:
		return nil, errors.New("driver: NewFactory needs the driver's context, the session service, the scope table, the model runtime and the tool service")
	case o.CleanupTimeout < 0 || o.FinalFlushTimeout < 0:
		return nil, errors.New("driver: a cleanup timeout cannot be negative")
	}
	if o.Prompt == nil {
		if ps, ok := prompt.Key.Lookup(o.Context); ok && ps != nil {
			o.Prompt = ps
		} else {
			return nil, errors.New("driver: NewFactory needs the prompt assembly: set FactoryOptions.Prompt or provide " + prompt.Key.Name() + " on the driver's context")
		}
	}
	if o.Budget != nil {
		if err := o.Budget.Validate(); err != nil {
			return nil, err
		}
	}
	if o.CleanupTimeout == 0 {
		o.CleanupTimeout = DefaultCleanupTimeout
	}
	if o.FinalFlushTimeout == 0 {
		o.FinalFlushTimeout = DefaultFinalFlushTimeout
	}
	return &factory{o: o}, nil
}

type factory struct{ o FactoryOptions }

// Build is creation steps 1 to 4: check the owner, mint the scope, acquire
// the session (Prepare and Publish, or Open for a resume), open the inbox and
// the model in state initializing, then run Setup and its commit. A failure
// tears down what was reached and returns the error once that has finished.
func (f *factory) Build(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
	if err := owner.Lifetime().Err(); err != nil {
		return nil, fmt.Errorf("driver: the owner is not active: %w", err)
	}
	if err := f.o.Context.Lifetime().Err(); err != nil {
		return nil, fmt.Errorf("driver: the driver is not active: %w", err)
	}
	ctx := owner.Lifetime()
	in := &instance{f: f, ref: ref, parent: o.Parent, state: agent.StateInitializing}
	fail := func(err error) (agent.Instance, error) {
		return nil, errors.Join(err, in.Close(context.Background()))
	}

	sc, err := f.o.Scopes.New(f.o.Context, string(ref.Session))
	if err != nil {
		return fail(err)
	}
	in.sc = sc
	if o.Resume {
		h, rep, err := f.o.Sessions.Open(ctx, nil, o.Session, session.OpenOptions{Scope: sc.Key()})
		if err != nil {
			return fail(err)
		}
		in.sess, in.report = h, rep
	} else {
		hdr := session.Header{ID: string(o.Session), Preset: o.Preset, Cwd: o.Cwd}
		if !o.Parent.IsZero() {
			hdr.Origin, hdr.Creator = "subagent", string(o.Parent.Session)
		}
		p, err := f.o.Sessions.Prepare(ctx, nil, session.CreateOptions{Header: hdr, Scope: sc.Key()})
		if err != nil {
			return fail(err)
		}
		// Publish discards the prepared session itself when it is vetoed.
		h, err := p.Publish(ctx)
		if err != nil {
			return fail(err)
		}
		in.sess = h
	}
	if in.inbox, err = agent.OpenInbox(ctx, in.sess); err != nil {
		return fail(err)
	}
	if f.o.Workdir != nil {
		if err := f.attachWorkdir(ctx, in); err != nil {
			return fail(err)
		}
	}
	in.model, err = agent.OpenModel(ctx, in.sess, agent.ModelOptions{Initial: o.Model, Defaults: f.o.Defaults, Scope: sc.Key()})
	if err != nil {
		return fail(err)
	}
	if o.Setup != nil {
		if err := runSetup(o.Setup, sc.Ctx(), in); err != nil {
			return fail(err)
		}
	}
	return in, nil
}

func runSetup(fn agent.SetupFunc, sc *cordis.Context, a agent.Agent) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("driver: setup panicked: %v", p)
		}
	}()
	commit, err := fn(sc, a)
	if err == nil && commit != nil {
		err = commit()
	}
	return err
}

// instance is one built agent. Its fields up to mu are set by Build and read
// only after it.
type instance struct {
	f      *factory
	ref    agent.Ref
	parent agent.Ref
	sc     *scope.Scope
	sess   session.Handle
	report session.OpenReport
	inbox  *agent.Inbox
	model  *agent.Model
	// wd is the working-directory component the session is attached to, nil
	// for none.
	wd Workdir

	// admit is held for reading by a Send and for writing by T1.
	admit   sync.RWMutex
	closing bool

	mu    sync.Mutex
	state agent.State
	drv   *Driver

	td    sync.Once
	tdErr error
}

func (in *instance) Ref() agent.Ref { return in.ref }

// Parent is the agent that spawned this one, zero for a root agent.
func (in *instance) Parent() agent.Ref { return in.parent }

func (in *instance) Session() session.ReadSession { return in.sess }
func (in *instance) Scope() *cordis.Context       { return in.sc.Ctx() }
func (in *instance) ScopeKey() scope.Key          { return in.sc.Key() }

// OwnScope is the agent's scope itself, which alone can change what it encloses:
// the creator's CreateOptions.Setup binds the agent's preset with it
// (agentpreset.Target), before the agent goes live.
func (in *instance) OwnScope() *scope.Scope { return in.sc }

// OpenReport is what a resume's open closed; zero for a created agent.
func (in *instance) OpenReport() session.OpenReport { return in.report }

func (in *instance) State() agent.State {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.state
}

func (in *instance) setState(s agent.State) {
	in.mu.Lock()
	in.state = s
	in.mu.Unlock()
}

func (in *instance) driver() *Driver {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.drv
}

func (in *instance) Status() agent.Status {
	if d := in.driver(); d != nil {
		return d.Status()
	}
	return agent.StatusIdle
}

// CoOwner implements agent.CoOwned: the driver's fiber.
func (in *instance) CoOwner() *cordis.Context { return in.f.o.Context }

// Activate implements agent.Activator (creation step 7): it starts the driver
// goroutine, which runs the input sent since step 3, and goes live.
func (in *instance) Activate() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.drv != nil || in.state != agent.StateInitializing {
		return fmt.Errorf("driver: %s cannot go live from state %s", in.ref, in.state)
	}
	o := in.f.o
	d, err := Start(Options{
		Context: o.Context, Agent: in, Session: in.sess, Inbox: in.inbox, Model: in.model,
		Models: o.Models, Tools: o.Tools, Prompt: o.Prompt, Budget: o.Budget, Now: o.Now, After: o.After,
		CleanupTimeout: o.CleanupTimeout, FinalFlushTimeout: o.FinalFlushTimeout, Hooks: o.Hooks, Workdir: in.wd,
	})
	if err != nil {
		return err
	}
	in.drv, in.state = d, agent.StateLive
	return nil
}

// Send implements agent.Sender. Input is admitted from creation step 3, so
// it is durable before the agent goes live; from T1 on it is refused with
// agent/closing.
func (in *instance) Send(ctx context.Context, list agent.List, m agent.InboxMessage) (agent.Receipt, error) {
	in.admit.RLock()
	defer in.admit.RUnlock()
	if in.closing {
		return agent.Receipt{}, agent.Closing(in.ref)
	}
	return in.inbox.Send(ctx, list, m)
}

// WaitIdle implements agent.Idler; an agent not yet live runs nothing.
func (in *instance) WaitIdle(ctx context.Context) error {
	if d := in.driver(); d != nil {
		return d.WaitIdle(ctx)
	}
	return nil
}

// Done implements agent.Runner.
func (in *instance) Done() <-chan struct{} {
	if d := in.driver(); d != nil {
		return d.Done()
	}
	return nil
}

// Cancel implements agent.Canceller.
func (in *instance) Cancel(c agent.Cause, o agent.CancelOptions) error {
	if d := in.driver(); d != nil {
		return d.Cancel(c, o)
	}
	return agent.CheckCancel(c, o)
}

// SelectModel implements agent.ModelSelector: it applies from the next step;
// from T1 on it is refused with agent/closing.
func (in *instance) SelectModel(ctx context.Context, sel agent.ModelSelection) (event.Receipt, error) {
	in.admit.RLock()
	defer in.admit.RUnlock()
	if in.closing {
		return event.Receipt{}, agent.Closing(in.ref)
	}
	return in.model.Select(ctx, sel)
}

// Stop implements agent.Stopper.
func (in *instance) Stop(ctx context.Context, cause agent.StopCause) error {
	if d := in.driver(); d != nil {
		return d.Stop(ctx, cause)
	}
	return nil
}
