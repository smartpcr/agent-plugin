package jsonrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"sync"
	"time"
)

// Defaults of the bounds Options leave unset.
const (
	DefaultMaxPending     = 64
	DefaultMaxInFlight    = 64
	DefaultMaxQueuedBytes = 32 << 20
	// DefaultWriteStall is the write-stall deadline, as stdio.Config's writeStall.
	DefaultWriteStall = 60 * time.Second
)

// CancelMethod is the notification, sent both ways, that cancels request params.id.
const CancelMethod = "$/cancel"

// Handler answers one request. Its context ends when the peer sends $/cancel for the
// request or the connection closes. A returned *Error, or an error with a
// Reason() string method, reaches the peer with its reason; any other error is sent
// as jsonrpc/internal without its text.
type Handler func(ctx context.Context, params json.RawMessage) (result any, err error)

// Options configure a connection. Zero values select the defaults.
type Options struct {
	// MaxFrameBytes bounds every frame (one line) read and written
	// (DefaultMaxLineBytes).
	MaxFrameBytes int
	// MaxPending bounds the outbound calls awaiting a response; one more fails at
	// once with jsonrpc/too-many-pending (DefaultMaxPending).
	MaxPending int
	// MaxInFlight bounds the inbound requests whose handler is running; one more is
	// answered jsonrpc/busy (DefaultMaxInFlight).
	MaxInFlight int
	// MaxQueuedBytes bounds the frames waiting for the writer; Call and Notify block
	// while it is reached (DefaultMaxQueuedBytes).
	MaxQueuedBytes int
	// WriteStall bounds one write to the peer: a frame the writer has not accepted
	// within it ends the connection with jsonrpc/write-stalled, and every pending
	// call fails once with that error (DefaultWriteStall). The blocked Write itself
	// returns only when the caller closes w.
	WriteStall time.Duration
	// Methods is the closed allow-list of request methods. Any other method is
	// answered jsonrpc/method-not-found and reaches no handler.
	Methods map[string]Handler
	// OnNotify receives every notification but $/cancel, in arrival order.
	OnNotify func(method string, params json.RawMessage)
	// OnError receives the faults that cannot be answered: a response that names no
	// pending call, or an error the peer sent to the null id.
	OnError func(err error)
	// Log, when set, receives the same faults as OnError and the write stall that
	// ends a connection, as warnings. The codec writes no other log line.
	Log *slog.Logger
}

func (o Options) withDefaults() Options {
	if o.MaxFrameBytes <= 0 {
		o.MaxFrameBytes = DefaultMaxLineBytes
	}
	if o.WriteStall <= 0 {
		o.WriteStall = DefaultWriteStall
	}
	if o.MaxPending <= 0 {
		o.MaxPending = DefaultMaxPending
	}
	if o.MaxInFlight <= 0 {
		o.MaxInFlight = DefaultMaxInFlight
	}
	if o.MaxQueuedBytes <= 0 {
		o.MaxQueuedBytes = DefaultMaxQueuedBytes
	}
	return o
}

// Conn is one JSON-RPC connection over a reader and a writer. One goroutine reads
// lines and settles responses and notifications in arrival order; request handlers
// run concurrently, at most MaxInFlight; one goroutine writes, so frames leave in
// one total order.
type Conn struct {
	o  Options
	rd *Reader
	w  io.Writer
	q  *queue

	base   context.Context
	cancel context.CancelFunc
	stop   func() bool

	mu       sync.Mutex
	nextID   int64
	pending  map[ID]chan *Response
	inflight map[ID]*running
	err      error
	done     chan struct{}

	// One-shot mode (ServeOne): framed is set by the reader once the first frame is
	// read, replied is closed once that request's reply is queued, and refusal is
	// the error a second frame was answered with (under mu).
	oneShot bool
	framed  bool
	replied chan struct{}
	reply   sync.Once
	refusal error
}

type running struct {
	cancel    context.CancelFunc
	cancelled bool
}

