package surface_test

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

func names(ts []vocab.ToolSchema) []string {
	var out []string
	for _, t := range ts {
		out = append(out, t.Name)
	}
	return out
}

// DH-T-SESSION-11: the header fold returns the latest snapshot; the tool history
// resolves an addition through the header it cites, restarts on a series start
// or a changed definition, and continues when a removed name returns unchanged.
//
//dsh:test DH-T-SESSION-11
func TestHeaderAndToolHistory(t *testing.T) {
	l := newLog(t)
	if _, ok := l.st.RequestHeader(); ok {
		t.Fatal("a header before any request/header")
	}
	h1 := l.add(header("r/a", schema("grep", "search")))
	h2 := l.add(header("r/b", schema("grep", "search"), schema("edit", "edit")))
	if h, ok := l.st.RequestHeader(); !ok || h.Seq != h2 || h.Route != "r/b" || !slices.Equal(names(h.Tools), []string{"grep", "edit"}) {
		t.Fatalf("header = %+v, want the latest", h)
	}
	th := l.st.ToolHistory()
	if th.Base != h1 || !slices.Equal(names(th.Declared), []string{"grep"}) {
		t.Fatalf("series base = %d %v, want %d [grep]", th.Base, names(th.Declared), h1)
	}

	// A name added twice in one update is refused and changes nothing.
	before, _ := l.st.Checkpoint()
	if _, err := l.try(toolUpdate(t, "m-dup", surface.ToolUpdate{Header: h2, Added: []string{"edit", "edit"}})); !errors.Is(err, event.ErrInvalidSurface) {
		t.Fatalf("a duplicate addition: err = %v", err)
	}
	if after, _ := l.st.Checkpoint(); after != before || l.st.Len() != h2+1 || len(l.st.ToolHistory().Active()) != 1 {
		t.Fatal("a refused duplicate addition changed the folds")
	}

	add := l.add(toolUpdate(t, "m-add", surface.ToolUpdate{Header: h2, Added: []string{"edit"}}))
	th = l.st.ToolHistory()
	if len(th.Changes) != 1 || th.Changes[0].Seq != add || th.Changes[0].Header != h2 || th.Changes[0].Added[0].Description != "edit" {
		t.Fatalf("changes = %+v", th.Changes)
	}
	if got := names(th.Active()); !slices.Equal(got, []string{"grep", "edit"}) {
		t.Fatalf("active = %v", got)
	}

	// Refusals: a header outside the series, a name the header does not define,
	// a removal of an inactive name.
	for _, u := range []surface.ToolUpdate{{Header: h2 + 100, Added: []string{"edit"}}, {Header: h2, Added: []string{"nope"}}, {Header: h2, Removed: []string{"nope"}}, {Header: h2, Removed: []string{"edit", "edit"}}} {
		if _, err := l.try(toolUpdate(t, "m-bad", u)); !errors.Is(err, event.ErrInvalidSurface) {
			t.Fatalf("update %+v: err = %v", u, err)
		}
	}

	l.add(toolUpdate(t, "m-rm", surface.ToolUpdate{Header: h2, Removed: []string{"edit"}}))
	// A removed name returns unchanged: the series continues.
	h3 := l.add(header("r/b", schema("grep", "search"), schema("edit", "edit")))
	l.add(toolUpdate(t, "m-readd", surface.ToolUpdate{Header: h3, Added: []string{"edit"}}))
	if th = l.st.ToolHistory(); th.Base != h1 || len(th.Changes) != 3 {
		t.Fatalf("series restarted on an unchanged definition: %+v", th)
	}

	// A changed definition restarts the series.
	h4 := l.add(header("r/b", schema("grep", "search v2")))
	if th = l.st.ToolHistory(); th.Base != h4 || len(th.Changes) != 0 || th.Declared[0].Description != "search v2" {
		t.Fatalf("changed definition did not restart: %+v", th)
	}
	if _, err := l.try(toolUpdate(t, "m-old", surface.ToolUpdate{Header: h2, Added: []string{"edit"}})); !errors.Is(err, event.ErrInvalidSurface) {
		t.Fatalf("an update citing the old series: %v", err)
	}

	// A surface replacement starts a new series at the next header.
	u := l.add(user(t, "m-u", "x"))
	l.add(userReplace(t, "m-u'", "y", u, u, u))
	h5 := l.add(header("r/b", schema("grep", "search v2")))
	if th = l.st.ToolHistory(); th.Base != h5 {
		t.Fatalf("a replacement did not start a series: base %d, want %d", th.Base, h5)
	}
}

