package views_test

import (
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
)

// A compaction's summary (a user/message that replaces history) is not a human
// prompt (session-store.md §6.2: "A human prompt updates last_prompt_at";
// DH-D-VIEW-3 orders the list by the last human prompt): the list summary
// counts one prompt and keeps its time, and the store's LastPromptAt and its
// prompt-order key agree with it, in memstore and in sqlite.
func TestCompactionIsNotAPrompt(t *testing.T) {
	for _, c := range []struct {
		name string
		open func(t *testing.T) store.Store
	}{
		{"memstore", func(*testing.T) store.Store { return memstore.New() }},
		{"sqlite", func(t *testing.T) store.Store {
			st := must(sqlite.Open(bg, filepath.Join(t.TempDir(), "dsh.db")))
			t.Cleanup(func() { _ = st.Close(bg) })
			return st
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := c.open(t)
			var now atomic.Int64
			now.Store(1_700_000_000_000)
			reg := views.New()
			b := must(views.RegisterBuiltins(reg))
			svc := session.NewService(cordisRoot(t), st, session.Config{Folder: reg, Clock: func() int64 { return now.Add(1000) }})
			h := must(svc.Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "s"}}))
			defer h.Close(bg)
			p := must(h.Append(bg, must(event.UserMessage.Draft(event.Append(), event.UserMessagePayload{
				Message: bmsg("u1", vocab.RoleUser, vocab.TextBlock("the human prompt"))}))))
			must(h.Append(bg, must(event.UserMessage.Draft(event.Replace(p.Seq, p.Seq), event.UserMessagePayload{
				Message: bmsg("sum", vocab.RoleUser, vocab.TextBlock("Summary: a compaction"))}, p.Seq))))
			if _, err := h.Flush(bg); err != nil {
				t.Fatal(err)
			}

			sum := must(b.ListSummary.Get(must(reg.Read(bg, h))))
			if sum.Prompts != 1 || sum.LastPromptAt != p.Time || sum.Preview != "the human prompt" || sum.Blank {
				t.Fatalf("list summary = %+v, want one prompt at %d", sum, p.Time)
			}
			stat := must(st.Stat(bg, "s"))
			if got := stat.LastPromptAt.UnixMilli(); got != p.Time {
				t.Fatalf("store LastPromptAt = %d, the human prompt was at %d", got, p.Time)
			}
			r := must(st.OpenRead(bg, "s"))
			recs := must(r.Read(bg, 0, 0, store.ReadOptions{}))
			_ = r.Close(bg)
			cut := time.UnixMilli(now.Load() + 10_000).UnixNano()
			if key, ok := store.PromptKey(stat.Header.CreatedAt, recs, cut); !ok || key != time.UnixMilli(p.Time).UnixNano() {
				t.Fatalf("prompt key = %d (%v), want the human prompt's %d", key, ok, time.UnixMilli(p.Time).UnixNano())
			}
			page := must(st.List(bg, store.Query{Order: store.OrderPrompt}))
			if len(page.Summaries) != 1 || page.Summaries[0].LastPromptAt.UnixMilli() != p.Time {
				t.Fatalf("prompt-order page = %+v", page.Summaries)
			}
			if store.IsPrompt(event.UserMessage.Name(), event.OpReplace) || !store.IsPrompt(event.UserMessage.Name(), event.OpAppend) ||
				store.IsPrompt(event.AssistantMessage.Name(), event.OpAppend) {
				t.Fatal("IsPrompt classifies a type or an operation wrongly")
			}
		})
	}
}
