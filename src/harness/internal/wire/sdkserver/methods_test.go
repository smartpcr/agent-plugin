package sdkserver_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/permission"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/sdkserver"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc/jsonrpctest"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// connectConn is connect, also returning the server's end of the connection.
func connectConn(t *testing.T, srv *sdkserver.Server) (*sdkserver.Conn, *client, *jsonrpctest.Peer) {
	t.Helper()
	c := must(srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	_, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: c.Methods()})
	cl := &client{t: t, peer: peer}
	var init protocol.InitializeResult
	cl.call("initialize", protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test", Version: "0"}}, &init)
	return c, cl, peer
}

// fakeWorkdir serves each session the directory /work/<id>, moves it to any
// path but the ones named in fails, which fail with their error, and fails
// Ensure for the sessions in broken.
type fakeWorkdir struct {
	mu     sync.Mutex
	moved  map[session.ID]string
	broken map[session.ID]bool
	fails  map[string]error
}

func newFakeWorkdir() *fakeWorkdir {
	return &fakeWorkdir{moved: map[session.ID]string{}, broken: map[session.ID]bool{}, fails: map[string]error{}}
}

func (w *fakeWorkdir) Ensure(_ context.Context, id session.ID) (workdir.WorkingDirectory, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.broken[id] {
		return workdir.WorkingDirectory{}, &store.Error{Code: store.CodeClosed, Msg: "the workdir fold is gone"}
	}
	if p, ok := w.moved[id]; ok {
		return workdir.WorkingDirectory{Path: p}, nil
	}
	return workdir.WorkingDirectory{Path: "/work/" + string(id)}, nil
}

func (w *fakeWorkdir) Change(_ context.Context, id session.ID, path string) (workdir.WorkingDirectory, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err, ok := w.fails[path]; ok {
		return workdir.WorkingDirectory{}, err
	}
	w.moved[id] = path
	return workdir.WorkingDirectory{Path: path}, nil
}

func (w *fakeWorkdir) breakEnsure(id session.ID) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.broken[id] = true
}

