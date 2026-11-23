//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	qtool "github.com/smartpcr/agent-plugin/src/harness/internal/human/question/tool"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/answerer"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

// dibOuterT is the test running the suite; scenarios take temp homes from it.
var dibOuterT *testing.T

// dibTB is the testing.TB a scenario hands the test kit (the scripted
// answerer): its cleanups run when the scenario ends and its failures fail the
// scenario, not the whole suite.
type dibTB struct {
	testing.TB
	mu       sync.Mutex
	cleanups []func()
	errs     []string
}

type dibFatal struct{ msg string }

func (t *dibTB) Helper() {}

func (t *dibTB) Cleanup(f func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanups = append(t.cleanups, f)
}

func (t *dibTB) Errorf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.errs = append(t.errs, fmt.Sprintf(format, args...))
}

func (t *dibTB) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	panic(dibFatal{fmt.Sprintf(format, args...)})
}

func (t *dibTB) finish() error {
	t.mu.Lock()
	fs := t.cleanups
	t.cleanups = nil
	t.mu.Unlock()
	for i := len(fs) - 1; i >= 0; i-- {
		fs[i]()
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.errs) > 0 {
		return errors.New(strings.Join(t.errs, "; "))
	}
	return nil
}

type dibState struct {
	tb       *dibTB
	root     *cordis.Context
	cleanups []func()

	// The inbox scenarios.
	sid   string
	st    store.Store
	home  home.Layout
	sess  session.Service
	h     session.Handle
	inbox *agent.Inbox
	rec   agent.Receipt
	first agent.Receipt
	msgID string

	h2     session.Handle
	report session.OpenReport

	// The grant scenario.
	grants   *approval.Grants
	approver *approval.Service
	ans      *answerer.Answerer
	agents   agent.Registry
	firstRef agent.Ref
	a1, a2   agent.Handle
	dir      string
	resumed  agent.Ref

	// The question scenario.
	call, tool, args string
	hreg             human.Registry
	svc              tools.Service
	res              tools.Result
}

func (s *dibState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *dibState) dispose() error {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
	if s.tb != nil {
		return s.tb.finish()
	}
	return nil
}

func (s *dibState) newRoot() *cordis.Context {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.cleanup(root.Dispose)
	return root
}

func (s *dibState) types(h interface {
	Read(ctx context.Context, from event.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error)
}) ([]event.Event, error) {
	return h.Read(context.Background(), 0, 1000)
}

// --- Inbox item durable before Send returns (TC-10b) ---

// sqliteHome provisions the embedded SQLite store in a home: a fresh directory
// under DSH_E2E_STORE_HOME when the compose run mounts one, else a test-kit temp
// home (the gate path). Nothing skips.
func (s *dibState) sqliteHome(sid string) error {
	ctx := context.Background()
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-dib-")
		if err != nil {
			return err
		}
		s.cleanup(func() { _ = os.RemoveAll(dir) })
		base = dir
	} else if dibOuterT != nil {
		base = dibOuterT.TempDir()
	} else {
		return errors.New("no outer *testing.T; run through TestE2E_tool_pipeline_approvals_and_human_commands_durable_inbox_questions_commands_and_grants")
	}
	s.home = home.New(filepath.Join(base, "home"))
	if err := home.Ensure(s.home); err != nil {
		return err
	}
	st, err := sqlite.OpenHome(ctx, s.home)
	if err != nil {
		return fmt.Errorf("open the SQLite store: %w", err)
	}
	s.cleanup(func() { _ = st.Close(context.Background()) })
	s.st = st
	return s.openSession(sid)
}

func (s *dibState) openSession(sid string) error {
	ctx := context.Background()
	s.sid = sid
	s.root = s.newRoot()
	s.sess = session.NewService(s.root, s.st, session.Config{})
	h, err := s.sess.Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: sid}})
	if err != nil {
		return fmt.Errorf("create the session: %w", err)
	}
	s.h = h
	s.cleanup(func() { _ = h.Close(context.Background()) })
	in, err := agent.OpenInbox(ctx, h)
	if err != nil {
		return fmt.Errorf("open the inbox: %w", err)
	}
	s.inbox = in
	return nil
}

