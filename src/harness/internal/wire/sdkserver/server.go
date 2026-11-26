// Package sdkserver is the SDK carrier, component wire.sdk (DH-D-SDK-4, C49): it
// serves the client requests of src/sdk/protocol version 1 over one jsonrpc
// connection per client. A connection owns the sessions it created or resumed;
// it drives only those, and reads any stored session's history.
//
// This file is the session methods. The scoped event forwarder, run results and
// the approval and question answerer are later stages (DH-D-SDK-5, -6).
package sdkserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// The history page bounds (sdk-acp-typert.md §6.2, session/history).
const (
	DefaultHistoryLimit = 500
	MaxHistoryLimit     = 5000
)

// Workdir is the working-directory service a connection reads and moves a
// session's directory through; *workdir.Service is one.
type Workdir interface {
	Ensure(ctx context.Context, id session.ID) (workdir.WorkingDirectory, error)
	Change(ctx context.Context, id session.ID, path string) (workdir.WorkingDirectory, error)
}

// Routes lists the model routes a selection may name; *llm.Runtime is one.
type Routes interface {
	Routes() []vocab.RouteID
}

// Options are what a Server serves with.
type Options struct {
	// Owner is the context every connection's fiber is applied under; the agents
	// a connection creates are owned by its fiber. Required.
	Owner *cordis.Context
	// Agents creates and resumes the agents; required.
	Agents agent.Registry
	// Sessions reads stored sessions for history and resume; required.
	Sessions session.Service
	// Query lists stored sessions for session/list; nil lists none.
	Query *query.Service
	// Workdir serves the working-directory methods; nil answers get from the
	// session header and refuses set with workdir/unavailable.
	Workdir Workdir
	// Routes validates a requested route by exact id; nil accepts any.
	Routes Routes
	// Ready is closed when boot reports the tree ready; initialize waits for it.
	// nil is ready.
	Ready <-chan struct{}
	// Info names the host in initialize's serverInfo.
	Info protocol.Implementation
	// Limits are the frame bounds initialize reports.
	Limits protocol.Limits
}

// Server serves the SDK protocol to any number of connections.
type Server struct {
	o Options

	mu sync.Mutex
	// held is which connection owns each session this server drives.
	held  map[session.ID]*Conn
	conns int
}

// New is a Server with o.
func New(o Options) (*Server, error) {
	if o.Owner == nil || o.Agents == nil || o.Sessions == nil {
		return nil, errors.New("sdkserver: Owner, Agents and Sessions are required")
	}
	return &Server{o: o, held: map[session.ID]*Conn{}}, nil
}

// Conn is one client connection's state: the gate, the defaults initialize
// gave, and the table of the sessions it owns.
type Conn struct {
	s       *Server
	fiber   *cordis.Fiber
	owner   *cordis.Context
	methods map[string]jsonrpc.Handler

	mu       sync.Mutex
	client   protocol.Implementation
	defaults protocol.Defaults
	owned    map[session.ID]agent.Handle
	closed   bool
}

// Connect opens a connection: its fiber under Options.Owner and its method table,
// gated by initialize. Close it when the transport ends.
func (s *Server) Connect() (*Conn, error) {
	s.mu.Lock()
	s.conns++
	name := "sdk.conn." + strconv.Itoa(s.conns)
	s.mu.Unlock()
	c := &Conn{s: s, owned: map[session.ID]agent.Handle{}}
	f, err := s.o.Owner.Apply(name, func(ctx *cordis.Context) error { c.owner = ctx; return nil })
	if err != nil {
		return nil, fmt.Errorf("sdkserver: open the connection fiber: %w", err)
	}
	if err := f.Wait(); err != nil {
		f.Dispose()
		return nil, fmt.Errorf("sdkserver: open the connection fiber: %w", err)
	}
	c.fiber = f
	m, err := protocol.Register(protocol.Requests(protocol.FromClient),
		protocol.Handle(protocol.InitializeMethod, c.initialize),
		protocol.Handle("session/create", c.create),
		protocol.Handle("session/resume", c.resume),
		protocol.Handle("session/list", c.list),
		protocol.Handle("session/prompt", c.prompt),
		protocol.Handle("session/query", c.query),
		protocol.Handle("session/wait", c.wait),
		protocol.Handle("session/cancel", c.cancel),
		protocol.Handle("session/close", c.closeSession),
		protocol.Handle("session/history", c.history),
		protocol.Handle("session/model/set", c.setModel),
		protocol.Handle("session/working-directory/get", c.getWorkdir),
		protocol.Handle("session/working-directory/set", c.setWorkdir),
		protocol.Handle("shutdown", c.shutdown),
	)
	if err != nil {
		f.Dispose()
		return nil, err
	}
	c.methods = protocol.NewGate(m).Methods()
	return c, nil
}

