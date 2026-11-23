package approval_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
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
	// failType fails every append of this event type.
	failType string
}

func (l *recLog) Append(_ context.Context, d event.Draft) (event.Receipt, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.fail == len(l.drafts)+1 || (l.failType != "" && d.Type() == l.failType) {
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

// peerAnswerer is a fake carrier whose answers carry one actor: it counts the
// items delivered to it and answers each with ans through the registry, off the
// delivering goroutine.
type peerAnswerer struct {
	reg   human.Registry
	by    human.Actor
	ans   human.Answer
	count atomic.Int32
	wg    sync.WaitGroup
	errs  chan error
}

func newPeerAnswerer(t *testing.T, root *cordis.Context, reg human.Registry, by human.Actor, ans human.Answer) *peerAnswerer {
	t.Helper()
	p := &peerAnswerer{reg: reg, by: by, ans: ans, errs: make(chan error, 16)}
	h, err := reg.RegisterAnswerer(root, by.Channel, p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		h.Dispose()
		p.wg.Wait()
		close(p.errs)
		for err := range p.errs {
			t.Errorf("answer: %v", err)
		}
	})
	return p
}

func (p *peerAnswerer) Deliver(_ context.Context, it human.Item) error {
	p.count.Add(1)
	p.wg.Go(func() {
		if err := p.reg.Answer(context.Background(), p.by, it.ID, p.ans); err != nil {
			p.errs <- err
		}
	})
	return nil
}

func (p *peerAnswerer) Withdraw(human.ID, human.Reason) {}

// TestAllowedOnce: an allow-once answer is allowed-once and attributed to the
// person on the channel, from the in-process peer of a test answerer; the item
// carried the call and its display.
//
//dsh:test DH-T-HUMAN-3, DH-T-HUMAN-4
func TestAllowedOnce(t *testing.T) {
	reg, a := bound(t, answerer.AllowOnce())
	log := &recLog{}
	d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), log, req())
	if err != nil {
		t.Fatal(err)
	}
	want := approval.By{Principal: "testkit", Channel: "tui", Peer: string(human.PeerInProcess)}
	if !d.Approved() || d.Outcome != approval.OutcomeAllowedOnce || d.By != want || d.Grant != "" {
		t.Fatalf("decision %+v, want allowed-once by %+v", d, want)
	}
	wantPair(t, log)
	if got := log.decided(t); got.Outcome != approval.OutcomeAllowedOnce || got.By != want || got.Call != "call-1" {
		t.Fatalf("approval/decided %+v", got)
	}
	if got := log.asked(t); got.Kind != approval.KindApproval || got.Timeout != 0 {
		t.Fatalf("approval/asked %+v, want kind approval and no timeout", got)
	}
	items := a.Asked()
	if len(items) != 1 || items[0].Kind != human.KindApproval || items[0].Call != "call-1" || string(items[0].ID) != d.ID {
		t.Fatalf("asked %+v", items)
	}
	if p, ok := items[0].Payload.(approval.Prompt); !ok || p.Display != `{"cmd":"ls"}` || p.Tool != "bash" {
		t.Fatalf("payload %#v", items[0].Payload)
	}
}

// Scenario "The decision names its peer" (16.1): a fake answerer whose actor has
// peer p1 allows a call once; approval/decided v2 names principal, channel, peer
// p1 and no grant id.
//
//dsh:test DH-T-HUMAN-4
func TestDecisionNamesItsPeer(t *testing.T) {
	root := cordis.New()
	t.Cleanup(root.Dispose)
	reg := human.New()
	newPeerAnswerer(t, root, reg, human.Actor{Channel: "ui", Name: "alice", Peer: "p1"}, human.Answer{Outcome: human.OutcomeAllowedOnce})
	if _, err := reg.Bind(root, me, human.Binding{Channel: "ui"}); err != nil {
		t.Fatal(err)
	}
	log := &recLog{}
	if _, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), log, req()); err != nil {
		t.Fatal(err)
	}
	wantPair(t, log)
	got := log.decided(t)
	if got.Outcome != approval.OutcomeAllowedOnce || got.By != (approval.By{Principal: "alice", Channel: "ui", Peer: "p1"}) || got.Grant != "" {
		t.Fatalf("approval/decided %+v, want allowed-once by alice on ui from p1, no grant", got)
	}
	var raw map[string]any
	log.mu.Lock()
	for _, d := range log.drafts {
		if d.Type() == approval.DecidedType.Name() {
			if err := json.Unmarshal([]byte(d.Raw()), &raw); err != nil {
				t.Fatal(err)
			}
		}
	}
	log.mu.Unlock()
	if _, has := raw["grant"]; has {
		t.Fatalf("the record names a grant: %v", raw)
	}
}

