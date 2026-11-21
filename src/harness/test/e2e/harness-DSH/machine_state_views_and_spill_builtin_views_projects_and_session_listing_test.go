//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/query"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

// bvplGolden is the committed golden listing the impl's unit test also asserts.
var bvplGolden = filepath.Join("..", "..", "..", "internal", "session", "views", "testdata", "list_by_project.golden")

var (
	bvplBG     = context.Background()
	bvplOuterT *testing.T
)

type bvplState struct {
	cleanups []func()
	home     home.Layout

	// refold
	header   session.Header
	recs     []event.Record
	cps      map[string]store.Checkpoint
	live     views.Stats
	compared []string

	// listing
	st      *sqlite.Store
	ws      *workspace.Registry
	listing query.Listing
	all     query.Listing

	// hidden
	root  *cordis.Context
	areg  agent.Registry
	marks *workspace.MemoryMarks
	arch  *workspace.Archiver
	fake  *bvplFakeAgent
	id    session.ID
	step  agent.PreStepRequest
	ag    agent.Agent
}

func (s *bvplState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *bvplState) close() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

func bvplMust[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// newHome makes the scenario's home: under t.TempDir() (embedded gate path) or
// a fresh directory under DSH_E2E_STORE_HOME (compose path).
func (s *bvplState) newHome() error {
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-bvpl-")
		if err != nil {
			return err
		}
		s.cleanup(func() { os.RemoveAll(dir) })
		base = dir
	} else if bvplOuterT != nil {
		base = bvplOuterT.TempDir()
	} else {
		return errors.New("no outer *testing.T; run through TestE2E_machine_state_views_and_spill_builtin_views_projects_and_session_listing")
	}
	root := filepath.Join(base, "home")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	s.home = home.New(root)
	return nil
}

// boot opens a sqlite store over the home and a session service folding with reg.
func (s *bvplState) boot(reg *views.Registry, clock func() int64) (*sqlite.Store, session.Service, func() error, error) {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	st, err := sqlite.OpenHome(bvplBG, s.home)
	if err != nil {
		root.Dispose()
		return nil, nil, nil, fmt.Errorf("open store: %w", err)
	}
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		root.Dispose()
		return st.Close(bvplBG)
	}
	s.cleanup(func() { _ = stop() })
	return st, session.NewService(root, st, session.Config{Folder: reg, Clock: clock}), stop, nil
}

// --- deterministic session content (mirrors the impl's views tests) ---

func bvplMsg(id string, role vocab.Role, blocks ...vocab.Block) vocab.Message {
	return bvplMust(vocab.RestoreMessage(id, role, nil, blocks...))
}

