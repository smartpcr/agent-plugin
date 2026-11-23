package archtest

import (
	"fmt"
	"go/ast"
	"go/token"
	"io/fs"
	"path"
	"slices"
	"strconv"
	"strings"
)

// EgressOwner is the directory, relative to the harness module root, below which
// non-test code may build an HTTP client (DH-D-UTIL-2): every outbound connection
// is made through a client that util/egress hands to a permit holder.
const EgressOwner = "internal/util/egress"

// TestkitDir is the test kit's directory, relative to the harness module root. Only
// tests import it (DH-T-UTIL-11), so its non-test code may build the fixture
// clients and call the test seams that tests use.
const TestkitDir = "internal/testkit"

// httpPath is the import path of the standard HTTP package.
const httpPath = "net/http"

// httpClientTypes are the net/http types whose value is a usable client of its
// own: a literal, new(T), or a variable, field or element of the type builds one.
// A pointer to one only refers to a client somebody else built.
var httpClientTypes = []string{"Client", "Transport"}

// httpDefaults are the net/http names that send through the process-wide default
// client or transport.
var httpDefaults = []string{"DefaultClient", "DefaultTransport", "Get", "Head", "Post", "PostForm"}

// HTTPClients is the source check of DH-T-UNDICI-8 and the source half of
// DH-T-HOST-19: it parses every non-test Go file of fsys (the harness module tree)
// outside EgressOwner, TestkitDir and testdata directories, and names each place
// that builds an HTTP client or sends through the default one -- a net/http.Client
// or net/http.Transport literal, new(T) or value; net/http.DefaultClient or
// DefaultTransport; or a package-level helper such as net/http.Get or Post. A
// client built anywhere else escapes the sharing gate, which only egress enforces.
func HTTPClients(fsys fs.FS) ([]string, error) {
	var found []string
	err := eachSource(fsys, []string{EgressOwner, TestkitDir}, func(fset *token.FileSet, _ string, f *ast.File) {
		found = append(found, httpClientsIn(fset, f)...)
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(found)
	return found, nil
}

func httpClientsIn(fset *token.FileSet, f *ast.File) []string {
	var found []string
	local := ""
	for _, imp := range f.Imports {
		if p, err := strconv.Unquote(imp.Path.Value); err != nil || p != httpPath {
			continue
		}
		name := path.Base(httpPath)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch name {
		case "_":
		case ".":
			found = append(found, fmt.Sprintf("%s: dot import of %s hides its clients from the egress check", fset.Position(imp.Pos()), httpPath))
		default:
			local = name
		}
	}
	if local == "" {
		return found
	}
	isHTTP := func(n ast.Node) (*ast.SelectorExpr, bool) {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return nil, false
		}
		id, ok := sel.X.(*ast.Ident)
		return sel, ok && id.Name == local
	}
	// A client type under a pointer is a reference, not a client of its own.
	pointed := map[*ast.SelectorExpr]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		if star, ok := n.(*ast.StarExpr); ok {
			if sel, ok := isHTTP(star.X); ok {
				pointed[sel] = true
			}
		}
		return true
	})
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := isHTTP(n)
		if !ok {
			return true
		}
		name := sel.Sel.Name
		switch {
		case slices.Contains(httpClientTypes, name) && !pointed[sel]:
			found = append(found, fmt.Sprintf("%s: a %s.%s value builds an HTTP client outside %s", fset.Position(sel.Pos()), httpPath, name, EgressOwner))
		case slices.Contains(httpDefaults, name):
			found = append(found, fmt.Sprintf("%s: %s.%s sends through the default client outside %s", fset.Position(sel.Pos()), httpPath, name, EgressOwner))
		}
		return true
	})
	return found
}

// A seam is a function that hands out a client past the egress permit check: the
// seam for the test kit's fake transport and recorded fixtures. Only tests, the
// test kit and the directories in also may use it.
type seam struct {
	pkg, name string
	also      []string
}

// seams are the test seams of the harness. llm/transport.Over is a model transport
// over any round tripper; egress.Over is the bare client under it.
var seams = []seam{
	{LLMTransportPackage, "Over", nil},
	{EgressPackage, "Over", []string{"internal/llm/transport"}},
}

// SeamCalls is the seam rule of DH-T-HOST-19 (M1-G-BOOT-8): it parses every
// non-test Go file of fsys (the harness module tree) outside TestkitDir and
// testdata directories, and names each use of a test seam -- llm/transport.Over,
// or egress.Over outside internal/llm/transport -- including a call inside the
// seam's own package. Only _test.go files and internal/testkit may use them, so no
// production path reaches the network without a permit.
func SeamCalls(fsys fs.FS) ([]string, error) {
	var found []string
	err := eachSource(fsys, []string{TestkitDir}, func(fset *token.FileSet, file string, f *ast.File) {
		found = append(found, seamsIn(fset, path.Dir(file), f)...)
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(found)
	return found, nil
}

func seamsIn(fset *token.FileSet, dir string, f *ast.File) []string {
	allowed := func(s seam) bool {
		return slices.ContainsFunc(s.also, func(d string) bool { return within(dir, d) })
	}
	local := map[string]string{} // local name -> import path
	var found []string
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil || !slices.ContainsFunc(seams, func(s seam) bool { return s.pkg == p }) {
			continue
		}
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch name {
		case "_":
		case ".":
			found = append(found, fmt.Sprintf("%s: dot import of %s hides its test seam from the seam check", fset.Position(imp.Pos()), p))
		default:
			local[name] = p
		}
	}
	// own is the seam whose package this file belongs to, if any.
	own := slices.IndexFunc(seams, func(s seam) bool { return strings.TrimPrefix(s.pkg, HarnessModule+"/") == dir })
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			id, ok := n.X.(*ast.Ident)
			if !ok {
				return true
			}
			p, ok := local[id.Name]
			if !ok {
				return true
			}
			for _, s := range seams {
				if s.pkg == p && s.name == n.Sel.Name && !allowed(s) {
					found = append(found, fmt.Sprintf("%s: %s.%s is a test seam: only tests and %s may use it", fset.Position(n.Pos()), p, s.name, TestkitDir))
				}
			}
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && own >= 0 && id.Name == seams[own].name && !allowed(seams[own]) {
				found = append(found, fmt.Sprintf("%s: %s.%s is a test seam: only tests and %s may use it", fset.Position(n.Pos()), seams[own].pkg, id.Name, TestkitDir))
			}
		}
		return true
	})
	return found
}

// eachSource parses every non-test Go file of fsys outside the exempt directories,
// testdata directories and hidden directories, and hands each to visit with its
// slash-separated path. An empty tree is an error, so a wrong root cannot pass.
func eachSource(fsys fs.FS, exempt []string, visit func(fset *token.FileSet, file string, f *ast.File)) error {
	fset := token.NewFileSet()
	parsed := 0
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if slices.Contains(exempt, p) || d.Name() == "testdata" || (p != "." && strings.HasPrefix(d.Name(), ".")) {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parseFile(fsys, fset, p)
		if err != nil {
			return err
		}
		parsed++
		visit(fset, p, f)
		return nil
	})
	if err != nil {
		return err
	}
	if parsed == 0 {
		return fmt.Errorf("archtest: no Go source to check")
	}
	return nil
}
