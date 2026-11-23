package analyzers

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// The site exception of DH-ADR-TOOLCHAIN-8 (M1-G-TOOL-7). A finding is accepted only
// by a directive at its site that names the check and says why:
//
//	//lint:ignore errdiscard,typeassert the reason, in words
//
// It is the form staticcheck reads (honnef.co/go/tools lintcmd/directives.go), and it
// is matched the same way: the directive belongs to the statement (or declaration)
// the comment map associates it with -- the one on the next line when the comment
// stands on its own line, the one it ends when it trails code -- and it suppresses
// the named checks on the line where that statement starts. One reader serves the
// own analyzers and staticcheck, so one total counts both.
//
// A directive without a reason, without a check, or naming a check that is neither
// an own analyzer nor a staticcheck check is reported by the lintignore analyzer and
// suppresses nothing. A //lint:file-ignore is reported too: an exception is written
// at its site, never for a whole file. No baseline, exemption list or file of
// recorded violations exists (QG-2): a directive with a reason is not a list.

const (
	ignorePrefix     = "//lint:ignore"
	fileIgnorePrefix = "//lint:file-ignore"
)

// ownChecks are the names the own analyzers report under: every analyzer of All and
// of Pending (TestOwnChecksNameEveryAnalyzer keeps this list in step).
var ownChecks = []string{"nosleep", "onespawn", "cleanup", "fuzzcheck", "rootcall", "lintignore", "errdiscard", "typeassert"}

// staticcheckCheck matches the check names of staticcheck's analyzers: SA, S, ST and
// QF followed by four digits, and U1000.
var staticcheckCheck = regexp.MustCompile(`^(?:(?:SA|S|ST|QF)[0-9]{4}|U1000)$`)

// canonicalCheck is check as the totals name it: a staticcheck check in upper case,
// an own analyzer in lower case.
func canonicalCheck(check string) string {
	if staticcheckCheck.MatchString(strings.ToUpper(check)) {
		return strings.ToUpper(check)
	}
	return strings.ToLower(check)
}

// KnownCheck reports whether a directive may name check: an own analyzer, or a
// staticcheck check. Names are matched without regard to case, as staticcheck does;
// a pattern such as "SA*" is not a check.
func KnownCheck(check string) bool {
	return slices.Contains(ownChecks, strings.ToLower(check)) || staticcheckCheck.MatchString(strings.ToUpper(check))
}

// Directive is one //lint:ignore (or //lint:file-ignore) comment.
type Directive struct {
	Pos    token.Pos // the comment
	Line   int       // the line of the statement it suppresses checks on
	Checks []string  // the named checks, as written
	Reason string    // everything after the checks; empty when missing
	File   bool      // a //lint:file-ignore, which is never accepted
}

// Problem is why the directive is not an accepted exception, or "" when it is one.
func (d Directive) Problem() string {
	switch {
	case d.File:
		return "//lint:file-ignore is not allowed: write //lint:ignore <check> <reason> at each site (DH-ADR-TOOLCHAIN-8)"
	case len(d.Checks) == 0:
		return "//lint:ignore names no check: write //lint:ignore <check>[,<check>] <reason> (DH-ADR-TOOLCHAIN-8)"
	}
	for _, c := range d.Checks {
		if !KnownCheck(c) {
			return fmt.Sprintf("//lint:ignore names unknown check %q: name an own analyzer or a staticcheck check (DH-ADR-TOOLCHAIN-8)", c)
		}
	}
	if d.Reason == "" {
		return fmt.Sprintf("//lint:ignore %s has no reason: say why the finding is accepted (DH-ADR-TOOLCHAIN-8)", strings.Join(d.Checks, ","))
	}
	return ""
}

// Accepted reports whether the directive is a valid, reasoned exception.
func (d Directive) Accepted() bool { return d.Problem() == "" }

// suppresses reports whether the accepted directive covers check at line.
func (d Directive) suppresses(check string, line int) bool {
	if !d.Accepted() || d.Line != line {
		return false
	}
	return slices.ContainsFunc(d.Checks, func(c string) bool { return strings.EqualFold(c, check) })
}