// Serve connects and serves one client over r and w until the transport ends,
// then closes the connection. o.Methods is replaced by the connection's.
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer, o jsonrpc.Options) error {
	c, err := s.Connect()
	if err != nil {
		return err
	}
	o.Methods = c.Methods()
	conn := jsonrpc.Serve(ctx, r, w, o)
	<-conn.Done()
	return c.Close(context.WithoutCancel(ctx))
}

// Methods is the connection's gated method table, for jsonrpc.Options.Methods.
func (c *Conn) Methods() map[string]jsonrpc.Handler { return c.methods }

// Close disposes every agent the connection owns (the sessions stay stored and
// resumable) and its fiber. It is idempotent.
func (c *Conn) Close(ctx context.Context) error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	owned := c.owned
	c.owned = map[session.ID]agent.Handle{}
	c.mu.Unlock()
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(owned)) {
		errs = append(errs, owned[id].Dispose(ctx))
		c.s.release(id, c)
	}
	c.fiber.Dispose()
	return errors.Join(errs...)
}

// Owned is the ids of the sessions the connection owns, sorted.
func (c *Conn) Owned() []session.ID {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(maps.Keys(c.owned))
}

// Tree is root and the live agents descended from it (subagents whose header
// names a creator in the tree), root first: what the connection drives and, from
// DH-D-SDK-5, what it is told about.
func (c *Conn) Tree(root session.ID) []agent.Agent {
	a, ok := c.agent(root)
	if !ok {
		return nil
	}
	tree := []agent.Agent{a}
	in := map[string]bool{string(root): true}
	all := c.s.o.Agents.List()
	for grew := true; grew; {
		grew = false
		for _, x := range all {
			h := x.Session().Header()
			if h.Origin == "subagent" && in[h.Creator] && !in[h.ID] {
				in[h.ID], grew = true, true
				tree = append(tree, x)
			}
		}
	}
	return tree
}

// agent is the live agent of a session this connection owns.
func (c *Conn) agent(id session.ID) (agent.Agent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	h, ok := c.owned[id]
	if !ok {
		return nil, false
	}
	return h.Agent(), true
}

// driven is the agent of a session this connection owns, or sdk/session-not-owned.
func (c *Conn) driven(id string) (agent.Agent, error) {
	if a, ok := c.agent(session.ID(id)); ok {
		return a, nil
	}
	return nil, fail(protocol.ReasonSessionNotOwned, fmt.Sprintf("session %q is not owned by this connection; create or resume it first", id))
}

// claim records that c owns id; it fails with session/owned when another
// connection does.
func (s *Server) claim(id session.ID, c *Conn) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o, ok := s.held[id]; ok && o != c {
		return fail(string(store.CodeOwned), fmt.Sprintf("session %q is owned by another connection of this host", id))
	}
	s.held[id] = c
	return nil
}

// release drops c's claim on id.
func (s *Server) release(id session.ID, c *Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.held[id] == c {
		delete(s.held, id)
	}
}

// ------------------------------------------------------------------ lifecycle

func (c *Conn) initialize(ctx context.Context, p protocol.InitializeParams) (protocol.InitializeResult, error) {
	v, err := protocol.Negotiate(p.ProtocolVersions, []int{protocol.Version})
	if err != nil {
		return protocol.InitializeResult{}, err
	}
	if r := c.s.o.Ready; r != nil {
		select {
		case <-r:
		case <-ctx.Done():
			return protocol.InitializeResult{}, ctx.Err()
		}
	}
	c.mu.Lock()
	c.client = p.ClientInfo
	if p.Defaults != nil {
		c.defaults = *p.Defaults
	}
	c.mu.Unlock()
	// Images, commands and client tools are held open (H-3) or later stages:
	// the host offers none of them yet.
	return protocol.InitializeResult{
		ProtocolVersion: v,
		ServerInfo:      c.s.o.Info,
		Capabilities:    protocol.HostCapabilities{Commands: []string{}},
		Limits:          c.s.o.Limits,
	}, nil
}