// bvplTurn is one deterministic turn n: a prompt, a step with a tool call and
// its result (a failed attempt first on every third turn), and an answer step.
func bvplTurn(n int) []event.Draft {
	call := fmt.Sprintf("call-%d", n)
	at := int64(1_700_000_000_000 + n*10_000)
	ds := []event.Draft{
		bvplMust(event.TurnStartType.Draft(event.TurnStart{Turn: n})),
		bvplMust(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{
			Message: bvplMsg(fmt.Sprintf("u%d", n), vocab.RoleUser, vocab.TextBlock(fmt.Sprintf("  prompt   number %d: list the files and explain each of them in detail, please  ", n)))})),
		bvplMust(event.StepStartType.Draft(event.StepStart{Turn: n, Step: 1})),
	}
	if n%3 == 0 {
		ds = append(ds, bvplMust(event.AssistantAttempt.Draft(event.AssistantAttemptPayload{
			Usage:   &vocab.Usage{Input: 10},
			Timing:  &event.Timing{Start: at, End: at + 50},
			Failure: &vocab.Failure{Code: vocab.CodeServer, Class: vocab.Transient, Message: "overloaded"}})))
	}
	ds = append(ds,
		bvplMust(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: bvplMsg(fmt.Sprintf("a%d-1", n), vocab.RoleAssistant, vocab.ToolUseBlock(call, "ls", json.RawMessage(`{"q":1}`))),
			Usage:   &vocab.Usage{Input: 100 + n, CacheRead: 5, Output: 7, Total: 112 + n},
			Timing:  &event.Timing{Start: at + 100, FirstToken: at + 150, End: at + 400}})),
		bvplMust(event.ToolCall.Draft(event.ToolCallPayload{CallID: call, Name: "ls", Arguments: `{"q":1}`})),
		bvplMust(event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: call, IsError: n%4 == 0,
			Message: bvplMsg(fmt.Sprintf("r%d", n), vocab.RoleTool, vocab.ToolResultBlock(call, n%4 == 0, vocab.TextPart("a.txt b.txt")))})),
		bvplMust(event.StepEndType.Draft(event.StepEnd{Turn: n, Step: 1})),
		bvplMust(event.StepStartType.Draft(event.StepStart{Turn: n, Step: 2})),
		bvplMust(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{
			Message: bvplMsg(fmt.Sprintf("a%d-2", n), vocab.RoleAssistant, vocab.TextBlock(fmt.Sprintf("answer %d", n))),
			Usage:   &vocab.Usage{Input: 120, Output: 30, Total: 150},
			Timing:  &event.Timing{Start: at + 500, FirstToken: at + 520, End: at + 900}})),
		bvplMust(event.StepEndType.Draft(event.StepEnd{Turn: n, Step: 2})),
		bvplMust(event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: event.Reason{Kind: event.ReasonCompleted}})),
	)
	return ds
}

// bvplFill appends whole turns (rewriting the tool result of every fifth turn),
// then request-context events, up to exactly total events.
func bvplFill(h session.Session, total int) error {
	for n := 1; ; n++ {
		ds := bvplTurn(n)
		if int(h.Len())+len(ds)+1 > total {
			break
		}
		recs, err := h.AppendGroup(bvplBG, ds)
		if err != nil {
			return fmt.Errorf("turn %d: %w", n, err)
		}
		if n%5 == 0 {
			var seq event.Seq
			for i, d := range ds {
				if d.Type() == event.ToolResult.Name() {
					seq = recs[i].Seq
				}
			}
			call := fmt.Sprintf("call-%d", n)
			d, err := event.ToolResult.Draft(event.Replace(seq, seq), event.ToolResultPayload{CallID: call,
				Message: bvplMsg(fmt.Sprintf("r%d-x", n), vocab.RoleTool, vocab.ToolResultBlock(call, false, vocab.TextPart("[elided]")))}, seq)
			if err != nil {
				return err
			}
			if _, err := h.Append(bvplBG, d); err != nil {
				return fmt.Errorf("rewrite in turn %d: %w", n, err)
			}
		}
	}
	for i := 0; int(h.Len()) < total; i++ {
		d, err := event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: fmt.Sprintf("fill-%d", i)})
		if err != nil {
			return err
		}
		if _, err := h.Append(bvplBG, d); err != nil {
			return err
		}
	}
	if int(h.Len()) != total {
		return fmt.Errorf("the session has %d events, want %d", h.Len(), total)
	}
	return nil
}

// --- Scenario: Refold equals stored for every built-in view ---

func (s *bvplState) aFixed200EventSessionInATempHome() error {
	if err := s.newHome(); err != nil {
		return err
	}
	reg := views.New()
	b, err := views.RegisterBuiltins(reg)
	if err != nil {
		return err
	}
	st, svc, stop, err := s.boot(reg, nil)
	if err != nil {
		return err
	}
	h, err := svc.Create(bvplBG, nil, session.CreateOptions{Header: session.Header{ID: "s-200"}})
	if err != nil {
		return err
	}
	if err := bvplFill(h, 200); err != nil {
		return err
	}
	snap, err := reg.Read(bvplBG, h)
	if err != nil {
		return err
	}
	if s.live, err = b.Stats.Get(snap); err != nil {
		return err
	}
	if err := h.Close(bvplBG); err != nil {
		return err
	}
	if _, err := os.Stat(s.home.DB()); err != nil {
		return fmt.Errorf("dsh.db not in the home: %w", err)
	}

	r, err := st.OpenRead(bvplBG, "s-200")
	if err != nil {
		return err
	}
	defer r.Close(bvplBG)
	if s.recs, err = r.Read(bvplBG, 0, 0, store.ReadOptions{}); err != nil {
		return err
	}
	s.header = r.Header()
	cps, err := r.Checkpoints(bvplBG)
	if err != nil {
		return err
	}
	s.cps = map[string]store.Checkpoint{}
	for _, c := range cps {
		s.cps[c.Key] = c
	}
	if len(s.recs) != 200 {
		return fmt.Errorf("%d records, want 200", len(s.recs))
	}
	_ = stop
	return nil
}

