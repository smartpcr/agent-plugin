//go:build e2e

package e2e

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/storetest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// sdmaOuterT is the suite's *testing.T: storetest.Run and the test-kit temp home
// need a real *testing.T, which godog's per-scenario TestingT does not expose.
var sdmaOuterT *testing.T

// The append budgets of fact F-2 (DH-T-STORE-18), compiled in so no environment
// can relax them. They are this suite's own copy, not shared with
// budgetAppendP95 and budgetAppendFlushP95 in
// internal/session/store/sqlite/budget_test.go: nothing keeps the two in step,
// so a change to F-2 must update both. underBudget also checks each figure
// against the one the feature file names.
const (
	sdmaBudgetAppendP95      = 10 * time.Millisecond
	sdmaBudgetAppendFlushP95 = 50 * time.Millisecond
)

var sdmaBG = context.Background()

// sdmaDBPath is the dsh.db of a fresh home: a fresh directory under
// DSH_E2E_STORE_HOME when the compose run mounts one, else a test-kit temp home
// under t.TempDir() (the embedded, ephemeral gate path).
func sdmaDBPath(t *testing.T) string {
	t.Helper()
	if base := os.Getenv("DSH_E2E_STORE_HOME"); base != "" {
		dir, err := os.MkdirTemp(base, "e2e-case-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		return filepath.Join(dir, "dsh.db")
	}
	return temphome.New(t).DB()
}

func sdmaOpen(t *testing.T, path string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(sdmaBG, path)
	if err != nil {
		t.Fatalf("sqlite.Open(%s): %v", path, err)
	}
	t.Cleanup(func() {
		if err := s.Close(sdmaBG); err != nil {
			t.Errorf("Store.Close: %v", err)
		}
	})
	return s
}

// sdmaFresh is a new connection to path sharing nothing with the store.
func sdmaFresh(path string) (*sql.DB, error) { return sql.Open("sqlite", path) }

var (
	sdmaMsMu sync.Mutex
	sdmaMs   = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
)

func sdmaCall(seq store.Seq) (store.Record, error) {
	d, err := event.ToolCall.Draft(event.ToolCallPayload{CallID: fmt.Sprintf("c%d", seq), Name: "grep", Arguments: `{"q":"x"}`})
	if err != nil {
		return store.Record{}, err
	}
	sdmaMsMu.Lock()
	sdmaMs++
	ms := sdmaMs
	sdmaMsMu.Unlock()
	return d.Stamp(seq, ms)
}

func sdmaP(d []time.Duration, q float64) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[int(float64(len(s)-1)*q)]
}

// sdmaRun runs fn as a subtest of the outer test and reports its failure as a
// step error, so assertions made with t also fail the scenario.
func sdmaRun(name string, fn func(t *testing.T)) error {
	if sdmaOuterT == nil {
		return errors.New("no outer *testing.T; run through TestE2E_sqlite_store_leases_and_recovery_sqlite_driver_measurement_and_atomic_appends")
	}
	if !sdmaOuterT.Run(name, fn) {
		return fmt.Errorf("subtest %s failed; see its output", name)
	}
	return nil
}

type sdmaState struct {
	// conformance
	mu        sync.Mutex
	passed    map[string]map[string]bool // provider -> case -> passed
	notWAL    []string
	sqliteDBs int
	dbPaths   map[string]string // dsh.db path -> first case that opened it
	dupPaths  []string
	ranSuite  bool

	// atomic
	path      string
	st        *sqlite.Store
	w         store.Writer
	batch     store.Batch
	appendErr error
	cleanups  []func()

	// budget
	appendOnly, both []time.Duration
	finalSum         store.Summary
	statErr          error
}

func (s *sdmaState) record(provider string, t *testing.T) {
	name := t.Name()
	name = name[strings.LastIndex(name, "/")+1:]
	t.Cleanup(func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.passed[provider] == nil {
			s.passed[provider] = map[string]bool{}
		}
		s.passed[provider][name] = !t.Failed()
	})
}

func (s *sdmaState) conformanceFactory() error {
	if sdmaOuterT == nil {
		return errors.New("no outer *testing.T")
	}
	s.passed = map[string]map[string]bool{}
	return nil
}

