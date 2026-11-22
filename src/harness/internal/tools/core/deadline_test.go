package core_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/memstore"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/policy"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/conform"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/core"
)

var epoch = time.Date(2025, 1, 2, 3, 4, 5, 0, time.UTC)

type noIn struct{}

// stuckTool ignores its context: it returns only when release is closed.
func stuckTool(timeout time.Duration, started chan<- struct{}, release <-chan struct{}) *tools.Declaration {
	return tools.Define(tools.Spec[noIn, string]{
		Name: "stuck", Description: "Ignores cancellation.", Timeout: timeout,
		Run: func(context.Context, noIn, tools.Run) (string, error) {
			if started != nil {
				started <- struct{}{}
			}
			<-release
			return "late", nil
		},
	})
}

// politeTool returns as soon as its context is cancelled, with the cause.
func politeTool(name string, timeout time.Duration, started chan<- struct{}, cause chan<- error) *tools.Declaration {
	return tools.Define(tools.Spec[noIn, string]{
		Name: name, Description: "Stops when cancelled.", Timeout: timeout,
		Run: func(ctx context.Context, _ noIn, _ tools.Run) (string, error) {
			started <- struct{}{}
			<-ctx.Done()
			if cause != nil {
				cause <- context.Cause(ctx)
			}
			return "", context.Cause(ctx)
		},
	})
}

func block(t *testing.T, clk *clock.Manual, n int) {
	t.Helper()
	if !clk.BlockUntil(n, 5*time.Second) {
		t.Fatalf("the clock has %d waiters, want %d", clk.Waiters(), n)
	}
}

func recv[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

type batchOut struct {
	res tools.BatchResult
	err error
}

func run(svc tools.Service, v tools.StepView, h session.Handle, ctx context.Context, calls ...[2]string) <-chan batchOut {
	out := make(chan batchOut, 1)
	go func() {
		res, err := svc.ExecuteBatch(ctx, tools.Batch{View: v, Log: h, Calls: requests(calls...)})
		out <- batchOut{res, err}
	}()
	return out
}

// DH-T-TOOLS-9, TC-10a: a body that ignores its context does not hold the turn.
// With a 10 s budget and 2 s grace on the virtual clock, after 12 s the call is
// answered outcome-unknown (timed out), the batch goes on to its next call, the
// body is listed as detached, a further call of the tool is busy, and its
// eventual return appends tool/late-return.
func TestDeadlineDoesNotHoldTheTurn(t *testing.T) {
	clk := clock.NewManual(epoch)
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{Clock: clk, Grace: 2 * time.Second, Checkpoint: recCheckpoint{&trace{}}})
	started, release := make(chan struct{}, 1), make(chan struct{})
	e.register(t, e.root, stuckTool(10*time.Second, started, release))
	e.register(t, e.root, addTool(&trace{}))
	stuck, add := [2]string{"stuck", `{}`}, [2]string{"add", `{"a":1,"b":2}`}
	h := &lateLog{Handle: openSession(t, memstore.New(), stuck, add), late: make(chan struct{}, 1)}
	v := view(t, svc)

	done := run(svc, v, h, context.Background(), stuck, add)
	recv(t, started, "the body to start")
	block(t, clk, 1)
	// One advance past both the budget and the grace: grace is measured from
	// the deadline, not from when its timer was noticed.
	clk.Advance(12 * time.Second)
	out := recv(t, done, "the batch")
	if out.err != nil {
		t.Fatal(out.err)
	}
	r := out.res.Results
	if r[0].Code != tools.CodeOutcomeUnknown || r[0].Cause != tools.CauseDeadline || r[0].Stage != tools.StageBody ||
		!strings.Contains(r[0].Text, "timed out after 10s") {
		t.Fatalf("stuck call: %+v", r[0])
	}
	if r[1].IsError || r[1].Value != 3 {
		t.Fatalf("the step did not continue: %+v", r[1])
	}
	det := svc.Detached()
	if len(det) != 1 || det[0].CallID != "c1" || det[0].Tool != "stuck" || det[0].Agent != me || !det[0].Since.Equal(epoch.Add(12*time.Second)) {
		t.Fatalf("Detached = %+v", det)
	}

	// The next step of the same agent, in a session whose call is unanswered.
	h2 := openSession(t, memstore.New(), stuck)
	busy := execute(t, svc, view(t, svc), h2, stuck)
	if busy[0].Code != tools.CodeBusy || busy[0].Stage != tools.StageBody {
		t.Fatalf("a second call while detached: %+v", busy[0])
	}

	close(release)
	recv(t, h.late, "tool/late-return")
	if got := svc.Detached(); len(got) != 0 {
		t.Fatalf("still detached after its return: %+v", got)
	}
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var late tools.LateReturn
	for _, ev := range evs {
		if ev.Type() == "tool/late-return" {
			if err := json.Unmarshal([]byte(ev.Raw()), &late); err != nil {
				t.Fatal(err)
			}
		}
	}
	if late != (tools.LateReturn{CallID: "c1", Tool: "stuck", OK: true}) {
		t.Fatalf("late return = %+v", late)
	}
	for _, ev := range evs {
		if ev.Type() == "tool/result" && strings.Contains(ev.Raw(), "late") && !strings.Contains(ev.Raw(), "outcome-unknown") {
			t.Fatalf("the late value reached a result: %s", ev.Raw())
		}
	}
}

