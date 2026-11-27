package replay_test

import (
	"context"
	"encoding/json"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/replay"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// request is the next request of a one-message session on route.
func request(t *testing.T, route vocab.RouteID) session.Request {
	t.Helper()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	h := must(session.NewService(root, memstore.New(), session.Config{}).Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	user := must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("hello")))
	must(h.AppendGroup(bg, []event.Draft{
		must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})),
		must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})),
		must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: route})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})),
	}))
	return must(h.NextRequest())
}

// reason is err's innermost herr reason.
func reason(err error) herr.Reason {
	r, _ := herr.ReasonOf(err)
	return r
}

// line is an export event line of type typ whose data is payload.
func line(typ string, payload any) string {
	return string(must(json.Marshal(map[string]any{"type": typ, "data": string(must(json.Marshal(payload)))})))
}

func text(id, s string) vocab.Message {
	return must(vocab.RestoreMessage(id, vocab.RoleAssistant, nil, vocab.TextBlock(s)))
}

// script is an export of the turns: a text reply answering digest, a tool call,
// and a failed attempt.
func script(digest string) string {
	tool := must(vocab.RestoreMessage("m2", vocab.RoleAssistant, nil, vocab.ToolUseBlock("c1", "write", json.RawMessage(`{}`))))
	failure := must(vocab.NewFailure(vocab.CodeInvalidRequest, "refused"))
	return strings.Join([]string{
		`{"dsh_export":1}`,
		line("user/message", map[string]any{}),
		line("assistant/message", map[string]any{"message": text("m1", "hi"), "request_digest": digest, "usage": vocab.Usage{Input: 3, Output: 1}}),
		line("assistant/message", map[string]any{"message": tool, "request_digest": "<digest:2>"}),
		line("assistant/attempt", map[string]any{"failure": failure, "request_digest": "<digest:3>"}),
	}, "\n") + "\n"
}

func TestLoadReadsTheScript(t *testing.T) {
	turns := must(replay.Load(strings.NewReader(script("sha256:x"))))
	if len(turns) != 3 || turns[0].Digest != "sha256:x" || turns[0].Message.Blocks()[0].Text.Text != "hi" || turns[0].Usage.Input != 3 ||
		turns[1].Message.Blocks()[0].ToolUse == nil || turns[2].Failure == nil {
		t.Fatalf("turns = %+v", turns)
	}
	for name, tc := range map[string]struct{ in, err string }{
		"empty":     {"", "the log is empty"},
		"header":    {`{"type":"x"}`, "not an export header"},
		"bad line":  {"{\"dsh_export\":1}\nnot json", "line 2"},
		"bad data":  {"{\"dsh_export\":1}\n" + `{"type":"assistant/message","data":"nope"}`, "line 2: assistant/message"},
		"no result": {"{\"dsh_export\":1}\n" + line("assistant/attempt", map[string]any{}), "neither a message nor a failure"},
	} {
		if _, err := replay.Load(strings.NewReader(tc.in)); err == nil || !strings.Contains(err.Error(), tc.err) {
			t.Errorf("%s: want %q, got %v", name, tc.err, err)
		}
	}
}

func collect(seq iter.Seq2[vocab.Chunk, error]) (chunks []vocab.Chunk) {
	for c, err := range seq {
		if err != nil {
			panic(err)
		}
		chunks = append(chunks, c)
	}
	return chunks
}

func TestTurnStreamOpensFillsAndClosesEachBlock(t *testing.T) {
	turns := must(replay.Load(strings.NewReader(script("sha256:x"))))
	got := collect(turns[0].Stream())
	if len(got) != 5 || got[1].(vocab.TextDelta).Text != "hi" || got[3].(vocab.UsageChunk).Usage.Output != 1 || got[4].(vocab.Finish).Reason != vocab.FinishStop {
		t.Errorf("text turn: %#v", got)
	}
	got = collect(turns[1].Stream())
	if len(got) != 3 || got[2].(vocab.Finish).Reason != vocab.FinishToolCalls {
		t.Errorf("tool turn: %#v", got)
	}
	got = collect(turns[2].Stream())
	if f := got[len(got)-1].(vocab.Finish); len(got) != 1 || f.Reason != vocab.FinishError || f.Failure == nil {
		t.Errorf("failed turn: %#v", got)
	}
	// A consumer that stops early stops the stream.
	for _, turn := range turns {
		for range turn.Stream() {
			break
		}
	}
}

func listen(p *replay.Player, req session.Request) (llm.StreamDecision, error) {
	return p.Listen(nil, llm.StreamRequest{Route: "replay", Request: req}, nil)
}

