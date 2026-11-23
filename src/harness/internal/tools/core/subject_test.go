package core_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

// Stage 16.2.2 (M1-G-PIPE-6): a policy names what a call needs with AskFor, the
// pipeline passes it to the approval service at stage 4, and an "allow for this
// session" answer creates a grant that later calls match.

type pathIn struct {
	Path string `json:"path" dsh:"required"`
}

func writeTool(tr *trace) *tools.Declaration {
	return tools.Define(tools.Spec[pathIn, string]{
		Name: "write", Description: "Writes a file.",
		Run: func(_ context.Context, in pathIn, _ tools.Run) (string, error) {
			tr.add("body")
			return "wrote " + in.Path, nil
		},
	})
}

// underDir asks for a write with the directory holding the path as its subject.
func underDir(_ context.Context, c tools.Call) tools.Decision {
	dir := filepath.Dir(c.Input().(pathIn).Path)
	return tools.AskFor("writes a file", "", tools.Subject{Kind: tools.SubjectDirectory, Name: dir, Label: "writes under " + dir})
}

func pathCall(p string) [2]string {
	raw, _ := json.Marshal(pathIn{Path: p})
	return [2]string{"write", string(raw)}
}

// prompts is the approval prompts delivered to reg.
func prompts(t *testing.T, reg *countingRegistry) []approval.Prompt {
	t.Helper()
	var out []approval.Prompt
	for _, it := range reg.delivered() {
		p, ok := it.Payload.(approval.Prompt)
		if !ok {
			t.Fatalf("payload %T", it.Payload)
		}
		out = append(out, p)
	}
	return out
}

// Scenario "A session grant matches a later call" (stage 16.2): a policy asks
// with the directory /w as the subject of a write to /w/a, and the answerer
// allows it for the session; a write to /w/b is then approved by that grant with
// no delivery, and a write to /wx/c, a prefix sibling, is asked again.
//
//dsh:test DH-T-HUMAN-5
func TestSessionGrantMatchesALaterCall(t *testing.T) {
	root := t.TempDir()
	w := filepath.Join(root, "w")
	e := newEnv(t)
	tr := &trace{}
	grants := approval.NewGrants()
	reg := &countingRegistry{answer: func(n int) human.Answer {
		if n == 1 {
			return human.Answer{Outcome: human.OutcomeAllowedSession}
		}
		return human.Answer{Outcome: human.OutcomeDenied, Text: "not there"}
	}}
	svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg, Grants: grants})})
	e.register(t, e.root, writeTool(tr))
	if _, err := svc.Decide(e.root, "under-dir", underDir); err != nil {
		t.Fatal(err)
	}
	calls := [][2]string{pathCall(filepath.Join(w, "a")), pathCall(filepath.Join(w, "b")), pathCall(filepath.Join(root, "wx", "c"))}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)

	live := grants.List(me)
	if len(live) != 1 || live[0].Subject.Kind != approval.ScopeDirectory || !strings.EqualFold(filepath.Base(live[0].Subject.Path), "w") || live[0].Subject.Label != "writes under "+w {
		t.Fatalf("grants %+v, want one directory grant of %s", live, w)
	}
	id := live[0].ID
	if r := res[0]; r.IsError || r.Approval != string(approval.OutcomeAllowedOnce) || r.Grant != id {
		t.Fatalf("first call %+v, want allowed once and grant %s created", r, id)
	}
	if r := res[1]; r.IsError || r.Approval != string(approval.OutcomeAllowedByGrant) || r.Grant != id {
		t.Fatalf("second call %+v, want allowed by grant %s", r, id)
	}
	if r := res[2]; r.Code != tools.CodeApprovalRejected || r.Approval != string(approval.OutcomeRejected) || r.Grant != "" {
		t.Fatalf("prefix sibling %+v, want asked again and rejected", r)
	}
	if tr.count("body") != 2 {
		t.Fatalf("the body ran %d times, want 2", tr.count("body"))
	}
	ps := prompts(t, reg)
	if len(ps) != 2 || ps[0].Call != "c1" || ps[1].Call != "c3" {
		t.Fatalf("delivered %+v, want the first and the third call only", ps)
	}
	if s := ps[0].Subject; s == nil || s.Kind != approval.ScopeDirectory || s.Path != w || s.Label != "writes under "+w {
		t.Fatalf("the first prompt's subject %+v, want directory %s labelled with its scope", s, w)
	}

	want := "tool/call,approval/asked,grant/created,approval/decided,tool/result," +
		"tool/call,approval/asked,approval/decided,tool/result," +
		"tool/call,approval/asked,approval/decided,tool/result"
	if got := strings.Join(types(t, h), ","); got != want {
		t.Fatalf("events\n %s\nwant\n %s", got, want)
	}
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var decided []approval.Decided
	for _, ev := range evs {
		if ev.Type() == approval.DecidedType.Name() {
			var d approval.Decided
			if err := json.Unmarshal([]byte(ev.Raw()), &d); err != nil {
				t.Fatal(err)
			}
			decided = append(decided, d)
		}
		if ev.Type() == "tool/result" && strings.Contains(ev.Raw(), `"approval`) && !strings.Contains(ev.Raw(), `rejected`) &&
			!strings.Contains(ev.Raw(), id) {
			t.Fatalf("an approved tool/result does not name the grant %s: %s", id, ev.Raw())
		}
	}
	if len(decided) != 3 || decided[1].Outcome != approval.OutcomeAllowedByGrant || decided[1].Detail != approval.DetailAllowedByGrant+":"+id ||
		decided[1].Grant != id {
		t.Fatalf("decisions %+v, want the second allowed-by-grant:%s", decided, id)
	}
}

