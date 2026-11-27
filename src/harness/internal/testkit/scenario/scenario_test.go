package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// recorder is a testing.TB that records failures and skips instead of ending the
// real test, so a test can check what Run does on a path that fails or skips its
// caller. Cleanups and TempDir are the real test's.
type recorder struct {
	testing.TB
	mu       sync.Mutex
	failures []string
	skips    []string
	logs     []string
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
}

func (r *recorder) Error(args ...any) { r.Errorf("%s", fmt.Sprint(args...)) }

func (r *recorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	runtime.Goexit()
}

func (r *recorder) Fatal(args ...any) { r.Fatalf("%s", fmt.Sprint(args...)) }

func (r *recorder) FailNow() {
	if !r.Failed() {
		r.Errorf("FailNow")
	}
	runtime.Goexit()
}

func (r *recorder) Skipf(format string, args ...any) {
	r.mu.Lock()
	r.skips = append(r.skips, fmt.Sprintf(format, args...))
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recorder) SkipNow() { r.Skipf("SkipNow") }

func (r *recorder) Failed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.failures) > 0
}

func (r *recorder) Skipped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.skips) > 0
}

func (r *recorder) Logf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}

// run calls fn on its own goroutine, so a Fatalf or Skipf ends fn alone.
func (r *recorder) run(fn func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
}

// report points DSH_SCENARIO_REPORT at a fresh file and clears the other inputs.
func report(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "report.jsonl")
	t.Setenv(ReportEnv, p)
	t.Setenv(RequireEnv, "")
	t.Setenv(BinEnv, "")
	return p
}

