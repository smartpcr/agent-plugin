package acceptance

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// TestDirective names the DH-T IDs a test proves (M1-G-CLOSE-9). It is the last
// line of the doc comment of a top-level Test, Fuzz or Benchmark function, so it
// sits on the line directly above the func:
//
//	//dsh:test DH-T-STORAGE-4, DH-T-STORAGE-14
//	func TestHomeIsPrivate(t *testing.T) { ... }
//
// A deferral exists only where impl.md defers the test. It is written in the
// package's doc.go and names the later stage whose impl.md section names the ID:
//
//	//dsh:test DH-T-TOOLCHAIN-1 later DH-S-16
//	package scenarios
//
// It is not an exemption list (QG-2): trace-tests accepts the deferral only when
// that stage's section of impl.md names the ID.
const TestDirective = "//dsh:test"

// The tag forms trace-tests accepts. FormDirective is the convention; the other two
// are the comment styles the tests carried before it, accepted so the report is
// meaningful while they migrate: an ID in the doc comment of the test function, or
// in a comment inside its body (the trailing "{ // DH-T-..." of the func line
// included).
const (
	FormDirective = "directive"
	FormDoc       = "doc comment"
	FormBody      = "body comment"
)

var (
	testIDPattern = regexp.MustCompile(`^DH-T-[A-Z][A-Z0-9]*-[1-9][0-9]*$`)
	testIDRef     = regexp.MustCompile(`DH-T-([A-Z][A-Z0-9]*)-([0-9]+)`)
	// testIDNext continues an ID list or range: a separator, then a full ID, a
	// shorthand "-n" in the area of the ID before it, or (after "..") a bare n.
	testIDNext = regexp.MustCompile("^`?(\\s*,\\s*(?:and\\s+|or\\s+)?|\\s+and\\s+|\\s+or\\s+|\\s+to\\s+|\\s+through\\s+|\\s*\\.\\.\\s*)`?(?:DH-T-([A-Z][A-Z0-9]*)-([0-9]+)|-([0-9]+)|([0-9]+))`?")
)

// maxRange bounds one expanded range, so a typo cannot make a list of millions.
const maxRange = 500

// ExpandTestIDs returns the DH-T IDs text names, in order and once each. A list or
// range may continue an ID with shorthand in its area: "`DH-T-X-1`, `-2` and `-5`",
// "`DH-T-X-1` to `-4`", "DH-T-X-1 to DH-T-X-4" and "DH-T-X-1..4" all expand.
func ExpandTestIDs(text string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(area string, n int) {
		id := "DH-T-" + area + "-" + strconv.Itoa(n)
		if n > 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for {
		loc := testIDRef.FindStringSubmatchIndex(text)
		if loc == nil {
			return out
		}
		area := text[loc[2]:loc[3]]
		n, _ := strconv.Atoi(text[loc[4]:loc[5]])
		add(area, n)
		text = text[loc[1]:]
		for {
			m := testIDNext.FindStringSubmatchIndex(text)
			if m == nil {
				break
			}
			if m[1] < len(text) && isWordByte(text[m[1]]) {
				break // "-3rd", "-30s": not an ID
			}
			sep := strings.TrimSpace(text[m[2]:m[3]])
			ranged := sep == "to" || sep == "through" || sep == ".."
			var next int
			nextArea := area
			switch {
			case m[4] >= 0:
				nextArea = text[m[4]:m[5]]
				next, _ = strconv.Atoi(text[m[6]:m[7]])
			case m[8] >= 0:
				next, _ = strconv.Atoi(text[m[8]:m[9]])
			case sep == "..":
				next, _ = strconv.Atoi(text[m[10]:m[11]])
			}
			if next == 0 {
				break // a bare number after a list separator is not an ID
			}
			if ranged && nextArea == area && next > n && next-n <= maxRange {
				for k := n + 1; k <= next; k++ {
					add(area, k)
				}
			} else {
				add(nextArea, next)
			}
			area, n = nextArea, next
			text = text[m[1]:]
		}
	}
}

func isWordByte(b byte) bool {
	return b == '_' || '0' <= b && b <= '9' || 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z'
}

// TestTag is one DH-T ID a test function names.
type TestTag struct {
	ID   string
	File string // slash path relative to the scanned root
	Test string // TestXxx, FuzzXxx or BenchmarkXxx
	Form string // FormDirective, FormDoc or FormBody
	Line int
}

// Deferral is one "//dsh:test DH-T-X-n later DH-S-m" of a package's doc.go.
type Deferral struct {
	ID    string
	Stage string // DH-S-m
	File  string
	Line  int
}

func (d Deferral) String() string {
	return fmt.Sprintf("%s later %s (%s:%d)", d.ID, d.Stage, d.File, d.Line)
}

// ScanTests returns the tags and deferrals of every *_test.go and doc.go file below
// dirs (all build tags; testdata, vendor, node_modules and hidden directories
// skipped), File relative to root. A misplaced or malformed directive is an error.
func ScanTests(root string, dirs []string) ([]TestTag, []Deferral, error) {
	var tags []TestTag
	var defs []Deferral
	var errs []error
	for _, dir := range dirs {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			continue
		}
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if p != dir && (name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(name, "_test.go") && name != "doc.go" {
				return nil
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			t, df, err := ParseTestFile(filepath.ToSlash(rel), src)
			if err != nil {
				errs = append(errs, err)
			}
			tags, defs = append(tags, t...), append(defs, df...)
			return nil
		})
		if err != nil {
			errs = append(errs, err)
		}
	}
	return tags, defs, errors.Join(errs...)
}

