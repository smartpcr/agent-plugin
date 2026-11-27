package title_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/replay"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/titlemodel"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/title"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// DH-T-VIEW-10: control and direction characters are removed, truncation never
// splits a character, and the fallback keeps its word and byte limits.
func TestNormalizeAndFallback(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"spaces":    {"  Fix\tthe\n\nlogin  bug ", "Fix the login bug"},
		"controls":  {"a\x00b\x1bc\u202Ed\u2066e\uFEFF", "abcde"},
		"invalid":   {"ok\xffay", "okay"},
		"truncated": {"日本語のタイトル", "日本語"}, // 9 bytes: never half a character
	}
	for name, c := range cases {
		max := 0
		if name == "truncated" {
			max = 10
		}
		if got := title.Normalize(c.in, max); got != c.want {
			t.Errorf("%s: Normalize(%q) = %q, want %q", name, c.in, got, c.want)
		}
	}
	if got := title.Fallback("please fix the login bug in the auth form", 5, 40); got != "please fix the login bug" {
		t.Errorf("Fallback by words = %q", got)
	}
	if got := title.Fallback("supercalifragilistic expialidocious", 5, 24); got != "supercalifragilistic" {
		t.Errorf("Fallback by bytes = %q", got)
	}
	if got := title.Fallback("supercalifragilistic", 5, 9); got != "supercali" {
		t.Errorf("Fallback of one long word = %q", got)
	}
}

// rig is a session service over memstore whose titles generate on llm.replay's
// stub route, playing replies in order.
type rig struct {
	root   *cordis.Context
	svc    session.Service
	titles *title.Service
	reqs   string
	fiber  *cordis.Fiber
	ended  chan struct{}
	// during, when set, runs as the title request is sent.
	during func()
}

type modelFunc func(context.Context, session.Request) (title.Attempt, error)

func (f modelFunc) Attempt(ctx context.Context, req session.Request) (title.Attempt, error) {
	return f(ctx, req)
}

func newRig(t *testing.T, replies ...string) *rig {
	t.Helper()
	return newRigWith(t, nil, replies...)
}

func newRigWith(t *testing.T, opts []title.Option, replies ...string) *rig {
	t.Helper()
	dir, reqs := t.TempDir(), t.TempDir()
	lines := []string{`{"dsh_export":1}`}
	for i, r := range replies {
		m := must(vocab.RestoreMessage("t"+string(rune('1'+i)), vocab.RoleAssistant, nil, vocab.TextBlock(r)))
		data := must(json.Marshal(map[string]any{"message": m, "request_digest": "<digest>", "usage": vocab.Usage{Input: 9, Output: 3}}))
		lines = append(lines, string(must(json.Marshal(map[string]any{"type": "assistant/message", "data": string(data)}))))
	}
	if err := os.WriteFile(filepath.Join(dir, replay.SessionFile), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := cordis.New()
	t.Cleanup(root.Dispose)
	rt := llm.New(llm.WithInterception(root))
	must(0, must(root.Apply("runtime", func(c *cordis.Context) error {
		_, err := llm.RuntimeKey.Provide(c, rt)
		return err
	})).Wait())
	fiber := must(root.Use(replay.Component, replay.Config{Scenario: dir, Route: "replay", Requests: reqs}))
	if err := fiber.Wait(); err != nil {
		t.Fatal(err)
	}
	reg := views.New()
	r := &rig{}
	m := titlemodel.New(rt)
	ts := must(title.New(reg, modelFunc(func(ctx context.Context, req session.Request) (title.Attempt, error) {
		if r.during != nil {
			r.during()
		}
		return m.Attempt(ctx, req)
	}), append([]title.Option{title.WithWarn(t.Logf)}, opts...)...))
	if err := ts.Listen(root); err != nil {
		t.Fatal(err)
	}
	// Registered after the service's listener, so a turn/end seen here has
	// already started any generation it was due.
	ended := make(chan struct{}, 16)
	must(session.Committed.On(root, func(_ *cordis.Context, a session.CommittedArgs) error {
		if a.Event.Type() == event.TurnEndType.Name() {
			ended <- struct{}{}
		}
		return nil
	}))
	r.root, r.svc, r.titles, r.reqs, r.fiber, r.ended = root, session.NewService(root, memstore.New(), session.Config{Folder: reg}), ts, reqs, fiber, ended
	t.Cleanup(ts.Wait)
	return r
}

func (r *rig) create(t *testing.T) session.Session {
	t.Helper()
	return r.createWith(t, session.Header{})
}

func (r *rig) createWith(t *testing.T, hd session.Header) session.Session {
	t.Helper()
	h := must(r.svc.Create(bg, nil, session.CreateOptions{Header: hd, Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	return h
}

// prompt appends the opening of turn n with the user's prompt on route replay.
func prompt(t *testing.T, h session.Session, n int, text string) {
	t.Helper()
	user := must(vocab.RestoreMessage("u"+string(rune('0'+n)), vocab.RoleUser, nil, vocab.TextBlock(text)))
	drafts := []event.Draft{must(event.TurnStartType.Draft(event.TurnStart{Turn: n})), must(event.StepStartType.Draft(event.StepStart{Turn: n, Step: 1}))}
	if n == 1 {
		drafts = append(drafts, must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "replay"})))
	}
	drafts = append(drafts, must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})))
	must(h.AppendGroup(bg, drafts))
}