// DH-T-TOOLS-8: a body that returns within grace after its budget ends gives
// tools/timeout, and its context's cause is tools.ErrTimeout.
func TestDeadlineCooperativeBodyTimesOut(t *testing.T) {
	clk := clock.NewManual(epoch)
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{Clock: clk, Grace: 2 * time.Second, Checkpoint: recCheckpoint{&trace{}}})
	started, cause := make(chan struct{}, 1), make(chan error, 1)
	e.register(t, e.root, politeTool("polite", 10*time.Second, started, cause))
	call := [2]string{"polite", `{}`}
	h := openSession(t, memstore.New(), call)
	done := run(svc, view(t, svc), h, context.Background(), call)
	recv(t, started, "the body to start")
	block(t, clk, 1)
	clk.Advance(10 * time.Second)
	if err := recv(t, cause, "the cancellation"); !errors.Is(err, tools.ErrTimeout) {
		t.Fatalf("cause = %v, want tools.ErrTimeout", err)
	}
	out := recv(t, done, "the batch")
	if out.err != nil {
		t.Fatal(out.err)
	}
	if r := out.res.Results[0]; r.Code != tools.CodeTimeout || r.Cause != tools.CauseDeadline || !r.IsError {
		t.Fatalf("result = %+v", r)
	}
	if len(svc.Detached()) != 0 {
		t.Fatal("a body that returned within grace was detached")
	}
}

// DH-T-TOOLS-8: the budget is the operator's override, else the declared
// Timeout, else the default; NoDeadline sets none, and a cancelled turn then
// gives tools/aborted.
func TestDeadlineBudgetPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name     string
		decl     time.Duration
		override time.Duration
		want     time.Duration
	}{
		{"default", 0, 0, 7 * time.Second},
		{"declared", 10 * time.Second, 0, 10 * time.Second},
		{"override", 10 * time.Second, 3 * time.Second, 3 * time.Second},
		{"override-beats-none", tools.NoDeadline, 4 * time.Second, 4 * time.Second},
		{"none", tools.NoDeadline, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewManual(epoch)
			e := newEnv(t)
			cfg := core.Config{Clock: clk, DefaultTimeout: 7 * time.Second, Grace: time.Second, Checkpoint: recCheckpoint{&trace{}}}
			if tc.override != 0 {
				cfg.Timeouts = map[string]time.Duration{"polite": tc.override}
			}
			svc := e.pipeline(t, cfg)
			started := make(chan struct{}, 1)
			e.register(t, e.root, politeTool("polite", tc.decl, started, nil))
			call := [2]string{"polite", `{}`}
			h := openSession(t, memstore.New(), call)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := run(svc, view(t, svc), h, ctx, call)
			recv(t, started, "the body to start")
			if tc.want == 0 {
				if n := clk.Waiters(); n != 0 {
					t.Fatalf("NoDeadline set %d timers", n)
				}
				cancel()
				out := recv(t, done, "the batch")
				if r := out.res.Results[0]; r.Code != tools.CodeAborted || r.Cause != tools.CauseCancelled {
					t.Fatalf("result = %+v", r)
				}
				return
			}
			block(t, clk, 1)
			clk.Advance(tc.want - time.Millisecond)
			select {
			case out := <-done:
				t.Fatalf("settled before its budget of %s: %+v", tc.want, out.res.Results)
			case <-time.After(20 * time.Millisecond):
			}
			clk.Advance(time.Millisecond)
			out := recv(t, done, "the batch")
			if r := out.res.Results[0]; r.Code != tools.CodeTimeout || !strings.Contains(r.Text, tc.want.String()) {
				t.Fatalf("result = %+v", r)
			}
		})
	}
}

