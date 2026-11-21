// Package memstore is the in-memory reference store.Store (DH-D-STORE-1). It keeps
// batches in memory and models "accepted" and "durable" as two counters per
// session, so a test can simulate a crash between them with Crash. It is for
// tests only; no production package imports it.
package memstore

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// Option configures a Store.
type Option func(*Store)

// WithRegistry judges opens against r instead of event.Default().
func WithRegistry(r *event.Registry) Option { return func(s *Store) { s.reg = r } }

// WithClock sets the clock that stamps CreatedAt when a header has none, and
// LastEventAt when a batch's records have no time.
func WithClock(now func() time.Time) Option { return func(s *Store) { s.now = now } }

// Store is the in-memory store.
type Store struct {
	reg      *event.Registry
	now      func() time.Time
	instance string

	mu       sync.Mutex
	sessions map[string]*session
	// gen counts crashes; a handle opened under an older gen died with its process.
	gen uint64
}

type ckKey struct {
	key     string
	version int
}

type session struct {
	header      store.Header
	records     []store.Record
	durable     int
	epoch       uint64
	holder      *writer
	since       time.Time
	checkpoints map[ckKey]store.Checkpoint
	durableCk   map[ckKey]store.Checkpoint
	deleted     bool
	lastEventAt time.Time
	// durableAt is lastEventAt as of the last point durable caught up with
	// records; a rollback falls back to it when the surviving last record has no
	// time of its own.
	durableAt time.Time
}

// markDurable makes every accepted record and checkpoint durable; s.mu held.
func (ss *session) markDurable() {
	ss.durable = len(ss.records)
	ss.durableCk = maps(ss.checkpoints)
	ss.durableAt = ss.lastEventAt
}

// rollback discards the accepted, undurable records and checkpoints and puts
// lastEventAt back to that of the surviving last record; s.mu held.
func (ss *session) rollback() {
	if len(ss.records) == ss.durable {
		ss.checkpoints = maps(ss.durableCk)
		return
	}
	ss.records = ss.records[:ss.durable]
	ss.checkpoints = maps(ss.durableCk)
	switch n := len(ss.records); {
	case n == 0:
		ss.lastEventAt = time.Time{}
	case ss.records[n-1].Time > 0:
		ss.lastEventAt = time.UnixMilli(ss.records[n-1].Time)
	default:
		ss.lastEventAt = ss.durableAt
	}
}

// New is an empty store.
func New(opts ...Option) *Store {
	s := &Store{reg: event.Default(), now: time.Now, sessions: map[string]*session{},
		instance: "mem-" + strconv.Itoa(os.Getpid())}
	for _, o := range opts {
		o(s)
	}
	return s
}

var _ store.Store = (*Store)(nil)

func (s *Store) holderOf(ss *session) store.Holder {
	return store.Holder{Instance: s.instance, PID: os.Getpid(), Kind: "memstore", Since: ss.since}
}

// Create implements store.Store.
func (s *Store) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if h.ID == "" {
		return nil, store.Errorf(store.CodeInvalidBatch, "a session needs an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[h.ID]; ok {
		return nil, store.Errorf(store.CodeExists, "session %q exists", h.ID)
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = s.now()
	}
	ss := &session{checkpoints: map[ckKey]store.Checkpoint{}, durableCk: map[ckKey]store.Checkpoint{}}
	if f := o.ForkOf; f != nil {
		p, ok := s.sessions[f.ID]
		if !ok {
			return nil, store.Errorf(store.CodeNotFound, "fork parent %q", f.ID)
		}
		if f.Through >= store.Seq(len(p.records)) {
			return nil, store.Errorf(store.CodeInvalidBoundary, "fork of %q through seq %d, which has %d records", f.ID, f.Through, len(p.records))
		}
		inherited := p.records[:f.Through+1]
		if err := store.CheckOpen(s.reg, inherited); err != nil {
			return nil, err
		}
		// The child's copy is durable, so the parent's inherited prefix must be
		// too: otherwise a crash or takeover could drop those seqs from the parent
		// and let it rewrite them, and the child would no longer match its
		// ForkSource/ForkThrough. A store may make records durable early.
		if n := len(inherited); p.durable < n {
			p.durable = n
			if n == len(p.records) {
				p.durableAt = p.lastEventAt
			}
		}
		for _, r := range inherited {
			ss.records = append(ss.records, r.Clone())
		}
		ss.durable = len(ss.records)
		if ms := inherited[len(inherited)-1].Time; ms > 0 {
			ss.lastEventAt = time.UnixMilli(ms)
		} else {
			ss.lastEventAt = p.lastEventAt
		}
		ss.durableAt = ss.lastEventAt
		h.ForkSource, h.ForkThrough, h.InheritedCount = f.ID, f.Through, len(ss.records)
	}
	ss.header = h
	s.sessions[h.ID] = ss
	return s.grant(ss), nil
}

