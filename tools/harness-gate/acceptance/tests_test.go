package acceptance

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// traceImpl is a two-stage plan: DH-S-1's steps link two deliverables of
// areas/x.md, and DH-S-16, a later stage, names DH-T-X-4 and -5 in its text.
const traceImpl = `# plan

## 6. The stages

<a id="dh-s-1"></a>
### DH-S-1: One

**Kind and priority.** Build, P0. Paired acceptance stage: [DH-S-3](#dh-s-3).

**Steps.**

- [x] **1.1** [DH-D-X-1](areas/x.md#dh-d-x-1) A thing (` + "`DH-T-X-9`" + `).
- [x] **1.2** [DH-D-X-2](areas/x.md#dh-d-x-2) Another, which also
  names DH-T-X-1 on its continuation line.

<a id="dh-s-16"></a>
### DH-S-16: Later

**Steps.**

- [ ] **16.1** Something.

**Exit evidence.** The record, with ` + "`DH-T-X-4`, `-5`" + ` passing.

## 7. Acceptance
`

const traceSpec = `# x

<a id="dh-d-x-1"></a>
### DH-D-X-1 A thing
- **Result.** Something.
- **Priority.** P0. **Done when.** ` + "`DH-T-X-1`, `-2` and" + `
  ` + "`DH-T-X-3` to `-5`" + ` pass.
- **Components.** none.

<a id="dh-d-x-2"></a>
### DH-D-X-2 Another
- **Done when.** DH-T-Y-1..3 pass, and DH-E-X-1 has passed.

<a id="dh-d-x-3"></a>
### DH-D-X-3 No done line
- **Result.** Nothing.
`

