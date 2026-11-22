package surface_test

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

var update = flag.Bool("update", false, "rewrite golden files")

// logb builds a log by folding drafts, as a live session would.
type logb struct {
	t    testing.TB
	st   *surface.State
	recs []event.Record
	now  int64
}

func newLog(t testing.TB) *logb { return &logb{t: t, st: surface.NewState(nil), now: 1000} }

// try stamps and folds d; on error the state is unchanged.
func (l *logb) try(d event.Draft, err error) (event.Seq, error) {
	l.t.Helper()
	if err != nil {
		l.t.Fatalf("draft: %v", err)
	}
	l.now += 10
	rec, err := d.Stamp(l.st.Len(), l.now)
	if err != nil {
		return 0, err
	}
	if err := l.st.Fold(rec, d.Reserved()); err != nil {
		return 0, err
	}
	l.recs = append(l.recs, rec)
	return rec.Seq, nil
}

func (l *logb) add(d event.Draft, err error) event.Seq {
	l.t.Helper()
	seq, err := l.try(d, err)
	if err != nil {
		l.t.Fatalf("fold: %v", err)
	}
	return seq
}

func msg(t testing.TB, id string, role vocab.Role, blocks ...vocab.Block) vocab.Message {
	t.Helper()
	m, err := vocab.RestoreMessage(id, role, nil, blocks...)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func text(s string) vocab.Block { return vocab.TextBlock(s) }

func system(t testing.TB, id, s string) (event.Draft, error) {
	return event.SystemMessage.Draft(event.Append(), event.MessagePayload{Message: msg(t, id, vocab.RoleSystem, text(s))})
}

func user(t testing.TB, id, s string) (event.Draft, error) {
	return event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: msg(t, id, vocab.RoleUser, text(s))})
}

func userReplace(t testing.TB, id, s string, start, end event.Seq, sources ...event.Seq) (event.Draft, error) {
	return event.UserMessage.Draft(event.Replace(start, end), event.UserMessagePayload{Message: msg(t, id, vocab.RoleUser, text(s))}, sources...)
}

func assistant(t testing.TB, id string, blocks ...vocab.Block) (event.Draft, error) {
	return event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: msg(t, id, vocab.RoleAssistant, blocks...)})
}

func call(id, name string) vocab.Block {
	return vocab.ToolUseBlock(id, name, json.RawMessage(`{"q":1}`))
}

func result(t testing.TB, id, callID, s string) (event.Draft, error) {
	return event.ToolResult.Draft(event.Append(), event.ToolResultPayload{CallID: callID,
		Message: msg(t, id, vocab.RoleTool, vocab.ToolResultBlock(callID, false, vocab.TextPart(s)))})
}

func toolCall(id, name string) (event.Draft, error) {
	return event.ToolCall.Draft(event.ToolCallPayload{CallID: id, Name: name, Arguments: `{"q":1}`})
}

func schema(name, desc string) vocab.ToolSchema {
	return vocab.ToolSchema{Name: name, Description: desc, Parameters: json.RawMessage(`{"type":"object"}`)}
}

func header(route string, tools ...vocab.ToolSchema) (event.Draft, error) {
	return event.RequestHeader.Draft(event.RequestHeaderPayload{Route: vocab.RouteID(route), Tools: tools})
}

func toolUpdate(t testing.TB, id string, u surface.ToolUpdate) (event.Draft, error) {
	data, _ := json.Marshal(u)
	m, err := vocab.RestoreMessage(id, vocab.RoleDeveloper, &vocab.Source{Kind: surface.ToolUpdateKind, Producers: []string{"tools"}, Data: data},
		text("tools changed"))
	if err != nil {
		t.Fatal(err)
	}
	return event.DeveloperMessage.Draft(event.Append(), event.MessagePayload{Message: m})
}

func turnStart(n int) (event.Draft, error) {
	return event.TurnStartType.Draft(event.TurnStart{Turn: n})
}
func turnEnd(n int, kind string) (event.Draft, error) {
	return event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: event.Reason{Kind: kind}})
}
func stepStart(turn, step int) (event.Draft, error) {
	return event.StepStartType.Draft(event.StepStart{Turn: turn, Step: step})
}
func stepEnd(turn, step int) (event.Draft, error) {
	return event.StepEndType.Draft(event.StepEnd{Turn: turn, Step: step})
}

func historyJSON(t testing.TB, h surface.History) string {
	t.Helper()
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if string(want) != got+"\n" {
		t.Fatalf("%s:\n got %s\nwant %s", name, got, want)
	}
}