// grant gives ss a new writer under a raised epoch; s.mu held.
func (s *Store) grant(ss *session) *writer {
	ss.epoch++
	w := &writer{reader: reader{s: s, id: ss.header.ID, header: ss.header, ss: ss, gen: s.gen}, epoch: ss.epoch}
	ss.holder = w
	ss.since = s.now()
	return w
}

// OpenWrite implements store.Store.
func (s *Store) OpenWrite(ctx context.Context, id string, o store.OpenOptions) (store.Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.live(id)
	if err != nil {
		return nil, err
	}
	if ss.holder != nil && o.Takeover != store.Force {
		return nil, &store.OwnedError{ID: id, Holder: s.holderOf(ss)}
	}
	if err := store.CheckOpen(s.reg, ss.records); err != nil {
		return nil, err
	}
	if ss.holder != nil {
		// The displaced writer's accepted, undurable records are discarded: they
		// were never durable.
		ss.rollback()
		ss.holder = nil
	}
	return s.grant(ss), nil
}

// OpenRead implements store.Store.
func (s *Store) OpenRead(ctx context.Context, id string) (store.Reader, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.live(id)
	if err != nil {
		return nil, err
	}
	if err := store.CheckOpen(s.reg, ss.records); err != nil {
		return nil, err
	}
	return &reader{s: s, id: id, header: ss.header, ss: ss, gen: s.gen}, nil
}

func (s *Store) live(id string) (*session, error) {
	ss, ok := s.sessions[id]
	if !ok || ss.deleted {
		return nil, store.Errorf(store.CodeNotFound, "session %q", id)
	}
	return ss, nil
}

func (s *Store) summary(ss *session) store.Summary {
	sum := store.Summary{Header: ss.header, NextSeq: store.Seq(len(ss.records)), Durable: store.Seq(ss.durable),
		LeaseEpoch: ss.epoch, LastEventAt: ss.lastEventAt}
	for i := len(ss.records) - 1; i >= 0; i-- {
		if r := ss.records[i]; r.Type == store.PromptType && r.Time > 0 {
			sum.LastPromptAt = time.UnixMilli(r.Time)
			break
		}
	}
	if ss.holder != nil {
		h := s.holderOf(ss)
		sum.Holder = &h
	}
	return sum
}