// The working-directory methods read and move an owned session's directory
// through the service; each owner error keeps its reason on the wire, with the
// reason's prefix cut from the message.
func TestWorkingDirectoryMethods(t *testing.T) {
	r := newRig(t)
	wd := newFakeWorkdir()
	wd.fails["/owned"] = fmt.Errorf("move: %w", &store.OwnedError{ID: "x", Holder: store.Holder{Instance: "other", Since: time.Unix(0, 0)}})
	wd.fails["/closed"] = &store.Error{Code: store.CodeClosed, Msg: "the log is closed"}
	wd.fails["/agent"] = &agent.Error{Code: agent.CodeInvalid, Msg: "bad path"}
	wd.fails["/preset"] = fmt.Errorf("move: %w", permission.ErrUnknownPreset)
	wd.fails["/plain"] = errors.New("plain failure")
	r.o.Workdir = wd
	cl := connect(t, r.server())

	var created protocol.SessionCreateResult
	cl.call("session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}}, &created)
	id := created.SessionID
	if created.Cwd != "/work/"+id {
		t.Fatalf("create cwd = %q, want the service's /work/%s", created.Cwd, id)
	}
	var got protocol.WorkingDirectoryResult
	cl.call("session/working-directory/get", protocol.SessionRef{SessionID: id}, &got)
	if got.Cwd != "/work/"+id {
		t.Fatalf("get = %q, want /work/%s", got.Cwd, id)
	}
	cl.call("session/working-directory/set", protocol.WorkingDirectorySetParams{SessionID: id, Path: "/elsewhere"}, &got)
	if got.Cwd != "/elsewhere" {
		t.Fatalf("set = %q, want /elsewhere", got.Cwd)
	}
	cl.call("session/working-directory/get", protocol.SessionRef{SessionID: id}, &got)
	if got.Cwd != "/elsewhere" {
		t.Fatalf("get after set = %q, want /elsewhere", got.Cwd)
	}

	for path, reason := range map[string]string{
		"/owned":  string(store.CodeOwned),
		"/closed": string(store.CodeClosed),
		"/agent":  string(agent.CodeInvalid),
		"/preset": sdkserver.ReasonUnknownPreset,
	} {
		resp := cl.respond("session/working-directory/set", protocol.WorkingDirectorySetParams{SessionID: id, Path: path})
		if resp.Error == nil || resp.Error.Reason != reason {
			t.Fatalf("set %s = %+v, want %s", path, resp, reason)
		}
		if strings.HasPrefix(resp.Error.Message, reason+": ") {
			t.Fatalf("set %s: message %q keeps its reason prefix", path, resp.Error.Message)
		}
	}
	if resp := cl.respond("session/working-directory/set", protocol.WorkingDirectorySetParams{SessionID: id, Path: "/plain"}); resp.Error == nil {
		t.Fatalf("set /plain = %s, want an error", resp.Result)
	}

	cl.refused("session/working-directory/get", protocol.SessionRef{SessionID: "ses_nobody"}, protocol.ReasonSessionNotOwned)
	cl.refused("session/working-directory/set", protocol.WorkingDirectorySetParams{SessionID: "ses_nobody", Path: "/x"}, protocol.ReasonSessionNotOwned)

	// A directory the service cannot report fails the create, which closes
	// the session it made: nothing is left live or owned.
	broken := "ses_broken_workdir"
	wd.breakEnsure(session.ID(broken))
	cl.refused("session/create", protocol.SessionCreateParams{SessionID: broken, Model: &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}}, string(store.CodeClosed))
	if _, live := r.reg.Get(session.ID(broken)); live {
		t.Fatal("a create whose directory failed left its session live")
	}
	cl.refused("session/prompt", protocol.SessionPromptParams{SessionID: broken, Content: text("x")}, protocol.ReasonSessionNotOwned)

	// So does a resume, of a session closed and then broken.
	var closed protocol.LastSeqResult
	cl.call("session/close", protocol.SessionRef{SessionID: id}, &closed)
	wd.breakEnsure(session.ID(id))
	cl.refused("session/resume", protocol.SessionResumeParams{SessionID: id}, string(store.CodeClosed))
	if _, live := r.reg.Get(session.ID(id)); live {
		t.Fatal("a resume whose directory failed left its session live")
	}
	cl.refused("session/working-directory/get", protocol.SessionRef{SessionID: id}, protocol.ReasonSessionNotOwned)
}

// Without a working-directory service get answers the session header's cwd
// and set is workdir/unavailable.
func TestWorkingDirectoryUnavailable(t *testing.T) {
	cl := connect(t, newServer(t))
	id := cl.createWith(protocol.SessionCreateParams{Cwd: "/from/header"})
	var got protocol.WorkingDirectoryResult
	cl.call("session/working-directory/get", protocol.SessionRef{SessionID: id}, &got)
	if got.Cwd != "/from/header" {
		t.Fatalf("get = %q, want the header's /from/header", got.Cwd)
	}
	cl.refused("session/working-directory/set", protocol.WorkingDirectorySetParams{SessionID: id, Path: "/x"}, string(workdir.CodeUnavailable))
}

// session/model/set selects a known route on an owned session and refuses a
// blank or unknown route, or a session the connection does not own.
func TestModelSet(t *testing.T) {
	cl := connect(t, newServer(t))
	id := cl.create()
	want := protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName, ReasoningEffort: "high", MaxOutputTokens: 64}
	var got protocol.ModelSetResult
	cl.call("session/model/set", protocol.ModelSetParams{SessionID: id, Model: want}, &got)
	if got.Model != want {
		t.Fatalf("model/set = %+v, want %+v", got.Model, want)
	}
	cl.refused("session/model/set", protocol.ModelSetParams{SessionID: id, Model: protocol.ModelRoute{Provider: " padded"}}, jsonrpc.ReasonInvalidParams)
	cl.refused("session/model/set", protocol.ModelSetParams{SessionID: id, Model: protocol.ModelRoute{Provider: "r/none"}}, string(vocab.CodeNoRoute))
	cl.refused("session/model/set", protocol.ModelSetParams{SessionID: "ses_nobody", Model: want}, protocol.ReasonSessionNotOwned)
}