// DH-T-TOOLS-8: a declaration's longer Grace wins over the registry's.
func TestDeadlineDeclaredGraceIsLonger(t *testing.T) {
	clk := clock.NewManual(epoch)
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{Clock: clk, Grace: time.Second, Checkpoint: recCheckpoint{&trace{}}})
	started, release := make(chan struct{}, 1), make(chan struct{})
	t.Cleanup(func() { close(release) })
	e.register(t, e.root, tools.Define(tools.Spec[noIn, string]{
		Name: "tree", Description: "Stops a process tree.", Timeout: time.Second, Grace: 30 * time.Second,
		Run: func(context.Context, noIn, tools.Run) (string, error) {
			started <- struct{}{}
			<-release
			return "", nil
		},
	}))
	call := [2]string{"tree", `{}`}
	h := openSession(t, memstore.New(), call)
	done := run(svc, view(t, svc), h, context.Background(), call)
	recv(t, started, "the body to start")
	block(t, clk, 1)
	clk.Advance(time.Second)
	block(t, clk, 1)
	clk.Advance(29 * time.Second)
	select {
	case <-done:
		t.Fatal("detached before the declared grace")
	case <-time.After(20 * time.Millisecond):
	}
	clk.Advance(time.Second)
	if r := recv(t, done, "the batch").res.Results[0]; r.Code != tools.CodeOutcomeUnknown || !strings.Contains(r.Text, "30s grace") {
		t.Fatalf("result = %+v", r)
	}
}

// The declaration refuses a negative timeout other than NoDeadline, a negative
// grace and a grace above tools.MaxGrace (60 s).
func TestDeadlineDeclarationChecks(t *testing.T) {
	for _, spec := range []tools.Spec[noIn, string]{
		{Timeout: -2 * time.Second},
		{Grace: -time.Second},
		{Grace: tools.MaxGrace + time.Nanosecond},
	} {
		spec.Name, spec.Description = "bad", "Bad."
		spec.Run = func(context.Context, noIn, tools.Run) (string, error) { return "", nil }
		if err := tools.Define(spec).Err(); err == nil {
			t.Fatalf("%+v was accepted", spec)
		}
	}
	longest := tools.Define(tools.Spec[noIn, string]{Name: "tree", Description: "Longest grace.", Grace: tools.MaxGrace,
		Run: func(context.Context, noIn, tools.Run) (string, error) { return "", nil }})
	if err := longest.Err(); err != nil || longest.Grace() != 60*time.Second {
		t.Fatalf("a grace of MaxGrace: %v, %v", err, longest.Grace())
	}
	ok := tools.Define(tools.Spec[noIn, string]{Name: "free", Description: "No deadline.", Timeout: tools.NoDeadline,
		Run: func(context.Context, noIn, tools.Run) (string, error) { return "", nil }})
	if err := ok.Err(); err != nil || ok.Timeout() != tools.NoDeadline {
		t.Fatalf("NoDeadline: %v, %v", err, ok.Timeout())
	}
}