func (c *Conn) create(ctx context.Context, p protocol.SessionCreateParams) (protocol.SessionCreateResult, error) {
	d := c.defaultsOf()
	route := p.Model
	if route == nil {
		route = d.Model
	}
	sel, err := c.s.selection(route)
	if err != nil {
		return protocol.SessionCreateResult{}, err
	}
	id := session.ID(p.SessionID)
	if id == "" {
		id = newID()
	}
	if err := c.s.claim(id, c); err != nil {
		return protocol.SessionCreateResult{}, err
	}
	h, err := c.s.o.Agents.Create(c.owner, agent.CreateOptions{
		Session: id, Preset: orElse(p.AgentPreset, d.AgentPreset), Cwd: orElse(p.Cwd, d.Cwd), Model: sel,
	})
	if err != nil {
		c.s.release(id, c)
		return protocol.SessionCreateResult{}, wire(err)
	}
	a, err := c.adopt(ctx, id, h)
	if err != nil {
		return protocol.SessionCreateResult{}, err
	}
	cwd, err := c.cwdOf(ctx, a)
	if err != nil {
		return protocol.SessionCreateResult{}, err
	}
	return protocol.SessionCreateResult{SessionID: string(id), Cwd: cwd, LastSeq: lastSeq(a.Session())}, nil
}

func (c *Conn) resume(ctx context.Context, p protocol.SessionResumeParams) (protocol.SessionResumeResult, error) {
	id := session.ID(p.SessionID)
	if a, ok := c.agent(id); ok {
		// Idempotent for a session this connection already owns.
		return c.resumed(ctx, a, false)
	}
	sel, err := c.s.selection(p.Model)
	if err != nil {
		return protocol.SessionResumeResult{}, err
	}
	if err := c.s.claim(id, c); err != nil {
		return protocol.SessionResumeResult{}, err
	}
	a, err := c.resumeClaimed(ctx, id, sel)
	if err != nil {
		c.s.release(id, c)
		return protocol.SessionResumeResult{}, err
	}
	rep, _ := agent.OpenReport(a)
	return c.resumed(ctx, a, rep.Interrupted)
}

// resumeClaimed resumes id, which c has claimed: a session another writer holds
// live, in this process or another, is session/owned.
func (c *Conn) resumeClaimed(ctx context.Context, id session.ID, sel agent.ModelSelection) (agent.Agent, error) {
	if _, live := c.s.o.Agents.Get(id); live {
		return nil, fail(string(store.CodeOwned), fmt.Sprintf("session %q is live in this host under another owner", id))
	}
	rs, release, err := c.s.o.Sessions.OpenRead(ctx, id)
	if err != nil {
		return nil, wire(err)
	}
	hdr := rs.Header()
	release()
	if hdr.Origin == "subagent" {
		return nil, fail(protocol.ReasonSubagentSession, fmt.Sprintf("session %q is a subagent's; resume its root %q", id, hdr.Creator))
	}
	h, err := c.s.o.Agents.Resume(c.owner, agent.CreateOptions{Session: id, Model: sel})
	if err != nil {
		return nil, wire(err)
	}
	return c.adoptClaimed(ctx, id, h)
}

func (c *Conn) resumed(ctx context.Context, a agent.Agent, interrupted bool) (protocol.SessionResumeResult, error) {
	cwd, err := c.cwdOf(ctx, a)
	if err != nil {
		return protocol.SessionResumeResult{}, err
	}
	return protocol.SessionResumeResult{
		SessionID: string(a.Ref().Session), Cwd: cwd, LastSeq: lastSeq(a.Session()), ClosedInterruptedTurn: interrupted,
	}, nil
}

// adopt files h in c's table; c has claimed id. A closed connection disposes it.
func (c *Conn) adopt(ctx context.Context, id session.ID, h agent.Handle) (agent.Agent, error) {
	a, err := c.adoptClaimed(ctx, id, h)
	if err != nil {
		c.s.release(id, c)
	}
	return a, err
}

func (c *Conn) adoptClaimed(ctx context.Context, id session.ID, h agent.Handle) (agent.Agent, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.Join(fail(protocol.ReasonShuttingDown, "the connection is closing"), h.Dispose(ctx))
	}
	c.owned[id] = h
	c.mu.Unlock()
	return h.Agent(), nil
}

