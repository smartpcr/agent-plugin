package driver_test

import (
	"context"
	"iter"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// clientRoute stamps every attempt its Prepared settles with a client
// invocation, as a signed-in client route does.
type clientRoute struct {
	driver.Models
	inv *event.ClientInvocation
}

func (c clientRoute) Prepare(ctx context.Context, rt vocab.RouteID, model string, ctl vocab.Controls) (llm.Prepared, error) {
	p, err := c.Models.Prepare(ctx, rt, model, ctl)
	if err != nil {
		return nil, err
	}
	return clientPrepared{Prepared: p, inv: c.inv}, nil
}

type clientPrepared struct {
	llm.Prepared
	inv *event.ClientInvocation
}

func (p clientPrepared) Stream(ctx context.Context, req session.Request, sink llm.AttemptSink) iter.Seq[vocab.Chunk] {
	return p.Prepared.Stream(ctx, req, llm.AttemptSinkFunc(func(a llm.Attempt) error {
		inv := *p.inv
		a.Client = &inv
		return sink.Settle(a)
	}))
}

// A cancel that keeps a streamed prefix settles it as the turn's interrupted
// assistant/message, and that record keeps the attempt's client invocation
// (the signed-in route's boundary, M1-D-PIPE-4 A), as every other settlement
// does.
func TestInterruptedPrefixKeepsTheClientInvocation(t *testing.T) {
	inv := &event.ClientInvocation{Client: "claude-code", Version: "2.1.312", Program: "claude", Args: []string{"-p"},
		ArgsDigest: "sha256:00", StdinDigest: "sha256:00"}
	r := newRig(t, rigOptions{
		turns:   []fakemodel.Turn{{Events: []fakemodel.Event{fakemodel.Text("partial")}, Hang: true}},
		options: func(o *driver.Options) { o.Models = clientRoute{Models: o.Models, inv: inv} },
	})
	r.send(agent.ListNextTurn, "start")
	r.waitRequest(1)
	r.prefixServed(1)
	must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeStep, Inbox: agent.InboxKeep}))
	r.idle()
	msgs := ofType(r.events(), "assistant/message")
	if len(msgs) != 1 {
		t.Fatalf("%d assistant messages, want the interrupted prefix", len(msgs))
	}
	p := decode[event.AssistantMessagePayload](t, msgs[0])
	if p.Client == nil || p.Client.Client != "claude-code" || p.Client.Version != "2.1.312" || p.RequestDigest == "" {
		t.Fatalf("the interrupted message lost its client invocation: %s", msgs[0].Raw())
	}
	checkClosed(t, r.sess)
}
