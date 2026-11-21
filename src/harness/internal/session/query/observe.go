package query

import (
	"context"
	"errors"
	"slices"
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
// no lease, so it works while another process writes the session. It is one
// committed cut: the head is read once and the window, the checkpoints and
// every view stop at it, so records appended meanwhile appear in none of them.
// A session the caller may not see is session/not-found, as a missing one.
func (s *Service) Observe(ctx context.Context, id session.ID, w Window) (Observation, error) {
	sum, err := s.check(ctx, id)
	if err != nil {
		return Observation{}, err
	}
	r, err := s.st.OpenRead(ctx, string(id))
	if err != nil {
		return Observation{}, err
	}
	defer func() { _ = r.Close(ctx) }()
	head := sum.NextSeq
	o := Observation{Header: r.Header(), Head: head, Next: max(w.From, head)}
	if n := min(w.limit(), int(head)-int(w.From)); n > 0 {
		if o.Events, err = r.Read(ctx, w.From, n, store.ReadOptions{}); err != nil {
			return Observation{}, err
		}
		for len(o.Events) > 0 && o.Events[len(o.Events)-1].Seq >= head {
			o.Events = o.Events[:len(o.Events)-1]
		}
		if k := len(o.Events); k > 0 {
			o.Next = o.Events[k-1].Seq + 1
		}
	}
	o.More = o.Next < head
	if o.Views, err = snapshot(ctx, r, head); err != nil {
		return Observation{}, err
	}
	return o, nil
}

// snapshot is the four built-in views at head, from one read of the
// checkpoints and one read of the records they need.
func snapshot(ctx context.Context, r store.Reader, head event.Seq) (Snapshot, error) {
	c, err := readCut(ctx, r, head)
	if err != nil {
		return Snapshot{}, err
	}
	ds, dt, dst, di := views.ListSummaryView(), views.TurnIndexView(), views.StatsView(), views.EventIndexView()
	s0, f0 := start(c, ds)
	s1, f1 := start(c, dt)
	s2, f2 := start(c, dst)
	s3, f3 := start(c, di)
	if err := c.load(ctx, r, min(f0, f1, f2, f3)); err != nil {
		return Snapshot{}, err
	}
	return Snapshot{
		Summary: finish(c, ds, s0, f0),
		Turns:   finish(c, dt, s1, f1),
		Stats:   finish(c, dst, s2, f2),
		Index:   finish(c, di, s3, f3),
	}, nil
}

// Link is one session of a lineage.
type Link struct {
	ID session.ID
	// Through is the last position the next link down inherited from this one;
	// unused on the session itself (the last link).
	Through event.Seq
	// Gone is an ancestor the caller cannot read: deleted after the fork, or
	// one it may not see (the two are not told apart).
	Gone bool
}

// Lineage is the fork chain of a session, root first and the session last,
// with its direct forks.
type Lineage struct {
	Chain []Link
	// Forks are the sessions forked from it that the caller may see, by id;
	// bounded by MaxLineage.
	Forks []session.ID
	// Truncated is set when the chain or the forks were cut at MaxLineage, or
	// the fork search at its candidate bound.
	Truncated bool
}

// Lineage walks id's fork ancestry (at most MaxLineage links) and its direct
// forks (examining at most MaxListed candidates). The walk stops at an
// ancestor the caller cannot read, marked Gone.
func (s *Service) Lineage(ctx context.Context, id session.ID) (Lineage, error) {
	sum, err := s.check(ctx, id)
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
		p, err := s.check(ctx, link.ID)
		if errors.Is(err, store.ErrNotFound) {
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
	slices.Reverse(chain)
	l.Chain = chain
	// Prompt order is as of the first page, so a fork prompted during the walk
	// is not skipped.
	q := store.Query{Order: store.OrderPrompt}
	// The fork bound is kept apart from the chain bound so a deep chain still
	// lists its forks.
	complete, forksFull := false, false
	for examined := 0; examined < s.maxListed && !forksFull; {
		q.Limit = min(listPage, s.maxListed-examined)
		p, err := s.st.List(ctx, q)
		if err != nil {
			return Lineage{}, err
		}
		examined += len(p.Summaries)
		for _, c := range p.Summaries {
			if c.Header.ForkSource != string(id) {
				continue
			}
			ws, f, err := s.access(ctx, session.ID(c.Header.ID))
			if err != nil {
				return Lineage{}, err
			}
			if !s.caller.Sees(ws, f) {
				continue
			}
			if len(l.Forks) == MaxLineage {
				forksFull = true
				break
			}
			l.Forks = append(l.Forks, session.ID(c.Header.ID))
		}
		if forksFull {
			break
		}
		if p.Next == "" {
			complete = true
			break
		}
		if len(p.Summaries) == 0 {
			break
		}
		q.Cursor = p.Next
	}
	l.Truncated = l.Truncated || forksFull || !complete
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
// fork position at the same seqs, so a record came from the parent when its
// seq is at or below the fork's Through, and from further up only while every
// cut on the way up holds it too.
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

// originOf resolves seq from the session upward: chain[i].Through is what
// chain[i+1] took from chain[i], so seq moves up to chain[i] only while it is
// within that cut, and stops at the first cut it exceeds (a fork may keep
// less of its parent than the parent kept of its own).
func originOf(chain []Link, seq event.Seq) session.ID {
	o := len(chain) - 1
	for i := len(chain) - 2; i >= 0 && seq <= chain[i].Through; i-- {
		o = i
	}
	return chain[o].ID
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
// The caller's access is checked at the start and before every poll: a session
// it may not see (or one hidden or deleted meanwhile) stops the follower with
// session/not-found, as a missing one.
func (s *Service) Follow(ctx context.Context, id session.ID, from event.Seq) (*Follower, error) {
	if _, err := s.check(ctx, id); err != nil {
		return nil, err
	}
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
			if _, err := s.check(ctx, id); err != nil {
				if ctx.Err() == nil {
					f.err = err
				}
				return
			}
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
