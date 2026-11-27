package title

import (
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// DH-T-VIEW-12: over-limit input is reduced as specified.
func TestClipAndReduce(t *testing.T) {
	if got := clip("héllo", 4); got != "h…" {
		t.Errorf("clip = %q", got)
	}
	if got := clip("hello", 3); got != "" {
		t.Errorf("clip below the mark = %q", got)
	}
	if got := clip("hi", 9); got != "hi" {
		t.Errorf("clip of a short text = %q", got)
	}
	ms := []Message{{1, strings.Repeat("a", 10)}, {2, strings.Repeat("b", 10)}, {3, strings.Repeat("c", 10)}, {4, strings.Repeat("d", 10)}}
	if got := reduce(ms, 100); len(got) != 4 {
		t.Errorf("reduce within budget = %v", got)
	}
	// The first and the newest that fit stay whole, the next is clipped, in log order.
	got := reduce(ms, 28)
	if len(got) != 3 || got[0].Seq != 1 || got[1].Seq != 3 || got[2].Seq != 4 || got[1].Text != "ccccc…" {
		t.Errorf("reduce = %+v", got)
	}
	if got := reduce(ms[:1], 5); len(got) != 1 || got[0].Text != "aa…" {
		t.Errorf("reduce of one long message = %+v", got)
	}
}

func TestValidators(t *testing.T) {
	ok := RequestPayload{Provider: "p", Purpose: Purpose, Route: "r", Messages: []string{"m"}, MessageSeqs: []event.Seq{1}}
	if err := validRequest(ok); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]RequestPayload{
		"no provider": {Purpose: Purpose, Route: "r", Messages: []string{"m"}, MessageSeqs: []event.Seq{1}},
		"no route":    {Provider: "p", Purpose: Purpose, Messages: []string{"m"}, MessageSeqs: []event.Seq{1}},
		"no message":  {Provider: "p", Purpose: Purpose, Route: "r"},
		"seq count":   {Provider: "p", Purpose: Purpose, Route: "r", Messages: []string{"m"}, MessageSeqs: []event.Seq{1, 2}},
	} {
		if validRequest(p) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if validAttempt(AttemptPayload{Outcome: "maybe"}) == nil || validAttempt(AttemptPayload{Outcome: OutcomeSuperseded}) != nil {
		t.Error("the attempt outcome set is not closed")
	}
	if (GeneratorConfig{ReasoningEffort: "high"}).effort() != "high" || (GeneratorConfig{}).withDefaults().effort() != "" {
		t.Error("effort")
	}
}

func TestProviders(t *testing.T) {
	r := NewProviders()
	if err := r.Register(NewFirstPrompt(GeneratorConfig{})); err != nil {
		t.Fatal(err)
	}
	if r.Register(NewFirstPrompt(GeneratorConfig{})) == nil || r.Register(nil) == nil {
		t.Error("a duplicate or nil provider was registered")
	}
	if _, err := r.Get("nope"); err == nil || !strings.Contains(err.Error(), FirstPromptID) {
		t.Errorf("Get of an unknown id: %v", err)
	}
	p, _ := r.Get(FirstPromptID)
	if _, err := p.Prompt(Input{}); err == nil {
		t.Error("a prompt with no input")
	}
	pr, err := p.Prompt(Input{Prompts: []Message{{1, "a"}, {2, "b"}}, Refresh: true})
	if err != nil || len(pr.Messages) != 2 || !strings.Contains(pr.System, "5 words") {
		t.Errorf("refresh prompt = %+v, %v", pr, err)
	}
}
