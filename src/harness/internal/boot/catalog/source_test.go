package catalog_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
)

// testKits are packages imported only by tests; they build trees and exit
// processes of their own, so the product rules below do not apply to them.
var testKits = []string{"internal/testkit/", "internal/tools/conform/", "internal/world/subprocess/subprocesstest/"}

// privateTrees are the product packages that create and dispose a cordis tree of
// their own: boot (the launch's root tree) and config (the throwaway preflight
// tree of a configuration check, never the launch's).
var privateTrees = []string{"internal/boot", "internal/boot/config"}

// sourceFile is one non-test Go file of the module: its package directory
// relative to the module root, its path and its syntax.
type sourceFile struct {
	dir, path string
	file      *ast.File
}

// moduleSources parses every non-test Go file of the harness module outside
// testdata and the test kits.
func moduleSources(t *testing.T) []sourceFile {
	t.Helper()
	root := filepath.Join("..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root: %v", err)
	}
	var out []sourceFile
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := filepath.ToSlash(strings.TrimPrefix(p, root+string(filepath.Separator)))
		if d.IsDir() {
			if d.Name() == "testdata" || (strings.HasPrefix(d.Name(), ".") && p != root) ||
				slices.ContainsFunc(testKits, func(k string) bool { return strings.HasPrefix(rel+"/", k) }) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		out = append(out, sourceFile{dir: path.Dir(rel), path: rel, file: f})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// sourceViolations applies the DH-T-BOOT-22 source rules to files.
func sourceViolations(files []sourceFile) []string {
	var out []string
	for _, sf := range files {
		bad := func(n ast.Node, what string) { out = append(out, sf.path+": "+what) }
		ast.Inspect(sf.file, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.SelectorExpr:
				id, _ := x.X.(*ast.Ident)
				switch {
				case id != nil && id.Name == "loader" && x.Sel.Name == "Register":
					bad(x, "uses loader.Register; components resolve through the catalogue")
				case id != nil && id.Name == "os" && x.Sel.Name == "Exit" && sf.dir != "cmd/dsh":
					bad(x, "uses os.Exit outside cmd/dsh")
				case id != nil && id.Name == "loader" && (x.Sel.Name == "WriteGuard" || x.Sel.Name == "LayerCodec") && sf.dir != "internal/boot/config":
					bad(x, "uses loader."+x.Sel.Name+" (the layer write-back) outside package config")
				case id != nil && id.Name == "config" && x.Sel.Name == "LockGuard" && sf.dir != "internal/boot/config":
					bad(x, "uses the write guard config.LockGuard outside package config")
				case x.Sel.Name == "EncodeLayer" && sf.dir != "internal/boot/config":
					bad(x, "calls EncodeLayer (the layer write-back) outside package config")
				case id != nil && id.Name == "cordis" && x.Sel.Name == "New" && !slices.Contains(privateTrees, sf.dir):
					bad(x, "creates a cordis tree outside boot and config")
				}
			case *ast.CallExpr:
				// X.Root().Dispose(): disposing the tree a context belongs to.
				if sel, ok := x.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Dispose" && sf.dir != "internal/boot" {
					if inner, ok := sel.X.(*ast.CallExpr); ok {
						if r, ok := inner.Fun.(*ast.SelectorExpr); ok && r.Sel.Name == "Root" {
							bad(x, "disposes the root tree outside package boot")
						}
					}
				}
			}
			return true
		})
	}
	return out
}

