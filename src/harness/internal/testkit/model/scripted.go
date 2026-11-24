package model

import (
	"context"
	"fmt"
	"iter"
	"sync"
	"testing"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// Script is one scripted attempt of a Scripted adapter, at the level of the
// chunk vocabulary: the chunks the attempt yields, in order, and then how it
// ends. With neither Err nor Block the attempt ends after its chunks, so a
// script without a vocab.Finish is a stream that closed early.
type Script struct {
	// Chunks are yielded first, in order.
	Chunks []vocab.Chunk
	// Err is yielded after Chunks and ends the attempt. A *vocab.Failure is a
	// failure the provider classified; any other error is a transport failure.
	Err error
	// Block holds the attempt open after Chunks until its context ends, then
	// yields the context's cause: the shape of a provider that stops sending.
	Block bool
}

// Chunks is a script that yields cs and ends.
func Chunks(cs ...vocab.Chunk) Script { return Script{Chunks: cs} }

// Fails is a script that yields before, then fails with f (a classified
// failure before or after the first chunk).
func Fails(f *vocab.Failure, before ...vocab.Chunk) Script { return Script{Chunks: before, Err: f} }

// Blocks is a script that yields before, then blocks until it is cancelled.
func Blocks(before ...vocab.Chunk) Script { return Script{Chunks: before, Block: true} }

// Answer is a complete text reply in one block: start, one delta, end, usage
// and a stop.
func Answer(text string) Script {
	return Chunks(
		vocab.BlockStart{Index: 0, Kind: vocab.KindText},
		vocab.TextDelta{Index: 0, Text: text},
		vocab.BlockEnd{Index: 0, Block: vocab.TextBlock(text)},
		vocab.UsageChunk{Usage: vocab.Usage{Input: 1, Output: 1}},
		vocab.Finish{Reason: vocab.FinishStop},
	)
}

// Attempted is one attempt a Scripted adapter was asked for: the call it was
// prepared for and the request it was sent.
type Attempted struct {
	Route   vocab.RouteID
	Model   string
	Config  vocab.CallConfig
	Request session.Request
}

// Scripted is the kit's chunk-level scripted model (util-test-support.md §6.1,
// "model"): an llm.Adapter whose attempts play Scripts in order, with no wire in
// between, so a test above the llm runtime scripts exactly the chunks a driver
// would produce. It records every attempt. An attempt with no script left fails
// the test and ends with a transport error; a script still unplayed when the test
// ends fails it too, so the script always matches the calls made. Prepare does
// no I/O and consumes nothing, as the adapter contract requires.
//
// It is safe for concurrent attempts; each takes the next script in arrival
// order. A test that needs per-caller scripts registers one Scripted per route.
type Scripted struct {
	t        testing.TB
	mu       sync.Mutex
	scripts  []Script
	attempts []Attempted
	// arrived is closed and replaced each time an attempt is recorded, waking
	// every WaitAttempts call to recheck the count.
	arrived chan struct{}
}

// NewScripted is a scripted adapter playing scripts, checked when the test ends.
func NewScripted(t testing.TB, scripts ...Script) *Scripted {
	t.Helper()
	s := &Scripted{t: t, scripts: scripts, arrived: make(chan struct{})}
	t.Cleanup(func() {
		s.mu.Lock()
		n := len(s.scripts)
		s.mu.Unlock()
		if n != 0 {
			t.Errorf("model: %d scripted attempt(s) never made", n)
		}
	})
	return s
}

// Script appends scripts.
func (s *Scripted) Script(scripts ...Script) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts = append(s.scripts, scripts...)
}

// Attempts is every attempt made so far, in arrival order.
func (s *Scripted) Attempts() []Attempted {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Attempted(nil), s.attempts...)
}

// WaitAttempts blocks until at least n attempts have been made and returns nil,
// or returns ctx's cause when ctx ends first. It is woken by each attempt; it
// does not poll, so inside a synctest bubble it is durably blocked.
func (s *Scripted) WaitAttempts(ctx context.Context, n int) error {
	for {
		s.mu.Lock()
		got, arrived := len(s.attempts), s.arrived
		s.mu.Unlock()
		if got >= n {
			return nil
		}
		select {
		case <-arrived:
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
}

// Prepare is the call for route and model; it does no I/O.
func (s *Scripted) Prepare(_ context.Context, route vocab.RouteID, model string, cfg vocab.CallConfig) (llm.Call, error) {
	return &scriptedCall{s: s, route: route, model: model, cfg: cfg}, nil
}

type scriptedCall struct {
	s     *Scripted
	route vocab.RouteID
	model string
	cfg   vocab.CallConfig
}

// next records an attempt and takes the next script.
func (s *Scripted) next(a Attempted) (Script, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, a)
	close(s.arrived)
	s.arrived = make(chan struct{})
	if len(s.scripts) == 0 {
		return Script{}, false
	}
	sc := s.scripts[0]
	s.scripts = s.scripts[1:]
	return sc, true
}

// Attempt plays the next script.
func (c *scriptedCall) Attempt(ctx context.Context, req session.Request) iter.Seq2[vocab.Chunk, error] {
	return func(yield func(vocab.Chunk, error) bool) {
		sc, ok := c.s.next(Attempted{Route: c.route, Model: c.model, Config: c.cfg, Request: req})
		if !ok {
			c.s.t.Errorf("model: unscripted attempt on route %q, model %q", c.route, c.model)
			yield(nil, fmt.Errorf("model: no scripted attempt left for route %q", c.route))
			return
		}
		for _, ch := range sc.Chunks {
			if !yield(ch, nil) {
				return
			}
		}
		switch {
		case sc.Err != nil:
			yield(nil, sc.Err)
		case sc.Block:
			<-ctx.Done()
			yield(nil, context.Cause(ctx))
		}
	}
}

// RegisterScripted registers route id on rt, served by s, and releases the
// registration when the test ends. The route lists the one model ModelName.
func RegisterScripted(t testing.TB, rt *llm.Runtime, id vocab.RouteID, s *Scripted) {
	t.Helper()
	RegisterRoute(t, rt, llm.Route{ID: id, Adapter: s})
}

// RegisterRoute registers r on rt, released when the test ends: for a scripted
// route that needs more than RegisterScripted gives it (a concurrency cap, a
// retry policy, defaults). A route with no models lists ModelName.
func RegisterRoute(t testing.TB, rt *llm.Runtime, r llm.Route) {
	t.Helper()
	if len(r.Models) == 0 {
		r.Models = []llm.Model{{Name: ModelName}}
	}
	reg, err := rt.Register("testkit", r)
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	t.Cleanup(reg.Close)
}
