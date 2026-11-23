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
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

const dlbSessionID = "s-dlb"

var dlbAgent = agent.Ref{Session: session.ID(dlbSessionID), Incarnation: 1}

var dlbEpoch = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

// dlbWait bounds every real-time wait; the scenarios themselves run on the
// virtual clock and never sleep.
const dlbWait = 10 * time.Second

// dlbOuterT is the test running the suite; scenarios take temp homes from it.
var dlbOuterT *testing.T

type dlbNoIn struct{}

type dlbAddIn struct {
	A int `json:"a" dsh:"required"`
	B int `json:"b" dsh:"required"`
}

type dlbBatch struct {
	res tools.BatchResult
	err error
}

type dlbState struct {
	root *cordis.Context
	tab  scope.Table
	svc  tools.Service
	clk  *clock.Manual

	st     store.Store
	home   home.Layout
	sqlite bool
	spill  *local.Store
	pol    *policy.Policy
	full   string

	started  chan struct{}
	release  chan struct{}
	released bool

	sess    session.Handle
	late    chan struct{} // a tool/late-return was appended (dlbLateLog)
	pending <-chan dlbBatch
	results []tools.Result

	cleanups []func()
}

func (s *dlbState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *dlbState) dispose() {
	s.releaseBody()
	// A detached body appends its late return to the session: let it land
	// before the session closes.
	if s.svc != nil {
		_ = s.untilLate(func() bool { return len(s.svc.Detached()) == 0 })
	}
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

func (s *dlbState) releaseBody() {
	if s.release != nil && !s.released {
		close(s.release)
		s.released = true
	}
}

// untilLate reports whether ok holds, re-checking it after every tool/late-return
// the pipeline appends (P-S): a detached body leaves the detached list before its
// late return is appended, so each change ok waits for comes with that signal.
// dlbWait bounds the wait.
func (s *dlbState) untilLate(ok func() bool) bool {
	timeout := time.After(dlbWait)
	for !ok() {
		select {
		case <-s.late:
		case <-timeout:
			return ok()
		}
	}
	return true
}

// dlbLateLog is the batch's session log, signalling late on each tool/late-return
// append once it is stored.
type dlbLateLog struct {
	session.Handle
	late chan<- struct{}
}

func (l dlbLateLog) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	rc, err := l.Handle.Append(ctx, d)
	if d.Type() == tools.LateReturnType.Name() {
		select {
		case l.late <- struct{}{}:
		default: // a signal is already pending; the waiter re-checks on it
		}
	}
	return rc, err
}

// boot composes the scope table and replaces the tool registry with a pipeline
// configured by cfg.
func (s *dlbState) boot(cfg core.Config) error {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	s.cleanup(root.Dispose)
	f, err := root.Use(scope.Component, cordis.None{})
	if err != nil {
		return err
	}
	if err := f.Wait(); err != nil {
		return err
	}
	g, err := root.Inject([]string{scope.TableKey.Name()}, func(c *cordis.Context) error {
		var err error
		s.tab, err = scope.TableKey.Get(c)
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		return err
	}
	s.root = root
	if cfg.Checkpoint == nil {
		cfg.Checkpoint = checkpoint.Default{}
	}
	s.svc = core.NewWith(root, s.tab, cfg)
	return nil
}

// --- Given ---

func (s *dlbState) virtualPipeline(graceS int) error {
	s.clk = clock.NewManual(dlbEpoch)
	s.st = memstore.New()
	return s.boot(core.Config{Clock: s.clk, Grace: time.Duration(graceS) * time.Second})
}

func (s *dlbState) registerStuck(name string, deadlineS int) error {
	s.started, s.release = make(chan struct{}, 4), make(chan struct{})
	started, release := s.started, s.release
	_, err := s.svc.Register(s.root, tools.Define(tools.Spec[dlbNoIn, string]{
		Name: name, Description: "Ignores cancellation.", Timeout: time.Duration(deadlineS) * time.Second,
		Run: func(context.Context, dlbNoIn, tools.Run) (string, error) {
			started <- struct{}{}
			<-release
			return "late value", nil
		},
	}))
	return err
}

func (s *dlbState) registerAdd(sig string) error {
	if sig != "add(a:int,b:int)" {
		return fmt.Errorf("only add(a:int,b:int) is declared, got %q", sig)
	}
	_, err := s.svc.Register(s.root, tools.Define(tools.Spec[dlbAddIn, int]{
		Name: "add", Description: "Adds two integers.",
		Run: func(_ context.Context, in dlbAddIn, _ tools.Run) (int, error) { return in.A + in.B, nil },
	}))
	return err
}

// sqliteSpillHome provisions the SQLite store and the local spill store in one
// home: a fresh directory under DSH_E2E_STORE_HOME when the compose run mounts
// one, else a test-kit temp home (the gate path). Nothing skips.
func (s *dlbState) sqliteSpillHome() error {
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-dlb-")
		if err != nil {
			return err
		}
		s.cleanup(func() { _ = os.RemoveAll(dir) })
		base = dir
	} else if dlbOuterT != nil {
		base = dlbOuterT.TempDir()
	} else {
		return errors.New("no outer *testing.T; run through TestE2E_tool_pipeline_approvals_and_human_commands_deadlines_and_result_budget")
	}
	s.home = home.New(filepath.Join(base, "home"))
	if err := home.Ensure(s.home); err != nil {
		return err
	}
	st, err := sqlite.OpenHome(context.Background(), s.home)
	if err != nil {
		return fmt.Errorf("open the SQLite store: %w", err)
	}
	s.cleanup(func() { _ = st.Close(context.Background()) })
	sp, err := local.New(s.home, local.DefaultConfig)
	if err != nil {
		return fmt.Errorf("open the spill store: %w", err)
	}
	s.st, s.sqlite, s.spill = st, true, sp
	return nil
}

