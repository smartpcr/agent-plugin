package driver_test

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/retry"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// retryWaits is the llm.retry component mounted on a rig's driver (M1-D-PIPE-2
// A), recording into the driver's session under the route's default policy,
// with the driver's waits and the RetryWaited notices it sent.
type retryWaits struct {
	mu      sync.Mutex
	sess    session.Session
	waits   []time.Duration
	notices []agent.RetryWaitEnded
}

// mountRetry registers the component on root, the notice observer, and returns
// the driver options hook that hands the component the session and records
// every wait the driver starts on clk.
func mountRetry(t *testing.T, root *cordis.Context, clk *clock.Manual) (*retryWaits, func(*driver.Options)) {
	t.Helper()
	w := &retryWaits{}
	c := must(retry.Listen(root, retry.Config{
		Record: func(ctx context.Context, _ agent.AttemptFailure, d event.Draft) error {
			w.mu.Lock()
			s := w.sess
			w.mu.Unlock()
			_, err := s.Append(ctx, d)
			return err
		},
		// The scripted route's own policy never retries; the default one does.
		Policy: func(agent.AttemptFailure) llm.RetryPolicy { return llm.DefaultRetry() },
		Now:    clk.Now, Jitter: func() float64 { return 0.5 },
	}))
	t.Cleanup(c.Close)
	must(agent.RetryWaited.On(root, func(_ *cordis.Context, n agent.RetryWaitEnded) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.notices = append(w.notices, n)
		return nil
	}))
	return w, func(o *driver.Options) {
		w.mu.Lock()
		w.sess = o.Session
		w.mu.Unlock()
		o.After = func(d time.Duration) <-chan time.Time {
			w.mu.Lock()
			w.waits = append(w.waits, d)
			w.mu.Unlock()
			return clk.After(d)
		}
	}
}

func (w *retryWaits) snapshot() ([]time.Duration, []agent.RetryWaitEnded) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Clone(w.waits), slices.Clone(w.notices)
}

// types is the log's event types, of those in keep only.
func types(evs []event.Event, keep ...string) []string {
	var out []string
	for _, e := range evs {
		if slices.Contains(keep, e.Type()) {
			out = append(out, e.Type())
		}
	}
	return out
}

// DH-T-LLM-7, DH-T-AGENT-10: with the retry component mounted, the component
// decides and logs llm/retry before the wait; the driver waits the decided
// delay exactly, on its own clock, sends RetryWaited, and the component logs
// llm/retry-started before the next attempt.
//
//dsh:test DH-T-LLM-7
func TestRetryComponentDecidesAndTheDriverWaits(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	clk := clock.NewManual(time.Unix(1_700_000_000, 0))
	w, hook := mountRetry(t, root, clk)
	r := newRig(t, rigOptions{root: root, svc: svc, clock: clk, options: hook, turns: []fakemodel.Turn{
		fakemodel.Status(503), fakemodel.Reply("ok")}})
	r.send(agent.ListNextTurn, "go")
	if !clk.BlockUntil(1, 10*time.Second) {
		t.Fatal("the driver never waited to retry")
	}
	// The decision is in the log before the driver's timer runs out.
	if got := types(r.events(), "assistant/attempt", "llm/retry", "llm/retry-started"); !slices.Equal(got, []string{"assistant/attempt", "llm/retry"}) {
		t.Fatalf("log while waiting %v", got)
	}
	clk.Advance(500 * time.Millisecond)
	r.idle()
	evs := r.events()
	want := []string{"assistant/attempt", "llm/retry", "llm/retry-started", "assistant/message", "step/end", "turn/end"}
	if got := types(evs, want...); !slices.Equal(got, want) {
		t.Fatalf("log %v, want %v", got, want)
	}
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonCompleted {
		t.Fatalf("turn/end %+v", got)
	}
	waits, notices := w.snapshot()
	if !slices.Equal(waits, []time.Duration{500 * time.Millisecond}) {
		t.Fatalf("the driver waited %v, want the decided 500ms once", waits)
	}
	if len(notices) != 1 || notices[0].Cancelled || notices[0].Attempt != 1 || notices[0].Step != 1 || notices[0].Turn != 1 {
		t.Fatalf("RetryWaited %+v", notices)
	}
	if n := len(r.sent.requests()); n != 2 {
		t.Fatalf("%d requests sent, want 2", n)
	}
	checkClosed(t, r.sess)
}

// DH-T-LLM-7, DH-T-AGENT-10: a user cancel during the driver's retry wait ends
// the step with cause user and the turn aborted{user}, not error with the
// provider's failure; the component logs llm/retry-abandoned "cancelled" before
// step/end, and no further attempt runs.
//
//dsh:test DH-T-LLM-7
func TestCancelDuringRetryComponentWaitEndsWithTheCause(t *testing.T) {
	root, svc := setup(t)
	must(svc.Register(root, tool("echo")))
	clk := clock.NewManual(time.Unix(1_700_000_000, 0))
	w, hook := mountRetry(t, root, clk)
	r := newRig(t, rigOptions{root: root, svc: svc, clock: clk, options: hook, turns: []fakemodel.Turn{fakemodel.Status(503)}})
	r.send(agent.ListNextTurn, "go")
	if !clk.BlockUntil(1, 10*time.Second) {
		t.Fatal("the driver never waited to retry")
	}
	must(0, r.drv.Cancel(agent.CauseUser, agent.CancelOptions{Scope: agent.ScopeTurn, Inbox: agent.InboxKeep}))
	r.idle()
	evs := r.events()
	if got := turnEnds(t, evs); len(got) != 1 || got[0].Kind != event.ReasonAborted || got[0].Cause != string(agent.CauseUser) || got[0].Failure != nil {
		t.Fatalf("turn/end %+v, want aborted{user} without the provider's failure", got)
	}
	if se := stepEnds(t, evs); len(se) != 1 || se[0].Cause != string(agent.CauseUser) {
		t.Fatalf("step/end %+v, want cause user", se)
	}
	want := []string{"assistant/attempt", "llm/retry", "llm/retry-abandoned", "step/end", "turn/end"}
	if got := types(evs, append(want, "llm/retry-started", "assistant/message")...); !slices.Equal(got, want) {
		t.Fatalf("log %v, want %v", got, want)
	}
	ab := decode[retry.Abandoned](t, ofType(evs, "llm/retry-abandoned")[0])
	if ab.Reason != llm.AbandonCancelled || ab.Attempt != 1 {
		t.Fatalf("llm/retry-abandoned %+v", ab)
	}
	if _, notices := w.snapshot(); len(notices) != 1 || !notices[0].Cancelled {
		t.Fatalf("RetryWaited %+v, want one cancelled", notices)
	}
	if n := len(r.sent.requests()); n != 1 {
		t.Fatalf("%d requests sent, want no attempt after the cancel", n)
	}
	checkClosed(t, r.sess)
}
