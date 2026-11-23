package session_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// Plan 17.1.3, scenario "Old headers still replay" (SQLite): a log whose
// request/header was written with version 1 reopens and folds; the header in
// force decodes with an empty fingerprint and manifest, and the request rebuilt
// from it has the same digest as the same log with a version-2 header that names
// an assembly: neither field is part of a request.
//
//dsh:test DH-T-SESSION-11
func TestOldHeadersStillReplay(t *testing.T) {
	ctx := context.Background()
	// Each log is the same session id in its own database: the request digest
	// covers the session id.
	dir := t.TempDir()
	stores := map[int]*sqlite.Store{}
	for _, v := range []int{1, 2} {
		st, err := sqlite.Open(ctx, filepath.Join(dir, fmt.Sprintf("v%d.db", v)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = st.Close(ctx) })
		stores[v] = st
	}
	const id = "s-header"
	tool := vocab.ToolSchema{Name: "echo", Description: "Echoes.", Parameters: json.RawMessage(`{"type":"object"}`)}
	user, err := vocab.RestoreMessage("m-1", vocab.RoleUser, nil, vocab.TextBlock("hello"))
	if err != nil {
		t.Fatal(err)
	}
	write := func(hp event.RequestHeaderPayload, version int) {
		t.Helper()
		hd, err := event.RequestHeader.Draft(hp)
		if err != nil {
			t.Fatal(err)
		}
		ud, err := event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: user})
		if err != nil {
			t.Fatal(err)
		}
		var recs []event.Record
		for i, d := range []event.Draft{hd, ud} {
			r, err := d.Stamp(event.Seq(i), int64(1_735_689_600_000+i))
			if err != nil {
				t.Fatal(err)
			}
			recs = append(recs, r)
		}
		recs[0].Version = version
		w, err := stores[version].Create(ctx, store.Header{ID: id}, store.CreateOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Append(ctx, store.Batch{First: 0, Records: recs}); err != nil {
			t.Fatal(err)
		}
		if err := w.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}
	write(event.RequestHeaderPayload{Route: "r/a", Tools: []vocab.ToolSchema{tool}}, 1)
	write(event.RequestHeaderPayload{Route: "r/a", Tools: []vocab.ToolSchema{tool}, Fingerprint: "sha256:ab",
		Manifest: []event.ManifestEntry{{Name: "identity", Owner: "tools.prompt"}}}, 2)

	root := newRoot(t)
	digest := func(version int) (surface.RequestHeader, string) {
		t.Helper()
		st := stores[version]
		svc := session.NewService(root, st, session.Config{})
		h, _, err := svc.Open(ctx, nil, session.ID(id), session.OpenOptions{})
		if err != nil {
			t.Fatalf("reopen v%d: %v", version, err)
		}
		defer func() { _ = h.Close(ctx) }()
		hdr, ok := h.RequestHeader()
		if !ok {
			t.Fatalf("v%d: no header in force", version)
		}
		r, err := st.OpenRead(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = r.Close(ctx) }()
		b, err := surface.Rebuild(ctx, event.Default(), func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
			return r.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
		}, id, 1)
		if err != nil {
			t.Fatalf("rebuild v%d: %v", version, err)
		}
		return hdr, b.Digest()
	}
	h1, d1 := digest(1)
	h2, d2 := digest(2)
	if h1.Fingerprint != "" || h1.Manifest != nil || h1.Route != "r/a" || len(h1.Tools) != 1 {
		t.Fatalf("the v1 header folded to %+v", h1)
	}
	if h2.Fingerprint != "sha256:ab" || len(h2.Manifest) != 1 || h2.Manifest[0].Owner != "tools.prompt" {
		t.Fatalf("the v2 header folded to %+v", h2)
	}
	if d1 == "" || d1 != d2 {
		t.Fatalf("rebuilt digests differ: v1 %s, v2 %s", d1, d2)
	}
}
