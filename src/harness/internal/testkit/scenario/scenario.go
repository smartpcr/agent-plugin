// Package scenario is the scenario helper (DH-D-UTIL-8, C55; DH-ADR-UTIL-8). A
// real-environment scenario is a Go test behind the scenario build tag that calls
// Run with its DH-E ID, what it needs and its body. Run checks the needs: an unmet
// need gives a skipped row with the reason, or a failure, "required scenario
// skipped", when the ID is on DSH_REQUIRE_SCENARIOS (M2-DEC-3, INV-13). A body
// runs with the built binary named by DSH_BIN, a scratch home and a scratch
// workspace of its own (M2-C-1), and a deadline. Every outcome is one JSON line
// appended to DSH_SCENARIO_REPORT (Row); Check fails a run whose report has no
// passed row for a required ID, which also catches a scenario that never ran.
//
// go test prints a skip as ok, so the report, not the exit status, is the record.
// Scenario files import only this package, the test kit's launcher, the standard
// library and src/sdk/... (architecture.md sec. 3.4 rule 5; the scenarioimport
// analyzer of tools/harness-gate).
package scenario

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/dshbin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// The environment the helper reads (impl.md sec. 7.2, fixed here by DH-S-16).
const (
	// BinEnv names the built dsh under test; a scenario never builds its own
	// binary when it is set (DH-ADR-TOOLCHAIN-1).
	BinEnv = "DSH_BIN"
	// RequireEnv lists, separated by commas or white space, the scenario IDs the
	// run must pass (DH-ADR-TOOLCHAIN-6).
	RequireEnv = "DSH_REQUIRE_SCENARIOS"
	// ReportEnv is the file every outcome is appended to; unset, the row goes to
	// a file in the test's temporary directory, whose path is logged.
	ReportEnv = "DSH_SCENARIO_REPORT"
)

// DefaultDeadline is a scenario's deadline when Needs gives none: impl.md sec.
// 7.2's starting value for the model and account environments.
const DefaultDeadline = 15 * time.Minute

// RequiredNotRun is the code of Check's failure (util-test-support.md sec. 6.2).
const RequiredNotRun = "scenario/required-not-run"

// Needs is what a scenario needs before its body may start.
type Needs struct {
	// Bin: DSH_BIN names the built dsh (S.Bin).
	Bin bool
	// Env are variables that must be set and not blank.
	Env []string
	// Programs must be found on PATH.
	Programs []string
	// OS lists the operating systems (GOOS values) the scenario runs on; empty is
	// every one.
	OS []string
	// Deadline bounds the body; zero is DefaultDeadline. A body that is still
	// running at its deadline has failed, not skipped.
	Deadline time.Duration
}

// Result is a row's outcome. Only Passed counts as evidence.
type Result string

const (
	Passed  Result = "passed"
	Failed  Result = "failed"
	Skipped Result = "skipped"
)

// RowKind marks a report line as a scenario row; Check ignores any other line.
const RowKind = "scenario"

// Row is one scenario's evidence statement.
type Row struct {
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Result Result `json:"result"`
	Reason string `json:"reason,omitempty"`
	// Build is the SHA-256 of the binary under test, when the scenario needs one.
	Build      string `json:"build,omitempty"`
	OS         string `json:"os"`
	DurationMS int64  `json:"durationMs"`
}

// S is one running scenario.
type S struct {
	testing.TB
	// ID is the scenario's DH-E ID.
	ID string
	// Bin is the built dsh (DSH_BIN) when Needs.Bin is set, else empty.
	Bin string
	// Home is a fresh harness home of this scenario alone, removed at cleanup.
	Home string
	// Workspace is a fresh, empty working directory, removed at cleanup.
	Workspace string

	ctx      context.Context
	mu       sync.Mutex
	hosts    []*launcher.Host
	cleanups []func()
}

// Cleanup registers f to run when the scenario ends, before Run writes its row,
// so a cleanup that fails fails the scenario's row too. Cleanups run last first.
func (s *S) Cleanup(f func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanups = append(s.cleanups, f)
}