// DH-T-SESSION-10: a log gives the expected history JSON; empty system,
// developer and assistant messages produce no node; a snapshot is unchanged by
// later appends and replacements, and unchanged nodes keep their identity.
//
//dsh:test DH-T-SESSION-10
func TestHistoryGolden(t *testing.T) {
	l := newLog(t)
	l.add(system(t, "m-sys", "you are terse"))
	l.add(event.DeveloperMessage.Draft(event.Append(), event.MessagePayload{Message: msg(t, "m-dev-empty", vocab.RoleDeveloper, text("  "))}))
	l.add(header("openai/gpt", schema("grep", "search")))
	l.add(turnStart(1))
	u1 := l.add(user(t, "m-u1", "find foo"))
	l.add(stepStart(1, 1))
	l.add(event.AssistantMessage.Draft(event.Append(), event.AssistantMessagePayload{Message: msg(t, "m-a-empty", vocab.RoleAssistant)}))
	a1 := l.add(assistant(t, "m-a1", text("looking"), call("c1", "grep")))
	l.add(toolCall("c1", "grep"))
	r1 := l.add(result(t, "m-r1", "c1", "foo.go:1"))
	l.add(stepEnd(1, 1))
	l.add(turnEnd(1, event.ReasonCompleted))

	before := l.st.History()
	beforeJSON := historyJSON(t, before)
	golden(t, "history.golden", beforeJSON)
	if before.Len() != 4 {
		t.Fatalf("history has %d nodes, want 4 (the empty developer and assistant messages produce none)", before.Len())
	}

	// A replacement of the user turn and a later append leave the snapshot as it was.
	l.add(userReplace(t, "m-sum", "summary of turn 1", u1, r1, u1, a1, r1))
	l.add(user(t, "m-u2", "next"))
	if got := historyJSON(t, before); got != beforeJSON {
		t.Fatalf("snapshot changed:\n%s\n%s", got, beforeJSON)
	}
	after := l.st.History()
	golden(t, "history-replaced.golden", historyJSON(t, after))
	if after.At(0) != before.At(0) {
		t.Fatalf("the unchanged system node lost its identity: %+v vs %+v", after.At(0), before.At(0))
	}
	m, err := after.At(1).Message()
	if err != nil || m.ID() != "m-sum" {
		t.Fatalf("node 1 is %v %v, want the summary", m.ID(), err)
	}
}

// DH-T-SESSION-9: each replacement rule of §3.2 accepts and rejects; a refused
// record changes nothing.
//
//dsh:test DH-T-SESSION-9
func TestReplacementRules(t *testing.T) {
	setup := func(t *testing.T) (*logb, map[string]event.Seq) {
		l := newLog(t)
		s := map[string]event.Seq{}
		s["sys"] = l.add(system(t, "m-sys", "sys"))
		l.add(turnStart(1))
		s["u1"] = l.add(user(t, "m-u1", "one"))
		l.add(stepStart(1, 1))
		s["a1"] = l.add(assistant(t, "m-a1", call("c1", "grep")))
		s["r1"] = l.add(result(t, "m-r1", "c1", "out"))
		l.add(stepEnd(1, 1))
		l.add(turnEnd(1, event.ReasonCompleted))
		s["u2"] = l.add(user(t, "m-u2", "two"))
		return l, s
	}
	cases := []struct {
		name  string
		draft func(t *testing.T, s map[string]event.Seq) (event.Draft, error)
		code  event.Code
	}{
		{"covering replace", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return userReplace(t, "x", "sum", s["u1"], s["r1"], s["u1"], s["a1"], s["r1"])
		}, ""},
		{"sources do not cover", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return userReplace(t, "x", "sum", s["u1"], s["r1"], s["u1"], s["r1"])
		}, event.CodeInvalidSurface},
		{"no sources", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return userReplace(t, "x", "sum", s["u1"], s["u1"])
		}, event.CodeInvalidSurface},
		{"range is not current nodes", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return userReplace(t, "x", "sum", s["u1"]+1, s["r1"], s["u1"]+1, s["a1"], s["r1"])
		}, event.CodeInvalidSurface},
		{"system head replaced by user", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return userReplace(t, "x", "sum", s["sys"], s["u1"], s["sys"], s["u1"])
		}, event.CodeInvalidSurface},
		{"system head replaced by system", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return event.SystemMessage.Draft(event.Replace(s["sys"], s["sys"]), event.MessagePayload{Message: msg(t, "x", vocab.RoleSystem, text("new"))}, s["sys"])
		}, ""},
		{"system over more than the head", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return event.SystemMessage.Draft(event.Replace(s["sys"], s["u1"]), event.MessagePayload{Message: msg(t, "x", vocab.RoleSystem, text("new"))}, s["sys"], s["u1"])
		}, event.CodeInvalidSurface},
		{"tool result content rewrite", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return event.ToolResult.Draft(event.Replace(s["r1"], s["r1"]), event.ToolResultPayload{CallID: "c1",
				Message: msg(t, "x", vocab.RoleTool, vocab.ToolResultBlock("c1", false, vocab.TextPart("short")))}, s["r1"])
		}, ""},
		{"tool result answers another call", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return event.ToolResult.Draft(event.Replace(s["r1"], s["r1"]), event.ToolResultPayload{CallID: "c9",
				Message: msg(t, "x", vocab.RoleTool, vocab.ToolResultBlock("c9", false, vocab.TextPart("short")))}, s["r1"])
		}, event.CodeInvalidSurface},
		{"tool result over a non-result", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return event.ToolResult.Draft(event.Replace(s["u2"], s["u2"]), event.ToolResultPayload{CallID: "c1",
				Message: msg(t, "x", vocab.RoleTool, vocab.ToolResultBlock("c1", false, vocab.TextPart("short")))}, s["u2"])
		}, event.CodeInvalidSurface},
		{"replacement after shadowing", func(t *testing.T, s map[string]event.Seq) (event.Draft, error) {
			return userReplace(t, "x", "sum", s["u2"], s["u2"], s["u2"])
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, s := setup(t)
			before, _ := l.st.Checkpoint()
			_, err := l.try(c.draft(t, s))
			if c.code == "" {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if !errors.Is(err, &event.Error{Code: c.code}) {
				t.Fatalf("err = %v, want %s", err, c.code)
			}
			if after, _ := l.st.Checkpoint(); after != before {
				t.Fatalf("a refused record changed the folds")
			}
		})
	}
}