func (s *dlbState) budgetPipeline(kb int) error {
	if s.spill == nil {
		return errors.New("the spill store was not provisioned")
	}
	if err := s.boot(core.Config{MaxResultBytes: kb << 10}); err != nil {
		return err
	}
	if got := s.svc.Limits().MaxResultBytes; got != kb<<10 {
		return fmt.Errorf("the registry's maxResultBytes is %d, want %d", got, kb<<10)
	}
	pol, _, err := policy.Mount(s.root, s.svc, s.spill, policy.Config{Now: func() time.Time { return dlbEpoch }})
	if err != nil {
		return fmt.Errorf("mount the spill policy: %w", err)
	}
	s.pol = pol
	return nil
}

func (s *dlbState) registerBig(name string, mb int) error {
	s.full = dlbNumbered(mb << 20)
	full := s.full
	_, err := s.svc.Register(s.root, tools.Define(tools.Spec[dlbNoIn, string]{
		Name: name, Description: "Prints a large text.",
		Run: func(context.Context, dlbNoIn, tools.Run) (string, error) { return full, nil },
	}))
	return err
}

// dlbNumbered is n bytes of numbered lines, so the head and tail are distinct.
func dlbNumbered(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "line %07d\n", i)
	}
	return b.String()[:n]
}

// --- When ---

// openStep opens a session in st whose step's assistant message asks for calls.
func (s *dlbState) openStep(st store.Store, calls []tools.CallRequest) (session.Handle, error) {
	ctx := context.Background()
	h, err := session.NewService(s.root, st, session.Config{}).Create(ctx, nil, session.CreateOptions{Header: session.Header{ID: dlbSessionID}})
	if err != nil {
		return nil, fmt.Errorf("create the session: %w", err)
	}
	s.cleanup(func() { _ = h.Close(context.Background()) })
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("go"))
	if err != nil {
		return nil, err
	}
	var uses []vocab.Block
	for _, c := range calls {
		uses = append(uses, vocab.ToolUseBlock(c.ID, c.Name, json.RawMessage(c.Arguments)))
	}
	asked, err := vocab.RestoreMessage("a1", vocab.RoleAssistant, nil, uses...)
	if err != nil {
		return nil, err
	}
	for _, mk := range []func() (event.Draft, error){
		func() (event.Draft, error) { return event.TurnStartType.Draft(event.TurnStart{Turn: 1}) },
		func() (event.Draft, error) { return event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1}) },
		func() (event.Draft, error) {
			return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		},
		func() (event.Draft, error) {
			return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: asked})
		},
	} {
		d, err := mk()
		if err != nil {
			return nil, err
		}
		if _, err := h.Append(ctx, d); err != nil {
			return nil, err
		}
	}
	return h, nil
}