// DH-T-TOOLS-9: the conformance helper fails a body that ignores its context
// and passes one that stops.
func TestConformanceCancellation(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	if err := conform.Cancellation(stuckTool(0, nil, release), nil, 50*time.Millisecond); err == nil ||
		!strings.Contains(err.Error(), "did not return") {
		t.Fatalf("an ignoring body passed: %v", err)
	}
	started := make(chan struct{}, 1)
	if err := conform.Cancellation(politeTool("polite", 0, started, nil), nil, time.Second); err != nil {
		t.Fatalf("a cooperative body failed: %v", err)
	}
	if err := conform.Cancellation(nil, nil, 0); err == nil {
		t.Fatal("no declaration passed")
	}
	if err := conform.Cancellation(tools.Define(tools.Spec[noIn, string]{Name: "bad", Timeout: -2}), nil, 0); err == nil {
		t.Fatal("an invalid declaration passed")
	}
	if err := conform.Cancellation(addTool(&trace{}), []byte(`{"a":"x"}`), 0); err == nil ||
		!strings.Contains(err.Error(), "never reached its body") {
		t.Fatalf("a call refused at validate passed: %v", err)
	}
}

// numbered is n bytes of numbered lines.
func numbered(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "line %07d\n", i)
	}
	return b.String()[:n]
}

func textTool(name, text string, boundsOwn bool) *tools.Declaration {
	return tools.Define(tools.Spec[noIn, string]{
		Name: name, Description: "Prints text.", BoundsOwnOutput: boundsOwn,
		Run: func(context.Context, noIn, tools.Run) (string, error) { return text, nil },
	})
}

// resultsOf is the logged tool/result payloads of h by call id, with their raw
// sizes.
func resultsOf(t *testing.T, h session.Handle) (map[string]event.ToolResultPayload, map[string]int) {
	t.Helper()
	evs, err := h.Read(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	out, sizes := map[string]event.ToolResultPayload{}, map[string]int{}
	for _, ev := range evs {
		if ev.Type() != "tool/result" {
			continue
		}
		var p event.ToolResultPayload
		if err := json.Unmarshal([]byte(ev.Raw()), &p); err != nil {
			t.Fatal(err)
		}
		out[p.CallID], sizes[p.CallID] = p, len(ev.Raw())
	}
	return out, sizes
}

// DH-T-TOOLS-6 (ceiling): with no spill seat, a result above maxResultBytes is
// cut at a rune boundary with a marker and Truncated is set.
func TestResultCeiling(t *testing.T) {
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{MaxResultBytes: 101, Checkpoint: recCheckpoint{&trace{}}})
	e.register(t, e.root, textTool("wide", strings.Repeat("é", 1000), false))
	call := [2]string{"wide", `{}`}
	h := openSession(t, memstore.New(), call)
	r := execute(t, svc, view(t, svc), h, call)[0]
	if len(r.Text) > 101 || !r.Truncated || !utf8.ValidString(r.Text) || !strings.Contains(r.Text, "[truncated:") || r.IsError {
		t.Fatalf("result (%d bytes) = %+v", len(r.Text), r)
	}
	evs, _ := h.Read(context.Background(), 0, 0)
	if raw := evs[len(evs)-1].Raw(); !strings.Contains(raw, `\"truncated\":true`) && !strings.Contains(raw, `"truncated":true`) {
		t.Fatalf("the logged result does not say truncated: %s", raw)
	}
}