func (c *Conn) list(ctx context.Context, p protocol.SessionListParams) (protocol.SessionListResult, error) {
	out := protocol.SessionListResult{Sessions: []protocol.SessionSummary{}}
	q := c.s.o.Query
	if q == nil {
		return out, nil
	}
	page, err := q.List(ctx, query.ListOptions{Cwd: p.Cwd, Cursor: p.Cursor, Limit: p.Limit})
	if err != nil {
		return out, wire(err)
	}
	for _, e := range page.Entries {
		if e.Header.Origin == "subagent" {
			continue
		}
		out.Sessions = append(out.Sessions, protocol.SessionSummary{
			SessionID: string(e.ID), Cwd: e.Header.Cwd, Title: e.Summary.Preview,
			UpdatedAt: e.LastEventAt.UTC().Format(time.RFC3339Nano),
		})
	}
	out.NextCursor = page.Next
	return out, nil
}

func (c *Conn) closeSession(ctx context.Context, p protocol.SessionRef) (protocol.LastSeqResult, error) {
	id := session.ID(p.SessionID)
	c.mu.Lock()
	h, ok := c.owned[id]
	delete(c.owned, id)
	c.mu.Unlock()
	if !ok {
		_, err := c.driven(p.SessionID)
		return protocol.LastSeqResult{}, err
	}
	defer c.s.release(id, c)
	rs := h.Agent().Session()
	if err := h.Dispose(ctx); err != nil {
		return protocol.LastSeqResult{}, wire(err)
	}
	return protocol.LastSeqResult{LastSeq: lastSeq(rs)}, nil
}

// shutdown is replied first; the launcher unloads the tree after the reply.
func (c *Conn) shutdown(context.Context, protocol.Empty) (protocol.Empty, error) {
	return protocol.Empty{}, nil
}

// ---------------------------------------------------------------------- drive

func (c *Conn) prompt(ctx context.Context, p protocol.SessionPromptParams) (protocol.SessionPromptResult, error) {
	list := agent.ListNextTurn
	if p.Delivery == "steer" {
		list = agent.ListNextStep
	}
	r, err := c.send(ctx, p.SessionID, list, p.MessageID, p.Content)
	if err != nil {
		return protocol.SessionPromptResult{}, err
	}
	return protocol.SessionPromptResult{MessageID: r.MessageID, Seq: int64(r.Seq)}, nil
}

// query queues the content as a prompt: no command registry is mounted yet, so
// initialize advertises no commands and nothing is recognised as one.
func (c *Conn) query(ctx context.Context, p protocol.SessionQueryParams) (protocol.SessionQueryResult, error) {
	r, err := c.send(ctx, p.SessionID, agent.ListNextTurn, "", p.Content)
	if err != nil {
		return protocol.SessionQueryResult{}, err
	}
	return protocol.SessionQueryResult{Kind: "prompt", MessageID: r.MessageID, Seq: int64(r.Seq)}, nil
}

// send queues content on an owned session's list. A repeated message id appends
// nothing and answers the first receipt.
func (c *Conn) send(ctx context.Context, id string, list agent.List, msgID string, content []protocol.ContentBlock) (agent.Receipt, error) {
	a, err := c.driven(id)
	if err != nil {
		return agent.Receipt{}, err
	}
	text, err := textOf(content)
	if err != nil {
		return agent.Receipt{}, err
	}
	r, err := agent.Send(ctx, a, list, agent.InboxMessage{ID: msgID, Source: agent.SourceUser, By: c.by(), Text: text})
	if dup, ok := errors.AsType[*agent.DuplicateError](err); ok {
		return dup.First, nil
	}
	if err != nil {
		return agent.Receipt{}, wire(err)
	}
	return r, nil
}

// wait returns once the root is idle and its live descendants have finished.
func (c *Conn) wait(ctx context.Context, p protocol.SessionRef) (protocol.LastSeqResult, error) {
	a, err := c.driven(p.SessionID)
	if err != nil {
		return protocol.LastSeqResult{}, err
	}
	for settled := false; !settled; {
		settled = true
		for _, x := range c.Tree(a.Ref().Session) {
			if err := agent.WaitIdle(ctx, x); err != nil {
				return protocol.LastSeqResult{}, wire(err)
			}
		}
		// A child may have started or woken its parent meanwhile.
		for _, x := range c.Tree(a.Ref().Session) {
			settled = settled && x.Status() == agent.StatusIdle
		}
	}
	return protocol.LastSeqResult{LastSeq: lastSeq(a.Session())}, nil
}

