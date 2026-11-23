package approval_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// resumer is an agent registry whose stub factory always resumes its session
// (session.Open): the agent of a host that starts over an existing home.
func resumer(t *testing.T, root *cordis.Context, sessions session.Service) agent.Registry {
	t.Helper()
	tab := scope.NewTable(root)
	reg := agent.NewRegistry(root)
	if _, err := reg.SetFactory(root, agent.FactoryFunc(func(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
		h, _, err := sessions.Open(context.Background(), owner, o.Session, session.OpenOptions{})
		if err != nil {
			return nil, err
		}
		sc, err := tab.New(owner, ref.String())
		if err != nil {
			return nil, err
		}
		return &lifeAgent{ref: ref, h: h, sc: sc}, nil
	})); err != nil {
		t.Fatal(err)
	}
	return reg
}

// crashLog writes, on the SQLite home at path, a session whose last turn is
// mid-call: the step asked for bash, the call is recorded, its approval is asked
// and, unless pending, allowed once (so its body may have started), and an
// earlier grant has no end. The host then stops without disposing anything, as
// a crash would. It returns the log's length and the ids of the request and
// grant.
func crashLog(t *testing.T, path string, id session.ID, pending bool) (event.Offset, string, string) {
	t.Helper()
	ctx := context.Background()
	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	root := cordis.New()
	defer root.Dispose()
	h, err := session.NewService(root, st, session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: string(id)}})
	if err != nil {
		t.Fatal(err)
	}
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("clean the build"))
	if err != nil {
		t.Fatal(err)
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock("c1", "bash", json.RawMessage(`{"cmd":"rm -rf build"}`)))
	if err != nil {
		t.Fatal(err)
	}
	const apr, grt = "apr-open", "grt-open"
	ref := agent.Ref{Session: id, Incarnation: 1}
	appendAll(t, h,
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
		func() (event.Draft, error) {
			return approval.GrantCreatedType.Draft(approval.GrantCreated{ID: grt, Agent: ref.String(),
				Subject: approval.Subject{Kind: approval.ScopeDirectory, Path: t.TempDir()}, By: approval.By{Principal: "alice", Channel: "ui"}})
		},
		func() (event.Draft, error) {
			return event.ToolCall.Draft(event.ToolCallPayload{CallID: "c1", Name: "bash", Arguments: `{"cmd":"rm -rf build"}`})
		},
		func() (event.Draft, error) {
			return approval.AskedType.Draft(approval.Asked{ID: apr, Kind: approval.KindApproval, Agent: ref.String(), Tool: "bash", Call: "c1", Origin: "model"})
		})
	if !pending {
		appendAll(t, h, func() (event.Draft, error) {
			return approval.DecidedType.Draft(approval.Decided{ID: apr, Tool: "bash", Call: "c1", Outcome: approval.OutcomeAllowedOnce,
				By: approval.By{Principal: "alice", Channel: "ui", Peer: "p1"}})
		})
	}
	n := h.Len()
	// The host dies: the session's store connection goes, no agent is disposed
	// and no listener sees an end.
	if err := h.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(ctx); err != nil {
		t.Fatal(err)
	}
	return n, apr, grt
}

