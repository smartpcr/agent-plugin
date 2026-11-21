//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/golden"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// trpaModuleRoot is src/harness relative to this package directory.
var trpaModuleRoot = filepath.Join("..", "..", "..")

type trpaEchoIn struct {
	Text string `json:"text" dsh:"required"`
}

type trpaEchoOut struct {
	Text string `json:"text"`
}

func trpaEcho(desc string) *tools.Declaration {
	return tools.Define(tools.Spec[trpaEchoIn, trpaEchoOut]{
		Name: "echo", Description: desc,
		Run: func(_ context.Context, in trpaEchoIn, _ tools.Run) (trpaEchoOut, error) { return trpaEchoOut(in), nil },
	})
}

// The golden's five tool shapes, identical to internal/tools/assembly_test.go.
type trpaPathIn struct {
	Path string `json:"path" dsh:"required"`
}

type trpaWriteIn struct {
	Path    string `json:"path" dsh:"required"`
	Content string `json:"content" dsh:"required"`
}

type trpaGrepIn struct {
	Pattern string `json:"pattern" dsh:"required"`
	Glob    string `json:"glob,omitempty"`
	Max     int    `json:"max,omitempty" dsh:"min=1,max=500"`
}

type trpaShellIn struct {
	Command string `json:"command" dsh:"required"`
	Mode    string `json:"mode,omitempty" dsh:"enum=sync|async"`
}

type trpaTextOut struct {
	Text string `json:"text"`
}

func trpaBody[In any](context.Context, In, tools.Run) (trpaTextOut, error) { return trpaTextOut{}, nil }

type trpaState struct {
	root *cordis.Context
	tab  scope.Table
	reg  tools.Service
	ps   *prompt.Service

	parent, child, sibling *scope.Scope
	first                  *tools.Declaration
	childHandle            *cordis.EffectHandle
	lastHandle             *cordis.EffectHandle
	lastErr                error

	five    []*tools.Declaration
	runs    []string
	toolSeq []string
}

func (s *trpaState) boot(withPrompt bool) error {
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	comps := []struct {
		comp *cordis.Component
		cfg  any
	}{{scope.Component, cordis.None{}}, {core.Component, cordis.None{}}}
	keys := []string{scope.TableKey.Name(), tools.Key.Name()}
	if withPrompt {
		cfg := prompt.Defaults()
		cfg.PersonaPrefix = "You are a careful engineer."
		cfg.PersonaSuffix = "Answer concisely."
		cfg.ToolOrder = []string{"read_file", prompt.Unlisted, "shell"}
		comps = append(comps, struct {
			comp *cordis.Component
			cfg  any
		}{prompt.Component, cfg})
		keys = append(keys, prompt.Key.Name())
	}
	for _, c := range comps {
		f, err := root.Use(c.comp, c.cfg)
		if err != nil {
			root.Dispose()
			return err
		}
		if err := f.Wait(); err != nil {
			root.Dispose()
			return fmt.Errorf("%s: %w", c.comp.Name(), err)
		}
	}
	g, err := root.Inject(keys, func(c *cordis.Context) error {
		var err error
		if s.tab, err = scope.TableKey.Get(c); err != nil {
			return err
		}
		if s.reg, err = tools.Key.Get(c); err != nil {
			return err
		}
		if withPrompt {
			s.ps, err = prompt.Key.Get(c)
		}
		return err
	})
	if err == nil {
		err = g.Wait()
	}
	if err != nil {
		root.Dispose()
		return err
	}
	s.root = root
	return nil
}

func (s *trpaState) dispose() {
	if s.root != nil {
		s.root.Dispose()
		s.root = nil
	}
}

func (s *trpaState) resolveEcho(k scope.Key) (string, error) {
	v, err := s.reg.Resolve(context.Background(), tools.Target{Scope: k})
	if err != nil {
		return "", err
	}
	defer v.Release()
	d, ok := v.Lookup("echo")
	if !ok {
		return "", fmt.Errorf("echo is not visible from scope %v", k)
	}
	return d.Description(), nil
}

func (s *trpaState) scopeNamed(name string) (*scope.Scope, error) {
	switch name {
	case "parent":
		return s.parent, nil
	case "child":
		return s.child, nil
	case "sibling":
		return s.sibling, nil
	}
	return nil, fmt.Errorf("unknown scope %q", name)
}

// --- Scenario: Duplicate tool name refused in a scope ---