// session/list pages the stored root sessions through the query service,
// leaving subagents out; with no query service it lists none.
func TestSessionList(t *testing.T) {
	r := newRig(t, fakemodel.Reply("listed"))
	cl := connect(t, r.server())
	var empty protocol.SessionListResult
	cl.call("session/list", protocol.SessionListParams{}, &empty)
	if empty.Sessions == nil || len(empty.Sessions) != 0 || empty.NextCursor != "" {
		t.Fatalf("list without a query service = %+v, want an empty page", empty)
	}

	r.o.Query = query.New(r.st)
	cl = connect(t, r.server())
	id := cl.create()
	var pr protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("list me")}, &pr)
	var w protocol.LastSeqResult
	cl.call("session/wait", protocol.SessionRef{SessionID: id}, &w)
	kid := subagent(t, r, id)
	appendRecords(t, kid, 1)

	var page protocol.SessionListResult
	cl.call("session/list", protocol.SessionListParams{Limit: 50}, &page)
	var found bool
	for _, s := range page.Sessions {
		if s.SessionID == string(kid.ID()) {
			t.Fatalf("list = %+v, want the subagent %s left out", page.Sessions, kid.ID())
		}
		if s.SessionID == id {
			found = true
			if _, err := time.Parse(time.RFC3339Nano, s.UpdatedAt); err != nil {
				t.Fatalf("updatedAt %q: %v", s.UpdatedAt, err)
			}
		}
	}
	if !found {
		t.Fatalf("list = %+v, want %s", page.Sessions, id)
	}
	if resp := cl.respond("session/list", protocol.SessionListParams{Cursor: "not a cursor"}); resp.Error == nil {
		t.Fatalf("list with a bad cursor = %s, want an error", resp.Result)
	}
}

// shutdown answers empty; session/query queues its content as a prompt; a
// steer is accepted on a running turn.
func TestShutdownQueryAndSteer(t *testing.T) {
	r := newRig(t, hang("working"))
	cl := connect(t, r.server())
	id := cl.create()
	var q protocol.SessionQueryResult
	cl.call("session/query", protocol.SessionQueryParams{SessionID: id, Content: text("what now")}, &q)
	if q.Kind != "prompt" || q.MessageID == "" || q.Seq < 0 {
		t.Fatalf("query = %+v, want a queued prompt", q)
	}
	running(t, r, id)
	var steer protocol.SessionPromptResult
	cl.call("session/prompt", protocol.SessionPromptParams{SessionID: id, Content: text("left"), Delivery: "steer", MessageID: "s1"}, &steer)
	if steer.MessageID != "s1" {
		t.Fatalf("steer = %+v", steer)
	}
	var cancelled protocol.SessionCancelResult
	cl.call("session/cancel", protocol.SessionCancelParams{SessionID: id, Scope: "step", Inbox: "discard"}, &cancelled)
	cl.refused("session/cancel", protocol.SessionCancelParams{SessionID: "ses_nobody", Inbox: "keep"}, protocol.ReasonSessionNotOwned)
	var out protocol.Empty
	cl.call("shutdown", protocol.Empty{}, &out)
}

// failingPermissions knows every preset and fails every switch.
type failingPermissions struct{}

func (failingPermissions) Lookup(string) (permission.Preset, bool) { return permission.Preset{}, true }

func (failingPermissions) Set(context.Context, agent.Ref, string, approval.By) (permission.Preset, error) {
	return permission.Preset{}, errors.New("the switch failed")
}