// ParseTestFile returns the tags of a *_test.go file, or the deferrals of a doc.go;
// name is the file's slash path. A directive outside the doc comment of a top-level
// Test, Fuzz or Benchmark function, a deferral outside doc.go, a doc.go directive
// without "later", and a malformed ID are errors; what parsed is still returned.
func ParseTestFile(name string, src []byte) ([]TestTag, []Deferral, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, name, src, parser.ParseComments)
	if err != nil {
		return nil, nil, err
	}
	isDoc := filepath.Base(name) == "doc.go"
	var tags []TestTag
	var defs []Deferral
	var errs []error
	pos := func(c *ast.Comment) string { return fset.Position(c.Pos()).String() }
	attached := map[*ast.Comment]bool{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || isDoc || fn.Recv != nil || !isTestFunc(fn.Name.Name) {
			continue
		}
		if fn.Doc != nil {
			// The doc text without its directive lines, joined, so a list may
			// continue on the next line.
			for _, id := range ExpandTestIDs(fn.Doc.Text()) {
				tags = append(tags, TestTag{ID: id, File: name, Test: fn.Name.Name, Form: FormDoc, Line: fset.Position(fn.Doc.Pos()).Line})
			}
			for _, c := range fn.Doc.List {
				rest, ok := directiveRest(c.Text)
				if !ok {
					continue
				}
				attached[c] = true
				ids, later, err := parseTestDirective(rest)
				switch {
				case err != nil:
					errs = append(errs, fmt.Errorf("%s: %s %s: %w", pos(c), TestDirective, rest, err))
				case later != "":
					errs = append(errs, fmt.Errorf("%s: %s %s: a deferral belongs in the package's doc.go, not on a test", pos(c), TestDirective, rest))
				default:
					for _, id := range ids {
						tags = append(tags, TestTag{ID: id, File: name, Test: fn.Name.Name, Form: FormDirective, Line: fset.Position(c.Pos()).Line})
					}
				}
			}
		}
		if fn.Body == nil {
			continue
		}
		for _, g := range f.Comments {
			if g.Pos() < fn.Body.Lbrace || g.End() > fn.Body.Rbrace {
				continue
			}
			for _, c := range g.List {
				if _, ok := directiveRest(c.Text); ok {
					continue // reported below as not on a test
				}
				for _, id := range ExpandTestIDs(c.Text) {
					tags = append(tags, TestTag{ID: id, File: name, Test: fn.Name.Name, Form: FormBody, Line: fset.Position(c.Pos()).Line})
				}
			}
		}
	}
	for _, g := range f.Comments {
		for _, c := range g.List {
			rest, ok := directiveRest(c.Text)
			if !ok || attached[c] {
				continue
			}
			if !isDoc {
				errs = append(errs, fmt.Errorf("%s: %s %s is not directly above a top-level Test, Fuzz or Benchmark function", pos(c), TestDirective, rest))
				continue
			}
			ids, later, err := parseTestDirective(rest)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %s %s: %w", pos(c), TestDirective, rest, err))
			case later == "":
				errs = append(errs, fmt.Errorf("%s: %s %s: a doc.go directive is a deferral and needs \"later DH-S-n\"", pos(c), TestDirective, rest))
			default:
				for _, id := range ids {
					defs = append(defs, Deferral{ID: id, Stage: later, File: name, Line: fset.Position(c.Pos()).Line})
				}
			}
		}
	}
	return dedupeTags(tags), defs, errors.Join(errs...)
}