// writeTree writes files (slash paths) below a new temporary root.
func writeTree(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func traceTree(t *testing.T, extra map[string]string) string {
	t.Helper()
	files := map[string]string{
		ImplPath:                  traceImpl,
		"docs/harness/areas/x.md": traceSpec,
		IndexPath:                 "built-through: DH-S-1\nstages:\n  DH-S-3: []\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	return writeTree(t, files)
}

func testFile(funcs string) string {
	return "package x\n\nimport \"testing\"\n\n" + funcs
}

func TestTraceTestsExpandsRanges(t *testing.T) {
	for text, want := range map[string][]string{
		"`DH-T-AGENT-1`, `-2`, `-3`, `-27` pass":               {"DH-T-AGENT-1", "DH-T-AGENT-2", "DH-T-AGENT-3", "DH-T-AGENT-27"},
		"`DH-T-CONTEXT-1` to `-3` and `-25` pass":              {"DH-T-CONTEXT-1", "DH-T-CONTEXT-2", "DH-T-CONTEXT-3", "DH-T-CONTEXT-25"},
		"DH-T-SESSION-3 to DH-T-SESSION-5 and DH-T-SESSION-19": {"DH-T-SESSION-3", "DH-T-SESSION-4", "DH-T-SESSION-5", "DH-T-SESSION-19"},
		"DH-T-Y-1..3 pass":                                        {"DH-T-Y-1", "DH-T-Y-2", "DH-T-Y-3"},
		"`DH-T-X-1`..`-2`, `DH-T-Z-7` through `-8`":               {"DH-T-X-1", "DH-T-X-2", "DH-T-Z-7", "DH-T-Z-8"},
		"`DH-T-NODEPROC-1` to `-2`, `DH-T-NODEPROC-10`, `-11`":    {"DH-T-NODEPROC-1", "DH-T-NODEPROC-2", "DH-T-NODEPROC-10", "DH-T-NODEPROC-11"},
		"`DH-T-CORDIS-1`, `DH-T-CORDIS-2` and\n  `DH-T-CORDIS-3`": {"DH-T-CORDIS-1", "DH-T-CORDIS-2", "DH-T-CORDIS-3"},
		"`DH-T-X-1` passes under `go test -race -count=5`":        {"DH-T-X-1"},
		"DH-T-X-4, 2 seconds; DH-T-X-4 again":                     {"DH-T-X-4"},
		"DH-T-X-1, -3rd item":                                     {"DH-T-X-1"},
		"DH-T-X-5 to -2 (backwards)":                              {"DH-T-X-5", "DH-T-X-2"},
		"the `DH-T-CORDIS-` tests":                                nil,
	} {
		if got := ExpandTestIDs(text); !slices.Equal(got, want) {
			t.Errorf("ExpandTestIDs(%q) = %v, want %v", text, got, want)
		}
	}

	root := traceTree(t, nil)
	p, err := ReadImpl(root)
	if err != nil {
		t.Fatal(err)
	}
	steps, problems := RequiredTests(root, p, 1)
	want := map[string][]string{
		// 1.2's own text names DH-T-X-1, so it is listed there, not at 1.1.
		"1.1": {"DH-T-X-9", "DH-T-X-2", "DH-T-X-3", "DH-T-X-4", "DH-T-X-5"},
		"1.2": {"DH-T-X-1", "DH-T-Y-1", "DH-T-Y-2", "DH-T-Y-3"},
	}
	if len(steps) != 2 {
		t.Fatalf("steps = %+v", steps)
	}
	for _, st := range steps {
		if !slices.Equal(st.Required, want[st.Step]) {
			t.Errorf("step %s requires %v, want %v", st.Step, st.Required, want[st.Step])
		}
	}
	if len(problems) != 0 {
		t.Errorf("problems = %v", problems)
	}
}

func TestTraceTestsMissingIDFails(t *testing.T) {
	// One test tags DH-T-X-1 with the directive; the rest of 1.1's IDs are named
	// in the legacy forms except DH-T-X-2, which no test names.
	root := traceTree(t, map[string]string{
		"src/m/x_test.go": testFile(`// TestOne proves the first.
//
//dsh:test DH-T-X-1, DH-T-X-9
func TestOne(t *testing.T) {}

// TestThree: DH-T-X-3 to -4 (old style).
func TestThree(t *testing.T) { // DH-T-X-5
	// DH-T-Y-1, -2, -3
}

// helper names DH-T-X-2, but it is not a test.
func helper() {}
`),
	})
	r, err := TraceTests(root, "")
	if err != nil {
		t.Fatal(err)
	}
	err = r.Err()
	if err == nil || !strings.Contains(err.Error(), "step 1.1 of DH-S-1: no test names DH-T-X-2") {
		t.Fatalf("Err = %v, want DH-T-X-2 named with its step", err)
	}
	if req, tag, def, miss := r.Totals("DH-S-1"); req != 9 || tag != 8 || def != 0 || miss != 1 {
		t.Errorf("totals = %d required, %d tagged, %d deferred, %d missing; want 9, 8, 0, 1", req, tag, def, miss)
	}
	if r.Forms[FormDirective] != 2 || r.Forms[FormDoc] != 2 || r.Forms[FormBody] != 4 {
		t.Errorf("forms = %v, want 2 directive, 2 doc comment, 4 body comment", r.Forms)
	}
	var out bytes.Buffer
	r.Write(&out)
	for _, want := range []string{"trace-tests: DH-S-1..DH-S-1", "       missing: DH-T-X-2", "  DH-S-1 total: required 9, tagged 8, deferred 0, missing 1", "tagged by form: directive 2, doc comment 2, body comment 4"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}

	// Tagging DH-T-X-2 makes the check pass.
	tagged := traceTree(t, map[string]string{
		"src/m/x_test.go": testFile("//dsh:test DH-T-X-1, DH-T-X-2, DH-T-X-3, DH-T-X-4, DH-T-X-5, DH-T-X-9\nfunc TestAll(t *testing.T) {}\n\n//dsh:test DH-T-Y-1, DH-T-Y-2, DH-T-Y-3\nfunc FuzzY(f *testing.F) {}\n"),
	})
	if r, err := TraceTests(tagged, "DH-S-1"); err != nil || r.Err() != nil {
		t.Errorf("TraceTests with every ID tagged = %v, %v", err, r.Err())
	}

	// An unknown --stage and an unreadable plan are input errors.
	if _, err := TraceTests(root, "S-1"); err == nil {
		t.Error("TraceTests(S-1) passed")
	}
	if _, err := TraceTests(t.TempDir(), "DH-S-1"); err == nil || !strings.Contains(err.Error(), ImplPath) {
		t.Errorf("TraceTests on an empty tree = %v, want the plan named", err)
	}
}

func TestTraceTestsDeferralMustBeNamedInImpl(t *testing.T) {
	all := "//dsh:test DH-T-X-1, DH-T-X-2, DH-T-X-4, DH-T-X-5, DH-T-X-9, DH-T-Y-1, DH-T-Y-2, DH-T-Y-3\nfunc TestAll(t *testing.T) {}\n"
	// DH-S-16 names DH-T-X-4 and -5, not DH-T-X-3: the deferral is refused.
	root := traceTree(t, map[string]string{
		"src/m/x_test.go": testFile(all),
		"src/m/doc.go":    "// Package x.\n//\n//dsh:test DH-T-X-3 later DH-S-16\npackage x\n",
	})
	r, err := TraceTests(root, "")
	if err != nil {
		t.Fatal(err)
	}
	err = r.Err()
	if err == nil || !strings.Contains(err.Error(), "deferral DH-T-X-3 later DH-S-16 (src/m/doc.go:3): DH-S-16's section of docs/harness/impl.md does not name DH-T-X-3") {
		t.Fatalf("Err = %v, want the unbacked deferral named", err)
	}
	if !strings.Contains(err.Error(), "no test names DH-T-X-3") {
		t.Errorf("Err = %v: an unbacked deferral leaves the ID missing", err)
	}

	// Deferring IDs DH-S-16 names passes, and they count as deferred.
	backed := traceTree(t, map[string]string{
		"src/m/x_test.go": testFile("//dsh:test DH-T-X-1, DH-T-X-2, DH-T-X-3, DH-T-X-9, DH-T-Y-1, DH-T-Y-2, DH-T-Y-3\nfunc TestAll(t *testing.T) {}\n"),
		"src/m/doc.go":    "//dsh:test DH-T-X-4, DH-T-X-5 later DH-S-16\npackage x\n",
	})
	r, err = TraceTests(backed, "")
	if err != nil || r.Err() != nil {
		t.Fatalf("TraceTests with a backed deferral = %v, %v", err, r.Err())
	}
	if _, _, def, _ := r.Totals(""); def != 2 {
		t.Errorf("deferred = %d, want 2", def)
	}

	// A deferral to a stage without a section, or to one not after the stage that
	// requires the ID, is refused too.
	for doc, want := range map[string]string{
		"//dsh:test DH-T-X-4 later DH-S-99\npackage x\n": "DH-S-99 has no section",
		"//dsh:test DH-T-X-9 later DH-S-1\npackage x\n":  "DH-T-X-9 is required by DH-S-1, and DH-S-1 is not later",
	} {
		tree := traceTree(t, map[string]string{"src/m/x_test.go": testFile(all + "\n//dsh:test DH-T-X-3\nfunc TestX3(t *testing.T) {}\n"), "src/m/doc.go": doc})
		r, err := TraceTests(tree, "DH-S-1")
		if err != nil {
			t.Fatal(err)
		}
		if err := r.Err(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("deferral %q: Err = %v, want %q", doc, err, want)
		}
	}
}

func TestParseTestFileRejects(t *testing.T) {
	tests := map[string]struct{ name, src, want string }{
		"on a helper":         {"a_test.go", testFile("//dsh:test DH-T-X-1\nfunc helper() {}\n"), "is not directly above a top-level Test, Fuzz or Benchmark function"},
		"on a method":         {"a_test.go", testFile("type s struct{}\n\n//dsh:test DH-T-X-1\nfunc (s) TestX(t *testing.T) {}\n"), "is not directly above"},
		"inside a body":       {"a_test.go", testFile("func TestX(t *testing.T) {\n\t//dsh:test DH-T-X-1\n}\n"), "is not directly above"},
		"floating":            {"a_test.go", testFile("//dsh:test DH-T-X-1\n\nfunc TestX(t *testing.T) {}\n"), "is not directly above"},
		"lowercase test name": {"a_test.go", testFile("//dsh:test DH-T-X-1\nfunc Testx(t *testing.T) {}\n"), "is not directly above"},
		"bad ID":              {"a_test.go", testFile("//dsh:test DH-E-X-1\nfunc TestX(t *testing.T) {}\n"), `"DH-E-X-1" is not a test ID`},
		"shorthand":           {"a_test.go", testFile("//dsh:test DH-T-X-1, -2\nfunc TestX(t *testing.T) {}\n"), `"-2" is not a test ID`},
		"deferral on a test":  {"a_test.go", testFile("//dsh:test DH-T-X-1 later DH-S-16\nfunc TestX(t *testing.T) {}\n"), "a deferral belongs in the package's doc.go"},
		"bad later stage":     {"doc.go", "//dsh:test DH-T-X-1 later S-16\npackage x\n", `"S-16" is not a stage ID`},
		"doc.go tag":          {"doc.go", "//dsh:test DH-T-X-1\npackage x\n", `needs "later DH-S-n"`},
		"doc.go bad ID":       {"doc.go", "//dsh:test X later DH-S-2\npackage x\n", `"X" is not a test ID`},
		"syntax":              {"a_test.go", "package", "expected"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, err := ParseTestFile("p/"+tt.name, []byte(tt.src))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("ParseTestFile = %v, want an error containing %q", err, tt.want)
			}
		})
	}
	// Other //dsh: directives and a test function in doc.go are not tags.
	tags, defs, err := ParseTestFile("p/doc.go", []byte("//dsh:testing is another word\npackage x\n\nfunc TestNot() {} // DH-T-X-1\n"))
	if err != nil || len(tags) != 0 || len(defs) != 0 {
		t.Errorf("ParseTestFile(doc.go) = %v, %v, %v; want nothing", tags, defs, err)
	}
	tags, _, err = ParseTestFile("p/b_test.go", []byte(testFile("//dsh:test DH-T-X-1\nfunc Benchmark(b *testing.B) { // DH-T-X-1\n}\n")))
	if err != nil || len(tags) != 1 || tags[0].Form != FormDirective || tags[0].Test != "Benchmark" {
		t.Errorf("ParseTestFile = %+v, %v; want one directive tag on Benchmark", tags, err)
	}
}