// Scenario "Crash during an open approval" (16.1), DH-T-HUMAN-4 "a log with an
// open pair is closed on resume before any append": given an SQLite session whose
// log ends with tool/call and approval/asked and no decision, when a new host
// resumes it through the agent registry, then before any other append the log
// gains approval/decided{unavailable, host-stopped} and grant/ended{host-stopped}
// for the open grant, and the core closes the turn, telling the model the call
// did not run (approval/unavailable, not tools/outcome-unknown). Nothing else is
// appended before the registry hands the agent out. Control: when the approval
// was allowed before the crash, the body may have started, so the call still
// closes tools/outcome-unknown and no decision is written.
//
//dsh:test DH-T-HUMAN-4
func TestOpenPairClosedOnResumeBeforeAnyAppend(t *testing.T) {
	for _, tc := range []struct {
		name    string
		pending bool
		want    []string
		code    string
		says    string
	}{
		{"approval pending", true,
			[]string{"tool/result", "step/end", "turn/end", "session/end-seed", "approval/decided", "grant/ended"},
			surface.CodeApprovalUnavailable, "did not run"},
		{"approval allowed (control)", false,
			[]string{"tool/result", "step/end", "turn/end", "session/end-seed", "grant/ended"},
			surface.CodeToolOutcomeUnknown, "is unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) { resumeAfterCrash(t, tc.pending, tc.want, tc.code, tc.says) })
	}
}

// The approval names and codes the session core matches by text (it cannot
// import this package) are this package's and the tools area's.
func TestSurfaceMirrorsTheApprovalNames(t *testing.T) {
	if approval.AskedType.Name() != surface.ApprovalAskedType || approval.DecidedType.Name() != surface.ApprovalDecidedType {
		t.Fatalf("surface matches %q and %q", surface.ApprovalAskedType, surface.ApprovalDecidedType)
	}
	if string(tools.CodeApprovalUnavailable) != surface.CodeApprovalUnavailable {
		t.Fatalf("surface closes held calls with %q, the tools area's code is %q", surface.CodeApprovalUnavailable, tools.CodeApprovalUnavailable)
	}
}

// resumeAfterCrash resumes a crashLog session on a new host and checks what the
// resume appended: exactly want, the call's closer coded code and saying says.
func resumeAfterCrash(t *testing.T, pending bool, want []string, code, says string) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "dsh.db")
	const id = session.ID("s-crash")
	n, apr, grt := crashLog(t, path, id, pending)

	st, err := sqlite.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close(ctx) })
	root := cordis.New()
	t.Cleanup(root.Dispose)
	sessions := session.NewService(root, st, session.Config{})
	grants := approval.NewGrants()
	svc := approval.New(approval.Config{Grants: grants})
	// The service binds before the grant table: decisions first, then grant ends.
	if _, err := svc.Bind(root, sessions); err != nil {
		t.Fatal(err)
	}
	if _, err := grants.Bind(root, sessions); err != nil {
		t.Fatal(err)
	}
	a, err := resumer(t, root, sessions).Resume(root, agent.CreateOptions{Session: id})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Dispose(ctx) })
	h, ok := sessions.Get(id)
	if !ok {
		t.Fatal("the resumed session is not live")
	}
	evs, err := h.Read(ctx, n, 0)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, ev := range evs {
		got = append(got, ev.Type())
	}
	// The core's closers of the open turn (the call, the step, the turn and the
	// end seed), then this area's closers, and nothing else.
	if !slices.Equal(got, want) {
		t.Fatalf("on resume the log gained %s, want %s", strings.Join(got, ","), strings.Join(want, ","))
	}
	res, err := event.ToolResult.As(evs[0])
	if err != nil || res.CallID != "c1" || !res.IsError {
		t.Fatalf("the core's closer %+v, %v", res, err)
	}
	if raw := evs[0].Raw(); !strings.Contains(raw, code+": ") || !strings.Contains(raw, says) {
		t.Fatalf("the model is told %s, want %s (%q)", raw, code, says)
	}
	if pending {
		d, err := approval.DecidedType.As(evs[4])
		if err != nil || d.ID != apr || d.Call != "c1" || d.Tool != "bash" || d.Outcome != approval.OutcomeUnavailable ||
			d.Detail != approval.DetailHostStopped || d.By != (approval.By{Principal: approval.PrincipalHarness}) {
			t.Fatalf("approval/decided %+v, %v; want unavailable host-stopped by harness for %s", d, err, apr)
		}
	}
	e, err := approval.GrantEndedType.As(evs[len(evs)-1])
	if err != nil || e.ID != grt || e.Reason != approval.EndHostStopped {
		t.Fatalf("grant/ended %+v, %v; want %s host-stopped", e, err, grt)
	}
	if len(grants.List(a.Agent().Ref())) != 0 {
		t.Fatal("a grant was read back from the log")
	}
	// A second resume finds nothing open: the closers are written once.
	if open, err := approval.CloseOnResume(ctx, h); err != nil || len(open) != 0 {
		t.Fatalf("a second close found %v, %v", open, err)
	}
}

