package output

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// declaredReasons parses every non-test Go file under internal/ and returns the
// string constants of type herr.Reason (or Reason inside package herr).
func declaredReasons(t *testing.T) map[herr.Reason]string {
	t.Helper()
	root := filepath.Join("..", "..")
	found := map[herr.Reason]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && d.Name() == "testdata" {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				if !isReasonType(vs.Type, file.Name.Name) {
					continue
				}
				for _, v := range vs.Values {
					lit, ok := v.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					s, _ := strconv.Unquote(lit.Value)
					found[herr.Reason(s)] = fset.Position(lit.Pos()).String()
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func isReasonType(expr ast.Expr, pkg string) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		x, ok := e.X.(*ast.Ident)
		return ok && x.Name == "herr" && e.Sel.Name == "Reason"
	case *ast.Ident:
		return pkg == "herr" && e.Name == "Reason"
	}
	return false
}

// Every error code the harness declares has a status, and the table names no code
// that nobody declares.
func TestEveryReasonHasAStatus(t *testing.T) {
	declared := declaredReasons(t)
	for r, at := range declared {
		if !Known(r) {
			t.Errorf("%s: %s has no exit status in output.codes", at, r)
		}
	}
	for r := range codes {
		if _, ok := declared[r]; !ok {
			t.Errorf("output.codes lists %s, which no package declares", r)
		}
	}
}

func TestStatusTableIsClosed(t *testing.T) {
	statuses := map[int]bool{}
	for _, s := range Statuses() {
		if statuses[s.Code] {
			t.Errorf("status %d listed twice", s.Code)
		}
		statuses[s.Code] = true
	}
	for r, s := range codes {
		if !statuses[s] {
			t.Errorf("%s maps to %d, which is not in the table", r, s)
		}
	}
}

func TestStatus(t *testing.T) {
	cases := []struct {
		err  error
		want int
	}{
		{nil, OK},
		{errors.New("plain"), Failed},
		{Usagef("unknown flag"), Usage},
		{herr.Wrap("profile/bad-layer", herr.New(ReasonUsage, "inner"), "outer"), Usage},
		{fmt.Errorf("wrapped: %w", herr.New("profile/unknown", "x")), Invalid},
		{herr.New("nobody/declares-this", "x"), Failed},
		{context.Canceled, Cancelled},
		{context.DeadlineExceeded, Limit},
	}
	for _, c := range cases {
		if got := Status(c.err); got != c.want {
			t.Errorf("Status(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

func TestFailText(t *testing.T) {
	var out, diag bytes.Buffer
	r := New(&out, &diag, Text)
	if got := r.Fail("catalog", Usagef("unknown flag: --x")); got != Usage {
		t.Fatalf("status = %d", got)
	}
	want := "dsh catalog: cli/usage: unknown flag: --x\n" + HelpHint + "\n"
	if diag.String() != want || out.Len() != 0 {
		t.Errorf("diag = %q, out = %q", diag.String(), out.String())
	}

	diag.Reset()
	if got := r.Fail("", errors.New("boom\nsecond line")); got != Failed {
		t.Fatalf("status = %d", got)
	}
	if diag.String() != "dsh: cli/failed: boom second line\n" {
		t.Errorf("diag = %q", diag.String())
	}
	if r.Fail("x", nil) != OK {
		t.Error("nil is not OK")
	}
}

func TestFailJSONEndsResultWithError(t *testing.T) {
	var out, diag bytes.Buffer
	r := New(&out, &diag, JSON)
	status := r.Fail("version", herr.New("profile/unknown", "no profile"))
	if status != Invalid {
		t.Fatalf("status = %d", status)
	}
	var doc map[string]map[string]string
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatalf("result %q: %v", out.String(), err)
	}
	if doc["error"]["code"] != "profile/unknown" || doc["error"]["message"] == "" {
		t.Errorf("error document = %v", doc)
	}
	if !strings.HasPrefix(diag.String(), "dsh version: profile/unknown: ") {
		t.Errorf("diag = %q", diag.String())
	}
}

func TestResultModes(t *testing.T) {
	var out bytes.Buffer
	r := New(&out, &bytes.Buffer{}, JSON)
	if err := r.Value(map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.Value(1); herr.Kind(err) != ReasonTwoDocuments {
		t.Errorf("second JSON value: %v", err)
	}
	if err := r.Line("text"); herr.Kind(err) != ReasonTwoDocuments {
		t.Errorf("text under JSON: %v", err)
	}
	if out.String() != "{\n  \"a\": 1\n}\n" {
		t.Errorf("out = %q", out.String())
	}

	out.Reset()
	lines := New(&out, &bytes.Buffer{}, JSONLines)
	_ = lines.Value(1)
	_ = lines.Value("two")
	if out.String() != "1\n\"two\"\n" {
		t.Errorf("JSON Lines = %q", out.String())
	}
}

type brokenWriter struct{ writes int }

func (b *brokenWriter) Write([]byte) (int, error) {
	b.writes++
	return 0, errors.New("broken pipe")
}

func TestClosedResultStream(t *testing.T) {
	w := &brokenWriter{}
	r := New(w, &bytes.Buffer{}, Text)
	if err := r.Line("a"); herr.Kind(err) != ReasonClosed {
		t.Fatalf("first write: %v", err)
	}
	if err := r.Line("b"); herr.Kind(err) != ReasonClosed || w.writes != 1 {
		t.Errorf("second write: %v after %d writes", err, w.writes)
	}
}
