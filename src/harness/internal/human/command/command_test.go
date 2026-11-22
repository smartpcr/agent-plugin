package command_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
)

var bg = context.Background()

var me = agent.Ref{Session: "s-command", Incarnation: 1}

type fixture struct {
	root  *cordis.Context
	tab   scope.Table
	reg   *command.Registry
	h     session.Handle
	inbox *agent.Inbox
	sc    *scope.Scope
}

func setup(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{root: cordis.New()}
	t.Cleanup(f.root.Dispose)
	f.tab = scope.NewTable(f.root)
	f.reg = command.NewRegistry(f.tab)
	h, err := session.NewService(f.root, memstore.New(), session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "s-command"}})
	if err != nil {
		t.Fatal(err)
	}
	f.h = h
	t.Cleanup(func() { h.Close(bg) })
	if f.inbox, err = agent.OpenInbox(bg, h); err != nil {
		t.Fatal(err)
	}
	if f.sc, err = f.tab.New(f.root, "agent-a"); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) target() command.Target {
	return command.Target{Agent: me, Scope: f.sc.Key(), Log: f.h, Inbox: f.inbox, Owner: "alice"}
}

func (f *fixture) types(t *testing.T) string {
	t.Helper()
	evs, err := f.h.Read(bg, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Type())
	}
	return strings.Join(out, ",")
}

func (f *fixture) register(t *testing.T, ctx *cordis.Context, c command.Command) {
	t.Helper()
	if _, err := f.reg.Register(ctx, c); err != nil {
		t.Fatal(err)
	}
}

var alice = human.Actor{Channel: "cli", Name: "alice"}

// DH-D-HUMAN-6: a command from any client is run without a model turn; run and
// done are paired and attributed; what the handler hands the agent is delivered
// through the inbox, attributed to the person.
func TestCommandRunsAttributedAndDeliversThroughTheInbox(t *testing.T) {
	f := setup(t)
	f.register(t, f.root, command.Command{Name: "steer", Summary: "Tell the agent something.",
		Run: func(ctx context.Context, c command.Call) (string, error) {
			r, err := c.Send(ctx, agent.ListNextTurn, c.Rest)
			return r.MessageID, err
		}})
	for _, by := range []human.Actor{alice, {Channel: "ui", Name: "bob"}, {Channel: "sdk"}} {
		res, err := f.reg.Execute(bg, f.target(), by, "/steer  use the staging cluster ")
		if err != nil || res.Outcome != command.OutcomeOK || res.Output == "" {
			t.Fatalf("%s: %+v, %v", by.Channel, res, err)
		}
	}
	if got := f.types(t); got != strings.Repeat("command/run,agent/inbox/spliced,command/done,", 2)+"command/run,agent/inbox/spliced,command/done" {
		t.Fatalf("log %s", got)
	}
	p := f.inbox.Pending(agent.ListNextTurn)
	if len(p) != 3 || p[0].Source != agent.SourceCommand || p[0].By != "alice" || p[1].By != "bob" || p[2].By != "sdk" || p[0].Text != "use the staging cluster" {
		t.Fatalf("inbox %+v", p)
	}
	evs, _ := f.h.Read(bg, 0, 1)
	run, err := command.RunType.As(evs[0])
	if err != nil || run.By != (command.By{Principal: "alice", Channel: "cli"}) || run.Name != "steer" || run.Agent != me.String() {
		t.Fatalf("command/run %+v, %v", run, err)
	}
	if f.h.Open().TurnOpen {
		t.Fatal("a command opened a turn")
	}
}

// DH-T-HUMAN-9: an unknown name logs nothing and is never a model message; a
// scoped command shadows a global one; typed arguments are validated; an
// owner-only command is refused for others; a handler that ignores cancellation
// settles aborted.
func TestCommandRegistryAndExecuteRules(t *testing.T) {
	f := setup(t)
	f.register(t, f.root, command.Command{Name: "who", Run: func(context.Context, command.Call) (string, error) { return "global", nil }})
	f.register(t, f.sc.Ctx(), command.Command{Name: "who", Run: func(context.Context, command.Call) (string, error) { return "scoped", nil }})
	type limitArgs struct {
		N int `json:"n"`
	}
	f.register(t, f.root, command.Command{Name: "limit", Args: schema.MustOf[limitArgs](),
		Run: func(_ context.Context, c command.Call) (string, error) { return string(c.Args), nil }})
	f.register(t, f.root, command.Command{Name: "permission", OwnerOnly: true, Run: func(context.Context, command.Call) (string, error) { return "set", nil }})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	f.register(t, f.root, command.Command{Name: "stuck", Timeout: 20 * time.Millisecond,
		Run: func(context.Context, command.Call) (string, error) { <-release; return "", nil }})

	for _, tc := range []struct {
		line string
		by   human.Actor
		want error
	}{
		{"/nosuch", alice, command.ErrUnknown},
		{"hello there", alice, command.ErrNotCommand},
		{`/limit {"n":"many"}`, alice, command.ErrInvalid},
		{`/limit {"n":1,"x":2}`, alice, command.ErrInvalid},
		{"/permission full", human.Actor{Channel: "ui", Name: "mallory"}, command.ErrForbidden},
	} {
		if _, err := f.reg.Execute(bg, f.target(), tc.by, tc.line); !errors.Is(err, tc.want) {
			t.Errorf("%q = %v, want %v", tc.line, err, tc.want)
		}
	}
	if f.h.Len() != 0 || len(f.inbox.Pending(agent.ListNextTurn))+len(f.inbox.Pending(agent.ListNextStep)) != 0 {
		t.Fatalf("refused lines logged %s", f.types(t))
	}

	if res, _ := f.reg.Execute(bg, f.target(), alice, "/who"); res.Output != "scoped" {
		t.Errorf("in scope: %q", res.Output)
	}
	other := f.target()
	other.Scope = scope.Key{}
	if res, _ := f.reg.Execute(bg, other, alice, "/who"); res.Output != "global" {
		t.Errorf("outside the scope: %q", res.Output)
	}
	if res, err := f.reg.Execute(bg, f.target(), alice, `/limit {"n":3}`); err != nil || res.Output != `{"n":3}` {
		t.Errorf("typed: %+v, %v", res, err)
	}
	if res, err := f.reg.Execute(bg, f.target(), alice, "/permission full"); err != nil || res.Output != "set" {
		t.Errorf("owner: %+v, %v", res, err)
	}
	if res, err := f.reg.Execute(bg, f.target(), alice, "/stuck"); err != nil || res.Outcome != command.OutcomeAborted {
		t.Errorf("stuck: %+v, %v", res, err)
	}
	if got := f.types(t); got != strings.TrimSuffix(strings.Repeat("command/run,command/done,", 5), ",") {
		t.Fatalf("log %s", got)
	}
	names := []string{}
	for _, c := range f.reg.List(f.sc.Key()) {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "limit,permission,stuck,who" {
		t.Errorf("list %v", names)
	}
}
