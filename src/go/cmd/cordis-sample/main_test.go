package main

import (
	"bytes"
	_ "embed"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

var update = flag.Bool("update", false, "rewrite testdata/output.golden from this run")

//go:embed main.go
var mainSource string

const goldenPath = "testdata/output.golden"

// event is one line written by the sample, stamped with the bubble clock.
type event struct {
	at   time.Time
	beat bool
	text string
}

// recorder is both the sample's Out and its log exporter: it writes every line to one
// buffer, log lines as "[<level>] <name> <text>" with no timestamp.
type recorder struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	events []event
}

func (r *recorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event{at: time.Now(), text: strings.TrimRight(string(p), "\n")})
	return r.buf.Write(p)
}

func (r *recorder) Export(message cordis.LogMessage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	line := fmt.Sprintf("[%s] %s %s", message.Level, message.Name, message.Text)
	r.events = append(r.events, event{at: message.Time, beat: strings.HasPrefix(message.Text, "beat:"), text: line})
	r.buf.WriteString(line + "\n")
}

// coincident reports a beat: line that carries the same instant as any other line.
// Goroutines runnable at one instant have no defined order, so such a pair would make
// the golden flaky; this turns it into a deterministic failure instead.
func (r *recorder) coincident() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, beat := range r.events {
		if !beat.beat {
			continue
		}
		for _, other := range r.events {
			if !other.beat && other.at.Equal(beat.at) {
				return fmt.Errorf("coincident events at %s: %q and %q", beat.at.Format("15:04:05.000"), beat.text, other.text)
			}
		}
	}
	return nil
}

// barrierPause resumes main 1ms before the window boundary, after every goroutine in
// the bubble has settled, so ticks strictly inside the window have already logged.
func barrierPause(d time.Duration) {
	synctest.Wait()
	<-time.After(d - time.Millisecond)
	synctest.Wait()
}

// exactPause resumes main on the window boundary itself, where a tick can fire.
func exactPause(d time.Duration) {
	synctest.Wait()
	<-time.After(d)
	synctest.Wait()
}

// runSample runs the walkthrough in a synctest bubble and returns its normalized output.
func runSample(t *testing.T, pause func(time.Duration)) (output string, coincidence error) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		rec := &recorder{}
		if err := run(sampleEnv{Out: rec, TempDir: dir, Exporter: rec, Pause: pause, Settle: synctest.Wait}); err != nil {
			t.Fatalf("run: %v", err)
		}
		rec.mu.Lock()
		output = strings.ReplaceAll(rec.buf.String(), dir, "<TMP>")
		rec.mu.Unlock()
		output = strings.ReplaceAll(output, `\`, "/")
		coincidence = rec.coincident()
	})
	return output, coincidence
}

func TestOutputMatchesGolden(t *testing.T) {
	output, coincidence := runSample(t, barrierPause)
	if coincidence != nil {
		t.Fatal(coincidence)
	}
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, []byte(output), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("%v (record it with -update)", err)
	}
	if output != string(golden) {
		t.Errorf("output differs from %s\n--- got\n%s\n--- want\n%s", goldenPath, output, golden)
	}
}

func TestSameInstantIsCoincidentEvents(t *testing.T) {
	_, coincidence := runSample(t, exactPause)
	if coincidence == nil || !strings.Contains(coincidence.Error(), "coincident events") {
		t.Fatalf("a pause ending on a tick instant must fail with coincident events, got %v", coincidence)
	}
}

func TestMainWiresRealEnvironment(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", mainSource, 0)
	if err != nil {
		t.Fatal(err)
	}
	var mainFunc *ast.FuncDecl
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "main" {
			mainFunc = fn
		}
	}
	if mainFunc == nil {
		t.Fatal("main.go declares no func main")
	}
	render := func(node ast.Node) string {
		var b strings.Builder
		if err := printer.Fprint(&b, fset, node); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}

	envs := map[string]*ast.CompositeLit{} // variable name -> sampleEnv literal
	var runArgs []string
	ast.Inspect(mainFunc.Body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, rhs := range n.Rhs {
				if lit, ok := rhs.(*ast.CompositeLit); ok && render(lit.Type) == "sampleEnv" && i < len(n.Lhs) {
					envs[render(n.Lhs[i])] = lit
				}
			}
		case *ast.CallExpr:
			if ident, ok := n.Fun.(*ast.Ident); ok && ident.Name == "run" && len(n.Args) == 1 {
				runArgs = append(runArgs, render(n.Args[0]))
			}
		}
		return true
	})
	if len(runArgs) != 1 {
		t.Fatalf("main must call run exactly once, found %d calls", len(runArgs))
	}
	lit := envs[runArgs[0]]
	if lit == nil {
		t.Fatalf("main calls run(%s), which is not a sampleEnv literal declared in main", runArgs[0])
	}
	fields := map[string]string{}
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			fields[render(kv.Key)] = render(kv.Value)
		}
	}
	want := map[string]string{
		"Out":      "os.Stdout",
		"TempDir":  "os.TempDir()",
		"Exporter": "cordis.NewConsoleExporter(os.Stdout, os.Stderr)",
		"Pause":    "time.Sleep",
		"Settle":   "func() {}",
	}
	for key, value := range want {
		if fields[key] != value {
			t.Errorf("main passes %s: %q, want %q", key, fields[key], value)
		}
	}
	if len(fields) != len(want) {
		t.Errorf("main sets fields %v, want exactly %v", fields, want)
	}
}
