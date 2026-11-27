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
)

var bg = context.Background()

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// mount is a runtime with llm.replay's stub route playing one reply (none
// when reply is ""), and llm.title-model over it.
func mount(t *testing.T, reply string) (*cordis.Context, string) {
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
	for _, f := range []*cordis.Fiber{must(root.Use(replay.Component, replay.Config{Scenario: dir, Route: "replay", Requests: reqs})), must(root.Use(titlemodel.Component, cordis.None{}))} {
		if err := f.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	return root, reqs
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
	root, _ := mount(t, "Hello")
	m := must(title.ModelKey.Get(root))
	a := must(m.Attempt(bg, request(t)))
	if a.Message == nil || a.Message.Blocks()[0].Text.Text != "Hello" || a.Finish.Failure != nil || a.ServedBy != replay.ServedBy {
		t.Errorf("attempt = %+v", a)
	}
}

// A failed attempt is returned with its failure and never tried again.
func TestAFailedAttemptIsNotRetried(t *testing.T) {
	root, reqs := mount(t, "")
	a, err := must(title.ModelKey.Get(root)).Attempt(bg, request(t))
	if err != nil || a.Finish.Failure == nil || a.Message != nil {
		t.Fatalf("attempt = %+v, %v", a, err)
	}
	if sent := must(os.ReadDir(reqs)); len(sent) != 1 {
		t.Errorf("%d attempts were sent", len(sent))
	}
}