// parseDirective parses the text of one comment; ok is false when it is not a
// directive.
func parseDirective(text string) (d Directive, ok bool) {
	var rest string
	switch {
	case strings.HasPrefix(text, fileIgnorePrefix):
		rest, d.File = text[len(fileIgnorePrefix):], true
	case strings.HasPrefix(text, ignorePrefix):
		rest = text[len(ignorePrefix):]
	default:
		return Directive{}, false
	}
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return Directive{}, false // "//lint:ignored", some other word
	}
	fields := strings.Fields(rest)
	if len(fields) > 0 {
		d.Checks = slices.DeleteFunc(strings.Split(fields[0], ","), func(c string) bool { return c == "" })
		d.Reason = strings.Join(fields[1:], " ")
	}
	return d, true
}

// Directives returns the directives of file, each with the line it applies to.
func Directives(fset *token.FileSet, file *ast.File) []Directive {
	has := false
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//lint:") {
				has = true
			}
		}
	}
	if !has {
		return nil
	}
	var out []Directive
	for node, groups := range ast.NewCommentMap(fset, file, file.Comments) {
		for _, cg := range groups {
			for _, c := range cg.List {
				d, ok := parseDirective(c.Text)
				if !ok {
					continue
				}
				d.Pos, d.Line = c.Pos(), fset.Position(node.Pos()).Line
				out = append(out, d)
			}
		}
	}
	slices.SortFunc(out, func(a, b Directive) int { return int(a.Pos - b.Pos) })
	return out
}

// LintIgnore reports every directive that is not an accepted exception: no reason,
// no check, an unknown check, or a //lint:file-ignore (DH-ADR-TOOLCHAIN-8).
var LintIgnore = &analysis.Analyzer{
	Name: "lintignore",
	Doc:  "require a known check and a reason on every //lint:ignore; forbid //lint:file-ignore",
	Run: func(pass *analysis.Pass) (any, error) {
		for _, file := range pass.Files {
			for _, d := range Directives(pass.Fset, file) {
				if p := d.Problem(); p != "" {
					pass.Report(analysis.Diagnostic{Pos: d.Pos, Message: p})
				}
			}
		}
		return nil, nil
	},
}

// report reports findings of check, except those an accepted directive at their site
// suppresses.
func report(pass *analysis.Pass, check string, findings []Finding) {
	if len(findings) == 0 {
		return
	}
	byName := map[string][]Directive{}
	for _, file := range pass.Files {
		if ds := Directives(pass.Fset, file); len(ds) > 0 {
			byName[pass.Fset.File(file.FileStart).Name()] = ds
		}
	}
	for _, f := range findings {
		pos := pass.Fset.Position(f.Pos)
		if slices.ContainsFunc(byName[pos.Filename], func(d Directive) bool { return d.suppresses(check, pos.Line) }) {
			continue
		}
		pass.Report(analysis.Diagnostic{Pos: f.Pos, Message: f.Message})
	}
}

// ExceptionTotals counts the accepted exceptions of a set of files.
type ExceptionTotals struct {
	Total   int            // accepted directives
	ByCheck map[string]int // per named check (canonicalCheck); a directive naming two counts for both
}

// String is "N" or "N (check n, ...)", the checks in order.
func (e ExceptionTotals) String() string {
	if e.Total == 0 {
		return "0"
	}
	var parts []string
	for _, c := range slices.Sorted(maps.Keys(e.ByCheck)) {
		parts = append(parts, fmt.Sprintf("%s %d", c, e.ByCheck[c]))
	}
	return fmt.Sprintf("%d (%s)", e.Total, strings.Join(parts, ", "))
}

// Exceptions parses the Go files at paths and totals their accepted exceptions, of
// the own analyzers and of staticcheck alike. Every file is read, whatever its build
// constraints, so an exception in a file of another platform or test layer counts.
// The vet gate prints the total ("exceptions: N").
func Exceptions(paths []string) (ExceptionTotals, error) {
	totals := ExceptionTotals{ByCheck: map[string]int{}}
	fset := token.NewFileSet()
	for _, p := range paths {
		file, err := parser.ParseFile(fset, p, nil, parser.ParseComments|parser.SkipObjectResolution)
		if err != nil {
			return ExceptionTotals{}, err
		}
		for _, d := range Directives(fset, file) {
			if !d.Accepted() {
				continue
			}
			totals.Total++
			for _, c := range d.Checks {
				totals.ByCheck[canonicalCheck(c)]++
			}
		}
	}
	return totals, nil
}