// DH-T-SESSION-13: a tool/result whose message does not answer the call its
// payload names is refused, on append and on replacement, and changes nothing.
//
//dsh:test DH-T-SESSION-13
func TestToolResultAnswersItsCall(t *testing.T) {
	resultOf := func(op event.Op, call string, blocks []vocab.Block, sources ...event.Seq) func(t *testing.T) (event.Draft, error) {
		return func(t *testing.T) (event.Draft, error) {
			return event.ToolResult.Draft(op, event.ToolResultPayload{CallID: call, Message: msg(t, "x", vocab.RoleTool, blocks...)}, sources...)
		}
	}
	block := func(id string) vocab.Block { return vocab.ToolResultBlock(id, false, vocab.TextPart("out")) }
	setup := func(t *testing.T) (*logb, event.Seq) {
		l := newLog(t)
		l.add(turnStart(1))
		l.add(stepStart(1, 1))
		l.add(assistant(t, "a", call("c1", "grep"), call("c2", "grep")))
		return l, l.add(result(t, "r1", "c1", "first"))
	}
	cases := []struct {
		name  string
		draft func(r1 event.Seq) func(t *testing.T) (event.Draft, error)
	}{
		{"append: block answers another call", func(event.Seq) func(*testing.T) (event.Draft, error) {
			return resultOf(event.Append(), "c2", []vocab.Block{block("c9")})
		}},
		{"append: block answers a pending call the payload does not name", func(event.Seq) func(*testing.T) (event.Draft, error) {
			return resultOf(event.Append(), "c2", []vocab.Block{block("c1")})
		}},
		{"append: two tool_result blocks", func(event.Seq) func(*testing.T) (event.Draft, error) {
			return resultOf(event.Append(), "c2", []vocab.Block{block("c2"), block("c2")})
		}},
		{"replace: block changes the call", func(r1 event.Seq) func(*testing.T) (event.Draft, error) {
			return resultOf(event.Replace(r1, r1), "c1", []vocab.Block{block("c2")}, r1)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l, r1 := setup(t)
			before, _ := l.st.Checkpoint()
			n := l.st.Len()
			if _, err := l.try(c.draft(r1)(t)); !errors.Is(err, event.ErrInvalidSurface) {
				t.Fatalf("err = %v, want %s", err, event.CodeInvalidSurface)
			}
			if after, _ := l.st.Checkpoint(); after != before || l.st.Len() != n || len(l.recs) != int(n) {
				t.Fatal("a refused tool/result changed the log or the folds")
			}
			if calls := l.st.Open().Calls; len(calls) != 1 || calls[0].ID != "c2" {
				t.Fatalf("pending calls = %+v, want [c2]", calls)
			}
		})
	}
	l, _ := setup(t)
	l.add(result(t, "r2", "c2", "second"))
	if l.st.Open().Calls != nil && len(l.st.Open().Calls) != 0 {
		t.Fatalf("a matching result left calls pending: %+v", l.st.Open().Calls)
	}
}

