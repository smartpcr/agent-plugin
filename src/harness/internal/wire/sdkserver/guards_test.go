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

// Every history page's result fits half a frame, measured as serialized; an
// event over half a frame is a page alone, and one no frame carries is refused
// jsonrpc/line-too-long and paged past.
func TestHistoryPagesFitHalfAFrame(t *testing.T) {
	const frame = 16 << 10
	r := newRig(t, hang("working"))
	r.o.Limits.MaxFrameBytes = frame
	cl := connect(t, r.server())
	id := cl.create()
	var big, huge int64
	prompts := []string{strings.Repeat("<a&", 500), strings.Repeat("<b&", 500), strings.Repeat("<c&", 500), strings.Repeat("<d&", 500),
		strings.Repeat("b", 10_000), strings.Repeat("c", 20_000), "tail"}
	for i, s := range prompts {
		var rc protocol.SessionPromptResult
		cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text(s), MessageID: fmt.Sprint("m", i)}, &rc)
		switch i {
		case 4:
			big = rc.Seq
		case 5:
			huge = rc.Seq
		}
		if i == 0 {
			running(t, r, id)
		}
	}

	var seen []int64
	var cut, alone, skipped bool
	params := map[string]any{"sessionId": id}
	for {
		resp := cl.respond("session/history", params)
		next := int64(len(seen))
		if skipped {
			next++
		}
		if resp.Error != nil {
			if resp.Error.Reason != jsonrpc.ReasonLineTooLong || next != huge || skipped {
				t.Fatalf("history after %v: %v, want line-too-long only at %d", params["afterSeq"], resp.Error, huge)
			}
			skipped, params["afterSeq"] = true, huge
			continue
		}
		var page protocol.SessionHistoryResult
		if err := json.Unmarshal(resp.Result, &page); err != nil {
			t.Fatal(err)
		}
		if len(resp.Result) > frame/2 {
			if len(page.Events) != 1 || page.Events[0].Seq != big {
				t.Fatalf("a %d-byte page of %d events, over half the frame", len(resp.Result), len(page.Events))
			}
			alone = true
		}
		if len(resp.Result) > frame-1024 {
			t.Fatalf("a %d-byte page leaves no room for its envelope in a %d-byte frame", len(resp.Result), frame)
		}
		for _, e := range page.Events {
			seen = append(seen, e.Seq)
		}
		if !page.More {
			break
		}
		cut = cut || len(page.Events) > 1
		params["afterSeq"] = page.Events[len(page.Events)-1].Seq
	}
	if !cut || !alone || !skipped {
		t.Fatalf("cut %v, alone %v, skipped %v: want each", cut, alone, skipped)
	}
	want := int64(0)
	for _, s := range seen {
		if want == huge {
			want++
		}
		if s != want {
			t.Fatalf("pages = %v, want every seq once except %d", seen, huge)
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
