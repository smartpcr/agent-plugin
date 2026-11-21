//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/workspace"
)

const (
	tmsfFoldKey = "e2e/stored-count"
	tmsfEvents  = 20
)

var (
	tmsfBG     = context.Background()
	tmsfOuterT *testing.T
)

type tmsfState struct {
	cleanups []func()
	home     home.Layout

	// machine state
	db    *sqlite.Store
	added workspace.Workspace
	got   workspace.Workspace
	list  []workspace.Workspace
	rev   int64

	// stored fold
	sessionID session.ID
	v1        store.Checkpoint
	head      int64
	v2        store.Checkpoint
	v2Found   bool
	readValue int
	readAsOf  int64
}

func (s *tmsfState) cleanup(f func()) { s.cleanups = append(s.cleanups, f) }

func (s *tmsfState) close() {
	for i := len(s.cleanups) - 1; i >= 0; i-- {
		s.cleanups[i]()
	}
	s.cleanups = nil
}

// newHome makes the scenario's home: under t.TempDir() (embedded gate path) or
// a fresh directory under DSH_E2E_STORE_HOME (compose path).
func (s *tmsfState) newHome() error {
	var base string
	if v := os.Getenv("DSH_E2E_STORE_HOME"); v != "" {
		dir, err := os.MkdirTemp(v, "e2e-tmsf-")
		if err != nil {
			return err
		}
		s.cleanup(func() { os.RemoveAll(dir) })
		base = dir
	} else if tmsfOuterT != nil {
		base = tmsfOuterT.TempDir()
	} else {
		return errors.New("no outer *testing.T; run through TestE2E_machine_state_views_and_spill_typed_machine_state_and_stored_folds")
	}
	root := filepath.Join(base, "home")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	s.home = home.New(root)
	return nil
}

func (s *tmsfState) openStore() (*sqlite.Store, error) {
	st, err := sqlite.OpenHome(tmsfBG, s.home)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	s.cleanup(func() { _ = st.Close(tmsfBG) })
	return st, nil
}

// --- Scenario: Machine state survives restart ---

func (s *tmsfState) aWorkspaceRegisteredInATempHome() error {
	if err := s.newHome(); err != nil {
		return err
	}
	folder := filepath.Join(filepath.Dir(s.home.DB()), "..", "project")
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return err
	}
	db, err := s.openStore()
	if err != nil {
		return err
	}
	s.db = db
	clock := time.Date(2025, 3, 4, 5, 6, 7, 891011121, time.UTC)
	reg, err := workspace.Open(tmsfBG, db, workspace.WithClock(func() time.Time { return clock }))
	if err != nil {
		return err
	}
	if s.added, err = reg.Add(tmsfBG, folder); err != nil {
		return err
	}
	if s.added.ID == "" || s.added.Title != "project" || s.added.Position != 0 || !s.added.CreatedAt.Equal(clock) {
		return fmt.Errorf("added workspace = %+v", s.added)
	}
	if _, err := os.Stat(s.home.DB()); err != nil {
		return fmt.Errorf("dsh.db not in the home: %w", err)
	}
	return nil
}

func (s *tmsfState) theStoreIsClosedAndReopened() error {
	if err := s.db.Close(tmsfBG); err != nil {
		return fmt.Errorf("close: %w", err)
	}
	db, err := s.openStore()
	if err != nil {
		return err
	}
	reg, err := workspace.Open(tmsfBG, db)
	if err != nil {
		return err
	}
	if s.got, err = reg.Get(tmsfBG, s.added.ID); err != nil {
		return err
	}
	if s.list, err = reg.List(tmsfBG); err != nil {
		return err
	}
	s.rev, err = reg.Revision(tmsfBG)
	return err
}

func (s *tmsfState) theWorkspaceRowIsReadBackWithIdenticalFields() error {
	if s.got != s.added {
		return fmt.Errorf("after restart\n got %+v\nwant %+v", s.got, s.added)
	}
	if len(s.list) != 1 || s.list[0] != s.added {
		return fmt.Errorf("list after restart = %+v", s.list)
	}
	if s.rev != 1 {
		return fmt.Errorf("revision after restart = %d, want 1", s.rev)
	}
	return nil
}

// --- Scenario: Changed fold version rebuilds ---

// tmsfFold counts events by step version, so a row of one version read as
// another would show.
func tmsfFold(version int) views.Definition[int] {
	return views.Definition[int]{Key: tmsfFoldKey, Version: version, Stored: true,
		Init:  func(session.Header) int { return 0 },
		Apply: func(n int, _ event.Event) (int, bool) { return n + version, true }}
}

// boot is one process: a sqlite store over the home and a session service
// folding with reg.
func (s *tmsfState) boot(reg *views.Registry) (*sqlite.Store, session.Service, func() error, error) {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	st, err := sqlite.OpenHome(tmsfBG, s.home)
	if err != nil {
		root.Dispose()
		return nil, nil, nil, err
	}
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		root.Dispose()
		return st.Close(tmsfBG)
	}
	s.cleanup(func() { _ = stop() })
	return st, session.NewService(root, st, session.Config{Folder: reg}), stop, nil
}