// bvplRefoldEqualsStored decodes d's stored row and compares it, as JSON, with
// d refolded from the whole log and from the log through the row's sequence.
func bvplRefoldEqualsStored[S any](d views.Definition[S], h session.Header, evs []event.Event, cps map[string]store.Checkpoint) (S, error) {
	var zero S
	cp, ok := cps[d.Key]
	if !ok {
		return zero, fmt.Errorf("%s: no stored row", d.Key)
	}
	if cp.StateVersion != d.Version || int(cp.Seq) >= len(evs) {
		return zero, fmt.Errorf("%s: row at version %d seq %d over %d events", d.Key, cp.StateVersion, cp.Seq, len(evs))
	}
	var stored S
	if err := json.Unmarshal([]byte(cp.State), &stored); err != nil {
		return zero, fmt.Errorf("%s: decoding the stored row: %w", d.Key, err)
	}
	refold := views.Refold(d, h, evs)
	through := views.Refold(d, h, evs[:cp.Seq+1])
	got, err := json.Marshal(stored)
	if err != nil {
		return zero, err
	}
	want, _ := json.Marshal(refold)
	at, _ := json.Marshal(through)
	if string(got) != string(want) || string(at) != string(want) {
		return zero, fmt.Errorf("%s: stored state differs from the refold:\nstored %s\nrefold %s\nthrough row seq %s", d.Key, got, want, at)
	}
	return refold, nil
}

func (s *bvplState) eachBuiltinViewIsRefoldedAndCompared() error {
	evs := make([]event.Event, len(s.recs))
	for i, rec := range s.recs {
		evs[i] = event.Of(rec)
	}
	sum, err := bvplRefoldEqualsStored(views.ListSummaryView(), s.header, evs, s.cps)
	if err != nil {
		return err
	}
	s.compared = append(s.compared, views.ListSummaryView().Key)
	turns, err := bvplRefoldEqualsStored(views.TurnIndexView(), s.header, evs, s.cps)
	if err != nil {
		return err
	}
	s.compared = append(s.compared, views.TurnIndexView().Key)
	stats, err := bvplRefoldEqualsStored(views.StatsView(), s.header, evs, s.cps)
	if err != nil {
		return err
	}
	s.compared = append(s.compared, views.StatsView().Key)
	idx, err := bvplRefoldEqualsStored(views.EventIndexView(), s.header, evs, s.cps)
	if err != nil {
		return err
	}
	s.compared = append(s.compared, views.EventIndexView().Key)

	// The refolded states are meaningful, not vacuously equal.
	if sum.Blank || sum.Prompts != len(turns.Turns) || sum.Turns != len(turns.Turns) || sum.Preview == "" || len([]rune(sum.Preview)) > views.PreviewRunes {
		return fmt.Errorf("list summary = %+v over %d turns", sum, len(turns.Turns))
	}
	if len(turns.Turns) == 0 {
		return errors.New("turn index is empty")
	}
	for _, e := range turns.Turns {
		if !e.Settled || e.Reason != event.ReasonCompleted || e.Steps != 2 || !e.HasPrompt || e.End <= e.Start {
			return fmt.Errorf("turn entry = %+v", e)
		}
	}
	if stats.Events != 200 || stats.Attempts != stats.AssistantMessages+stats.FailedAttempts ||
		stats.Outcomes[event.ReasonCompleted] != len(turns.Turns) || stats.ToolErrors == 0 || stats.Replacements == 0 {
		return fmt.Errorf("stats = %+v", stats)
	}
	if fmt.Sprint(s.live) != fmt.Sprint(stats) {
		return fmt.Errorf("live stats %+v differ from the refold %+v", s.live, stats)
	}
	surf, err := surface.Replay(nil, s.recs)
	if err != nil {
		return err
	}
	shadowed := 0
	for _, rec := range s.recs {
		e, ok := idx.Entry(rec.Seq)
		if !ok || e.Class != surf.Classify(rec).String() {
			return fmt.Errorf("seq %d (%s): index %+v, surface class %s", rec.Seq, rec.Type, e, surf.Classify(rec))
		}
		if e.Class == views.ClassShadowed {
			shadowed++
		}
	}
	if shadowed == 0 || len(idx.History) != surf.History().Len() {
		return fmt.Errorf("%d shadowed, history %d vs surface %d", shadowed, len(idx.History), surf.History().Len())
	}
	return nil
}

