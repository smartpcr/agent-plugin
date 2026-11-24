package allevents_test

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	// Every package that declares a durable event type on event.Default. The
	// source scan of TestEveryDeclarationIsInventoried fails when one is missing.
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/human/question"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/retry"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/tools"

	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	_ "github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
)

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// notes are the core notes of session/store and the notes of every other type.
func notes(t *testing.T) []store.InventoryNote {
	t.Helper()
	var out []store.InventoryNote
	for _, path := range []string{filepath.Join("..", "testdata", "persisted.notes"), filepath.Join("testdata", "persisted.notes")} {
		n, err := store.ParseNotes(read(t, path))
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, n...)
	}
	return out
}

// TestInventory is DH-T-STORE-10 over the whole program: the inventory of every
// durable event type the harness declares matches the committed golden, and every
// type, version and fingerprint in it is accounted for in persisted.notes (the
// core types' notes stay in session/store/testdata).
//
//dsh:test DH-T-STORE-10, DH-T-ZOD-9
func TestInventory(t *testing.T) {
	cur, err := store.Inventory(event.Default())
	if err != nil {
		t.Fatal(err)
	}
	was, err := store.ParseInventory(read(t, filepath.Join("testdata", "persisted.golden")))
	if err != nil {
		t.Fatal(err)
	}
	all := notes(t)
	if problems := store.CheckInventory(cur, was, all); len(problems) > 0 {
		t.Fatalf("persisted types changed without a version bump and note:\n%s", strings.Join(problems, "\n"))
	}
	if problems := store.CheckInventory(cur, nil, all); len(problems) > 0 {
		t.Fatalf("persisted types without a note:\n%s", strings.Join(problems, "\n"))
	}
	golden.Assert(t, "persisted", store.RenderInventory(cur))
}

// TestEveryDeclarationIsInventoried scans the module's sources for every event
// type declared on event.Default outside session/event and requires each in the
// registry this test inventories, so a declaring package this test does not link
// fails here instead of escaping DH-T-STORE-10.
//
//dsh:test DH-T-STORE-10
func TestEveryDeclarationIsInventoried(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root: %v", err)
	}
	declared := map[string]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "testdata" || (strings.HasPrefix(name, ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		slash := filepath.ToSlash(path)
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(slash, "internal/session/event/") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		names, err := declarationsIn(f)
		if err != nil {
			return fmt.Errorf("%s: %w", slash, err)
		}
		for _, name := range names {
			declared[name] = slash
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(declared) == 0 {
		t.Fatal("the scan found no declaration; it no longer reads the sources")
	}
	known := map[string]bool{}
	for _, info := range event.Default().Types() {
		known[info.Name] = true
	}
	for name, file := range declared {
		if !known[name] {
			t.Errorf("%s (declared in %s) is not in the inventoried registry: import its package in this test", name, file)
		}
	}
	f, err := parser.ParseFile(token.NewFileSet(), "src.go", "package p\nvar _ = event.MustDefine(event.Default(), event.Spec[int]{Name: x})", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := declarationsIn(f); err == nil {
		t.Error("a declaration whose name is not a literal was accepted: the scan could not see it")
	}
}

// declarationsIn returns the Name of every event.MustDefine or event.Define call
// on event.Default() in f; a Name that is not a string literal is an error, since
// the scan cannot read it.
func declarationsIn(f *ast.File) ([]string, error) {
	var names []string
	var bad error
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) < 2 || !isSelector(call.Fun, "event", "MustDefine", "Define") {
			return true
		}
		reg, ok := call.Args[0].(*ast.CallExpr)
		if !ok || !isSelector(reg.Fun, "event", "Default") {
			return true
		}
		lit, ok := call.Args[1].(*ast.CompositeLit)
		if !ok {
			return true
		}
		for _, e := range lit.Elts {
			kv, ok := e.(*ast.KeyValueExpr)
			if key, _ := kv.Key.(*ast.Ident); !ok || key == nil || key.Name != "Name" {
				continue
			}
			s, isLit := kv.Value.(*ast.BasicLit)
			if !isLit || s.Kind != token.STRING {
				bad = errors.New("an event type's Name is not a string literal")
				continue
			}
			if name, err := strconv.Unquote(s.Value); err == nil {
				names = append(names, name)
			}
		}
		return true
	})
	return names, bad
}

func isSelector(e ast.Expr, pkg string, names ...string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && slices.Contains(names, sel.Sel.Name)
}