// start runs the batch on its own goroutine, as the step's executor would.
func (s *dlbState) start(st store.Store, calls ...tools.CallRequest) (session.Handle, <-chan dlbBatch, error) {
	h, err := s.openStep(st, calls)
	if err != nil {
		return nil, nil, err
	}
	v, err := s.svc.Resolve(context.Background(), tools.Target{Agent: dlbAgent})
	if err != nil {
		return nil, nil, err
	}
	if s.late == nil {
		s.late = make(chan struct{}, 1)
	}
	log := dlbLateLog{Handle: h, late: s.late}
	out := make(chan dlbBatch, 1)
	go func() {
		defer v.Release()
		res, err := s.svc.ExecuteBatch(context.Background(), tools.Batch{View: v, Log: log, Calls: calls})
		out <- dlbBatch{res, err}
	}()
	return h, out, nil
}

func (s *dlbState) await(out <-chan dlbBatch, n int) ([]tools.Result, error) {
	select {
	case b := <-out:
		if b.err != nil {
			return nil, fmt.Errorf("execute the batch: %w", b.err)
		}
		if len(b.res.Results) != n {
			return nil, fmt.Errorf("%d results for %d calls", len(b.res.Results), n)
		}
		return b.res.Results, nil
	case <-time.After(dlbWait):
		return nil, errors.New("the batch is still held by the call after the virtual deadline and grace")
	}
}

func (s *dlbState) callsStuckThenAdd(stuck, addArgs string) error {
	h, out, err := s.start(s.st,
		tools.CallRequest{ID: "c1", Name: stuck, Arguments: `{}`},
		tools.CallRequest{ID: "c2", Name: "add", Arguments: addArgs})
	if err != nil {
		return err
	}
	s.sess, s.pending = h, out
	return nil
}

func (s *dlbState) advanceAfterStart(name string, secs int) error {
	select {
	case <-s.started:
	case <-time.After(dlbWait):
		return fmt.Errorf("the body of %s never started", name)
	}
	// The deadline's timer is armed before the body starts.
	if !s.clk.BlockUntil(1, dlbWait) {
		return fmt.Errorf("the virtual clock has %d waiters, want the deadline's", s.clk.Waiters())
	}
	select {
	case b := <-s.pending:
		return fmt.Errorf("the batch ended before the clock advanced: %+v", b)
	default:
	}
	s.clk.Advance(time.Duration(secs) * time.Second)
	res, err := s.await(s.pending, 2)
	if err != nil {
		return err
	}
	s.results = res
	return nil
}

func (s *dlbState) modelCalls(name, args string) error {
	h, out, err := s.start(s.st, tools.CallRequest{ID: "c1", Name: name, Arguments: args})
	if err != nil {
		return err
	}
	s.sess = h
	res, err := s.await(out, 1)
	if err != nil {
		return err
	}
	s.results = res
	return nil
}

func (s *dlbState) bodyReturns(string) error {
	s.releaseBody()
	if !s.untilLate(func() bool { return len(s.svc.Detached()) == 0 }) {
		return fmt.Errorf("still detached after its return: %+v", s.svc.Detached())
	}
	return nil
}

// --- Then ---

func (s *dlbState) result(name string) (tools.Result, error) {
	for _, r := range s.results {
		if r.Name == name {
			return r, nil
		}
	}
	return tools.Result{}, fmt.Errorf("no result for %s in %+v", name, s.results)
}

func (s *dlbState) answeredTimedOut(name, code, budget, cause string) error {
	r, err := s.result(name)
	if err != nil {
		return err
	}
	// Answered timed out: the budget ended and, the body ignoring its context,
	// grace ended too, so the call settles without waiting for the body.
	if !r.IsError || r.Code != tools.CodeOutcomeUnknown || string(r.Code) != code || string(r.Cause) != cause || r.Stage != tools.StageBody ||
		!strings.Contains(r.Text, "timed out after "+budget) || r.Seq == 0 {
		return fmt.Errorf("the call of %s: %+v, want timed out after %s (cause %s) with its tool/result appended", name, r, budget, cause)
	}
	return nil
}

func (s *dlbState) stepContinues(name, text string) error {
	r, err := s.result(name)
	if err != nil {
		return err
	}
	if r.IsError || r.Text != text || r.Stage != tools.StageShape || r.Seq == 0 {
		return fmt.Errorf("the call of %s: %+v, want success %q", name, r, text)
	}
	return nil
}

func (s *dlbState) events() ([]event.Event, error) {
	evs, err := s.sess.Read(context.Background(), 0, 0)
	if err != nil {
		return nil, err
	}
	if len(evs) < 4 {
		return nil, fmt.Errorf("the session holds %d events, want the step's four first", len(evs))
	}
	return evs[4:], nil
}

