package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

	dmu      sync.Mutex
	dcond    *sync.Cond
	target   event.Seq            // the next seq once committed; the deliverer's goal
	next     event.Seq            // the deliverer's cursor
	fps      map[event.Seq]string // fingerprints of committed, undelivered records
	stop     int
	finished chan struct{}
}

func newLive(v *service, id ID, h Header, key scope.Key, w store.Writer, st *surface.State, cursor, durable event.Seq) *live {
	s := &live{svc: v, id: id, header: h, key: key, w: w, st: st,
		target: st.Len(), next: cursor, fps: map[event.Seq]string{}, finished: make(chan struct{})}
	s.dcond = sync.NewCond(&s.dmu)
	s.durable.Store(uint64(durable))
	return s
}

// fingerprint identifies a committed record by its hot fields, as the deliverer
// reads it back without the cold part.
func fingerprint(r event.Record) string {
	r.Cold = ""
	b, _ := json.Marshal(r)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
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
	s.dmu.Lock()
	for i, r := range recs {
		out[i] = event.Receipt{Seq: r.Seq, Time: r.Time}
		s.fps[r.Seq] = fingerprint(r)
	}
	s.target = s.st.Len()
	s.dcond.Broadcast()
	s.dmu.Unlock()
	return out, nil
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
// releases the lease and then notifies Closed. It is idempotent.
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
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// deliver is the deliverer: it reads committed records forward from storage in
// pages and hands each to the Committed listeners once, in order, outside the
// append lock. Before delivering a page it checks the storage clauses the core
// relies on; a broken one closes the session with a classified error and
// nothing of the bad page is delivered.
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
		s.dmu.Unlock()

		recs, err := s.w.Read(ctx, from, min(page, int(target-from)), store.ReadOptions{})
		if err == nil {
			err = s.check(from, target, recs)
		}
		if err != nil {
			// A takeover discards the displaced writer's undurable tail, so a short
			// or failed read is the lost lease when the writer no longer holds it.
			if ferr := s.w.Flush(ctx); ferr != nil && (isLeaseLost(ferr) || errors.Is(ferr, store.ErrDeleted)) {
				err = ferr
			}
			s.mu.Lock()
			s.failLocked(err)
			s.mu.Unlock()
			return
		}
		for _, r := range recs {
			Committed.Notify(s.svc.base, s.key, CommittedArgs{Session: s, Event: event.Of(r)})
			s.dmu.Lock()
			delete(s.fps, r.Seq)
			s.next = r.Seq + 1
			stop := s.stop == stopNow
			s.dmu.Unlock()
			if stop {
				return
			}
		}
	}
}

// check is the clauses of one feed read: it starts at from, has no gap, is not
// short, and each record is the one this session committed.
func (s *live) check(from, target event.Seq, recs []event.Record) error {
	if len(recs) == 0 {
		return brokenStore(s.id, "a read from %d returned nothing although %d records are committed (a stale tail)", from, target)
	}
	s.dmu.Lock()
	defer s.dmu.Unlock()
	for i, r := range recs {
		want := from + event.Seq(i)
		switch {
		case i == 0 && r.Seq != want:
			return brokenStore(s.id, "a read from %d began at seq %d", from, r.Seq)
		case r.Seq != want:
			return brokenStore(s.id, "a read has a gap: seq %d where %d was expected", r.Seq, want)
		case r.Seq >= target:
			return brokenStore(s.id, "a read returned seq %d, past the committed end %d", r.Seq, target)
		case fingerprint(r) != s.fps[r.Seq]:
			return brokenStore(s.id, "the record at seq %d is not the one this session committed", r.Seq)
		}
	}
	return nil
}

// finish completes a close: the lease is released, the session leaves the
// service and Closed is notified.
func (s *live) finish() {
	_ = s.w.Close(context.Background())
	s.svc.remove(s)
	s.mu.Lock()
	cause := s.cause
	s.mu.Unlock()
	Closed.Notify(s.svc.base, s.key, ClosedArgs{ID: s.id, Cause: cause})
	close(s.finished)
}
