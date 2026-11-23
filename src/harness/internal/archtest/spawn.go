package archtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"slices"
	"strconv"
)

// SpawnOwner is the directory, relative to the harness module root, below which
// non-test code may start a program (DH-D-EXEC-1).
const SpawnOwner = "internal/world/subprocess"

// spawnCalls are the functions that start a program without os/exec, by import
// path; an empty list means every function of the package.
var spawnCalls = map[string][]string{
	"os":                       {"StartProcess"},
	"syscall":                  {"StartProcess", "ForkExec", "Exec", "CreateProcess", "CreateProcessAsUser"},
	"golang.org/x/sys/unix":    {"Exec", "ForkExec"},
	"golang.org/x/sys/windows": {"CreateProcess", "CreateProcessAsUser", "ShellExecute", "ShellExecuteEx"},
	"os/exec":                  nil,
}

// Spawns is the source half of the onespawn rule: it parses every non-test Go file
// of fsys (the harness module tree) outside SpawnOwner and testdata directories,
// and names each call that starts a program. The import half is the os/exec
// rule in Rules.
func Spawns(fsys fs.FS) ([]string, error) {
	var found []string
	err := eachSource(fsys, []string{SpawnOwner}, func(fset *token.FileSet, _ string, f *ast.File) {
		found = append(found, spawnsIn(fset, f)...)
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(found)
	return found, nil
}

// parseFile parses one Go file of fsys.
func parseFile(fsys fs.FS, fset *token.FileSet, p string) (*ast.File, error) {
	src, err := fs.ReadFile(fsys, p)
	if err != nil {
		return nil, err
	}
	return parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
}

func spawnsIn(fset *token.FileSet, f *ast.File) []string {
	local := map[string]string{} // local name -> import path
	var found []string
	for _, imp := range f.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if _, ok := spawnCalls[p]; !ok {
			continue
		}
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		switch name {
		case "_":
		case ".":
			found = append(found, fmt.Sprintf("%s: dot import of %s hides its calls from the onespawn check", fset.Position(imp.Pos()), p))
		default:
			local[name] = p
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		id, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		p, ok := local[id.Name]
		if !ok {
			return true
		}
		if names := spawnCalls[p]; names == nil || slices.Contains(names, sel.Sel.Name) {
			found = append(found, fmt.Sprintf("%s: %s.%s starts a program outside %s", fset.Position(sel.Pos()), p, sel.Sel.Name, SpawnOwner))
		}
		return true
	})
	return found
}