// DH-T-STORAGE-10, DH-T-STORAGE-12, TC-10b: through the real pipeline on the
// SQLite store and a spill store in a temporary home, a 2 MB result with a 64 KB
// maxResultBytes is logged within 64 KB with its spill reference, and the spill
// file holds all 2 MB; a result under the budget, a look-alike notice and a
// tool that bounds its own output get no spill field.
func TestOversizedResultSpilledBeforeReachingTheModel(t *testing.T) {
	ctx := context.Background()
	st, err := sqlite.Open(ctx, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close(ctx) })
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	sp, err := local.New(l, local.DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	const limit = 64 << 10
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{MaxResultBytes: limit, Checkpoint: recCheckpoint{&trace{}}})
	pol, _, err := policy.Mount(e.root, svc, sp, policy.Config{Now: func() time.Time { return epoch }})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSpill(e.root, "second", func(context.Context, tools.Call, tools.Outcome) tools.SpillResult {
		return tools.SpillResult{}
	}); err == nil {
		t.Fatal("a second spill seat in one layer was accepted")
	}
	full := numbered(2 << 20)
	lookAlike := "(Omitted 5 of 10 bytes. Full result saved to /tmp/x, kept until 2025-02-01. Use read with offset and limit, or grep this path.)"
	e.register(t, e.root, textTool("big", full, false))
	e.register(t, e.root, textTool("small", "fits", false))
	e.register(t, e.root, textTool("liar", lookAlike, false))
	e.register(t, e.root, textTool("reader", numbered(100<<10), true))
	calls := [][2]string{{"big", `{}`}, {"small", `{}`}, {"liar", `{}`}, {"reader", `{}`}}
	h := openSession(t, st, calls...)
	res := execute(t, svc, view(t, svc), h, calls...)

	big := res[0]
	if big.IsError || big.Spill == nil || big.Truncated || len(big.Text) > pol.Budget() || len(big.Text) > limit {
		t.Fatalf("big: spill %+v, truncated %v, %d bytes", big.Spill, big.Truncated, len(big.Text))
	}
	if !strings.HasPrefix(big.Text, full[:1000]) || !strings.Contains(big.Text, full[len(full)-1000:]) {
		t.Fatal("the preview does not keep the head and the tail in order")
	}
	logged, sizes := resultsOf(t, h)
	if p := logged["c1"]; p.Spill == nil || *p.Spill != *big.Spill || p.Spill.Bytes != 2<<20 || sizes["c1"] > limit {
		t.Fatalf("logged big: spill %+v, %d bytes", p.Spill, sizes["c1"])
	}
	host, ok := sp.HostPath(big.Spill.Path)
	if !ok || !strings.Contains(big.Text, host) {
		t.Fatalf("the notice does not name the saved file %q", host)
	}
	saved, err := os.ReadFile(host)
	if err != nil || !bytes.Equal(saved, []byte(full)) {
		t.Fatalf("the spill file holds %d bytes, want all %d: %v", len(saved), len(full), err)
	}
	if !strings.HasPrefix(host, l.Spill()) {
		t.Fatalf("the spill file %s is not under the temp home's spill directory %s", host, l.Spill())
	}
	for i, id := range []string{"c2", "c3"} {
		if r := res[i+1]; r.Spill != nil || logged[id].Spill != nil || r.Text != []string{"fits", lookAlike}[i] {
			t.Fatalf("%s was changed: %+v", id, r)
		}
	}
	if r := res[3]; r.Spill != nil || logged["c4"].Spill != nil || !r.Truncated || len(r.Text) > limit {
		t.Fatalf("a tool bounding its own output: spill %+v, truncated %v, %d bytes", r.Spill, r.Truncated, len(r.Text))
	}
}

// DH-T-TOOLS-9: grace after a deadline ends at expiry+grace however the clock
// gets there: noticed at 10 s, the call is still waiting at 11.999 s and is
// answered at 12 s.
func TestDeadlineGraceEndsAtExpiryPlusGrace(t *testing.T) {
	clk := clock.NewManual(epoch)
	e := newEnv(t)
	svc := e.pipeline(t, core.Config{Clock: clk, Grace: 2 * time.Second, Checkpoint: recCheckpoint{&trace{}}})
	started, release := make(chan struct{}, 1), make(chan struct{})
	t.Cleanup(func() { close(release) })
	e.register(t, e.root, stuckTool(10*time.Second, started, release))
	call := [2]string{"stuck", `{}`}
	h := openSession(t, memstore.New(), call)
	done := run(svc, view(t, svc), h, context.Background(), call)
	recv(t, started, "the body to start")
	block(t, clk, 1)
	clk.Advance(10 * time.Second)
	block(t, clk, 1) // the grace timer
	clk.Advance(2*time.Second - time.Millisecond)
	select {
	case out := <-done:
		t.Fatalf("answered before expiry+grace: %+v", out.res.Results)
	case <-time.After(20 * time.Millisecond):
	}
	clk.Advance(time.Millisecond)
	if r := recv(t, done, "the batch").res.Results[0]; r.Code != tools.CodeOutcomeUnknown || r.Cause != tools.CauseDeadline {
		t.Fatalf("result = %+v", r)
	}
}