func isTestFunc(name string) bool {
	for _, prefix := range []string{"Test", "Fuzz", "Benchmark"} {
		if rest, ok := strings.CutPrefix(name, prefix); ok && (rest == "" || !('a' <= rest[0] && rest[0] <= 'z')) {
			return true
		}
	}
	return false
}

func directiveRest(text string) (string, bool) {
	rest, ok := strings.CutPrefix(text, TestDirective)
	if !ok || (rest != "" && rest[0] != ' ') {
		return "", false
	}
	return strings.TrimSpace(rest), true
}

// parseTestDirective parses "DH-T-X-1[, DH-T-Y-2 ...] [later DH-S-n]".
func parseTestDirective(rest string) (ids []string, later string, err error) {
	list := rest
	if i := strings.Index(rest, " later "); i >= 0 {
		list, later = rest[:i], strings.TrimSpace(rest[i+len(" later "):])
		if _, ok := StageNumber(later); !ok {
			return nil, "", fmt.Errorf("%q is not a stage ID (DH-S-n)", later)
		}
	}
	for _, item := range strings.Split(list, ",") {
		item = strings.TrimSpace(item)
		if !testIDPattern.MatchString(item) {
			return nil, "", fmt.Errorf("%q is not a test ID (DH-T-<AREA>-<n>)", item)
		}
		ids = append(ids, item)
	}
	return ids, later, nil
}

// dedupeTags keeps one tag per (ID, file, test), the strongest form.
func dedupeTags(tags []TestTag) []TestTag {
	rank := map[string]int{FormDirective: 0, FormDoc: 1, FormBody: 2}
	var out []TestTag
	for _, t := range tags {
		i := slices.IndexFunc(out, func(o TestTag) bool { return o.ID == t.ID && o.File == t.File && o.Test == t.Test })
		switch {
		case i < 0:
			out = append(out, t)
		case rank[t.Form] < rank[out[i].Form]:
			out[i] = t
		}
	}
	return out
}

// StepTests is what one impl.md step requires: the DH-T IDs its own text names and
// those of the "Done when" lines of the deliverables it links to. Each ID is listed
// at one step: the first step whose own text names it, else the first step linking
// a deliverable whose "Done when" line names it.
type StepTests struct {
	Stage, Step string
	Required    []string
	Tagged      []string
	Deferred    []string
	Missing     []string
}

// TestReport is the result of trace-tests.
type TestReport struct {
	Through  string
	Steps    []StepTests
	Forms    map[string]int // required IDs tagged, by the strongest form that tags each
	Problems []string       // broken links, bad deferrals, misplaced directives
}

