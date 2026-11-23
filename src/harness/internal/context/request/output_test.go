package request_test

import (
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
)

// claimedMsg is a claimed user message of one text block.
func claimedMsg(text string) vocab.Message {
	return must(vocab.NewMessage(vocab.RoleUser, vocab.TextBlock(text)))
}

// contribution is a message of kind with text.
func contribution(kind, text string) vocab.Message {
	return must(request.Message(kind, "test."+kind, text, nil))
}

// texts is each message's text, its blocks joined by "+".
func texts(msgs []vocab.Message) []string {
	var out []string
	for _, m := range msgs {
		var parts []string
		for _, b := range m.Blocks() {
			if b.Kind == vocab.KindText {
				parts = append(parts, b.Text.Text)
			} else {
				parts = append(parts, string(b.Kind))
			}
		}
		out = append(out, strings.Join(parts, "+"))
	}
	return out
}

// DH-T-CONTEXT-2 (17.3.4, an order case): the step's batch is each claimed
// message, reworded where a contributor reworded it, directly followed by the
// messages placed after it, contributor by contributor in table order, then
// every contributor's Messages in table order, whatever the registration
// order. A reworded message keeps its id, source and other blocks; its text
// blocks become the one readable text.
//
//dsh:test DH-T-CONTEXT-2
func TestAfterAndRewordMergeInTableOrder(t *testing.T) {
	root, tab := bare(t)
	p, _ := must2(request.New(root, request.Options{Table: tab, Sessions: func(agent.Ref) (request.Log, error) { return &memLog{}, nil }}))
	img := vocab.ImageBlock("image/png", "att_1")
	c1 := claimedMsg("see @session:abc")
	c2 := must(vocab.RestoreMessage("msg_c2", vocab.RoleUser, &vocab.Source{Kind: "user", Producers: []string{"inbox"}},
		vocab.TextBlock("compare @session:def"), img, vocab.TextBlock("and this")))
	// Registered out of table order: skill-invocation first.
	must(p.Register(root, &fake{name: request.KindSkillInvocation, t: t, run: func(request.Input) (request.Output, error) {
		return request.Output{
			Messages: []vocab.Message{contribution(request.KindSkillInvocation, "skill tail")},
			After:    map[string][]vocab.Message{c1.ID(): {contribution(request.KindSkillInvocation, "skill after c1")}},
		}, nil
	}}))
	must(p.Register(root, &fake{name: request.KindClock, t: t, run: func(in request.Input) (request.Output, error) {
		if len(in.Claimed) != 2 || in.Claimed[1].ID() != c2.ID() {
			t.Errorf("the clock saw claimed %v", texts(in.Claimed))
		}
		return request.Output{
			Messages: []vocab.Message{contribution(request.KindClock, "clock tail")},
			After: map[string][]vocab.Message{
				c1.ID(): {contribution(request.KindClock, "clock after c1 #1"), contribution(request.KindClock, "clock after c1 #2")},
				c2.ID(): {contribution(request.KindClock, "clock after c2")},
			},
		}, nil
	}}))
	must(p.Register(root, &fake{name: request.KindInstructions, t: t, run: func(request.Input) (request.Output, error) {
		return request.Output{Reword: map[string]string{c2.ID(): "compare session 'Release plan' (def)"}}, nil
	}}))

	d := askStep(t, root, agent.PreStepRequest{Turn: 1, Step: 1, Messages: []vocab.Message{c1, c2}})
	enter, ok := d.(agent.Enter)
	if !ok {
		t.Fatalf("decision %+v, want enter", d)
	}
	want := []string{
		"see @session:abc", "clock after c1 #1", "clock after c1 #2", "skill after c1",
		"compare session 'Release plan' (def)+image", "clock after c2",
		"clock tail", "skill tail",
	}
	if got := texts(enter.Messages); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("batch\n got %q\nwant %q", got, want)
	}
	re := enter.Messages[4]
	if src, ok := re.Source(); re.ID() != c2.ID() || re.Role() != vocab.RoleUser || !ok || src.Kind != "user" || src.Producers[0] != "inbox" {
		t.Fatalf("the reworded message lost its id, role or source: %s %s %+v", re.ID(), re.Role(), src)
	}
	if enter.Messages[0].ID() != c1.ID() {
		t.Fatal("the claimed message that nothing reworded changed")
	}
}

