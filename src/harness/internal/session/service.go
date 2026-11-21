package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// Service owns every live session of this process. Fork (DH-D-SESSION-7) and
// AppendLogOnly (DH-D-SESSION-9) are later steps and not yet members.
type Service interface {
	// Prepare builds and leases a session that no one can see; Publish or
	// Discard it.
	Prepare(ctx context.Context, owner *cordis.Context, o CreateOptions) (*Pending, error)
	// Create is Prepare and Publish.
	Create(ctx context.Context, owner *cordis.Context, o CreateOptions) (Handle, error)
	// Open takes the write lease and closes an interrupted turn (ADR-8).
	Open(ctx context.Context, owner *cordis.Context, id ID, o OpenOptions) (Handle, OpenReport, error)
	// OpenRead folds what is committed, without the lease; an interrupted turn's
	// closers are folded in memory and written nowhere. The func releases it.
	OpenRead(ctx context.Context, id ID) (ReadSession, func(), error)
	// Get is a session live in this process.
	Get(id ID) (Session, bool)
	// Live is every live session, in creation order; a copy.
	Live() []Session
}

// Config tunes a Service. Zero fields take the defaults.
type Config struct {
	// Registry is the event vocabulary; nil is event.Default().
	Registry *event.Registry
	// Clock is the time in Unix milliseconds; nil is the wall clock.
	Clock func() int64
	// MaxEventBytes bounds one event's encoded parts; 0 is 8 MiB.
	MaxEventBytes int
	// CheckpointEvery is how many events pass between checkpoints of the core
	// folds; 0 is 500 and a negative value disables them (Close still writes one).
	CheckpointEvery int
	// FeedPage is how many records the deliverer reads per call; 0 is 256.
	FeedPage int
	// Folder is A06's fold hook; nil is none.
	Folder Folder
}

// The defaults.
const (
	DefaultMaxEventBytes   = 8 << 20
	DefaultCheckpointEvery = 500
	DefaultFeedPage        = 256
)

type service struct {
	base  *cordis.Context
	store store.Store
	cfg   Config

	mu    sync.Mutex
	live  map[ID]*live
	order []*live
}

// NewService is a Service over st whose live events are dispatched through base.
func NewService(base *cordis.Context, st store.Store, cfg Config) Service {
	if cfg.Registry == nil {
		cfg.Registry = event.Default()
	}
	if cfg.Clock == nil {
		cfg.Clock = func() int64 { return time.Now().UnixMilli() }
	}
	if cfg.MaxEventBytes <= 0 {
		cfg.MaxEventBytes = DefaultMaxEventBytes
	}
	if cfg.CheckpointEvery == 0 {
		cfg.CheckpointEvery = DefaultCheckpointEvery
	}
	if cfg.FeedPage <= 0 {
		cfg.FeedPage = DefaultFeedPage
	}
	return &service{base: base, store: st, cfg: cfg, live: map[ID]*live{}}
}

func newID() ID {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return ID("ses_" + hex.EncodeToString(b[:]))
}

// Pending is a prepared session: built and leased, invisible until Publish.
type Pending struct {
	svc   *service
	s     *live
	owner *cordis.Context
	mu    sync.Mutex
	done  bool
}

// ID is the prepared session's id.
func (p *Pending) ID() ID { return p.s.id }

func (v *service) Prepare(ctx context.Context, owner *cordis.Context, o CreateOptions) (*Pending, error) {
	h := o.Header
	if h.ID == "" {
		h.ID = string(newID())
	}
	if h.CreatedAt.IsZero() {
		h.CreatedAt = time.UnixMilli(v.cfg.Clock()).UTC()
	}
	w, err := v.store.Create(ctx, h, store.CreateOptions{})
	if err != nil {
		return nil, err
	}
	s := newLive(v, ID(h.ID), w.Header(), o.Scope, w, surface.NewState(v.cfg.Registry), 0, 0)
	return &Pending{svc: v, s: s, owner: owner}, nil
}