// An Ask with no subject offers no session choice: the prompt's subject is nil,
// an allowed-session answer creates no grant, and the next call is asked again.
// An application subject reaches the prompt as its two ids; one that is not
// valid is no subject.
//
//dsh:test DH-T-HUMAN-5
func TestAskSubjectReachesThePromptOnlyWhenNamed(t *testing.T) {
	for _, tc := range []struct {
		name string
		dec  tools.Decision
		want *approval.Subject
	}{
		{"none", tools.Ask("writes", ""), nil},
		{"application", tools.AskFor("drives", "", tools.Subject{Kind: tools.SubjectApplication, Name: "macos:com.example.app", Label: "the app"}),
			&approval.Subject{Kind: approval.ScopeApplication, Provider: "macos", App: "com.example.app", Label: "the app"}},
		{"invalid", tools.AskFor("drives", "", tools.Subject{Kind: tools.SubjectApplication, Name: "no-provider"}), nil},
		{"unknown kind", tools.AskFor("browses", "", tools.Subject{Kind: "website", Name: "https://example.com"}), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			tr := &trace{}
			grants := approval.NewGrants()
			reg := &countingRegistry{answer: func(int) human.Answer { return human.Answer{Outcome: human.OutcomeAllowedSession} }}
			svc := e.pipeline(t, core.Config{Approval: approval.New(approval.Config{Registry: reg, Grants: grants})})
			e.register(t, e.root, writeTool(tr))
			dec := tc.dec
			if _, err := svc.Decide(e.root, "ask", func(context.Context, tools.Call) tools.Decision { return dec }); err != nil {
				t.Fatal(err)
			}
			calls := [][2]string{pathCall("/w/a"), pathCall("/w/b")}
			h := openSession(t, memstore.New(), calls...)
			res := execute(t, svc, view(t, svc), h, calls...)
			ps := prompts(t, reg)
			if tc.want == nil {
				if len(ps) != 2 || ps[0].Subject != nil || ps[1].Subject != nil || len(grants.List(me)) != 0 {
					t.Fatalf("prompts %+v, grants %+v; want both asked with no subject and no grant", ps, grants.List(me))
				}
				for _, r := range res {
					if r.IsError || r.Approval != string(approval.OutcomeAllowedOnce) || r.Grant != "" {
						t.Fatalf("result %+v", r)
					}
				}
				return
			}
			if len(ps) == 0 || ps[0].Subject == nil || *ps[0].Subject != *tc.want {
				t.Fatalf("prompts %+v, want the subject %+v", ps, tc.want)
			}
		})
	}
}
