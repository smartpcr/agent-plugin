package driver_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// NewFactory refuses a missing part, a negative timeout and a negative budget.
func TestNewFactoryRefusesBadOptions(t *testing.T) {
	root, tsvc := setup(t)
	valid := func() driver.FactoryOptions {
		return driver.FactoryOptions{
			Context: root, Sessions: session.NewService(root, memstore.New(), session.Config{}),
			Scopes: scope.NewTable(root), Models: llm.New(), Tools: tsvc,
		}
	}
	for name, edit := range map[string]func(*driver.FactoryOptions){
		"no context":               func(o *driver.FactoryOptions) { o.Context = nil },
		"no tools":                 func(o *driver.FactoryOptions) { o.Tools = nil },
		"negative cleanup timeout": func(o *driver.FactoryOptions) { o.CleanupTimeout = -time.Second },
		"negative flush timeout":   func(o *driver.FactoryOptions) { o.FinalFlushTimeout = -time.Second },
		"negative budget":          func(o *driver.FactoryOptions) { o.Budget = &driver.Budget{MaxSteps: -1} },
	} {
		o := valid()
		edit(&o)
		if f, err := driver.NewFactory(o); err == nil || f != nil {
			t.Errorf("NewFactory with %s: %v, %v", name, f, err)
		}
	}
	if _, err := driver.NewFactory(valid()); err != nil {
		t.Fatalf("NewFactory with valid options: %v", err)
	}
}

// probe is what Setup saw of the agent before it went live.
type probe struct {
	scopeOK   bool
	status    agent.Status
	idle      error
	done      <-chan struct{}
	cancel    error
	badCancel error
	stop      error
	parent    agent.Ref
	// inst is the instance Setup was handed: the built agent itself.
	inst agent.Agent
}

// DH-T-AGENT-19: before it goes live an agent runs nothing: in Setup it is
// idle, WaitIdle returns at once, it has no done channel, a valid cancel and a
// stop have nothing to do and an invalid cancel is still refused. Live, it
// cannot go live again, a cancel reaches its driver, and a stop ends the driver
// so that no new turn opens.
func TestAgentBeforeAndAfterItGoesLive(t *testing.T) {
	l := newLife(t, lifeOptions{})
	var p probe
	h, err := l.create(l.root, "p", func(sc *cordis.Context, a agent.Agent) (func() error, error) {
		p.scopeOK = a.Scope() == sc
		p.status = a.Status()
		p.idle = agent.WaitIdle(bg, a)
		p.done = agent.Done(a)
		p.cancel = agent.Cancel(a, agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep})
		p.badCancel = a.(agent.Canceller).Cancel("nobody", agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep})
		p.stop = agent.Stop(bg, a, agent.StopHidden)
		p.parent = a.(interface{ Parent() agent.Ref }).Parent()
		p.inst = a
		return nil, nil
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	switch {
	case !p.scopeOK:
		t.Error("in Setup the agent's scope is not the scope Setup runs in")
	case p.status != agent.StatusIdle:
		t.Errorf("in Setup the agent is %s, want idle", p.status)
	case p.idle != nil || p.done != nil:
		t.Errorf("in Setup WaitIdle = %v and Done = %v, want nil and none", p.idle, p.done)
	case p.cancel != nil || p.stop != nil:
		t.Errorf("in Setup cancel = %v and stop = %v, want nothing to do", p.cancel, p.stop)
	case !errors.Is(p.badCancel, agent.ErrInvalidCancel):
		t.Errorf("in Setup an invalid cancel: %v, want refused", p.badCancel)
	case !p.parent.IsZero():
		t.Errorf("a root agent has parent %v", p.parent)
	}

	a := h.Agent()
	if a.State() != agent.StateLive {
		t.Fatalf("state %s, want live", a.State())
	}
	if done := agent.Done(a); done == nil {
		t.Fatal("a live agent has no done channel")
	}
	if err := p.inst.(agent.Activator).Activate(); err == nil {
		t.Fatal("a live agent went live again")
	}
	if err := agent.Cancel(a, agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}); err != nil {
		t.Fatalf("cancel an idle live agent: %v", err)
	}
	if err := agent.Stop(bg, a, agent.StopHidden); err != nil {
		t.Fatalf("stop a live agent: %v", err)
	}
	// Stop returns once the driver goroutine has exited, so input sent after
	// it can open no turn: it reaches no model.
	select {
	case <-agent.Done(a):
	default:
		t.Fatal("Stop returned before the driver goroutine exited")
	}
	_, _ = agent.Send(bg, a, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "after the stop"})
	must(0, h.Dispose(bg))
	if n := len(l.tr.Requests()); n != 0 {
		t.Fatalf("a stopped agent called the model %d times", n)
	}
}

// DH-T-AGENT-19: a creation that fails while it is built leaves no agent and
// frees the lease: a resume of a session that does not exist, a session id
// already taken, a Setup that panics and a Setup whose commit fails.
func TestFailedBuildsLeaveNothing(t *testing.T) {
	l := newLife(t, lifeOptions{})
	if _, err := l.reg.Resume(l.root, agent.CreateOptions{Session: "missing", Model: agent.ModelSelection{Route: route}}); err == nil {
		t.Error("a resume of a session that does not exist succeeded")
	}
	l.absent("missing")

	taken := must(l.svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "taken"}}))
	must(0, taken.Close(bg))
	if _, err := l.create(l.root, "taken", nil); err == nil {
		t.Error("an agent was created over a session that already exists")
	}
	l.absent("taken")
	l.released("taken")

	errCommit := errors.New("commit refused")
	for name, tc := range map[string]struct {
		setup agent.SetupFunc
		want  string
	}{
		"panic": {func(*cordis.Context, agent.Agent) (func() error, error) { panic("setup bug") }, "setup panicked: setup bug"},
		"commit": {func(*cordis.Context, agent.Agent) (func() error, error) {
			return func() error { return errCommit }, nil
		}, errCommit.Error()},
	} {
		if _, err := l.create(l.root, name, tc.setup); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("create with a Setup %s: %v, want %q", name, err, tc.want)
		}
		l.absent(name)
		l.released(name)
	}
	if n := len(l.tr.Requests()); n != 0 {
		t.Fatalf("a failed build called the model %d times", n)
	}
}

// A creation whose Setup commits starts live, and its commit ran once.
func TestSetupCommitRunsOnce(t *testing.T) {
	l := newLife(t, lifeOptions{turns: []fakemodel.Turn{fakemodel.Reply("hi")}})
	commits := 0
	h := must(l.create(l.root, "c", func(*cordis.Context, agent.Agent) (func() error, error) {
		return func() error { commits++; return nil }, nil
	}))
	if commits != 1 || h.Agent().State() != agent.StateLive {
		t.Fatalf("commit ran %d times, state %s", commits, h.Agent().State())
	}
	must(agent.Send(bg, h.Agent(), agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, Text: "go"}))
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	must(0, agent.WaitIdle(ctx, h.Agent()))
	must(0, h.Dispose(bg))
}
