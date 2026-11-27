package titlemodel_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// mount is a runtime with llm.replay's stub route playing one reply (none
// when reply is ""), and the title model over it.
func mount(t *testing.T, reply string) (*cordis.Context, title.Model, string) {
	t.Helper()
	dir, reqs := t.TempDir(), t.TempDir()
	lines := []string{`{"dsh_export":1}`}
	if reply != "" {
		m := must(vocab.RestoreMessage("t1", vocab.RoleAssistant, nil, vocab.TextBlock(reply)))
		data := must(json.Marshal(map[string]any{"message": m, "request_digest": "<digest>"}))
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
	if err := must(root.Use(replay.Component, replay.Config{Scenario: dir, Route: "replay", Requests: reqs})).Wait(); err != nil {
		t.Fatal(err)
	}
	return root, titlemodel.New(rt), reqs
}

// request is a title request on the stub route, rebuilt from its event.
func request(t *testing.T) session.Request {
	t.Helper()
	root := cordis.New()
	t.Cleanup(root.Dispose)
	h := must(session.NewService(root, memstore.New(), session.Config{}).Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	r := must(h.Append(bg, must(title.RequestType.Draft(title.RequestPayload{Provider: title.FirstPromptID, Route: "replay",
		Messages: []string{"hello"}, MessageSeqs: []event.Seq{0}, Purpose: title.Purpose}))))
	return must(h.RequestFrom(r.Seq))
}

func TestAttemptReturnsTheOneRecord(t *testing.T) {
	_, m, _ := mount(t, "Hello")
	a := must(m.Attempt(bg, request(t)))
	if a.Message == nil || a.Message.Blocks()[0].Text.Text != "Hello" || a.Finish.Failure != nil || a.ServedBy != replay.ServedBy {
		t.Errorf("attempt = %+v", a)
	}
}

// A failed attempt is returned with its failure and never tried again.
func TestAFailedAttemptIsNotRetried(t *testing.T) {
	_, m, reqs := mount(t, "")
	a, err := m.Attempt(bg, request(t))
	if err != nil || a.Finish.Failure == nil || a.Message != nil {
		t.Fatalf("attempt = %+v, %v", a, err)
	}
	if sent := must(os.ReadDir(reqs)); len(sent) != 1 {
		t.Errorf("%d attempts were sent", len(sent))
	}
}

// session.title.llm binds the runtime's model into session.title while it is
// loaded: a Refresh generates through the route, and once it unloads the
// service has no model and the title stays as it was.
func TestComponentBindsTheModelWhileLoaded(t *testing.T) {
	root, _, _ := mount(t, "Hello world")
	reg := views.New()
	must(0, must(root.Apply("views", func(c *cordis.Context) error {
		_, err := views.Key.Provide(c, reg)
		return err
	})).Wait())
	if err := must(root.Use(title.Component, title.ComponentConfig{})).Wait(); err != nil {
		t.Fatal(err)
	}
	llmFiber := must(root.Use(titlemodel.Component, cordis.None{}))
	if err := llmFiber.Wait(); err != nil {
		t.Fatal(err)
	}
	s := must(title.Key.Get(root))
	h := must(session.NewService(root, memstore.New(), session.Config{Folder: reg}).Create(bg, nil, session.CreateOptions{Scope: scope.Key{}}))
	t.Cleanup(func() { _ = h.Close(bg) })
	user := must(vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("say hello")))
	must(h.AppendGroup(bg, []event.Draft{
		must(event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "replay"})),
		must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})),
	}))
	must(h.Flush(bg))
	if got, err := s.Refresh(bg, h.ID()); err != nil || got.Text != "Hello world" || got.Source != title.SourceGenerated {
		t.Fatalf("Refresh with the model bound = %+v, %v", got, err)
	}
	llmFiber.Dispose()
	before := must(s.Get(bg, h.ID()))
	if _, err := s.Refresh(bg, h.ID()); herr.Kind(err) != title.CodeUnavailable {
		t.Fatalf("Refresh after session.title.llm unloaded: %v", err)
	}
	if got := must(s.Get(bg, h.ID())); got.Revision != before.Revision || got.Text != "Hello world" {
		t.Errorf("title moved after the unload: %+v", got)
	}
}