// cancel cancels with cause user; a turn cancel replies after the turn/end.
func (c *Conn) cancel(ctx context.Context, p protocol.SessionCancelParams) (protocol.SessionCancelResult, error) {
	a, err := c.driven(p.SessionID)
	if err != nil {
		return protocol.SessionCancelResult{}, err
	}
	scope := agent.ScopeTurn
	if p.Scope == "step" {
		scope = agent.ScopeStep
	}
	running := a.Status() == agent.StatusRunning
	if err := agent.Cancel(a, agent.CauseUser, agent.CancelOptions{Scope: scope, Inbox: agent.InboxChoice(p.Inbox)}); err != nil {
		return protocol.SessionCancelResult{}, wire(err)
	}
	if running && scope == agent.ScopeTurn {
		if err := agent.WaitIdle(ctx, a); err != nil {
			return protocol.SessionCancelResult{}, wire(err)
		}
	}
	return protocol.SessionCancelResult{Cancelled: running, LastSeq: lastSeq(a.Session())}, nil
}

// -------------------------------------------------------------------- history

// history pages the committed events of any session, live or stored, with no
// lease. The first page starts at seq 0 (afterSeq absent or 0); later pages
// start after afterSeq. A page is cut before it would exceed half a frame.
func (c *Conn) history(ctx context.Context, p protocol.SessionHistoryParams) (protocol.SessionHistoryResult, error) {
	limit := p.Limit
	switch {
	case limit <= 0:
		limit = DefaultHistoryLimit
	case limit > MaxHistoryLimit:
		limit = MaxHistoryLimit
	}
	rs, release, err := c.s.read(ctx, session.ID(p.SessionID))
	if err != nil {
		return protocol.SessionHistoryResult{}, err
	}
	defer release()
	var types map[string]bool
	if len(p.Types) > 0 {
		types = map[string]bool{}
		for _, t := range p.Types {
			types[t] = true
		}
	}
	budget := c.s.o.Limits.MaxFrameBytes / 2
	out := protocol.SessionHistoryResult{Events: []protocol.Event{}, LastSeq: lastSeq(rs)}
	pos, head := event.Offset(0), rs.Len()
	if p.AfterSeq > 0 {
		pos = event.Offset(p.AfterSeq + 1)
	}
	var used int64
page:
	for pos < head && len(out.Events) < limit {
		evs, err := rs.Read(ctx, pos, limit)
		if err != nil {
			return protocol.SessionHistoryResult{}, wire(err)
		}
		if len(evs) == 0 {
			break
		}
		for _, e := range evs {
			if types != nil && !types[e.Type()] {
				pos = e.Seq() + 1
				continue
			}
			w := wireEvent(e)
			size := int64(len(w.Data)) + 256
			if budget > 0 && used+size > budget && len(out.Events) > 0 {
				break page
			}
			used += size
			out.Events = append(out.Events, w)
			pos = e.Seq() + 1
			if len(out.Events) == limit {
				break page
			}
		}
	}
	out.More = pos < head
	return out, nil
}

// read is session id for reading: the live session when this process holds it,
// else a lease-free read of the stored one.
func (s *Server) read(ctx context.Context, id session.ID) (session.ReadSession, func(), error) {
	if a, ok := s.o.Agents.Get(id); ok {
		return a.Session(), func() {}, nil
	}
	if live, ok := s.o.Sessions.Get(id); ok {
		return live, func() {}, nil
	}
	rs, release, err := s.o.Sessions.OpenRead(ctx, id)
	if err != nil {
		return nil, nil, wire(err)
	}
	return rs, release, nil
}

func wireEvent(e event.Event) protocol.Event {
	data := json.RawMessage(e.Raw())
	if len(data) == 0 {
		data = json.RawMessage("null")
	}
	var sources []string
	for _, s := range e.Sources() {
		sources = append(sources, strconv.FormatInt(int64(s), 10))
	}
	return protocol.Event{
		Seq: int64(e.Seq()), Time: time.UnixMilli(e.Time()).UTC().Format(time.RFC3339Nano), Type: e.Type(),
		Version: e.Version(), Ignorable: e.Ignorable(), Sources: sources, Data: data,
	}
}

// ---------------------------------------------------- model, working directory

func (c *Conn) setModel(ctx context.Context, p protocol.ModelSetParams) (protocol.ModelSetResult, error) {
	a, err := c.driven(p.SessionID)
	if err != nil {
		return protocol.ModelSetResult{}, err
	}
	sel, err := c.s.selection(&p.Model)
	if err != nil {
		return protocol.ModelSetResult{}, err
	}
	if _, err := agent.SelectModel(ctx, a, sel); err != nil {
		return protocol.ModelSetResult{}, wire(err)
	}
	return protocol.ModelSetResult{Model: p.Model}, nil
}

