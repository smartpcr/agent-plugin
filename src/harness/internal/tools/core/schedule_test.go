package core_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

type keyIn struct {
	Key string `json:"key" dsh:"required"`
	ID  string `json:"id" dsh:"required"`
}

// activity records, under one lock, the bodies running and what each saw.
type activity struct {
	mu      sync.Mutex
	running map[string]string // id -> key
	max     int
	ended   []string
	overlap map[[2]string]bool // ids seen running at once, in start order
}

func newActivity() *activity {
	return &activity{running: map[string]string{}, overlap: map[[2]string]bool{}}
}

func (a *activity) start(id, key string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for other := range a.running {
		a.overlap[[2]string{other, id}] = true
	}
	a.running[id] = key
	if len(a.running) > a.max {
		a.max = len(a.running)
	}
}

func (a *activity) end(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.running, id)
	a.ended = append(a.ended, id)
}

// keyedTool writes the resource named by its key; body(id) runs inside the
// recorded start and end.
func keyedTool(name string, act *activity, ov func(keyIn) tools.Overlap, body func(id string)) *tools.Declaration {
	return tools.Define(tools.Spec[keyIn, string]{
		Name: name, Description: "Touches a keyed resource.",
		Overlap: ov,
		Run: func(_ context.Context, in keyIn, _ tools.Run) (string, error) {
			act.start(in.ID, in.Key)
			defer act.end(in.ID)
			body(in.ID)
			return in.ID, nil
		},
	})
}

func resultOrder(t *testing.T, h session.Handle) (calls, results []string) {
	t.Helper()
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range evs {
		if p, err := event.ToolCall.As(ev); err == nil {
			calls = append(calls, p.CallID)
		}
		if p, err := event.ToolResult.As(ev); err == nil {
			results = append(results, p.CallID)
		}
	}
	return calls, results
}

// DH-T-AGENT-26: calls 1 and 3 share a resource key and call 2 finishes first;
// with maxParallel=3, calls 1 and 3 never overlap and results commit 1, 2, 3.
func TestConflictingCallsSerializeAndResultsKeepModelOrder(t *testing.T) {
	e := newEnv(t)
	act := newActivity()
	gate := make(chan struct{})
	started := make(chan struct{})
	svc := e.pipeline(t, core.Config{MaxParallel: 3})
	e.register(t, e.root, keyedTool("touch", act,
		func(in keyIn) tools.Overlap { return tools.Writes(in.Key) },
		func(id string) {
			switch id {
			case "1":
				close(started)
				<-gate // held until call 2 has finished
			case "2":
				<-started // call 2 runs while call 1 does
				defer close(gate)
			}
		}))
	calls := [][2]string{
		{"touch", `{"key":"a","id":"1"}`},
		{"touch", `{"key":"b","id":"2"}`},
		{"touch", `{"key":"a","id":"3"}`},
	}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)

	var got []string
	for _, r := range res {
		if r.IsError {
			t.Fatalf("result %+v", r)
		}
		got = append(got, r.CallID)
	}
	if want := []string{"c1", "c2", "c3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("results %v, want %v", got, want)
	}
	if want := []string{"2", "1", "3"}; !reflect.DeepEqual(act.ended, want) {
		t.Fatalf("bodies ended %v, want %v", act.ended, want)
	}
	if act.overlap[[2]string{"1", "3"}] || act.overlap[[2]string{"3", "1"}] {
		t.Fatal("calls 1 and 3 share key a but overlapped")
	}
	if !act.overlap[[2]string{"1", "2"}] && !act.overlap[[2]string{"2", "1"}] {
		t.Fatal("calls 1 and 2 do not conflict but did not overlap")
	}
	lc, lr := resultOrder(t, h)
	if want := []string{"c1", "c2", "c3"}; !reflect.DeepEqual(lc, want) || !reflect.DeepEqual(lr, want) {
		t.Fatalf("logged calls %v results %v, want both %v", lc, lr, want)
	}
}

