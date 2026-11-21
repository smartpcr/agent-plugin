// Package query is how the UI, the SDK and tools read sessions
// (docs/harness/areas/session-views.md DH-D-VIEW-5): one interface that lists
// sessions (grouped by project), observes one through a bounded window and a
// snapshot of its built-in views, traces it across its forks with a bounded
// lineage, and follows it as it grows. It reads the store only; it never writes.
package query

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

// Bounds of the reads.
const (
	// DefaultWindow is an Observe or Trace window with no limit.
	DefaultWindow = 100
	// MaxWindow clamps every window.
	MaxWindow = 500
	// MaxListed bounds ListByProject's walk of the store.
	MaxListed = 10_000
	// MaxLineage bounds the fork chain a lineage or trace walks.
	MaxLineage = 32
	// DefaultFollowInterval is how often a follower polls the store.
	DefaultFollowInterval = 100 * time.Millisecond
)

// Service reads sessions from one store. Membership and flags come from the
// workspace registry when one is given; without it every session is ungrouped
// and unflagged (or flagged by WithMarks).
type Service struct {
	st       store.Store
	ws       *workspace.Registry
	marks    workspace.Marks
	interval time.Duration
}

// Option configures New.
type Option func(*Service)

// WithWorkspaces groups by r's projects and reads flags from it.
func WithWorkspaces(r *workspace.Registry) Option {
	return func(s *Service) {
		s.ws = r
		if r != nil {
			s.marks = r
		}
	}
}

// WithMarks reads archive marks from m (a store without machine-state tables).
func WithMarks(m workspace.Marks) Option { return func(s *Service) { s.marks = m } }

// WithFollowInterval sets the follower's poll interval.
func WithFollowInterval(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.interval = d
		}
	}
}