// Serve starts a connection reading frames from r and writing to w. It ends when r
// ends, when ctx ends, on a line over the bound, on a write error, on a write stall
// or on Close. The caller owns r and w: closing them is what stops a blocked read
// or write.
func Serve(ctx context.Context, r io.Reader, w io.Writer, o Options) *Conn {
	return start(ctx, r, w, o, false)
}

func start(ctx context.Context, r io.Reader, w io.Writer, o Options, oneShot bool) *Conn {
	o = o.withDefaults()
	c := &Conn{
		o:        o,
		rd:       NewReader(r, o.MaxFrameBytes),
		w:        w,
		q:        newQueue(o.MaxQueuedBytes),
		pending:  map[ID]chan *Response{},
		inflight: map[ID]*running{},
		done:     make(chan struct{}),
		oneShot:  oneShot,
	}
	if oneShot {
		c.replied = make(chan struct{})
	}
	c.base, c.cancel = context.WithCancel(context.WithoutCancel(ctx))
	// The callback runs closeWith, which takes c.mu: holding it here makes the
	// callback of an already-ended ctx wait until c.stop is assigned.
	c.mu.Lock()
	c.stop = context.AfterFunc(ctx, func() { c.closeWith(newError(ReasonClosed, "the connection's context ended")) })
	c.mu.Unlock()
	go c.writeLoop()
	go c.readLoop()
	return c
}

// Done is closed when the connection has ended.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns why the connection ended, or nil while it runs.
func (c *Conn) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Close ends the connection: every pending call fails once with jsonrpc/closed,
// every handler's context ends, and nothing more is dispatched or written.
func (c *Conn) Close() error {
	c.closeWith(newError(ReasonClosed, "closed by this end"))
	return nil
}

func (c *Conn) closeWith(err error) {
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return
	}
	c.err = err
	// A write stall means the peer is gone: the pending calls fail with the stall
	// itself. Any other end fails them with jsonrpc/closed naming the cause.
	var closed *Error
	if !errors.As(err, &closed) || closed.Reason != ReasonWriteStalled {
		closed = newError(ReasonClosed, "the connection ended before the response: "+err.Error())
	}
	for id, ch := range c.pending {
		ch <- &Response{ID: id, Error: closed}
	}
	c.pending = nil
	stop := c.stop
	c.mu.Unlock()
	c.cancel()
	stop()
	c.q.close()
	close(c.done)
}