// end closes turn n and flushes it, which is when a title is due.
func end(t *testing.T, h session.Session, n int) {
	t.Helper()
	must(h.AppendGroup(bg, []event.Draft{
		must(event.StepEndType.Draft(event.StepEnd{Turn: n, Step: 1})),
		must(event.TurnEndType.Draft(event.TurnEnd{Turn: n, Reason: event.Reason{Kind: event.ReasonCompleted}})),
	}))
	must(h.Flush(bg))
}

func (r *rig) get(t *testing.T, h session.Session) title.Title {
	t.Helper()
	return must(r.titles.Get(bg, h.ID()))
}

// settled waits until the last turn/end was delivered and the generation it
// started, if any, has ended, and returns the title.
func (r *rig) settled(t *testing.T, h session.Session) title.Title {
	t.Helper()
	select {
	case <-r.ended:
	case <-time.After(10 * time.Second):
		t.Fatal("turn/end was never delivered")
	}
	r.titles.Wait()
	return r.get(t, h)
}

// titleLog is the title events of h's log, in order.
func titleLog(t *testing.T, h session.Session) []event.Event {
	t.Helper()
	var out []event.Event
	for _, ev := range must(h.Read(bg, 0, int(h.Len()))) {
		if strings.HasPrefix(ev.Type(), "session/title") {
			out = append(out, ev)
		}
	}
	return out
}

// DH-T-VIEW-10, TC-18a: the fallback shows at once, the generated title after
// the first turn, and the log holds request, attempt and title in that order.
func TestFallbackThenGenerated(t *testing.T) {
	r := newRig(t, `"Fix the login bug"`)
	h := r.create(t)
	prompt(t, h, 1, "please fix the login bug in the auth form")
	got := r.get(t, h)
	if got.Text != "please fix the login bug" || got.Source != title.SourceFallback || got.Pinned {
		t.Fatalf("fallback = %+v", got)
	}
	if evs := titleLog(t, h); len(evs) != 0 {
		t.Fatalf("the fallback appended %d events", len(evs))
	}
	end(t, h, 1)
	got = r.settled(t, h)
	if got.Text != "Fix the login bug" || got.Pinned || got.RequestSeq == nil || got.Pending != nil {
		t.Fatalf("generated = %+v", got)
	}
	evs := titleLog(t, h)
	types := make([]string, len(evs))
	for i, ev := range evs {
		types[i] = ev.Type()
	}
	if strings.Join(types, ",") != "session/title-request,session/title-attempt,session/title" {
		t.Fatalf("title events = %v", types)
	}
	a := must(title.AttemptType.As(evs[1]))
	if a.Outcome != title.OutcomeAccepted || a.RequestSeq != evs[0].Seq() || a.ServedBy != replay.ServedBy || a.Usage == nil {
		t.Errorf("attempt = %+v", a)
	}
	if p := must(title.TitleType.As(evs[2])); *p.RequestSeq != evs[0].Seq() || len(p.MessageSeqs) != 1 {
		t.Errorf("title = %+v", p)
	}
	// DH-T-VIEW-11: what was sent is the request rebuilt from the logged event.
	sent := must(os.ReadFile(filepath.Join(r.reqs, "000.json")))
	if want := must(h.RequestFrom(evs[0].Seq())).Canonical() + "\n"; string(sent) != want {
		t.Errorf("sent %s, rebuilt %s", sent, want)
	}
	// A second turn is not due a title under first-prompt.
	prompt(t, h, 2, "and the logout bug")
	end(t, h, 2)
	if again := r.settled(t, h); again.Revision != got.Revision {
		t.Errorf("the second turn retitled: %+v", again)
	}
}

func reason(err error) herr.Reason {
	r, _ := herr.ReasonOf(err)
	return r
}