// Publish makes the session visible: Created is emitted in order and its first
// error (or panic) vetoes publication, after which the session is discarded and
// Closed follows with that cause.
func (p *Pending) Publish(ctx context.Context) (Handle, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return nil, errorf(CodeClosed, "session %q was already published or discarded", p.s.id)
	}
	p.done = true
	if err := p.svc.publish(ctx, p.owner, p.s); err != nil {
		_ = p.svc.store.Delete(context.WithoutCancel(ctx), string(p.s.id), store.DeleteOptions{})
		return nil, err
	}
	return p.s, nil
}

// Discard releases the lease and deletes the prepared session.
func (p *Pending) Discard(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return nil
	}
	p.done = true
	if err := p.s.w.Close(ctx); err != nil {
		return err
	}
	return p.svc.store.Delete(ctx, string(p.s.id), store.DeleteOptions{})
}

func (v *service) Create(ctx context.Context, owner *cordis.Context, o CreateOptions) (Handle, error) {
	p, err := v.Prepare(ctx, owner, o)
	if err != nil {
		return nil, err
	}
	return p.Publish(ctx)
}

func emitCreated(base *cordis.Context, s *live) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("session: a %s listener panicked: %v", Created.Name(), r)
		}
	}()
	return Created.Emit(base, s.key, CreatedArgs{Session: s})
}

// publish emits Created and, unless vetoed, registers s, starts its deliverer
// and ties it to owner.
func (v *service) publish(ctx context.Context, owner *cordis.Context, s *live) error {
	if err := emitCreated(v.base, s); err != nil {
		_ = s.w.Close(context.WithoutCancel(ctx))
		Closed.Notify(v.base, s.key, ClosedArgs{ID: s.id, Cause: err})
		return err
	}
	v.mu.Lock()
	v.live[s.id] = s
	v.order = append(v.order, s)
	v.mu.Unlock()
	go s.deliver()
	if owner != nil {
		if _, err := owner.Effect("session "+string(s.id), func() (cordis.Disposer, error) {
			return func() error { return s.Close(context.Background()) }, nil
		}); err != nil {
			_ = s.Close(context.WithoutCancel(ctx))
			return err
		}
	}
	return nil
}

func (v *service) remove(s *live) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.live[s.id] == s {
		delete(v.live, s.id)
	}
	v.order = slices.DeleteFunc(v.order, func(o *live) bool { return o == s })
}

func (v *service) Get(id ID) (Session, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, ok := v.live[id]
	if !ok {
		return nil, false
	}
	return s, true
}

func (v *service) Live() []Session {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := make([]Session, len(v.order))
	for i, s := range v.order {
		out[i] = s
	}
	return out
}

func (v *service) Open(ctx context.Context, owner *cordis.Context, id ID, o OpenOptions) (Handle, OpenReport, error) {
	w, err := v.store.OpenWrite(ctx, string(id), store.OpenOptions{Takeover: o.Takeover})
	if err != nil {
		return nil, OpenReport{}, err
	}
	fail := func(err error) (Handle, OpenReport, error) {
		_ = w.Close(context.WithoutCancel(ctx))
		return nil, OpenReport{}, err
	}
	sum, err := v.store.Stat(ctx, string(id))
	if err != nil {
		return fail(err)
	}
	st, fromCP, err := replay(ctx, v.cfg.Registry, w, sum.NextSeq, id)
	if err != nil {
		return fail(err)
	}
	s := newLive(v, id, w.Header(), o.Scope, w, st, sum.NextSeq, sum.Durable)
	report := OpenReport{Was: st.Open(), FromCheckpoint: fromCP}
	if report.Was.IsOpen() {
		report.Interrupted = true
		drafts, err := surface.Closers(report.Was, surface.CauseInterrupted, st.Len())
		if err != nil {
			return fail(err)
		}
		seed, err := event.EndSeed.Draft(event.EndSeedPayload{Epoch: w.Epoch(), Cause: string(surface.CauseInterrupted)})
		if err != nil {
			return fail(err)
		}
		s.mu.Lock()
		report.Closers, err = s.appendLocked(ctx, append(drafts, seed), true, true)
		s.mu.Unlock()
		if err != nil {
			return fail(err)
		}
		// The closers are durable before anyone sees the session
		// (DH-D-STORE-4): a crash right after Open replays them, not the
		// open brackets again.
		if _, err := s.Flush(ctx); err != nil {
			return fail(err)
		}
	}
	if r, ok := v.cfg.Folder.(Restorer); ok {
		s.restoreFolds(ctx, r)
	}
	if err := v.publish(ctx, owner, s); err != nil {
		return nil, OpenReport{}, err
	}
	return s, report, nil
}