func (s *sdmaState) sqliteFactory(t *testing.T) store.Store {
	s.record("sqlite", t)
	path := sdmaDBPath(t)
	// Registered before the store's own cleanup, so it runs after Close.
	t.Cleanup(func() {
		db, err := sdmaFresh(path)
		if err != nil {
			t.Errorf("fresh open of %s: %v", path, err)
			return
		}
		defer db.Close()
		var mode string
		if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
			s.mu.Lock()
			s.notWAL = append(s.notWAL, fmt.Sprintf("%s: %q %v", t.Name(), mode, err))
			s.mu.Unlock()
		}
	})
	st := sdmaOpen(t, path)
	s.mu.Lock()
	s.sqliteDBs++
	if s.dbPaths == nil {
		s.dbPaths = map[string]string{}
	}
	if first, seen := s.dbPaths[path]; seen {
		s.dupPaths = append(s.dupPaths, fmt.Sprintf("%s reused by %s (first opened by %s)", path, t.Name(), first))
	} else {
		s.dbPaths[path] = t.Name()
	}
	s.mu.Unlock()
	return st
}

func (s *sdmaState) runSuites() error {
	s.ranSuite = true
	// Both suites run; failures surface through the subtests and the Then steps.
	_ = sdmaOuterT.Run("storetest-memstore", func(t *testing.T) {
		storetest.Run(t, func(t *testing.T) store.Store { s.record("memstore", t); return memstore.New() })
	})
	_ = sdmaOuterT.Run("storetest-sqlite", func(t *testing.T) {
		storetest.Run(t, s.sqliteFactory)
	})
	return nil
}

func (s *sdmaState) sqlitePassesWhatMemstorePasses() error {
	if !s.ranSuite {
		return errors.New("the suites did not run")
	}
	mem, lite := s.passed["memstore"], s.passed["sqlite"]
	if len(mem) == 0 {
		return errors.New("the memstore suite ran no cases")
	}
	var memPassed, missing []string
	for name, ok := range mem {
		if !ok {
			continue
		}
		memPassed = append(memPassed, name)
		if !lite[name] {
			missing = append(missing, name)
		}
	}
	slices.Sort(missing)
	if len(memPassed) == 0 {
		return errors.New("no case passed on memstore")
	}
	if len(missing) > 0 {
		return fmt.Errorf("cases passing on memstore but not on SQLite: %v", missing)
	}
	return nil
}

func (s *sdmaState) everyCaseOwnWAL() error {
	if s.sqliteDBs != len(s.passed["sqlite"]) || s.sqliteDBs == 0 {
		return fmt.Errorf("opened %d SQLite stores for %d cases", s.sqliteDBs, len(s.passed["sqlite"]))
	}
	if len(s.dupPaths) > 0 {
		return fmt.Errorf("SQLite cases shared a dsh.db: %v", s.dupPaths)
	}
	if len(s.dbPaths) != s.sqliteDBs {
		return fmt.Errorf("opened %d SQLite stores but only %d distinct dsh.db paths", s.sqliteDBs, len(s.dbPaths))
	}
	if len(s.notWAL) > 0 {
		return fmt.Errorf("databases not in WAL mode: %v", s.notWAL)
	}
	return nil
}

// --- atomic append ---

func (s *sdmaState) emptySession(id string) error {
	return sdmaRun("atomic-setup", func(t *testing.T) {
		s.path = sdmaDBPath(sdmaOuterT)
		st, err := sqlite.Open(sdmaBG, s.path)
		if err != nil {
			t.Fatalf("sqlite.Open: %v", err)
		}
		s.st = st
		s.cleanups = append(s.cleanups, func() { st.Close(sdmaBG) })
		w, err := st.Create(sdmaBG, store.Header{ID: id}, store.CreateOptions{})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		s.w = w
		s.cleanups = append(s.cleanups, func() { w.Close(sdmaBG) })
		if n, _, err := s.countRows(id); err != nil || n != 0 {
			t.Fatalf("new session holds %d events (%v), want 0", n, err)
		}
	})
}

// countRows is what a fresh connection reads of session id: its events and its
// next seq.
func (s *sdmaState) countRows(id string) (events, next int64, err error) {
	db, err := sdmaFresh(s.path)
	if err != nil {
		return 0, 0, err
	}
	defer db.Close()
	if err := db.QueryRow(`SELECT count(*) FROM events JOIN sessions ON events.session = sessions.uid WHERE sessions.id = ?`, id).Scan(&events); err != nil {
		return 0, 0, err
	}
	if err := db.QueryRow(`SELECT next_seq FROM sessions WHERE id = ?`, id).Scan(&next); err != nil {
		return 0, 0, err
	}
	return events, next, nil
}