func (s *bvplState) allFourBuiltinViewsAreEqual() error {
	if len(s.compared) != 4 {
		return fmt.Errorf("compared %d built-in views (%v), want 4", len(s.compared), s.compared)
	}
	seen := map[string]bool{}
	for _, k := range s.compared {
		if seen[k] {
			return fmt.Errorf("view %s compared twice", k)
		}
		seen[k] = true
	}
	return nil
}

// --- Scenario: Listing groups by project ---

func (s *bvplState) threeSessionsInTwoProjects() error {
	if err := s.newHome(); err != nil {
		return err
	}
	var now atomic.Int64
	now.Store(1_700_000_000_000)
	reg := views.New()
	if _, err := views.RegisterBuiltins(reg); err != nil {
		return err
	}
	st, svc, _, err := s.boot(reg, func() int64 { return now.Add(1000) })
	if err != nil {
		return err
	}
	s.st = st
	if s.ws, err = workspace.Open(bvplBG, st); err != nil {
		return err
	}
	base := filepath.Dir(s.home.DB())
	dirs := map[string]workspace.Workspace{}
	for _, name := range []string{"alpha", "beta"} {
		dir := filepath.Join(base, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if dirs[name], err = s.ws.Add(bvplBG, dir); err != nil {
			return err
		}
	}
	// Three listed sessions (s-a1, s-a2 in alpha; s-b1 in beta) plus s-a3,
	// archived so it is hidden from the listing.
	for _, x := range []struct {
		id, project string
		turns       []int
	}{{"s-a1", "alpha", []int{1, 2}}, {"s-b1", "beta", []int{3}}, {"s-a2", "alpha", []int{4}}, {"s-a3", "alpha", []int{5}}} {
		h, err := svc.Create(bvplBG, nil, session.CreateOptions{Header: session.Header{ID: x.id, Cwd: dirs[x.project].Path}})
		if err != nil {
			return err
		}
		for _, n := range x.turns {
			if _, err := h.AppendGroup(bvplBG, bvplTurn(n)); err != nil {
				return err
			}
		}
		if err := h.Close(bvplBG); err != nil {
			return err
		}
		if err := s.ws.Attach(bvplBG, dirs[x.project].ID, session.ID(x.id)); err != nil {
			return err
		}
	}
	if err := s.ws.Pin(bvplBG, "s-a1", true); err != nil {
		return err
	}
	return s.ws.SetArchive(bvplBG, "s-a3", workspace.ArchiveArchived)
}

func (s *bvplState) sessionsAreListedByProject() error {
	q := query.New(s.st, query.WithWorkspaces(s.ws))
	var err error
	if s.listing, err = q.ListByProject(bvplBG, query.ListOptions{}); err != nil {
		return err
	}
	s.all, err = q.As(query.Operator()).ListByProject(bvplBG, query.ListOptions{})
	return err
}

func bvplRender(groups []query.Group) string {
	var sb strings.Builder
	for _, g := range groups {
		if g.Ungrouped {
			sb.WriteString("(ungrouped)\n")
		} else {
			fmt.Fprintf(&sb, "project %s (position %d)\n", g.Workspace.Title, g.Workspace.Position)
		}
		for _, e := range g.Sessions {
			pin := " "
			if e.Flags.Pinned {
				pin = "*"
			}
			fmt.Fprintf(&sb, "  %s %-6s prompts=%d turns=%d events=%d last=%d\n", pin, e.ID, e.Summary.Prompts, e.Summary.Turns, e.Events, e.Summary.LastPromptAt)
			fmt.Fprintf(&sb, "      first prompt at seq %d\n", e.Summary.FirstPrompt)
			fmt.Fprintf(&sb, "      preview: %q\n", e.Summary.Preview)
		}
	}
	return sb.String()
}

func (s *bvplState) theResultEqualsTheCommittedGoldenListing() error {
	if s.listing.Truncated || s.listing.Examined != 4 {
		return fmt.Errorf("listing examined %d, truncated %v", s.listing.Examined, s.listing.Truncated)
	}
	want, err := os.ReadFile(bvplGolden)
	if err != nil {
		return fmt.Errorf("reading the committed golden: %w", err)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n"))
	got := bvplRender(s.listing.Groups)
	if got != string(want) {
		return fmt.Errorf("listing differs from %s\n--- got ---\n%s--- want ---\n%s", filepath.ToSlash(bvplGolden), got, want)
	}
	if len(s.all.Groups) == 0 || len(s.all.Groups[0].Sessions) != 3 {
		return fmt.Errorf("operator listing of alpha = %+v, want 3 sessions including the hidden one", s.all.Groups)
	}
	return nil
}

// --- Scenario: Hidden session stops working ---

// bvplFakeAgent is an agent through the agent contract: a memstore session and
// scope, no loop, running a turn; Stop records its causes.
type bvplFakeAgent struct {
	ref     agent.Ref
	h       session.Handle
	sc      *scope.Scope
	mu      sync.Mutex
	running bool
	stops   []agent.StopCause
}

func (f *bvplFakeAgent) Ref() agent.Ref               { return f.ref }
func (f *bvplFakeAgent) Session() session.ReadSession { return f.h }
func (f *bvplFakeAgent) Scope() *cordis.Context       { return f.sc.Ctx() }
func (f *bvplFakeAgent) ScopeKey() scope.Key          { return f.sc.Key() }
func (f *bvplFakeAgent) State() agent.State           { return agent.StateLive }
func (f *bvplFakeAgent) Status() agent.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.running {
		return agent.StatusRunning
	}
	return agent.StatusIdle
}
func (f *bvplFakeAgent) Stop(_ context.Context, cause agent.StopCause) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stops = append(f.stops, cause)
	f.running = false
	return nil
}
func (f *bvplFakeAgent) Close(ctx context.Context) error {
	_ = f.sc.Close()
	return f.h.Close(ctx)
}
func (f *bvplFakeAgent) stopped() []agent.StopCause {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]agent.StopCause(nil), f.stops...)
}

