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
)

// tracker is the evidence row of one top-level test that reaches the helper
// through Bin, Need or Start instead of Run (the scenarios that predate Run,
// architecture.md I-2): the test's outcome becomes its row when it ends.
type tracker struct {
	id    string
	start time.Time

	mu     sync.Mutex
	reason string
	build  string
	byRun  bool // Run writes the rows of this test
}

var (
	trackMu sync.Mutex
	tracked = map[string]*tracker{} // by top-level test name
	// ids maps a test name to the DH-E ID of its //dsh:scenario directive,
	// read by Main from the package's files.
	ids map[string]string
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
// later, so it sees their failures too.
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
	tr := &tracker{id: id, start: time.Now()}
	tracked[name] = tr
	t.Cleanup(func() { tr.finish(t, name) })
	return tr
}

// own marks t's test as one whose rows Run writes.
func own(t testing.TB) {
	tr := track(t)
	tr.mu.Lock()
	tr.byRun = true
	tr.mu.Unlock()
}

// finish writes the row of a tracked test: failed with the first recorded
// reason, skipped (failed when required), failed past DefaultDeadline, or passed.
func (tr *tracker) finish(t testing.TB, name string) {
	trackMu.Lock()
	delete(tracked, name)
	trackMu.Unlock()
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.byRun {
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
	case took > DefaultDeadline:
		row.Result, row.Reason = Failed, fmt.Sprintf("deadline %s reached", DefaultDeadline)
		t.Errorf("%s: the test ran past the scenario deadline of %s", tr.id, DefaultDeadline)
	default:
		row.Result = Passed
	}
	write(t, row)
}

// Directives maps each test of the *_test.go files in dir that carries a
// //dsh:scenario directive in its doc comment to the directive's ID.
func Directives(dir string) (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*_test.go"))
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, p := range files {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		pending := ""
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			switch {
			case strings.HasPrefix(line, "//dsh:scenario "):
				if fields := strings.Fields(line); len(fields) > 1 {
					pending = fields[1]
				}
			case strings.HasPrefix(line, "//"):
			case strings.HasPrefix(line, "func Test"):
				if name, _, ok := strings.Cut(strings.TrimPrefix(line, "func "), "("); ok && pending != "" {
					out[name] = pending
				}
				pending = ""
			default:
				pending = ""
			}
		}
		err = sc.Err()
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p, err)
		}
	}
	return out, nil
}

// Main runs a scenario package's tests: its TestMain calls it. It reads the
// package's //dsh:scenario directives (the IDs of the rows its tests write), runs
// the tests through dshbin.Run, then, when DSH_SCENARIO_REPORT and
// DSH_REQUIRE_SCENARIOS are set, checks the report for the required IDs this
// package declares (Check), so a required scenario that did not pass, or did not
// run, fails the test binary.
func Main(m *testing.M) {
	found, err := Directives(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
		os.Exit(1)
	}
	ids = found
	if err := enter(m, found); err != nil {
		fmt.Fprintf(os.Stderr, "scenario: %v\n", err)
		os.Exit(1)
	}
	os.Exit(verify(dshbin.Run(m), os.Getenv(ReportEnv), os.Getenv(RequireEnv), found))
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
