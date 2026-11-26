package sdkserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/iotest"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/permission"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// hang is a model turn that streams text and then holds the turn running.
func hang(text string) fakemodel.Turn {
	return fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text(text)}, Hang: true}
}

// running is id's agent once the model has streamed the first turn's scripted
// events: the turn is then running and held open.
func running(t *testing.T, r *rig, id string) agent.Agent {
	t.Helper()
	select {
	case <-r.tr.PrefixServed(1):
	case <-time.After(10 * time.Second):
		t.Fatalf("session %s never reached the model", id)
	}
	a, ok := r.reg.Get(session.ID(id))
	if !ok || a.Status() != agent.StatusRunning {
		t.Fatalf("session %s is not running", id)
	}
	return a
}

// Values outside delivery, scope, inbox and content type's closed sets are
// jsonrpc/invalid-params, and neither queue input nor cancel the turn.
func TestInvalidEnumsDoNothing(t *testing.T) {
	r := newRig(t, hang("working"))
	cl := connect(t, r.server())
	id := cl.create()
	before := cl.lastSeq(id)

	cl.refused("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("x"), Delivery: "later"}, jsonrpc.ReasonInvalidParams)
	cl.refused("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: []protocol.ContentBlock{{Type: "video"}}}, jsonrpc.ReasonInvalidParams)
	cl.refused("session/query", protocol.SessionQueryParams{SessionID: id, Content: []protocol.ContentBlock{{Type: "audio"}}}, jsonrpc.ReasonInvalidParams)
	if after := cl.lastSeq(id); after != before {
		t.Fatalf("refused prompts moved lastSeq %d -> %d", before, after)
	}

	var rc protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("go"), Delivery: "follow-up"}, &rc)
	a := running(t, r, id)
	cl.refused("session/cancel", protocol.SessionCancelParams{SessionID: id, Scope: "session", Inbox: "keep"}, jsonrpc.ReasonInvalidParams)
	cl.refused("session/cancel", protocol.SessionCancelParams{SessionID: id}, jsonrpc.ReasonInvalidParams)
	cl.refused("session/cancel", protocol.SessionCancelParams{SessionID: id, Inbox: "drop"}, jsonrpc.ReasonInvalidParams)
	if a.Status() != agent.StatusRunning {
		t.Fatalf("a refused cancel stopped the turn: %s", a.Status())
	}
	var cancelled protocol.SessionCancelResult
	cl.call("session/cancel", protocol.SessionCancelParams{SessionID: id, Scope: "turn", Inbox: "keep"}, &cancelled)
	if !cancelled.Cancelled {
		t.Fatalf("cancel = %+v, want the running turn cancelled", cancelled)
	}
}

// Every successful history page's result fits half a frame, measured as
// serialized, with no exception: an event that alone would exceed half a frame
// is refused jsonrpc/line-too-long and paged past with afterSeq.
func TestHistoryPagesFitHalfAFrame(t *testing.T) {
	const frame = 16 << 10
	r := newRig(t, hang("working"))
	r.o.Limits.MaxFrameBytes = frame
	cl := connect(t, r.server())
	id := cl.create()
	oversized := map[int64]bool{}
	prompts := []string{strings.Repeat("<a&", 500), strings.Repeat("<b&", 500), strings.Repeat("<c&", 500), strings.Repeat("<d&", 500),
		strings.Repeat("b", 10_000), strings.Repeat("c", 20_000), "tail"}
	for i, s := range prompts {
		var rc protocol.SessionPromptResult
		cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text(s), MessageID: fmt.Sprint("m", i)}, &rc)
		if i == 4 || i == 5 {
			oversized[rc.Seq] = true
		}
		if i == 0 {
			running(t, r, id)
		}
	}

	var seen []int64
	refused := map[int64]bool{}
	cut := false
	next := int64(0)
	params := map[string]any{"sessionId": id}
	for {
		resp := cl.respond("session/history", params)
		if resp.Error != nil {
			if resp.Error.Reason != jsonrpc.ReasonLineTooLong || !oversized[next] || refused[next] {
				t.Fatalf("history from %d: %v, want line-too-long only at %v", next, resp.Error, oversized)
			}
			refused[next], params["afterSeq"] = true, next
			next++
			continue
		}
		if len(resp.Result) > frame/2 {
			t.Fatalf("a %d-byte page, over half the %d-byte frame", len(resp.Result), frame)
		}
		var page protocol.SessionHistoryResult
		if err := json.Unmarshal(resp.Result, &page); err != nil {
			t.Fatal(err)
		}
		for _, e := range page.Events {
			seen = append(seen, e.Seq)
		}
		if !page.More {
			break
		}
		cut = cut || len(page.Events) > 1
		next = page.Events[len(page.Events)-1].Seq + 1
		params["afterSeq"] = next - 1
	}
	if !cut || len(refused) != len(oversized) {
		t.Fatalf("cut %v, refused %v: want a multi-event cut and every event of %v refused", cut, refused, oversized)
	}
	want := int64(0)
	for _, s := range seen {
		for oversized[want] {
			want++
		}
		if s != want {
			t.Fatalf("pages = %v, want every seq once except %v", seen, oversized)
		}
		want++
	}
}

