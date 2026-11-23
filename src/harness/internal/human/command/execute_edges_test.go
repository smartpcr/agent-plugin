package command_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// failingLog fails its Append number failAt (1-based) and passes the others to
// the session log.
type failingLog struct {
	next   command.Log
	failAt int
	calls  int
}

var errAppend = errors.New("append refused")

func (l *failingLog) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	l.calls++
	if l.calls == l.failAt {
		return event.Receipt{}, errAppend
	}
	return l.next.Append(ctx, d)
}

// DH-T-HUMAN-9: a command needs a valid name and a handler; a target needs an
// agent and a log; Parse keeps "//" lines and bad names out.
//
//dsh:test DH-T-HUMAN-9
func TestRegisterParseAndTargetRefusals(t *testing.T) {
	f := setup(t)
	ok := func(context.Context, command.Call) (string, error) { return "", nil }
	for _, c := range []command.Command{
		{Name: "", Run: ok},
		{Name: "Upper", Run: ok},
		{Name: "9lives", Run: ok},
		{Name: strings.Repeat("a", 33), Run: ok},
		{Name: "nohandler"},
	} {
		if h, err := f.reg.Register(f.root, c); !errors.Is(err, command.ErrBadCommand) || h != nil {
			t.Errorf("Register(%q, handler %v) = %v, %v; want %v", c.Name, c.Run != nil, h, err, command.ErrBadCommand)
		}
	}
	if _, found := f.reg.Lookup(f.sc.Key(), "nohandler"); found {
		t.Fatal("a refused command is visible")
	}

	for line, want := range map[string]error{
		"//comment":  command.ErrNotCommand,
		"":           command.ErrNotCommand,
		"/Steer now": command.ErrUnknown,
		"/ steer":    command.ErrUnknown,
	} {
		if _, _, err := command.Parse(line); !errors.Is(err, want) {
			t.Errorf("Parse(%q) = %v; want %v", line, err, want)
		}
	}
	if name, rest, err := command.Parse("  /steer   a  b  "); err != nil || name != "steer" || rest != "a  b" {
		t.Errorf("Parse = %q, %q, %v; want steer, \"a  b\"", name, rest, err)
	}

	f.register(t, f.root, command.Command{Name: "who", Run: ok})
	noAgent := f.target()
	noAgent.Agent = agent.Ref{}
	noLog := f.target()
	noLog.Log = nil
	for _, tg := range []command.Target{noAgent, noLog} {
		if _, err := f.reg.Execute(bg, tg, alice, "/who"); !errors.Is(err, command.ErrNoTarget) {
			t.Errorf("Execute without agent or log = %v; want %v", err, command.ErrNoTarget)
		}
	}
	// An owner-only command is refused when the agent has no recorded owner, even
	// for a person whose name is empty.
	f.register(t, f.root, command.Command{Name: "grant", OwnerOnly: true, Run: ok})
	unowned := f.target()
	unowned.Owner = ""
	if _, err := f.reg.Execute(bg, unowned, human.Actor{}, "/grant"); !errors.Is(err, command.ErrForbidden) {
		t.Errorf("owner-only on an unowned agent = %v; want %v", err, command.ErrForbidden)
	}
	if f.h.Len() != 0 {
		t.Fatalf("refusals logged %s", f.types(t))
	}
}