// maxParallel bounds the bodies of one batch even when none conflict.
func TestBatchNeverExceedsMaxParallel(t *testing.T) {
	e := newEnv(t)
	act := newActivity()
	svc := e.pipeline(t, core.Config{MaxParallel: 2})
	e.register(t, e.root, keyedTool("free", act,
		func(keyIn) tools.Overlap { return tools.Free },
		func(string) { time.Sleep(20 * time.Millisecond) }))
	var calls [][2]string
	for _, id := range []string{"1", "2", "3", "4", "5"} {
		calls = append(calls, [2]string{"free", `{"key":"k","id":"` + id + `"}`})
	}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)
	if len(res) != 5 {
		t.Fatalf("%d results", len(res))
	}
	if act.max != 2 {
		t.Fatalf("at most %d bodies ran at once, want 2", act.max)
	}
	if _, lr := resultOrder(t, h); !reflect.DeepEqual(lr, []string{"c1", "c2", "c3", "c4", "c5"}) {
		t.Fatalf("logged results %v", lr)
	}
}

// A tool without an Overlap, and one the operator forces exclusive, overlaps
// nothing.
func TestExclusiveCallsRunAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		cfg  core.Config
		ov   func(keyIn) tools.Overlap
	}{
		{name: "undeclared", cfg: core.Config{MaxParallel: 4}},
		{name: "forced", cfg: core.Config{MaxParallel: 4, ForceExclusive: []string{"t"}},
			ov: func(keyIn) tools.Overlap { return tools.Free }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			act := newActivity()
			svc := e.pipeline(t, tc.cfg)
			e.register(t, e.root, keyedTool("t", act, tc.ov, func(string) { time.Sleep(5 * time.Millisecond) }))
			calls := [][2]string{{"t", `{"key":"a","id":"1"}`}, {"t", `{"key":"b","id":"2"}`}, {"t", `{"key":"c","id":"3"}`}}
			h := openSession(t, memstore.New(), calls...)
			execute(t, svc, view(t, svc), h, calls...)
			if act.max != 1 {
				t.Fatalf("%d bodies ran at once", act.max)
			}
		})
	}
}

// An argument over MaxArgumentBytes is classified without being decoded: the
// tool's Overlap function never sees it, and the call settles
// arguments-too-large.
func TestOversizedArgumentsAreNotClassified(t *testing.T) {
	e := newEnv(t)
	act := newActivity()
	var classified []string
	var mu sync.Mutex
	svc := e.pipeline(t, core.Config{MaxParallel: 3, MaxArgumentBytes: 40})
	e.register(t, e.root, keyedTool("touch", act,
		func(in keyIn) tools.Overlap {
			mu.Lock()
			defer mu.Unlock()
			classified = append(classified, in.ID)
			return tools.Writes(in.Key)
		},
		func(string) {}))
	calls := [][2]string{
		{"touch", `{"key":"a","id":"1"}`},
		{"touch", `{"key":"` + strings.Repeat("x", 64) + `","id":"2"}`},
	}
	h := openSession(t, memstore.New(), calls...)
	res := execute(t, svc, view(t, svc), h, calls...)
	if res[0].IsError {
		t.Fatalf("call 1: %+v", res[0])
	}
	if res[1].Code != tools.CodeArgumentsTooLarge {
		t.Fatalf("call 2: %+v", res[1])
	}
	if !reflect.DeepEqual(classified, []string{"1"}) {
		t.Fatalf("Overlap saw %v, want only call 1", classified)
	}
}

// failCallLog refuses the n-th tool/call append and then closes failed.
type failCallLog struct {
	session.Handle
	mu     sync.Mutex
	n      int
	failed chan struct{}
}

func (l *failCallLog) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	if d.Type() == event.ToolCall.Name() {
		l.mu.Lock()
		l.n--
		hit := l.n == 0
		l.mu.Unlock()
		if hit {
			close(l.failed)
			return event.Receipt{}, errors.New("disk full")
		}
	}
	return l.Handle.Append(ctx, d)
}