func (s *trpaState) bootedRegistryWithScopes() error {
	if err := s.boot(false); err != nil {
		return err
	}
	var err error
	if s.parent, err = s.tab.New(s.root, "parent"); err != nil {
		return err
	}
	if s.child, err = s.tab.New(s.root, "child", s.parent.Key()); err != nil {
		return err
	}
	s.sibling, err = s.tab.New(s.root, "sibling", s.parent.Key())
	return err
}

func (s *trpaState) scopeWithTool(name, desc string) error {
	if name != "echo" {
		return fmt.Errorf("only echo is declared, got %q", name)
	}
	s.first = trpaEcho(desc)
	if _, err := s.reg.Register(s.parent.Ctx(), s.first); err != nil {
		return fmt.Errorf("register first echo: %w", err)
	}
	return nil
}

func (s *trpaState) secondRegisteredSameScope(name, desc string) error {
	s.lastHandle, s.lastErr = s.reg.Register(s.parent.Ctx(), trpaEcho(desc))
	return nil
}

func (s *trpaState) registrationFails(code string) error {
	if s.lastErr == nil {
		return errors.New("the second registration succeeded")
	}
	if !errors.Is(s.lastErr, tools.ErrDuplicate) {
		return fmt.Errorf("error %v is not tools/duplicate", s.lastErr)
	}
	var te *tools.Error
	if !errors.As(s.lastErr, &te) || string(te.Code) != code {
		return fmt.Errorf("error %v does not carry code %q", s.lastErr, code)
	}
	if s.lastHandle != nil {
		return errors.New("a refused registration returned a handle")
	}
	return nil
}

func (s *trpaState) scopeResolves(name, tool, want string) error {
	sc, err := s.scopeNamed(name)
	if err != nil {
		return err
	}
	got, err := s.resolveEcho(sc.Key())
	if err != nil {
		return err
	}
	if got != want {
		return fmt.Errorf("%s scope sees %s as %q, want %q", name, tool, got, want)
	}
	return nil
}

func (s *trpaState) registeredInChild(name, desc string) error {
	h, err := s.reg.Register(s.child.Ctx(), trpaEcho(desc))
	if err != nil {
		return fmt.Errorf("child registration of a shadowing echo: %w", err)
	}
	s.childHandle = h
	return nil
}

func (s *trpaState) childRegistrationDisposed(string) error {
	if s.childHandle == nil {
		return errors.New("no child registration to dispose")
	}
	s.childHandle.Dispose()
	return nil
}

// --- Scenario: Prompt assembly is deterministic ---

func (s *trpaState) fiveToolsToRegister() error {
	s.five = []*tools.Declaration{
		tools.Define(tools.Spec[trpaPathIn, trpaTextOut]{Name: "read_file", Description: "Read a file.", Run: trpaBody[trpaPathIn],
			Effects: []tools.Effect{tools.EffectNone}}),
		tools.Define(tools.Spec[trpaWriteIn, trpaTextOut]{Name: "write_file", Description: "Write a file.", Run: trpaBody[trpaWriteIn],
			Effects: []tools.Effect{tools.EffectFiles}}),
		tools.Define(tools.Spec[trpaGrepIn, trpaTextOut]{Name: "grep", Description: "Search file contents.", Run: trpaBody[trpaGrepIn]}),
		tools.Define(tools.Spec[trpaShellIn, trpaTextOut]{Name: "shell", Description: "Run a shell command.", Run: trpaBody[trpaShellIn]}),
		tools.Define(tools.Spec[trpaPathIn, trpaTextOut]{Name: "list_dir", Description: "List a directory.", Run: trpaBody[trpaPathIn],
			Disclosure: tools.Full, Reach: tools.ReachDirect}),
	}
	for _, d := range s.five {
		if err := d.Err(); err != nil {
			return fmt.Errorf("declare %s: %w", d.Name(), err)
		}
	}
	return nil
}