func (s *bvplState) aSessionWithAnAgentRegisteredThroughTheAgentContractFake() error {
	root := cordis.New()
	root.Loggers().ClearExporters()
	s.cleanup(root.Dispose)
	s.root = root
	svc := session.NewService(root, memstore.New(), session.Config{})
	tab := scope.NewTable(root)
	s.areg = agent.NewRegistry(root)
	s.marks = workspace.NewMemoryMarks()
	if _, err := s.areg.SetFactory(root, agent.FactoryFunc(func(owner *cordis.Context, o agent.CreateOptions, ref agent.Ref) (agent.Instance, error) {
		h, err := svc.Create(bvplBG, nil, session.CreateOptions{Header: session.Header{ID: string(o.Session)}})
		if err != nil {
			return nil, err
		}
		sc, err := tab.New(owner, string(o.Session))
		if err != nil {
			return nil, err
		}
		s.fake = &bvplFakeAgent{ref: ref, h: h, sc: sc, running: true}
		return s.fake, nil
	})); err != nil {
		return err
	}
	s.id = "sess-hide"
	h, err := s.areg.Create(root, agent.CreateOptions{Session: s.id})
	if err != nil {
		return err
	}
	s.cleanup(func() { _ = h.Dispose(bvplBG) })
	s.arch = workspace.NewArchiver(s.marks, workspace.AgentProvider(s.areg))
	if _, err := workspace.GuardSteps(root, s.marks, nil); err != nil {
		return err
	}
	a, ok := s.areg.Get(s.id)
	if !ok {
		return errors.New("no live agent registered")
	}
	s.ag = a
	if a.Status() != agent.StatusRunning {
		return fmt.Errorf("agent status = %v, want running", a.Status())
	}
	s.step = agent.PreStepRequest{Ctx: bvplBG, Agent: a.Ref(), Turn: 1, Step: 1}
	d, err := agent.AskPreStep(root, a.ScopeKey(), s.step)
	if err != nil {
		return err
	}
	if _, ok := d.(agent.Enter); !ok {
		return fmt.Errorf("a visible session's step: %#v", d)
	}
	if got := s.fake.stopped(); len(got) != 0 {
		return fmt.Errorf("stops before hiding = %v", got)
	}
	return nil
}