// DH-T-SESSION-13: bracket refusals, the SP-3 rule and the exact closers for a
// table of interruption points.
//
//dsh:test DH-T-SESSION-13
func TestBracketsAndClosers(t *testing.T) {
	t.Run("refusals", func(t *testing.T) {
		l := newLog(t)
		if _, err := l.try(stepStart(1, 1)); !errors.Is(err, surface.ErrUnbalanced) {
			t.Fatalf("step outside a turn: %v", err)
		}
		l.add(turnStart(1))
		if _, err := l.try(turnStart(2)); !errors.Is(err, surface.ErrUnbalanced) {
			t.Fatalf("turn inside a turn: %v", err)
		}
		l.add(stepStart(1, 1))
		before, _ := l.st.Checkpoint()
		if _, err := l.try(turnEnd(1, event.ReasonCompleted)); !errors.Is(err, surface.ErrUnbalanced) {
			t.Fatalf("turn/end with an open step: %v", err)
		}
		if after, _ := l.st.Checkpoint(); after != before {
			t.Fatal("a refused turn/end changed the folds")
		}
		l.add(assistant(t, "m-a", call("c1", "grep")))
		if _, err := l.try(stepEnd(1, 1)); !errors.Is(err, surface.ErrUnbalanced) {
			t.Fatalf("step/end with an unanswered call: %v", err)
		}
		if _, err := l.try(result(t, "m-r", "c9", "x")); !errors.Is(err, surface.ErrUnbalanced) {
			t.Fatalf("a result for no call: %v", err)
		}
		if _, err := l.try(turnEnd(1, event.ReasonInterrupted)); !errors.Is(err, surface.ErrUnbalanced) {
			t.Fatalf("an interrupted turn end from outside the core: %v", err)
		}
		l.add(result(t, "m-r", "c1", "x"))
		l.add(stepEnd(1, 1))
		if _, err := l.try(stepStart(1, 1)); !errors.Is(err, surface.ErrUnbalanced) {
			t.Fatalf("a repeated step number: %v", err)
		}
		l.add(turnEnd(1, event.ReasonCompleted))
		if _, err := l.try(turnStart(1)); !errors.Is(err, surface.ErrUnbalanced) {
			t.Fatalf("a turn number not above the last: %v", err)
		}
		// Log-only events outside a turn stay legal.
		l.add(header("r/a"))
	})

	type point struct {
		name  string
		setup func(l *logb)
		want  []string // type and, for results, code
	}
	points := []point{
		{"in a turn", func(l *logb) { l.add(turnStart(1)) }, []string{"turn/end"}},
		{"in a step", func(l *logb) { l.add(turnStart(1)); l.add(stepStart(1, 1)) }, []string{"step/end", "turn/end"}},
		{"call not started", func(l *logb) {
			l.add(turnStart(1))
			l.add(stepStart(1, 1))
			l.add(assistant(t, "m-a", call("c1", "grep"), call("c2", "edit")))
		}, []string{"tool/result " + surface.CodeToolNotStarted, "tool/result " + surface.CodeToolNotStarted, "step/end", "turn/end"}},
		{"call running", func(l *logb) {
			l.add(turnStart(1))
			l.add(stepStart(1, 1))
			l.add(assistant(t, "m-a", call("c1", "grep"), call("c2", "edit")))
			l.add(result(t, "m-r1", "c1", "ok"))
			l.add(toolCall("c2", "edit"))
		}, []string{"tool/result " + surface.CodeToolOutcomeUnknown, "step/end", "turn/end"}},
		{"between turns", func(l *logb) {
			l.add(turnStart(1))
			l.add(turnEnd(1, event.ReasonCompleted))
		}, nil},
	}
	for _, p := range points {
		t.Run(p.name, func(t *testing.T) {
			l := newLog(t)
			p.setup(l)
			at := l.st.Len()
			ds, err := surface.Closers(l.st.Open(), surface.CauseInterrupted, at)
			if err != nil {
				t.Fatal(err)
			}
			again, _ := surface.Closers(l.st.Open(), surface.CauseInterrupted, at)
			var got []string
			last := l.st.LastTime()
			for i, d := range ds {
				if d.Raw() != again[i].Raw() {
					t.Fatalf("closer %d is not deterministic", i)
				}
				rec, err := d.Stamp(l.st.Len(), last)
				if err != nil {
					t.Fatal(err)
				}
				if err := l.st.Fold(rec, true); err != nil {
					t.Fatalf("closer %d %s refused: %v", i, d.Type(), err)
				}
				desc := d.Type()
				if d.Type() == event.ToolResult.Name() {
					v, _ := event.ToolResult.Decode(rec)
					m := v.Message
					if !v.IsError || m.ID() != "msg_closer_"+itoa(at)+"_"+itoa(event.Seq(i)) {
						t.Fatalf("closer result %+v", v)
					}
					src, _ := m.Source()
					var data struct{ Code string }
					_ = json.Unmarshal(src.Data, &data)
					desc += " " + data.Code
				}
				if d.Type() == event.TurnEndType.Name() {
					v, _ := event.TurnEndType.Decode(rec)
					if v.Reason.Kind != event.ReasonInterrupted || v.Reason.Cause != string(surface.CauseInterrupted) {
						t.Fatalf("turn end reason %+v", v.Reason)
					}
				}
				got = append(got, desc)
			}
			if !slices.Equal(got, p.want) {
				t.Fatalf("closers = %v, want %v", got, p.want)
			}
			if l.st.Open().IsOpen() {
				t.Fatalf("still open after closers: %+v", l.st.Open())
			}
		})
	}
}