// RequiredTests returns, for every step of DH-S-1..through, the DH-T IDs it
// requires; problems names each link to a deliverable whose file or anchor is
// missing, and each such deliverable without a "Done when" line.
func RequiredTests(root string, p *ImplPlan, through int) (steps []StepTests, problems []string) {
	specs := map[string][]string{}
	seen := map[string]bool{}
	var stages []*ImplStage
	for n := 1; n <= through; n++ {
		s, ok := p.Stage(fmt.Sprintf("DH-S-%d", n))
		if !ok {
			problems = append(problems, fmt.Sprintf("DH-S-%d has no section in %s", n, ImplPath))
			continue
		}
		stages = append(stages, s)
	}
	named := map[string]string{} // ID -> the first step whose own text names it
	for _, s := range stages {
		for _, st := range s.Steps {
			for _, id := range ExpandTestIDs(st.Text) {
				if _, ok := named[id]; !ok {
					named[id] = s.ID + "/" + st.ID
				}
			}
		}
	}
	for _, s := range stages {
		for _, st := range s.Steps {
			ids := ExpandTestIDs(st.Text)
			for _, d := range st.Deliverables {
				lines, ok := specs[d.File]
				if !ok {
					data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(specDir), filepath.FromSlash(d.File)))
					if err != nil {
						problems = append(problems, fmt.Sprintf("step %s links %s: %v", st.ID, d.File, err))
					}
					lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
					specs[d.File] = lines
				}
				done, found := doneWhen(lines, d.Anchor)
				switch {
				case !found:
					if len(lines) > 1 {
						problems = append(problems, fmt.Sprintf("step %s links %s#%s: no such anchor", st.ID, d.File, d.Anchor))
					}
				case done == "":
					problems = append(problems, fmt.Sprintf("step %s links %s, which has no \"Done when\" line (%s)", st.ID, d.ID, d.File))
				default:
					ids = append(ids, ExpandTestIDs(done)...)
				}
			}
			var req []string
			for _, id := range ids {
				if at, ok := named[id]; ok && at != s.ID+"/"+st.ID {
					continue // listed at the step that names it
				}
				if !seen[id] {
					seen[id] = true
					req = append(req, id)
				}
			}
			steps = append(steps, StepTests{Stage: s.ID, Step: st.ID, Required: req})
		}
	}
	return steps, problems
}

// doneWhen returns the text of the "Done when" lines of the deliverable section at
// anchor: from the marker to the end of its bullet or paragraph.
func doneWhen(lines []string, anchor string) (string, bool) {
	start := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, `<a id="`+anchor+`"></a>`) })
	if start < 0 {
		return "", false
	}
	var b strings.Builder
	headings := 0
	for i := start + 1; i < len(lines); i++ {
		l := strings.TrimSpace(lines[i])
		if strings.HasPrefix(l, "<a id=") {
			break
		}
		if strings.HasPrefix(l, "#") {
			if headings++; headings > 1 {
				break
			}
			continue
		}
		_, after, ok := strings.Cut(l, "Done when.")
		if !ok {
			continue
		}
		b.WriteString(strings.TrimPrefix(after, "**"))
		for i+1 < len(lines) {
			next := strings.TrimSpace(lines[i+1])
			if next == "" || strings.HasPrefix(next, "- ") || strings.HasPrefix(next, "#") || strings.HasPrefix(next, "<a ") || strings.HasPrefix(next, "|") {
				break
			}
			b.WriteString(" " + next)
			i++
		}
		b.WriteByte(' ')
	}
	return strings.TrimSpace(b.String()), true
}

// TraceTestDirs are the directories, relative to the repository root, whose tests
// trace-tests scans: every Go module of the repository lives below one of them.
var TraceTestDirs = []string{"src", "tools"}

