package answerer_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
)

// The scripted answerer settles each delivered item with its next reply through
// the real registry, and leaves a Never item pending until its asker leaves.
func TestScriptedAnswererSettlesThroughTheRegistry(t *testing.T) {
	root := testkit.Tree(t)
	reg := human.New()
	t.Cleanup(reg.Close)
	a := answerer.Register(t, root, reg, "ui", answerer.AllowOnce(), answerer.Text("blue"), answerer.Never())
	ref := agent.Ref{Session: session.ID("ses-a"), Incarnation: 1}
	if _, err := reg.Bind(root, ref, human.Binding{Channel: "ui"}); err != nil {
		t.Fatal(err)
	}
	ask := func(ctx context.Context, kind human.Kind) (human.Answer, human.Actor, error) {
		return reg.Ask(ctx, human.Item{Kind: kind, To: human.Audience{Agent: ref}, Payload: "x"})
	}

	ans, by, err := ask(context.Background(), human.KindApproval)
	if err != nil || ans.Outcome != human.OutcomeAllowedOnce || by != a.Actor() {
		t.Fatalf("approval = %+v by %+v (%v), want allowed-once by the kit", ans, by, err)
	}
	ans, _, err = ask(context.Background(), human.KindQuestion)
	if err != nil || ans.Outcome != human.OutcomeAnswered || ans.Text != "blue" {
		t.Fatalf("question = %+v (%v), want answered blue", ans, err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	ans, _, err = ask(ctx, human.KindApproval)
	if err == nil && ans.Outcome != human.OutcomeCancelled {
		t.Fatalf("a never-answered item settled %+v", ans)
	}
	asked := a.Asked()
	if len(asked) != 3 {
		t.Fatalf("delivered %d items, want 3", len(asked))
	}
	if _, ok := a.Withdrawn(asked[2].ID); !ok {
		t.Errorf("the abandoned item was not withdrawn from the answerer")
	}
}

// AllowSession and Deny settle approvals with their outcomes, and Next hands
// the test each delivered item in order.
func TestScriptedApprovalsAndNext(t *testing.T) {
	root := testkit.Tree(t)
	reg := human.New()
	t.Cleanup(reg.Close)
	a := answerer.Register(t, root, reg, "ui", answerer.AllowSession(), answerer.Deny())
	ref := agent.Ref{Session: session.ID("ses-b"), Incarnation: 1}
	if _, err := reg.Bind(root, ref, human.Binding{Channel: "ui"}); err != nil {
		t.Fatal(err)
	}
	for i, want := range []human.Outcome{human.OutcomeAllowedSession, human.OutcomeDenied} {
		ans, _, err := reg.Ask(context.Background(), human.Item{Kind: human.KindApproval, To: human.Audience{Agent: ref}, Payload: i})
		if err != nil || ans.Outcome != want {
			t.Fatalf("approval %d = %+v (%v), want %s", i, ans, err, want)
		}
		if it := a.Next(t); it.Kind != human.KindApproval || it.ID != a.Asked()[i].ID {
			t.Fatalf("Next = %+v, want delivered item %d", it, i)
		}
	}
}

// A second answerer on a taken channel fails its test.
func TestRegisterOnATakenChannelFailsItsTest(t *testing.T) {
	root := testkit.Tree(t)
	reg := human.New()
	t.Cleanup(reg.Close)
	answerer.Register(t, root, reg, "ui")
	r := &recorder{TB: t}
	if !r.run(func() { answerer.Register(r, root, reg, "ui") }) {
		t.Fatal("a second answerer on channel ui was registered")
	}
	if got := r.failed(); len(got) != 1 || !strings.HasPrefix(got[0], "answerer: ") {
		t.Fatalf("failures %q, want one answerer error", got)
	}
}

// askAsync asks it on reg until ctx ends, joined when the test ends. The
// returned stop cancels the ask and waits for Ask to return; Ask has then
// returned from the answerer's Deliver too.
func askAsync(t *testing.T, ctx context.Context, reg human.Registry, it human.Item) (stop func()) {
	t.Helper()
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(ctx)
	stop = func() { cancel(); <-done }
	t.Cleanup(stop)
	go func() {
		defer close(done)
		_, _, _ = reg.Ask(ctx, it)
	}()
	return stop
}

// When its test ends the answerer reports what went wrong: a reply the registry
// refused, a reply still scripted, and a delivery it had no reply for.
func TestCleanupReportsRefusedLeftoverAndUnscriptedReplies(t *testing.T) {
	root := testkit.Tree(t)
	reg := human.New()
	t.Cleanup(reg.Close)
	refused, unscripted := &recorder{TB: t}, &recorder{TB: t}
	// A text answer is no answer to an approval; Deny is never asked for.
	a := answerer.Register(refused, root, reg, "ui", answerer.Text("blue"), answerer.Deny())
	b := answerer.Register(unscripted, root, reg, "cli")
	refA := agent.Ref{Session: session.ID("ses-a"), Incarnation: 1}
	refB := agent.Ref{Session: session.ID("ses-b"), Incarnation: 1}
	for ref, ch := range map[agent.Ref]human.Channel{refA: "ui", refB: "cli"} {
		if _, err := reg.Bind(root, ref, human.Binding{Channel: ch}); err != nil {
			t.Fatal(err)
		}
	}
	stopA := askAsync(t, context.Background(), reg, human.Item{Kind: human.KindApproval, To: human.Audience{Agent: refA}, Payload: "x"})
	stopB := askAsync(t, context.Background(), reg, human.Item{Kind: human.KindQuestion, To: human.Audience{Agent: refB}, Payload: "y"})
	a.Next(t)
	b.Next(t)
	// Next returns from inside Deliver; the asks are joined first so each
	// Deliver has returned before its answerer's cleanup waits for its replies.
	stopA()
	stopB()

	refused.cleanup()
	unscripted.cleanup()
	got := refused.failed()
	// The text reply is refused either as no answer to an approval or, when the
	// ask's cancel settled the item first, as late: a refusal both ways.
	refusedReply := len(got) > 0 && (strings.Contains(got[0], "is not an answer to a") || strings.Contains(got[0], "has settled"))
	if len(got) != 2 || !refusedReply || !strings.Contains(got[1], "1 scripted repl(ies) never asked for") {
		t.Fatalf("the refusing answerer reported %q, want the refused reply and the leftover Deny", got)
	}
	if got := unscripted.failed(); len(got) != 1 || !strings.Contains(got[0], "unscripted delivery of "+string(b.Asked()[0].ID)) {
		t.Fatalf("the unscripted answerer reported %q", got)
	}
}
