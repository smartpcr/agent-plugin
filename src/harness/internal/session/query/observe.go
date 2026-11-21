package query

import (
	"context"
	"errors"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// Window is a bounded read of a log: Limit records from From (0: DefaultWindow;
// clamped to MaxWindow).
type Window struct {
	From  event.Seq
	Limit int
}

func (w Window) limit() int {
	switch {
	case w.Limit <= 0:
		return DefaultWindow
	case w.Limit > MaxWindow:
		return MaxWindow
	}
	return w.Limit
}

// Snapshot is the built-in views of a session at its head.
type Snapshot struct {
	Summary views.ListSummary
	Turns   views.TurnIndex
	Stats   views.Stats
	Index   views.EventIndex
}

// Observation is one bounded look at a session: a window of its log, its
// views at the head, and the cursor of the next window ("" at the end).
type Observation struct {
	Header store.Header
	Head   event.Seq
	Events []store.Record
	Views  Snapshot
	Next   event.Seq
	More   bool
}

// Observe is a bounded window of session id and its built-in views; it takes
// no lease, so it works while another process writes the session.
func (s *Service) Observe(ctx context.Context, id session.ID, w Window) (Observation, error) {
	r, err := s.st.OpenRead(ctx, string(id))
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = r.Close(ctx) }()
	sum, err := s.st.Stat(ctx, string(id))
	if err != nil {
		return Observation{}, err
	}
	recs, err := r.Read(ctx, w.From, w.limit(), store.ReadOptions{})
	if err != nil {
		return Observation{}, err
	}
	o := Observation{Header: r.Header(), Head: sum.NextSeq, Events: recs}
	if n := len(recs); n > 0 {
		o.Next = recs[n-1].Seq + 1
	} else {
		o.Next = max(w.From, sum.NextSeq)
	}
	o.More = o.Next < sum.NextSeq
	if o.Views, err = snapshot(ctx, r); err != nil {
		return Observation{}, err
	}
	return o, nil
}

func snapshot(ctx context.Context, r store.Reader) (Snapshot, error) {
	var sn Snapshot
	var err error
	if sn.Summary, err = stateAt(ctx, r, views.ListSummaryView(), -1); err != nil {
		return sn, err
	}
	if sn.Turns, err = stateAt(ctx, r, views.TurnIndexView(), -1); err != nil {
		return sn, err
	}
	if sn.Stats, err = stateAt(ctx, r, views.StatsView(), -1); err != nil {
		return sn, err
	}
	sn.Index, err = stateAt(ctx, r, views.EventIndexView(), -1)
	return sn, err
}

// Link is one session of a lineage.
type Link struct {
	ID session.ID
	// Through is the last position the next link down inherited from this one;
	// unused on the session itself (the last link).
	Through event.Seq
	// Gone is an ancestor the store no longer has (deleted after the fork).
	Gone bool
}

// Lineage is the fork chain of a session, root first and the session last,
// with its direct forks.
type Lineage struct {
	Chain []Link
	// Forks are the sessions forked from it, by id; bounded by MaxLineage.
	Forks []session.ID
	// Truncated is set when the chain or the forks were cut at MaxLineage.
	Truncated bool
}

// Lineage walks id's fork ancestry (at most MaxLineage links) and its direct
// forks.
func (s *Service) Lineage(ctx context.Context, id session.ID) (Lineage, error) {
	sum, err := s.st.Stat(ctx, string(id))
	if err != nil {
		return Lineage{}, err
	}
	var l Lineage
	chain := []Link{{ID: id}}
	h := sum.Header
	for h.ForkSource != "" {
		if len(chain) >= MaxLineage {
			l.Truncated = true
			break
		}
		link := Link{ID: session.ID(h.ForkSource), Through: h.ForkThrough}
		p, err := s.st.Stat(ctx, h.ForkSource)
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrDeleted) {
			link.Gone = true
			chain = append(chain, link)
			break
		}
		if err != nil {
			return Lineage{}, err
		}
		chain = append(chain, link)
		h = p.Header
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	l.Chain = chain
	q := store.Query{}
	for walked := 0; walked < MaxListed; {
		p, err := s.st.List(ctx, q)
		if err != nil {
			return Lineage{}, err
		}
		for _, c := range p.Summaries {
			if c.Header.ForkSource != string(id) {
				continue
			}
			if len(l.Forks) == MaxLineage {
				l.Truncated = true
				break
			}
			l.Forks = append(l.Forks, session.ID(c.Header.ID))
		}
		walked += len(p.Summaries) + 1
		if p.Next == "" || l.Truncated {
			break
		}
		q.Cursor = p.Next
	}
	sortIDs(l.Forks)
	return l, nil
}