func itoa(s event.Seq) string { return string(must(json.Marshal(s))) }

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// genLog appends a random valid log: turns of steps with tool calls, headers,
// tool updates and compactions.
func genLog(t testing.TB, r *rand.Rand, turns int) *logb {
	l := newLog(t)
	l.add(system(t, "m-sys", "sys"))
	tools := []vocab.ToolSchema{schema("grep", "search"), schema("edit", "edit")}
	hdr := l.add(header("r/a", tools...))
	id := 0
	next := func() string { id++; return "m" + string(must(json.Marshal(id))) }
	active := map[string]bool{"grep": true, "edit": true}
	for turn := 1; turn <= turns; turn++ {
		l.add(turnStart(turn))
		l.add(user(t, next(), "ask"))
		for step := 1; step <= 1+r.IntN(3); step++ {
			l.add(stepStart(turn, step))
			var calls []vocab.Block
			for c := 0; c < r.IntN(3); c++ {
				calls = append(calls, call(next(), "grep"))
			}
			l.add(assistant(t, next(), append([]vocab.Block{text("thinking")}, calls...)...))
			for _, c := range calls {
				if r.IntN(2) == 0 {
					l.add(toolCall(c.ToolUse.ID, "grep"))
				}
				l.add(result(t, next(), c.ToolUse.ID, "out"))
			}
			l.add(stepEnd(turn, step))
		}
		l.add(turnEnd(turn, event.ReasonCompleted))
		switch r.IntN(4) {
		case 0: // compact everything after the system prompt
			seqs := l.st.History().Seqs()
			if len(seqs) > 2 {
				l.add(userReplace(t, next(), "summary", seqs[1], seqs[len(seqs)-1], seqs[1:]...))
			}
			hdr = l.add(header("r/a", tools...))
			active["edit"] = true
		case 1: // toggle a tool
			if active["edit"] {
				l.add(toolUpdate(t, next(), surface.ToolUpdate{Header: hdr, Removed: []string{"edit"}}))
			} else {
				l.add(toolUpdate(t, next(), surface.ToolUpdate{Header: hdr, Added: []string{"edit"}}))
			}
			active["edit"] = !active["edit"]
		case 2:
			l.add(event.RequestContext.Draft(event.RequestContextPayload{Key: "k", Value: "v"}))
		}
	}
	return l
}

type folds struct {
	History  string
	Header   surface.RequestHeader
	HasHead  bool
	Tools    surface.ToolHistory
	Open     surface.Brackets
	Len      event.Seq
	LastTime int64
}

func foldsOf(t testing.TB, s *surface.State) folds {
	h, ok := s.RequestHeader()
	return folds{historyJSON(t, s.History()), h, ok, s.ToolHistory(), s.Open(), s.Len(), s.LastTime()}
}

func readSeqs(recs []event.Record) surface.ReadSeqsFunc {
	return func(_ context.Context, seqs []event.Seq) ([]event.Record, error) {
		out := make([]event.Record, len(seqs))
		for i, s := range seqs {
			out[i] = recs[s]
		}
		return out, nil
	}
}