// DH-T-CONTEXT-2, -3 (17.3.4, the checks): an output that places messages
// after, or rewords, a message outside the claimed batch, rewords with no
// text, rewords a message with no text, rewords one another contributor
// already reworded, repeats an id or carries a message of another kind fails
// its contributor as a whole: an optional one is skipped with the rule named
// and nothing of its output enters; a required one rejects the step.
//
//dsh:test DH-T-CONTEXT-2, DH-T-CONTEXT-3
func TestAfterAndRewordAreChecked(t *testing.T) {
	c1 := claimedMsg("see @session:abc")
	imageOnly := must(vocab.NewMessage(vocab.RoleUser, vocab.ImageBlock("image/png", "att_1")))
	dup := contribution(request.KindClock, "twice")
	for _, tc := range []struct {
		name string
		out  request.Output
		want string
	}{
		{"after an unknown message", request.Output{After: map[string][]vocab.Message{"msg_nope": {contribution(request.KindClock, "x")}}}, "msg_nope, which is not a claimed message"},
		{"reword an unknown message", request.Output{Reword: map[string]string{"msg_nope": "x"}}, "rewords msg_nope, which is not a claimed message"},
		{"reword with no text", request.Output{Reword: map[string]string{c1.ID(): "  "}}, "with no text"},
		{"reword a message with no text", request.Output{Reword: map[string]string{imageOnly.ID(): "x"}}, "has no text to reword"},
		{"reword twice", request.Output{Reword: map[string]string{c1.ID(): "again"}}, `which "instructions" already reworded`},
		{"a repeated id", request.Output{Messages: []vocab.Message{dup}, After: map[string][]vocab.Message{c1.ID(): {dup}}}, "already in the step's batch"},
		{"a claimed id", request.Output{Messages: []vocab.Message{must(c1.WithSource(vocab.Source{Kind: request.KindClock, Producers: []string{"test.clock"}}))}}, "already in the step's batch"},
		{"an after message of another kind", request.Output{After: map[string][]vocab.Message{c1.ID(): {contribution(request.KindTmux, "x")}}}, `without a "clock" source`},
	} {
		for _, required := range []bool{false, true} {
			name := tc.name + map[bool]string{false: " (optional)", true: " (required)"}[required]
			t.Run(name, func(t *testing.T) {
				root, tab := bare(t)
				log := &memLog{}
				p, _ := must2(request.New(root, request.Options{Table: tab, Sessions: func(agent.Ref) (request.Log, error) { return log, nil }}))
				must(p.Register(root, &fake{name: request.KindInstructions, t: t, run: func(request.Input) (request.Output, error) {
					return request.Output{Reword: map[string]string{c1.ID(): "see session 'Notes' (abc)"}}, nil
				}}))
				out := tc.out
				// The failing output also carries a valid part, which must not enter.
				out.Messages = append(out.Messages, contribution(request.KindClock, "valid part"))
				must(p.Register(root, &fake{name: request.KindClock, required: required, t: t, run: func(request.Input) (request.Output, error) {
					return out, nil
				}}))
				d := askStep(t, root, agent.PreStepRequest{Turn: 1, Step: 1, Messages: []vocab.Message{c1, imageOnly}})
				if required {
					rej, ok := d.(agent.Reject)
					if !ok || rej.By != request.KindClock || !strings.Contains(rej.Reason, tc.want) {
						t.Fatalf("decision %+v, want a reject by the clock naming %q", d, tc.want)
					}
					return
				}
				enter, ok := d.(agent.Enter)
				if !ok {
					t.Fatalf("decision %+v, want enter", d)
				}
				if got := texts(enter.Messages); strings.Join(got, "|") != "see session 'Notes' (abc)|image" {
					t.Fatalf("batch %q: part of a failed output entered", got)
				}
				if got := log.types(); len(got) != 1 || got[0] != request.SkippedType.Name() {
					t.Fatalf("logged %q, want one context/skipped", got)
				}
			})
		}
	}
}
