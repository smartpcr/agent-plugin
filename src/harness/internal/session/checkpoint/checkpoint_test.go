package checkpoint_test

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/checkpoint"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store/sqlite"
)

var bg = context.Background()

var errDisk = errors.New("injected: disk full")

// faults wraps a real SQLite store; while fail is set, every writer's Flush
// returns errDisk without reaching the store.
type faults struct {
	store.Store
	fail    atomic.Bool
	flushes atomic.Int64
}

func (f *faults) Create(ctx context.Context, h store.Header, o store.CreateOptions) (store.Writer, error) {
	w, err := f.Store.Create(ctx, h, o)
	if err != nil {
		return nil, err
	}
	return &faultWriter{Writer: w, f: f}, nil
}

type faultWriter struct {
	store.Writer
	f *faults
}

func (w *faultWriter) Flush(ctx context.Context) error {
	w.f.flushes.Add(1)
	if w.f.fail.Load() {
		return errDisk
	}
	return w.Writer.Flush(ctx)
}

func newSession(t *testing.T) (session.Handle, *faults, *cordis.Context) {
	t.Helper()
	s, err := sqlite.Open(bg, filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close(bg) })
	f := &faults{Store: s}
	root := cordis.New()
	root.Loggers().ClearExporters()
	t.Cleanup(root.Dispose)
	h, err := session.NewService(root, f, session.Config{}).Create(bg, nil, session.CreateOptions{Header: session.Header{ID: "cp"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close(bg) })
	return h, f, root
}

func appendDraft(t *testing.T, h session.Handle, d event.Draft) {
	t.Helper()
	if _, err := h.Append(bg, d); err != nil {
		t.Fatal(err)
	}
}

// DH-T-STORE-9 / SP-8: a failed flush before a tool body means the body never
// runs, and the step-end checkpoint fails too.
func TestFailedFlushBlocksTheToolBody(t *testing.T) {
	h, f, _ := newSession(t)
	appendDraft(t, h, must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})))
	appendDraft(t, h, must(event.StepStartType.Draft(event.StepStart{Turn: 1, Step: 1})))
	f.fail.Store(true)

	var calls int
	err := checkpoint.RunTool(bg, nil, h, false, func(context.Context) error { calls++; return nil })
	if calls != 0 {
		t.Fatalf("the body ran %d times behind a failed flush", calls)
	}
	if !errors.Is(err, checkpoint.ErrNotStarted) || !errors.Is(err, errDisk) {
		t.Fatalf("tool-body checkpoint = %v, want tools/not-started wrapping the flush error", err)
	}
	var ce *checkpoint.Error
	if !errors.As(err, &ce) || ce.Point != checkpoint.ToolBody {
		t.Fatalf("error = %#v, want a checkpoint.Error at tool-body", err)
	}

	appendDraft(t, h, must(event.StepEndType.Draft(event.StepEnd{Turn: 1, Step: 1})))
	err = checkpoint.Before(bg, nil, h, checkpoint.StepEnd)
	if !errors.Is(err, checkpoint.ErrFailed) || !errors.Is(err, errDisk) {
		t.Fatalf("step-end checkpoint = %v, want session/checkpoint-failed", err)
	}
}

func TestCheckpointFlushesBeforeTheWork(t *testing.T) {
	h, f, _ := newSession(t)
	appendDraft(t, h, must(event.TurnStartType.Draft(event.TurnStart{Turn: 1})))
	for _, p := range []checkpoint.Point{checkpoint.Attempt, checkpoint.ToolBody, checkpoint.StepEnd} {
		before := f.flushes.Load()
		var durable event.Offset
		err := checkpoint.Run(bg, nil, h, p, func(context.Context) error {
			durable = h.Durable()
			return nil
		})
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		if f.flushes.Load() != before+1 {
			t.Fatalf("%s: flushes = %d, want one before the work", p, f.flushes.Load()-before)
		}
		if durable != h.Len() {
			t.Fatalf("%s: durable = %d at the work, want %d", p, durable, h.Len())
		}
	}
}

func TestNestedCallAddsNoFlush(t *testing.T) {
	h, f, _ := newSession(t)
	f.fail.Store(true)
	before := f.flushes.Load()
	var calls int
	if err := checkpoint.RunTool(bg, nil, h, true, func(context.Context) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	if calls != 1 || f.flushes.Load() != before {
		t.Fatalf("calls = %d, flushes = %d; want the nested body run with no flush", calls, f.flushes.Load()-before)
	}
}

func TestCancelledBeforeTheBodyIsAbortedBeforeStart(t *testing.T) {
	h, f, _ := newSession(t)
	ctx, cancel := context.WithCancel(bg)
	cancel()
	before := f.flushes.Load()
	var calls int
	err := checkpoint.RunTool(ctx, nil, h, false, func(context.Context) error { calls++; return nil })
	if calls != 0 || !errors.Is(err, checkpoint.ErrAbortedBeforeStart) {
		t.Fatalf("calls = %d, err = %v; want tools/aborted-before-start and no body", calls, err)
	}
	if f.flushes.Load() != before {
		t.Fatal("a cancelled checkpoint flushed")
	}
}

type counting struct{ points []checkpoint.Point }

func (c *counting) Before(_ context.Context, _ checkpoint.Session, p checkpoint.Point) error {
	c.points = append(c.points, p)
	return nil
}

func TestMountedPolicyAndDefault(t *testing.T) {
	_, _, root := newSession(t)
	if _, ok := checkpoint.Mounted(root).(checkpoint.Default); !ok {
		t.Fatal("with none mounted the policy is not Default")
	}
	if _, ok := checkpoint.Mounted(nil).(checkpoint.Default); !ok {
		t.Fatal("a nil context's policy is not Default")
	}
	c := &counting{}
	if _, err := checkpoint.Key.Provide(root, c); err != nil {
		t.Fatal(err)
	}
	p := checkpoint.Mounted(root)
	if p != checkpoint.Policy(c) {
		t.Fatalf("mounted = %T, want the composition's policy", p)
	}
	if err := checkpoint.Before(bg, p, nil, checkpoint.Attempt); err != nil {
		t.Fatal(err)
	}
	if len(c.points) != 1 || c.points[0] != checkpoint.Attempt {
		t.Fatalf("points = %v", c.points)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}
