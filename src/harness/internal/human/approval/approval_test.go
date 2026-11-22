package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
)

// recLog records the drafts appended to it; fail makes the n-th append (1-based)
// fail.
type recLog struct {
	mu     sync.Mutex
	drafts []event.Draft
	fail   int
}

func (l *recLog) Append(_ context.Context, d event.Draft) (event.Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail == len(l.drafts)+1 {
		return event.Receipt{}, errors.New("disk full")
	}
	l.drafts = append(l.drafts, d)
	return event.Receipt{Seq: event.Seq(len(l.drafts))}, nil
}

func (l *recLog) types() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, d := range l.drafts {
		out = append(out, d.Type())
	}
	return out
}

func (l *recLog) decided(t *testing.T) approval.Decided {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, d := range l.drafts {
		if d.Type() == "approval/decided" {
			var v approval.Decided
			if err := json.Unmarshal([]byte(d.Raw()), &v); err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	t.Fatal("no approval/decided appended")
	return approval.Decided{}
}

func (l *recLog) asked(t *testing.T) approval.Asked {
	t.Helper()
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, d := range l.drafts {
		if d.Type() == "approval/asked" {
			var v approval.Asked
			if err := json.Unmarshal([]byte(d.Raw()), &v); err != nil {
				t.Fatal(err)
			}
			return v
		}
	}
	t.Fatal("no approval/asked appended")
	return approval.Asked{}
}

var me = agent.Ref{Session: session.ID("s-approval"), Incarnation: 1}

func req() approval.Request {
	return approval.Request{Agent: me, Tool: "bash", Call: "call-1", Origin: "model", Reason: "writes", Display: `{"cmd":"ls"}`}
}

// bound returns a registry whose agent me is bound to channel "tui" with an
// answerer scripted with replies.
func bound(t *testing.T, replies ...answerer.Reply) (human.Registry, *answerer.Answerer) {
	t.Helper()
	root := cordis.New()
	t.Cleanup(func() { root.Dispose() })
	reg := human.New()
	a := answerer.Register(t, root, reg, "tui", replies...)
	if _, err := reg.Bind(root, me, human.Binding{Channel: "tui"}); err != nil {
		t.Fatal(err)
	}
	return reg, a
}

func wantPair(t *testing.T, log *recLog) {
	t.Helper()
	if got := strings.Join(log.types(), ","); got != "approval/asked,approval/decided" {
		t.Fatalf("audit %s, want approval/asked,approval/decided", got)
	}
	if a, d := log.asked(t), log.decided(t); a.ID != d.ID || a.ID == "" {
		t.Fatalf("asked id %q and decided id %q differ", a.ID, d.ID)
	}
}

// TestApproved: an allow-once answer is approved and attributed to the person on
// the channel; the item carried the call and its display.
func TestApproved(t *testing.T) {
	reg, a := bound(t, answerer.AllowOnce())
	log := &recLog{}
	d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), log, req())
	if err != nil {
		t.Fatal(err)
	}
	if !d.Approved() || d.By != (approval.By{Principal: "testkit", Channel: "tui"}) {
		t.Fatalf("decision %+v, want approved by testkit on tui", d)
	}
	wantPair(t, log)
	if got := log.decided(t); got.Outcome != approval.OutcomeApproved || got.By.Principal != "testkit" || got.Call != "call-1" {
		t.Fatalf("approval/decided %+v", got)
	}
	items := a.Asked()
	if len(items) != 1 || items[0].Kind != human.KindApproval || items[0].Call != "call-1" || string(items[0].ID) != d.ID {
		t.Fatalf("asked %+v", items)
	}
	if p, ok := items[0].Payload.(approval.Prompt); !ok || p.Display != `{"cmd":"ls"}` || p.Tool != "bash" {
		t.Fatalf("payload %#v", items[0].Payload)
	}
}

// TestAllowSessionIsApprovedOnce: a session grant answer approves this call only.
func TestAllowSessionIsApprovedOnce(t *testing.T) {
	reg, _ := bound(t, answerer.AllowSession())
	d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), &recLog{}, req())
	if err != nil || d.Outcome != approval.OutcomeApproved {
		t.Fatalf("decision %+v, %v", d, err)
	}
}

// TestDenied: a denial is denied and attributed.
func TestDenied(t *testing.T) {
	reg, _ := bound(t, answerer.Deny())
	log := &recLog{}
	d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), log, req())
	if err != nil {
		t.Fatal(err)
	}
	if d.Outcome != approval.OutcomeDenied || d.Approved() || d.By.Principal != "testkit" {
		t.Fatalf("decision %+v", d)
	}
	wantPair(t, log)
	if got := log.decided(t); got.Outcome != approval.OutcomeDenied {
		t.Fatalf("approval/decided %+v", got)
	}
}

// TestTimeout: an answerer that never answers times out on the service's clock,
// attributed to the harness; the item is withdrawn.
func TestTimeout(t *testing.T) {
	reg, a := bound(t, answerer.Never())
	clk := clock.NewManual(time.Unix(0, 0))
	svc := approval.New(approval.Config{Registry: reg, Timeout: time.Minute, After: clk.After})
	log := &recLog{}
	type res struct {
		d   approval.Decision
		err error
	}
	done := make(chan res, 1)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	wg.Go(func() {
		d, err := svc.Request(t.Context(), log, req())
		done <- res{d, err}
	})
	it := a.Next(t)
	if !clk.BlockUntil(1, 5*time.Second) {
		t.Fatal("the service started no timer")
	}
	clk.Advance(time.Minute)
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	if r.d.Outcome != approval.OutcomeTimeout || r.d.By.Principal != approval.PrincipalHarness {
		t.Fatalf("decision %+v, want timeout by harness", r.d)
	}
	wantPair(t, log)
	if got := log.asked(t); got.Timeout != time.Minute.Milliseconds() {
		t.Fatalf("approval/asked timeout %d ms", got.Timeout)
	}
	if _, ok := a.Withdrawn(it.ID); !ok {
		t.Fatal("the timed-out item was not withdrawn from the answerer")
	}
}