// CloseOnResume and CloseGrantsOnResume called directly: an open pair is closed
// host-stopped in the order asked; an open grant ends session-ended when its
// holder disposed cleanly and host-stopped when it did not; the policy the log
// set is restored at creation and forgotten at disposal.
//
//dsh:test DH-T-HUMAN-4, DH-T-HUMAN-6
func TestResumeClosersDirectly(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	_, h := liveSession(t, root, me.Session)
	ref := me.String()
	appendAll(t, h,
		func() (event.Draft, error) {
			return approval.AskedType.Draft(approval.Asked{ID: "apr-1", Kind: approval.KindApproval, Agent: ref, Tool: "a"})
		},
		func() (event.Draft, error) {
			return approval.AskedType.Draft(approval.Asked{ID: "apr-2", Kind: approval.KindApproval, Agent: ref, Tool: "b"})
		},
		func() (event.Draft, error) {
			return approval.DecidedType.Draft(approval.Decided{ID: "apr-1", Tool: "a", Outcome: approval.OutcomeRejected, By: approval.By{Principal: "p"}})
		},
		func() (event.Draft, error) {
			return approval.AskedType.Draft(approval.Asked{ID: "apr-3", Kind: approval.KindConsent, Agent: ref, Tool: "c"})
		},
		func() (event.Draft, error) {
			return approval.GrantCreatedType.Draft(approval.GrantCreated{ID: "grt-1", Agent: ref,
				Subject: approval.Subject{Kind: approval.ScopeApplication, Provider: "x", App: "y"}, By: approval.By{Principal: "p"}})
		})
	open, err := approval.CloseOnResume(ctx, h)
	if err != nil || !slices.Equal(open, []string{"apr-2", "apr-3"}) {
		t.Fatalf("closed %v, %v; want apr-2 and apr-3", open, err)
	}
	ended, err := approval.CloseGrantsOnResume(ctx, h, true)
	if err != nil || !slices.Equal(ended, []string{"grt-1"}) {
		t.Fatalf("ended %v, %v", ended, err)
	}
	evs, _ := h.Read(ctx, 0, 0)
	if e, _ := approval.GrantEndedType.As(evs[len(evs)-1]); e.Reason != approval.EndSessionEnded {
		t.Fatalf("a cleanly disposed holder's grant ended %q", e.Reason)
	}
	if open, err := approval.CloseOnResume(ctx, &recLogReader{}); err != nil || len(open) != 0 {
		t.Fatalf("an empty log: %v, %v", open, err)
	}
}

// TestBindRestoresThePolicy: a resumed incarnation is under the policy its log
// last set; a disposed one's is forgotten here, and an agent whose session is not
// live is under the default.
//
//dsh:test DH-T-HUMAN-3
func TestBindRestoresThePolicy(t *testing.T) {
	ctx := context.Background()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	sessions := session.NewService(root, memstore.New(), session.Config{})
	svc := approval.New(approval.Config{})
	if _, err := svc.Bind(root, sessions); err != nil {
		t.Fatal(err)
	}
	reg := lifecycle(t, root, sessions, false)
	a1, err := reg.Create(root, agent.CreateOptions{Session: "s-policy"})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPolicy(ctx, a1.Agent().Ref(), approval.PolicyNever, approval.By{Principal: "owner"}); err != nil {
		t.Fatal(err)
	}
	if err := a1.Dispose(ctx); err != nil {
		t.Fatal(err)
	}
	if p := svc.Policy("s-policy"); p != approval.PolicyAsk {
		t.Fatalf("after disposal the policy is %q here", p)
	}
	a2, err := reg.Create(root, agent.CreateOptions{Session: "s-policy"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a2.Dispose(ctx) })
	if p := svc.Policy("s-policy"); p != approval.PolicyNever {
		t.Fatalf("the resumed session's policy is %q, want never from its log", p)
	}
}

// recLogReader is an empty GrantLog.
type recLogReader struct{ recLog }

func (*recLogReader) Len() event.Offset { return 0 }
func (*recLogReader) Read(context.Context, event.Offset, int, ...session.ReadOption) ([]event.Event, error) {
	return nil, nil
}
