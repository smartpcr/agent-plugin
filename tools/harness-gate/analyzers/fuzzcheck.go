package analyzers

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/tools/go/analysis"
)

// UntrustedParserMarker is the line comment that declares a package a parser of
// untrusted bytes (QG-6): `//dsh:untrusted-parser`, optionally followed by a space
// and a note.
const UntrustedParserMarker = "//dsh:untrusted-parser"

// SeedsFile is the test file that registers a marked package's fuzz seeds.
const SeedsFile = "seeds_test.go"

// FuzzCheck enforces the fuzz-target convention of DH-ADR-TOOLCHAIN-3 on every
// package marked //dsh:untrusted-parser; see FuzzCheckFindings.
var FuzzCheck = &analysis.Analyzer{
	Name: "fuzzcheck",
	Doc:  "require a seeded Fuzz* target in every package marked " + UntrustedParserMarker,
	Run:  runFuzzCheck,
}

// PackageFile is one parsed file of a package and its file name.
type PackageFile struct {
	Name string
	File *ast.File
}

// FuzzCheckFindings is the fuzzcheck predicate over the files of one package,
// including its in-package _test.go files (go vet analyzes a package together with
// them). A package with the marker in a non-test file must have:
//
//   - a fuzz target, func FuzzXxx(f *testing.F), in an in-package _test.go file;
//   - a seeds_test.go file that does not import os or io/ioutil: its seeds are a Go
//     slice or //go:embed bytes compiled into the test, so a plain go test runs
//     them with no file read at run time;
//   - every f.Add argument in seeds_test.go compiled in (see the provenance rules in
//     fuzzseeds.go): f.Add(readSeed()) with readSeed in another file is reported;
//   - no f.Add on a *testing.F parameter outside seeds_test.go;
//   - in every fuzz target, seeds registered from seeds_test.go: either the target
//     is declared there and calls f.Add, or it passes f, at that position, to a
//     seeds_test.go function that calls Add on the *testing.F parameter there (or
//     passes it on to another such function). A target or helper that reassigns its
//     *testing.F parameter seeds nothing.
//
// An on-disk corpus under testdata/fuzz/<FuzzName>/ is optional; go test loads it
// from the package directory by itself, so nothing here requires or reads it.
// A missing target is reported once, at the first marker; a missing seeds file once,
// at the first target.
func FuzzCheckFindings(files []PackageFile) []Finding {
	marker := token.NoPos
	var targets []fuzzTarget
	var seeds *PackageFile
	for i, pf := range files {
		test := isTestFile(pf.Name)
		if !test && marker == token.NoPos {
			marker = findMarker(pf.File)
		}
		if !test {
			continue
		}
		if path.Base(strings.ReplaceAll(pf.Name, `\`, "/")) == SeedsFile {
			seeds = &files[i]
		}
		for _, decl := range pf.File.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok {
				if param, ok := fuzzParam(pf.File, fn); ok && isFuzzName(fn.Name.Name) {
					targets = append(targets, fuzzTarget{file: pf, fn: fn, param: param})
				}
			}
		}
	}
	if marker == token.NoPos {
		return nil
	}
	if len(targets) == 0 {
		return []Finding{{Pos: marker, Message: "package is marked " + UntrustedParserMarker +
			" but has no fuzz target: add func FuzzXxx(f *testing.F) with its seeds in " + SeedsFile + " (QG-6)"}}
	}
	if seeds == nil {
		return []Finding{{Pos: targets[0].fn.Name.Pos(), Message: targets[0].fn.Name.Name + ": package is marked " + UntrustedParserMarker +
			" but has no " + SeedsFile + ": register the fuzz seeds there with f.Add from a Go slice or //go:embed bytes (QG-6)"}}
	}

	var out []Finding
	for _, imp := range seeds.File.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err == nil && (p == "os" || p == "io/ioutil") {
			out = append(out, Finding{Pos: imp.Pos(), Message: SeedsFile + " imports " + p +
				": seeds are compiled in (a Go slice or //go:embed bytes), never read from disk at run time (QG-6)"})
		}
	}
	prov := newProvenance(seeds, files)
	var seedFuncs []seedFunc // seeds_test.go functions with a *testing.F parameter
	for _, decl := range seeds.File.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		sf := seedFunc{fn: fn, env: prov.collectEnv(seeds.File, fn), params: testingFParams(seeds.File, fn)}
		for _, p := range sf.params {
			for _, call := range addCalls(fn.Body, p.name, sf.env) {
				for _, arg := range call.Args {
					if bad, why := prov.check(arg, sf.env); bad != nil {
						out = append(out, Finding{Pos: arg.Pos(), Message: p.name + ".Add argument " + types.ExprString(arg) +
							" is not compiled in (" + why + "): seeds are literals, a Go slice or //go:embed bytes declared in " + SeedsFile + " (QG-6)"})
					}
				}
			}
		}
		if fn.Recv == nil && len(sf.params) > 0 {
			seedFuncs = append(seedFuncs, sf)
		}
	}
	seeders := seederParams(seedFuncs)

	for _, pf := range files {
		if !isTestFile(pf.Name) || pf.Name == seeds.Name {
			continue
		}
		for _, decl := range pf.File.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil {
				env := prov.collectEnv(pf.File, fn)
				for _, p := range testingFParams(pf.File, fn) {
					for _, call := range addCalls(fn.Body, p.name, env) {
						out = append(out, Finding{Pos: call.Pos(), Message: p.name + ".Add outside " + SeedsFile +
							": register every fuzz seed in " + SeedsFile + " (QG-6)"})
					}
				}
			}
		}
	}

	for _, tg := range targets {
		env := prov.collectEnv(tg.file.File, tg.fn)
		if tg.fn.Body != nil && !reassigned(env, tg.param) {
			inSeeds := tg.file.Name == seeds.Name
			if (inSeeds && len(addCalls(tg.fn.Body, tg.param, env)) > 0) || passesToSeeder(tg.fn.Body, tg.param, seeders, env) {
				continue
			}
		}
		out = append(out, Finding{Pos: tg.fn.Name.Pos(), Message: tg.fn.Name.Name + " registers no seeds from " + SeedsFile +
			": call " + tg.param + ".Add there, in the target itself or in a " + SeedsFile + " function that receives " + tg.param +
			" as the *testing.F it seeds (QG-6)"})
	}
	return out
}

// seedFunc is a seeds_test.go function together with its *testing.F parameters.
type seedFunc struct {
	fn     *ast.FuncDecl
	env    *seedEnv
	params []fParam
}

// seederParams returns, per seeds_test.go function, the positions of the *testing.F
// parameters it seeds: it calls Add on that parameter, or passes it on at a seeded
// position of another seeder. A parameter the function reassigns seeds nothing.
func seederParams(funcs []seedFunc) map[string]map[int]bool {
	seeders := map[string]map[int]bool{}
	for changed := true; changed; {
		changed = false
		for _, sf := range funcs {
			for _, p := range sf.params {
				name := sf.fn.Name.Name
				if seeders[name][p.index] || reassigned(sf.env, p.name) {
					continue
				}
				if len(addCalls(sf.fn.Body, p.name, sf.env)) > 0 || passesToSeeder(sf.fn.Body, p.name, seeders, sf.env) {
					if seeders[name] == nil {
						seeders[name] = map[int]bool{}
					}
					seeders[name][p.index] = true
					changed = true
				}
			}
		}
	}
	return seeders
}

type fuzzTarget struct {
	file  PackageFile
	fn    *ast.FuncDecl
	param string
}

// findMarker returns the position of the first untrusted-parser marker in file.
func findMarker(file *ast.File) token.Pos {
	for _, cg := range file.Comments {
		for _, c := range cg.List {
			rest, ok := strings.CutPrefix(c.Text, UntrustedParserMarker)
			if ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
				return c.Pos()
			}
		}
	}
	return token.NoPos
}

// isFuzzName applies go test's rule: "Fuzz", or "Fuzz" followed by a non-lowercase
// rune.
func isFuzzName(name string) bool {
	rest, ok := strings.CutPrefix(name, "Fuzz")
	if !ok {
		return false
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return rest == "" || !unicode.IsLower(r)
}

// fuzzParam returns the parameter name of a fuzz target's signature, func(f *testing.F)
// with no result.
func fuzzParam(file *ast.File, fn *ast.FuncDecl) (string, bool) {
	if fn.Recv != nil || fn.Type.TypeParams != nil || fn.Type.Results != nil {
		return "", false
	}
	params := fn.Type.Params.List
	if len(params) != 1 || len(params[0].Names) > 1 || !isTestingF(file, params[0].Type) {
		return "", false
	}
	if len(params[0].Names) == 0 {
		return "_", true
	}
	return params[0].Names[0].Name, true
}

// fParam is a *testing.F parameter: its name ("_" when unnamed) and its position in
// the parameter list.
type fParam struct {
	name  string
	index int
}

// testingFParams returns fn's named *testing.F parameters.
func testingFParams(file *ast.File, fn *ast.FuncDecl) []fParam {
	var out []fParam
	index := 0
	for _, field := range fn.Type.Params.List {
		f := isTestingF(file, field.Type)
		if len(field.Names) == 0 {
			index++
			continue
		}
		for _, n := range field.Names {
			if f && n.Name != "_" {
				out = append(out, fParam{name: n.Name, index: index})
			}
			index++
		}
	}
	return out
}

// isTestingF reports whether expr is the type *testing.F as file spells it.
func isTestingF(file *ast.File, expr ast.Expr) bool {
	star, ok := expr.(*ast.StarExpr)
	if !ok {
		return false
	}
	name, ok := importName(file, "testing")
	if !ok {
		return false
	}
	switch x := star.X.(type) {
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		return ok && id.Name == name && x.Sel.Name == "F"
	case *ast.Ident:
		return name == "." && x.Name == "F"
	}
	return false
}

// addCalls returns body's calls of param.Add, param being the parameter env knows
// by that name rather than a variable of the same name in a nested scope.
func addCalls(body *ast.BlockStmt, param string, env *seedEnv) []*ast.CallExpr {
	if param == "_" {
		return nil
	}
	var calls []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok && sel.Sel.Name == "Add" {
				if id, ok := ast.Unparen(sel.X).(*ast.Ident); ok && env.ident(id) == param {
					calls = append(calls, call)
				}
			}
		}
		return true
	})
	return calls
}

// passesToSeeder reports whether body calls a seeder with param at a position that
// seeder seeds. A call through a name the function itself declares (a local
// addSeeds := func(*testing.F) {} shadowing the seeds_test.go helper) is not a call
// of the seeder.
func passesToSeeder(body *ast.BlockStmt, param string, seeders map[string]map[int]bool, env *seedEnv) bool {
	if param == "_" {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if fn, ok := ast.Unparen(call.Fun).(*ast.Ident); ok && !env.declared[env.ident(fn)] {
				for i, arg := range call.Args {
					if id, ok := ast.Unparen(arg).(*ast.Ident); ok && env.ident(id) == param && seeders[env.ident(fn)][i] {
						found = true
					}
				}
			}
		}
		return !found
	})
	return found
}

func runFuzzCheck(pass *analysis.Pass) (any, error) {
	files := make([]PackageFile, 0, len(pass.Files))
	hasTests := false
	for _, f := range pass.Files {
		name := pass.Fset.File(f.FileStart).Name()
		files = append(files, PackageFile{Name: name, File: f})
		hasTests = hasTests || isTestFile(name)
	}
	// go vet analyzes a package with tests only together with them. A loader that also
	// yields the package alone (go/packages with Tests, as analysistest uses) would make
	// that variant look untested; its test variant is judged instead.
	if !hasTests && len(files) > 0 && hasInPackageTests(filepath.Dir(files[0].Name), pass.Pkg.Name()) {
		return nil, nil
	}
	for _, f := range FuzzCheckFindings(files) {
		pass.Report(analysis.Diagnostic{Pos: f.Pos, Message: f.Message})
	}
	return nil, nil
}

// hasInPackageTests reports whether dir holds a _test.go file of package pkg that the
// default build context selects.
func hasInPackageTests(dir, pkg string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !isTestFile(name) {
			continue
		}
		if ok, err := build.Default.MatchFile(dir, name); err != nil || !ok {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.PackageClauseOnly)
		if err == nil && f.Name.Name == pkg {
			return true
		}
	}
	return false
}