// Traced is one record of a trace and the session that first wrote it.
type Traced struct {
	store.Record
	// Origin is the ancestor the record was inherited from, or the session
	// itself.
	Origin session.ID
}

// Trace is a window of id's log with every record labelled by its origin
// across the fork chain: a fork's log holds its parent's records through the
// fork position at the same seqs, so a seq at or below a link's Through came
// from that link (or from further up).
type Trace struct {
	Lineage Lineage
	Events  []Traced
	Next    event.Seq
	More    bool
}

// Trace reads a bounded window of id and labels each record's origin.
func (s *Service) Trace(ctx context.Context, id session.ID, w Window) (Trace, error) {
	l, err := s.Lineage(ctx, id)
	if err != nil {
		return Trace{}, err
	}
	o, err := s.Observe(ctx, id, w)
	if err != nil {
		return Trace{}, err
	}
	t := Trace{Lineage: l, Next: o.Next, More: o.More}
	for _, rec := range o.Events {
		t.Events = append(t.Events, Traced{Record: rec, Origin: originOf(l.Chain, rec.Seq)})
	}
	return t, nil
}

// originOf is the first link, root first, whose inherited prefix holds seq:
// chain[i].Through is what chain[i+1] took from chain[i].
func originOf(chain []Link, seq event.Seq) session.ID {
	for i := 0; i < len(chain)-1; i++ {
		if seq <= chain[i].Through {
			return chain[i].ID
		}
	}
	return chain[len(chain)-1].ID
}

// Follower delivers a session's records in seq order as they are written.
type Follower struct {
	events chan store.Record
	done   chan struct{}
	cancel context.CancelFunc
	err    error
}

// Events is closed when the follower stops; Err then says why.
func (f *Follower) Events() <-chan store.Record { return f.events }

// Err is nil until the follower stops, then the reason (nil after Close or a
// cancelled context).
func (f *Follower) Err() error {
	select {
	case <-f.done:
		return f.err
	default:
		return nil
	}
}

// Close stops the follower and waits for it; idempotent.
func (f *Follower) Close() {
	f.cancel()
	<-f.done
}

// Follow delivers id's records from position from (inherited ones included, so
// a fork's follower sees its parent's prefix first), then each new record as
// it becomes visible, polling every follow interval through a lease-free read.
// A record is delivered once and in order; a slow reader backs the poll off.
func (s *Service) Follow(ctx context.Context, id session.ID, from event.Seq) (*Follower, error) {
	r, err := s.st.OpenRead(ctx, string(id))
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(ctx)
	f := &Follower{events: make(chan store.Record, 64), done: make(chan struct{}), cancel: cancel}
	go func() {
		defer close(f.done)
		defer close(f.events)
		defer func() { _ = r.Close(context.WithoutCancel(ctx)) }()
		tick := time.NewTicker(s.interval)
		defer tick.Stop()
		next := from
		for {
			recs, err := r.Read(ctx, next, MaxWindow, store.ReadOptions{})
			if err != nil {
				if ctx.Err() == nil {
					f.err = err
				}
				return
			}
			for _, rec := range recs {
				select {
				case f.events <- rec:
					next = rec.Seq + 1
				case <-ctx.Done():
					return
				}
			}
			if len(recs) == MaxWindow {
				continue
			}
			select {
			case <-tick.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	return f, nil
}
