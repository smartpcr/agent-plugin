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

// RevealCallers are the directories, relative to the harness module root, whose
// non-test code may call Reveal() (DH-D-HUMAN-7, interaction-credentials.md §6.2
// "Printed"): the secret store itself, and each provider that puts a key into a
// request at its point of use. A new caller is added here, in review.
var RevealCallers = []string{
	"internal/human/secret",
	"internal/llm/provider/deepseek",
}

// EnvWriter is the directory, relative to the harness module root, below which
// non-test code may write the process environment: the starter (DH-D-EXEC-1). The
// harness never writes a stored value or a .env entry into its own environment
// (DH-D-HUMAN-7, "Inherited by a child process").
const EnvWriter = SpawnOwner

// envWrites are the functions that write the process environment, by import path.
var envWrites = map[string][]string{
	"os":      {"Setenv"},
	"syscall": {"Setenv"},
}

// Reveals is the source check on Reveal() callers: it parses every non-test Go
// file of fsys (the harness module tree) outside RevealCallers and testdata
// directories, and names each call of a method called Reveal with no arguments.
// The check is by name, not by type, so it also catches a wrapper's Reveal; it
// cannot see a call made by reflection or from a template.
func Reveals(fsys fs.FS) ([]string, error) {
	var found []string
	err := eachSource(fsys, RevealCallers, func(fset *token.FileSet, _ string, f *ast.File) {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 0 {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Reveal" {
				found = append(found, fmt.Sprintf("%s: Reveal() is called outside %s", fset.Position(sel.Sel.Pos()), strings.Join(RevealCallers, ", ")))
			}
			return true
		})
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(found)
	return found, nil
}

// EnvWrites is the source check on os.Setenv: it parses every non-test Go file of
// fsys outside EnvWriter and testdata directories, and names each reference to
// os.Setenv or syscall.Setenv, called or taken as a value, and each dot import of
// those packages, which would hide one.
func EnvWrites(fsys fs.FS) ([]string, error) {
	var found []string
	err := eachSource(fsys, []string{EnvWriter}, func(fset *token.FileSet, _ string, f *ast.File) {
		local := map[string]string{} // local name -> import path
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			if _, ok := envWrites[p]; !ok {
				continue
			}
			name := path.Base(p)
			if imp.Name != nil {
				name = imp.Name.Name
			}
			switch name {
			case "_":
			case ".":
				found = append(found, fmt.Sprintf("%s: dot import of %s hides its Setenv from the environment check", fset.Position(imp.Pos()), p))
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
			if p, ok := local[id.Name]; ok && slices.Contains(envWrites[p], sel.Sel.Name) {
				found = append(found, fmt.Sprintf("%s: %s.%s writes the process environment outside %s", fset.Position(sel.Pos()), p, sel.Sel.Name, EnvWriter))
			}
			return true
		})
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(found)
	return found, nil
}