// TestSourceRules is DH-T-BOOT-22 over the module's source: no call to
// loader.Register; os.Exit only in cmd/dsh; the root tree created and disposed
// only in package boot (config's own preflight tree aside); the loader's
// write-back and the write guard only in package config; and every id in a
// built-in profile's Required list exists in that profile's shipped layers.
//
//dsh:test DH-T-BOOT-22
func TestSourceRules(t *testing.T) {
	files := moduleSources(t)
	if p := sourceViolations(files); len(p) > 0 {
		t.Fatalf("source rules broken:\n%s", strings.Join(p, "\n"))
	}
	// The rules see the code they guard: boot creates the root, cmd/dsh exits,
	// config writes layers back.
	seen := map[string]bool{}
	for _, sf := range files {
		ast.Inspect(sf.file, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok {
					seen[sf.dir+" "+id.Name+"."+sel.Sel.Name] = true
				}
				if sel.Sel.Name == "EncodeLayer" {
					seen[sf.dir+" EncodeLayer"] = true
				}
			}
			return true
		})
	}
	for _, want := range []string{"internal/boot cordis.New", "cmd/dsh os.Exit", "internal/boot/config EncodeLayer", "internal/boot/config loader.WriteGuard"} {
		if !seen[want] {
			t.Errorf("the scan does not see %q: it no longer reads the guarded code", want)
		}
	}

	// A required id the shipped layers lack is a fault once its component is in the
	// build catalogue; until then the profile does not launch (boot's audit names it)
	// and the profile package's golden test keeps the list of such ids.
	cat, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range append(profile.Definitions(), profile.Builtins()...) {
		var layers []loader.Layer
		for _, name := range def.Bundles {
			layer, err := profile.Bundle(name)
			if err != nil {
				t.Fatalf("%s: %v", def.Name, err)
			}
			layers = append(layers, layer)
		}
		tree, _, _ := loader.Compose(layers)
		missing := slices.DeleteFunc(missingRequired(def.Required, tree), func(id string) bool {
			_, err := cat.Lookup(id)
			return err != nil
		})
		if len(missing) > 0 {
			t.Errorf("profile %s requires %v, which its shipped layers do not hold", def.Name, missing)
		}
	}
}

// missingRequired returns the required ids no entry of tree has, at any depth.
func missingRequired(required []string, tree []loader.EntryOptions) []string {
	ids := map[string]bool{}
	var walk func([]loader.EntryOptions)
	walk = func(es []loader.EntryOptions) {
		for _, e := range es {
			ids[e.ID] = true
			walk(e.Children)
		}
	}
	walk(tree)
	var out []string
	for _, id := range required {
		if !ids[id] {
			out = append(out, id)
		}
	}
	return out
}

// Each rule fires on the fault it exists for, so its silence over the module is
// meaningful.
//
//dsh:test DH-T-BOOT-22
func TestSourceRulesCatchFaults(t *testing.T) {
	src := func(dir, body string) sourceFile {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", "package x\nfunc f() {\n"+body+"\n}\n", 0)
		if err != nil {
			t.Fatal(err)
		}
		return sourceFile{dir: dir, path: dir + "/x.go", file: f}
	}
	for want, sf := range map[string]sourceFile{
		"loader.Register":              src("internal/x", "loader.Register(\"a\", nil)"),
		"os.Exit outside cmd/dsh":      src("internal/cli", "os.Exit(1)"),
		"disposes the root tree":       src("internal/agent", "ctx.Root().Dispose()"),
		"creates a cordis tree":        src("internal/agent", "_ = cordis.New()"),
		"loader.WriteGuard":            src("internal/storage/settings", "var g loader.WriteGuard; _ = g"),
		"loader.LayerCodec":            src("internal/storage/settings", "_ = c.(loader.LayerCodec)"),
		"config.LockGuard":             src("internal/cli", "_ = config.LockGuard(nil, 0)"),
		"EncodeLayer (the layer write": src("internal/cli", "c.EncodeLayer(nil, nil)"),
	} {
		if p := strings.Join(sourceViolations([]sourceFile{sf}), "; "); !strings.Contains(p, want) {
			t.Errorf("%s: violations %q, want one naming %q", sf.path, p, want)
		}
	}
	allowed := []sourceFile{
		src("cmd/dsh", "os.Exit(0)"),
		src("internal/boot", "root := cordis.New(); root.Dispose(); ctx.Root().Dispose()"),
		src("internal/boot/config", "_ = cordis.New(); var g loader.WriteGuard; _ = g; c.EncodeLayer(nil, nil)"),
	}
	if p := sourceViolations(allowed); len(p) > 0 {
		t.Errorf("allowed uses reported: %q", p)
	}
	tree := []loader.EntryOptions{{ID: "g", Group: true, Children: []loader.EntryOptions{{ID: "a"}}}}
	if m := missingRequired([]string{"a", "g", "b"}, tree); !slices.Equal(m, []string{"b"}) {
		t.Errorf("missingRequired = %v, want [b]", m)
	}
}
