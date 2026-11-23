package analyzers

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"
)

// TestNewAnalyzersOnFixtures is the "each new analyzer rejects its seeded violation"
// scenario of plan stage 13.5: every // want of the rootcall, errdiscard, typeassert
// and lintignore fixtures matches and nothing else is reported, and a reasoned
// //lint:ignore silences its site (DH-T-TOOLCHAIN-8).
func TestNewAnalyzersOnFixtures(t *testing.T) {
	dir := analysistest.TestData()
	analysistest.Run(t, dir, RootCall, "rootcall", "github.com/smartpcr/agent-plugin/src/go/cordis")
	analysistest.Run(t, dir, ErrDiscard, "errdiscard")
	analysistest.Run(t, dir, TypeAssert, "typeassert")
	analysistest.Run(t, dir, LintIgnore, "lintignore")
}

// TestReasonlessExceptionFails is the "reasonless exception fails" scenario of plan
// stage 13.5 (DH-T-TOOLCHAIN-8): a //lint:ignore errdiscard without a reason is one
// lintignore diagnostic naming the missing reason and suppresses nothing; with a
// reason the site is silent and the total is 1.
func TestReasonlessExceptionFails(t *testing.T) {
	const reasonless = "package x\n\nfunc f() error { return nil }\n\nfunc g() {\n\t//lint:ignore errdiscard\n\t_ = f()\n}\n"
	const reasoned = "package x\n\nfunc f() error { return nil }\n\nfunc g() {\n\t//lint:ignore errdiscard the result is checked by the caller's retry\n\t_ = f()\n}\n"

	fset, file := parse(t, "x.go", reasonless)
	ds := Directives(fset, file)
	if len(ds) != 1 {
		t.Fatalf("directives = %v, want one", ds)
	}
	if p := ds[0].Problem(); !strings.Contains(p, "errdiscard has no reason") {
		t.Errorf("reasonless problem = %q, want it to name the missing reason", p)
	}
	if ds[0].suppresses("errdiscard", 7) {
		t.Error("a reasonless directive suppresses its site")
	}

	fset, file = parse(t, "x.go", reasoned)
	ds = Directives(fset, file)
	if len(ds) != 1 || !ds[0].Accepted() || ds[0].Line != 7 || !ds[0].suppresses("errdiscard", 7) {
		t.Fatalf("reasoned directive = %+v, want accepted on line 7", ds)
	}
	if ds[0].suppresses("typeassert", 7) || ds[0].suppresses("errdiscard", 6) {
		t.Error("a directive suppresses another check or another line")
	}

	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "a.go"), filepath.Join(dir, "b.go")}
	for i, src := range []string{reasonless, reasoned} {
		if err := os.WriteFile(paths[i], []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Exceptions(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got.Total != 1 || got.String() != "1 (errdiscard 1)" {
		t.Errorf("exceptions = %s, want 1 (errdiscard 1)", got)
	}
}

// TestExceptionsCountStaticcheckDirectives: one reader counts the own analyzers'
// directives and staticcheck's; a directive naming two checks is one exception
// counted under both checks; a file-ignore never counts.
func TestExceptionsCountStaticcheckDirectives(t *testing.T) {
	got, err := Exceptions([]string{filepath.Join("testdata", "src", "lintignore", "lintignore.go")})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "2 (SA4006 1, U1000 1, errdiscard 1)" {
		t.Errorf("exceptions = %s, want 2 (SA4006 1, U1000 1, errdiscard 1)", got)
	}
	if _, err := Exceptions([]string{filepath.Join(t.TempDir(), "missing.go")}); err == nil {
		t.Error("an unreadable file was counted as no exceptions")
	}
}

func TestParseDirective(t *testing.T) {
	tests := []struct {
		text   string
		ok     bool
		checks []string
		reason string
		file   bool
	}{
		{"//lint:ignore SA4006,errdiscard  the reason,  in words", true, []string{"SA4006", "errdiscard"}, "the reason, in words", false},
		{"//lint:ignore errdiscard", true, []string{"errdiscard"}, "", false},
		{"//lint:ignore", true, nil, "", false},
		{"//lint:file-ignore U1000 generated", true, []string{"U1000"}, "generated", true},
		{"//lint:ignored errdiscard reason", false, nil, "", false},
		{"// lint:ignore errdiscard reason", false, nil, "", false},
	}
	for _, tt := range tests {
		d, ok := parseDirective(tt.text)
		if ok != tt.ok || !slices.Equal(d.Checks, tt.checks) || d.Reason != tt.reason || d.File != tt.file {
			t.Errorf("parseDirective(%q) = %+v, %v", tt.text, d, ok)
		}
	}
	for check, known := range map[string]bool{
		"errdiscard": true, "TypeAssert": true, "SA4000": true, "st1003": true, "QF1001": true, "U1000": true, "S1016": true,
		"SA*": false, "SA400": false, "nosuch": false, "": false,
	} {
		if KnownCheck(check) != known {
			t.Errorf("KnownCheck(%q) = %v, want %v", check, !known, known)
		}
	}
}

// TestOwnChecksNameEveryAnalyzer keeps the directive reader's list of own checks in
// step with the analyzers: a directive may name exactly the analyzers of All and
// Pending.
func TestOwnChecksNameEveryAnalyzer(t *testing.T) {
	var names []string
	for _, a := range append(slices.Clone(All), Pending...) {
		names = append(names, a.Name)
	}
	slices.Sort(names)
	got := slices.Sorted(slices.Values(ownChecks))
	if !slices.Equal(got, names) {
		t.Errorf("ownChecks = %v, want the analyzer names %v", got, names)
	}
	for _, a := range Pending {
		if slices.Contains(All, a) {
			t.Errorf("%s is in both All and Pending", a.Name)
		}
	}
}
