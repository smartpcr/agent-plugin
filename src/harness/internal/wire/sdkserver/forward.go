package sdkserver

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/sdk/protocol"
)

// This file is the scoped event forwarder (DH-D-SDK-5, DH-ADR-SDK-7): one per
// session a connection is told about. It does not queue events from the commit
// listener: session.Committed only wakes it, and it reads the log from its
// cursor in pages and hands each event to the connection's one writer, so a
// slow client leaves the backlog in storage. It is also the run tracker
// (runs.go): a session.run.completed is written right after the turn/end that
// completes it.
//
// Guarantees (sdk-acp-typert.md §6.2): per session, session.event arrives in seq
// order with no gap from the lastSeq session/create or session/resume returned;
// a response carrying seq n is written after session.event n (the watermark);
// nothing is promised between two sessions. Only sessions this connection owns
// and their live subagent descendants are reported. session.status,
// session.stream and session.closed are not sent yet.

// Notifier writes one notification to the client; *jsonrpc.Conn is one.
type Notifier interface {
	Notify(method string, params any) error
}

// ForwardPage bounds one read of the log by a forwarder.
const ForwardPage = 256

// The host notifications the forwarder writes.
const (
	EventNotification        = "session.event"
	RunCompletedNotification = "session.run.completed"
)

// Attach starts reporting to n: from now on every session the connection
// creates or resumes, and each live subagent descendant, is forwarded. Attach it
// before initialize; a session already owned is forwarded from its current end.
// Serve attaches its transport itself. A second Attach is an error.
func (c *Conn) Attach(n Notifier) error {
	if n == nil {
		return errors.New("sdkserver: Attach needs a notifier")
	}
	c.mu.Lock()
	switch {
	case c.closed:
		c.mu.Unlock()
		return fail(protocol.ReasonShuttingDown, "the connection is closing")
	case c.out != nil:
		c.mu.Unlock()
		return errors.New("sdkserver: the connection is already attached")
	}
	c.out = n
	c.fwd = map[session.ID]*forwarder{}
	owned := make([]session.ReadSession, 0, len(c.owned))
	for _, h := range c.owned {
		owned = append(owned, h.Agent().Session())
	}
	c.mu.Unlock()
	// The listener is an effect of the connection's fiber: it leaves with it.
	if _, err := session.Committed.On(c.owner, func(_ *cordis.Context, a session.CommittedArgs) error {
		c.committed(a)
		return nil
	}); err != nil {
		return fmt.Errorf("sdkserver: listen for commits: %w", err)
	}
	for _, rs := range owned {
		c.follow(rs, rs.Len())
	}
	return nil
}

// deferred is the notifier of a transport not started yet: Serve attaches it
// before the codec can dispatch a request, then sets the codec. A forwarder
// writing early waits for it.
type deferred struct {
	ready chan struct{}
	n     Notifier
}

func (d *deferred) Notify(method string, params any) error {
	<-d.ready
	return d.n.Notify(method, params)
}

func (d *deferred) set(n Notifier) {
	d.n = n
	close(d.ready)
}

// follow starts forwarding root session rs from seq start, the first one its
// client has not been told of. It is a no-op unless attached, or while rs is
// forwarded; a forwarder that ended with its session is replaced.
func (c *Conn) follow(rs session.ReadSession, start event.Offset) {
	id := rs.ID()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.out == nil || c.closed {
		return
	}
	if old := c.fwd[id]; old != nil && !old.hasEnded() {
		return
	}
	f := c.newForwarder(rs, protocol.Scope{SessionID: string(id), RootSessionID: string(id)}, newRunTracker(), start)
	c.fwd[id] = f
	go f.run()
}

// committed wakes the forwarder of a's session. The first commit of a subagent
// whose creator is forwarded starts one for it, from that event: a child's
// notifications carry its root and parent.
func (c *Conn) committed(a session.CommittedArgs) {
	id := a.Session.ID()
	c.mu.Lock()
	f := c.fwd[id]
	if f == nil && !c.closed && c.out != nil {
		h := a.Session.Header()
		if p := c.fwd[session.ID(h.Creator)]; p != nil && h.Origin == "subagent" {
			f = c.newForwarder(a.Session, protocol.Scope{
				SessionID: string(id), RootSessionID: p.scope.RootSessionID, ParentSessionID: h.Creator,
			}, nil, a.Event.Seq())
			c.fwd[id] = f
			go f.run()
		}
	}
	c.mu.Unlock()
	if f != nil {
		f.poke()
	}
}

// unfollow stops the forwarder of id and waits for it to end; it returns the
// stopped forwarder, nil for none.
func (c *Conn) unfollow(ctx context.Context, id session.ID) (*forwarder, error) {
	c.mu.Lock()
	f := c.fwd[id]
	delete(c.fwd, id)
	c.mu.Unlock()
	if f == nil {
		return nil, nil
	}
	return f, f.stop(ctx)
}

// stopForwarders stops every forwarder of a closing connection.
func (c *Conn) stopForwarders(ctx context.Context) error {
	c.mu.Lock()
	fs := c.fwd
	c.fwd = map[session.ID]*forwarder{}
	c.mu.Unlock()
	var errs []error
	for _, f := range fs {
		errs = append(errs, f.stop(ctx))
	}
	return errors.Join(errs...)
}

