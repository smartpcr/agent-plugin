package driver_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"reflect"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/agent/driver"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/surface"
)

// recorder is the driver's model runtime with every request it streams kept:
// the requests actually sent, as the transport's caller handed them over.
type recorder struct {
	driver.Models
	mu   sync.Mutex
	sent []session.Request
}

func (r *recorder) Prepare(ctx context.Context, rt vocab.RouteID, model string, c vocab.Controls) (llm.Prepared, error) {
	p, err := r.Models.Prepare(ctx, rt, model, c)
	if err != nil {
		return nil, err
	}
	return recorded{Prepared: p, r: r}, nil
}

func (r *recorder) requests() []session.Request {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]session.Request(nil), r.sent...)
}

type recorded struct {
	llm.Prepared
	r *recorder
}

func (p recorded) Stream(ctx context.Context, req session.Request, sink llm.AttemptSink) iter.Seq[vocab.Chunk] {
	p.r.mu.Lock()
	p.r.sent = append(p.r.sent, req)
	p.r.mu.Unlock()
	return p.Prepared.Stream(ctx, req, sink)
}

// replayed is the helper of DH-T-AGENT-8 (agent-loop.md §9.1), run after each
// driver test: the log replayed into a fresh session gives the same history,
// position and brackets, and every request rebuilt from the log at its
// requestAsOf equals, byte for byte, the one the driver sent, which the log's
// settlement of that attempt names.
func (r *rig) replayed() {
	t := r.t
	t.Helper()
	id := r.sess.ID()
	rs, release, err := session.NewService(r.root, r.store, session.Config{}).OpenRead(bg, id)
	if err != nil {
		t.Fatalf("replay: open the log read-only: %v", err)
	}
	defer release()
	want, got := must(json.Marshal(r.sess.History())), must(json.Marshal(rs.History()))
	if !bytes.Equal(want, got) {
		t.Fatalf("replay: the replayed history differs\nlive:   %s\nreplay: %s", want, got)
	}
	if rs.Len() != r.sess.Len() || !reflect.DeepEqual(rs.Open(), r.sess.Open()) {
		t.Fatalf("replay: len %d brackets %+v, live len %d brackets %+v", rs.Len(), rs.Open(), r.sess.Len(), r.sess.Open())
	}

	rd, err := r.store.OpenRead(bg, string(id))
	if err != nil {
		t.Fatalf("replay: open the store: %v", err)
	}
	defer func() { _ = rd.Close(bg) }()
	read := func(ctx context.Context, from event.Offset, limit int) ([]event.Record, error) {
		return rd.Read(ctx, from, limit, store.ReadOptions{WithAux: true})
	}
	// The settlements of the log: requestAsOf@requestDigest of each attempt.
	logged := map[string]int{}
	for _, e := range must(r.sess.Read(bg, 0, 0)) {
		var p struct {
			AsOf   *event.Seq `json:"request_as_of"`
			Digest string     `json:"request_digest"`
		}
		switch e.Type() {
		case "assistant/message", "assistant/attempt":
			if err := json.Unmarshal([]byte(e.Raw()), &p); err != nil {
				t.Fatalf("replay: decode %s: %v", e.Type(), err)
			}
			if p.AsOf != nil && p.Digest != "" {
				logged[fmt.Sprintf("%d@%s", *p.AsOf, p.Digest)]++
			}
		}
	}
	sent := r.sent.requests()
	for i, req := range sent {
		b, err := surface.Rebuild(bg, event.Default(), read, string(id), req.AsOf())
		if err != nil {
			t.Fatalf("replay: rebuild request %d at %d: %v", i+1, req.AsOf(), err)
		}
		if b.Digest() != req.Digest() || b.Canonical() != req.Canonical() {
			t.Fatalf("replay: request %d rebuilt at %d differs from the one sent\nsent:    %s\nrebuilt: %s", i+1, req.AsOf(), req.Canonical(), b.Canonical())
		}
		k := fmt.Sprintf("%d@%s", req.AsOf(), req.Digest())
		if logged[k] == 0 {
			t.Fatalf("replay: request %d (%s) has no settlement in the log: %v", i+1, k, logged)
		}
		logged[k]--
	}
	for k, n := range logged {
		if n != 0 {
			t.Fatalf("replay: %d settlement(s) %s name a request never sent", n, k)
		}
	}
}