// TempDir is a fresh directory removed by the scenario's cleanup (Cleanup).
func (s *S) TempDir() string {
	s.Helper()
	d, err := os.MkdirTemp("", "dsh-scenario-")
	if err != nil {
		s.Fatalf("scenario: %v", err)
	}
	s.Cleanup(func() {
		// A process just stopped may hold a file for a moment on Windows.
		for i := 0; ; i++ {
			err := os.RemoveAll(d)
			if err == nil {
				return
			}
			if i == 50 {
				s.Errorf("scenario: removing %s: %v", d, err)
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return d
}

// cleanup runs the scenario's cleanups, last first, each on a goroutine of its
// own so a FailNow in one ends that cleanup alone.
func (s *S) cleanup() {
	for {
		s.mu.Lock()
		n := len(s.cleanups)
		if n == 0 {
			s.mu.Unlock()
			return
		}
		f := s.cleanups[n-1]
		s.cleanups = s.cleanups[:n-1]
		s.mu.Unlock()
		done := make(chan struct{})
		go func() {
			defer close(done)
			f()
		}()
		<-done
	}
}

// Context ends at the scenario's deadline, with ErrDeadline as its cause.
func (s *S) Context() context.Context { return s.ctx }

// ErrDeadline is the cause of S.Context at the deadline.
var ErrDeadline = errors.New("scenario: deadline reached")

// Program is p with the built binary as its program and the scenario's home and
// workspace applied: DSH_HOME names s.Home (after p.Env, so an entry there cannot
// point the binary at another home) and an empty Dir is s.Workspace.
func (s *S) Program(env []string, args ...string) launcher.Program {
	s.Helper()
	if s.Bin == "" {
		s.Fatalf("%s: the scenario starts dsh but does not declare Needs.Bin", s.ID)
	}
	return launcher.Program{Argv: append([]string{s.Bin}, args...), Env: append(slices.Clone(env), "DSH_HOME="+s.Home), Dir: s.Workspace}
}

// Spawn starts the built binary with args in the scenario's home and workspace
// (Program); a tree still running at cleanup is stopped whole.
func (s *S) Spawn(env []string, args ...string) *launcher.Host {
	s.Helper()
	h := launcher.Spawn(s, s.Program(env, args...))
	s.mu.Lock()
	s.hosts = append(s.hosts, h)
	s.mu.Unlock()
	return h
}

// stopHosts stops, whole, every tree Spawn started, so a body blocked on one of
// them returns at the deadline.
func (s *S) stopHosts() {
	s.mu.Lock()
	hosts := slices.Clone(s.hosts)
	s.mu.Unlock()
	for _, h := range hosts {
		ctx, cancel := context.WithTimeout(context.Background(), 3*launcher.Grace)
		_ = h.Process.StopTree(ctx, 0)
		cancel()
	}
}

// abandonAfter is how long Run waits, after the deadline and the stop of the
// scenario's trees, for the body to return before it fails the scenario and
// leaves the body's goroutine behind.
var abandonAfter = 30 * time.Second

// Run declares and runs scenario id. The body runs on a goroutine of its own and
// Run returns when it has returned, or at its deadline: there Run stops the trees
// Spawn started, waits abandonAfter for the body and, if it still runs, fails the
// scenario without waiting further (the deadline bounds the scenario, not only
// S.Context). Every path writes exactly one row, after the scenario's cleanups
// (S.Cleanup, its home and workspace, the trees Spawn started) have run, so a
// cleanup that fails turns a passed row into a failed one.
func Run(t testing.TB, id string, needs Needs, body func(s *S)) {
	t.Helper()
	own(t)
	start := time.Now()
	row := Row{Kind: RowKind, ID: id, OS: runtime.GOOS}
	var s *S
	// Every path ends here, a Fatalf or SkipNow on this goroutine included.
	defer func() {
		if s != nil {
			failed := t.Failed()
			s.cleanup()
			if !failed && t.Failed() && row.Result != Failed {
				row.Result, row.Reason = Failed, "a cleanup of the scenario failed"
			}
		}
		if row.Result == "" {
			row.Result, row.Reason = Failed, "the scenario ended before its body returned"
		}
		row.DurationMS = time.Since(start).Milliseconds()
		write(t, row)
	}()
	required := Required(id)
	if reason := Unmet(needs); reason != "" {
		row.Reason = reason
		if required {
			row.Result = Failed
			t.Fatalf("%s: required scenario skipped: %s (listed in %s)", id, reason, RequireEnv)
		}
		row.Result = Skipped
		t.Skipf("%s: %s", id, reason)
	}
	s = &S{TB: t, ID: id}
	if needs.Bin {
		s.Bin = os.Getenv(BinEnv)
		sum, err := digest(s.Bin)
		if err != nil {
			row.Result, row.Reason = Failed, fmt.Sprintf("%s=%s: %v", BinEnv, s.Bin, err)
			t.Fatalf("%s: %s", id, row.Reason)
		}
		row.Build = sum
	}
	s.Home = temphome.New(s).Root()
	s.Workspace = s.TempDir()
	deadline := needs.Deadline
	if deadline <= 0 {
		deadline = DefaultDeadline
	}
	ctx, cancel := context.WithTimeoutCause(context.Background(), deadline, ErrDeadline)
	defer cancel()
	s.ctx = ctx
	done := make(chan struct{})
	var returned bool
	var panicked any
	var stack []byte
	go func() {
		defer close(done)
		// A FailNow or SkipNow in the body ends this goroutine (runtime.Goexit)
		// and leaves returned false; a panic is recovered and recorded.
		defer func() {
			if v := recover(); v != nil {
				panicked, stack = v, debug.Stack()
			}
		}()
		body(s)
		returned = true
	}()
	late, abandoned := false, false
	select {
	case <-done:
	case <-ctx.Done():
		late = true
		s.stopHosts()
		select {
		case <-done:
		case <-time.After(abandonAfter):
			abandoned = true
		}
	}
	switch {
	case late:
		row.Result, row.Reason = Failed, fmt.Sprintf("deadline %s reached", deadline)
		if abandoned {
			row.Reason += fmt.Sprintf("; the body did not return within %s after it", abandonAfter)
		}
		t.Errorf("%s: the body ran past its deadline of %s", id, deadline)
	case panicked != nil:
		row.Result, row.Reason = Failed, fmt.Sprintf("the body panicked: %v", panicked)
		t.Errorf("%s: the body panicked: %v\n%s", id, panicked, stack)
	case t.Failed():
		row.Result = Failed
	case t.Skipped():
		// A scenario does not skip itself; the needs decide. A required one
		// that skipped anyway fails.
		row.Result, row.Reason = Skipped, "the body skipped"
		if required {
			row.Result = Failed
			t.Errorf("%s: required scenario skipped: the body skipped (listed in %s)", id, RequireEnv)
		}
	case !returned:
		row.Result, row.Reason = Failed, "the body ended without returning"
		t.Errorf("%s: %s", id, row.Reason)
	default:
		row.Result = Passed
	}
	// A body ended by FailNow or SkipNow, or by a panic, ends its caller the same
	// way, as it would had it run on the test's goroutine.
	switch {
	case abandoned, !returned && t.Failed():
		t.FailNow()
	case !returned && t.Skipped():
		t.SkipNow()
	}
}

// Required reports whether id is on DSH_REQUIRE_SCENARIOS.
func Required(id string) bool {
	return slices.Contains(SplitRequired(os.Getenv(RequireEnv)), id)
}

// SplitRequired splits a DSH_REQUIRE_SCENARIOS value on commas and white space.
func SplitRequired(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
}

// Unmet is the reason needs are not met in this environment, empty when they are.
func Unmet(needs Needs) string {
	var reasons []string
	if len(needs.OS) > 0 && !slices.Contains(needs.OS, runtime.GOOS) {
		reasons = append(reasons, fmt.Sprintf("runs on %s, not %s", strings.Join(needs.OS, ", "), runtime.GOOS))
	}
	if needs.Bin && strings.TrimSpace(os.Getenv(BinEnv)) == "" {
		reasons = append(reasons, BinEnv+" is not set: the built dsh")
	}
	for _, name := range needs.Env {
		if strings.TrimSpace(os.Getenv(name)) == "" {
			reasons = append(reasons, name+" is not set")
		}
	}
	env := subprocess.Env(os.Environ())
	for _, prog := range needs.Programs {
		if _, err := subprocess.Lookup(prog, env); err != nil {
			reasons = append(reasons, prog+" is not on PATH")
		}
	}
	return strings.Join(reasons, "; ")
}

// Need returns the run-time input name, which the operator of a run sets to
// describe the real environment. A missing input fails the scenario naming the
// variable: a scenario that cannot run never skips (DH-O-15).
func Need(t testing.TB, name, what string) string {
	t.Helper()
	tr := track(t)
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		tr.fail(fmt.Sprintf("%s is not set: %s", name, what))
		t.Fatalf("%s is not set: %s", name, what)
	}
	return v
}

// Bin, Need and Start are the shared entry points of a scenario that does not
// call Run: the first of them a test calls tracks it (track), so the test writes
// exactly one row when it ends.

// Bin is the built dsh a scenario starts: the file DSH_BIN names when it is set
// (a name that is not a file fails t), else this checkout's ./cmd/dsh built once
// per test binary (dshbin; the package's TestMain calls Main). A test that set a
// build variable (dshbin.VariantEnv: GOOS, GOARCH, CGO_ENABLED, GOFLAGS, ...) gets
// the build of that variant, never DSH_BIN or the default build.
func Bin(t testing.TB) string {
	t.Helper()
	tr := track(t)
	p := strings.TrimSpace(os.Getenv(BinEnv))
	if p == "" || !dshbin.Default() {
		p = dshbin.Path(t)
	}
	sum, err := digest(p)
	if err != nil {
		tr.fail(fmt.Sprintf("%s=%s: %v", BinEnv, p, err))
		t.Fatalf("%s=%s: %v", BinEnv, p, err)
	}
	tr.built(sum)
	return p
}

// Start launches p, the built binary in SDK host mode, against a scratch home of
// its own (launcher.Start: DSH_HOME is applied after p.Env and the home is removed
// at cleanup), so a host scenario gets the same isolation as a Run body.
func Start(t testing.TB, p launcher.Program) *launcher.Session {
	t.Helper()
	track(t)
	return launcher.Start(t, p)
}

// digest is the SHA-256 of the regular file at p, in hex.
func digest(p string) (string, error) {
	if !filepath.IsAbs(p) {
		return "", errors.New("not an absolute path")
	}
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil {
		return "", err
	} else if !info.Mode().IsRegular() {
		return "", errors.New("not a regular file")
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// reportMu serializes this process's appends to a report.
var reportMu sync.Mutex

// write appends row to the report and logs it. A report that cannot be written
// fails t: a run without its record holds no evidence.
func write(t testing.TB, row Row) {
	t.Helper()
	line, err := json.Marshal(row)
	if err != nil {
		t.Errorf("scenario: %v", err)
		return
	}
	p := os.Getenv(ReportEnv)
	if p == "" {
		p = filepath.Join(t.TempDir(), "scenario-report.jsonl")
	}
	reportMu.Lock()
	defer reportMu.Unlock()
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err == nil {
		_, err = f.Write(append(line, '\n'))
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		t.Errorf("scenario: writing the report %s: %v", p, err)
		return
	}
	t.Logf("scenario report %s: %s", p, line)
}

// ReadReport reads the scenario rows of a report; other lines are skipped, so a
// scenario's own JSON lines may share the file.
func ReadReport(r io.Reader) ([]Row, error) {
	var rows []Row
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var row Row
		if json.Unmarshal(sc.Bytes(), &row) != nil || row.Kind != RowKind {
			continue
		}
		rows = append(rows, row)
	}
	return rows, sc.Err()
}

// Check fails, naming each, every required ID that has no passed row or has any
// row that is not passed: skipped is not passed, and a scenario that never ran
// has no row.
func Check(rows []Row, required []string) error {
	results := map[string][]Result{}
	for _, r := range rows {
		results[r.ID] = append(results[r.ID], r.Result)
	}
	var errs []error
	for _, id := range required {
		got := results[id]
		switch {
		case len(got) == 0:
			errs = append(errs, fmt.Errorf("%s: %s: no row; the scenario did not run", id, RequiredNotRun))
		case slices.ContainsFunc(got, func(r Result) bool { return r != Passed }):
			errs = append(errs, fmt.Errorf("%s: %s: rows %v; only passed counts, skipped is not passed", id, RequiredNotRun, got))
		}
	}
	return errors.Join(errs...)
}
