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
	"errors"
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
	// DefaultLimit is a List page with no limit; MaxLimit clamps every page
	// (query.Config of docs/harness/areas/session-views.md §6.2).
	DefaultLimit = 20
	MaxLimit     = 100
	// MaxListed bounds the store candidates ListByProject and Lineage's fork
	// search examine (WithMaxListed lowers it).
	MaxListed = 10_000
	// listPage is the page those walks ask the store for.
	listPage = 100
	// MaxLineage bounds the fork chain a lineage or trace walks.
	MaxLineage = 32
	// DefaultFollowInterval is how often a follower polls the store.
	DefaultFollowInterval = 100 * time.Millisecond
)

// Caller is what a reader may see (DH-D-VIEW-5: "within what the caller may
// see"). Every method applies the same rule: List and ListByProject leave out
// what the caller may not see, and Observe, Lineage, Trace and Follow answer
// for such a session exactly as for a missing one (session/not-found), so a
// caller cannot tell a session it may not see from one that does not exist.
type Caller struct {
	// AllProjects sees the sessions of every project.
	AllProjects bool
	// Projects are the workspace ids whose sessions it sees otherwise.
	Projects []string
	// Ungrouped sees the sessions attached to no project.
	Ungrouped bool
	// Hidden sees pending and archived sessions.
	Hidden bool
}

// Viewer sees every session that is not hidden: the default caller.
func Viewer() Caller { return Caller{AllProjects: true, Ungrouped: true} }

// Operator sees every session, hidden ones too.
func Operator() Caller { return Caller{AllProjects: true, Ungrouped: true, Hidden: true} }

// Sees reports whether c may see a session attached to workspace ws ("" when
// ungrouped) with flags f.
func (c Caller) Sees(ws string, f workspace.Flags) bool {
	if f.Archive.Hidden() && !c.Hidden {
		return false
	}
	if ws == "" {
		return c.Ungrouped
	}
	return c.AllProjects || slices.Contains(c.Projects, ws)
}

func (c Caller) seesProject(ws string) bool { return c.AllProjects || slices.Contains(c.Projects, ws) }