// TestAllowSessionIsAllowedOnce: a session grant answer, with no grant table,
// allows this call only.
//
//dsh:test DH-T-HUMAN-3
func TestAllowSessionIsAllowedOnce(t *testing.T) {
	reg, _ := bound(t, answerer.AllowSession())
	d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), &recLog{}, req())
	if err != nil || d.Outcome != approval.OutcomeAllowedOnce || d.Grant != "" {
		t.Fatalf("decision %+v, %v", d, err)
	}
}

// TestRejected: a person's no is rejected and attributed.
//
//dsh:test DH-T-HUMAN-3, DH-T-HUMAN-4
func TestRejected(t *testing.T) {
	reg, _ := bound(t, answerer.Deny())
	log := &recLog{}
	d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), log, req())
	if err != nil {
		t.Fatal(err)
	}
	if d.Outcome != approval.OutcomeRejected || d.Approved() || d.By.Principal != "testkit" || d.By.Channel != "tui" {
		t.Fatalf("decision %+v", d)
	}
	wantPair(t, log)
	if got := log.decided(t); got.Outcome != approval.OutcomeRejected {
		t.Fatalf("approval/decided %+v", got)
	}
}

// TestTimeout: with a set timeout, an answerer that never answers is unavailable
// with detail timeout on the service's clock, attributed to the harness; the item
// is withdrawn.
//
//dsh:test DH-T-HUMAN-3
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
	if r.d.Outcome != approval.OutcomeUnavailable || r.d.Detail != approval.DetailTimeout || r.d.By.Principal != approval.PrincipalHarness {
		t.Fatalf("decision %+v, want unavailable (timeout) by harness", r.d)
	}
	wantPair(t, log)
	if got := log.asked(t); got.Timeout != time.Minute.Milliseconds() {
		t.Fatalf("approval/asked timeout %d ms", got.Timeout)
	}
	if _, ok := a.Withdrawn(it.ID); !ok {
		t.Fatal("the timed-out item was not withdrawn from the answerer")
	}
}

// TestDefaultWaits: the default timeout is 0 (M1-D-PIPE-1 A): no timer is started
// and the request waits for its answer.
//
//dsh:test DH-T-HUMAN-3
func TestDefaultWaits(t *testing.T) {
	if approval.DefaultTimeout != 0 {
		t.Fatalf("DefaultTimeout %v, want 0 (wait)", approval.DefaultTimeout)
	}
	reg, _ := bound(t, answerer.AllowOnce())
	var timers atomic.Int32
	svc := approval.New(approval.Config{Registry: reg, After: func(time.Duration) <-chan time.Time { timers.Add(1); return nil }})
	log := &recLog{}
	if d, err := svc.Request(t.Context(), log, req()); err != nil || d.Outcome != approval.OutcomeAllowedOnce {
		t.Fatalf("decision %+v, %v", d, err)
	}
	if n := timers.Load(); n != 0 {
		t.Fatalf("%d timers started, want none", n)
	}
	if got := log.asked(t); got.Timeout != 0 {
		t.Fatalf("approval/asked timeout %d ms, want 0", got.Timeout)
	}
}

// TestUnavailableMeansNo: nobody to ask -- no registry, an unbound agent, a
// channel without an answerer, a stopped host -- is unavailable, logged, never
// allowed (SP-7).
//
//dsh:test DH-T-HUMAN-3
func TestUnavailableMeansNo(t *testing.T) {
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
			if d.Outcome != approval.OutcomeUnavailable || d.Approved() || d.Detail != name {
				t.Fatalf("decision %+v, want unavailable (%s)", d, name)
			}
			wantPair(t, log)
			if got := log.decided(t); got.Outcome != approval.OutcomeUnavailable || got.By.Principal != approval.PrincipalHarness || got.By.Peer != "" {
				t.Fatalf("approval/decided %+v", got)
			}
		})
	}
}

// panicky is an answerer whose Deliver panics.
type panicky struct{}

func (panicky) Deliver(context.Context, human.Item) error { panic("boom") }
func (panicky) Withdraw(human.ID, human.Reason)           {}