// DH-T-VIEW-10, TC-18a: a renamed title is pinned; Refresh leaves it until Unpin.
func TestPinnedTitleNotOverwritten(t *testing.T) {
	r := newRig(t, "Login bug fix")
	h := r.create(t)
	prompt(t, h, 1, "please fix the login bug")
	must(h.Flush(bg))
	if err := r.titles.Attach(bg, h); err != nil {
		t.Fatal(err)
	}
	renamed := must(r.titles.Rename(bg, h.ID(), "  My\tsession  "))
	if renamed.Text != "My session" || renamed.Source != title.SourceUser || !renamed.Pinned {
		t.Fatalf("renamed = %+v", renamed)
	}
	if _, err := r.titles.Rename(bg, h.ID(), " \u202E "); reason(err) != title.CodeInvalid {
		t.Errorf("an empty rename: %v", err)
	}
	if _, err := r.titles.Refresh(bg, h.ID()); reason(err) != title.CodePinned {
		t.Fatalf("Refresh of a pinned title: %v", err)
	}
	if got := r.get(t, h); got.Text != renamed.Text || got.Revision != renamed.Revision || !got.Pinned {
		t.Fatalf("after Refresh = %+v", got)
	}
	unpinned := must(r.titles.Unpin(bg, h.ID()))
	if unpinned.Text != "My session" || unpinned.Pinned {
		t.Fatalf("unpinned = %+v", unpinned)
	}
	got := must(r.titles.Refresh(bg, h.ID()))
	if got.Text != "Login bug fix" || got.Source != title.SourceGenerated || got.Pinned {
		t.Fatalf("refreshed = %+v", got)
	}
}

// A reply that is not a title is rejected and the fallback stays.
func TestRejectedReplyKeepsTheFallback(t *testing.T) {
	r := newRig(t, "   ")
	h := r.create(t)
	prompt(t, h, 1, "hello there")
	must(h.Flush(bg))
	if err := r.titles.Attach(bg, h); err != nil {
		t.Fatal(err)
	}
	_, err := r.titles.Refresh(bg, h.ID())
	if reason(err) != title.CodeRejected {
		t.Fatalf("Refresh: %v", err)
	}
	if got := r.get(t, h); got.Source != title.SourceFallback || got.Pending != nil {
		t.Errorf("title = %+v", got)
	}
	evs := titleLog(t, h)
	if len(evs) != 2 || must(title.AttemptType.As(evs[1])).Outcome != title.OutcomeRejected {
		t.Errorf("title events = %d", len(evs))
	}
}

// A request left unsettled by a stopped process is settled interrupted on
// Attach, and a later turn may title the session again.
func TestAttachSettlesAnInterruptedRequest(t *testing.T) {
	r := newRig(t)
	h := r.create(t)
	prompt(t, h, 1, "hello there")
	seqs := []event.Seq{3}
	must(h.Append(bg, must(title.RequestType.Draft(title.RequestPayload{Provider: title.FirstPromptID, Route: "replay", Messages: []string{"hello there"}, MessageSeqs: seqs, Purpose: title.Purpose}))))
	must(h.Flush(bg))
	r.titles.Detach(h.ID())
	if err := r.titles.Attach(bg, h); err != nil {
		t.Fatal(err)
	}
	evs := titleLog(t, h)
	if len(evs) != 2 || must(title.AttemptType.As(evs[1])).Outcome != title.OutcomeInterrupted {
		t.Fatalf("title events = %d", len(evs))
	}
	if got := r.get(t, h); got.Pending != nil {
		t.Errorf("still pending: %+v", got)
	}
}

