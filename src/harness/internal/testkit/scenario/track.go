package scenario

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	//lint:ignore typeassert testing.M has no per-test hook; enter reads its unexported test list to track each scenario from entry (C55 one row per scenario)
	"unsafe"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/dshbin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// tracker is the evidence row of one top-level test that reaches the helper
// through Bin, Need or Start instead of Run (the scenarios that predate Run,
// architecture.md I-2): the test's outcome becomes its row when it ends.
type tracker struct {
	id    string
	start time.Time

	mu      sync.Mutex
	reason  string
	build   string
	byRun   bool // Run writes the rows of this test
	done    bool // finish has run
	written bool // expire wrote the row
	timer   *time.Timer

	// deadline is the test's declared deadline (//dsh:deadline), else
	// trackDeadline, when the test was first tracked; zero is none. after and
	// abandon are abandonAfter and abandon then.
	deadline, after time.Duration
	abandon         func(string)
}

var (
	// trackDeadline is the deadline of a tracked test that declares none: zero
	// (none, the test is bounded by go test -timeout alone) outside this
	// package's own tests. Long scenarios that predate Run must not be failed
	// and their binary ended at a fixed bound they never declared.
	trackDeadline time.Duration
	// abandon ends the test binary when a tracked test is still running
	// abandonAfter past its deadline and the stop of its trees.
	abandon = func(msg string) {
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
)

var (
	trackMu sync.Mutex
	tracked = map[string]*tracker{} // by top-level test name
	// ids maps a test name to the DH-E ID of its //dsh:scenario directive,
	// read by Main from the package's files.
	ids map[string]string
	// deadlines maps a test name to the deadline its //dsh:deadline line
	// declares, read by Main with ids.
	deadlines map[string]time.Duration
)

func (tr *tracker) fail(reason string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.reason == "" {
		tr.reason = reason
	}
}

func (tr *tracker) built(sum string) {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.build = sum
}

// topName is the name of the top-level test of the test named name.
func topName(name string) string {
	top, _, _ := strings.Cut(name, "/")
	return top
}

// track returns the tracker of t's top-level test. The first call registers, on
// t, the cleanup that writes the row; it runs after every cleanup registered
// later, so it sees their failures too. It also starts the test's deadline
// (expire), when it has one.
func track(t testing.TB) *tracker {
	name := topName(t.Name())
	trackMu.Lock()
	defer trackMu.Unlock()
	if tr := tracked[name]; tr != nil {
		return tr
	}
	id := ids[name]
	if id == "" {
		id = name
	}
	deadline := deadlines[name]
	if deadline <= 0 {
		deadline = trackDeadline
	}
	tr := &tracker{id: id, start: time.Now(), deadline: deadline, after: abandonAfter, abandon: abandon}
	tracked[name] = tr
	if tr.deadline > 0 {
		tr.mu.Lock()
		tr.timer = time.AfterFunc(tr.deadline, func() { tr.expire(t, name) })
		tr.mu.Unlock()
	}
	t.Cleanup(func() { tr.finish(t, name) })
	return tr
}

// expire ends a tracked test that is still running at its declared deadline: it
// writes
// the test's failed row at once, fails the test and force-stops every process
// tree the test started through the launcher, so a test blocked on a child is
// released. A test still running abandonAfter later ends the test binary
// (abandon), its row already written. Run's tests keep Run's own deadline.
func (tr *tracker) expire(t testing.TB, name string) {
	tr.mu.Lock()
	if tr.done || tr.byRun {
		tr.mu.Unlock()
		return
	}
	if tr.reason == "" {
		tr.reason = fmt.Sprintf("deadline %s reached", tr.deadline)
	}
	tr.written = true
	write(t, Row{Kind: RowKind, ID: tr.id, OS: runtime.GOOS, Build: tr.build, Result: Failed, Reason: tr.reason, DurationMS: time.Since(tr.start).Milliseconds()})
	t.Errorf("%s: the test ran past the scenario deadline of %s; stopping its process trees", tr.id, tr.deadline)
	if err := launcher.StopTrees(name); err != nil {
		t.Errorf("%s: stopping its process trees: %v", tr.id, err)
	}
	tr.mu.Unlock()
	end, after := tr.abandon, tr.after
	msg := fmt.Sprintf("scenario: %s (%s) still runs %s after its deadline of %s and the stop of its trees", tr.id, name, after, tr.deadline)
	time.AfterFunc(after, func() {
		tr.mu.Lock()
		done := tr.done
		tr.mu.Unlock()
		if !done {
			end(msg)
		}
	})
}

// own marks t's test as one whose rows Run writes.
func own(t testing.TB) {
	tr := track(t)
	tr.mu.Lock()
	tr.byRun = true
	tr.mu.Unlock()
}

// finish writes the row of a tracked test: failed with the first recorded
// reason, skipped (failed when required), failed past its deadline, or passed.
func (tr *tracker) finish(t testing.TB, name string) {
	trackMu.Lock()
	delete(tracked, name)
	trackMu.Unlock()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	tr.done = true
	if tr.timer != nil {
		tr.timer.Stop()
	}
	if tr.byRun || tr.written {
		return
	}
	took := time.Since(tr.start)
	row := Row{Kind: RowKind, ID: tr.id, OS: runtime.GOOS, Build: tr.build, DurationMS: took.Milliseconds()}
	switch {
	case t.Failed():
		row.Result, row.Reason = Failed, tr.reason
		if row.Reason == "" {
			row.Reason = "the test failed"
		}
	case t.Skipped():
		row.Result, row.Reason = Skipped, "the test skipped"
		if Required(tr.id) {
			row.Result = Failed
			t.Errorf("%s: required scenario skipped (listed in %s)", tr.id, RequireEnv)
		}
	case tr.deadline > 0 && took > tr.deadline:
		row.Result, row.Reason = Failed, fmt.Sprintf("deadline %s reached", tr.deadline)
		t.Errorf("%s: the test ran past the scenario deadline of %s", tr.id, tr.deadline)
	default:
		row.Result = Passed
	}
	write(t, row)
}

// Directives maps each test of the *_test.go files in dir that carries a
// //dsh:scenario directive in its doc comment to the directive's ID.
func Directives(dir string) (map[string]string, error) {
	out, _, err := directives(dir)
	return out, err
}

// DeadlineDirective, followed by a Go duration (//dsh:deadline 4h), in the doc
// comment of a test with a //dsh:scenario directive declares the deadline Main
// tracks that test against. A test that declares none has no tracked deadline:
// go test -timeout bounds it. It is a line of its own, so the parsers that read
// //dsh:scenario <ID> are unaffected.
const DeadlineDirective = "//dsh:deadline"

// directives is Directives with the declared deadlines (DeadlineDirective) of
// the same tests.
func directives(dir string) (map[string]string, map[string]time.Duration, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, nil, err
	}
	out := map[string]string{}
	limits := map[string]time.Duration{}
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			return nil, nil, err
		}
		pending := ""
		var limit time.Duration
		sc := bufio.NewScanner(f)
		line := 0
		for sc.Scan() {
			line++
			text := strings.TrimSpace(sc.Text())
			switch {
			case strings.HasPrefix(text, "//dsh:scenario "):
				if fields := strings.Fields(text); len(fields) > 1 {
					pending = fields[1]
				}
			case strings.HasPrefix(text, DeadlineDirective+" "):
				fields := strings.Fields(text)
				d, perr := time.ParseDuration(fields[len(fields)-1])
				if len(fields) != 2 || perr != nil || d <= 0 {
					f.Close()
					return nil, nil, fmt.Errorf("%s:%d: %s wants one positive duration, got %q", p, line, DeadlineDirective, text)
				}
				limit = d
			case strings.HasPrefix(text, "//"):
			case strings.HasPrefix(text, "func Test"):
				if name, _, ok := strings.Cut(strings.TrimPrefix(text, "func "), "("); ok && pending != "" {
					out[name] = pending
					if limit > 0 {
						limits[name] = limit
					}
				}
				pending, limit = "", 0
			default:
				pending, limit = "", 0
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	return out, limits, nil
}

// Main runs a scenario package's tests: its TestMain calls it. It reads the
// package's //dsh:scenario directives (the IDs of the rows its tests write) and
// their //dsh:deadline lines (DeadlineDirective), runs
// the tests through dshbin.Run, then, when DSH_SCENARIO_REPORT and
// DSH_REQUIRE_SCENARIOS are set, checks the report for the required IDs this
// package declares (Check), so a required scenario that did not pass, or did not
// run, fails the test binary. While the tests run, DSH_SCENARIO_REPORT names the
// detail file beside the report (DetailSuffix), so a scenario's own detail output
// never truncates the rows.
func Main(m *testing.M) {
	found, limits, err := directives(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
		os.Exit(1)
	}
	ids, deadlines = found, limits
	if err := enter(m, found); err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
		os.Exit(1)
	}
	report, err := separate()
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
		os.Exit(1)
	}
	os.Exit(verify(dshbin.Run(m), report, os.Getenv(RequireEnv), found))
}