func dibUser(id, text string) agent.InboxMessage {
	return agent.InboxMessage{ID: id, Source: agent.SourceUser, By: "alice", Text: text}
}

func (s *dibState) send(id, text string) error {
	rec, err := s.inbox.Send(context.Background(), agent.ListNextTurn, dibUser(id, text))
	if err != nil {
		return fmt.Errorf("send %s: %w", id, err)
	}
	if rec.MessageID != id {
		return fmt.Errorf("receipt %+v for %s", rec, id)
	}
	s.rec, s.msgID = rec, id
	return nil
}

func (s *dibState) secondConnection(id string) error {
	ctx := context.Background()
	other, err := sqlite.OpenHome(ctx, s.home)
	if err != nil {
		return fmt.Errorf("open a second connection: %w", err)
	}
	defer other.Close(ctx)
	r, err := other.OpenRead(ctx, s.sid)
	if err != nil {
		return fmt.Errorf("second connection: open %s: %w", s.sid, err)
	}
	defer r.Close(ctx)
	recs, err := r.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		return fmt.Errorf("second connection: read: %w", err)
	}
	i := slices.IndexFunc(recs, func(x event.Record) bool { return x.Type == agent.SplicedType.Name() })
	if i < 0 {
		return fmt.Errorf("the second connection sees no %s among %d records", agent.SplicedType.Name(), len(recs))
	}
	if recs[i].Seq != s.rec.Seq {
		return fmt.Errorf("the splice is at seq %d, the receipt says %d", recs[i].Seq, s.rec.Seq)
	}
	var sp agent.Splice
	if err := json.Unmarshal([]byte(recs[i].Payload), &sp); err != nil {
		return fmt.Errorf("decode the splice: %w", err)
	}
	if sp.List != agent.ListNextTurn || len(sp.Inserted) != 1 || sp.Inserted[0].ID != id || sp.Inserted[0].Text != "hello" || sp.Inserted[0].Source != agent.SourceUser {
		return fmt.Errorf("the second connection read splice %+v, want %s inserted on next-turn", sp, id)
	}
	return nil
}

// --- One accepted message is at most one turn (DH-T-AGENT-6, TC-10a) ---

func (s *dibState) memSessionWith(sid, id string) error {
	s.st = memstore.New()
	if err := s.openSession(sid); err != nil {
		return err
	}
	return s.send(id, "do it")
}

func (s *dibState) driverCrashes(id string, turn int) error {
	ctx := context.Background()
	m, ok, err := s.inbox.ClaimTurn(ctx, turn)
	if err != nil || !ok || m.ID != id {
		return fmt.Errorf("the driver's claim = %+v %v %v, want %s", m, ok, err, id)
	}
	first, ok := s.inbox.Seen(id)
	if !ok {
		return fmt.Errorf("%s is not seen after its claim", id)
	}
	s.first = first
	// The crash: the driver stops before turn/end; only the session is closed.
	return s.h.Close(ctx)
}

func (s *dibState) resume() error {
	ctx := context.Background()
	h2, report, err := s.sess.Open(ctx, s.newRoot(), session.ID(s.sid), session.OpenOptions{})
	if err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	s.cleanup(func() { _ = h2.Close(context.Background()) })
	s.h2, s.report = h2, report
	in, err := agent.OpenInbox(ctx, h2)
	if err != nil {
		return fmt.Errorf("resume: fold the inbox: %w", err)
	}
	s.inbox = in
	return nil
}

func (s *dibState) interrupted() error {
	if !s.report.Interrupted {
		return fmt.Errorf("the resume did not close the open turn: %+v", s.report)
	}
	return nil
}