// Stat implements store.Store.
func (s *Store) Stat(ctx context.Context, id string) (store.Summary, error) {
	if err := ctx.Err(); err != nil {
		return store.Summary{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.live(id)
	if err != nil {
		return store.Summary{}, err
	}
	return s.summary(ss), nil
}

const defaultLimit = 50

// List implements store.Store.
func (s *Store) List(ctx context.Context, q store.Query) (store.Page, error) {
	if err := ctx.Err(); err != nil {
		return store.Page{}, err
	}
	limit := q.PageLimit(defaultLimit)
	if q.Order == store.OrderPrompt {
		return s.listByPrompt(q, limit)
	}
	var after *cursor
	if q.Cursor != "" {
		c, err := parseCursor(q.Cursor)
		if err != nil {
			return store.Page{}, err
		}
		after = &c
	}
	s.mu.Lock()
	var all []store.Summary
	for _, ss := range s.sessions {
		if ss.deleted || (q.Cwd != "" && ss.header.Cwd != q.Cwd) || (q.Creator != "" && ss.header.Creator != q.Creator) {
			continue
		}
		all = append(all, s.summary(ss))
	}
	s.mu.Unlock()
	slices.SortFunc(all, func(a, b store.Summary) int { return compare(cursorOf(a), cursorOf(b)) })
	var page store.Page
	for _, sum := range all {
		if after != nil && compare(cursorOf(sum), *after) <= 0 {
			continue
		}
		if len(page.Summaries) == limit {
			page.Next = cursorOf(page.Summaries[limit-1]).String()
			break
		}
		page.Summaries = append(page.Summaries, sum)
	}
	return page, nil
}

// listByPrompt is List in store.OrderPrompt: every session's key is computed
// from its records as of the cut.
func (s *Store) listByPrompt(q store.Query, limit int) (store.Page, error) {
	var after *store.PromptCursor
	cut := q.AsOf
	if q.Cursor != "" {
		c, err := store.ParsePromptCursor(q.Cursor)
		if err != nil {
			return store.Page{}, err
		}
		after, cut = &c, time.Unix(0, c.AsOf)
	}
	if cut.IsZero() {
		cut = s.now()
	}
	type keyed struct {
		key int64
		sum store.Summary
	}
	s.mu.Lock()
	var all []keyed
	for _, ss := range s.sessions {
		if ss.deleted || (q.Cwd != "" && ss.header.Cwd != q.Cwd) || (q.Creator != "" && ss.header.Creator != q.Creator) {
			continue
		}
		key, ok := store.PromptKey(ss.header.CreatedAt, ss.records, cut.UnixNano())
		if ok && (after == nil || after.Precedes(key, ss.header.ID)) {
			all = append(all, keyed{key, s.summary(ss)})
		}
	}
	s.mu.Unlock()
	slices.SortFunc(all, func(a, b keyed) int {
		if c := cmp.Compare(b.key, a.key); c != 0 {
			return c
		}
		return strings.Compare(a.sum.Header.ID, b.sum.Header.ID)
	})
	var page store.Page
	for i, k := range all {
		if i == limit {
			last := all[limit-1]
			page.Next = store.PromptCursor{AsOf: cut.UnixNano(), Key: last.key, ID: last.sum.Header.ID}.String()
			break
		}
		page.Summaries = append(page.Summaries, k.sum)
	}
	return page, nil
}

// cursor is a List position: the sort key of the last summary returned.
type cursor struct {
	last, created int64
	id            string
}

func cursorOf(s store.Summary) cursor {
	return cursor{last: s.LastEventAt.UnixNano(), created: s.Header.CreatedAt.UnixNano(), id: s.Header.ID}
}

func (c cursor) String() string { return fmt.Sprintf("%d.%d.%s", c.last, c.created, c.id) }

func parseCursor(v string) (cursor, error) {
	parts := strings.SplitN(v, ".", 3)
	if len(parts) == 3 {
		last, err1 := strconv.ParseInt(parts[0], 10, 64)
		created, err2 := strconv.ParseInt(parts[1], 10, 64)
		if err1 == nil && err2 == nil {
			return cursor{last: last, created: created, id: parts[2]}, nil
		}
	}
	return cursor{}, store.Errorf(store.CodeInvalidBoundary, "list cursor %q is not one this store issued", v)
}

// compare orders the most recent first, then by id.
func compare(a, b cursor) int {
	if c := cmp.Compare(b.last, a.last); c != 0 {
		return c
	}
	if c := cmp.Compare(b.created, a.created); c != 0 {
		return c
	}
	return strings.Compare(a.id, b.id)
}

// Delete implements store.Store.
func (s *Store) Delete(ctx context.Context, id string, _ store.DeleteOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ss, err := s.live(id)
	if err != nil {
		return err
	}
	if ss.holder != nil {
		return &store.OwnedError{ID: id, Holder: s.holderOf(ss)}
	}
	ss.deleted = true
	ss.records = nil
	delete(s.sessions, id)
	return nil
}

// Flush implements store.Store: every session's accepted records become durable.
func (s *Store) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.sessions {
		ss.markDurable()
	}
	return nil
}

// Crash simulates the death of the process: every accepted, undurable record and
// checkpoint is lost, LastEventAt falls back to the surviving last record, every
// lease is released, and every writer and reader opened before the crash fails
// with session/closed.
func (s *Store) Crash() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ss := range s.sessions {
		ss.rollback()
		if ss.holder != nil {
			ss.holder.crashed = true
			ss.holder = nil
		}
	}
	s.gen++
	s.instance += "+"
}