// session/resume refuses an unknown session, a subagent's, one live in the
// host under another owner and a bad model; on an owned session it selects a
// named model, and a preset that fails to apply closes the session.
func TestResumeRefusals(t *testing.T) {
	r := newRig(t)
	srv := r.server()
	cl := connect(t, srv)
	model := &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}

	cl.refused("session/resume", protocol.SessionResumeParams{SessionID: "ses_unknown"}, string(store.CodeNotFound))
	kid := subagent(t, r, "ses_someone")
	cl.refused("session/resume", protocol.SessionResumeParams{SessionID: string(kid.ID())}, protocol.ReasonSubagentSession)
	loner := must(r.reg.Create(r.o.Owner, agent.CreateOptions{Session: "ses_loner", Model: agent.ModelSelection{Route: route, Model: fakemodel.ModelName}}))
	t.Cleanup(func() { _ = loner.Dispose(context.Background()) })
	cl.refused("session/resume", protocol.SessionResumeParams{SessionID: "ses_loner"}, string(store.CodeOwned))
	cl.refused("session/resume", protocol.SessionResumeParams{SessionID: "ses_loner", Model: &protocol.ModelRoute{Provider: " padded"}}, jsonrpc.ReasonInvalidParams)

	id := cl.create()
	var again protocol.SessionResumeResult
	cl.call("session/resume", protocol.SessionResumeParams{SessionID: id, Model: model}, &again)
	if again.SessionID != id {
		t.Fatalf("idempotent resume with a model = %+v", again)
	}

	r.o.Permissions = failingPermissions{}
	pcl := connect(t, r.server())
	pid := pcl.create()
	if resp := pcl.respond("session/resume", protocol.SessionResumeParams{SessionID: pid, PermissionMode: "any"}); resp.Error == nil {
		t.Fatalf("an owned resume whose preset failed = %s, want an error", resp.Result)
	}
	if _, live := r.reg.Get(session.ID(pid)); live {
		t.Fatal("a resume whose preset failed left its session live")
	}
	pcl.refused("session/prompt", protocol.SessionPromptParams{SessionID: pid, Content: text("x")}, protocol.ReasonSessionNotOwned)
	// A stored session whose preset fails to apply on resume is not adopted.
	if resp := pcl.respond("session/resume", protocol.SessionResumeParams{SessionID: pid, PermissionMode: "any"}); resp.Error == nil {
		t.Fatalf("a stored resume whose preset failed = %s, want an error", resp.Result)
	}
	if _, live := r.reg.Get(session.ID(pid)); live {
		t.Fatal("a stored resume whose preset failed left its session live")
	}
}

// session/create refuses a blank route, an id another connection owns and an
// id already stored.
func TestCreateRefusals(t *testing.T) {
	srv := newServer(t)
	first, second := connect(t, srv), connect(t, srv)
	first.refused("session/create", protocol.SessionCreateParams{Model: &protocol.ModelRoute{Provider: " padded"}}, jsonrpc.ReasonInvalidParams)
	id := first.createWith(protocol.SessionCreateParams{SessionID: "ses_taken"})
	second.refused("session/create", protocol.SessionCreateParams{SessionID: id, Model: &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}}, string(store.CodeOwned))
	var closed protocol.LastSeqResult
	first.call("session/close", protocol.SessionRef{SessionID: id}, &closed)
	if resp := second.respond("session/create", protocol.SessionCreateParams{SessionID: id, Model: &protocol.ModelRoute{Provider: string(route), Model: fakemodel.ModelName}}); resp.Error == nil {
		t.Fatalf("a create over a stored session = %s, want an error", resp.Result)
	}
}

// session/history clamps an over-large limit, refuses a cursor below -1, an
// unknown session and undecodable params, and reads a session that is live
// in the session service but no agent.
func TestHistoryRefusals(t *testing.T) {
	r := newRig(t)
	cl := connect(t, r.server())
	id := cl.create()
	var page protocol.SessionHistoryResult
	cl.call("session/history", protocol.SessionHistoryParams{SessionID: id, Limit: sdkserver.MaxHistoryLimit + 1}, &page)
	if int64(len(page.Events)) != page.LastSeq+1 || page.More {
		t.Fatalf("history with an over-large limit = %+v, want every event", page)
	}
	cl.refused("session/history", map[string]any{"sessionId": id, "afterSeq": -2}, jsonrpc.ReasonInvalidParams)
	cl.refused("session/history", map[string]any{"sessionId": 5}, jsonrpc.ReasonInvalidParams)
	cl.refused("session/history", protocol.SessionHistoryParams{SessionID: "ses_unknown"}, string(store.CodeNotFound))

	kid := subagent(t, r, id)
	appendRecords(t, kid, 2)
	cl.call("session/history", protocol.SessionHistoryParams{SessionID: string(kid.ID())}, &page)
	if int(page.LastSeq) != int(kid.Len())-1 || len(page.Events) != int(kid.Len()) {
		t.Fatalf("history of a live non-agent session = %+v, want its %d events", page, kid.Len())
	}
}