func (s *dibState) nothingPending(turn int) error {
	if p := s.inbox.Pending(agent.ListNextTurn); len(p) != 0 {
		return fmt.Errorf("pending next-turn after the resume: %+v", p)
	}
	if p := s.inbox.Pending(agent.ListNextStep); len(p) != 0 {
		return fmt.Errorf("pending next-step after the resume: %+v", p)
	}
	before := s.h2.Len()
	m, ok, err := s.inbox.ClaimTurn(context.Background(), turn)
	if err != nil || ok {
		return fmt.Errorf("the resumed agent claimed %+v (%v, %v)", m, ok, err)
	}
	if s.h2.Len() != before {
		return errors.New("a claim that took nothing appended to the log")
	}
	return nil
}

func (s *dibState) oneTurn(turns, msgs int, id string) error {
	evs, err := s.types(s.h2)
	if err != nil {
		return err
	}
	nt, nm := 0, 0
	for _, ev := range evs {
		switch ev.Type() {
		case event.TurnStartType.Name():
			nt++
		case event.UserMessage.Name():
			if strings.Contains(ev.Raw(), `"`+id+`"`) {
				nm++
			}
		}
	}
	if nt != turns || nm != msgs {
		return fmt.Errorf("the log has %d turn/start and %d user/message for %s, want %d and %d", nt, nm, id, turns, msgs)
	}
	return nil
}

// reportedDiscarded: the claim writes only the splice and the driver admits the
// message at point 8 (M1-D-LOOP-1 A), so a crash between the two leaves no
// user/message; the resumed inbox reports the claimed message discarded instead.
func (s *dibState) reportedDiscarded(id string, turn int) error {
	d := s.inbox.Discarded()
	if len(d) != 1 || d[0].Message.ID != id || d[0].Turn != turn || d[0].Cause != agent.CauseUnadmitted {
		return fmt.Errorf("discarded %+v, want %s claimed by turn %d with cause %q", d, id, turn, agent.CauseUnadmitted)
	}
	return nil
}

func (s *dibState) resendRefused(id string) error {
	_, err := s.inbox.Send(context.Background(), agent.ListNextTurn, dibUser(id, "do it"))
	if !errors.Is(err, agent.ErrDuplicateMessage) {
		return fmt.Errorf("the resend of %s = %v, want a duplicate", id, err)
	}
	var d *agent.DuplicateError
	if !errors.As(err, &d) || d.First != s.first {
		return fmt.Errorf("the duplicate's receipt %+v, want the first %+v", d, s.first)
	}
	return nil
}

// --- Grant does not survive resume (DH-D-HUMAN-3) ---

// dibLifeAgent is a stub agent whose session is the real one of its id: created
// the first time, resumed (session.Open) after.
type dibLifeAgent struct {
	ref agent.Ref
	h   session.Handle
	sc  *scope.Scope
}

func (a *dibLifeAgent) Ref() agent.Ref               { return a.ref }
func (a *dibLifeAgent) Session() session.ReadSession { return a.h }
func (a *dibLifeAgent) Scope() *cordis.Context       { return a.sc.Ctx() }
func (a *dibLifeAgent) ScopeKey() scope.Key          { return a.sc.Key() }
func (a *dibLifeAgent) Status() agent.Status         { return agent.StatusIdle }
func (a *dibLifeAgent) State() agent.State           { return agent.StateLive }
func (a *dibLifeAgent) Close(ctx context.Context) error {
	_ = a.sc.Close()
	return a.h.Close(ctx)
}

func dibReply(name string) (answerer.Reply, error) {
	switch name {
	case "allow-session":
		return answerer.AllowSession(), nil
	case "allow-once":
		return answerer.AllowOnce(), nil
	}
	return answerer.Reply{}, fmt.Errorf("unknown reply %q", name)
}