// Seed stores a durable, unheld session holding recs as they are, without the
// admission rule an Append applies: it stands in for rows another build wrote, so
// a test can prove an open judges them (storetest.Seeder). It is for fixtures.
func (s *Store) Seed(ctx context.Context, h store.Header, recs []store.Record) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if h.ID == "" {
		return store.Errorf(store.CodeInvalidBatch, "a session needs an id")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[h.ID]; ok {
		return store.Errorf(store.CodeExists, "session %q exists", h.ID)
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = s.now()
	}
	ss := &session{header: h, checkpoints: map[ckKey]store.Checkpoint{}, durableCk: map[ckKey]store.Checkpoint{}}
	for _, r := range recs {
		ss.records = append(ss.records, r.Clone())
	}
	ss.durable = len(ss.records)
	if n := len(recs); n > 0 && recs[n-1].Time > 0 {
		ss.lastEventAt = time.UnixMilli(recs[n-1].Time)
	}
	ss.durableAt = ss.lastEventAt
	s.sessions[h.ID] = ss
	return nil
}

func maps(m map[ckKey]store.Checkpoint) map[ckKey]store.Checkpoint {
	out := make(map[ckKey]store.Checkpoint, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

type reader struct {
	s      *Store
	id     string
	header store.Header
	ss     *session
	gen    uint64 // the store's crash count when the handle was opened
	closed bool   // under s.mu
}

func (r *reader) Header() store.Header { return r.header }

// session is the reader's session; s.mu held.
func (r *reader) session() (*session, error) {
	if r.gen != r.s.gen {
		return nil, store.Errorf(store.CodeClosed, "reader of %q died with its process", r.id)
	}
	if r.closed {
		return nil, store.Errorf(store.CodeClosed, "reader of %q is closed", r.id)
	}
	ss := r.ss
	if ss.deleted {
		return nil, store.Errorf(store.CodeDeleted, "session %q was deleted", r.id)
	}
	return ss, nil
}

func view(rec store.Record, o store.ReadOptions) store.Record {
	rec = rec.Clone()
	if !o.WithAux {
		rec.Cold = ""
	}
	return rec
}

func (r *reader) Read(ctx context.Context, from store.Seq, limit int, o store.ReadOptions) ([]store.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, store.Errorf(store.CodeInvalidBoundary, "read limit %d is negative", limit)
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	ss, err := r.session()
	if err != nil {
		return nil, err
	}
	if from >= store.Seq(len(ss.records)) {
		return nil, nil
	}
	start, end := int(from), len(ss.records)
	if limit > 0 && limit < end-start {
		end = start + limit
	}
	out := make([]store.Record, 0, end-start)
	for _, rec := range ss.records[from:end] {
		out = append(out, view(rec, o))
	}
	return out, nil
}

func (r *reader) ReadSeqs(ctx context.Context, seqs []store.Seq, o store.ReadOptions) ([]store.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	ss, err := r.session()
	if err != nil {
		return nil, err
	}
	out := make([]store.Record, 0, len(seqs))
	for _, q := range seqs {
		if q >= store.Seq(len(ss.records)) {
			return nil, store.Errorf(store.CodeInvalidBoundary, "session %q has no seq %d", r.id, q)
		}
		out = append(out, view(ss.records[q], o))
	}
	return out, nil
}

func (r *reader) Checkpoints(ctx context.Context) ([]store.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.s.mu.Lock()
	defer r.s.mu.Unlock()
	ss, err := r.session()
	if err != nil {
		return nil, err
	}
	out := make([]store.Checkpoint, 0, len(ss.checkpoints))
	for _, c := range ss.checkpoints {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b store.Checkpoint) int {
		return cmp.Or(strings.Compare(a.Key, b.Key), cmp.Compare(a.StateVersion, b.StateVersion))
	})
	return out, nil
}

func (r *reader) Close(context.Context) error {
	r.s.mu.Lock()
	r.closed = true
	r.s.mu.Unlock()
	return nil
}

type writer struct {
	reader
	epoch   uint64
	crashed bool // under s.mu
}

func (w *writer) Epoch() uint64 { return w.epoch }

// check is the guard of an append or flush; s.mu held.
func (w *writer) check() (*session, error) {
	if w.crashed || w.gen != w.s.gen {
		return nil, store.Errorf(store.CodeClosed, "writer of %q died with its process", w.id)
	}
	if w.closed {
		return nil, store.Errorf(store.CodeClosed, "writer of %q is closed", w.id)
	}
	ss := w.ss
	if ss.deleted {
		return nil, store.Errorf(store.CodeDeleted, "session %q was deleted", w.id)
	}
	if ss.holder != w || ss.epoch != w.epoch {
		return nil, store.Errorf(store.CodeLeaseLost, "session %q: epoch %d was taken over (now %d)", w.id, w.epoch, ss.epoch)
	}
	return ss, nil
}

func (w *writer) Append(ctx context.Context, b store.Batch) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	ss, err := w.check()
	if err != nil {
		return err
	}
	if b.First != store.Seq(len(ss.records)) {
		return store.Errorf(store.CodeSeqConflict, "session %q: batch starts at %d, next seq is %d", w.id, b.First, len(ss.records))
	}
	last := int64(0)
	if n := len(ss.records); n > 0 {
		last = ss.records[n-1].Time
	}
	for i, rec := range b.Records {
		if rec.Seq != b.First+store.Seq(i) {
			return store.Errorf(store.CodeSeqConflict, "session %q: record %d of the batch has seq %d, want %d", w.id, i, rec.Seq, b.First+store.Seq(i))
		}
		if rec.Type == "" {
			return store.Errorf(store.CodeInvalidBatch, "session %q: record at seq %d has no type", w.id, rec.Seq)
		}
		if err := store.Admit(w.s.reg, rec); err != nil {
			return err
		}
		if rec.Time < last {
			return store.Errorf(store.CodeInvalidBatch, "session %q: record at seq %d goes back in time", w.id, rec.Seq)
		}
		last = rec.Time
	}
	total := len(ss.records) + len(b.Records)
	for _, c := range b.Checkpoints {
		if c.Key == "" || c.Seq >= store.Seq(total) {
			return store.Errorf(store.CodeInvalidBatch, "session %q: checkpoint %q at seq %d is outside the log", w.id, c.Key, c.Seq)
		}
	}
	for _, rec := range b.Records {
		ss.records = append(ss.records, rec.Clone())
	}
	for _, c := range b.Checkpoints {
		ss.checkpoints[ckKey{c.Key, c.StateVersion}] = c
	}
	if n := len(b.Records); n > 0 {
		if ms := b.Records[n-1].Time; ms > 0 {
			ss.lastEventAt = time.UnixMilli(ms)
		} else {
			ss.lastEventAt = w.s.now()
		}
	}
	return nil
}

func (w *writer) Flush(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	ss, err := w.check()
	if err != nil {
		return err
	}
	ss.markDurable()
	return nil
}

// Close flushes, releases the lease and keeps the epoch. A writer that lost its
// lease closes without error and changes nothing.
func (w *writer) Close(context.Context) error {
	w.s.mu.Lock()
	defer w.s.mu.Unlock()
	if w.closed {
		return nil
	}
	if ss, err := w.check(); err == nil {
		ss.markDurable()
		ss.holder = nil
	}
	w.closed = true
	return nil
}
