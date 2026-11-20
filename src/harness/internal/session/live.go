package session

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// The deliverer's stop states.
const (
	running = iota
	drain   // deliver up to the last committed event, then finish
	stopNow // finish without delivering more
)

// live is one live session: the core folds under the append lock and a
// deliverer that tails storage.
type live struct {
	svc    *service
	id     ID
	header Header
	key    scope.Key
	w      store.Writer

	mu      sync.Mutex // the append lock
	st      *surface.State
	closed  bool
	cause   error
	sinceCP int

	durable atomic.Uint64

	dmu    sync.Mutex
	dcond  *sync.Cond
	target event.Seq // the next seq once committed; the deliverer's goal
	next   event.Seq // the deliverer's cursor
	// The deliverer checks every record it delivers against a hash chain over
	// the records this session committed, link(h, r) = sha256(h || r's hot
	// fields), so the session's state stays a fixed size however far delivery
	// lags. The appender keeps the chain's head and leaves an anchor, the
	// chain's value at a seq, per append, thinning them to anchorLimit. Before
	// delivering anything below an anchor the deliverer re-derives the chain
	// from storage up to it; a segment longer than a page is first scanned
	// whole and split with anchors of its own (refine), each derived from a
	// scan that matched the anchor above it. No read is trusted unless it
	// itself re-derived an anchor.
	head     [32]byte // the chain at target; the appender's, under mu
	anchors  []anchor // ascending, the last at target; under dmu
	refine   []anchor // the deliverer's splits, ascending; under dmu
	at       [32]byte // the chain at next; the deliverer's
	stop     int
	finished chan struct{}
}

// anchor is the chain's value over the records below seq.
type anchor struct {
	seq event.Seq
	h   [32]byte
}

// anchorLimit bounds the appender's anchors; refineFanout is how many anchors
// one scan of a long segment leaves.
var (
	anchorLimit  = 64
	refineFanout = 16
)

func newLive(v *service, id ID, h Header, key scope.Key, w store.Writer, st *surface.State, cursor, durable event.Seq) *live {
	s := &live{svc: v, id: id, header: h, key: key, w: w, st: st,
		target: st.Len(), next: cursor, finished: make(chan struct{})}
	s.head = sha256.Sum256([]byte(id))
	s.at = s.head
	s.dcond = sync.NewCond(&s.dmu)
	s.durable.Store(uint64(durable))
	return s
}

// link extends the chain by one record, by its hot fields as the deliverer
// reads it back without the cold part.
func link(h [32]byte, r event.Record) [32]byte {
	r.Cold = ""
	b, _ := json.Marshal(r)
	d := sha256.New()
	d.Write(h[:])
	d.Write(b)
	var out [32]byte
	d.Sum(out[:0])
	return out
}

// thin drops every other anchor, keeping the last.
func thin(as []anchor) []anchor {
	out := as[:0]
	for i := (len(as) - 1) % 2; i < len(as); i += 2 {
		out = append(out, as[i])
	}
	return out
}

func (s *live) ID() ID               { return s.id }
func (s *live) Header() Header       { return s.header }
func (s *live) Scope() scope.Key     { return s.key }
func (s *live) Durable() Offset      { return Offset(s.durable.Load()) }
func (s *live) String() string       { return fmt.Sprintf("session %q", s.id) }
func (s *live) closedErr() error     { return &ClosedError{ID: s.id, Cause: s.cause} }
func (s *live) maxBytes() int        { return s.svc.cfg.MaxEventBytes }
func (s *live) reg() *event.Registry { return s.svc.cfg.Registry }

func (s *live) Len() Offset {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Len()
}

func (s *live) History() surface.History {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.History()
}

func (s *live) RequestHeader() (surface.RequestHeader, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.RequestHeader()
}

func (s *live) ToolHistory() surface.ToolHistory {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.ToolHistory()
}

func (s *live) Open() surface.Brackets {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Open()
}

func (s *live) AsOf(fn func(at Offset)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(s.st.Len())
}