func TestPlayerServesInOrderAndChecksRealDigests(t *testing.T) {
	req := request(t, "replay")
	dir := t.TempDir()
	turns := must(replay.Load(strings.NewReader(script(req.Digest()))))
	p := replay.NewPlayer(turns, replay.ModeReplay, dir)
	for i := range 3 {
		d, err := listen(p, req)
		if s, ok := d.(llm.Serve); err != nil || !ok || s.ServedBy != replay.ServedBy {
			t.Fatalf("attempt %d: %#v, %v", i, d, err)
		}
	}
	if _, err := listen(p, req); reason(err) != replay.Unscripted {
		t.Errorf("a fourth attempt: %v, want %s", err, replay.Unscripted)
	}
	if err := p.Unconsumed(); err != nil {
		t.Error(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "002.json"))
	if err != nil || string(b) != req.Canonical()+"\n" {
		t.Errorf("request 002: %q, %v", b, err)
	}

	mismatch := replay.NewPlayer(must(replay.Load(strings.NewReader(script("sha256:other")))), replay.ModeReplay, "")
	if _, err := listen(mismatch, req); reason(err) != replay.RequestMismatch {
		t.Errorf("a different request: %v, want %s", err, replay.RequestMismatch)
	}
	if err := mismatch.Unconsumed(); reason(err) != replay.Unconsumed {
		t.Errorf("unplayed turns: %v, want %s", err, replay.Unconsumed)
	}
	refresh := replay.NewPlayer(must(replay.Load(strings.NewReader(script("sha256:other")))), replay.ModeRefresh, "")
	if _, err := listen(refresh, req); err != nil {
		t.Errorf("refresh checks no digest: %v", err)
	}
	unwritable := replay.NewPlayer(turns, replay.ModeReplay, filepath.Join(dir, "none"))
	if _, err := listen(unwritable, req); err == nil || !strings.Contains(err.Error(), "writing request 0") {
		t.Errorf("an unwritable requests directory: %v", err)
	}
}

// mount provides an intercepting runtime and mounts llm.replay with cfg.
func mount(t *testing.T, cfg replay.Config) (*llm.Runtime, *cordis.Fiber, error) {
	t.Helper()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	rt := llm.New(llm.WithInterception(root))
	must(0, must(root.Apply("runtime", func(c *cordis.Context) error {
		_, err := llm.RuntimeKey.Provide(c, rt)
		return err
	})).Wait())
	fiber := must(root.Use(replay.Component, cfg))
	return rt, fiber, fiber.Wait()
}

func TestComponentServesTheStubRoute(t *testing.T) {
	dir := t.TempDir()
	req := request(t, "replay")
	if err := os.WriteFile(filepath.Join(dir, replay.SessionFile), []byte(script(req.Digest())), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, fiber, err := mount(t, replay.Config{Scenario: dir, Route: "replay"})
	if err != nil {
		t.Fatal(err)
	}
	var recs []llm.Attempt
	var reply strings.Builder
	for c := range must(rt.Prepare(bg, "replay", "replay", vocab.Controls{})).Stream(bg, req, llm.AttemptSinkFunc(func(a llm.Attempt) error { recs = append(recs, a); return nil })) {
		if d, ok := c.(vocab.TextDelta); ok {
			reply.WriteString(d.Text)
		}
	}
	if reply.String() != "hi" || len(recs) != 1 || recs[0].ServedBy != replay.ServedBy {
		t.Errorf("reply %q, records %+v", reply.String(), recs)
	}
	fiber.Dispose()
}

func TestComponentRefusesABadConfig(t *testing.T) {
	dir := t.TempDir()
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, replay.SessionFile), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		cfg replay.Config
		err string
	}{
		"no scenario": {replay.Config{}, "names no scenario"},
		"mode":        {replay.Config{Scenario: dir, Mode: "record"}, "not supported"},
		"no session":  {replay.Config{Scenario: dir}, replay.SessionFile},
		"bad session": {replay.Config{Scenario: bad}, "line 1"},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := mount(t, tc.cfg)
			if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("want %q, got %v", tc.err, err)
			}
		})
	}
}

func TestStubRouteRefusesEveryAttempt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, replay.SessionFile), []byte(`{"dsh_export":1}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt, _, err := mount(t, replay.Config{Scenario: dir, Route: "replay", Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	// The script is empty, so the replay fails the attempt before the stub; the
	// stub itself, reached directly, refuses.
	var failed bool
	for c := range must(rt.Prepare(bg, "replay", "m", vocab.Controls{})).Stream(bg, request(t, "replay"), llm.AttemptSinkFunc(func(llm.Attempt) error { return nil })) {
		if f, ok := c.(vocab.Finish); ok && f.Reason == vocab.FinishError {
			failed = true
		}
	}
	if !failed {
		t.Error("an unscripted attempt did not fail")
	}
}