func (s *dibState) grantingAgent(sid, ch, r1, r2 string) error {
	ctx := context.Background()
	s.tb = &dibTB{TB: dibOuterT}
	s.sid = sid
	s.root = s.newRoot()
	root := s.root
	var replies []answerer.Reply
	for _, n := range []string{r1, r2} {
		r, err := dibReply(n)
		if err != nil {
			return err
		}
		replies = append(replies, r)
	}
	s.firstRef = agent.Ref{Session: session.ID(sid), Incarnation: 1}
	s.resumed = agent.Ref{Session: session.ID(sid), Incarnation: 2}
	reg := human.New()
	s.ans = answerer.Register(s.tb, root, reg, human.Channel(ch), replies...)
	for _, r := range []agent.Ref{s.firstRef, s.resumed} {
		if _, err := reg.Bind(root, r, human.Binding{Channel: human.Channel(ch)}); err != nil {
			return err
		}
	}
	s.grants = approval.NewGrants()
	s.approver = approval.New(approval.Config{Registry: reg, Grants: s.grants})
	s.sess = session.NewService(root, memstore.New(), session.Config{})
	if _, err := s.grants.Bind(root, s.sess); err != nil {
		return fmt.Errorf("bind the grant table to the agent lifecycle: %w", err)
	}
	tab := scope.NewTable(root)
	s.agents = agent.NewRegistry(root)
	opened := map[session.ID]bool{}
	_, err := s.agents.SetFactory(root, agent.FactoryFunc(func(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
		var h session.Handle
		var err error
		if opened[o.Session] {
			h, _, err = s.sess.Open(ctx, owner, o.Session, session.OpenOptions{})
		} else {
			h, err = s.sess.Create(ctx, owner, session.CreateOptions{Header: session.Header{ID: string(o.Session)}})
		}
		if err != nil {
			return nil, err
		}
		opened[o.Session] = true
		sc, err := tab.New(owner, ref.String())
		if err != nil {
			return nil, err
		}
		return &dibLifeAgent{ref: ref, h: h, sc: sc}, nil
	}))
	if err != nil {
		return err
	}
	a1, err := s.agents.Create(root, agent.CreateOptions{Session: session.ID(sid)})
	if err != nil {
		return fmt.Errorf("create the agent: %w", err)
	}
	if a1.Agent().Ref() != s.firstRef {
		return fmt.Errorf("the agent is %v, want %v", a1.Agent().Ref(), s.firstRef)
	}
	s.a1 = a1
	s.cleanup(func() { _ = a1.Dispose(context.Background()) })
	return nil
}

func dibDirReq(ref agent.Ref, call, path string) approval.Request {
	return approval.Request{Agent: ref, Tool: "write", Call: call, Origin: "model", Reason: "writes",
		Subject: &approval.Subject{Kind: approval.ScopeDirectory, Path: path}}
}

func (s *dibState) firstGrant(name, _ string) error {
	ctx := context.Background()
	s.dir = filepath.Join(dibOuterT.TempDir(), name)
	if err := os.MkdirAll(filepath.Join(s.dir, "sub"), 0o755); err != nil {
		return err
	}
	h, ok := s.sess.Get(session.ID(s.sid))
	if !ok {
		return errors.New("the agent's session is not live")
	}
	d, err := s.approver.Request(ctx, h, dibDirReq(s.firstRef, "c1", s.dir))
	if err != nil || !d.Approved() || !strings.HasPrefix(d.Detail, approval.DetailGranted+":") {
		return fmt.Errorf("the first call's decision %+v, %v; want approved with a grant", d, err)
	}
	list := s.grants.List(s.firstRef)
	if len(list) != 1 || list[0].Subject.Kind != approval.ScopeDirectory {
		return fmt.Errorf("grants %+v, want one directory grant", list)
	}
	// While the incarnation lives, the grant covers a call inside the directory.
	d, err = s.approver.Request(ctx, h, dibDirReq(s.firstRef, "c2", filepath.Join(s.dir, "sub", "f.txt")))
	if err != nil || !d.Approved() || d.Detail != approval.DetailAllowedByGrant+":"+list[0].ID || len(s.ans.Asked()) != 1 {
		return fmt.Errorf("the covered call %+v, %v, asked %d; want allowed by the grant without asking", d, err, len(s.ans.Asked()))
	}
	return nil
}

