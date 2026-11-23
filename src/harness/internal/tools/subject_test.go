package tools_test

import (
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
)

// A subject is valid only for a known kind and a name a grant can match; AskFor
// with one that is not valid is a plain Ask, with no subject.
func TestSubjectValidityAndAskFor(t *testing.T) {
	for _, tc := range []struct {
		s    tools.Subject
		want bool
	}{
		{tools.Subject{Kind: tools.SubjectDirectory, Name: "/w"}, true},
		{tools.Subject{Kind: tools.SubjectDirectory}, false},
		{tools.Subject{Kind: tools.SubjectApplication, Name: "macos:com.example"}, true},
		{tools.Subject{Kind: tools.SubjectApplication, Name: "win:C:\\app.exe"}, true},
		{tools.Subject{Kind: tools.SubjectApplication, Name: "macos"}, false},
		{tools.Subject{Kind: tools.SubjectApplication, Name: ":app"}, false},
		{tools.Subject{Kind: tools.SubjectApplication, Name: "macos:"}, false},
		{tools.Subject{Kind: "website", Name: "https://example.com"}, false},
	} {
		if got := tc.s.Valid(); got != tc.want {
			t.Errorf("%+v.Valid() = %v", tc.s, got)
		}
		d := tools.AskFor("r", "d", tc.s)
		if d.Verdict != tools.VerdictAsk || d.Reason != "r" || d.Display != "d" || (d.Subject != nil) != tc.want {
			t.Errorf("AskFor(%+v) = %+v", tc.s, d)
		}
		if tc.want && (*d.Subject != tc.s) {
			t.Errorf("AskFor kept %+v, want %+v", *d.Subject, tc.s)
		}
	}
	if d := tools.Ask("r", ""); d.Subject != nil {
		t.Fatalf("Ask has subject %+v", d.Subject)
	}
}

type box struct{ Items []string }

// A Settlement hands out copies: the value, the spill reference, the parts and
// the durable bytes an observer changes reach neither a second read nor the
// result it was built from. A value that cannot be copied reads as nil.
func TestSettlementIsImmutable(t *testing.T) {
	c := tools.NewCall("c1", "t", []byte(`{}`), tools.OriginModel, "", agent.Ref{}, nil, nil)
	r := tools.Result{CallID: "c1", Text: "a", Value: box{Items: []string{"x"}}, Spill: &event.Spill{Path: "p", Bytes: 1},
		Parts: []vocab.Part{vocab.TextPart("a"), vocab.ImagePart("image/png", "att")}, Seq: 7}
	s := tools.NewSettlement(c, r, `{"call_id":"c1"}`)
	got := s.Result()
	got.Value.(box).Items[0] = "y"
	got.Spill.Path = "q"
	got.Parts[1].Image.Ref = "other"
	s.Durable()[0] = 'X'
	again := s.Result()
	if again.Value.(box).Items[0] != "x" || again.Spill.Path != "p" || again.Parts[1].Image.Ref != "att" || again.Seq != 7 ||
		string(s.Durable()) != `{"call_id":"c1"}` || s.Call().ID() != "c1" {
		t.Fatalf("a settlement changed: %+v, durable %s", again, s.Durable())
	}
	if r.Value.(box).Items[0] != "x" || r.Spill.Path != "p" || r.Parts[1].Image.Ref != "att" {
		t.Fatalf("the source result changed: %+v", r)
	}
	if v := tools.NewSettlement(c, tools.Result{Value: func() {}}, "").Result().Value; v != nil {
		t.Fatalf("an uncopyable value reads as %v", v)
	}
	if p := tools.CloneParts(nil); p != nil {
		t.Fatal("CloneParts(nil) is not nil")
	}
}
