package builtin_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/replay"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/title"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// bootTitles composes session.title and session.title.llm from the build
// catalogue through the loader, over a session service, its view registry and
// an llm runtime whose replay route plays reply. llmDisabled is the SDK
// profile's entry (sdk.yml disables session.title.llm).
func bootTitles(t *testing.T, reply string, llmDisabled bool) (*title.Service, session.Handle) {
	t.Helper()
	ctx := context.Background()
	cat, err := builtin.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	m, err := vocab.RestoreMessage("t1", vocab.RoleAssistant, nil, vocab.TextBlock(reply))
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(map[string]any{"message": m, "request_digest": "<digest>"})
	line, _ := json.Marshal(map[string]any{"type": "assistant/message", "data": string(data)})
	if err := os.WriteFile(filepath.Join(dir, replay.SessionFile), []byte(strings.Join([]string{`{"dsh_export":1}`, string(line)}, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := testkit.Tree(t)
	reg := views.New()
	svc := session.NewService(root, memstore.New(), session.Config{Folder: reg})
	rt := llm.New(llm.WithInterception(root))
	if _, err := views.Key.Provide(root, reg); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Key.Provide(root, svc); err != nil {
		t.Fatal(err)
	}
	if _, err := llm.RuntimeKey.Provide(root, rt); err != nil {
		t.Fatal(err)
	}
	if err := mustFiber(t)(root.Use(replay.Component, replay.Config{Scenario: dir, Route: "replay", Requests: t.TempDir()})); err != nil {
		t.Fatal(err)
	}
	if err := mustFiber(t)(root.Use(loader.Component, loader.Options{Resolver: cat})); err != nil {
		t.Fatal(err)
	}
	ldr := get(t, root, loader.Key)
	if err := ldr.Apply([]loader.EntryOptions{
		{ID: title.Name, Name: title.Name},
		{ID: "session.title.llm", Name: "session.title.llm", Disabled: llmDisabled},
	}); err != nil {
		t.Fatal(err)
	}
	s := get(t, root, title.Key)
	h, err := svc.Create(ctx, nil, session.CreateOptions{Scope: scope.Key{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(ctx) })
	user, err := vocab.RestoreMessage("u1", vocab.RoleUser, nil, vocab.TextBlock("fix the flaky build"))
	if err != nil {
		t.Fatal(err)
	}
	hdr, _ := event.RequestHeader.Draft(event.RequestHeaderPayload{Route: "replay"})
	msg, _ := event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
	if _, err := h.AppendGroup(ctx, []event.Draft{hdr, msg}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.Flush(ctx); err != nil {
		t.Fatal(err)
	}
	return s, h
}

func mustFiber(t *testing.T) func(*cordis.Fiber, error) error {
	return func(f *cordis.Fiber, err error) error {
		t.Helper()
		if err != nil {
			return err
		}
		return f.Wait()
	}
}

// With both catalogue entries a session shows its fallback, and a Refresh
// generates through the configured route.
func TestTitlesComposeFromTheCatalogue(t *testing.T) {
	ctx := context.Background()
	s, h := bootTitles(t, "Flaky build fix", false)
	if got, err := s.Get(ctx, h.ID()); err != nil || got.Source != title.SourceFallback || got.Text != "fix the flaky build" {
		t.Fatalf("title before generation = %+v, %v", got, err)
	}
	if got, err := s.Refresh(ctx, h.ID()); err != nil || got.Text != "Flaky build fix" || got.Source != title.SourceGenerated {
		t.Fatalf("Refresh = %+v, %v", got, err)
	}
}

// The SDK profile disables session.title.llm: no title is generated, and the
// fallback and Rename still work.
func TestSDKDisablingTheTitleModelKeepsTheFallback(t *testing.T) {
	ctx := context.Background()
	s, h := bootTitles(t, "Flaky build fix", true)
	if _, err := s.Refresh(ctx, h.ID()); herr.Kind(err) != title.CodeUnavailable {
		t.Fatalf("Refresh without session.title.llm: %v, want %s", err, title.CodeUnavailable)
	}
	if got, err := s.Get(ctx, h.ID()); err != nil || got.Source != title.SourceFallback {
		t.Fatalf("title = %+v, %v; want the fallback", got, err)
	}
	if got, err := s.Rename(ctx, h.ID(), "Mine"); err != nil || got.Text != "Mine" || !got.Pinned {
		t.Fatalf("Rename = %+v, %v", got, err)
	}
}