// Call sends a request and waits for its response, decoding the result into result
// when it is not nil. Ending ctx stops the wait and sends $/cancel to the peer. A
// call over MaxPending fails at once with jsonrpc/too-many-pending.
func (c *Conn) Call(ctx context.Context, method string, params, result any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		c.mu.Unlock()
		return newError(ReasonClosed, "the connection has ended: "+c.err.Error())
	}
	if len(c.pending) >= c.o.MaxPending {
		c.mu.Unlock()
		return newError(ReasonTooManyPending, strconv.Itoa(c.o.MaxPending)+" calls are already pending")
	}
	c.nextID++
	id := Int64ID(c.nextID)
	ch := make(chan *Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	line, err := encodeLine(&Request{ID: id, Method: method, Params: raw}, c.o.MaxFrameBytes)
	if err == nil {
		// Waiting for queue room ends with ctx: the request was never sent, so no
		// $/cancel follows and the pending slot is released at once.
		err = c.q.push(ctx, line, nil, false)
	}
	if err != nil {
		c.forget(id)
		return err
	}
	select {
	case resp := <-ch:
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil {
			if err := json.Unmarshal(resp.Result, result); err != nil {
				return fmt.Errorf("jsonrpc: decoding the result of %s: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		if c.forget(id) {
			if line, err := encodeLine(&Notification{Method: CancelMethod, Params: cancelParams(id)}, c.o.MaxFrameBytes); err == nil {
				_ = c.q.push(ctx, line, nil, true)
			}
		}
		return ctx.Err()
	}
}

// forget removes a pending call and reports whether it was still pending.
func (c *Conn) forget(id ID) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.pending[id]
	delete(c.pending, id)
	return ok
}

func cancelParams(id ID) json.RawMessage {
	return json.RawMessage(`{"id":` + id.String() + `}`)
}

// Notify sends a notification. It blocks while the outbound queue is full.
func (c *Conn) Notify(method string, params any) error {
	raw, err := marshalParams(params)
	if err != nil {
		return err
	}
	line, err := encodeLine(&Notification{Method: method, Params: raw}, c.o.MaxFrameBytes)
	if err != nil {
		return err
	}
	return c.q.push(context.Background(), line, nil, false)
}

// Fault sends err as an error response to the null id: a fault the peer made
// that answers none of its requests (a reply the peer sent to one of ours that
// was invalid or late). The peer's OnError receives it.
func (c *Conn) Fault(err error) error {
	if err == nil {
		return errors.New("jsonrpc: Fault needs an error")
	}
	line, lerr := encodeLine(&Response{ID: ID{}, Error: toWire(err)}, c.o.MaxFrameBytes)
	if lerr != nil {
		return lerr
	}
	return c.q.push(context.Background(), line, nil, false)
}

// Barrier returns once every frame queued before it has been written.
func (c *Conn) Barrier(ctx context.Context) error {
	done := make(chan struct{})
	if err := c.q.push(ctx, nil, done, true); err != nil {
		return err
	}
	select {
	case <-done:
		return nil
	case <-c.done:
		return newError(ReasonClosed, "the connection ended before the barrier")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func marshalParams(params any) (json.RawMessage, error) {
	switch p := params.(type) {
	case nil:
		return nil, nil
	case json.RawMessage:
		return p, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, newError(ReasonInvalidParams, "the parameters do not encode: "+err.Error())
	}
	return raw, nil
}

// writeLoop is the one writer. Each frame is written by a helper goroutine while
// writeLoop waits for it with the WriteStall timer, so a peer that accepts nothing
// ends the connection instead of holding it forever. After a stall the helper stays
// in its Write until the caller closes w; it then exits.
func (c *Conn) writeLoop() {
	lines := make(chan []byte)
	written := make(chan error, 1)
	go func() {
		for line := range lines {
			_, err := c.w.Write(line)
			written <- err
		}
	}()
	defer close(lines)
	stall := time.NewTimer(c.o.WriteStall)
	stall.Stop()
	for {
		it, ok := c.q.pop()
		if !ok {
			return
		}
		if it.line != nil {
			lines <- it.line
			stall.Reset(c.o.WriteStall)
			select {
			case err := <-written:
				stall.Stop()
				if err != nil {
					c.closeWith(newError(ReasonClosed, "writing to the peer failed: "+err.Error()))
					return
				}
			case <-stall.C:
				err := newError(ReasonWriteStalled, "the peer accepted no frame for "+c.o.WriteStall.String())
				if c.o.Log != nil {
					c.o.Log.Warn("jsonrpc: write stalled; closing the connection", "err", err)
				}
				c.closeWith(err)
				return
			case <-c.done:
				return
			}
		}
		c.q.release(len(it.line))
		if it.done != nil {
			close(it.done)
		}
	}
}

// errOneRequest answers every frame of a one-shot connection after the first.
const errOneRequest = "a one-shot connection carries exactly one request"

func (c *Conn) readLoop() {
	for {
		line, err := c.rd.ReadLine()
		select {
		case <-c.done:
			return
		default:
		}
		if err != nil {
			if HasReason(err, ReasonLineTooLong) {
				c.answerAndClose(ID{}, err)
				return
			}
			if c.oneShot && c.framed {
				// The request is read and its reply is still owed: the end of the
				// peer's input does not end a one-shot connection.
				return
			}
			c.closeWith(newError(ReasonClosed, "the peer's input ended: "+err.Error()))
			return
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if c.oneShot {
			if c.framed {
				c.refuse(line)
				continue
			}
			c.framed = true
		}
		f, err := Decode(line)
		if err != nil {
			// The decode error is a parse-error or invalid-request.
			if c.oneShot {
				c.answerAndClose(requestID(line), err)
				return
			}
			c.respond(requestID(line), nil, err)
			continue
		}
		if _, ok := f.(*Request); c.oneShot && !ok {
			c.answerAndClose(ID{}, newError(ReasonInvalidRequest, errOneRequest+", and this frame is not one"))
			return
		}
		switch f := f.(type) {
		case *Request:
			c.dispatch(f)
		case *Notification:
			c.notify(f)
		case *Response:
			c.settle(f)
		}
	}
}

// refuse answers a frame after the first of a one-shot connection with
// jsonrpc/invalid-request, to its id when it has one, and records the refusal.
func (c *Conn) refuse(line []byte) {
	err := newError(ReasonInvalidRequest, errOneRequest)
	c.mu.Lock()
	if c.refusal == nil {
		c.refusal = err
	}
	c.mu.Unlock()
	c.respond(requestID(line), nil, err)
}

// served marks the reply of a one-shot connection's request as queued.
func (c *Conn) served() {
	if c.oneShot {
		c.reply.Do(func() { close(c.replied) })
	}
}

// requestID recovers the id of a request frame that Decode refused, so the error
// reaches the peer's pending call rather than the null id. It answers the null id
// when the id cannot be detected: the line is not one JSON object, it has no
// "method" (an answer to a malformed response would land in the peer's own id
// space), "id" is absent, repeated or not a valid non-null id.
func requestID(line []byte) ID {
	if !json.Valid(line) {
		return ID{}
	}
	dec := json.NewDecoder(bytes.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return ID{}
	}
	var raw json.RawMessage
	ids, method := 0, false
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return ID{}
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return ID{}
		}
		switch tok {
		case "id":
			ids++
			raw = v
		case "method":
			method = true
		}
	}
	if !method || ids != 1 {
		return ID{}
	}
	id, ok := parseID(raw)
	if !ok {
		return ID{}
	}
	return id
}

// answerAndClose writes one error frame to id for a fatal read fault, then ends the
// connection with it. The frame is the codec's own and small, so it is sent even
// when it is longer than a tiny line bound.
func (c *Conn) answerAndClose(id ID, err error) {
	if b, e := Encode(&Response{ID: id, Error: toWire(err)}); e == nil {
		line := append(b, '\n')
		done := make(chan struct{})
		if c.q.push(context.Background(), line, done, true) == nil {
			select {
			case <-done:
			case <-c.done:
			}
		}
	}
	c.closeWith(err)
}

func (c *Conn) dispatch(req *Request) {
	h, ok := c.o.Methods[req.Method]
	if !ok {
		c.respond(req.ID, nil, newError(ReasonMethodNotFound, strconv.Quote(req.Method)+" is not a method of this connection"))
		c.served()
		return
	}
	c.mu.Lock()
	if _, dup := c.inflight[req.ID]; dup {
		c.mu.Unlock()
		c.respond(req.ID, nil, newError(ReasonInvalidRequest, "request "+req.ID.String()+" is already in flight"))
		return
	}
	if len(c.inflight) >= c.o.MaxInFlight {
		c.mu.Unlock()
		c.respond(req.ID, nil, newError(ReasonBusy, strconv.Itoa(c.o.MaxInFlight)+" requests are already in flight"))
		return
	}
	ctx, cancel := context.WithCancel(c.base)
	run := &running{cancel: cancel}
	c.inflight[req.ID] = run
	c.mu.Unlock()
	go c.handle(ctx, req, h, run)
}

func (c *Conn) handle(ctx context.Context, req *Request, h Handler, run *running) {
	var result any
	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				err = newError(ReasonInternal, "internal error")
			}
		}()
		result, err = h(ctx, req.Params)
	}()
	c.mu.Lock()
	delete(c.inflight, req.ID)
	cancelled := run.cancelled
	c.mu.Unlock()
	run.cancel()
	if cancelled {
		result, err = nil, newError(ReasonCancelled, "request "+req.ID.String()+" was cancelled")
	}
	c.respond(req.ID, result, err)
	c.served()
}