// TestNoAnswererMeansNo: nobody to ask -- no registry, an unbound agent, a
// channel without an answerer, a stopped host -- is no-answerer, logged, never
// approved (SP-7).
func TestNoAnswererMeansNo(t *testing.T) {
	cases := map[string]func(t *testing.T) human.Registry{
		"no-registry": func(*testing.T) human.Registry { return nil },
		"unbound":     func(*testing.T) human.Registry { return human.New() },
		"no-answerer": func(t *testing.T) human.Registry {
			root := cordis.New()
			t.Cleanup(func() { root.Dispose() })
			reg := human.New()
			if _, err := reg.Bind(root, me, human.Binding{Channel: "gone"}); err != nil {
				t.Fatal(err)
			}
			return reg
		},
		"host-stopping": func(*testing.T) human.Registry { reg := human.New(); reg.Close(); return reg },
	}
	for name, mk := range cases {
		t.Run(name, func(t *testing.T) {
			log := &recLog{}
			d, err := approval.New(approval.Config{Registry: mk(t)}).Request(t.Context(), log, req())
			if err != nil {
				t.Fatal(err)
			}
			if d.Outcome != approval.OutcomeNoAnswerer || d.Approved() || d.Detail != name {
				t.Fatalf("decision %+v, want no-answerer (%s)", d, name)
			}
			wantPair(t, log)
			if got := log.decided(t); got.Outcome != approval.OutcomeNoAnswerer || got.By.Principal != approval.PrincipalHarness {
				t.Fatalf("approval/decided %+v", got)
			}
		})
	}
}

// panicky is an answerer whose Deliver panics.
type panicky struct{}

func (panicky) Deliver(context.Context, human.Item) error { panic("boom") }
func (panicky) Withdraw(human.ID, human.Reason)           {}

// TestFailedAnswererMeansNo: an answerer that fails delivery settles
// no-answerer, never an approval.
func TestFailedAnswererMeansNo(t *testing.T) {
	root := cordis.New()
	t.Cleanup(func() { root.Dispose() })
	reg := human.New()
	if _, err := reg.RegisterAnswerer(root, "tui", panicky{}); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Bind(root, me, human.Binding{Channel: "tui"}); err != nil {
		t.Fatal(err)
	}
	d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), &recLog{}, req())
	if err != nil || d.Outcome != approval.OutcomeNoAnswerer {
		t.Fatalf("decision %+v, %v; want no-answerer", d, err)
	}
}

// TestCancelledIsDenied: the caller's cancellation is a denial, still audited.
func TestCancelledIsDenied(t *testing.T) {
	reg, a := bound(t, answerer.Never())
	ctx, cancel := context.WithCancel(t.Context())
	log := &recLog{}
	done := make(chan approval.Decision, 1)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	wg.Go(func() {
		d, _ := approval.New(approval.Config{Registry: reg}).Request(ctx, log, req())
		done <- d
	})
	a.Next(t)
	cancel()
	d := <-done
	if d.Outcome != approval.OutcomeDenied || !strings.HasPrefix(d.Detail, "cancelled") {
		t.Fatalf("decision %+v, want denied (cancelled)", d)
	}
	wantPair(t, log)
}

// TestAuditFailureRefuses: a request whose audit cannot be written is refused
// with an error; when approval/asked fails nobody is asked.
func TestAuditFailureRefuses(t *testing.T) {
	for _, n := range []int{1, 2} {
		var replies []answerer.Reply
		if n == 2 {
			// Nobody is asked when approval/asked fails, so only n == 2 consumes it.
			replies = append(replies, answerer.AllowOnce())
		}
		reg, a := bound(t, replies...)
		d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), &recLog{fail: n}, req())
		if err == nil || d.Approved() {
			t.Fatalf("append %d failing: decision %+v, %v; want a refusal with an error", n, d, err)
		}
		if n == 1 && len(a.Asked()) != 0 {
			t.Fatal("asked although approval/asked was not written")
		}
	}
}

// TestInvalidRequest: a request without its agent or tool is refused.
func TestInvalidRequest(t *testing.T) {
	svc := approval.New(approval.Config{})
	if d, err := svc.Request(t.Context(), &recLog{}, approval.Request{Tool: "bash"}); err == nil || d.Approved() {
		t.Fatalf("no agent: %+v, %v", d, err)
	}
	if d, err := svc.Request(t.Context(), nil, req()); err == nil || d.Approved() {
		t.Fatalf("no log: %+v, %v", d, err)
	}
}

// TestDecidedRefusesAnOpenOutcome: approval/decided admits only the closed set.
func TestDecidedRefusesAnOpenOutcome(t *testing.T) {
	if _, err := approval.DecidedType.Draft(approval.Decided{ID: "x", Tool: "t", Outcome: "maybe", By: approval.By{Principal: "p"}}); err == nil {
		t.Fatal("an outcome outside the closed set was drafted")
	}
	if _, err := approval.DecidedType.Draft(approval.Decided{ID: "x", Tool: "t", Outcome: approval.OutcomeApproved}); err == nil {
		t.Fatal("an unattributed decision was drafted")
	}
}