// TraceTests checks the repository at root: every DH-T ID that a step of
// DH-S-1..through requires is named by a test (any accepted form) or deferred by a
// valid doc.go deferral. through is "DH-S-n", or empty for the index's
// built-through. An error is returned only when the inputs cannot be read; the
// report's Err says whether the check passed.
func TraceTests(root, through string) (*TestReport, error) {
	if through == "" {
		x, err := ReadIndex(root)
		if err != nil {
			return nil, err
		}
		through = x.BuiltThrough
	}
	last, ok := StageNumber(through)
	if !ok {
		return nil, fmt.Errorf("--stage %q is not a stage ID (DH-S-n)", through)
	}
	p, err := ReadImpl(root)
	if err != nil {
		return nil, err
	}
	dirs := make([]string, 0, len(TraceTestDirs))
	for _, d := range TraceTestDirs {
		dirs = append(dirs, filepath.Join(root, d))
	}
	tags, defs, scanErr := ScanTests(root, dirs)
	steps, problems := RequiredTests(root, p, last)
	r := &TestReport{Through: through, Steps: steps, Forms: map[string]int{}, Problems: problems}
	if scanErr != nil {
		for _, e := range strings.Split(scanErr.Error(), "\n") {
			r.Problems = append(r.Problems, e)
		}
	}
	best := map[string]string{}
	rank := map[string]int{FormDirective: 0, FormDoc: 1, FormBody: 2}
	for _, t := range tags {
		if f, ok := best[t.ID]; !ok || rank[t.Form] < rank[f] {
			best[t.ID] = t.Form
		}
	}
	requiredAt := map[string]int{}
	for _, st := range steps {
		n, _ := StageNumber(st.Stage)
		for _, id := range st.Required {
			requiredAt[id] = n
		}
	}
	valid := map[string]bool{}
	for _, d := range defs {
		s, ok := p.Stage(d.Stage)
		switch {
		case !ok:
			r.Problems = append(r.Problems, fmt.Sprintf("deferral %s: %s has no section in %s", d, d.Stage, ImplPath))
		case !slices.Contains(ExpandTestIDs(s.Text), d.ID):
			r.Problems = append(r.Problems, fmt.Sprintf("deferral %s: %s's section of %s does not name %s, so nothing defers it (QG-2: a deferral is not an exemption)", d, d.Stage, ImplPath, d.ID))
		default:
			if at, req := requiredAt[d.ID]; req {
				if m, _ := StageNumber(d.Stage); m <= at {
					r.Problems = append(r.Problems, fmt.Sprintf("deferral %s: %s is required by DH-S-%d, and %s is not later", d, d.ID, at, d.Stage))
					continue
				}
			}
			valid[d.ID] = true
		}
	}
	for i := range r.Steps {
		st := &r.Steps[i]
		for _, id := range st.Required {
			switch f, tagged := best[id]; {
			case tagged:
				st.Tagged = append(st.Tagged, id)
				r.Forms[f]++
			case valid[id]:
				st.Deferred = append(st.Deferred, id)
			default:
				st.Missing = append(st.Missing, id)
			}
		}
	}
	return r, nil
}

// Totals sums the step counts of stage ("" for every stage): required, tagged,
// deferred and missing.
func (r *TestReport) Totals(stage string) (required, tagged, deferred, missing int) {
	for _, st := range r.Steps {
		if stage == "" || st.Stage == stage {
			required += len(st.Required)
			tagged += len(st.Tagged)
			deferred += len(st.Deferred)
			missing += len(st.Missing)
		}
	}
	return
}

// Err fails on any missing ID and any problem.
func (r *TestReport) Err() error {
	var errs []error
	for _, st := range r.Steps {
		if len(st.Missing) > 0 {
			errs = append(errs, fmt.Errorf("step %s of %s: no test names %s", st.Step, st.Stage, strings.Join(st.Missing, ", ")))
		}
	}
	for _, p := range r.Problems {
		errs = append(errs, errors.New(p))
	}
	return errors.Join(errs...)
}

// Write prints the report: per step the required, tagged, deferred and missing IDs,
// per stage and overall the counts, the forms the tags were found in, and the
// problems.
func (r *TestReport) Write(w io.Writer) {
	list := func(ids []string) string {
		if len(ids) == 0 {
			return "-"
		}
		return strings.Join(ids, ", ")
	}
	fmt.Fprintf(w, "trace-tests: DH-S-1..%s\n", r.Through)
	stage := ""
	flush := func() {
		if stage != "" {
			req, tag, def, miss := r.Totals(stage)
			fmt.Fprintf(w, "  %s total: required %d, tagged %d, deferred %d, missing %d\n", stage, req, tag, def, miss)
		}
	}
	for _, st := range r.Steps {
		if st.Stage != stage {
			flush()
			stage = st.Stage
			fmt.Fprintf(w, "%s\n", stage)
		}
		fmt.Fprintf(w, "  %s required: %s\n", st.Step, list(st.Required))
		if len(st.Required) > 0 {
			fmt.Fprintf(w, "       tagged: %s\n       deferred: %s\n       missing: %s\n", list(st.Tagged), list(st.Deferred), list(st.Missing))
		}
	}
	flush()
	req, tag, def, miss := r.Totals("")
	fmt.Fprintf(w, "total: required %d, tagged %d, deferred %d, missing %d\n", req, tag, def, miss)
	fmt.Fprintf(w, "tagged by form: %s %d, %s %d, %s %d\n", FormDirective, r.Forms[FormDirective], FormDoc, r.Forms[FormDoc], FormBody, r.Forms[FormBody])
	for _, p := range r.Problems {
		fmt.Fprintf(w, "problem: %s\n", p)
	}
}
