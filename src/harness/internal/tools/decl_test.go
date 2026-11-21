package tools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

type readIn struct {
	Path  string `json:"path" dsh:"required"`
	Limit int    `json:"limit,omitempty" dsh:"min=1,max=2000"`
}

type readOut struct {
	Text string `json:"text"`
}

type leakyIn struct {
	Counts map[string]int `json:"counts"`
	Mode   string         `json:"mode" dsh:"ui=select"`
}

func runRead(context.Context, readIn, tools.Run) (readOut, error) { return readOut{}, nil }

// DH-T-TOOLS-1: the schema is derived once and byte-stable; a type outside the
// model-facing subset fails with every offending path.
func TestDeclarationSchema(t *testing.T) {
	spec := tools.Spec[readIn, readOut]{Name: "read", Description: "Read a file.", Run: runRead}
	a, b := tools.Define(spec), tools.Define(spec)
	if a.Err() != nil {
		t.Fatal(a.Err())
	}
	sa, sb := a.Schema(), b.Schema()
	if string(sa.Parameters) != string(sb.Parameters) || sa.Name != "read" || sa.Description != "Read a file." {
		t.Fatalf("schemas differ or are wrong: %s vs %s", sa.Parameters, sb.Parameters)
	}
	if err := sa.Validate(); err != nil {
		t.Fatalf("a good declaration's schema fails the model subset: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(sa.Parameters, &doc); err != nil || doc["type"] != "object" {
		t.Fatalf("parameters %s: want an object root", sa.Parameters)
	}
	if !tools.Known("read") {
		t.Fatal("a defined name is not Known")
	}

	bad := tools.Define(tools.Spec[leakyIn, readOut]{
		Name: "leaky", Description: "Leaks.",
		Run: func(context.Context, leakyIn, tools.Run) (readOut, error) { return readOut{}, nil },
	})
	var te *tools.Error
	if !errors.As(bad.Err(), &te) || te.Code != tools.CodeInvalidDeclaration {
		t.Fatalf("leaky: %v; want tools/invalid-declaration", bad.Err())
	}
	all := strings.Join(te.Issues, "\n")
	for _, want := range []string{"counts", "mode"} {
		if !strings.Contains(all, want) {
			t.Errorf("issues %q do not name %s", te.Issues, want)
		}
	}
	if tools.Known("leaky") {
		t.Fatal("a refused declaration is Known")
	}

	several := tools.Define(tools.Spec[readIn, readOut]{Name: "bad name", Reach: "anywhere"})
	if !errors.As(several.Err(), &te) || len(te.Issues) < 4 {
		t.Fatalf("want every problem (name, description, reach, body), got %v", several.Err())
	}
}

// dynamicRuns keeps each -count run's prefix fresh: declared prefixes are process-wide.
var dynamicRuns int

func TestDynamicDeclaration(t *testing.T) {
	run := func(_ context.Context, args json.RawMessage, _ tools.Run) (json.RawMessage, error) { return args, nil }
	dynamicRuns++
	prefix := fmt.Sprintf("mcp%d_", dynamicRuns)
	ok := tools.Dynamic(tools.DynamicSpec{
		Name: prefix + "fetch", Description: "Fetch.", Origin: "mcp:web", Run: run,
		Schema: json.RawMessage(`{"type":"object","properties":{"url":{"type":"string"}},"required":["url"]}`),
	})
	if ok.Err() != nil || ok.Origin() != "mcp:web" {
		t.Fatalf("dynamic: %v", ok.Err())
	}
	if tools.Known(prefix + "fetch") {
		t.Fatal("a dynamic name is Known without a declared prefix")
	}
	tools.DeclarePrefix(prefix)
	if !tools.Known(prefix + "fetch") {
		t.Fatal("a declared prefix does not make the dynamic name Known")
	}
	for _, spec := range []tools.DynamicSpec{
		{Name: "noorigin", Description: "x", Run: run, Schema: json.RawMessage(`{"type":"object"}`)},
		{Name: "array", Description: "x", Origin: "o", Run: run, Schema: json.RawMessage(`{"type":"array"}`)},
	} {
		if !errors.Is(tools.Dynamic(spec).Err(), tools.ErrInvalidDeclaration) {
			t.Errorf("%s: want tools/invalid-declaration", spec.Name)
		}
	}
}

func TestOverlap(t *testing.T) {
	d := tools.Define(tools.Spec[readIn, readOut]{
		Name: "read", Description: "Read a file.", Run: runRead,
		Overlap: func(in readIn) tools.Overlap { return tools.Reads("file:" + in.Path) },
	})
	o := d.Classify(json.RawMessage(`{"path":"a"}`))
	if got := o.ReadKeys(); len(got) != 1 || got[0] != "file:a" {
		t.Fatalf("Classify = %v", got)
	}
	if !d.Classify(json.RawMessage(`{"path":1}`)).IsExclusive() {
		t.Fatal("invalid arguments must classify Exclusive")
	}
	if !tools.Reads("x").Conflicts(tools.Writes("x")) || tools.Reads("x").Conflicts(tools.Reads("x")) {
		t.Fatal("reads/writes conflict rule")
	}
	if !tools.Free.Conflicts(tools.Exclusive) || tools.Free.Conflicts(tools.Writes("y")) {
		t.Fatal("free/exclusive conflict rule")
	}
}