func (s *bvplState) theSessionIsHidden() error {
	return s.arch.Hide(bvplBG, s.id)
}

func (s *bvplState) theAgentReceivesAStopWithCause(cause string) error {
	if got := s.fake.stopped(); len(got) != 1 || string(got[0]) != cause || got[0] != agent.StopHidden {
		return fmt.Errorf("stops = %v, want exactly one with cause %q", got, cause)
	}
	hidden, err := s.arch.Hidden(bvplBG, s.id)
	if err != nil || !hidden {
		return fmt.Errorf("hidden = %v %v", hidden, err)
	}
	d, err := agent.AskPreStep(s.root, s.ag.ScopeKey(), s.step)
	if err != nil {
		return err
	}
	if r, ok := d.(agent.Reject); !ok || r.By != workspace.GuardBy {
		return fmt.Errorf("a hidden session's next step: %#v, want a reject by %s", d, workspace.GuardBy)
	}
	return nil
}

func InitializeScenario_machine_state_views_and_spill_builtin_views_projects_and_session_listing(ctx *godog.ScenarioContext) {
	s := &bvplState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, err
	})
	ctx.Step(`^a fixed 200-event session in a temp home$`, s.aFixed200EventSessionInATempHome)
	ctx.Step(`^each built-in view is refolded from the log and compared with its stored state$`, s.eachBuiltinViewIsRefoldedAndCompared)
	ctx.Step(`^all four built-in views are equal$`, s.allFourBuiltinViewsAreEqual)
	ctx.Step(`^three sessions in two projects$`, s.threeSessionsInTwoProjects)
	ctx.Step(`^sessions are listed by project$`, s.sessionsAreListedByProject)
	ctx.Step(`^the result equals the committed golden listing$`, s.theResultEqualsTheCommittedGoldenListing)
	ctx.Step(`^a session with an agent registered through the agent contract fake$`, s.aSessionWithAnAgentRegisteredThroughTheAgentContractFake)
	ctx.Step(`^the session is hidden$`, s.theSessionIsHidden)
	ctx.Step(`^the agent receives a stop with cause "([^"]*)"$`, s.theAgentReceivesAStopWithCause)
}

func TestE2E_machine_state_views_and_spill_builtin_views_projects_and_session_listing(t *testing.T) {
	bvplOuterT = t
	defer func() { bvplOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "machine_state_views_and_spill_builtin_views_projects_and_session_listing",
		ScenarioInitializer: InitializeScenario_machine_state_views_and_spill_builtin_views_projects_and_session_listing,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"machine_state_views_and_spill_builtin_views_projects_and_session_listing.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
