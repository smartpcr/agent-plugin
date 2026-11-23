package request_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/context/request"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
	fakemodel "github.com/smartpcr/agent-plugin/src/harness/internal/testkit/model"
)

// world is what the contributors observe: a file, a clock, a shell and the
// session lookup. Once down, every read fails the test and returns an error.
type world struct {
	t     *testing.T
	turn  atomic.Int32
	down  atomic.Bool
	reads atomic.Int32
}

func (w *world) read(what string) error {
	w.reads.Add(1)
	if w.down.Load() {
		w.t.Errorf("the %s was read during the rebuild", what)
		return fmt.Errorf("the %s is forbidden during the rebuild", what)
	}
	return nil
}

// file is AGENTS.md: it changes before the third turn.
func (w *world) file() (string, error) {
	if err := w.read("file"); err != nil {
		return "", err
	}
	if w.turn.Load() >= 3 {
		return "Build with make; test with make test-race.", nil
	}
	return "Build with make; test with make test.", nil
}

// clock is the time, one hour per turn.
func (w *world) clock() (string, error) {
	if err := w.read("clock"); err != nil {
		return "", err
	}
	return fmt.Sprintf("2026-11-23T%02d:00:00Z", 8+w.turn.Load()), nil
}

// shell is the tmux pane: the user moves to window 1 before the third turn.
func (w *world) shell() (string, error) {
	if err := w.read("shell"); err != nil {
		return "", err
	}
	if w.turn.Load() >= 3 {
		return "main:1.0", nil
	}
	return "main:0.0", nil
}

// lastData is the source data of the last visible message of kind, "" for none.
func lastData(in request.Input, kind string) (string, error) {
	msgs, err := in.Visible.Messages()
	if err != nil {
		return "", err
	}
	last := ""
	for _, m := range msgs {
		if src, ok := m.Source(); ok && src.Kind == kind {
			last = string(src.Data)
		}
	}
	return last, nil
}

// observer is a contributor of kind that observes read and enters it when it
// differs from its visible state.
func observer(kind string, required bool, read func() (string, error)) *fake {
	return &fake{name: kind, required: required, run: func(in request.Input) (request.Output, error) {
		v, err := read()
		if err != nil {
			return request.Output{}, err
		}
		data := map[string]string{"observed": v}
		raw, _ := json.Marshal(data)
		if last, err := lastData(in, kind); err != nil || last == string(raw) {
			return request.Output{}, err
		}
		m, err := request.Message(kind, "test."+kind, kind+": "+v, data)
		return request.Output{Messages: []vocab.Message{m}}, err
	}}
}

// DH-T-CONTEXT-1 (17.3.3, the Stage 17.3 scenario "A recorded log replays
// without observing"): three turns run with contributors that read a file, a
// clock and a shell, through the session lookup; the instruction file and the
// pane change before the third turn, the clock every turn. Then every
// contributor, file, clock, shell and session read fails, and every request is
// rebuilt from the recorded log alone: each rebuilt digest equals the digest
// its assistant/message recorded and the digest of the request sent.
//
//dsh:test DH-T-CONTEXT-1
func TestRecordedLogRebuildsEveryRequest(t *testing.T) {
	w := &world{t: t}
	r := newRigWith(t, func(o *request.Options) {
		live := o.Sessions
		o.Sessions = func(a agent.Ref) (request.Log, error) {
			if err := w.read("session"); err != nil {
				return nil, err
			}
			return live(a)
		}
	}, fakemodel.Reply("one"), fakemodel.Reply("two"), fakemodel.Reply("three"))
	contributors := []*fake{
		observer(request.KindClock, false, w.clock),
		observer(request.KindTmux, false, w.shell),
		observer(request.KindInstructions, true, w.file),
	}
	r.register(contributors...)
	for i, text := range []string{"first", "second", "third"} {
		w.turn.Store(int32(i + 1))
		r.turn(text)
	}
	if len(r.skipped()) != 0 {
		t.Fatalf("contributors were skipped: %+v", r.skipped())
	}
	// Turn 1 enters all three; turn 2 only the clock; turn 3 all three again.
	want := []string{
		"user", request.KindInstructions, request.KindClock, request.KindTmux,
		"user", request.KindClock,
		"user", request.KindInstructions, request.KindClock, request.KindTmux,
	}
	if got := r.kinds(); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("source kinds %q, want %q", got, want)
	}
	sent := r.sent.requests()
	if len(sent) != 3 {
		t.Fatalf("%d requests sent, want 3", len(sent))
	}

	// The recorded log, frozen: the rebuild reads nothing else.
	rd := must(r.st.OpenRead(bg, string(r.sess.ID())))
	recs := must(rd.Read(bg, 0, 0, store.ReadOptions{WithAux: true}))
	must(0, rd.Close(bg))
	read := func(_ context.Context, from event.Offset, limit int) ([]event.Record, error) {
		if from >= event.Offset(len(recs)) {
			return nil, nil
		}
		end := min(len(recs), int(from)+limit)
		return recs[from:end], nil
	}

	w.down.Store(true)
	for _, c := range contributors {
		c.forbid.Store(true)
	}
	before := w.reads.Load()
	var n int
	for _, rec := range recs {
		if rec.Type != event.AssistantMessage.Name() {
			continue
		}
		var p event.AssistantMessagePayload
		must(0, json.Unmarshal([]byte(rec.Payload), &p))
		if p.RequestAsOf == nil || p.RequestDigest == "" {
			t.Fatalf("assistant/message at %d records no request", rec.Seq)
		}
		b := must(surface.Rebuild(bg, event.Default(), read, string(r.sess.ID()), *p.RequestAsOf))
		if b.Digest() != p.RequestDigest {
			t.Fatalf("request %d: rebuilt digest %s, recorded %s", n+1, b.Digest(), p.RequestDigest)
		}
		if n >= len(sent) || sent[n].Digest() != p.RequestDigest || sent[n].Canonical() != b.Canonical() {
			t.Fatalf("request %d: the recorded or rebuilt request differs from the one sent", n+1)
		}
		n++
	}
	if n != 3 {
		t.Fatalf("%d recorded requests rebuilt, want 3", n)
	}
	if got := w.reads.Load() - before; got != 0 {
		t.Fatalf("the rebuild made %d reads of the world", got)
	}
}
