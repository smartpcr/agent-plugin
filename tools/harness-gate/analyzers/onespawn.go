package analyzers

import (
	"go/ast"
	"go/types"
	"path"
	"strconv"
	"strings"
)

// OneSpawn reports an os/exec import outside internal/world/subprocess: every
// program is started through that one package (SP-12). It also reports a
// subprocess.Spec literal that sets no Launch: every start carries a launch plan,
// which the one starter executes (DH-D-EXEC-4, C38).
var OneSpawn = newAnalyzer("onespawn",
	"forbid importing os/exec outside internal/world/subprocess (SP-12) and a subprocess.Spec literal without Launch (DH-D-EXEC-4)",
	OneSpawnFindings)

// SpawnPackage is the only package directory that may import os/exec.
const SpawnPackage = "internal/world/subprocess"

// OneSpawnFindings is the onespawn predicate. Test files are not checked: SP-12 and
// the required plan are rules of the product's spawn path, a test may launch the
// toolchain itself, and a test proves the starter refuses a Spec without Launch.
func OneSpawnFindings(filename string, file *ast.File, info *types.Info) []Finding {
	if isTestFile(filename) {
		return nil
	}
	// Both separators are accepted on every host, so the verdict for a path does
	// not depend on the platform the analyzer runs on.
	dir := "/" + strings.Trim(path.Dir(strings.ReplaceAll(filename, `\`, "/")), "/") + "/"
	inside := strings.HasSuffix(dir, "/"+SpawnPackage+"/")
	var out []Finding
	if !inside {
		for _, imp := range file.Imports {
			if imp.Path.Value == `"os/exec"` {
				out = append(out, Finding{Pos: imp.Pos(), Message: "os/exec imported outside " + SpawnPackage + ": start programs through " + SpawnPackage + " (SP-12)"})
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if ok && isSpawnSpec(file, info, lit, inside) && !setsLaunch(lit) {
			out = append(out, Finding{Pos: lit.Pos(), Message: "subprocess.Spec without Launch: every start carries a launch plan (DH-D-EXEC-4)"})
		}
		return true
	})
	return out
}

// isSpawnPath reports whether p is the import path of the spawn package.
func isSpawnPath(p string) bool {
	return p == SpawnPackage || strings.HasSuffix(p, "/"+SpawnPackage)
}

// isSpawnSpec reports whether lit is a literal of the spawn package's Spec. With
// type information the literal's type decides, so an elided element type
// ([]subprocess.Spec{{...}}) counts too; without it the literal must name
// subprocess.Spec, or Spec inside the spawn package itself.
func isSpawnSpec(file *ast.File, info *types.Info, lit *ast.CompositeLit, inside bool) bool {
	if info != nil {
		if t := info.TypeOf(lit); t != nil {
			named, ok := t.(*types.Named)
			if !ok {
				return false
			}
			obj := named.Obj()
			return obj.Name() == "Spec" && obj.Pkg() != nil && isSpawnPath(obj.Pkg().Path())
		}
	}
	switch t := lit.Type.(type) {
	case *ast.SelectorExpr:
		x, ok := t.X.(*ast.Ident)
		if !ok || t.Sel.Name != "Spec" {
			return false
		}
		for _, imp := range file.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil || !isSpawnPath(p) {
				continue
			}
			if name, ok := importName(file, p); ok && name == x.Name {
				return true
			}
		}
	case *ast.Ident:
		return inside && t.Name == "Spec"
	}
	return false
}

// setsLaunch reports whether lit gives Launch: a keyed Launch element, or the
// positional form, which lists every field.
func setsLaunch(lit *ast.CompositeLit) bool {
	if len(lit.Elts) == 0 {
		return false
	}
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Launch" {
			return true
		}
	}
	return false
}