// respond queues the response to id. Failures to queue mean the connection ended.
func (c *Conn) respond(id ID, result any, err error) {
	resp := &Response{ID: id}
	if err != nil {
		resp.Error = toWire(err)
	} else if raw, merr := json.Marshal(result); merr != nil {
		resp.Error = newError(ReasonInternal, "internal error")
	} else {
		resp.Result = raw
	}
	line, lerr := encodeLine(resp, c.o.MaxFrameBytes)
	if lerr != nil {
		// The codec's own error frame is small; it is sent past a tiny bound.
		b, _ := Encode(&Response{ID: id, Error: toWire(lerr)})
		line = append(b, '\n')
	}
	_ = c.q.push(context.Background(), line, nil, false)
}

func (c *Conn) notify(n *Notification) {
	if n.Method == CancelMethod {
		var p struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(n.Params, &p) != nil {
			return
		}
		id, ok := parseID(p.ID)
		if !ok {
			return
		}
		c.mu.Lock()
		if run := c.inflight[id]; run != nil {
			run.cancelled = true
			run.cancel()
		}
		c.mu.Unlock()
		return
	}
	if c.o.OnNotify != nil {
		c.o.OnNotify(n.Method, n.Params)
	}
}

func (c *Conn) settle(r *Response) {
	if r.ID.IsNull() {
		c.report(r.Error)
		return
	}
	c.mu.Lock()
	ch, ok := c.pending[r.ID]
	delete(c.pending, r.ID)
	c.mu.Unlock()
	if !ok {
		c.report(newError(ReasonUnknownResponse, "response "+r.ID.String()+" names no pending call"))
		return
	}
	ch <- r
}