// New is a Service over st.
func New(st store.Store, opts ...Option) *Service {
	s := &Service{st: st, interval: DefaultFollowInterval}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Entry is one listed session: its header, store summary, list summary view and
// flags.
type Entry struct {
	ID          session.ID
	Header      store.Header
	Events      event.Seq
	LastEventAt time.Time
	Summary     views.ListSummary
	Flags       workspace.Flags
	// Workspace is the project the session is attached to, "" when ungrouped.
	Workspace string
}

// ListOptions filters List and ListByProject.
type ListOptions struct {
	Cwd, Creator string
	// IncludeBlank lists sessions with no prompt; by default they are hidden.
	IncludeBlank bool
	// IncludeHidden lists pending and archived sessions.
	IncludeHidden bool
	// Limit and Cursor page List (the store's keyset); ListByProject walks all.
	Limit  int
	Cursor string
}

// Page is one page of List.
type Page struct {
	Entries []Entry
	Next    string
}

// List is one page of sessions in the store's order, each with its summary and
// flags; blank and hidden sessions are filtered out unless asked for, so a page
// may hold fewer than Limit.
func (s *Service) List(ctx context.Context, o ListOptions) (Page, error) {
	p, err := s.st.List(ctx, store.Query{Cwd: o.Cwd, Creator: o.Creator, Limit: o.Limit, Cursor: o.Cursor})
	if err != nil {
		return Page{}, err
	}
	out := Page{Next: p.Next}
	for _, sum := range p.Summaries {
		e, err := s.entry(ctx, sum)
		if err != nil {
			return Page{}, err
		}
		if keep(e, o) {
			out.Entries = append(out.Entries, e)
		}
	}
	return out, nil
}

func keep(e Entry, o ListOptions) bool {
	return (o.IncludeBlank || !e.Summary.Blank) && (o.IncludeHidden || !e.Flags.Archive.Hidden())
}

func (s *Service) entry(ctx context.Context, sum store.Summary) (Entry, error) {
	id := session.ID(sum.Header.ID)
	e := Entry{ID: id, Header: sum.Header, Events: sum.NextSeq, LastEventAt: sum.LastEventAt}
	var err error
	if e.Summary, err = s.summary(ctx, sum.Header.ID); err != nil {
		return Entry{}, err
	}
	if s.marks != nil {
		if e.Flags, err = s.marks.Flags(ctx, id); err != nil {
			return Entry{}, err
		}
	}
	if s.ws != nil {
		if e.Workspace, _, err = s.ws.WorkspaceOf(ctx, id); err != nil {
			return Entry{}, err
		}
	}
	return e, nil
}

// summary is the session's list summary: its stored row taken up by the log
// after it, or a refold when there is no usable row.
func (s *Service) summary(ctx context.Context, id string) (views.ListSummary, error) {
	r, err := s.st.OpenRead(ctx, id)
	if err != nil {
		return views.ListSummary{}, err
	}
	defer func() { _ = r.Close(ctx) }()
	return stateAt(ctx, r, views.ListSummaryView(), -1)
}

// stateAt is d's state over r's first asOf events (-1: all of them), from d's
// stored row when it is usable (a row records the seq it reflects).
func stateAt[S any](ctx context.Context, r store.Reader, d views.Definition[S], asOf int) (S, error) {
	var zero S
	cps, err := r.Checkpoints(ctx)
	if err != nil {
		return zero, err
	}
	state, from := d.Init(r.Header()), event.Seq(0)
	for _, c := range cps {
		if c.Key != d.Key || c.StateVersion != d.Version || (asOf >= 0 && int(c.Seq) >= asOf) {
			continue
		}
		var v S
		if json.Unmarshal([]byte(c.State), &v) == nil && c.Seq+1 >= from {
			state, from = v, c.Seq+1
		}
	}
	limit := 0
	if asOf >= 0 {
		if int(from) >= asOf {
			return state, nil
		}
		limit = asOf - int(from)
	}
	recs, err := r.Read(ctx, from, limit, store.ReadOptions{})
	if err != nil {
		return zero, err
	}
	return views.FoldFrom(d, state, events(recs)), nil
}

func events(recs []store.Record) []event.Event {
	out := make([]event.Event, len(recs))
	for i, r := range recs {
		out[i] = event.Of(r)
	}
	return out
}

// Group is one project of ListByProject and its sessions. The ungrouped group
// has no workspace.
type Group struct {
	Workspace workspace.Workspace
	Ungrouped bool
	Sessions  []Entry
}

// ListByProject is every listed session grouped by project: the projects in
// the registry's order, each with its sessions pinned first, then by the last
// prompt (newest first), then by id; the ungrouped sessions come last, and a
// project with no listed session is still shown. It walks at most MaxListed
// sessions.
func (s *Service) ListByProject(ctx context.Context, o ListOptions) ([]Group, error) {
	var projects []workspace.Workspace
	if s.ws != nil {
		var err error
		if projects, err = s.ws.List(ctx); err != nil {
			return nil, err
		}
	}
	groups := make([]Group, len(projects), len(projects)+1)
	at := map[string]int{}
	for i, w := range projects {
		groups[i] = Group{Workspace: w}
		at[w.ID] = i
	}
	ungrouped := Group{Ungrouped: true}
	q := o
	q.Cursor, q.Limit = "", 0
	for walked := 0; walked < MaxListed; {
		p, err := s.List(ctx, q)
		if err != nil {
			return nil, err
		}
		for _, e := range p.Entries {
			if i, ok := at[e.Workspace]; ok {
				groups[i].Sessions = append(groups[i].Sessions, e)
			} else {
				ungrouped.Sessions = append(ungrouped.Sessions, e)
			}
		}
		walked += len(p.Entries) + 1
		if p.Next == "" {
			break
		}
		q.Cursor = p.Next
	}
	if len(ungrouped.Sessions) > 0 {
		groups = append(groups, ungrouped)
	}
	for i := range groups {
		slices.SortFunc(groups[i].Sessions, bySidebar)
	}
	return groups, nil
}

// bySidebar orders a project's sessions: pinned first, then the last prompt
// (newest first; a blank session by its creation), then id.
func bySidebar(a, b Entry) int {
	if a.Flags.Pinned != b.Flags.Pinned {
		if a.Flags.Pinned {
			return -1
		}
		return 1
	}
	if c := cmp.Compare(lastPrompt(b), lastPrompt(a)); c != 0 {
		return c
	}
	return strings.Compare(string(a.ID), string(b.ID))
}

func sortIDs(ids []session.ID) { slices.Sort(ids) }

func lastPrompt(e Entry) int64 {
	if e.Summary.LastPromptAt != 0 {
		return e.Summary.LastPromptAt
	}
	return e.Header.CreatedAt.UnixMilli()
}