// TestFailedAnswererMeansNo: an answerer that fails delivery settles unavailable,
// never an approval.
//
//dsh:test DH-T-HUMAN-3
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
	if err != nil || d.Outcome != approval.OutcomeUnavailable || !strings.HasPrefix(d.Detail, "answerer-failed") {
		t.Fatalf("decision %+v, %v; want unavailable (answerer-failed)", d, err)
	}
}

// odd is a registry whose every answer is the given one, or a panic.
type odd struct {
	human.Registry
	ans   human.Answer
	panic bool
}

func (o odd) Ask(context.Context, human.Item) (human.Answer, human.Actor, error) {
	if o.panic {
		panic("registry down")
	}
	return o.ans, human.Actor{Channel: "ui", Name: "eve"}, nil
}

// TestUnknownAnswerIsUnavailable: an answer outside the approval vocabulary and a
// panicking registry are unavailable, never allowed.
//
//dsh:test DH-T-HUMAN-3
func TestUnknownAnswerIsUnavailable(t *testing.T) {
	for name, reg := range map[string]human.Registry{
		"unknown": odd{ans: human.Answer{Outcome: human.OutcomeAnswered, Text: "yes"}},
		"panic":   odd{panic: true},
	} {
		t.Run(name, func(t *testing.T) {
			log := &recLog{}
			d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), log, req())
			if err != nil || d.Outcome != approval.OutcomeUnavailable || d.Approved() || d.By.Principal != approval.PrincipalHarness {
				t.Fatalf("decision %+v, %v; want unavailable by harness", d, err)
			}
			wantPair(t, log)
		})
	}
}

// TestCancelled: the caller's cancellation is cancelled, still audited.
//
//dsh:test DH-T-HUMAN-3
func TestCancelled(t *testing.T) {
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
	if d.Outcome != approval.OutcomeCancelled || d.Approved() || d.By.Principal != approval.PrincipalHarness {
		t.Fatalf("decision %+v, want cancelled by harness", d)
	}
	wantPair(t, log)
	if got := log.decided(t); got.Outcome != approval.OutcomeCancelled {
		t.Fatalf("approval/decided %+v", got)
	}
}

// TestAuditFailureRefuses: a request whose audit cannot be written is refused
// with an error and no allowed decision; when approval/asked fails nobody is
// asked.
//
//dsh:test DH-T-HUMAN-3
func TestAuditFailureRefuses(t *testing.T) {
	for _, n := range []int{1, 2} {
		var replies []answerer.Reply
		if n == 2 {
			// Nobody is asked when approval/asked fails, so only n == 2 consumes it.
			replies = append(replies, answerer.AllowOnce())
		}
		reg, a := bound(t, replies...)
		d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), &recLog{fail: n}, req())
		if err == nil || d.Approved() || d.Outcome != approval.OutcomeUnavailable {
			t.Fatalf("append %d failing: decision %+v, %v; want a refusal with an error", n, d, err)
		}
		if n == 1 && len(a.Asked()) != 0 {
			t.Fatal("asked although approval/asked was not written")
		}
	}
}

// TestRequestOutsideATurn: a request that belongs to no tool call and no turn
// (a consent, say) is accepted and audited like any other.
//
//dsh:test DH-T-HUMAN-4
func TestRequestOutsideATurn(t *testing.T) {
	reg, _ := bound(t, answerer.AllowOnce())
	log := &recLog{}
	d, err := approval.New(approval.Config{Registry: reg}).Request(t.Context(), log,
		approval.Request{Agent: me, Kind: approval.KindConsent, Tool: "prepare-directory", Reason: "creates /w", Mode: "workspace-write"})
	if err != nil || d.Outcome != approval.OutcomeAllowedOnce {
		t.Fatalf("decision %+v, %v", d, err)
	}
	wantPair(t, log)
	if got := log.asked(t); got.Kind != approval.KindConsent || got.Call != "" || got.Mode != "workspace-write" {
		t.Fatalf("approval/asked %+v", got)
	}
}

// TestInvalidRequest: a request without its agent or tool, or of an unknown kind,
// is refused.
func TestInvalidRequest(t *testing.T) {
	svc := approval.New(approval.Config{})
	if d, err := svc.Request(t.Context(), &recLog{}, approval.Request{Tool: "bash"}); err == nil || d.Approved() {
		t.Fatalf("no agent: %+v, %v", d, err)
	}
	if d, err := svc.Request(t.Context(), nil, req()); err == nil || d.Approved() {
		t.Fatalf("no log: %+v, %v", d, err)
	}
	r := req()
	r.Kind = "question"
	if d, err := svc.Request(t.Context(), &recLog{}, r); err == nil || d.Approved() {
		t.Fatalf("unknown kind: %+v, %v", d, err)
	}
}