func TestScanTestsSkipsTestdataAndReportsBadFiles(t *testing.T) {
	root := writeTree(t, map[string]string{
		"src/m/a_test.go":           testFile("//dsh:test DH-T-X-1\nfunc TestA(t *testing.T) {}\n"),
		"src/m/testdata/b_test.go":  "broken",
		"src/m/.hidden/c_test.go":   "broken",
		"src/m/vendor/d_test.go":    "broken",
		"src/m/plain.go":            "broken",
		"tools/t/doc.go":            "//dsh:test DH-T-X-2 later DH-S-16\npackage t\n",
		"tools/t/node_modules/e.go": "broken",
	})
	tags, defs, err := ScanTests(root, []string{filepath.Join(root, "src"), filepath.Join(root, "tools"), filepath.Join(root, "absent")})
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 1 || tags[0].File != "src/m/a_test.go" || len(defs) != 1 || defs[0].File != "tools/t/doc.go" {
		t.Errorf("ScanTests = %+v, %+v", tags, defs)
	}
	bad := writeTree(t, map[string]string{"src/m/z_test.go": "package"})
	if _, _, err := ScanTests(bad, []string{filepath.Join(bad, "src")}); err == nil || !strings.Contains(err.Error(), "src/m/z_test.go") {
		t.Errorf("ScanTests on a broken file = %v, want it named", err)
	}
	// The broken file is a problem of the report, not an input error.
	tree := traceTree(t, map[string]string{"src/m/z_test.go": "package"})
	r, err := TraceTests(tree, "")
	if err != nil || r.Err() == nil || !strings.Contains(r.Err().Error(), "src/m/z_test.go") {
		t.Errorf("TraceTests with a broken file = %v, %v", err, r.Err())
	}
}

func TestRequiredTestsReportsBrokenLinks(t *testing.T) {
	impl := strings.Replace(traceImpl, "areas/x.md#dh-d-x-2", "areas/x.md#dh-d-x-3", 1)
	impl = strings.Replace(impl, "- [ ] **16.1** Something.", "- [ ] **16.1** [DH-D-X-4](areas/x.md#dh-d-x-4) Gone, [DH-D-Q-1](areas/q.md#dh-d-q-1).", 1)
	root := writeTree(t, map[string]string{ImplPath: impl, "docs/harness/areas/x.md": traceSpec})
	p, err := ReadImpl(root)
	if err != nil {
		t.Fatal(err)
	}
	_, problems := RequiredTests(root, p, 17)
	joined := strings.Join(problems, "\n")
	for _, want := range []string{
		`step 1.2 links DH-D-X-3, which has no "Done when" line (areas/x.md)`,
		"step 16.1 links areas/x.md#dh-d-x-4: no such anchor",
		"step 16.1 links areas/q.md:",
		"DH-S-2 has no section in docs/harness/impl.md",
		"DH-S-17 has no section",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("problems lack %q:\n%s", want, joined)
		}
	}
}