func rows(t *testing.T, p string) []Row {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := ReadReport(f)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func one(t *testing.T, p string) Row {
	t.Helper()
	got := rows(t, p)
	if len(got) != 1 {
		t.Fatalf("report rows = %+v, want one", got)
	}
	return got[0]
}

// TC-17a: a required scenario whose DSH_BIN is unset fails, it does not skip.
//
//dsh:test DH-T-UTIL-19
//dsh:test DH-T-TOOLCHAIN-6
func TestRequiredScenarioWithMissingInputFails(t *testing.T) {
	p := report(t)
	t.Setenv(RequireEnv, "DH-E-OTHER, DH-E-X-1")
	r := &recorder{TB: t}
	ran := false
	r.run(func() { Run(r, "DH-E-X-1", Needs{Bin: true}, func(*S) { ran = true }) })
	if ran {
		t.Error("the body ran without its input")
	}
	if len(r.skips) != 0 || len(r.failures) != 1 || !strings.Contains(r.failures[0], "required scenario skipped") || !strings.Contains(r.failures[0], BinEnv) {
		t.Fatalf("failures %q, skips %q; want one failure naming required scenario skipped and %s", r.failures, r.skips, BinEnv)
	}
	if row := one(t, p); row.ID != "DH-E-X-1" || row.Result != Failed || !strings.Contains(row.Reason, BinEnv) {
		t.Errorf("row = %+v, want failed naming %s", row, BinEnv)
	}
}

//dsh:test DH-T-UTIL-19
func TestUnrequiredScenarioWithMissingInputSkipsWithReason(t *testing.T) {
	p := report(t)
	r := &recorder{TB: t}
	r.run(func() {
		Run(r, "DH-E-X-2", Needs{Env: []string{"DSH_SCENARIO_TEST_UNSET_VAR"}, Programs: []string{"dsh-no-such-program-on-path"}, OS: []string{"plan9"}}, func(*S) {
			t.Error("the body ran without its input")
		})
	})
	if len(r.failures) != 0 || len(r.skips) != 1 {
		t.Fatalf("failures %q, skips %q; want one skip", r.failures, r.skips)
	}
	row := one(t, p)
	for _, want := range []string{"DSH_SCENARIO_TEST_UNSET_VAR is not set", "dsh-no-such-program-on-path is not on PATH", "plan9"} {
		if !strings.Contains(row.Reason, want) {
			t.Errorf("reason %q lacks %q", row.Reason, want)
		}
	}
	if row.Result != Skipped {
		t.Errorf("result = %s, want skipped", row.Result)
	}
}

func TestPassedScenarioGetsAHomeWorkspaceAndBuildOfItsOwn(t *testing.T) {
	p := report(t)
	bin := filepath.Join(t.TempDir(), "dsh")
	if err := os.WriteFile(bin, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv(BinEnv, bin)
	t.Setenv(RequireEnv, "DH-E-X-3")
	var homes []string
	for range 2 {
		Run(t, "DH-E-X-3", Needs{Bin: true}, func(s *S) {
			if s.Bin != bin || s.Home == "" || s.Workspace == "" || s.Context().Err() != nil {
				t.Errorf("scenario = %+v", s)
			}
			prog := s.Program([]string{"DSH_HOME=elsewhere"}, "version")
			if prog.Argv[0] != bin || prog.Dir != s.Workspace || prog.Env[len(prog.Env)-1] != "DSH_HOME="+s.Home {
				t.Errorf("program = %+v", prog)
			}
			homes = append(homes, s.Home)
		})
	}
	if homes[0] == homes[1] {
		t.Errorf("two scenarios shared the home %s", homes[0])
	}
	got := rows(t, p)
	if len(got) != 2 {
		t.Fatalf("rows = %+v", got)
	}
	// sha256("binary")
	const sum = "9a3a45d01531a20e89ac6ae10b0b0beb0492acd7216a368aa062d1a5fecaf9cd"
	for _, row := range got {
		if row.Result != Passed || row.Build != sum || row.OS != runtime.GOOS {
			t.Errorf("row = %+v, want passed with build %s", row, sum)
		}
	}
	if err := Check(got, []string{"DH-E-X-3"}); err != nil {
		t.Error(err)
	}
}

func TestBinThatIsNotAFileFails(t *testing.T) {
	p := report(t)
	t.Setenv(BinEnv, t.TempDir())
	r := &recorder{TB: t}
	r.run(func() { Run(r, "DH-E-X-4", Needs{Bin: true}, func(*S) { t.Error("the body ran") }) })
	if len(r.failures) != 1 || one(t, p).Result != Failed {
		t.Errorf("failures %q, want one", r.failures)
	}
	r = &recorder{TB: t}
	r.run(func() { Bin(r) })
	if len(r.failures) != 1 || !strings.Contains(r.failures[0], "not a regular file") {
		t.Errorf("Bin failures %q, want not a regular file", r.failures)
	}
}

func TestBodyOutcomesAreRecorded(t *testing.T) {
	cases := []struct {
		name     string
		required bool
		body     func(*S)
		want     Result
		failures int
	}{
		{"failed", false, func(s *S) { s.Errorf("broken") }, Failed, 1},
		{"fatal", false, func(s *S) { s.Fatalf("broken") }, Failed, 1},
		{"skipped", false, func(s *S) { s.SkipNow() }, Skipped, 0},
		{"required skipped", true, func(s *S) { s.SkipNow() }, Failed, 1},
		{"panicked", false, func(*S) { panic("boom") }, Failed, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := report(t)
			if c.required {
				t.Setenv(RequireEnv, "DH-E-X-5")
			}
			r := &recorder{TB: t}
			r.run(func() { Run(r, "DH-E-X-5", Needs{}, c.body) })
			if row := one(t, p); row.Result != c.want {
				t.Errorf("row = %+v, want %s", row, c.want)
			}
			if len(r.failures) != c.failures {
				t.Errorf("failures %q, want %d", r.failures, c.failures)
			}
		})
	}
}

func TestBodyPastItsDeadlineFails(t *testing.T) {
	p := report(t)
	r := &recorder{TB: t}
	r.run(func() {
		Run(r, "DH-E-X-6", Needs{Deadline: time.Millisecond}, func(s *S) {
			<-s.Context().Done()
		})
	})
	row := one(t, p)
	if row.Result != Failed || !strings.Contains(row.Reason, "deadline") || len(r.failures) != 1 {
		t.Errorf("row = %+v, failures %q; want failed at its deadline", row, r.failures)
	}
}

// A body that ignores its context is not waited for past the deadline and the
// grace that follows it: the scenario fails and Run returns.
func TestBodyThatIgnoresItsDeadlineIsAbandoned(t *testing.T) {
	p := report(t)
	old := abandonAfter
	abandonAfter = 10 * time.Millisecond
	t.Cleanup(func() { abandonAfter = old })
	stuck := make(chan struct{})
	defer close(stuck)
	r := &recorder{TB: t}
	after := false
	r.run(func() {
		Run(r, "DH-E-X-10", Needs{Deadline: time.Millisecond}, func(*S) { <-stuck })
		after = true
	})
	row := one(t, p)
	if row.Result != Failed || !strings.Contains(row.Reason, "did not return") || after || len(r.failures) != 1 {
		t.Errorf("row = %+v, failures %q, returned to the caller %v; want failed, abandoned, caller ended", row, r.failures, after)
	}
}

func TestReportDefaultsToATempFile(t *testing.T) {
	t.Setenv(ReportEnv, "")
	t.Setenv(RequireEnv, "")
	r := &recorder{TB: t}
	r.run(func() { Run(r, "DH-E-X-7", Needs{}, func(*S) {}) })
	if len(r.failures) != 0 || len(r.logs) != 1 || !strings.Contains(r.logs[0], "scenario-report.jsonl") {
		t.Errorf("failures %q, logs %q", r.failures, r.logs)
	}
}

func TestUnwritableReportFails(t *testing.T) {
	t.Setenv(ReportEnv, t.TempDir())
	t.Setenv(RequireEnv, "")
	r := &recorder{TB: t}
	r.run(func() { Run(r, "DH-E-X-8", Needs{}, func(*S) {}) })
	if len(r.failures) != 1 || !strings.Contains(r.failures[0], "writing the report") {
		t.Errorf("failures %q, want the report write failure", r.failures)
	}
}

//dsh:test DH-T-UTIL-19
//dsh:test DH-T-TOOLCHAIN-6
func TestCheckFailsARequiredScenarioWithoutAPassedRow(t *testing.T) {
	report := strings.Join([]string{
		`{"kind":"scenario","id":"A","result":"passed"}`,
		`{"kind":"scenario","id":"B","result":"skipped"}`,
		`{"kind":"scenario","id":"C","result":"passed"}`,
		`{"kind":"scenario","id":"C","result":"failed"}`,
		`{"id":"D","result":"passed"}`,
		`not json`,
	}, "\n")
	got, err := ReadReport(strings.NewReader(report))
	if err != nil || len(got) != 4 {
		t.Fatalf("rows %+v, %v; want the four scenario rows", got, err)
	}
	if err := Check(got, []string{"A"}); err != nil {
		t.Errorf("A passed: %v", err)
	}
	err = Check(got, []string{"A", "B", "C", "D"})
	if err == nil {
		t.Fatal("Check passed B, C and D")
	}
	for _, want := range []string{"B: " + RequiredNotRun, "C: " + RequiredNotRun, "D: " + RequiredNotRun + ": no row"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%v lacks %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "A:") {
		t.Errorf("%v names A", err)
	}
}

func TestNeedFailsOnAMissingInput(t *testing.T) {
	t.Setenv("DSH_SCENARIO_TEST_NEED", " value ")
	if got := Need(t, "DSH_SCENARIO_TEST_NEED", "a test input"); got != "value" {
		t.Errorf("Need = %q", got)
	}
	t.Setenv("DSH_SCENARIO_TEST_NEED", "")
	r := &recorder{TB: t}
	r.run(func() { Need(r, "DSH_SCENARIO_TEST_NEED", "a test input") })
	if len(r.failures) != 1 || len(r.skips) != 0 || !strings.Contains(r.failures[0], "DSH_SCENARIO_TEST_NEED is not set") {
		t.Errorf("failures %q, skips %q", r.failures, r.skips)
	}
}

func TestSplitRequired(t *testing.T) {
	got := SplitRequired(" A,B\tC\n, ,D ")
	if strings.Join(got, "|") != "A|B|C|D" {
		t.Errorf("SplitRequired = %q", got)
	}
}

func TestProgramWithoutBinFails(t *testing.T) {
	r := &recorder{TB: t}
	r.run(func() { (&S{TB: r, ID: "DH-E-X-9"}).Program(nil) })
	if len(r.failures) != 1 || !strings.Contains(r.failures[0], "Needs.Bin") {
		t.Errorf("failures %q", r.failures)
	}
}

// A test that reaches the helper through Bin, Need or Start, not Run, writes
// exactly one row when it ends, under the ID of its //dsh:scenario directive.
func TestTrackedTestWritesOneRow(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "dsh")
	if err := os.WriteFile(bin, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	old := ids
	ids = map[string]string{"TestTrackedTestWritesOneRow": "DH-E-T-1"}
	t.Cleanup(func() { ids = old })
	dir := t.TempDir()
	cases := []struct {
		name     string
		required bool
		fn       func(r *recorder)
		want     Result
		reason   string
		failures int
	}{
		{"missing input", false, func(r *recorder) {
			Bin(r)
			Need(r, "DSH_SCENARIO_TEST_UNSET_VAR", "an input")
			r.Errorf("unreachable")
		}, Failed, "DSH_SCENARIO_TEST_UNSET_VAR is not set", 1},
		{"passed", false, func(r *recorder) { Bin(r); Bin(r) }, Passed, "", 0},
		{"failed", false, func(r *recorder) { Bin(r); r.Errorf("broken") }, Failed, "the test failed", 1},
		{"skipped", false, func(r *recorder) { Bin(r); r.SkipNow() }, Skipped, "skipped", 0},
		{"required skipped", true, func(r *recorder) { Bin(r); r.SkipNow() }, Failed, "skipped", 1},
	}
	for i, c := range cases {
		p := filepath.Join(dir, fmt.Sprintf("report-%d.jsonl", i))
		var r *recorder
		t.Run(c.name, func(t *testing.T) {
			t.Setenv(ReportEnv, p)
			t.Setenv(BinEnv, bin)
			t.Setenv(RequireEnv, "")
			if c.required {
				t.Setenv(RequireEnv, "DH-E-T-1")
			}
			r = &recorder{TB: t}
			r.run(func() { c.fn(r) })
		})
		row := one(t, p)
		if row.ID != "DH-E-T-1" || row.Result != c.want || !strings.Contains(row.Reason, c.reason) {
			t.Errorf("%s: row = %+v, want %s with %q", c.name, row, c.want, c.reason)
		}
		if row.Build == "" {
			t.Errorf("%s: row %+v has no build", c.name, row)
		}
		if len(r.failures) != c.failures {
			t.Errorf("%s: failures %q, want %d", c.name, r.failures, c.failures)
		}
	}
}

// A Run inside a tracked test writes the rows; the test adds none.
func TestRunOwnsTheRowsOfItsTest(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.jsonl")
	t.Run("run", func(t *testing.T) {
		t.Setenv(ReportEnv, p)
		t.Setenv(RequireEnv, "")
		t.Setenv(BinEnv, "")
		Need(t, ReportEnv, "the report")
		Run(t, "DH-E-T-2", Needs{}, func(*S) {})
	})
	if row := one(t, p); row.ID != "DH-E-T-2" || row.Result != Passed {
		t.Errorf("row = %+v", row)
	}
}

// The row is written after the scenario's cleanups: a body that passed followed
// by a cleanup that fails, or that ends its goroutine, gives one failed row, and
// the cleanups after it, the home and the workspace still go.
func TestCleanupFailureFailsThePassedRow(t *testing.T) {
	cases := map[string]func(s *S){
		"error": func(s *S) { s.Errorf("cleanup failed") },
		"fatal": func(s *S) { s.Fatalf("cleanup failed") },
	}
	for name, fail := range cases {
		t.Run(name, func(t *testing.T) {
			p := report(t)
			r := &recorder{TB: t}
			var home, workspace string
			ran := false
			r.run(func() {
				Run(r, "DH-E-X-11", Needs{}, func(s *S) {
					home, workspace = s.Home, s.Workspace
					s.Cleanup(func() { ran = true })
					s.Cleanup(func() { fail(s) })
				})
			})
			row := one(t, p)
			if row.Result != Failed || !strings.Contains(row.Reason, "cleanup") || len(r.failures) != 1 {
				t.Errorf("row = %+v, failures %q; want one failed row naming the cleanup", row, r.failures)
			}
			if !ran {
				t.Error("a cleanup registered before the failing one did not run")
			}
			for _, d := range []string{home, workspace} {
				if _, err := os.Stat(d); !os.IsNotExist(err) {
					t.Errorf("%s survives the scenario (%v)", d, err)
				}
			}
		})
	}
}

// A test with a //dsh:scenario directive is tracked from its first line (Main
// wraps it through enter), so one that never calls Bin, Need or Start, or fails
// before it would, still writes exactly one row; an undeclared test writes none.
func TestDeclaredTestIsTrackedFromEntry(t *testing.T) {
	if err := enter(&testing.M{}, nil); err != nil {
		t.Fatalf("enter: %v", err)
	}
	old := ids
	ids = map[string]string{"TestDeclaredTestIsTrackedFromEntry": "DH-E-T-3"}
	t.Cleanup(func() { ids = old })
	dir := t.TempDir()
	ran := 0
	tests := []testing.InternalTest{
		{Name: "TestDeclaredTestIsTrackedFromEntry", F: func(*testing.T) { ran++ }},
		{Name: "TestUndeclared", F: func(*testing.T) { ran++ }},
	}
	wrapTests(tests, ids)
	for i, tc := range tests {
		p := filepath.Join(dir, fmt.Sprintf("report-%d.jsonl", i))
		t.Run("no helper", func(t *testing.T) {
			t.Setenv(ReportEnv, p)
			t.Setenv(RequireEnv, "")
			tc.F(t)
		})
		_, err := os.Stat(p)
		if declared := i == 0; declared {
			if row := one(t, p); row.ID != "DH-E-T-3" || row.Result != Passed {
				t.Errorf("row = %+v, want DH-E-T-3 passed", row)
			}
		} else if !os.IsNotExist(err) {
			t.Errorf("%s wrote a row (%v)", tc.Name, err)
		}
	}
	if ran != 2 {
		t.Errorf("the bodies ran %d times, want 2", ran)
	}
	p := filepath.Join(dir, "report-early.jsonl")
	t.Run("fails first", func(t *testing.T) {
		t.Setenv(ReportEnv, p)
		t.Setenv(RequireEnv, "")
		r := &recorder{TB: t}
		r.run(func() {
			track(r)
			r.Fatalf("broken before any helper")
		})
	})
	if row := one(t, p); row.ID != "DH-E-T-3" || row.Result != Failed {
		t.Errorf("row = %+v, want DH-E-T-3 failed", row)
	}
}

func TestDirectives(t *testing.T) {
	dir := t.TempDir()
	src := strings.Join([]string{
		"package x",
		"// TestA does a.",
		"//",
		"//dsh:scenario DH-E-A-1",
		"func TestA(t *testing.T) {}",
		"//dsh:scenario DH-E-B-1",
		"var x = 1",
		"func TestB(t *testing.T) {}",
		"func TestC(t *testing.T) {}",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, "a_test.go"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Directives(dir)
	if err != nil || len(got) != 1 || got["TestA"] != "DH-E-A-1" {
		t.Errorf("Directives = %v, %v; want TestA only", got, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "d_test.go"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Directives(dir); err == nil {
		t.Error("Directives read a directory as a file")
	}
}

// Main's check: a required ID this package declares fails the test binary unless
// the report has its passed row; IDs of other packages are not this binary's.
func TestVerify(t *testing.T) {
	p := filepath.Join(t.TempDir(), "report.jsonl")
	if err := os.WriteFile(p, []byte(`{"kind":"scenario","id":"DH-E-A-1","result":"passed"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{"TestA": "DH-E-A-1", "TestB": "DH-E-B-1"}
	cases := []struct {
		name          string
		code          int
		report, needs string
		want          int
	}{
		{"tests failed", 2, p, "DH-E-B-1", 2},
		{"no report", 0, "", "DH-E-B-1", 0},
		{"another package's ID", 0, p, "DH-E-OTHER-1", 0},
		{"passed", 0, p, "DH-E-A-1", 0},
		{"did not run", 0, p, "DH-E-A-1,DH-E-B-1", 1},
		{"unreadable report", 0, filepath.Join(t.TempDir(), "missing"), "DH-E-A-1", 1},
	}
	for _, c := range cases {
		if got := verify(c.code, c.report, c.needs, declared); got != c.want {
			t.Errorf("%s: verify = %d, want %d", c.name, got, c.want)
		}
	}
}