// DH-T-SESSION-9 and -14: for generated valid logs the incremental fold equals a
// fold from storage, and a checkpoint at any position plus the suffix equals
// full replay.
//
//dsh:test DH-T-SESSION-9, DH-T-SESSION-14
func TestIncrementalEqualsReplay(t *testing.T) {
	for seed := uint64(1); seed <= 25; seed++ {
		r := rand.New(rand.NewPCG(seed, seed))
		l := genLog(t, r, 2+r.IntN(6))
		want := foldsOf(t, l.st)
		replayed, err := surface.Replay(nil, l.recs)
		if err != nil {
			t.Fatalf("seed %d: replay: %v", seed, err)
		}
		if got := foldsOf(t, replayed); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: replay differs:\n%+v\n%+v", seed, got, want)
		}
		cut := r.IntN(len(l.recs))
		prefix, _ := surface.Replay(nil, l.recs[:cut+1])
		state, err := prefix.Checkpoint()
		if err != nil {
			t.Fatal(err)
		}
		restored, err := surface.Restore(context.Background(), nil, surface.CheckpointVersion, state, readSeqs(l.recs))
		if err != nil {
			t.Fatalf("seed %d: restore at %d: %v", seed, cut, err)
		}
		for _, rec := range l.recs[cut+1:] {
			if err := restored.Fold(rec, true); err != nil {
				t.Fatalf("seed %d: suffix at %d: %v", seed, rec.Seq, err)
			}
		}
		if got := foldsOf(t, restored); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d: checkpoint at %d plus suffix differs:\n%+v\n%+v", seed, cut, got, want)
		}
	}
}

// DH-T-SESSION-14: a checkpoint of another version is refused, so the caller
// discards it and replays.
//
//dsh:test DH-T-SESSION-14
func TestCheckpointOtherVersion(t *testing.T) {
	l := genLog(t, rand.New(rand.NewPCG(7, 7)), 2)
	state, _ := l.st.Checkpoint()
	if _, err := surface.Restore(context.Background(), nil, surface.CheckpointVersion+1, state, readSeqs(l.recs)); !errors.Is(err, surface.ErrCheckpoint) {
		t.Fatalf("err = %v, want %v", err, surface.ErrCheckpoint)
	}
}

// DH-T-SESSION-9: the planner never panics and never changes state on error.
//
//dsh:test DH-T-SESSION-9
func FuzzPlan(f *testing.F) {
	f.Add("user/message", 1, uint8(1), uint64(0), uint64(0), `{"message":{"id":"m","role":"user","blocks":[{"kind":"text","text":{"text":"x"}}]}}`, false, uint64(0))
	f.Add("tool/result", 2, uint8(2), uint64(2), uint64(3), `{"call_id":"c1","message":{"id":"m","role":"tool","blocks":[]}}`, false, uint64(2))
	f.Add("turn/end", 1, uint8(0), uint64(0), uint64(0), `{"turn":1,"reason":{"kind":"interrupted"}}`, true, uint64(0))
	f.Add("plugin:x/y", 0, uint8(0), uint64(0), uint64(0), `{}`, true, uint64(0))
	f.Fuzz(func(t *testing.T, typ string, version int, op uint8, start, end uint64, payload string, byCore bool, source uint64) {
		l := genLog(t, rand.New(rand.NewPCG(3, 3)), 2)
		before := foldsOf(t, l.st)
		cp, _ := l.st.Checkpoint()
		rec := event.Record{Seq: l.st.Len(), Time: l.st.LastTime(), Type: typ, Version: version, Op: event.OpKind(op),
			Start: event.Seq(start), End: event.Seq(end), Payload: payload, Ignorable: typ == "" || version == 0}
		if source > 0 {
			rec.Sources = []event.Seq{event.Seq(source)}
		}
		if _, err := l.st.Plan(rec, byCore); err != nil {
			if got := foldsOf(t, l.st); !reflect.DeepEqual(got, before) {
				t.Fatal("a refused plan changed the folds")
			}
			if got, _ := l.st.Checkpoint(); got != cp {
				t.Fatal("a refused plan changed the checkpoint")
			}
		}
		_ = l.st.Fold(rec, byCore)
	})
}