// DH-T-TOOLS-8: a body that returns at its expiry has timed out even when its
// return is seen before the deadline's timer (lateClock never delivers the
// budget's timer, as a timer noticed late); one that returns a moment before
// its expiry keeps its value.
func TestDeadlineReturnAtExpiryIsTimeout(t *testing.T) {
	for _, tc := range []struct {
		name  string
		after time.Duration
		code  tools.Code
	}{
		{"at-expiry", 10 * time.Second, tools.CodeTimeout},
		{"after-expiry", 11 * time.Second, tools.CodeTimeout},
		{"before-expiry", 10*time.Second - time.Millisecond, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewManual(epoch)
			e := newEnv(t)
			svc := e.pipeline(t, core.Config{Clock: lateClock{clk}, Grace: 2 * time.Second, Checkpoint: recCheckpoint{&trace{}}})
			e.register(t, e.root, tools.Define(tools.Spec[noIn, string]{
				Name: "racer", Description: "Returns on its own timer.", Timeout: 10 * time.Second,
				Run: func(context.Context, noIn, tools.Run) (string, error) {
					<-clk.After(tc.after)
					return "done", nil
				},
			}))
			call := [2]string{"racer", `{}`}
			h := openSession(t, memstore.New(), call)
			done := run(svc, view(t, svc), h, context.Background(), call)
			block(t, clk, 1) // the body's timer
			clk.Advance(tc.after)
			r := recv(t, done, "the batch").res.Results[0]
			if r.Code != tc.code || (tc.code == "" && r.IsError) || (tc.code != "" && r.Cause != tools.CauseDeadline) {
				t.Fatalf("result = %+v", r)
			}
		})
	}
}

// lateClock is a manual clock whose timers the pipeline asks for are never
// delivered: the pipeline sees a body's return before any deadline.
type lateClock struct{ *clock.Manual }

func (lateClock) After(time.Duration) <-chan time.Time { return make(chan time.Time) }

// firedClock is a manual clock whose first timer, the call's budget, the test
// fires itself without advancing time; later timers are the manual clock's.
type firedClock struct {
	*clock.Manual
	fire  chan time.Time
	asked *atomic.Bool
}

func (c firedClock) After(d time.Duration) <-chan time.Time {
	if c.asked.CompareAndSwap(false, true) {
		return c.fire
	}
	return c.Manual.After(d)
}

// DH-T-TOOLS-8: a body that returned before its expiry keeps its value when the
// deadline is seen first, or together with its return: the recorded return
// time, not the select's order, decides.
func TestDeadlineReturnBeforeExpiryWithDeadlineSeen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		runs  int
		first bool // fire the deadline before the body returns
	}{
		{"deadline-seen-first", 1, true},
		{"both-ready", 50, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for range tc.runs {
				clk := firedClock{clock.NewManual(epoch), make(chan time.Time, 1), &atomic.Bool{}}
				e := newEnv(t)
				svc := e.pipeline(t, core.Config{Clock: clk, Grace: 2 * time.Second, Checkpoint: recCheckpoint{&trace{}}})
				started, release, back := make(chan struct{}, 1), make(chan struct{}), make(chan struct{})
				e.register(t, e.root, tools.Define(tools.Spec[noIn, string]{
					Name: "quick", Description: "Returns in budget.", Timeout: 10 * time.Second,
					Run: func(context.Context, noIn, tools.Run) (string, error) {
						started <- struct{}{}
						<-release
						defer close(back)
						return "done", nil
					},
				}))
				call := [2]string{"quick", `{}`}
				h := openSession(t, memstore.New(), call)
				done := run(svc, view(t, svc), h, context.Background(), call)
				recv(t, started, "the body to start")
				if tc.first {
					clk.fire <- epoch.Add(10 * time.Second)
					block(t, clk.Manual, 1) // the pipeline is in grace: it saw the deadline
					close(release)
				} else {
					close(release)
					<-back
					clk.fire <- epoch.Add(10 * time.Second)
				}
				r := recv(t, done, "the batch").res.Results[0]
				if r.IsError || r.Code != "" || r.Cause != "" || r.Text != "done" {
					t.Fatalf("an in-budget return was not kept: %+v", r)
				}
			}
		})
	}
}