func (c *Conn) getWorkdir(ctx context.Context, p protocol.SessionRef) (protocol.WorkingDirectoryResult, error) {
	a, err := c.driven(p.SessionID)
	if err != nil {
		return protocol.WorkingDirectoryResult{}, err
	}
	cwd, err := c.cwdOf(ctx, a)
	return protocol.WorkingDirectoryResult{Cwd: cwd}, err
}

func (c *Conn) setWorkdir(ctx context.Context, p protocol.WorkingDirectorySetParams) (protocol.WorkingDirectoryResult, error) {
	a, err := c.driven(p.SessionID)
	if err != nil {
		return protocol.WorkingDirectoryResult{}, err
	}
	if c.s.o.Workdir == nil {
		return protocol.WorkingDirectoryResult{}, fail(string(workdir.CodeUnavailable), "this host serves no working-directory service")
	}
	wd, err := c.s.o.Workdir.Change(ctx, a.Ref().Session, p.Path)
	if err != nil {
		return protocol.WorkingDirectoryResult{}, wire(err)
	}
	return protocol.WorkingDirectoryResult{Cwd: wd.Path}, nil
}

// cwdOf is a's working directory: the workdir fold's when served, else the
// header's.
func (c *Conn) cwdOf(ctx context.Context, a agent.Agent) (string, error) {
	if c.s.o.Workdir == nil {
		return a.Session().Header().Cwd, nil
	}
	wd, err := c.s.o.Workdir.Ensure(ctx, a.Ref().Session)
	if err != nil {
		return "", wire(err)
	}
	return wd.Path, nil
}

// -------------------------------------------------------------------- helpers

// selection is route as an agent selection, validated by exact id; nil is none.
func (s *Server) selection(route *protocol.ModelRoute) (agent.ModelSelection, error) {
	if route == nil {
		return agent.ModelSelection{}, nil
	}
	id := vocab.RouteID(route.Provider)
	if err := id.Validate(); err != nil {
		return agent.ModelSelection{}, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Reason: jsonrpc.ReasonInvalidParams, Message: "model.provider: " + err.Error()}
	}
	if s.o.Routes != nil && !slices.Contains(s.o.Routes.Routes(), id) {
		return agent.ModelSelection{}, fail(string(vocab.CodeNoRoute), fmt.Sprintf("no route %q", id))
	}
	return agent.ModelSelection{Route: id, Model: route.Model, Controls: vocab.Controls{
		Effort: route.ReasoningEffort, MaxOutputTokens: route.MaxOutputTokens,
	}}, nil
}

// textOf is the text of a prompt's blocks. Inline images (H-3) and attachment
// references are not built: they are sdk/invalid-content.
func textOf(blocks []protocol.ContentBlock) (string, error) {
	var parts []string
	for i, b := range blocks {
		if b.Type != "text" {
			return "", fail(protocol.ReasonInvalidContent, fmt.Sprintf("content[%d]: %q blocks are not supported by this host", i, b.Type))
		}
		parts = append(parts, b.Text)
	}
	text := strings.Join(parts, "\n\n")
	if strings.TrimSpace(text) == "" {
		return "", fail(protocol.ReasonInvalidContent, "content has no text")
	}
	return text, nil
}

func (c *Conn) defaultsOf() protocol.Defaults {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.defaults
}

// by names the carrier's principal on the messages it queues.
func (c *Conn) by() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return "sdk:" + orElse(c.client.Name, "client")
}

// lastSeq is the seq of rs's last committed event, -1 for none.
func lastSeq(rs session.ReadSession) int64 { return int64(rs.Len()) - 1 }

func newID() session.ID {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return session.ID("ses_" + hex.EncodeToString(b[:]))
}

func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func fail(reason, msg string) *jsonrpc.Error {
	return &jsonrpc.Error{Code: jsonrpc.CodeApplication, Message: msg, Reason: reason}
}

// wire is err as the peer sees it: the owner's reason, unchanged. An error that
// names its own reason, or none, is left to the codec.
func wire(err error) error {
	if owned, ok := errors.AsType[*store.OwnedError](err); ok {
		return passed(string(store.CodeOwned), owned)
	}
	if se, ok := errors.AsType[*store.Error](err); ok {
		return passed(string(se.Code), se)
	}
	if ae, ok := errors.AsType[*agent.Error](err); ok {
		return passed(string(ae.Code), ae)
	}
	return err
}

func passed(reason string, err error) *jsonrpc.Error {
	msg := err.Error()
	if after, ok := strings.CutPrefix(msg, reason+": "); ok {
		msg = after
	}
	return fail(reason, msg)
}