func (s *live) Read(ctx context.Context, from Offset, limit int, opts ...ReadOption) ([]event.Event, error) {
	s.mu.Lock()
	n, closed := s.st.Len(), s.closed
	s.mu.Unlock()
	if closed {
		return nil, s.closedErrLocked()
	}
	return readRange(ctx, s.w, from, limit, n, opts)
}

func (s *live) closedErrLocked() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closedErr()
}

// readRange reads [from, min(from+limit, n)) from r.
func readRange(ctx context.Context, r store.Reader, from Offset, limit int, n Offset, opts []ReadOption) ([]event.Event, error) {
	if limit < 0 {
		return nil, errorf(event.CodeInvalidEvent, "read limit %d is negative", limit)
	}
	var o readOptions
	for _, f := range opts {
		f(&o)
	}
	if from >= n {
		return nil, nil
	}
	want := int(n - from)
	if limit > 0 && limit < want {
		want = limit
	}
	recs, err := r.Read(ctx, from, want, store.ReadOptions{WithAux: o.cold})
	if err != nil {
		return nil, err
	}
	out := make([]event.Event, 0, len(recs))
	for _, rec := range recs {
		out = append(out, event.Of(rec))
	}
	return out, nil
}

func (s *live) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	rs, err := s.appendDrafts(ctx, []event.Draft{d}, false, false)
	if err != nil {
		return event.Receipt{}, err
	}
	return rs[0], nil
}

func (s *live) AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error) {
	return s.appendDrafts(ctx, ds, false, false)
}

func (s *live) AppendPluginRecord(ctx context.Context, typ string, data json.RawMessage) (event.Receipt, error) {
	owner, record, ok := event.ParsePluginType(typ)
	if !ok {
		return event.Receipt{}, errorf(event.CodeInvalidEvent, "%q is not a plugin record type", typ)
	}
	d, err := event.PluginDraft(owner, record, data)
	if err != nil {
		return event.Receipt{}, err
	}
	return s.Append(ctx, d)
}

func (s *live) CloseOpen(ctx context.Context, cause surface.Cause) ([]event.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.st.Open()
	if !b.IsOpen() {
		return nil, nil
	}
	drafts, err := surface.Closers(b, cause, s.st.Len())
	if err != nil {
		return nil, err
	}
	return s.appendLocked(ctx, drafts, true, true)
}