// TestStoredRowsDecode keeps a stored row of every version of every non-core type
// that has been bumped, and requires each to decode with the current registry: a
// row of the old integer duration form converts to the text form (plan 14.4.1).
//
//dsh:test DH-T-STORE-10
func TestStoredRowsDecode(t *testing.T) {
	reg := event.Default()
	type tv struct {
		typ string
		v   int
	}
	have := map[tv]event.Record{}
	sc := bufio.NewScanner(strings.NewReader(read(t, filepath.Join("testdata", "rows.jsonl"))))
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var rec event.Record
		if err := json.Unmarshal(sc.Bytes(), &rec); err != nil {
			t.Fatalf("row: %v", err)
		}
		if _, err := reg.DecodeAny(rec); err != nil {
			t.Errorf("%s v%d at seq %d does not decode: %v", rec.Type, rec.Version, rec.Seq, err)
		}
		have[tv{rec.Type, rec.Version}] = rec
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	core := map[string]bool{}
	inv, err := store.ParseInventory(read(t, filepath.Join("..", "testdata", "persisted.golden")))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range inv {
		core[e.Type] = true
	}
	for _, info := range reg.Types() {
		if core[info.Name] || info.Version == 1 {
			continue // core rows live in session/store; a type never bumped has no old row
		}
		for v := 1; v <= info.Version; v++ {
			if _, ok := have[tv{info.Name, v}]; !ok {
				t.Errorf("testdata/rows.jsonl has no %s v%d row", info.Name, v)
			}
		}
	}

	for v := 1; v <= 2; v++ {
		s, err := retry.ScheduledType.Decode(have[tv{"llm/retry", v}])
		if err != nil {
			t.Fatalf("llm/retry v%d: %v", v, err)
		}
		p := s.Policy
		if s.Failure == nil || s.Failure.RetryAfter != 2*time.Second || p.BaseDelay != 500*time.Millisecond ||
			p.MaxDelay != 10*time.Second || p.MaxElapsed != 30*time.Minute || p.RetryAfterCap != time.Minute {
			t.Fatalf("llm/retry v%d decoded to %+v, %+v", v, s.Failure, p)
		}
		a, err := retry.AbandonedType.Decode(have[tv{"llm/retry-abandoned", v}])
		if err != nil || a.Failure == nil || a.Failure.RetryAfter != 3*time.Second {
			t.Fatalf("llm/retry-abandoned v%d = %+v, %v", v, a.Failure, err)
		}
	}
}

// shapes returns the derived document of every durable event payload the
// program declares. The catalogued component configs are not walked yet: the
// config of util.watch does not derive (M1-G-FND-21.4).
func shapes() map[string]*schema.Type {
	out := map[string]*schema.Type{}
	for _, info := range event.Default().Types() {
		out["event "+info.Name] = info.Schema
	}
	return out
}

func render(m map[string]*schema.Type) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	for _, n := range names {
		b.WriteString(n + " " + string(m[n].JSONSchema()) + "\n")
	}
	return b.String()
}

// childOut names the file a re-executed copy of this test writes its documents to.
const childOut = "DSH_ALLEVENTS_DOCS"

// TestDerivationIsStableAcrossProcesses is DH-T-ZOD-2 over every durable event
// payload: deriving in this process and again in a second process gives the same
// bytes; a derivation error fails. (Property order equals field order:
// internal/schema TestDocument.)
//
//dsh:test DH-T-ZOD-2
func TestDerivationIsStableAcrossProcesses(t *testing.T) {
	here := render(shapes())
	if out := os.Getenv(childOut); out != "" {
		if err := os.WriteFile(out, []byte(here), 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	if again := render(shapes()); again != here {
		t.Fatal("deriving twice in one process changed a document")
	}
	file := filepath.Join(t.TempDir(), "docs.txt")
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestDerivationIsStableAcrossProcesses$", "-test.count=1",
		"-test.gocoverdir="+os.Getenv("GOCOVERDIR"))
	cmd.Env = append(os.Environ(), childOut+"="+file)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("second process: %v\n%s", err, out)
	}
	if there := read(t, file); there != here {
		t.Fatalf("a second process derived other documents:\n%s", golden.Diff(here, there))
	}
}

// TestDocumentsAreValidDraft2020 is DH-T-ZOD-10's first clause over every
// durable event payload: each document, as an export would write it, is resolved by
// the module as draft 2020-12 (schema.FromJSON resolves it) and keeps its
// fingerprint. (The second clause, function-like strings kept as data:
// internal/schema TestFromJSON. No export command exists yet: M1-G-FND-21.4.)
//
//dsh:test DH-T-ZOD-10
func TestDocumentsAreValidDraft2020(t *testing.T) {
	for name, typ := range shapes() {
		back, err := schema.FromJSON(typ.JSONSchema())
		if err != nil {
			t.Errorf("%s: the module refuses the document: %v", name, err)
			continue
		}
		if back.Fingerprint() != typ.Fingerprint() {
			t.Errorf("%s: the reloaded document fingerprints %s, the derived %s", name, back.Fingerprint(), typ.Fingerprint())
		}
	}
}