func (s *dibState) endAndResume() error {
	ctx := context.Background()
	if err := s.a1.Dispose(ctx); err != nil {
		return fmt.Errorf("end the session: %w", err)
	}
	if got := s.grants.List(s.firstRef); len(got) != 0 {
		return fmt.Errorf("grants of the ended agent %+v", got)
	}
	a2, err := s.agents.Create(s.root, agent.CreateOptions{Session: session.ID(s.sid)})
	if err != nil {
		return fmt.Errorf("resume: %w", err)
	}
	s.cleanup(func() { _ = a2.Dispose(context.Background()) })
	if a2.Agent().Ref() != s.resumed {
		return fmt.Errorf("the resumed agent is %v, want %v", a2.Agent().Ref(), s.resumed)
	}
	s.a2 = a2
	return nil
}

func (s *dibState) grantsEmpty() error {
	if got := s.grants.List(s.resumed); len(got) != 0 {
		return fmt.Errorf("the resumed agent's grants %+v, want none", got)
	}
	return nil
}

func (s *dibState) asksAgain(string) error {
	h, ok := s.sess.Get(session.ID(s.sid))
	if !ok {
		return errors.New("the resumed session is not live")
	}
	d, err := s.approver.Request(context.Background(), h, dibDirReq(s.resumed, "c3", s.dir))
	if err != nil || !d.Approved() || strings.HasPrefix(d.Detail, approval.DetailAllowedByGrant) {
		return fmt.Errorf("the resumed call %+v, %v; want approved by an answer, not a grant", d, err)
	}
	if n := len(s.ans.Asked()); n != 2 {
		return fmt.Errorf("the answerer was asked %d times, want 2", n)
	}
	return nil
}

func (s *dibState) grantLog(created, ended int, reason string) error {
	h, ok := s.sess.Get(session.ID(s.sid))
	if !ok {
		return errors.New("the resumed session is not live")
	}
	evs, err := s.types(h)
	if err != nil {
		return err
	}
	var c, e int
	for _, ev := range evs {
		switch ev.Type() {
		case approval.GrantCreatedType.Name():
			c++
		case approval.GrantEndedType.Name():
			g, err := approval.GrantEndedType.As(ev)
			if err != nil {
				return err
			}
			if g.Reason != reason {
				return fmt.Errorf("grant/ended reason %q, want %q", g.Reason, reason)
			}
			e++
		}
	}
	if c != created || e != ended {
		return fmt.Errorf("the log has %d grant/created and %d grant/ended, want %d and %d", c, e, created, ended)
	}
	return nil
}

// --- Question answered through the inbox (TC-10a) ---

func (s *dibState) modelCalled(sid, name, call, args string) error {
	ctx := context.Background()
	s.tb = &dibTB{TB: dibOuterT}
	s.sid, s.tool, s.call, s.args = sid, name, call, args
	s.root = s.newRoot()
	s.hreg = human.New()
	h, err := session.NewService(s.root, memstore.New(), session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: sid}})
	if err != nil {
		return err
	}
	s.h = h
	s.cleanup(func() { _ = h.Close(context.Background()) })
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		return err
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, vocab.ToolUseBlock(call, name, json.RawMessage(args)))
	if err != nil {
		return err
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err == nil {
			_, err = h.Append(ctx, d)
		}
		if err != nil {
			return err
		}
	}
	in, err := agent.OpenInbox(ctx, h)
	if err != nil {
		return err
	}
	s.inbox = in
	return nil
}

func (s *dibState) fakeAnswerer(ch, text string) error {
	ref := agent.Ref{Session: session.ID(s.sid), Incarnation: 1}
	s.ans = answerer.Register(s.tb, s.root, s.hreg, human.Channel(ch), answerer.Text(text))
	if _, err := s.hreg.Bind(s.root, ref, human.Binding{Channel: human.Channel(ch)}); err != nil {
		return err
	}
	qs := question.New(question.Config{Registry: s.hreg})
	s.cleanup(qs.Close)
	h, in := s.h, s.inbox
	resolve := func(agent.Ref) (question.Log, question.Inbox, error) { return h, in, nil }
	s.svc = core.NewWith(s.root, scope.NewTable(s.root), core.Config{})
	if _, err := s.svc.Register(s.root, qtool.Declare(qs, resolve)); err != nil {
		return fmt.Errorf("register %s: %w", qtool.Name, err)
	}
	return nil
}

