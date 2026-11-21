package tools_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

type pathIn struct {
	Path string `json:"path" dsh:"required"`
}

type writeIn struct {
	Path    string `json:"path" dsh:"required"`
	Content string `json:"content" dsh:"required"`
}

type grepIn struct {
	Pattern string `json:"pattern" dsh:"required"`
	Glob    string `json:"glob,omitempty"`
	Max     int    `json:"max,omitempty" dsh:"min=1,max=500"`
}

type shellIn struct {
	Command string `json:"command" dsh:"required"`
	Mode    string `json:"mode,omitempty" dsh:"enum=sync|async"`
}

type textOut struct {
	Text string `json:"text"`
}

func body[In any](context.Context, In, tools.Run) (textOut, error) { return textOut{}, nil }

// fiveTools is the golden's tool set, declared once per test binary.
var fiveTools = []*tools.Declaration{
	tools.Define(tools.Spec[pathIn, textOut]{Name: "read_file", Description: "Read a file.", Run: body[pathIn],
		Effects: []tools.Effect{tools.EffectNone}}),
	tools.Define(tools.Spec[writeIn, textOut]{Name: "write_file", Description: "Write a file.", Run: body[writeIn],
		Effects: []tools.Effect{tools.EffectFiles}}),
	tools.Define(tools.Spec[grepIn, textOut]{Name: "grep", Description: "Search file contents.", Run: body[grepIn]}),
	tools.Define(tools.Spec[shellIn, textOut]{Name: "shell", Description: "Run a shell command.", Run: body[shellIn]}),
	tools.Define(tools.Spec[pathIn, textOut]{Name: "list_dir", Description: "List a directory.", Run: body[pathIn],
		Disclosure: tools.Full, Reach: tools.ReachDirect}),
}

// assembleOnce boots a fresh root, registers the five tools, sections, variables
// and runtime contexts in the order perm gives, and dumps the assembly.
func assembleOnce(t *testing.T, rng *rand.Rand) string {
	t.Helper()
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	defer root.Dispose()

	cfg := prompt.Defaults()
	cfg.PersonaPrefix = "You are a careful engineer."
	cfg.PersonaSuffix = "Answer concisely."
	cfg.ToolOrder = []string{"read_file", prompt.Unlisted, "shell"}
	for _, c := range []struct {
		comp *cordis.Component
		cfg  any
	}{{scope.Component, cordis.None{}}, {core.Component, cordis.None{}}, {prompt.Component, cfg}} {
		f, err := root.Use(c.comp, c.cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	var (
		tab scope.Table
		reg tools.Service
		ps  *prompt.Service
	)
	g, err := root.Inject([]string{scope.TableKey.Name(), tools.Key.Name(), prompt.Key.Name()}, func(c *cordis.Context) error {
		var err error
		if tab, err = scope.TableKey.Get(c); err != nil {
			return err
		}
		if reg, err = tools.Key.Get(c); err != nil {
			return err
		}
		ps, err = prompt.Key.Get(c)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.Wait(); err != nil {
		t.Fatal(err)
	}
	agent, err := tab.New(root, "agent")
	if err != nil {
		t.Fatal(err)
	}

	var steps []func() error
	for _, d := range fiveTools {
		steps = append(steps, func() error { _, err := reg.Register(root, d); return err })
	}
	add := func(sec prompt.Section) {
		steps = append(steps, func() error { _, err := ps.Section(root, sec); return err })
	}
	add(prompt.Section{Name: "harness", Order: prompt.OrderHarness, Text: "Work in {{cwd}} on {{os}}."})
	add(prompt.Section{Name: "tool-guidance", Order: prompt.OrderTools, Text: "Prefer read_file over shell."})
	add(prompt.Section{Name: "environment", Order: prompt.OrderEnvironment, Text: "Model: {{model}}."})
	add(prompt.Section{Name: "project", Order: prompt.OrderProject, Text: "Project notes."})
	add(prompt.Section{Name: "a-tie", Order: prompt.OrderProject, Text: "Ties order by name."})
	for name, v := range map[string]string{"cwd": "/work", "os": "linux", "model": "deepseek-chat"} {
		steps = append(steps, func() error {
			_, err := ps.Variable(root, name, func(prompt.Input) (string, bool, error) { return v, true, nil })
			return err
		})
	}
	for name, order := range map[string]int{"clock": 1, "git": 2} {
		steps = append(steps, func() error {
			_, err := ps.Context(root, prompt.RuntimeContext{Name: name, Order: order, Optional: true,
				Func: func(prompt.Input) (string, bool, error) { return name + " snapshot", true, nil }})
			return err
		})
	}
	steps = append(steps, func() error {
		_, err := ps.Section(agent.Ctx(), prompt.Section{Name: "project", Order: prompt.OrderProject, Text: "Agent project notes."})
		return err
	})
	rng.Shuffle(len(steps), func(i, j int) { steps[i], steps[j] = steps[j], steps[i] })
	for _, step := range steps {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}

	v, err := reg.Resolve(context.Background(), tools.Target{Scope: agent.Key()})
	if err != nil {
		t.Fatal(err)
	}
	defer v.Release()
	a, err := ps.Assemble(context.Background(), prompt.Input{View: v})
	if err != nil {
		t.Fatal(err)
	}
	text, err := ps.Render(a)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.MarshalIndent(a.Manifest(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== system prompt ==\n%s\n\n== manifest ==\n%s\n\n== runtime contexts ==\n", text, manifest)
	for _, c := range a.Contexts {
		fmt.Fprintf(&b, "%s (%s): %s\n", c.Name, c.Owner, c.Text)
	}
	b.WriteString("\n== tools ==\n")
	for _, ts := range a.Tools {
		raw, err := json.Marshal(ts)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	fmt.Fprintf(&b, "\n== fingerprint ==\n%s\n", a.Fingerprint)
	return b.String()
}

// DH-T-TOOLS-14: five tools (and the sections, variables and contexts around them)
// registered in random order across 100 runs assemble to bytes equal to the
// committed golden.
func TestPromptAssemblyIsDeterministic(t *testing.T) {
	rng := rand.New(rand.NewPCG(14, 1014))
	first := assembleOnce(t, rng)
	for run := 1; run < 100; run++ {
		if got := assembleOnce(t, rng); got != first {
			t.Fatalf("run %d differs from run 0:\n%s", run, golden.Diff(first, got))
		}
	}
	golden.Assert(t, "prompt", first)
}