func (s *dlbState) logHolds(csv string) error {
	evs, err := s.events()
	if err != nil {
		return err
	}
	var got []string
	for _, e := range evs {
		got = append(got, e.Type())
	}
	if strings.Join(got, ",") != csv {
		return fmt.Errorf("the step logged %v, want %s", got, csv)
	}
	return nil
}

func (s *dlbState) listedDetached(name string) error {
	det := s.svc.Detached()
	if len(det) != 1 || det[0].CallID != "c1" || det[0].Tool != name || det[0].Agent != dlbAgent ||
		!det[0].Since.Equal(dlbEpoch.Add(12*time.Second)) {
		return fmt.Errorf("Detached = %+v, want %s/c1 of %v since the grace ended", det, name, dlbAgent)
	}
	return nil
}

func (s *dlbState) furtherCallBusy(name, code, stage string) error {
	_, out, err := s.start(memstore.New(), tools.CallRequest{ID: "c1", Name: name, Arguments: `{}`})
	if err != nil {
		return err
	}
	res, err := s.await(out, 1)
	if err != nil {
		return err
	}
	if r := res[0]; !r.IsError || string(r.Code) != code || string(r.Stage) != stage {
		return fmt.Errorf("a further call of %s: %+v, want %s at %s", name, r, code, stage)
	}
	select {
	case <-s.started:
		return fmt.Errorf("a second body of %s started while the first is detached", name)
	default:
	}
	return nil
}

func (s *dlbState) lateReturnLogged(name string) error {
	var evs []event.Event
	var late *tools.LateReturn
	ok := s.untilLate(func() bool {
		all, err := s.sess.Read(context.Background(), 0, 0)
		if err != nil {
			return false
		}
		evs = all
		for _, ev := range all {
			if ev.Type() == tools.LateReturnType.Name() {
				var l tools.LateReturn
				if json.Unmarshal([]byte(ev.Raw()), &l) == nil {
					late = &l
					return true
				}
			}
		}
		return false
	})
	if !ok || late == nil {
		return fmt.Errorf("no tool/late-return was logged")
	}
	if *late != (tools.LateReturn{CallID: "c1", Tool: name, OK: true}) {
		return fmt.Errorf("tool/late-return = %+v", *late)
	}
	for _, ev := range evs {
		if ev.Type() == event.ToolResult.Name() && strings.Contains(ev.Raw(), "late value") {
			return fmt.Errorf("the late value reached a tool/result: %s", ev.Raw())
		}
	}
	return nil
}

func (s *dlbState) withinWithSpill(kb, mb int) error {
	if len(s.results) != 1 {
		return fmt.Errorf("%d results, want one", len(s.results))
	}
	r := s.results[0]
	if r.IsError || r.Spill == nil || r.Spill.Bytes != int64(mb<<20) || len(r.Text) > kb<<10 || len(r.Text) > s.pol.Budget() || r.Seq == 0 {
		return fmt.Errorf("big: error %v %s, spill %+v, truncated %v, %d bytes; want within %d KB with a %d MB spill",
			r.IsError, r.Code, r.Spill, r.Truncated, len(r.Text), kb, mb)
	}
	return nil
}

func (s *dlbState) previewNamesFile() error {
	r := s.results[0]
	if !strings.HasPrefix(r.Text, s.full[:1000]) || !strings.Contains(r.Text, s.full[len(s.full)-1000:]) {
		return errors.New("the preview does not keep the head and the tail of the full result")
	}
	host, ok := s.spill.HostPath(r.Spill.Path)
	if !ok || !strings.Contains(r.Text, host) {
		return fmt.Errorf("the notice does not name the spill file %q", host)
	}
	return nil
}

func (s *dlbState) secondConnLogged(kb int) error {
	if !s.sqlite {
		return errors.New("the SQLite store was not provisioned")
	}
	ctx := context.Background()
	other, err := sqlite.Open(ctx, s.home.DB())
	if err != nil {
		return err
	}
	defer other.Close(ctx)
	rd, err := other.OpenRead(ctx, dlbSessionID)
	if err != nil {
		return err
	}
	defer rd.Close(ctx)
	recs, err := rd.Read(ctx, 0, 0, store.ReadOptions{})
	if err != nil {
		return err
	}
	want := s.results[0].Spill
	for _, rec := range recs {
		if rec.Type != event.ToolResult.Name() {
			continue
		}
		var p event.ToolResultPayload
		if err := json.Unmarshal([]byte(rec.Payload), &p); err != nil {
			return err
		}
		if p.CallID != "c1" {
			continue
		}
		if p.Spill == nil || *p.Spill != *want {
			return fmt.Errorf("the logged spill reference is %+v, want %+v", p.Spill, *want)
		}
		if len(rec.Payload) > kb<<10 {
			return fmt.Errorf("the logged tool/result is %d bytes, over %d KB", len(rec.Payload), kb)
		}
		return nil
	}
	return errors.New("a second connection sees no tool/result for c1")
}