func (s *dibState) callRuns() error {
	ctx := context.Background()
	ref := agent.Ref{Session: session.ID(s.sid), Incarnation: 1}
	v, err := s.svc.Resolve(ctx, tools.Target{Agent: ref})
	if err != nil {
		return err
	}
	defer v.Release()
	res, err := s.svc.ExecuteBatch(ctx, tools.Batch{View: v, Log: s.h, Calls: []tools.CallRequest{{ID: s.call, Name: s.tool, Arguments: s.args}}})
	if err != nil {
		return fmt.Errorf("execute the call: %w", err)
	}
	if len(res.Results) != 1 {
		return fmt.Errorf("%d results for one call", len(res.Results))
	}
	s.res = res.Results[0]
	return nil
}

func (s *dibState) callReturns(status, answer, by string) error {
	if s.res.IsError {
		return fmt.Errorf("the call failed: %s", s.res.Text)
	}
	var out qtool.Out
	if err := json.Unmarshal([]byte(s.res.Text), &out); err != nil {
		return fmt.Errorf("decode the result %q: %w", s.res.Text, err)
	}
	if out.Status != status || out.Answer != answer || out.By != by || out.ID == "" {
		return fmt.Errorf("the call returned %+v, want %s %q by %s", out, status, answer, by)
	}
	s.msgID = "ans-" + out.ID
	return nil
}

func (s *dibState) enteredInbox(source, by, call string) error {
	if _, ok := s.inbox.Seen(s.msgID); !ok {
		return fmt.Errorf("the inbox never admitted the answer %s", s.msgID)
	}
	if p := slices.Concat(s.inbox.Pending(agent.ListNextStep), s.inbox.Pending(agent.ListNextTurn)); len(p) != 0 {
		return fmt.Errorf("the answer handed to the call is still pending: %+v", p)
	}
	evs, err := s.types(s.h)
	if err != nil {
		return err
	}
	var in, out bool
	for _, ev := range evs {
		if ev.Type() != agent.SplicedType.Name() {
			continue
		}
		sp, err := agent.SplicedType.As(ev)
		if err != nil {
			return err
		}
		for _, m := range sp.Inserted {
			if m.ID == s.msgID {
				if m.Source != source || m.By != by || m.Call != call {
					return fmt.Errorf("the answer entered the inbox as %+v, want %s by %s for %s", m, source, by, call)
				}
				in = true
			}
		}
		if in && sp.Removed == 1 && sp.Outcome != nil && sp.Outcome.Kind == agent.OutcomeDiscarded && sp.Outcome.Cause == question.CauseReturnedToCall {
			out = true
		}
	}
	if !in || !out {
		return fmt.Errorf("splices of the answer: admitted %v, handed to the call %v", in, out)
	}
	return nil
}

func (s *dibState) questionLogged(call, text, by, ch string) error {
	evs, err := s.types(s.h)
	if err != nil {
		return err
	}
	var asked *question.Asked
	var answered *question.Answered
	for _, ev := range evs {
		switch ev.Type() {
		case question.AskedType.Name():
			a, err := question.AskedType.As(ev)
			if err != nil {
				return err
			}
			asked = &a
		case question.AnsweredType.Name():
			a, err := question.AnsweredType.As(ev)
			if err != nil {
				return err
			}
			answered = &a
		}
	}
	if asked == nil || asked.Call != call || !asked.Wait || asked.Agent == "" {
		return fmt.Errorf("question/asked %+v, want one waiting on call %s", asked, call)
	}
	if answered == nil || answered.ID != asked.ID || answered.Call != call || answered.Outcome != question.OutcomeAnswered ||
		answered.Text != text || answered.By.Principal != by || answered.By.Channel != ch || answered.Message != s.msgID || !answered.ToCall {
		return fmt.Errorf("question/answered %+v, want %q by %s on %s, handed to call %s as %s", answered, text, by, ch, call, s.msgID)
	}
	return nil
}

