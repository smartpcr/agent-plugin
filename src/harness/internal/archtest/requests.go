package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"slices"
	"strings"
)

// RequestTypes is the TC-05 predicate for internal/llm/vocab (RM-6, C07): the
// vocabulary holds no request type, because session.Request is the only one. It
// parses every non-test Go file at the root of fsys and names each type called
// Request and each struct that holds a []Message field together with a
// Controls field, which is a request under another name. The vocab package
// runs it over its own sources, embedded with embed.FS.
func RequestTypes(fsys fs.FS) ([]string, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	var found []string
	parsed := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		f, err := parser.ParseFile(fset, name, src, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		parsed++
		ast.Inspect(f, func(n ast.Node) bool {
			spec, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			pos := fset.Position(spec.Pos())
			if spec.Name.Name == "Request" {
				found = append(found, fmt.Sprintf("%s: type Request", pos))
			}
			if st, ok := spec.Type.(*ast.StructType); ok && holdsRequest(st) {
				found = append(found, fmt.Sprintf("%s: struct %s holds []Message and Controls", pos, spec.Name.Name))
			}
			return true
		})
	}
	if parsed == 0 {
		return nil, fmt.Errorf("archtest: no Go source to check")
	}
	slices.Sort(found)
	return found, nil
}

func holdsRequest(st *ast.StructType) bool {
	var messages, controls bool
	for _, f := range st.Fields.List {
		t := f.Type
		if star, ok := t.(*ast.StarExpr); ok {
			t = star.X
		}
		if arr, ok := t.(*ast.ArrayType); ok && arr.Len == nil && typeName(arr.Elt) == "Message" {
			messages = true
		}
		if typeName(t) == "Controls" {
			controls = true
		}
	}
	return messages && controls
}

// typeName is the name of a plain or package-qualified type, or "".
func typeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		return t.Sel.Name
	case *ast.StarExpr:
		return typeName(t.X)
	}
	return ""
}