func tmsfCheckpoints(st store.Store, id session.ID) (map[int]store.Checkpoint, error) {
	r, err := st.OpenRead(tmsfBG, string(id))
	if err != nil {
		return nil, err
	}
	defer r.Close(tmsfBG)
	cps, err := r.Checkpoints(tmsfBG)
	if err != nil {
		return nil, err
	}
	out := map[int]store.Checkpoint{}
	for _, c := range cps {
		if c.Key == tmsfFoldKey {
			out[c.StateVersion] = c
		}
	}
	return out, nil
}

func tmsfDraft(v string) (event.Draft, error) {
	return event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: v})
}

func (s *tmsfState) aStoredFoldAtVersion1Over20Events() error {
	if err := s.newHome(); err != nil {
		return err
	}
	reg := views.New()
	if _, err := views.Register(reg, tmsfFold(1)); err != nil {
		return err
	}
	st, svc, stop, err := s.boot(reg)
	if err != nil {
		return err
	}
	h, err := svc.Create(tmsfBG, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		return err
	}
	s.sessionID = h.ID()
	for i := range tmsfEvents {
		d, err := tmsfDraft(strconv.Itoa(i))
		if err != nil {
			return err
		}
		if _, err := h.Append(tmsfBG, d); err != nil {
			return err
		}
	}
	if err := h.Close(tmsfBG); err != nil {
		return err
	}
	cps, err := tmsfCheckpoints(st, s.sessionID)
	if err != nil {
		return err
	}
	v1, ok := cps[1]
	if !ok || v1.Seq != tmsfEvents-1 || v1.State != strconv.Itoa(tmsfEvents) {
		return fmt.Errorf("version 1 row = %+v (present %v), want seq %d state %d", v1, ok, tmsfEvents-1, tmsfEvents)
	}
	s.v1 = v1
	return stop()
}

func (s *tmsfState) theFoldIsReRegisteredAtVersion2AndTheStoreOpens() error {
	reg := views.New()
	fold, err := views.Register(reg, tmsfFold(2))
	if err != nil {
		return err
	}
	st, svc, _, err := s.boot(reg)
	if err != nil {
		return err
	}
	h, _, err := svc.Open(tmsfBG, nil, s.sessionID, session.OpenOptions{})
	if err != nil {
		return err
	}
	s.cleanup(func() { _ = h.Close(tmsfBG) })
	s.head = int64(h.Len())
	cps, err := tmsfCheckpoints(st, s.sessionID)
	if err != nil {
		return err
	}
	s.v2, s.v2Found = cps[2]
	snap, err := reg.Read(tmsfBG, h)
	if err != nil {
		return err
	}
	if s.readValue, err = fold.Get(snap); err != nil {
		return err
	}
	s.readAsOf = int64(snap.AsOf)
	return nil
}

func (s *tmsfState) theStoredStateIsRebuiltRecordsVersion2AndItsSequenceEqualsTheLogHead() error {
	if s.head != tmsfEvents {
		return fmt.Errorf("reopened log head = %d, want %d", s.head, tmsfEvents)
	}
	if !s.v2Found {
		return errors.New("no version 2 row after the store opened: the fold was not rebuilt")
	}
	if s.v2.StateVersion != 2 {
		return fmt.Errorf("rebuilt row version = %d, want 2", s.v2.StateVersion)
	}
	// Seq is the last folded event; the log head is the next sequence.
	if int64(s.v2.Seq) != s.head-1 {
		return fmt.Errorf("rebuilt row seq = %d, log head = %d (want seq %d)", s.v2.Seq, s.head, s.head-1)
	}
	if want := strconv.Itoa(2 * tmsfEvents); s.v2.State != want {
		return fmt.Errorf("rebuilt row state = %q, want %q (version 1 state %q)", s.v2.State, want, s.v1.State)
	}
	if s.readValue != 2*tmsfEvents || s.readAsOf != s.head {
		return fmt.Errorf("read = %d as of %d, want %d as of %d", s.readValue, s.readAsOf, 2*tmsfEvents, s.head)
	}
	return nil
}

func InitializeScenario_machine_state_views_and_spill_typed_machine_state_and_stored_folds(ctx *godog.ScenarioContext) {
	s := &tmsfState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.close()
		return c, err
	})
	ctx.Step(`^a workspace registered in a temp home$`, s.aWorkspaceRegisteredInATempHome)
	ctx.Step(`^the store is closed and reopened$`, s.theStoreIsClosedAndReopened)
	ctx.Step(`^the workspace row is read back with identical fields$`, s.theWorkspaceRowIsReadBackWithIdenticalFields)
	ctx.Step(`^a stored fold at version 1 over 20 events$`, s.aStoredFoldAtVersion1Over20Events)
	ctx.Step(`^the fold is re-registered at version 2 and the store opens$`, s.theFoldIsReRegisteredAtVersion2AndTheStoreOpens)
	ctx.Step(`^the stored state is rebuilt, records version 2 and its sequence equals the log head$`, s.theStoredStateIsRebuiltRecordsVersion2AndItsSequenceEqualsTheLogHead)
}

func TestE2E_machine_state_views_and_spill_typed_machine_state_and_stored_folds(t *testing.T) {
	tmsfOuterT = t
	defer func() { tmsfOuterT = nil }()
	suite := godog.TestSuite{
		Name:                "machine_state_views_and_spill_typed_machine_state_and_stored_folds",
		ScenarioInitializer: InitializeScenario_machine_state_views_and_spill_typed_machine_state_and_stored_folds,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"machine_state_views_and_spill_typed_machine_state_and_stored_folds.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