func InitializeScenario_tool_pipeline_approvals_and_human_commands_durable_inbox_questions_commands_and_grants(ctx *godog.ScenarioContext) {
	s := &dibState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = dibState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		return c, s.dispose()
	})

	ctx.Step(`^the SQLite store in a temp home with session "([^"]+)" and its inbox$`, s.sqliteHome)
	ctx.Step(`^Send returns for message "([^"]+)" with text "([^"]*)" on the next-turn list$`, s.send)
	ctx.Step(`^a second connection to the database immediately reads the inbox splice of "([^"]+)" at the receipt's seq$`, s.secondConnection)

	ctx.Step(`^session "([^"]+)" over the memory store with message "([^"]+)" in its inbox$`, s.memSessionWith)
	ctx.Step(`^a driver stub that takes "([^"]+)" as turn (\d+) and crashes before the turn ends$`, s.driverCrashes)
	ctx.Step(`^the agent resumes the session$`, s.resume)
	ctx.Step(`^the resume reports the open turn as interrupted$`, s.interrupted)
	ctx.Step(`^the resumed inbox has nothing pending and a claim of turn (\d+) takes nothing$`, s.nothingPending)
	ctx.Step(`^the log shows (\d+) turns? and (\d+) user messages? for "([^"]+)"$`, s.oneTurn)
	ctx.Step(`^the resumed inbox reports "([^"]+)" discarded as unadmitted by turn (\d+)$`, s.reportedDiscarded)
	ctx.Step(`^a resend of "([^"]+)" is refused as a duplicate with the first receipt$`, s.resendRefused)

	ctx.Step(`^an agent of session "([^"]+)" answered on channel "([^"]+)" with ([a-z-]+) then ([a-z-]+)$`, s.grantingAgent)
	ctx.Step(`^a call needing directory "([^"]+)" is approved and creates a session grant of "([^"]+)"$`, s.firstGrant)
	ctx.Step(`^the session is ended and resumed by a new incarnation$`, s.endAndResume)
	ctx.Step(`^the grant list of the resumed agent is empty$`, s.grantsEmpty)
	ctx.Step(`^a call needing directory "([^"]+)" asks again$`, s.asksAgain)
	ctx.Step(`^the session log holds (\d+) grant/created and (\d+) grant/ended with reason "([^"]+)"$`, s.grantLog)

	ctx.Step(`^the model of session "([^"]+)" called "([^"]+)" as "([^"]+)" with arguments '([^']*)'$`, s.modelCalled)
	ctx.Step(`^a fake answerer on channel "([^"]+)" that replies "([^"]+)"$`, s.fakeAnswerer)
	ctx.Step(`^the call runs$`, s.callRuns)
	ctx.Step(`^the waiting call returns status "([^"]+)" with answer "([^"]*)" by "([^"]+)"$`, s.callReturns)
	ctx.Step(`^the reply entered the inbox as a "([^"]+)" message by "([^"]+)" naming call "([^"]+)" and was handed to the call$`, s.enteredInbox)
	ctx.Step(`^the log holds question/asked for call "([^"]+)" and question/answered "([^"]+)" attributed to "([^"]+)" on channel "([^"]+)"$`, s.questionLogged)
}

func TestE2E_tool_pipeline_approvals_and_human_commands_durable_inbox_questions_commands_and_grants(t *testing.T) {
	dibOuterT = t
	t.Cleanup(func() { dibOuterT = nil })
	suite := godog.TestSuite{
		Name:                "tool_pipeline_approvals_and_human_commands_durable_inbox_questions_commands_and_grants",
		ScenarioInitializer: InitializeScenario_tool_pipeline_approvals_and_human_commands_durable_inbox_questions_commands_and_grants,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"tool_pipeline_approvals_and_human_commands_durable_inbox_questions_commands_and_grants.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
