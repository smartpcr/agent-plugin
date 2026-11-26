package sdkserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
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
	if _, err := session.Closed.On(c.owner, func(_ *cordis.Context, a session.ClosedArgs) error {
		c.childClosed(a.ID)
		return nil
	}); err != nil {
		return fmt.Errorf("sdkserver: listen for closes: %w", err)
	}
	for _, rs := range owned {
		c.follow(rs, rs.Len())
	}
	return nil
}

// deferred is the transport of a codec not started yet: Serve attaches it
// before the codec can dispatch a request, then sets the codec. A forwarder
// writing early waits for it.
type deferred struct {
	ready chan struct{}
	n     *jsonrpc.Conn
	w     *lineWriter // the codec's writer, which Fault writes past the codec

	mu  sync.Mutex
	err error // the forwarding failure that ended the transport
}

func (d *deferred) Notify(method string, params any) error {
	<-d.ready
	return d.n.Notify(method, params)
}

// Call carries a host request (answer.go) once the codec is set.
func (d *deferred) Call(ctx context.Context, method string, params, result any) error {
	select {
	case <-d.ready:
	case <-ctx.Done():
		return ctx.Err()
	}
	return d.n.Call(ctx, method, params, result)
}

// Fault sends e to the client's null id (answer.go). jsonrpc.Conn sends no
// error to the null id, so the line is written to the codec's writer directly,
// between two of the codec's lines (it writes one line per Write) and after
// every frame queued before it (a Barrier), so the client reads the rejection
// before the request asked again.
func (d *deferred) Fault(e *jsonrpc.Error) error {
	<-d.ready
	if e == nil {
		return errors.New("sdkserver: Fault needs an error")
	}
	line, err := jsonrpc.Encode(&jsonrpc.Response{ID: jsonrpc.ID{}, Error: e})
	if err != nil {
		return fmt.Errorf("sdkserver: encode the rejection: %w", err)
	}
	if err := d.n.Barrier(context.Background()); err != nil {
		return err
	}
	return d.w.writeLine(append(line, '\n'), d.n.Done())
}

// lineWriter serializes whole lines onto w: the codec's and Fault's.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// writeLine writes line unless the codec ends first. Like the codec's own
// writer, a write stalled by the peer stays until the caller closes w.
func (l *lineWriter) writeLine(line []byte, done <-chan struct{}) error {
	written := make(chan error, 1)
	go func() {
		_, err := l.Write(line)
		written <- err
	}()
	select {
	case err := <-written:
		return err
	case <-done:
		return fail(jsonrpc.ReasonClosed, "the connection ended before the rejection was written")
	}
}

func (d *deferred) set(n *jsonrpc.Conn) {
	d.n = n
	close(d.ready)
}

// abort ends the transport for a failed forwarder, keeping the first failure
// unless the transport had already ended.
func (d *deferred) abort(err error) {
	<-d.ready
	select {
	case <-d.n.Done():
		return
	default:
	}
	d.mu.Lock()
	if d.err == nil {
		d.err = err
	}
	d.mu.Unlock()
	_ = d.n.Close()
}