// initialize refuses a version it does not speak, and waits for Ready: it
// answers once Ready closes, and fails when cancelled first.
func TestInitializeWaitsForReady(t *testing.T) {
	r := newRig(t)
	ready := make(chan struct{})
	r.o.Ready = ready
	srv := r.server()
	init := protocol.InitializeParams{ProtocolVersions: []int{protocol.Version}, ClientInfo: protocol.Implementation{Name: "test"}}

	c := must(srv.Connect())
	t.Cleanup(func() { _ = c.Close(context.Background()) })
	_, peer := jsonrpctest.NewPeer(t, jsonrpc.Options{Methods: c.Methods()})
	cl := &client{t: t, peer: peer}
	if resp := cl.respond("initialize", protocol.InitializeParams{ProtocolVersions: []int{protocol.Version + 100}}); resp.Error == nil {
		t.Fatalf("initialize with no common version = %s, want an error", resp.Result)
	}

	cancelled := cl.id()
	peer.Request(cancelled, "initialize", init)
	peer.Cancel(cancelled)
	if resp := peer.ExpectResponse(cancelled); resp.Error == nil {
		t.Fatalf("a cancelled initialize = %s, want an error", resp.Result)
	}

	waiting := cl.id()
	peer.Request(waiting, "initialize", init)
	close(ready)
	if resp := peer.ExpectResponse(waiting); resp.Error != nil {
		t.Fatalf("initialize after Ready: %v", resp.Error)
	}
}

// New refuses options without the required owners; a server whose owner is
// disposed opens no connection, and Serve reports it.
func TestServerWithoutOwners(t *testing.T) {
	if _, err := sdkserver.New(sdkserver.Options{}); err == nil {
		t.Fatal("New with no owners succeeded")
	}
	r := newRig(t)
	gone := cordis.New()
	gone.Loggers().ClearExporters()
	gone.Dispose()
	o := r.o
	o.Owner = gone
	srv := must(sdkserver.New(o))
	if _, err := srv.Connect(); err == nil {
		t.Fatal("Connect under a disposed owner succeeded")
	}
	if err := srv.Serve(context.Background(), strings.NewReader(""), nil, jsonrpc.Options{}); err == nil {
		t.Fatal("Serve under a disposed owner succeeded")
	}
}

// Tree is nil for a session the connection does not own, and holds a live
// subagent of an owned root after the root.
func TestTreeHoldsLiveSubagents(t *testing.T) {
	r := newRig(t)
	c, cl, _ := connectConn(t, r.server())
	if tree := c.Tree("ses_nobody"); tree != nil {
		t.Fatalf("Tree of an unowned session = %v, want nil", tree)
	}
	root := cl.create()
	parent, _ := r.reg.Get(session.ID(root))
	kid := must(r.reg.Create(r.o.Owner, agent.CreateOptions{Session: "ses_tree_kid", Parent: parent.Ref(),
		Model: agent.ModelSelection{Route: route, Model: fakemodel.ModelName}}))
	t.Cleanup(func() { _ = kid.Dispose(context.Background()) })
	tree := c.Tree(session.ID(root))
	if len(tree) != 2 || tree[0].Ref().Session != session.ID(root) || tree[1].Ref().Session != "ses_tree_kid" {
		t.Fatalf("Tree = %v, want the root then its subagent", tree)
	}
}

// Serve on a closed connection fails at once.
func TestServeClosedConnection(t *testing.T) {
	c := must(newServer(t).Connect())
	if err := c.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := c.Serve(context.Background(), strings.NewReader(""), nil, jsonrpc.Options{}); err == nil {
		t.Fatal("Serve on a closed connection succeeded")
	}
}
