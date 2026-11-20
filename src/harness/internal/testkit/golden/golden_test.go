package golden

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// recorder is a testing.TB that records failures instead of failing the test, so a
// test can observe what Assert reports.
type recorder struct {
	testing.TB
	errors, fatals, logs []string
}

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}
func (r *recorder) Fatalf(format string, args ...any) {
	r.fatals = append(r.fatals, fmt.Sprintf(format, args...))
}
func (r *recorder) Logf(format string, args ...any) {
	r.logs = append(r.logs, fmt.Sprintf(format, args...))
}
func (r *recorder) failed() bool { return len(r.errors)+len(r.fatals) > 0 }

// inTempDir runs the test from a fresh t.TempDir(), so testdata/ is the test's own.
func inTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// setUpdate sets the -update switch for the rest of the test, as "go test -update"
// would, outside CI: CI is unset for the test so the switch is honoured on a CI
// runner too.
func setUpdate(t *testing.T, on bool) {
	t.Helper()
	old := flag.Lookup("update").Value.String()
	if err := flag.Set("update", fmt.Sprint(on)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { flag.Set("update", old) })
	t.Setenv("CI", "") // restores CI after the test
	os.Unsetenv("CI")
}

func writeGolden(t *testing.T, name, content string) string {
	t.Helper()
	file := Path(name)
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return file
}

func readFile(t *testing.T, file string) string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestAssertDetectsDriftAndUpdatesOnlyOnSwitch is DH-T-TOOLCHAIN-4 (with
// TestAssertRefusesUpdateUnderCI): a golden holding "a" compared with "b" fails with
// a diff and keeps "a"; only -update writes "b".
func TestAssertDetectsDriftAndUpdatesOnlyOnSwitch(t *testing.T) {
	inTempDir(t)
	file := writeGolden(t, "x", "a\n")

	setUpdate(t, false)
	r := &recorder{TB: t}
	Assert(r, "x", "b\n")
	if len(r.errors) != 1 || len(r.fatals) != 0 {
		t.Fatalf("without -update: errors %q fatals %q, want one diff error", r.errors, r.fatals)
	}
	if !strings.Contains(r.errors[0], "--- want\n+++ got\n-a\n+b\n") {
		t.Errorf("failure has no diff:\n%s", r.errors[0])
	}
	if got := readFile(t, file); got != "a\n" {
		t.Errorf("without -update the golden holds %q, want %q", got, "a\n")
	}

	setUpdate(t, true)
	r = &recorder{TB: t}
	Assert(r, "x", []byte("b\n"))
	if r.failed() {
		t.Fatalf("with -update: errors %q fatals %q, want none", r.errors, r.fatals)
	}
	if got := readFile(t, file); got != "b\n" {
		t.Errorf("with -update the golden holds %q, want %q", got, "b\n")
	}

	setUpdate(t, false)
	r = &recorder{TB: t}
	Assert(r, "x", "b\n")
	if r.failed() {
		t.Errorf("after the update: errors %q fatals %q, want a match", r.errors, r.fatals)
	}
}

func TestAssertRefusesUpdateUnderCI(t *testing.T) {
	inTempDir(t)
	file := writeGolden(t, "x", "a\n")
	for _, ci := range []string{"true", ""} {
		setUpdate(t, true)
		t.Setenv("CI", ci)
		r := &recorder{TB: t}
		Assert(r, "x", "b\n")
		if len(r.fatals) != 1 || !strings.Contains(r.fatals[0], "-update refused for testdata/x.golden: CI is set") {
			t.Errorf("CI=%q: fatals = %q, want the refusal", ci, r.fatals)
		}
		if got := readFile(t, file); got != "a\n" {
			t.Errorf("CI=%q: the golden holds %q, want it unchanged", ci, got)
		}
	}
}

func TestAssertMissingGoldenFailsWithoutCreatingIt(t *testing.T) {
	inTempDir(t)
	setUpdate(t, false)
	r := &recorder{TB: t}
	Assert(r, "missing", "x")
	if len(r.fatals) != 1 || !strings.Contains(r.fatals[0], "testdata/missing.golden does not exist (record it with -update)") {
		t.Errorf("fatals = %q", r.fatals)
	}
	if _, err := os.Stat(Path("missing")); !os.IsNotExist(err) {
		t.Errorf("golden created without -update: %v", err)
	}
}

func TestAssertUpdateCreatesNestedGoldenAndKeepsIdenticalFile(t *testing.T) {
	inTempDir(t)
	setUpdate(t, true)
	r := &recorder{TB: t}
	Assert(r, "sub/dir/y", "y\n")
	if r.failed() || len(r.logs) != 1 || r.logs[0] != "golden: updated testdata/sub/dir/y.golden" {
		t.Fatalf("errors %q fatals %q logs %q", r.errors, r.fatals, r.logs)
	}
	file := filepath.Join("testdata", "sub", "dir", "y.golden")
	if got := readFile(t, file); got != "y\n" {
		t.Fatalf("golden holds %q", got)
	}
	// An identical re-record leaves the file alone: a read-only file would fail a write.
	if err := os.Chmod(file, 0o444); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(file, 0o644) })
	r = &recorder{TB: t}
	Assert(r, "sub/dir/y", "y\n")
	if r.failed() {
		t.Errorf("identical update: errors %q fatals %q", r.errors, r.fatals)
	}
}

