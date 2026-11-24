package driver_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// DH-ADR-BUNDLE-6: a waking send made inside a maintenance task opens its turn
// only after the task returns; a task whose context ends while the gate is held
// does not run.
func TestMaintainHoldsTheTurnBack(t *testing.T) {
	r := newRig(t, rigOptions{turns: []fakemodel.Turn{fakemodel.Reply("ok")}})
	err := r.drv.Maintain(bg, func(context.Context) error {
		r.send(agent.ListNextTurn, "go")
		select {
		case <-r.requests:
			t.Error("a model request was sent inside the maintenance task")
		case <-time.After(150 * time.Millisecond):
		}
		if n := len(ofType(r.events(), "turn/start")); n != 0 {
			t.Errorf("%d turns opened inside the maintenance task", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	r.idle()
	if got := turnEnds(t, r.events()); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end after the task %+v", got)
	}

	in, out := make(chan struct{}), make(chan struct{})
	go func() {
		_ = r.drv.Maintain(bg, func(context.Context) error { close(in); <-out; return nil })
	}()
	<-in
	ctx, cancel := context.WithCancel(bg)
	cancel()
	ran := false
	if err := r.drv.Maintain(ctx, func(context.Context) error { ran = true; return nil }); !errors.Is(err, context.Canceled) || ran {
		t.Fatalf("maintain under an ended context = %v, ran %v", err, ran)
	}
	close(out)
}