// Service reads sessions from one store for one caller. Membership and flags
// come from the workspace registry when one is given; without it every session
// is ungrouped and unflagged (or flagged by WithMarks).
type Service struct {
	st        store.Store
	ws        *workspace.Registry
	marks     workspace.Marks
	caller    Caller
	interval  time.Duration
	maxListed int
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

// WithCaller reads as c; the default is Viewer.
func WithCaller(c Caller) Option { return func(s *Service) { s.caller = c } }

// WithFollowInterval sets the follower's poll interval.
func WithFollowInterval(d time.Duration) Option {
	return func(s *Service) {
		if d > 0 {
			s.interval = d
		}
	}
}

// WithMaxListed lowers the candidate bound of the walks below MaxListed.
func WithMaxListed(n int) Option {
	return func(s *Service) {
		if n > 0 && n < MaxListed {
			s.maxListed = n
		}
	}
}

// New is a Service over st.
func New(st store.Store, opts ...Option) *Service {
	s := &Service{st: st, caller: Viewer(), interval: DefaultFollowInterval, maxListed: MaxListed}
	for _, o := range opts {
		o(s)
	}
	return s
}

// As is a copy of s reading as c.
func (s *Service) As(c Caller) *Service {
	cp := *s
	cp.caller = c
	return &cp
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

// ListOptions filters List and ListByProject. What the caller may not see is
// never listed (Caller); hidden sessions are listed only to a Hidden caller.
type ListOptions struct {
	Cwd, Creator string
	// IncludeBlank lists sessions with no prompt; by default they are hidden.
	IncludeBlank bool
	// Limit and Cursor page List (the store's keyset); ListByProject walks all.
	// Limit 0 (or less) is DefaultLimit, more than MaxLimit is MaxLimit.
	Limit  int
	Cursor string
	// AsOf is the cut of List's first page (zero: now); later pages keep the
	// cut their cursor carries.
	AsOf time.Time
}

// Page is one page of List.
type Page struct {
	Entries []Entry
	Next    string
}

// List is one page of sessions by their last prompt (a blank session by its
// creation), newest first, then id, as of the first page's cut
// (store.OrderPrompt on the store's last_prompt_at index, DH-D-VIEW-5): an
// assistant's activity never reorders it, and a session prompted while the
// caller pages keeps its place, so no session is skipped or listed twice; a
// session created after the cut is left to a new listing. Each entry has its
// summary and flags; blank sessions and those the caller may not see are
// filtered out, so a page may hold fewer than Limit.
func (s *Service) List(ctx context.Context, o ListOptions) (Page, error) {
	p, _, err := s.page(ctx, store.Query{Cwd: o.Cwd, Creator: o.Creator, Limit: pageLimit(o.Limit), Cursor: o.Cursor,
		Order: store.OrderPrompt, AsOf: o.AsOf}, o)
	return p, err
}

// pageLimit is the store page List asks for: n within (0, MaxLimit].
func pageLimit(n int) int {
	switch {
	case n <= 0:
		return DefaultLimit
	case n > MaxLimit:
		return MaxLimit
	}
	return n
}

// page is one filtered store page and how many candidates it examined.
func (s *Service) page(ctx context.Context, q store.Query, o ListOptions) (Page, int, error) {
	p, err := s.st.List(ctx, q)
	if err != nil {
		return Page{}, 0, err
	}
	out := Page{Next: p.Next}
	for _, sum := range p.Summaries {
		e, err := s.entry(ctx, sum)
		if err != nil {
			return Page{}, 0, err
		}
		if (o.IncludeBlank || !e.Summary.Blank) && s.caller.Sees(e.Workspace, e.Flags) {
			out.Entries = append(out.Entries, e)
		}
	}
	return out, len(p.Summaries), nil
}

func (s *Service) entry(ctx context.Context, sum store.Summary) (Entry, error) {
	id := session.ID(sum.Header.ID)
	e := Entry{ID: id, Header: sum.Header, Events: sum.NextSeq, LastEventAt: sum.LastEventAt}
	var err error
	if e.Workspace, e.Flags, err = s.access(ctx, id); err != nil {
		return Entry{}, err
	}
	if !s.caller.Sees(e.Workspace, e.Flags) {
		return e, nil
	}
	if e.Summary, err = s.summary(ctx, sum); err != nil {
		return Entry{}, err
	}
	return e, nil
}

// access is session id's project and flags.
func (s *Service) access(ctx context.Context, id session.ID) (ws string, f workspace.Flags, err error) {
	if s.marks != nil {
		if f, err = s.marks.Flags(ctx, id); err != nil {
			return "", f, err
		}
	}
	if s.ws != nil {
		if ws, _, err = s.ws.WorkspaceOf(ctx, id); err != nil {
			return "", f, err
		}
	}
	return ws, f, nil
}

// notFound is what a caller learns of a session that is missing, deleted or
// that it may not see: the same error for all three.
func notFound(id session.ID) error {
	return store.Errorf(store.CodeNotFound, "no session %s", id)
}

// check is session id's summary when the caller may see it, notFound otherwise.
func (s *Service) check(ctx context.Context, id session.ID) (store.Summary, error) {
	sum, err := s.st.Stat(ctx, string(id))
	if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrDeleted) {
		return store.Summary{}, notFound(id)
	}
	if err != nil {
		return store.Summary{}, err
	}
	ws, f, err := s.access(ctx, id)
	if err != nil {
		return store.Summary{}, err
	}
	if !s.caller.Sees(ws, f) {
		return store.Summary{}, notFound(id)
	}
	return sum, nil
}

// summary is the session's list summary at the head sum reports.
func (s *Service) summary(ctx context.Context, sum store.Summary) (views.ListSummary, error) {
	r, err := s.st.OpenRead(ctx, sum.Header.ID)
	if err != nil {
		return views.ListSummary{}, err
	}
	defer func() { _ = r.Close(ctx) }()
	c, err := readCut(ctx, r, sum.NextSeq)
	if err != nil {
		return views.ListSummary{}, err
	}
	d := views.ListSummaryView()
	st, from := start(c, d)
	if err := c.load(ctx, r, from); err != nil {
		return views.ListSummary{}, err
	}
	return finish(c, d, st, from), nil
}

// cut is one committed cut of a log: everything below head and nothing past
// it. Its checkpoints are read once and only those below head are kept; its
// records are read once, from the earliest position any fold needs.
type cut struct {
	head event.Seq
	hdr  store.Header
	cps  []store.Checkpoint
	from event.Seq
	evs  []event.Event
}

func readCut(ctx context.Context, r store.Reader, head event.Seq) (*cut, error) {
	cps, err := r.Checkpoints(ctx)
	if err != nil {
		return nil, err
	}
	c := &cut{head: head, hdr: r.Header()}
	for _, cp := range cps {
		if cp.Seq < head {
			c.cps = append(c.cps, cp)
		}
	}
	return c, nil
}

// start is d's state from its stored row in c when one is usable (a row
// records the seq it reflects), else d's initial state; from is where the log
// takes it up.
func start[S any](c *cut, d views.Definition[S]) (S, event.Seq) {
	state, from := d.Init(c.hdr), event.Seq(0)
	for _, cp := range c.cps {
		if cp.Key != d.Key || cp.StateVersion != d.Version {
			continue
		}
		var v S
		if json.Unmarshal([]byte(cp.State), &v) == nil && cp.Seq+1 >= from {
			state, from = v, cp.Seq+1
		}
	}
	return state, from
}

// load reads c's records [from, head) in one read.
func (c *cut) load(ctx context.Context, r store.Reader, from event.Seq) error {
	c.from = from
	n := int(c.head) - int(from)
	if n <= 0 {
		return nil
	}
	recs, err := r.Read(ctx, from, n, store.ReadOptions{})
	if err != nil {
		return err
	}
	if len(recs) != n {
		return store.Errorf(store.CodeNotFound, "session %s: %d of the %d records below its head %d", c.hdr.ID, len(recs), n, c.head)
	}
	c.evs = events(recs)
	return nil
}

// finish is d folded from state over c's records from from up to the head.
func finish[S any](c *cut, d views.Definition[S], state S, from event.Seq) S {
	if from >= c.head {
		return state
	}
	return views.FoldFrom(d, state, c.evs[from-c.from:])
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

// Listing is ListByProject's result.
type Listing struct {
	Groups []Group
	// Examined is how many store candidates the walk read.
	Examined int
	// Truncated is set when the walk stopped at its candidate bound before the
	// store's last page: Groups then hold only what the examined candidates
	// gave, not every session.
	Truncated bool
}

// ListByProject is every listed session grouped by project: the projects the
// caller may see in the registry's order, each with its sessions pinned first,
// then by the last prompt (newest first), then by id; the ungrouped sessions
// come last, and a project with no listed session is still shown. It examines
// at most MaxListed store candidates (filtered ones count) and reports
// Truncated when that bound cut the walk.
func (s *Service) ListByProject(ctx context.Context, o ListOptions) (Listing, error) {
	var projects []workspace.Workspace
	if s.ws != nil {
		all, err := s.ws.List(ctx)
		if err != nil {
			return Listing{}, err
		}
		for _, w := range all {
			if s.caller.seesProject(w.ID) {
				projects = append(projects, w)
			}
		}
	}
	groups := make([]Group, len(projects), len(projects)+1)
	at := map[string]int{}
	for i, w := range projects {
		groups[i] = Group{Workspace: w}
		at[w.ID] = i
	}
	ungrouped := Group{Ungrouped: true}
	var out Listing
	q := store.Query{Cwd: o.Cwd, Creator: o.Creator, Order: store.OrderPrompt}
	complete := false
	for out.Examined < s.maxListed {
		q.Limit = min(listPage, s.maxListed-out.Examined)
		p, n, err := s.page(ctx, q, o)
		if err != nil {
			return Listing{}, err
		}
		out.Examined += n
		for _, e := range p.Entries {
			if i, ok := at[e.Workspace]; ok {
				groups[i].Sessions = append(groups[i].Sessions, e)
			} else {
				ungrouped.Sessions = append(ungrouped.Sessions, e)
			}
		}
		if p.Next == "" {
			complete = true
			break
		}
		if n == 0 {
			break
		}
		q.Cursor = p.Next
	}
	out.Truncated = !complete
	if len(ungrouped.Sessions) > 0 {
		groups = append(groups, ungrouped)
	}
	for i := range groups {
		slices.SortFunc(groups[i].Sessions, bySidebar)
	}
	out.Groups = groups
	return out, nil
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