// refuseSeq installs a trigger so the database itself, not Writer.Append's
// checks, refuses one row after earlier rows of the batch were inserted.
func (s *sdmaState) refuseSeq(seq int) error {
	db, err := sdmaFresh(s.path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(fmt.Sprintf(`CREATE TRIGGER e2e_refuse BEFORE INSERT ON events WHEN NEW.seq = %d
		BEGIN SELECT RAISE(ABORT, 'e2e: the third row is refused'); END`, seq))
	return err
}

func (s *sdmaState) appendThree(a, b, c int) error {
	recs := make([]store.Record, 0, 3)
	for _, seq := range []int{a, b, c} {
		r, err := sdmaCall(store.Seq(seq))
		if err != nil {
			return err
		}
		recs = append(recs, r)
	}
	s.batch = store.Batch{First: store.Seq(a), Records: recs}
	s.appendErr = s.w.Append(sdmaBG, s.batch)
	return nil
}

func (s *sdmaState) appendFailsWith(code string) error {
	if s.appendErr == nil {
		return errors.New("the append succeeded, want a refusal")
	}
	if !errors.Is(s.appendErr, &store.Error{Code: store.Code(code)}) || !strings.Contains(s.appendErr.Error(), "e2e: the third row") {
		return fmt.Errorf("Append = %v, want %s from the test trigger", s.appendErr, code)
	}
	return nil
}

func (s *sdmaState) freshHolds(want int64) func() error {
	return func() error {
		n, next, err := s.countRows("atomic")
		if err != nil {
			return err
		}
		if n != want || next != want {
			return fmt.Errorf("a fresh read holds %d events, next seq %d; want %d, %d", n, next, want, want)
		}
		return nil
	}
}

func (s *sdmaState) nextSeqStill(want int) error {
	sum, err := s.st.Stat(sdmaBG, "atomic")
	if err != nil {
		return err
	}
	if sum.NextSeq != store.Seq(want) || sum.Durable != store.Seq(want) {
		return fmt.Errorf("Stat = NextSeq %d, Durable %d; want %d", sum.NextSeq, sum.Durable, want)
	}
	return nil
}

func (s *sdmaState) retryWithoutRefusal() error {
	db, err := sdmaFresh(s.path)
	if err != nil {
		return err
	}
	if _, err := db.Exec(`DROP TRIGGER e2e_refuse`); err != nil {
		db.Close()
		return err
	}
	db.Close()
	if err := s.w.Append(sdmaBG, s.batch); err != nil {
		return fmt.Errorf("append without the refusal: %w", err)
	}
	return s.w.Flush(sdmaBG)
}

// --- budget ---

func (s *sdmaState) pinnedDriver() error {
	// The driver registered under "sqlite" is the one the store opens with; a
	// throwaway in-memory database proves it is compiled in, no system SQLite.
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return err
	}
	defer db.Close()
	var v string
	if err := db.QueryRow(`SELECT sqlite_version()`).Scan(&v); err != nil {
		return fmt.Errorf("embedded driver: %w", err)
	}
	fmt.Printf("F-2 driver modernc.org/sqlite embedded, SQLite %s\n", v)
	return nil
}