func (d *deferred) failure() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// abort ends the connection's transport after a forwarder failed: the client
// would otherwise wait for events or a session.run.completed never sent, with
// no reply to carry the failure. A notifier that cannot be closed is left as is;
// the next watermark request still fails. A failure to write to a transport
// already closing is not a forwarding failure.
func (c *Conn) abort(err error) {
	if err == nil || jsonrpc.HasReason(err, jsonrpc.ReasonClosed) {
		return
	}
	c.mu.Lock()
	out := c.out
	c.mu.Unlock()
	switch n := out.(type) {
	case *deferred:
		n.abort(err)
	case interface{ Close() error }:
		_ = n.Close()
	}
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

// childClosed ends the forwarder of a closed subagent session once it has sent
// the events the close wrote. A root's forwarder is left to session/close and
// drop, which stop it with its descendants.
func (c *Conn) childClosed(id session.ID) {
	c.mu.Lock()
	f := c.fwd[id]
	c.mu.Unlock()
	if f == nil || f.scope.ParentSessionID == "" {
		return
	}
	go func() {
		// The drain context is cancelled when the root is unfollowed or the
		// connection closes: nothing is sent for a session no longer reported.
		ctx := f.drainCtx
		defer f.drainCancel()
		if err := f.stop(ctx); err == nil && f.failure() == nil {
			if err := c.drainClosed(ctx, f); err != nil && ctx.Err() == nil {
				c.abort(err)
			}
		}
		c.mu.Lock()
		if c.fwd[id] == f {
			delete(c.fwd, id)
		}
		c.mu.Unlock()
	}()
}

// unfollow stops the forwarder of root id and those of its subagent
// descendants, and waits for them to end; it returns the root's stopped
// forwarder, nil for none.
func (c *Conn) unfollow(ctx context.Context, id session.ID) (*forwarder, error) {
	c.mu.Lock()
	f := c.fwd[id]
	delete(c.fwd, id)
	var kids []*forwarder
	for kid, k := range c.fwd {
		if k.scope.RootSessionID == string(id) {
			kids = append(kids, k)
			delete(c.fwd, kid)
		}
	}
	c.mu.Unlock()
	var errs []error
	for _, k := range kids {
		k.drainCancel()
		errs = append(errs, k.stop(ctx))
	}
	if f == nil {
		return nil, errors.Join(errs...)
	}
	f.drainCancel()
	return f, errors.Join(append([]error{f.stop(ctx)}, errs...)...)
}

// stopForwarders stops every forwarder of a closing connection.
func (c *Conn) stopForwarders(ctx context.Context) error {
	c.mu.Lock()
	fs := c.fwd
	c.fwd = map[session.ID]*forwarder{}
	c.mu.Unlock()
	var errs []error
	for _, f := range fs {
		f.drainCancel()
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
	fail  func(error) // ends the transport after a failure
	rs    session.ReadSession
	scope protocol.Scope
	runs  *runTracker // nil for a subagent: its result is subagent.finished
	start event.Offset

	// drainCtx bounds a closed subagent's final drain; it is cancelled when
	// the forwarder is unfollowed or its connection closes.
	drainCtx    context.Context
	drainCancel context.CancelFunc

	wake chan struct{} // one pending wake-up
	quit chan struct{}
	done chan struct{}

	mu     sync.Mutex
	cursor event.Offset  // the next seq to send
	moved  chan struct{} // closed and replaced when cursor moves or the forwarder ends
	ended  bool
	err    error // why it ended, nil for a stop
}

// forwardFailed is the reply of a request whose seq a forwarder could not send.
func forwardFailed(id string, seq event.Seq, err error) error {
	msg := fmt.Sprintf("session %s: event %d was not sent", id, seq)
	if err != nil {
		msg += ": " + err.Error()
	} else {
		msg += ": forwarding stopped"
	}
	return &jsonrpc.Error{Code: jsonrpc.CodeInternal, Reason: jsonrpc.ReasonInternal, Message: msg}
}

func (c *Conn) newForwarder(rs session.ReadSession, sc protocol.Scope, runs *runTracker, start event.Offset) *forwarder {
	dctx, dcancel := context.WithCancel(context.Background())
	return &forwarder{
		out: c.out, fail: c.abort, rs: rs, scope: sc, runs: runs, start: start, cursor: start,
		drainCtx: dctx, drainCancel: dcancel,
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
	// under the client is session.closed, which is not sent yet. Anything but a
	// stop is a failure: the client misses events from the cursor on, so the
	// transport ends rather than leave it waiting for them.
	if f.stopping() {
		err = nil
	}
	f.end(err)
	if err != nil {
		f.fail(err)
	}
}

func (f *forwarder) stopping() bool {
	select {
	case <-f.quit:
		return true
	default:
		return false
	}
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

func (f *forwarder) end(err error) {
	f.mu.Lock()
	f.ended, f.err = true, err
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

// failure is why the forwarder ended short of its session's end, nil while it
// runs or after a stop.
func (f *forwarder) failure() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.err
}

// reached waits until seq is sent. A forwarder that ended before sending it
// fails the caller: the reply must not claim seq while the client lacks it.
func (f *forwarder) reached(ctx context.Context, seq event.Seq) error {
	f.poke()
	for {
		f.mu.Lock()
		sent, ended, err, moved := f.cursor > seq, f.ended, f.err, f.moved
		f.mu.Unlock()
		if sent {
			return nil
		}
		if ended {
			return forwardFailed(f.scope.SessionID, seq, err)
		}
		select {
		case <-moved:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