// A log that refuses call 2's tool/call stops admission at once: while call 1
// still runs, call 3, though free to overlap, is neither logged nor started.
func TestLogFailureStopsAdmissionWhileEarlierCallsRun(t *testing.T) {
	e := newEnv(t)
	act := newActivity()
	svc := e.pipeline(t, core.Config{MaxParallel: 3})
	calls := [][2]string{
		{"free", `{"key":"a","id":"1"}`},
		{"free", `{"key":"b","id":"2"}`},
		{"free", `{"key":"c","id":"3"}`},
	}
	h := &failCallLog{Handle: openSession(t, memstore.New(), calls...), n: 2, failed: make(chan struct{})}
	e.register(t, e.root, keyedTool("free", act,
		func(keyIn) tools.Overlap { return tools.Free },
		func(id string) {
			if id == "1" {
				<-h.failed // call 1 runs until call 2's append has failed
			}
		}))
	_, err := svc.ExecuteBatch(context.Background(), tools.Batch{View: view(t, svc), Log: h, Calls: requests(calls...)})
	if err == nil {
		t.Fatal("a refused tool/call did not fail the batch")
	}
	if !reflect.DeepEqual(act.ended, []string{"1"}) {
		t.Fatalf("bodies run %v, want only call 1", act.ended)
	}
	lc, lr := resultOrder(t, h.Handle)
	if !reflect.DeepEqual(lc, []string{"c1"}) || !reflect.DeepEqual(lr, []string{"c1", "c2"}) {
		t.Fatalf("logged calls %v results %v, want calls [c1] and results [c1 c2]", lc, lr)
	}
}

// A detached body keeps its declared resource keys: a later call of a different
// tool writing the same key, in the same batch or a later one, is refused busy
// at once without waiting on the body, while one on another key runs. Forcing
// either tool exclusive orders the batch but hides no key.
func TestDetachedBodyKeepsItsResourceKeys(t *testing.T) {
	for _, tc := range []struct {
		name   string
		forced []string
		batch  bool // the conflicting calls follow hold in its own batch
	}{
		{name: "later batch"},
		{name: "same batch", batch: true},
		{name: "detached forced exclusive", forced: []string{"hold"}, batch: true},
		{name: "later call forced exclusive", forced: []string{"poke"}, batch: true},
		{name: "both forced, later batch", forced: []string{"hold", "poke"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewManual(epoch)
			e := newEnv(t)
			act := newActivity()
			svc := e.pipeline(t, core.Config{Clock: clk, DefaultTimeout: 7 * time.Second, Grace: time.Second,
				MaxParallel: 3, ForceExclusive: tc.forced, Checkpoint: recCheckpoint{&trace{}}})
			started, release := make(chan struct{}), make(chan struct{})
			writes := func(in keyIn) tools.Overlap { return tools.Writes(in.Key) }
			e.register(t, e.root, keyedTool("hold", act, writes, func(string) {
				close(started)
				<-release
			}))
			e.register(t, e.root, keyedTool("poke", act, writes, func(string) {}))
			defer close(release)

			hold := [2]string{"hold", `{"key":"k","id":"1"}`}
			same, other := [2]string{"poke", `{"key":"k","id":"2"}`}, [2]string{"poke", `{"key":"j","id":"3"}`}
			calls := [][2]string{hold}
			if tc.batch {
				calls = append(calls, same, other)
			}
			done := run(svc, view(t, svc), openSession(t, memstore.New(), calls...), context.Background(), calls...)
			recv(t, started, "the held body to start")
			block(t, clk, 1)
			clk.Advance(8 * time.Second)
			out := recv(t, done, "the batch")
			if out.err != nil || out.res.Results[0].Code != tools.CodeOutcomeUnknown {
				t.Fatalf("held call: %+v", out)
			}
			if len(svc.Detached()) != 1 {
				t.Fatalf("Detached = %+v", svc.Detached())
			}
			res := out.res.Results[1:]
			if !tc.batch {
				res = execute(t, svc, view(t, svc), openSession(t, memstore.New(), same, other), same, other)
			}
			if res[0].Code != tools.CodeBusy || res[0].Stage != tools.StageBody || !strings.Contains(res[0].Text, "resource key") {
				t.Fatalf("same key: %+v", res[0])
			}
			if res[1].IsError {
				t.Fatalf("other key: %+v", res[1])
			}
			act.mu.Lock()
			defer act.mu.Unlock()
			if _, ok := act.running["2"]; ok || slices.Contains(act.ended, "2") {
				t.Fatal("the conflicting call ran beside the detached body")
			}
			if !slices.Contains(act.ended, "3") {
				t.Fatalf("bodies ended %v, want call 3 among them", act.ended)
			}
		})
	}
}