// replay folds a stored session from its newest usable checkpoint, or from 0,
// checking each clause the core relies on: the first seq asked for, no gap, and
// no short tail before next.
func replay(ctx context.Context, reg *event.Registry, r store.Reader, next event.Seq, id ID) (*surface.State, bool, error) {
	var st *surface.State
	cps, err := r.Checkpoints(ctx)
	if err != nil {
		return nil, false, err
	}
	for _, c := range cps {
		if c.Key != surface.CheckpointKey || c.Seq >= next {
			continue
		}
		restored, err := surface.Restore(ctx, reg, c.StateVersion, c.State, func(ctx context.Context, seqs []event.Seq) ([]event.Record, error) {
			return r.ReadSeqs(ctx, seqs, store.ReadOptions{})
		})
		if err == nil && restored.Len() == c.Seq+1 && (st == nil || restored.Len() > st.Len()) {
			st = restored
		}
	}
	fromCP := st != nil
	if st == nil {
		st = surface.NewState(reg)
	}
	for st.Len() < next {
		want := min(1024, int(next-st.Len()))
		recs, err := r.Read(ctx, st.Len(), want, store.ReadOptions{})
		if err != nil {
			return nil, false, err
		}
		if len(recs) == 0 {
			return nil, false, brokenStore(id, "the store reports %d records but the log ends at %d", next, st.Len())
		}
		for i, rec := range recs {
			if rec.Seq != st.Len() {
				if i == 0 {
					return nil, false, brokenStore(id, "a read from %d began at seq %d", st.Len(), rec.Seq)
				}
				return nil, false, brokenStore(id, "a read has a gap: seq %d after %d", rec.Seq, st.Len()-1)
			}
			if rec.Seq >= next {
				break
			}
			if err := st.Fold(rec, true); err != nil {
				return nil, false, err
			}
		}
	}
	return st, fromCP, nil
}

func (v *service) OpenRead(ctx context.Context, id ID) (ReadSession, func(), error) {
	r, err := v.store.OpenRead(ctx, string(id))
	if err != nil {
		return nil, nil, err
	}
	release := func() { _ = r.Close(context.Background()) }
	sum, err := v.store.Stat(ctx, string(id))
	if err != nil {
		release()
		return nil, nil, err
	}
	st, _, err := replay(ctx, v.cfg.Registry, r, sum.NextSeq, id)
	if err != nil {
		release()
		return nil, nil, err
	}
	rs := &readSession{id: id, header: r.Header(), r: r, st: st, stored: sum.NextSeq, durable: sum.Durable}
	if b := st.Open(); b.IsOpen() {
		drafts, err := surface.Closers(b, surface.CauseInterrupted, st.Len())
		if err == nil {
			for _, d := range drafts {
				var rec event.Record
				if rec, err = d.Stamp(st.Len(), st.LastTime()); err == nil {
					err = st.Fold(rec, true)
				}
				if err != nil {
					break
				}
				rs.extra = append(rs.extra, rec)
			}
		}
		if err != nil {
			release()
			return nil, nil, errors.Join(errors.New("session: folding the closers of a read-open"), err)
		}
	}
	return rs, release, nil
}
