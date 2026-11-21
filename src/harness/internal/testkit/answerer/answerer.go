// Package answerer is the test kit's scripted human (DH-D-UTIL-6). An Answerer is
// a human.Answerer registered on a real interaction registry: each delivered item
// takes the next scripted reply, which it settles through the registry's Answer
// as a person on its channel would, off the delivering goroutine (Deliver must
// not block). A reply of Never leaves the item pending, the shape of a person who
// walked away.
package answerer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
)

// Reply is one scripted reply.
type Reply struct {
	Answer human.Answer
	Never  bool
}

// AllowOnce allows an approval once.
func AllowOnce() Reply { return Reply{Answer: human.Answer{Outcome: human.OutcomeAllowedOnce}} }

// AllowSession allows an approval for the session.
func AllowSession() Reply { return Reply{Answer: human.Answer{Outcome: human.OutcomeAllowedSession}} }

// Deny denies an approval.
func Deny() Reply { return Reply{Answer: human.Answer{Outcome: human.OutcomeDenied}} }

// Text answers a question.
func Text(s string) Reply {
	return Reply{Answer: human.Answer{Outcome: human.OutcomeAnswered, Text: s}}
}

// Never leaves the item unanswered.
func Never() Reply { return Reply{Never: true} }

// Answerer is a scripted answerer on one channel.
type Answerer struct {
	t   testing.TB
	reg human.Registry
	by  human.Actor

	mu        sync.Mutex
	script    []Reply
	asked     []human.Item
	withdrawn map[human.ID]human.Reason
	errs      []error
	got       chan human.Item
	wg        sync.WaitGroup
}

// Register registers an answerer for channel ch on reg, owned by owner, scripted
// with replies; it answers as a person named "testkit". When the test ends the
// registration is released, in-flight answers are awaited and any reply the
// registry refused, or still scripted, fails the test.
func Register(t testing.TB, owner *cordis.Context, reg human.Registry, ch human.Channel, replies ...Reply) *Answerer {
	t.Helper()
	a := &Answerer{t: t, reg: reg, by: human.Actor{Channel: ch, Name: "testkit"}, script: replies,
		withdrawn: map[human.ID]human.Reason{}, got: make(chan human.Item, 64)}
	h, err := reg.RegisterAnswerer(owner, ch, a)
	if err != nil {
		t.Fatalf("answerer: %v", err)
	}
	t.Cleanup(func() {
		h.Dispose()
		a.wg.Wait()
		a.mu.Lock()
		defer a.mu.Unlock()
		for _, err := range a.errs {
			t.Errorf("answerer: %v", err)
		}
		if n := len(a.script); n != 0 {
			t.Errorf("answerer: %d scripted repl(ies) never asked for", n)
		}
	})
	return a
}

// Deliver records it and settles it with the next reply.
func (a *Answerer) Deliver(_ context.Context, it human.Item) error {
	a.mu.Lock()
	a.asked = append(a.asked, it)
	r := Never()
	if len(a.script) > 0 {
		r, a.script = a.script[0], a.script[1:]
	} else {
		a.errs = append(a.errs, &unscripted{it.ID})
	}
	a.mu.Unlock()
	select {
	case a.got <- it:
	default:
	}
	if r.Never {
		return nil
	}
	a.wg.Go(func() {
		if err := a.reg.Answer(context.Background(), a.by, it.ID, r.Answer); err != nil {
			a.mu.Lock()
			a.errs = append(a.errs, err)
			a.mu.Unlock()
		}
	})
	return nil
}

// Withdraw records why the registry withdrew id.
func (a *Answerer) Withdraw(id human.ID, why human.Reason) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.withdrawn[id] = why
}

// Actor is who the answerer answers as.
func (a *Answerer) Actor() human.Actor { return a.by }

// Asked is every item delivered so far, in order.
func (a *Answerer) Asked() []human.Item {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]human.Item(nil), a.asked...)
}

// Withdrawn is why id was withdrawn, and whether it was.
func (a *Answerer) Withdrawn(id human.ID) (human.Reason, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	why, ok := a.withdrawn[id]
	return why, ok
}

// Next waits up to five seconds for the next delivered item.
func (a *Answerer) Next(t testing.TB) human.Item {
	t.Helper()
	select {
	case it := <-a.got:
		return it
	case <-time.After(5 * time.Second):
		t.Fatal("answerer: nothing was delivered")
		return human.Item{}
	}
}

type unscripted struct{ id human.ID }

func (u *unscripted) Error() string { return "unscripted delivery of " + string(u.id) }