// A generated title must cite its request and is never pinned.
func TestGeneratedTitleCitesItsRequest(t *testing.T) {
	seq := event.Seq(1)
	for name, p := range map[string]title.TitlePayload{
		"no request": {Title: "x", Source: title.SourceGenerated},
		"pinned":     {Title: "x", Source: title.SourceGenerated, RequestSeq: &seq, Pinned: true},
		"fallback":   {Title: "x", Source: title.SourceFallback},
		"padded":     {Title: " x", Source: title.SourceUser},
	} {
		if _, err := title.TitleType.Draft(p); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// everyPrompt is a provider that retitles after every own prompt, from the
// latest.
type everyPrompt struct{}

func (everyPrompt) ID() string             { return "every" }
func (everyPrompt) Cadence() title.Cadence { return title.EveryPrompt }
func (everyPrompt) Prompt(in title.Input) (title.Prompt, error) {
	return title.Prompt{Messages: in.Prompts[len(in.Prompts)-1:]}, nil
}

// The configured provider's cadence decides when a session is due; a new turn
// with no new prompt is not.
func TestEveryPromptCadence(t *testing.T) {
	r := newRigWith(t, []title.Option{title.WithProvider(everyPrompt{}), title.WithConfig(title.Config{Provider: "every"}),
		title.WithGenerator(title.GeneratorConfig{Route: "replay"})}, "First", "Second")
	h := r.create(t)
	prompt(t, h, 1, "one")
	end(t, h, 1)
	if got := r.settled(t, h); got.Text != "First" {
		t.Fatalf("after turn 1 = %+v", got)
	}
	prompt(t, h, 2, "two")
	end(t, h, 2)
	if got := r.settled(t, h); got.Text != "Second" || got.Requests != 2 {
		t.Fatalf("after turn 2 = %+v", got)
	}
	must(h.AppendGroup(bg, []event.Draft{must(event.TurnStartType.Draft(event.TurnStart{Turn: 3})), must(event.StepStartType.Draft(event.StepStart{Turn: 3, Step: 1}))}))
	end(t, h, 3)
	if got := r.settled(t, h); got.Requests != 2 {
		t.Errorf("a turn with no prompt retitled: %+v", got)
	}
}

// A failed attempt is logged once with its failure; the fallback stays.
func TestFailedAttemptKeepsTheFallback(t *testing.T) {
	r := newRig(t)
	h := r.create(t)
	prompt(t, h, 1, "hello there")
	end(t, h, 1)
	got := r.settled(t, h)
	if got.Source != title.SourceFallback || got.Pending != nil || got.Requests != 1 {
		t.Fatalf("title = %+v", got)
	}
	evs := titleLog(t, h)
	if len(evs) != 2 {
		t.Fatalf("title events = %d", len(evs))
	}
	if a := must(title.AttemptType.As(evs[1])); a.Outcome != title.OutcomeFailed || a.ErrorCode == "" {
		t.Errorf("attempt = %+v", a)
	}
	// First-prompt titles once: the next turn does not try again.
	prompt(t, h, 2, "again")
	end(t, h, 2)
	if got := r.settled(t, h); got.Requests != 1 {
		t.Errorf("retried: %+v", got)
	}
}

func TestOperationsRefuse(t *testing.T) {
	r := newRig(t)
	h := r.create(t)
	if _, err := r.titles.Get(bg, h.ID()); err != nil {
		t.Fatalf("the created session is not attached: %v", err)
	}
	if _, err := r.titles.Refresh(bg, h.ID()); reason(err) != title.CodeNoInput {
		t.Errorf("Refresh with no prompt: %v", err)
	}
	user := must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("no route yet")))
	must(h.Append(bg, must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user}))))
	if _, err := r.titles.Refresh(bg, h.ID()); reason(err) != title.CodeNoRoute {
		t.Errorf("Refresh with no route: %v", err)
	}
	if got := must(r.titles.Unpin(bg, h.ID())); got.Pinned || got.Revision != 0 {
		t.Errorf("Unpin of an unpinned title = %+v", got)
	}
	r.titles.Detach(h.ID())
	if _, err := r.titles.Rename(bg, h.ID(), "x"); reason(err) != title.CodeUnavailable {
		t.Errorf("Rename of a detached session: %v", err)
	}
	if _, err := title.New(views.New(), nil); err == nil {
		t.Error("New with no runtime")
	}
	if _, err := title.New(views.New(), titlemodel.New(llm.New()), title.WithConfig(title.Config{Provider: "nope"})); reason(err) != title.CodeUnknownProvider {
		t.Errorf("New with an unknown provider: %v", err)
	}
}

// DH-T-VIEW-12: a subagent session is never titled automatically.
func TestSubagentIsNeverTitled(t *testing.T) {
	r := newRig(t)
	h := r.createWith(t, session.Header{Origin: "subagent", Creator: "parent"})
	prompt(t, h, 1, "do the sub task")
	end(t, h, 1)
	if got := r.settled(t, h); got.Source != title.SourceFallback || got.Requests != 0 || len(titleLog(t, h)) != 0 {
		t.Errorf("subagent title = %+v", got)
	}
}

// DH-T-VIEW-11, -12: a result that comes back after the title moved (here a
// rename meanwhile) is settled superseded and cannot append a title.
func TestStaleResultCannotAppend(t *testing.T) {
	r := newRig(t, "Generated")
	h := r.create(t)
	prompt(t, h, 1, "please fix the login bug")
	must(h.Flush(bg))
	r.during = func() { must(r.titles.Rename(bg, h.ID(), "Mine")) }
	if _, err := r.titles.Refresh(bg, h.ID()); reason(err) != title.CodeSuperseded {
		t.Fatalf("Refresh: %v", err)
	}
	got := r.get(t, h)
	if got.Text != "Mine" || !got.Pinned || got.Pending != nil {
		t.Errorf("title = %+v", got)
	}
	evs := titleLog(t, h)
	if len(evs) != 3 || must(title.AttemptType.As(evs[2])).Outcome != title.OutcomeSuperseded {
		t.Errorf("title events = %d", len(evs))
	}
}