// enter makes each test of m that carries a //dsh:scenario directive tracked
// from its first line (track), so the test writes exactly one row whether or not
// it reaches Bin, Need or Start, and a failure before any of them is recorded
// too; the test bodies are unchanged (architecture.md I-2). The testing package
// offers no per-test hook, so enter rewrites the test list testing.M holds; a Go
// release that changes that field fails Main here rather than dropping rows.
func enter(m *testing.M, declared map[string]string) error {
	f := reflect.ValueOf(m).Elem().FieldByName("tests")
	if !f.IsValid() || f.Type() != reflect.TypeFor[[]testing.InternalTest]() {
		return errors.New("testing.M has no tests field of []testing.InternalTest: Main cannot track the scenarios from their entry")
	}
	tests := *(*[]testing.InternalTest)(unsafe.Pointer(f.UnsafeAddr()))
	wrapTests(tests, declared)
	return nil
}

// wrapTests replaces, in place, the function of each test in declared by one that
// tracks the test before it runs.
func wrapTests(tests []testing.InternalTest, declared map[string]string) {
	for i := range tests {
		if _, ok := declared[tests[i].Name]; !ok {
			continue
		}
		body := tests[i].F
		tests[i].F = func(t *testing.T) {
			track(t)
			body(t)
		}
	}
}

// verify is Main's check after the tests ended with code.
func verify(code int, report, require string, declared map[string]string) int {
	if code != 0 || report == "" {
		return code
	}
	mine := map[string]bool{}
	for _, id := range declared {
		mine[id] = true
	}
	var required []string
	for _, id := range SplitRequired(require) {
		if mine[id] {
			required = append(required, id)
		}
	}
	if len(required) == 0 {
		return code
	}
	f, err := os.Open(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
		return 1
	}
	defer f.Close()
	rows, err := ReadReport(f)
	if err == nil {
		err = Check(rows, required)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %s: %v\n", report, err)
		return 1
	}
	return code
}