// TestDecidedRefusesAnOpenOutcome: approval/decided admits only the closed set,
// and approval/asked only the two kinds.
func TestDecidedRefusesAnOpenOutcome(t *testing.T) {
	if _, err := approval.DecidedType.Draft(approval.Decided{ID: "x", Tool: "t", Outcome: "approved", By: approval.By{Principal: "p"}}); err == nil {
		t.Fatal("an outcome outside the closed set was drafted")
	}
	if _, err := approval.DecidedType.Draft(approval.Decided{ID: "x", Tool: "t", Outcome: approval.OutcomeAllowedOnce}); err == nil {
		t.Fatal("an unattributed decision was drafted")
	}
	if _, err := approval.AskedType.Draft(approval.Asked{ID: "x", Agent: "a", Tool: "t", Kind: "question"}); err == nil {
		t.Fatal("an asked of an unknown kind was drafted")
	}
}

// TestVersion1RecordsUpgrade: a version-1 audit pair decodes into the
// M1-D-PIPE-1 vocabulary (Rule 6): every old outcome maps to its new one, a grant
// id in the detail becomes the grant, and an old asked is an approval.
//
//dsh:test DH-T-HUMAN-3
func TestVersion1RecordsUpgrade(t *testing.T) {
	rec := func(typ, payload string) event.Record {
		return event.Record{Seq: 1, Time: 1, Type: typ, Version: 1, Payload: payload}
	}
	a, err := approval.AskedType.Decode(rec("approval/asked", `{"id":"apr-1","agent":"s/1","tool":"bash","call":"c1","timeout_ms":300000}`))
	if err != nil || a.Kind != approval.KindApproval || a.Timeout != 300000 || a.Subject != nil {
		t.Fatalf("asked v1 = %+v, %v", a, err)
	}
	for _, tc := range []struct {
		outcome, detail string
		want            approval.Outcome
		wantDetail      string
		grant           string
	}{
		{"approved", "", approval.OutcomeAllowedOnce, "", ""},
		{"approved", "allowed-by-grant:grt-1", approval.OutcomeAllowedByGrant, "allowed-by-grant:grt-1", "grt-1"},
		{"approved", "granted:grt-2", approval.OutcomeAllowedOnce, "granted:grt-2", "grt-2"},
		{"approved", "grant-refused:approval/grant-cap", approval.OutcomeAllowedOnce, "grant-refused:approval/grant-cap", ""},
		{"denied", "not now", approval.OutcomeRejected, "not now", ""},
		{"denied", "cancelled: context canceled", approval.OutcomeCancelled, "cancelled: context canceled", ""},
		{"denied", "grant-audit-failed", approval.OutcomeUnavailable, "grant-audit-failed", ""},
		{"denied", "unexpected answer answered", approval.OutcomeUnavailable, "unexpected answer answered", ""},
		{"timeout", "5m0s", approval.OutcomeUnavailable, approval.DetailTimeout, ""},
		{"no-answerer", "unbound", approval.OutcomeUnavailable, "unbound", ""},
	} {
		payload, _ := json.Marshal(map[string]any{"id": "apr-1", "tool": "bash", "outcome": tc.outcome, "detail": tc.detail,
			"by": map[string]string{"principal": "alice", "channel": "ui"}})
		d, err := approval.DecidedType.Decode(rec("approval/decided", string(payload)))
		if err != nil || d.Outcome != tc.want || d.Detail != tc.wantDetail || d.Grant != tc.grant || d.By.Principal != "alice" || d.By.Peer != "" {
			t.Errorf("decided v1 %s %q = %+v, %v; want %s %q grant %q", tc.outcome, tc.detail, d, err, tc.want, tc.wantDetail, tc.grant)
		}
	}
	if _, err := approval.DecidedType.Decode(rec("approval/decided", `{"id":"apr-1","tool":"bash","outcome":"maybe","by":{"principal":"p"}}`)); err == nil {
		t.Error("a v1 outcome outside the old set decoded")
	}
	if _, err := approval.DecidedType.Decode(rec("approval/decided", `{"id":"apr-1","tool":"bash","outcome":7,"by":{"principal":"p"}}`)); err == nil {
		t.Error("a v1 record whose outcome is not text decoded")
	}
}