// knobs are the sandbox mode and approval policy owners, in memory.
type knobs struct {
	mu     sync.Mutex
	mode   map[session.ID]sandbox.Mode
	policy map[session.ID]approval.Policy
}

func newKnobs() *knobs {
	return &knobs{mode: map[session.ID]sandbox.Mode{}, policy: map[session.ID]approval.Policy{}}
}

func (k *knobs) Mode(_ context.Context, s session.ID) (sandbox.Mode, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if m, ok := k.mode[s]; ok {
		return m, nil
	}
	return sandbox.WorkspaceWrite, nil
}

func (k *knobs) SetMode(_ context.Context, s session.ID, m sandbox.Mode) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.mode[s] = m
	return nil
}

func (k *knobs) Policy(s session.ID) approval.Policy {
	k.mu.Lock()
	defer k.mu.Unlock()
	if p, ok := k.policy[s]; ok {
		return p
	}
	return approval.PolicyAsk
}

func (k *knobs) SetPolicy(_ context.Context, ag agent.Ref, p approval.Policy, _ approval.By) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.policy[ag.Session] = p
	return nil
}

func (k *knobs) modeOf(t *testing.T, id string) sandbox.Mode {
	t.Helper()
	return must(k.Mode(context.Background(), session.ID(id)))
}

// permissionMode selects the session's permission preset: on create, from the
// request or else the connection's defaults; on resume, only when named.
func TestPermissionModeIsApplied(t *testing.T) {
	r := newRig(t)
	k := newKnobs()
	r.o.Permissions = must(permission.New(permission.DefaultConfig(), permission.Deps{Sessions: r.svc, Sandbox: k, Approval: k}))
	cl := connectWith(t, r.server(), &protocol.Defaults{PermissionMode: permission.PresetDangerFullAccess})
	model := &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}

	explicit := cl.createWith(protocol.SessionCreateParams{PermissionMode: permission.PresetReadOnly})
	if m := k.modeOf(t, explicit); m != sandbox.ReadOnly {
		t.Fatalf("explicit read-only created with mode %s", m)
	}
	if m := k.modeOf(t, cl.create()); m != sandbox.DangerFullAccess {
		t.Fatalf("the default preset created with mode %s", m)
	}
	cl.refused("session/create", protocol.SessionCreateParams{Model: model, PermissionMode: "root"}, sdkserver.ReasonUnknownPreset)
	if n := len(r.reg.List()); n != 2 {
		t.Fatalf("an unknown preset created a session: %d live", n)
	}

	reopen := func(mode string) {
		t.Helper()
		var closed protocol.LastSeqResult
		cl.call("session/close", protocol.SessionRef{SessionID: explicit}, &closed)
		var rr protocol.SessionResumeResult
		cl.call("session/resume", protocol.SessionResumeParams{SessionID: explicit, PermissionMode: mode}, &rr)
	}
	reopen(permission.PresetWorkspaceWrite)
	if m := k.modeOf(t, explicit); m != sandbox.WorkspaceWrite {
		t.Fatalf("resume to workspace-write left mode %s", m)
	}
	reopen("")
	if m := k.modeOf(t, explicit); m != sandbox.WorkspaceWrite {
		t.Fatalf("a resume naming no preset changed mode to %s", m)
	}
	var closed protocol.LastSeqResult
	cl.call("session/close", protocol.SessionRef{SessionID: explicit}, &closed)
	cl.refused("session/resume", protocol.SessionResumeParams{SessionID: explicit, PermissionMode: "root"}, sdkserver.ReasonUnknownPreset)
	cl.refused("session/prompt", protocol.SessionPromptParams{SessionID: explicit, Content: text("x")}, protocol.ReasonSessionNotOwned)

	r.o.Permissions = nil
	bare := connect(t, r.server())
	bare.refused("session/create", protocol.SessionCreateParams{Model: model, PermissionMode: permission.PresetReadOnly}, sdkserver.ReasonPermissionUnavailable)
}

// gated holds each permission Set until the test releases it, failing it with
// the error released.
type gated struct {
	sdkserver.Permissions
	entered chan struct{}
	release chan error
}