// reached is the watermark: it returns once the client has been sent
// session.event seq of session id (and any run it completed). It returns at
// once when the session is not forwarded, seq is negative, or the forwarder
// has ended.
func (c *Conn) reached(ctx context.Context, id session.ID, seq int64) error {
	if seq < 0 {
		return nil
	}
	c.mu.Lock()
	f := c.fwd[id]
	c.mu.Unlock()
	if f == nil {
		return nil
	}
	return f.reached(ctx, event.Seq(seq))
}

// drainClosed sends what a session committed after f stopped, up to its end,
// from a lease-free read: the events a close wrote. f has stopped.
func (c *Conn) drainClosed(ctx context.Context, f *forwarder) error {
	if f == nil {
		return nil
	}
	rs, release, err := c.s.read(ctx, session.ID(f.scope.SessionID))
	if err != nil {
		return err
	}
	defer release()
	return f.pump(ctx, rs, false)
}

// forwarder is one session's log tail toward one connection.
type forwarder struct {
	out   Notifier
	rs    session.ReadSession
	scope protocol.Scope
	runs  *runTracker // nil for a subagent: its result is subagent.finished
	start event.Offset

	wake chan struct{} // one pending wake-up
	quit chan struct{}
	done chan struct{}

	mu     sync.Mutex
	cursor event.Offset  // the next seq to send
	moved  chan struct{} // closed and replaced when cursor moves or the forwarder ends
	ended  bool
}

func (c *Conn) newForwarder(rs session.ReadSession, sc protocol.Scope, runs *runTracker, start event.Offset) *forwarder {
	return &forwarder{
		out: c.out, rs: rs, scope: sc, runs: runs, start: start, cursor: start,
		wake: make(chan struct{}, 1), quit: make(chan struct{}), done: make(chan struct{}), moved: make(chan struct{}),
	}
}

func (f *forwarder) poke() {
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

func (f *forwarder) run() {
	// quit cancels a read in flight; a write blocked on a full queue ends with
	// the transport.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-f.quit:
			cancel()
		case <-ctx.Done():
		}
	}()
	err := f.prefix(ctx)
	for err == nil {
		if err = f.pump(ctx, f.rs, true); err != nil {
			break
		}
		select {
		case <-f.wake:
		case <-f.quit:
			err = context.Canceled
		}
	}
	// The run ends with its session, its transport or a stop. A session closed
	// under the client is session.closed, which is not sent yet.
	f.end()
}

// prefix folds the runs of the events before start, which the client is not
// sent: a turn open at start is reported when it ends.
func (f *forwarder) prefix(ctx context.Context) error {
	if f.runs == nil {
		return nil
	}
	for pos := event.Offset(0); pos < f.start; {
		evs, err := f.rs.Read(ctx, pos, min(ForwardPage, int(f.start-pos)))
		if err != nil {
			return err
		}
		if len(evs) == 0 {
			return nil
		}
		for _, e := range evs {
			if e.Seq() >= f.start {
				return nil
			}
			if _, err := f.runs.observe(e); err != nil {
				return err
			}
			pos = e.Seq() + 1
		}
	}
	return nil
}

// pump sends every committed event of rs from the cursor to rs's end, each
// followed by the runs it completes. live stops it between pages on quit.
func (f *forwarder) pump(ctx context.Context, rs session.ReadSession, live bool) error {
	for {
		f.mu.Lock()
		pos := f.cursor
		f.mu.Unlock()
		if pos >= rs.Len() {
			return nil
		}
		if live {
			select {
			case <-f.quit:
				return context.Canceled
			default:
			}
		}
		evs, err := rs.Read(ctx, pos, ForwardPage)
		if err != nil {
			return err
		}
		if len(evs) == 0 {
			return nil
		}
		for _, e := range evs {
			if e.Seq() < pos {
				continue
			}
			if err := f.send(e); err != nil {
				return err
			}
			pos = e.Seq() + 1
			f.advance(pos)
		}
	}
}

// send writes e and then each run it completes.
func (f *forwarder) send(e event.Event) error {
	var runs []protocol.RunCompletedNotification
	if f.runs != nil {
		var err error
		if runs, err = f.runs.observe(e); err != nil {
			return err
		}
	}
	if err := f.out.Notify(EventNotification, protocol.SessionEventNotification{Scope: f.scope, Event: wireEvent(e)}); err != nil {
		return err
	}
	for _, r := range runs {
		r.Scope = f.scope
		if err := f.out.Notify(RunCompletedNotification, r); err != nil {
			return err
		}
	}
	return nil
}

func (f *forwarder) advance(pos event.Offset) {
	f.mu.Lock()
	f.cursor = pos
	close(f.moved)
	f.moved = make(chan struct{})
	f.mu.Unlock()
}

func (f *forwarder) end() {
	f.mu.Lock()
	f.ended = true
	close(f.moved)
	f.moved = make(chan struct{})
	f.mu.Unlock()
	close(f.done)
}

func (f *forwarder) hasEnded() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ended
}

// stop ends the forwarder and waits for it, or for ctx.
func (f *forwarder) stop(ctx context.Context) error {
	select {
	case <-f.quit:
	default:
		close(f.quit)
	}
	select {
	case <-f.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (f *forwarder) reached(ctx context.Context, seq event.Seq) error {
	f.poke()
	for {
		f.mu.Lock()
		ok, moved := f.cursor > seq || f.ended, f.moved
		f.mu.Unlock()
		if ok {
			return nil
		}
		select {
		case <-moved:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