func TestAssertReportsUnwritableAndUnreadableGolden(t *testing.T) {
	inTempDir(t)
	// testdata is a file, so neither testdata/z.golden nor its directory can exist.
	if err := os.WriteFile("testdata", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	setUpdate(t, true)
	r := &recorder{TB: t}
	Assert(r, "z", "z")
	if len(r.fatals) != 1 || !strings.HasPrefix(r.fatals[0], "golden: update testdata/z.golden:") {
		t.Errorf("update fatals = %q", r.fatals)
	}

	inTempDir(t)
	if err := os.MkdirAll(Path("d"), 0o755); err != nil { // a directory where the golden should be
		t.Fatal(err)
	}
	setUpdate(t, false)
	r = &recorder{TB: t}
	Assert(r, "d", "d")
	if len(r.fatals) != 1 || strings.Contains(r.fatals[0], "does not exist") {
		t.Errorf("read fatals = %q", r.fatals)
	}
}

func TestAssertRejectsNamesOutsideTestdata(t *testing.T) {
	inTempDir(t)
	for _, name := range []string{"", ".", "..", "../x", "a/../../x", "/abs", `C:\x`, `a\b`, "a/", "a//b"} {
		setUpdate(t, true)
		r := &recorder{TB: t}
		Assert(r, name, "x")
		if len(r.fatals) != 1 || !strings.HasPrefix(r.fatals[0], "golden: ") {
			t.Errorf("name %q: fatals = %q, want one rejection", name, r.fatals)
		}
	}
	if entries, _ := os.ReadDir("."); len(entries) != 0 {
		t.Errorf("a rejected name wrote %v", entries)
	}
}

func TestPath(t *testing.T) {
	if got, want := Path("render/plain"), filepath.Join("testdata", "render", "plain.golden"); got != want {
		t.Errorf("Path = %q, want %q", got, want)
	}
}

func TestDiff(t *testing.T) {
	tests := []struct{ name, want, got, diff string }{
		{"equal", "a\nb\n", "a\nb\n", " a\n b\n"},
		{"changed line", "a\nb\nc\n", "a\nB\nc\n", " a\n-b\n+B\n c\n"},
		{"added", "a\n", "a\nb\n", " a\n+b\n"},
		{"removed", "a\nb\n", "b\n", "-a\n b\n"},
		{"empty want", "", "x\n", "+x\n"},
		{"missing newline", "a\n", "a", "-a\n+a\n\\ no newline at end of file\n"},
		{"carriage return", "a\n", "a\r\n", "-a\n+\"a\\r\"\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Diff(tt.want, tt.got); got != "--- want\n+++ got\n"+tt.diff {
				t.Errorf("Diff =\n%s\nwant\n--- want\n+++ got\n%s", got, tt.diff)
			}
		})
	}
}

func TestDiffOfLargeInputReportsFirstDifference(t *testing.T) {
	a := strings.Repeat("same\n", 3000)
	got := Diff(a+"x\n", a+"y\n")
	want := "--- want\n+++ got\n@@ first difference at line 3001 (3001 want lines, 3001 got lines) @@\n-x\n+y\n"
	if got != want {
		t.Errorf("Diff =\n%q\nwant\n%q", got, want)
	}
	if got := Diff(a, a+"tail\n"); !strings.HasSuffix(got, "@@\n+tail\n") {
		t.Errorf("Diff of an appended line = %q", got)
	}
}

// TestDiffGolden records Diff's own output format with the helper it serves.
func TestDiffGolden(t *testing.T) {
	Assert(t, "diff", Diff("one\ntwo\nthree\nfour\n", "one\n2\nthree\nfour\nfive"))
}