func (g *gated) Set(ctx context.Context, ag agent.Ref, name string, by approval.By) (permission.Preset, error) {
	g.entered <- struct{}{}
	if err := <-g.release; err != nil {
		return permission.Preset{}, err
	}
	return g.Permissions.Set(ctx, ag, name, by)
}

// No drive method reaches a created or resumed session until its requested
// preset is in force: a prompt during the switch is sdk/session-not-owned and
// reaches no model, and a failed switch leaves no session live.
func TestPresetAppliedBeforeAdmission(t *testing.T) {
	r := newRig(t, fakemodel.Turn{Events: []fakemodel.Event{fakemodel.Text("ok")}})
	k := newKnobs()
	g := &gated{
		Permissions: must(permission.New(permission.DefaultConfig(), permission.Deps{Sessions: r.svc, Sandbox: k, Approval: k})),
		entered:     make(chan struct{}), release: make(chan error),
	}
	r.o.Permissions = g
	cl := connect(t, r.server())
	model := &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}
	during := func(method string, params any, id string, setErr error) *jsonrpc.Response {
		t.Helper()
		rid := cl.id()
		cl.peer.Request(rid, method, params)
		select {
		case <-g.entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("%s never reached the permission switch", method)
		}
		cl.refused("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("early")}, protocol.ReasonSessionNotOwned)
		if n := len(r.tr.Requests()); n != 0 {
			t.Fatalf("a prompt during the switch reached the model: %d requests", n)
		}
		g.release <- setErr
		return cl.peer.ExpectResponse(rid)
	}

	failed := "ses_gated_failed"
	if resp := during("session/create", protocol.SessionCreateParams{SessionID: failed, Model: model, PermissionMode: permission.PresetReadOnly}, failed, errors.New("switch failed")); resp.Error == nil {
		t.Fatalf("a create whose switch failed succeeded: %s", resp.Result)
	}
	if _, live := r.reg.Get(session.ID(failed)); live {
		t.Fatal("a create whose switch failed left its session live")
	}
	cl.refused("session/prompt", protocol.SessionPromptParams{SessionID: failed, Content: text("late")}, protocol.ReasonSessionNotOwned)

	id := "ses_gated_ok"
	if resp := during("session/create", protocol.SessionCreateParams{SessionID: id, Model: model, PermissionMode: permission.PresetReadOnly}, id, nil); resp.Error != nil {
		t.Fatalf("create: %v", resp.Error)
	}
	if m := k.modeOf(t, id); m != sandbox.ReadOnly {
		t.Fatalf("created with mode %s, want read-only", m)
	}
	var closed protocol.LastSeqResult
	cl.call("session/close", protocol.SessionRef{SessionID: id}, &closed)
	if resp := during("session/resume", protocol.SessionResumeParams{SessionID: id, Model: model, PermissionMode: permission.PresetWorkspaceWrite}, id, nil); resp.Error != nil {
		t.Fatalf("resume: %v", resp.Error)
	}
	if m := k.modeOf(t, id); m != sandbox.WorkspaceWrite {
		t.Fatalf("resumed with mode %s, want workspace-write", m)
	}
	var rc protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("go")}, &rc)
	select {
	case <-r.tr.PrefixServed(1):
	case <-time.After(10 * time.Second):
		t.Fatal("the admitted session's prompt never reached the model")
	}
}

type failingWriter struct{ err error }

func (w failingWriter) Write([]byte) (int, error) { return 0, w.err }

// Serve returns nil when the peer's input ends or ctx ends, and the failure of a
// transport that fails.
func TestServeReportsTransportFailures(t *testing.T) {
	srv := newServer(t)
	ctx := context.Background()
	if err := srv.Serve(ctx, strings.NewReader(""), io.Discard, jsonrpc.Options{}); err != nil {
		t.Fatalf("EOF: %v, want nil", err)
	}

	ended, cancel := context.WithCancel(ctx)
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	time.AfterFunc(50*time.Millisecond, cancel)
	if err := srv.Serve(ended, pr, io.Discard, jsonrpc.Options{}); err != nil {
		t.Fatalf("ctx ended: %v, want nil", err)
	}

	boom := errors.New("boom")
	if err := srv.Serve(ctx, iotest.ErrReader(boom), io.Discard, jsonrpc.Options{}); !errors.Is(err, boom) {
		t.Fatalf("a failed read: %v, want boom", err)
	}

	pr, pw = io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })
	go func() { _, _ = io.WriteString(pw, `{"jsonrpc":"2.0","id":1,"method":"session/list","params":{}}`+"\n") }()
	if err := srv.Serve(ctx, pr, failingWriter{boom}, jsonrpc.Options{}); !errors.Is(err, boom) {
		t.Fatalf("a failed write: %v, want boom", err)
	}
}
