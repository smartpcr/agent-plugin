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

	ctx context.Context
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
	return launcher.Spawn(s, s.Program(env, args...))
}

// Run declares and runs scenario id. It returns once the body has returned, or
// after the row of a skip or a failure is written.
func Run(t testing.TB, id string, needs Needs, body func(s *S)) {
	t.Helper()
	start := time.Now()
	row := Row{Kind: RowKind, ID: id, OS: runtime.GOOS}
	required := Required(id)
	if reason := Unmet(needs); reason != "" {
		row.Reason = reason
		if required {
			row.Result = Failed
			write(t, row)
			t.Fatalf("%s: required scenario skipped: %s (listed in %s)", id, reason, RequireEnv)
		}
		row.Result = Skipped
		write(t, row)
		t.Skipf("%s: %s", id, reason)
	}
	s := &S{TB: t, ID: id}
	if needs.Bin {
		s.Bin = os.Getenv(BinEnv)
		sum, err := digest(s.Bin)
		if err != nil {
			row.Result, row.Reason = Failed, fmt.Sprintf("%s=%s: %v", BinEnv, s.Bin, err)
			write(t, row)
			t.Fatalf("%s: %s", id, row.Reason)
		}
		row.Build = sum
	}
	s.Home = temphome.New(t).Root()
	s.Workspace = t.TempDir()
	deadline := needs.Deadline
	if deadline <= 0 {
		deadline = DefaultDeadline
	}
	ctx, cancel := context.WithTimeoutCause(context.Background(), deadline, ErrDeadline)
	s.ctx = ctx
	returned := false
	// Deferred, so a body ended by FailNow or SkipNow (runtime.Goexit) or by a
	// panic still writes its row.
	defer func() {
		late := errors.Is(context.Cause(ctx), ErrDeadline)
		cancel()
		row.DurationMS = time.Since(start).Milliseconds()
		switch {
		case late:
			row.Result, row.Reason = Failed, fmt.Sprintf("deadline %s reached", deadline)
			t.Errorf("%s: the body ran past its deadline of %s", id, deadline)
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
			row.Result, row.Reason = Failed, "the body panicked"
		default:
			row.Result = Passed
		}
		write(t, row)
	}()
	body(s)
	returned = true
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
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		t.Fatalf("%s is not set: %s", name, what)
	}
	return v
}

// Bin is the built dsh a scenario starts: the file DSH_BIN names when it is set
// (a name that is not a file fails t), else this checkout's ./cmd/dsh built once
// per test binary (dshbin; the package's TestMain calls Main).
func Bin(t testing.TB) string {
	t.Helper()
	if p := strings.TrimSpace(os.Getenv(BinEnv)); p != "" {
		if _, err := digest(p); err != nil {
			t.Fatalf("%s=%s: %v", BinEnv, p, err)
		}
		return p
	}
	return dshbin.Path(t)
}

// Main runs a scenario package's tests (dshbin.Main): its TestMain calls it.
func Main(m *testing.M) { dshbin.Main(m) }

// Start launches p, the built binary in SDK host mode, against a scratch home of
// its own (launcher.Start: DSH_HOME is applied after p.Env and the home is removed
// at cleanup), so a host scenario gets the same isolation as a Run body.
func Start(t testing.TB, p launcher.Program) *launcher.Session {
	t.Helper()
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
