package analyzers

import (
	"go/ast"
	"go/types"
	"path/filepath"
	"strings"
)

// OneSpawn reports an os/exec import outside internal/world/subprocess: every
// program is started through that one package (SP-12).
var OneSpawn = newAnalyzer("onespawn",
	"forbid importing os/exec outside internal/world/subprocess (SP-12)",
	OneSpawnFindings)

// SpawnPackage is the only package directory that may import os/exec.
const SpawnPackage = "internal/world/subprocess"

// OneSpawnFindings is the onespawn predicate. Test files are not checked: SP-12 is
// a rule of the product's spawn path, and a test may launch the toolchain itself.
func OneSpawnFindings(filename string, file *ast.File, _ *types.Info) []Finding {
	if isTestFile(filename) {
		return nil
	}
	dir := "/" + strings.Trim(filepath.ToSlash(filepath.Dir(filename)), "/") + "/"
	if strings.HasSuffix(dir, "/"+SpawnPackage+"/") {
		return nil
	}
	var out []Finding
	for _, imp := range file.Imports {
		if imp.Path.Value == `"os/exec"` {
			out = append(out, Finding{Pos: imp.Pos(), Message: "os/exec imported outside " + SpawnPackage + ": start programs through " + SpawnPackage + " (SP-12)"})
		}
	}
	return out
}