// assembleOnce mirrors internal/tools/assembly_test.go so the bytes it dumps are
// comparable to the committed golden.
func (s *trpaState) assembleOnce(rng *rand.Rand) (string, []string, error) {
	if err := s.boot(true); err != nil {
		return "", nil, err
	}
	defer s.dispose()
	root, reg, ps := s.root, s.reg, s.ps
	agent, err := s.tab.New(root, "agent")
	if err != nil {
		return "", nil, err
	}

	var steps []func() error
	for _, d := range s.five {
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
			return "", nil, err
		}
	}

	v, err := reg.Resolve(context.Background(), tools.Target{Scope: agent.Key()})
	if err != nil {
		return "", nil, err
	}
	defer v.Release()
	a, err := ps.Assemble(context.Background(), prompt.Input{View: v})
	if err != nil {
		return "", nil, err
	}
	text, err := ps.Render(a)
	if err != nil {
		return "", nil, err
	}
	manifest, err := json.MarshalIndent(a.Manifest(), "", "  ")
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "== system prompt ==\n%s\n\n== manifest ==\n%s\n\n== runtime contexts ==\n", text, manifest)
	for _, c := range a.Contexts {
		fmt.Fprintf(&b, "%s (%s): %s\n", c.Name, c.Owner, c.Text)
	}
	b.WriteString("\n== tools ==\n")
	var seq []string
	for _, ts := range a.Tools {
		raw, err := json.Marshal(ts)
		if err != nil {
			return "", nil, err
		}
		b.Write(raw)
		b.WriteByte('\n')
		seq = append(seq, ts.Name)
	}
	fmt.Fprintf(&b, "\n== fingerprint ==\n%s\n", a.Fingerprint)
	return b.String(), seq, nil
}

func (s *trpaState) assembledNTimes(n int) error {
	rng := rand.New(rand.NewPCG(14, 1014))
	for run := 0; run < n; run++ {
		out, seq, err := s.assembleOnce(rng)
		if err != nil {
			return fmt.Errorf("run %d: %w", run, err)
		}
		s.runs = append(s.runs, out)
		if run == 0 {
			s.toolSeq = seq
		}
	}
	return nil
}

func (s *trpaState) everyRunIdentical() error {
	if len(s.runs) == 0 {
		return errors.New("no runs were assembled")
	}
	for i, got := range s.runs[1:] {
		if got != s.runs[0] {
			return fmt.Errorf("run %d differs from run 0:\n%s", i+1, golden.Diff(s.runs[0], got))
		}
	}
	return nil
}

func (s *trpaState) bytesEqualGolden(rel string) error {
	want, err := os.ReadFile(filepath.Join(trpaModuleRoot, filepath.FromSlash(rel)))
	if err != nil {
		return fmt.Errorf("read committed golden: %w", err)
	}
	if len(want) == 0 {
		return errors.New("the committed golden is empty")
	}
	if !bytes.Equal(want, []byte(s.runs[0])) {
		return fmt.Errorf("assembly differs from %s:\n%s", rel, golden.Diff(string(want), s.runs[0]))
	}
	return nil
}

func (s *trpaState) toolListIs(csv string) error {
	if got := strings.Join(s.toolSeq, ","); got != csv {
		return fmt.Errorf("tool list %q, want %q", got, csv)
	}
	return nil
}

func InitializeScenario_tool_pipeline_approvals_and_human_commands_tool_registry_and_prompt_assembly(ctx *godog.ScenarioContext) {
	s := &trpaState{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = trpaState{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		s.dispose()
		return c, nil
	})

	ctx.Step(`^a booted tool registry with a parent scope and its child and sibling scopes$`, s.bootedRegistryWithScopes)
	ctx.Step(`^a scope with tool "([^"]+)" described as "([^"]+)"$`, s.scopeWithTool)
	ctx.Step(`^a second "([^"]+)" described as "([^"]+)" is registered in the same scope$`, s.secondRegisteredSameScope)
	ctx.Step(`^registration fails with "([^"]+)" and returns no handle$`, s.registrationFails)
	ctx.Step(`^the (parent|child|sibling) scope (?:still )?resolves "([^"]+)" to "([^"]+)"$`, s.scopeResolves)
	ctx.Step(`^"([^"]+)" described as "([^"]+)" is registered in the child scope$`, s.registeredInChild)
	ctx.Step(`^the child scope's "([^"]+)" registration is disposed$`, s.childRegistrationDisposed)

	ctx.Step(`^five tools, five sections, three variables and two runtime contexts to register$`, s.fiveToolsToRegister)
	ctx.Step(`^the step prompt and tool list are assembled (\d+) times in random registration order$`, s.assembledNTimes)
	ctx.Step(`^every run produces identical bytes$`, s.everyRunIdentical)
	ctx.Step(`^the bytes equal the committed golden "([^"]+)"$`, s.bytesEqualGolden)
	ctx.Step(`^the assembled tool list is "([^"]+)" in that order$`, s.toolListIs)
}

func TestE2E_tool_pipeline_approvals_and_human_commands_tool_registry_and_prompt_assembly(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "tool_pipeline_approvals_and_human_commands_tool_registry_and_prompt_assembly",
		ScenarioInitializer: InitializeScenario_tool_pipeline_approvals_and_human_commands_tool_registry_and_prompt_assembly,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"tool_pipeline_approvals_and_human_commands_tool_registry_and_prompt_assembly.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
