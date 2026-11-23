package driver_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// recPolicy is a checkpoint policy mounted on the driver's context: it records
// each point with the type of the session's last event, then flushes. fail, when
// set, fails the checkpoint at that point instead of flushing.
type recPolicy struct {
	mu     sync.Mutex
	sess   session.ReadSession
	points []string
	fail   checkpoint.Point
}

func (p *recPolicy) Before(ctx context.Context, s checkpoint.Session, at checkpoint.Point) error {
	p.mu.Lock()
	last := ""
	if p.sess != nil {
		if evs, err := p.sess.Read(ctx, 0, 0); err == nil && len(evs) > 0 {
			last = evs[len(evs)-1].Type()
		}
	}
	p.points = append(p.points, string(at)+"@"+last)
	fail := p.fail
	p.mu.Unlock()
	if fail == at {
		return errFlaky
	}
	_, err := s.Flush(ctx)
	return err
}

func (p *recPolicy) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.points)
}

// mounted is a rig whose driver and tool service run under pol, mounted on
// their context under checkpoint.Key.
func mounted(t *testing.T, pol *recPolicy, turns ...fakemodel.Turn) *rig {
	t.Helper()
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	must(checkpoint.Key.Provide(root, checkpoint.Policy(pol)))
	r := newRig(t, rigOptions{root: root, svc: svc, turns: turns})
	pol.mu.Lock()
	pol.sess = r.sess
	pol.mu.Unlock()
	return r
}

// DH-D-STORE-4 at the driver: one step with one tool call takes the mounted
// policy's checkpoint before the attempt, before the tool body and after the
// step/end, in that order; the closing step takes Attempt and StepEnd.
//
//dsh:test DH-T-STORE-8
func TestMountedCheckpointGovernsEveryPoint(t *testing.T) {
	pol := &recPolicy{}
	r := mounted(t, pol, fakemodel.Calls(echo("c1")), fakemodel.Reply("done"))
	r.send(agent.ListNextTurn, "hi")
	r.idle()
	got := pol.seen()
	points := make([]string, len(got))
	for i, p := range got {
		points[i], _, _ = strings.Cut(p, "@")
	}
	want := []string{
		string(checkpoint.Attempt), string(checkpoint.ToolBody), string(checkpoint.StepEnd),
		string(checkpoint.Attempt), string(checkpoint.StepEnd),
	}
	if !slices.Equal(points, want) {
		t.Fatalf("checkpoints %q, want %q", got, want)
	}
	// The step-end checkpoint follows its step/end; the tool body's follows the
	// call's tool/call record.
	if got[2] != "step-end@step/end" || got[4] != "step-end@step/end" {
		t.Fatalf("step-end checkpoints %q do not follow step/end", got)
	}
	if !strings.HasPrefix(got[1], "tool-body@tool/") {
		t.Fatalf("tool-body checkpoint %q does not follow the call's record", got[1])
	}
	if ends := turnEnds(t, r.events()); len(ends) != 1 || ends[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v, want one completed", ends)
	}
	checkClosed(t, r.sess)
}

// A failed step-end checkpoint ends the turn error, reason driver/flush, with
// the step closed; the next step's attempt never runs.
func TestFailedStepEndCheckpointEndsTheTurn(t *testing.T) {
	pol := &recPolicy{fail: checkpoint.StepEnd}
	r := mounted(t, pol, fakemodel.Calls(echo("c1")))
	r.send(agent.ListNextTurn, "hi")
	r.idle()
	evs := r.events()
	ends := turnEnds(t, evs)
	if len(ends) != 1 || ends[0].Kind != event.ReasonError || ends[0].By != "driver/flush" || !strings.Contains(ends[0].Detail, errFlaky.Error()) {
		t.Fatalf("turn/end %+v, want one error by driver/flush", ends)
	}
	if n := len(ofType(evs, "step/start")); n != 1 {
		t.Fatalf("%d steps ran after the failed checkpoint, want 1", n)
	}
	checkClosed(t, r.sess)
	if r.drv.Err() != nil {
		t.Fatalf("driver stopped: %v", r.drv.Err())
	}
}

// A failed attempt checkpoint is a checkpoint.Error: the attempt does not run
// and the turn ends error, reason driver/flush.
func TestFailedAttemptCheckpointSkipsTheAttempt(t *testing.T) {
	pol := &recPolicy{fail: checkpoint.Attempt}
	r := mounted(t, pol)
	r.send(agent.ListNextTurn, "hi")
	r.idle()
	ends := turnEnds(t, r.events())
	if len(ends) != 1 || ends[0].Kind != event.ReasonError || ends[0].By != "driver/flush" ||
		!strings.Contains(ends[0].Detail, string(checkpoint.CodeFailed)) {
		t.Fatalf("turn/end %+v, want one error by driver/flush", ends)
	}
	if n := len(r.sent.requests()); n != 0 {
		t.Fatalf("%d model requests sent past a failed checkpoint", n)
	}
	if errors.Is(r.drv.Err(), checkpoint.ErrFailed) {
		t.Fatalf("driver stopped on a retryable checkpoint failure: %v", r.drv.Err())
	}
	checkClosed(t, r.sess)
}