// appendDrafts is the append path: the drafts' parts were encoded when they were
// built; here they are admitted and size-checked outside the lock.
func (s *live) appendDrafts(ctx context.Context, ds []event.Draft, byCore, reuseTime bool) ([]event.Receipt, error) {
	if len(ds) == 0 {
		return nil, errorf(CodeEmptyGroup, "an append group needs at least one draft")
	}
	for _, d := range ds {
		if err := d.Check(byCore); err != nil {
			return nil, err
		}
		if n := len(d.Raw()) + len(d.ColdRaw()); n > s.maxBytes() {
			return nil, errorf(CodeTooLarge, "type %q is %d bytes, above the %d byte limit", d.Type(), n, s.maxBytes())
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.appendLocked(ctx, ds, byCore, reuseTime)
}

// appendLocked assigns positions and a time, plans every draft against the
// folds, hands storage one batch and only then applies the plans. Any failure
// leaves folds, storage and feed unchanged. The caller holds s.mu.
func (s *live) appendLocked(ctx context.Context, ds []event.Draft, byCore, reuseTime bool) ([]event.Receipt, error) {
	if s.closed {
		return nil, s.closedErr()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cfg := s.svc.cfg
	checkpoint := cfg.CheckpointEvery > 0 && s.sinceCP+len(ds) >= cfg.CheckpointEvery
	cloned := len(ds) > 1 || checkpoint || cfg.Folder != nil
	st := s.st
	if cloned {
		st = s.st.Clone()
	}
	t := cfg.Clock()
	if reuseTime || t < st.LastTime() {
		t = st.LastTime()
	}
	first := s.st.Len()
	recs := make([]event.Record, len(ds))
	var single surface.Plan
	for i, d := range ds {
		rec, err := d.Stamp(st.Len(), t)
		if err != nil {
			return nil, err
		}
		p, err := st.Plan(rec, byCore)
		if err != nil {
			return nil, err
		}
		if cloned {
			st.Apply(p)
		} else {
			single = p
		}
		recs[i] = rec
	}
	last := recs[len(recs)-1].Seq
	batch := store.Batch{First: first, Records: recs}
	if cfg.Folder != nil {
		for _, d := range s.fold(st, recs) {
			batch.Checkpoints = append(batch.Checkpoints, store.Checkpoint{Key: d.Key, StateVersion: d.StateVersion, Seq: last, State: d.State})
		}
	}
	if checkpoint {
		state, err := st.Checkpoint()
		if err != nil {
			return nil, err
		}
		batch.Checkpoints = append(batch.Checkpoints, store.Checkpoint{Key: surface.CheckpointKey, StateVersion: surface.CheckpointVersion, Seq: last, State: state})
	}
	if err := s.w.Append(ctx, batch); err != nil {
		switch {
		case isLeaseLost(err):
			s.failLocked(err)
		case errors.Is(err, store.ErrSeqConflict):
			err = brokenStore(s.id, "the store refused first seq %d: %v", first, err)
			s.failLocked(err)
		}
		return nil, err
	}
	if cloned {
		s.st = st
	} else {
		s.st.Apply(single)
	}
	if checkpoint {
		s.sinceCP = 0
	} else {
		s.sinceCP += len(ds)
	}
	out := make([]event.Receipt, len(recs))
	for i, r := range recs {
		out[i] = event.Receipt{Seq: r.Seq, Time: r.Time}
	}
	// Nothing after the store accepted the batch can fail: the chain is
	// extended in memory and the deliverer woken.
	h := s.head
	for _, r := range recs {
		h = link(h, r)
	}
	s.head = h
	s.dmu.Lock()
	s.anchors = append(s.anchors, anchor{seq: last + 1, h: h})
	if len(s.anchors) > anchorLimit {
		s.anchors = thin(s.anchors)
	}
	s.target = s.st.Len()
	s.dcond.Broadcast()
	s.dmu.Unlock()
	return out, nil
}

// classify prefers a lost lease or a deleted session over a broken-store
// reading: a takeover discards the displaced writer's undurable tail, so a short
// or failed read is the lost lease when the writer no longer holds it.
func (s *live) classify(ctx context.Context, err error) error {
	if ferr := s.w.Flush(ctx); ferr != nil && (isLeaseLost(ferr) || errors.Is(ferr, store.ErrDeleted)) {
		return ferr
	}
	return err
}

// fold calls the Folder over the batch with a frozen view of the folds after it;
// a panic is contained and drops the rows.
func (s *live) fold(st *surface.State, recs []event.Record) (rows []Derived) {
	defer func() {
		if r := recover(); r != nil {
			s.svc.base.Logger().Error(fmt.Errorf("session %q: the folder panicked: %v", s.id, r))
			rows = nil
		}
	}()
	batch := make([]event.Event, len(recs))
	for i, r := range recs {
		r.Cold = ""
		batch[i] = event.Of(r)
	}
	view := &readSession{id: s.id, header: s.header, key: s.key, r: s.w, st: st, stored: st.Len(), durable: s.Durable()}
	return s.svc.cfg.Folder.Fold(view, batch)
}

// failLocked closes the session with cause; the deliverer stops at once and
// finishes the close. The caller holds s.mu.
func (s *live) failLocked(cause error) {
	if s.cause == nil {
		s.cause = cause
	}
	s.closed = true
	s.dmu.Lock()
	s.stop = stopNow
	s.dcond.Broadcast()
	s.dmu.Unlock()
}

func (s *live) Flush(ctx context.Context) (Offset, error) {
	s.mu.Lock()
	if s.closed {
		defer s.mu.Unlock()
		return s.Durable(), s.closedErr()
	}
	at := s.st.Len()
	s.mu.Unlock()
	if err := s.w.Flush(ctx); err != nil {
		if isLeaseLost(err) {
			s.mu.Lock()
			s.failLocked(err)
			s.mu.Unlock()
		}
		return s.Durable(), err
	}
	for {
		old := s.durable.Load()
		if uint64(at) <= old || s.durable.CompareAndSwap(old, uint64(at)) {
			break
		}
	}
	return s.Durable(), nil
}

func (s *live) NextRequest() (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := surface.Build(s.st, string(s.id))
	if err != nil {
		return Request{}, err
	}
	return Request{b: b}, nil
}

func (s *live) RequestFrom(seq event.Seq) (Request, error) {
	ctx := context.Background()
	s.mu.Lock()
	if s.closed {
		defer s.mu.Unlock()
		return Request{}, s.closedErr()
	}
	if seq >= s.st.Len() {
		defer s.mu.Unlock()
		return Request{}, errorf(CodeNotCommitted, "seq %d is not a committed event; the log ends at %d", seq, s.st.Len())
	}
	recs, err := s.w.ReadSeqs(ctx, []event.Seq{seq}, store.ReadOptions{WithAux: true})
	if err != nil {
		s.mu.Unlock()
		return Request{}, err
	}
	if len(recs) != 1 || recs[0].Seq != seq {
		s.mu.Unlock()
		return Request{}, brokenStore(s.id, "a read of seq %d returned %d records", seq, len(recs))
	}
	rec := recs[0]
	if !surface.HasBuilder(rec.Type) {
		s.mu.Unlock()
		return Request{}, errorf(CodeNoBuilder, "type %q at seq %d declares no request builder", rec.Type, seq)
	}
	if seq+1 == s.st.Len() {
		defer s.mu.Unlock()
		b, err := surface.BuildFrom(s.st, string(s.id), rec)
		if err != nil {
			return Request{}, err
		}
		return Request{b: b}, nil
	}
	s.mu.Unlock()
	b, err := surface.RebuildFrom(ctx, s.reg(), readFunc(s.w), string(s.id), seq)
	if err != nil {
		return Request{}, err
	}
	return Request{b: b}, nil
}

func readFunc(r store.Reader) surface.ReadFunc {
	return func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return r.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
}

// Close delivers every committed event, writes a checkpoint of the core folds,
// releases the lease and then notifies Closed. It is idempotent, and returns the
// cause Closed carries: nil for an ordinary close, else what ended the session
// (a lost lease, a broken store, or a writer that failed to flush or release).
func (s *live) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		if n := s.st.Len(); n > 0 && s.sinceCP > 0 {
			if state, err := s.st.Checkpoint(); err == nil {
				cp := store.Checkpoint{Key: surface.CheckpointKey, StateVersion: surface.CheckpointVersion, Seq: n - 1, State: state}
				if err := s.w.Append(ctx, store.Batch{First: n, Checkpoints: []store.Checkpoint{cp}}); err != nil {
					s.svc.base.Logger().Error(fmt.Errorf("session %q: the closing checkpoint: %w", s.id, err))
				} else {
					s.sinceCP = 0
				}
			}
		}
		s.dmu.Lock()
		if s.stop == running {
			s.stop = drain
		}
		s.dcond.Broadcast()
		s.dmu.Unlock()
	}
	s.mu.Unlock()
	select {
	case <-s.finished:
		return s.endErr()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// deliver is the deliverer: it reads committed records forward from storage in
// pages and hands each to the Committed listeners once, in order, outside the
// append lock. It delivers a segment only after re-deriving the chain over it
// from the very read it delivers and matching the anchor that ends it; a
// broken clause or a mismatch closes the session with a classified error and
// nothing of the bad segment is delivered.
func (s *live) deliver() {
	defer s.finish()
	ctx := context.Background()
	page := s.svc.cfg.FeedPage
	for {
		s.dmu.Lock()
		for s.next >= s.target && s.stop == running {
			s.dcond.Wait()
		}
		if s.stop == stopNow || s.next >= s.target {
			s.dmu.Unlock()
			return
		}
		from, target := s.next, s.target
		a := s.anchors[0]
		if len(s.refine) > 0 && s.refine[0].seq < a.seq {
			a = s.refine[0]
		}
		s.dmu.Unlock()

		if int(a.seq-from) > page {
			marks, err := s.split(ctx, from, target, a, page)
			if err != nil {
				s.fail(ctx, err)
				return
			}
			s.dmu.Lock()
			s.refine = append(marks, s.refine...)
			s.dmu.Unlock()
			continue
		}
		recs, err := s.scan(ctx, from, target, a, 0, true, nil)
		if err != nil {
			s.fail(ctx, err)
			return
		}
		for _, r := range recs {
			Committed.Notify(s.svc.base, s.key, CommittedArgs{Session: s, Event: event.Of(r)})
			s.dmu.Lock()
			s.next = r.Seq + 1
			stop := s.stop == stopNow
			s.dmu.Unlock()
			if stop {
				return
			}
		}
		s.at = a.h
		s.dmu.Lock()
		s.anchors = dropThrough(s.anchors, a.seq)
		s.refine = dropThrough(s.refine, a.seq)
		s.dmu.Unlock()
	}
}

// split scans the long segment [from, a.seq) without keeping it and returns
// anchors at most refineFanout apart; they hold only if the scan matched a.
func (s *live) split(ctx context.Context, from, target event.Seq, a anchor, page int) ([]anchor, error) {
	stride := max(event.Seq(page), (a.seq-from+event.Seq(refineFanout)-1)/event.Seq(refineFanout))
	var marks []anchor
	_, err := s.scan(ctx, from, target, a, stride, false, func(m anchor) { marks = append(marks, m) })
	return marks, err
}

// scan reads [from, a.seq) page by page, checks the storage clauses, and
// re-derives the chain from s.at; it fails unless the chain matches a. With
// keep it returns the records; with stride it reports the chain every stride
// records to mark.
func (s *live) scan(ctx context.Context, from, target event.Seq, a anchor, stride event.Seq, keep bool, mark func(anchor)) ([]event.Record, error) {
	var out []event.Record
	h := s.at
	page := s.svc.cfg.FeedPage
	for pos := from; pos < a.seq; {
		recs, err := s.w.Read(ctx, pos, min(page, int(a.seq-pos)), store.ReadOptions{})
		if err != nil {
			return nil, err
		}
		if len(recs) == 0 {
			return nil, brokenStore(s.id, "a read from %d returned nothing although %d records are committed (a stale tail)", pos, target)
		}
		for i, r := range recs {
			if pos >= a.seq {
				break // more than asked for
			}
			switch {
			case r.Seq >= target:
				return nil, brokenStore(s.id, "a read returned seq %d, past the committed end %d", r.Seq, target)
			case i == 0 && r.Seq != pos:
				return nil, brokenStore(s.id, "a read from %d began at seq %d", pos, r.Seq)
			case r.Seq != pos:
				return nil, brokenStore(s.id, "a read has a gap: seq %d where %d was expected", r.Seq, pos)
			}
			h = link(h, r)
			pos++
			if keep {
				out = append(out, r)
			}
			if mark != nil && pos < a.seq && (pos-from)%stride == 0 {
				mark(anchor{seq: pos, h: h})
			}
		}
	}
	if h != a.h {
		return nil, brokenStore(s.id, "the records in [%d, %d) are not the ones this session committed", from, a.seq)
	}
	return out, nil
}

// dropThrough drops the anchors at or below seq.
func dropThrough(as []anchor, seq event.Seq) []anchor {
	i := 0
	for i < len(as) && as[i].seq <= seq {
		i++
	}
	return as[i:]
}

// fail closes the session from the deliverer with err, classified.
func (s *live) fail(ctx context.Context, err error) {
	err = s.classify(ctx, err)
	s.mu.Lock()
	s.failLocked(err)
	s.mu.Unlock()
}

// finish completes a close: the lease is released, the session leaves the
// service and Closed is notified. A writer that fails to close (its flush or
// lease release failed) becomes the close's cause unless an earlier failure is.
func (s *live) finish() {
	werr := s.w.Close(context.Background())
	s.svc.remove(s)
	s.mu.Lock()
	if werr != nil && s.cause == nil {
		s.cause = fmt.Errorf("session %q: closing the writer: %w", s.id, werr)
	}
	cause := s.cause
	s.mu.Unlock()
	Closed.Notify(s.svc.base, s.key, ClosedArgs{ID: s.id, Cause: cause})
	close(s.finished)
}

// endErr is the cause the session ended with, nil for an ordinary close; Close
// returns it and Closed carries it.
func (s *live) endErr() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cause
}
