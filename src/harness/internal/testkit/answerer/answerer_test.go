package answerer_test

import (
	"context"
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
