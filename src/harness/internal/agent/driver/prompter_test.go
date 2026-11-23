package driver_test

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// testPrompt is the minimal Prompter of the driver tests, so they run without
// the tools.prompt component: one section of text (none when empty), the view's
// tools by name or in order when set, and the runtime contexts given. Its
// fingerprint covers the text and the tools, as the component's does.
type testPrompt struct {
	mu    sync.Mutex
	text  string
	order []string
	ctxs  []prompt.Snapshot
	fail  error
	calls int
}

// defaultPrompt is the system prompt of a rig that names none.
const defaultPrompt = "You are the test agent."

func newTestPrompt(text string) *testPrompt { return &testPrompt{text: text} }

func (p *testPrompt) set(fn func(p *testPrompt)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(p)
}

func (p *testPrompt) assembled() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func (p *testPrompt) Assemble(ctx context.Context, in prompt.Input) (prompt.Assembly, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if err := ctx.Err(); err != nil {
		return prompt.Assembly{}, err
	}
	if p.fail != nil {
		return prompt.Assembly{}, p.fail
	}
	var a prompt.Assembly
	if p.text != "" {
		a.Sections = []prompt.RenderedSection{{Name: "test", Order: prompt.OrderHarness, Owner: "driver-test", Text: p.text}}
	}
	ds := in.View.Tools()
	var names []string
	byName := map[string]vocab.ToolSchema{}
	for _, d := range ds {
		names = append(names, d.Name())
		byName[d.Name()] = d.Schema()
	}
	slices.Sort(names)
	if len(p.order) > 0 {
		var ordered []string
		for _, n := range p.order {
			if _, ok := byName[n]; ok {
				ordered = append(ordered, n)
			}
		}
		for _, n := range names {
			if !slices.Contains(ordered, n) {
				ordered = append(ordered, n)
			}
		}
		names = ordered
	}
	for _, n := range names {
		a.Tools = append(a.Tools, byName[n])
	}
	a.Contexts = slices.Clone(p.ctxs)
	fp, err := vocab.Digest(struct {
		Text  string             `json:"text"`
		Tools []vocab.ToolSchema `json:"tools"`
	}{p.text, a.Tools})
	if err != nil {
		return prompt.Assembly{}, err
	}
	a.Fingerprint = fp
	return a, nil
}

func (p *testPrompt) Render(a prompt.Assembly) (string, error) {
	texts := make([]string, len(a.Sections))
	for i, s := range a.Sections {
		texts[i] = s.Text
	}
	return strings.Join(texts, "\n\n"), nil
}