func (s *dlbState) spillHoldsAll(mb int) error {
	host, ok := s.spill.HostPath(s.results[0].Spill.Path)
	if !ok {
		return fmt.Errorf("the spill store cannot map %q to a host path", s.results[0].Spill.Path)
	}
	rel, err := filepath.Rel(s.home.Spill(), host)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("the spill file %s is not under the temp home's spill directory %s", host, s.home.Spill())
	}
	saved, err := os.ReadFile(host)
	if err != nil {
		return err
	}
	if len(saved) != mb<<20 || !bytes.Equal(saved, []byte(s.full)) {
		return fmt.Errorf("the spill file holds %d bytes, want all %d of the result", len(saved), mb<<20)
	}
	return nil
}

func InitializeScenario_tool_pipeline_approvals_and_human_commands_deadlines_and_result_budget(ctx *godog.ScenarioContext) {
	s := &dlbState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = dlbState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.dispose()
		return c, nil
	})

	ctx.Step(`^a guarded pipeline on the virtual clock with a (\d+) s grace over the memory store$`, s.virtualPipeline)
	ctx.Step(`^tool "([^"]+)" whose body ignores cancellation with a (\d+) s deadline$`, s.registerStuck)
	ctx.Step(`^tool "([^"]+)" is registered$`, s.registerAdd)
	ctx.Step(`^the SQLite store and the spill store in a temp home$`, s.sqliteSpillHome)
	ctx.Step(`^a guarded pipeline with maxResultBytes of (\d+) KB and the spill policy mounted$`, s.budgetPipeline)
	ctx.Step(`^tool "([^"]+)" returning (\d+) MB of text$`, s.registerBig)

	ctx.Step(`^the model calls "([^"]+)" and then "add" with arguments '([^']*)'$`, s.callsStuckThenAdd)
	ctx.Step(`^the body of "([^"]+)" has started and the virtual clock advances (\d+) s$`, s.advanceAfterStart)
	ctx.Step(`^the model calls "([^"]+)" with arguments '([^']*)'$`, s.modelCalls)
	ctx.Step(`^the detached body of "([^"]+)" finally returns$`, s.bodyReturns)

	ctx.Step(`^the call of "([^"]+)" is answered "([^"]+)" \(timed out after "([^"]+)"\) with cause "([^"]+)"$`, s.answeredTimedOut)
	ctx.Step(`^the step continues: the call of "([^"]+)" succeeds with text "([^"]*)"$`, s.stepContinues)
	ctx.Step(`^the session log after the step holds "([^"]+)"$`, s.logHolds)
	ctx.Step(`^the body of "([^"]+)" is listed as detached for the step's agent$`, s.listedDetached)
	ctx.Step(`^a further call of "([^"]+)" by the same agent ends "([^"]+)" at the "([^"]+)" stage$`, s.furtherCallBusy)
	ctx.Step(`^the session log records a tool/late-return for "([^"]+)" and its late value never reaches a tool/result$`, s.lateReturnLogged)
	ctx.Step(`^the result returned to the model is within (\d+) KB with a spill reference of (\d+) MB$`, s.withinWithSpill)
	ctx.Step(`^the preview keeps the head and the tail of the full result and names the spill file$`, s.previewNamesFile)
	ctx.Step(`^a second connection to the database sees the logged tool/result within (\d+) KB with the same spill reference$`, s.secondConnLogged)
	ctx.Step(`^the spill file in the temp home holds all (\d+) MB of the result$`, s.spillHoldsAll)
}

func TestE2E_tool_pipeline_approvals_and_human_commands_deadlines_and_result_budget(t *testing.T) {
	dlbOuterT = t
	t.Cleanup(func() { dlbOuterT = nil })
	suite := godog.TestSuite{
		Name:                "tool_pipeline_approvals_and_human_commands_deadlines_and_result_budget",
		ScenarioInitializer: InitializeScenario_tool_pipeline_approvals_and_human_commands_deadlines_and_result_budget,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"tool_pipeline_approvals_and_human_commands_deadlines_and_result_budget.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