// DH-ADR-STORAGE-4: a spill seat that panics, or whose Replace grows the text,
// is reported in Result.SpillFailure and the logged result, and the text, still
// within maxResultBytes, ends with a notice that the full result was not saved.
func TestSpillSeatFailureIsReported(t *testing.T) {
	const limit = 1000
	for _, tc := range []struct {
		name string
		seat tools.SpillShaper
		want string
	}{
		{"panic", func(context.Context, tools.Call, tools.Outcome) tools.SpillResult {
			panic(strings.Repeat("disk on fire\n", 50))
		}, "panicked: disk on fire"},
		{"grows", func(_ context.Context, _ tools.Call, o tools.Outcome) tools.SpillResult {
			return tools.SpillResult{Replace: true, Text: o.Text + "more", Ref: &event.Spill{Path: "x", Bytes: 1}}
		}, "grew the text"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			svc := e.pipeline(t, core.Config{MaxResultBytes: limit, Checkpoint: recCheckpoint{&trace{}}})
			if _, err := svc.SetSpill(e.root, "broken", tc.seat); err != nil {
				t.Fatal(err)
			}
			e.register(t, e.root, textTool("big", numbered(10_000), false))
			call := [2]string{"big", `{}`}
			h := openSession(t, memstore.New(), call)
			r := execute(t, svc, view(t, svc), h, call)[0]
			if r.IsError || r.Spill != nil || !r.Truncated || len(r.Text) > limit || !strings.Contains(r.SpillFailure, tc.want) {
				t.Fatalf("result (%d bytes) = %+v", len(r.Text), r)
			}
			if !strings.HasSuffix(r.Text, "]") || !strings.Contains(r.Text, "[the full result was not saved for later reading: the spill seat broken") ||
				!strings.Contains(r.Text, "[truncated:") {
				t.Fatalf("the notice is missing or cut: %q", r.Text[max(len(r.Text)-400, 0):])
			}
			logged, sizes := resultsOf(t, h)
			evs, _ := h.Read(context.Background(), 0, 0)
			raw := evs[len(evs)-1].Raw()
			if logged["c1"].Spill != nil || sizes["c1"] > 4*limit || !strings.Contains(raw, "spillFailure") {
				t.Fatalf("logged result: %s", raw)
			}
		})
	}
	// A small result keeps all its text with the notice; a ceiling below the
	// notice's size still holds.
	for _, tc := range []struct {
		limit int
		cut   bool
	}{{1000, false}, {40, true}} {
		e := newEnv(t)
		svc := e.pipeline(t, core.Config{MaxResultBytes: tc.limit, Checkpoint: recCheckpoint{&trace{}}})
		if _, err := svc.SetSpill(e.root, "broken", func(context.Context, tools.Call, tools.Outcome) tools.SpillResult { panic("no") }); err != nil {
			t.Fatal(err)
		}
		e.register(t, e.root, textTool("small", "fits", false))
		call := [2]string{"small", `{}`}
		r := execute(t, svc, view(t, svc), openSession(t, memstore.New(), call), call)[0]
		if len(r.Text) > tc.limit || r.Truncated != tc.cut || r.SpillFailure == "" ||
			(!tc.cut && (!strings.HasPrefix(r.Text, "fits\n[the full result was not saved") || !strings.HasSuffix(r.Text, "]"))) {
			t.Fatalf("limit %d: %+v", tc.limit, r)
		}
	}
}

// lateLog is a session that signals when tool/late-return is appended to it.
type lateLog struct {
	session.Handle
	late chan struct{}
}

func (l *lateLog) Append(ctx context.Context, d event.Draft) (event.Receipt, error) {
	rc, err := l.Handle.Append(ctx, d)
	if d.Type() == tools.LateReturnType.Name() {
		l.late <- struct{}{}
	}
	return rc, err
}