// runBudget is an independent re-measurement, not a run of TestBudgetStoreAppend:
// it repeats that test's append loop (n single-event appends, each followed by a
// flush, the append timed apart) against a fresh dsh.db from this suite, and does
// not repeat its open/reopen timings. The step keeps the test's name so the
// feature reads against the unit test it mirrors.
func (s *sdmaState) runBudget(n int) error {
	return sdmaRun("budget-store-append", func(t *testing.T) {
		st := sdmaOpen(t, sdmaDBPath(t))
		w, err := st.Create(sdmaBG, store.Header{ID: "budget"}, store.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		recs := make([]store.Record, n)
		for i := range recs {
			if recs[i], err = sdmaCall(store.Seq(i)); err != nil {
				t.Fatal(err)
			}
		}
		s.appendOnly = make([]time.Duration, 0, n)
		s.both = make([]time.Duration, 0, n)
		for i, rec := range recs {
			start := time.Now()
			if err := w.Append(sdmaBG, store.Batch{First: store.Seq(i), Records: []store.Record{rec}}); err != nil {
				t.Fatal(err)
			}
			mid := time.Now()
			if err := w.Flush(sdmaBG); err != nil {
				t.Fatal(err)
			}
			end := time.Now()
			s.appendOnly = append(s.appendOnly, mid.Sub(start))
			s.both = append(s.both, end.Sub(start))
		}
		if err := w.Close(sdmaBG); err != nil {
			t.Fatal(err)
		}
		s.finalSum, s.statErr = st.Stat(sdmaBG, "budget")
	})
}

func (s *sdmaState) everyAppendDurable() error {
	n := store.Seq(len(s.both))
	if n == 0 {
		return errors.New("no append was measured")
	}
	if s.statErr != nil || s.finalSum.NextSeq != n || s.finalSum.Durable != n {
		return fmt.Errorf("Stat = %+v, %v; want %d events, all durable", s.finalSum, s.statErr, n)
	}
	return nil
}

func (s *sdmaState) underBudget(which string, ms int) func() error {
	return func() error {
		d, budget := s.both, sdmaBudgetAppendFlushP95
		if which == "append" {
			d, budget = s.appendOnly, sdmaBudgetAppendP95
		}
		if want := time.Duration(ms) * time.Millisecond; want != budget {
			return fmt.Errorf("the feature names %v but the compiled %s budget is %v", want, which, budget)
		}
		if len(d) == 0 {
			return errors.New("no append was measured")
		}
		if got := sdmaP(d, .95); got > budget {
			return fmt.Errorf("%s p95 = %v, over its budget of %v", which, got, budget)
		}
		return nil
	}
}

func (s *sdmaState) printFigures() error {
	for _, m := range []struct {
		name string
		d    []time.Duration
	}{{"append", s.appendOnly}, {"append+flush", s.both}} {
		line := fmt.Sprintf("F-2 %-14s n=%4d p50=%v p95=%v p99=%v max=%v", m.name, len(m.d), sdmaP(m.d, .5), sdmaP(m.d, .95), sdmaP(m.d, .99), sdmaP(m.d, 1))
		fmt.Println(line)
		sdmaOuterT.Log(line)
	}
	return nil
}

func InitializeScenario_sqlite_store_leases_and_recovery_sqlite_driver_measurement_and_atomic_appends(ctx *godog.ScenarioContext) {
	s := &sdmaState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = sdmaState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		for i := len(s.cleanups) - 1; i >= 0; i-- {
			s.cleanups[i]()
		}
		return c, nil
	})

	ctx.Step(`^storetest\.Run with a factory opening dsh\.db in a test-kit temp home$`, s.conformanceFactory)
	ctx.Step(`^the suite runs on memstore and on SQLite$`, s.runSuites)
	ctx.Step(`^every case that passes on memstore passes on SQLite$`, s.sqlitePassesWhatMemstorePasses)
	ctx.Step(`^every SQLite case opened its own dsh\.db in WAL mode$`, s.everyCaseOwnWAL)

	ctx.Step(`^a SQLite store with session "([^"]*)" holding no events$`, s.emptySession)
	ctx.Step(`^the database refuses the insert of the event at seq (\d+)$`, s.refuseSeq)
	ctx.Step(`^an append of three events at seqs (\d+), (\d+) and (\d+) runs$`, s.appendThree)
	ctx.Step(`^the append fails with "([^"]*)"$`, s.appendFailsWith)
	ctx.Step(`^a fresh read of the database holds none of the three rows$`, s.freshHolds(0))
	ctx.Step(`^the session's next seq is still (\d+)$`, s.nextSeqStill)
	ctx.Step(`^the refusal is removed and the same append runs again$`, s.retryWithoutRefusal)
	ctx.Step(`^a fresh read of the database holds all three rows$`, s.freshHolds(3))

	ctx.Step(`^the pinned modernc\.org/sqlite driver$`, s.pinnedDriver)
	ctx.Step(`^TestBudgetStoreAppend runs (\d+) single-event appends with flush$`, s.runBudget)
	ctx.Step(`^every append is durable$`, s.everyAppendDurable)
	ctx.Step(`^the append\+flush p95 is under the compiled budget of (\d+) ms$`, func(ms int) error { return s.underBudget("append+flush", ms)() })
	ctx.Step(`^the append p95 is under the compiled budget of (\d+) ms$`, func(ms int) error { return s.underBudget("append", ms)() })
	ctx.Step(`^the measured figures are printed for the evidence record$`, s.printFigures)
}

func TestE2E_sqlite_store_leases_and_recovery_sqlite_driver_measurement_and_atomic_appends(t *testing.T) {
	sdmaOuterT = t
	defer func() { sdmaOuterT = nil }()
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_sqlite_store_leases_and_recovery_sqlite_driver_measurement_and_atomic_appends,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"sqlite_store_leases_and_recovery_sqlite_driver_measurement_and_atomic_appends.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