// DH-T-HUMAN-9: a handler's error settles the command failed, a panic settles it
// failed with the panic named, and a handler that returns after its deadline
// settles aborted; every settlement is logged as command/done.
//
//dsh:test DH-T-HUMAN-9
func TestHandlerFailurePanicAndLateReturn(t *testing.T) {
	f := setup(t)
	f.register(t, f.root, command.Command{Name: "fail", Run: func(context.Context, command.Call) (string, error) {
		return "half", errors.New("backend down")
	}})
	f.register(t, f.root, command.Command{Name: "boom", Run: func(context.Context, command.Call) (string, error) {
		panic("handler bug")
	}})
	f.register(t, f.root, command.Command{Name: "late", Timeout: 1, Run: func(ctx context.Context, _ command.Call) (string, error) {
		<-ctx.Done()
		return "partial", ctx.Err()
	}})

	res, err := f.reg.Execute(bg, f.target(), alice, "/fail")
	if err != nil || res.Outcome != command.OutcomeFailed || res.Output != "half" || res.Detail != "backend down" || res.ID == "" {
		t.Errorf("/fail = %+v, %v; want failed with output and detail", res, err)
	}
	res, err = f.reg.Execute(bg, f.target(), alice, "/boom")
	if err != nil || res.Outcome != command.OutcomeFailed || !strings.Contains(res.Detail, "panic: handler bug") {
		t.Errorf("/boom = %+v, %v; want failed naming the panic", res, err)
	}
	res, err = f.reg.Execute(bg, f.target(), alice, "/late")
	if err != nil || res.Outcome != command.OutcomeAborted || res.Detail == "" {
		t.Errorf("/late = %+v, %v; want aborted with a detail", res, err)
	}
	if got := f.types(t); got != strings.TrimSuffix(strings.Repeat("command/run,command/done,", 3), ",") {
		t.Fatalf("log %s", got)
	}
	evs, err := f.h.Read(bg, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	done, err := command.DoneType.As(evs[0])
	if err != nil || done.Outcome != command.OutcomeFailed || done.Output != "half" || done.Detail != "backend down" {
		t.Fatalf("command/done of /fail = %+v, %v", done, err)
	}
}

// DH-T-HUMAN-9: typed arguments default to an empty object; a person with no
// name is attributed to the channel, and with neither to "unknown".
//
//dsh:test DH-T-HUMAN-9
func TestTypedDefaultsAndAttribution(t *testing.T) {
	f := setup(t)
	type limitArgs struct {
		N int `json:"n,omitempty"`
	}
	f.register(t, f.root, command.Command{Name: "limit", Args: schema.MustOf[limitArgs](),
		Run: func(_ context.Context, c command.Call) (string, error) { return string(c.Args) + "|" + c.Rest, nil }})
	if res, err := f.reg.Execute(bg, f.target(), human.Actor{}, "/limit"); err != nil || res.Output != "{}|" {
		t.Fatalf("/limit with no arguments = %+v, %v; want {}", res, err)
	}
	evs, err := f.h.Read(bg, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	run, err := command.RunType.As(evs[0])
	if err != nil || run.By.Principal != "unknown" || run.By.Channel != "" || string(run.Args) != "{}" {
		t.Fatalf("command/run = %+v, %v; want principal unknown and args {}", run, err)
	}
}

// DH-T-HUMAN-9: a command/run that cannot be logged fails the command before the
// handler runs; a command/done that cannot be logged returns the settlement with
// the error.
//
//dsh:test DH-T-HUMAN-9
func TestLogFailuresAroundTheHandler(t *testing.T) {
	f := setup(t)
	runs := 0
	f.register(t, f.root, command.Command{Name: "count", Run: func(context.Context, command.Call) (string, error) {
		runs++
		return "counted", nil
	}})

	tg := f.target()
	tg.Log = &failingLog{next: f.h, failAt: 1}
	if res, err := f.reg.Execute(bg, tg, alice, "/count"); !errors.Is(err, errAppend) || !strings.Contains(err.Error(), "command/run") || res.ID != "" || runs != 0 {
		t.Fatalf("run not logged: %+v, %v (handler ran %d times); want the append error and no run", res, err, runs)
	}

	tg.Log = &failingLog{next: f.h, failAt: 2}
	res, err := f.reg.Execute(bg, tg, alice, "/count")
	if !errors.Is(err, errAppend) || !strings.Contains(err.Error(), "command/done") || runs != 1 {
		t.Fatalf("done not logged: %v (handler ran %d times); want the append error after one run", err, runs)
	}
	if res.ID == "" || res.Outcome != command.OutcomeOK || res.Output != "counted" {
		t.Fatalf("settlement %+v; want the handler's result with its id", res)
	}
	if got := f.types(t); got != "command/run" {
		t.Fatalf("log %s; want only the command/run", got)
	}
}

// DH-T-HUMAN-9: a Send after the command settled is refused, and a call without
// an inbox cannot send.
//
//dsh:test DH-T-HUMAN-9
func TestSendAfterSettleAndWithoutInbox(t *testing.T) {
	f := setup(t)
	var kept command.Call
	f.register(t, f.root, command.Command{Name: "keep", Run: func(_ context.Context, c command.Call) (string, error) {
		kept = c
		return "", nil
	}})
	if _, err := f.reg.Execute(bg, f.target(), alice, "/keep"); err != nil {
		t.Fatal(err)
	}
	if _, err := kept.Send(bg, agent.ListNextTurn, "too late"); !errors.Is(err, command.ErrSettled) || !strings.Contains(err.Error(), kept.ID) {
		t.Fatalf("Send after settle = %v; want %v naming %s", err, command.ErrSettled, kept.ID)
	}
	if n := len(f.inbox.Pending(agent.ListNextTurn)); n != 0 {
		t.Fatalf("a refused send reached the inbox (%d pending)", n)
	}
	if _, err := (command.Call{ID: "cmd-x"}).Send(bg, agent.ListNextTurn, "x"); err == nil || !strings.Contains(err.Error(), "no inbox") {
		t.Fatalf("Send without an inbox = %v; want refused", err)
	}
}

// The command events refuse drafts that miss their identity or carry an outcome
// outside the closed set.
//
//dsh:test DH-T-HUMAN-9
func TestCommandEventValidation(t *testing.T) {
	if _, err := command.RunType.Draft(command.Run{ID: "cmd-1", Agent: "a", Name: "x"}); err == nil {
		t.Error("command/run without a principal was accepted")
	}
	if _, err := command.RunType.Draft(command.Run{ID: "cmd-1", Agent: "a", Name: "x", By: command.By{Principal: "p"}}); err != nil {
		t.Errorf("a complete command/run was refused: %v", err)
	}
	if _, err := command.DoneType.Draft(command.Done{Name: "x", Outcome: command.OutcomeOK}); err == nil {
		t.Error("command/done without an id was accepted")
	}
	if _, err := command.DoneType.Draft(command.Done{ID: "cmd-1", Name: "x", Outcome: "maybe"}); err == nil || !strings.Contains(err.Error(), "closed set") {
		t.Errorf("command/done with outcome maybe = %v; want refused", err)
	}
}