// report hands a fault that cannot be answered to Log, then to OnError.
func (c *Conn) report(err error) {
	if c.o.Log != nil {
		c.o.Log.Warn("jsonrpc: a fault that cannot be answered", "err", err)
	}
	if c.o.OnError != nil {
		c.o.OnError(err)
	}
}

// queue is the outbound frame queue, bounded in bytes.
type queue struct {
	mu     sync.Mutex
	cond   *sync.Cond
	items  []item
	bytes  int
	max    int
	closed bool
}

type item struct {
	line []byte
	done chan struct{}
}

func newQueue(maxBytes int) *queue {
	q := &queue{max: maxBytes}
	q.cond = sync.NewCond(&q.mu)
	return q
}

// push appends a frame. Unless control is set it waits while the queued bytes would
// pass the bound (a frame alone in the queue is always admitted); the wait ends with
// ctx, returning its error. Control frames ($/cancel, barriers, the last error
// frame) are small and never wait.
func (q *queue) push(ctx context.Context, line []byte, done chan struct{}, control bool) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.full(line, control) {
		wake := context.AfterFunc(ctx, func() {
			q.mu.Lock()
			q.cond.Broadcast()
			q.mu.Unlock()
		})
		defer wake()
		for q.full(line, control) {
			if err := ctx.Err(); err != nil {
				return err
			}
			q.cond.Wait()
		}
	}
	if q.closed {
		return newError(ReasonClosed, "the connection has ended")
	}
	q.items = append(q.items, item{line: line, done: done})
	q.bytes += len(line)
	q.cond.Broadcast()
	return nil
}

// full reports whether a frame of line must wait for room.
func (q *queue) full(line []byte, control bool) bool {
	return !control && !q.closed && q.bytes > 0 && q.bytes+len(line) > q.max
}

// pop waits for the next frame; false once the queue is closed.
func (q *queue) pop() (item, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.items) == 0 && !q.closed {
		q.cond.Wait()
	}
	if q.closed {
		return item{}, false
	}
	it := q.items[0]
	q.items[0] = item{}
	q.items = q.items[1:]
	return it, true
}

func (q *queue) release(n int) {
	q.mu.Lock()
	q.bytes -= n
	q.cond.Broadcast()
	q.mu.Unlock()
}

func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.items = nil
	q.cond.Broadcast()
	q.mu.Unlock()
}
