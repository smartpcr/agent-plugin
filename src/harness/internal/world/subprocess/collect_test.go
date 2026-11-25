package subprocess_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	spilllocal "github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/sandboxtest"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

const spillOwner = "session-collect"

// newSpillStore is a local spill store in a fresh home under t.TempDir().
func newSpillStore(t *testing.T) (*spilllocal.Store, home.Layout) {
	t.Helper()
	l := home.New(t.TempDir())
	if err := home.Ensure(l); err != nil {
		t.Fatal(err)
	}
	s, err := spilllocal.New(l, spilllocal.Config{MaxSaveBytes: 64 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return s, l
}

// The plan's scenario "Two readers see the same tail" (TC-05b, logic part): with
// 10 KiB written, two readers reading at once see identical bytes, and reading
// drains nothing -- a later reader sees the same tail, and a reader's own offset
// moves only that reader. Run in a ring that wraps and in the default ring.
//
//dsh:test DH-T-EXEC-7
func TestTwoReadersSeeTheSameTail(t *testing.T) {
	data := make([]byte, 10<<10)
	subprocesstest.StreamBytes(data, 0)
	for _, ring := range []int{4 << 10, 0} {
		t.Run(fmt.Sprintf("ring %d", ring), func(t *testing.T) {
			c := subprocess.NewCollector(subprocess.CollectOptions{TailBytes: ring})
			if _, err := c.Write(data); err != nil {
				t.Fatal(err)
			}
			want := data[max(0, len(data)-c.Bound()):]

			var wg sync.WaitGroup
			t.Cleanup(wg.Wait)
			start := make(chan struct{})
			got := make([][]byte, 2)
			for i := range got {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					got[i], _ = c.Reader().Next()
				}()
			}
			close(start)
			wg.Wait()
			if !bytes.Equal(got[0], got[1]) || !bytes.Equal(got[0], want) {
				t.Fatalf("readers saw %d and %d bytes; want both the same %d-byte tail", len(got[0]), len(got[1]), len(want))
			}

			col := c.Collected()
			if !bytes.Equal(col.Tail, want) || col.Written != int64(len(data)) || col.Truncated != (len(want) < len(data)) {
				t.Fatalf("after two reads: tail %d bytes, written %d, truncated %v; the ring was drained", len(col.Tail), col.Written, col.Truncated)
			}
			r := c.Reader()
			if first, skipped := r.Next(); !bytes.Equal(first, want) || skipped != int64(len(data)-len(want)) {
				t.Fatalf("a new reader saw %d bytes, skipped %d; want the tail and %d skipped", len(first), skipped, len(data)-len(want))
			}
			if again, _ := r.Next(); len(again) != 0 || r.Offset() != int64(len(data)) {
				t.Fatalf("the reader's second Next = %d bytes at %d; want nothing new at %d", len(again), r.Offset(), len(data))
			}
			if _, err := c.Write([]byte("more")); err != nil {
				t.Fatal(err)
			}
			if more, _ := r.Next(); string(more) != "more" {
				t.Fatalf("after a write the reader's Next = %q; want \"more\"", more)
			}
			if b, from := c.Since(0); from != int64(len(data)+4-len(b)) {
				t.Fatalf("Since(0) after the write starts at %d with %d bytes; offsets do not match", from, len(b))
			}
		})
	}
}

// The ring holds at most its bound, keeps the last bytes, and its tail never
// starts inside a UTF-8 character even when the bytes kept begin in one.
//
//dsh:test DH-T-EXEC-7
func TestCollectorTailIsBoundedAndCutOnACharacter(t *testing.T) {
	c := subprocess.NewCollector(subprocess.CollectOptions{TailBytes: 8})
	// "ab", four 2-byte runes and "z": 11 bytes, so the ring keeps the last 8,
	// which begin with the second byte of the first "é".
	for _, s := range []string{"a", "bé", "éé", "éz"} {
		if _, err := c.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}
	col := c.Collected()
	if c.Held() > c.Bound() || c.Bound() != 8 {
		t.Fatalf("the ring holds %d bytes with bound %d", c.Held(), c.Bound())
	}
	if string(col.Tail) != "éééz" || col.TailFrom != 4 || !col.Truncated || col.Written != 11 {
		t.Fatalf("tail %q from %d (truncated %v, written %d); want \"éééz\" from 4, truncated, 11 written",
			col.Tail, col.TailFrom, col.Truncated, col.Written)
	}
	small := subprocess.NewCollector(subprocess.CollectOptions{})
	if _, err := small.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if col := small.Collected(); string(col.Tail) != "hi" || col.Truncated || small.Held() > 512 {
		t.Fatalf("a short stream: tail %q truncated %v, held %d; want it whole in a small buffer", col.Tail, col.Truncated, small.Held())
	}
}

// A stream that fits the ring makes no spill file; one that passes the spill
// cap discards its spill, is reported once, and keeps its tail.
//
//dsh:test DH-T-EXEC-7
func TestCollectorSpillCap(t *testing.T) {
	store, l := newSpillStore(t)
	ownerDir := filepath.Join(l.Spill(), spill.OwnerDir(spillOwner))

	fits := subprocess.NewCollector(subprocess.CollectOptions{TailBytes: 64, Spill: store, SpillOwner: spillOwner})
	if _, err := fits.Write(bytes.Repeat([]byte("a"), 64)); err != nil {
		t.Fatal(err)
	}
	if err := fits.Close(); err != nil {
		t.Fatal(err)
	}
	if col := fits.Collected(); col.Spill != nil || col.SpillErr != nil {
		t.Fatalf("a stream that fits the ring: spill %+v, error %v; want neither", col.Spill, col.SpillErr)
	}
	if _, err := os.Stat(ownerDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a stream that fits the ring left %s: %v", ownerDir, err)
	}

	var reported []error
	over := subprocess.NewCollector(subprocess.CollectOptions{TailBytes: 16, Spill: store, SpillOwner: spillOwner,
		SpillBytes: 100, OnSpillError: func(err error) { reported = append(reported, err) }})
	data := make([]byte, 200)
	subprocesstest.StreamBytes(data, 0)
	for i := 0; i < len(data); i += 10 {
		if _, err := over.Write(data[i : i+10]); err != nil {
			t.Fatalf("a write past the spill cap failed: %v", err)
		}
	}
	if err := over.Close(); err != nil {
		t.Fatalf("Close = %v; the spill was already dropped", err)
	}
	col := over.Collected()
	if col.Spill != nil || !herr.Has(col.SpillErr, subprocess.ReasonSpillDropped) || !herr.Has(col.SpillErr, spill.ReasonTooLarge) {
		t.Fatalf("over the cap: spill %+v, error %v; want no spill and %s for %s", col.Spill, col.SpillErr, subprocess.ReasonSpillDropped, spill.ReasonTooLarge)
	}
	if len(reported) != 1 || !bytes.Equal(col.Tail, data[len(data)-16:]) || !col.Truncated {
		t.Fatalf("over the cap: %d errors reported, tail %q (truncated %v); want one report and the last 16 bytes", len(reported), col.Tail, col.Truncated)
	}
	if entries, err := os.ReadDir(ownerDir); err != nil || len(entries) != 0 {
		t.Fatalf("the discarded spill left %v in %s (%v)", entries, ownerDir, err)
	}
}

// failingStore refuses every file.
type failingStore struct{ spill.Store }

func (failingStore) Reserve(owner, name string) spill.Ref {
	return spill.Ref{ID: spill.NewID(owner, name), Owner: owner}
}

func (failingStore) Create(context.Context, spill.Ref, int64) (spill.Writer, error) {
	return nil, errors.New("disk full")
}

// A spill store that refuses the file degrades the stream to memory: every
// write still succeeds, the tail is kept, and the error is reported once.
//
//dsh:test DH-T-EXEC-7
func TestCollectorFailingSpillStoreDegradesToMemory(t *testing.T) {
	var reported int
	c := subprocess.NewCollector(subprocess.CollectOptions{TailBytes: 4, Spill: failingStore{}, SpillOwner: spillOwner,
		OnSpillError: func(error) { reported++ }})
	for _, s := range []string{"abc", "def", "ghi"} {
		if _, err := c.Write([]byte(s)); err != nil {
			t.Fatalf("Write = %v; a refused spill must not fail the stream", err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	col := c.Collected()
	if string(col.Tail) != "fghi" || col.Spill != nil || !herr.Has(col.SpillErr, subprocess.ReasonSpillDropped) || reported != 1 {
		t.Fatalf("tail %q, spill %+v, error %v, %d reports; want \"fghi\", no spill, %s reported once",
			col.Tail, col.Spill, col.SpillErr, reported, subprocess.ReasonSpillDropped)
	}
	if _, err := c.Write([]byte("x")); err == nil {
		t.Fatal("a Write after Close succeeded")
	}
}

// The plan's scenario "Flood is bounded and recoverable" (TC-05b): a re-exec
// helper writes 64 MiB to standard output under the default ring; the ring holds
// no more than its bound, its tail is the last bytes written, the host did not
// buffer the stream, and the spill file holds the whole stream.
//
//dsh:test DH-T-EXEC-7
func TestFloodIsBoundedAndRecoverable(t *testing.T) {
	const total = 64 << 20
	store, _ := newSpillStore(t)
	c := subprocess.NewCollector(subprocess.CollectOptions{Spill: store, SpillOwner: spillOwner, SpillName: "stdout.txt", SpillBytes: total})
	spec := subprocesstest.Spec(t, subprocesstest.RoleStream, fmt.Sprint(total))
	spec.Stdout = c

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	res, err := subprocess.Run(context.Background(), spec, subprocess.RunOptions{Timeout: 120 * time.Second})
	runtime.ReadMemStats(&after)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("Run = %+v, %v; want exit 0", res, err)
	}
	if c.Bound() != subprocess.DefaultRingBytes || c.Held() != c.Bound() {
		t.Fatalf("the ring holds %d bytes, bound %d; want exactly the default %d", c.Held(), c.Bound(), subprocess.DefaultRingBytes)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > total/4 {
		t.Errorf("the host allocated %d bytes while 64 MiB went through; the stream was buffered", grew)
	}
	col := c.Collected()
	want := make([]byte, len(col.Tail))
	subprocesstest.StreamBytes(want, total-int64(len(want)))
	if col.Written != total || !col.Truncated || col.TailFrom != total-int64(len(col.Tail)) || !bytes.Equal(col.Tail, want) {
		t.Fatalf("written %d, truncated %v, tail %d bytes from %d; want the last bytes of %d", col.Written, col.Truncated, len(col.Tail), col.TailFrom, total)
	}
	if len(col.Tail) < subprocess.DefaultRingBytes-3 {
		t.Errorf("the tail is %d bytes; ASCII output keeps the whole ring", len(col.Tail))
	}
	if col.Spill == nil || col.Spill.Bytes != total || col.SpillErr != nil {
		t.Fatalf("spill %+v (error %v); want the whole %d-byte stream saved", col.Spill, col.SpillErr, total)
	}
	f, err := store.Open(context.Background(), spillOwner, col.Spill.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	checkStream(t, f, total)
}

// checkStream reads r to its end and checks it is exactly n bytes of StreamBytes.
func checkStream(t *testing.T, r io.Reader, n int64) {
	t.Helper()
	got := make([]byte, 1<<20)
	want := make([]byte, len(got))
	var off int64
	for {
		k, err := io.ReadFull(r, got)
		if k > 0 {
			subprocesstest.StreamBytes(want[:k], off)
			if !bytes.Equal(got[:k], want[:k]) {
				t.Fatalf("the spill differs from the stream in the %d bytes at offset %d", k, off)
			}
			off += int64(k)
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if off != n {
		t.Fatalf("the spill holds %d bytes; want %d", off, n)
	}
}

// The plan's scenario "Hard limit ends the tree" (TC-05b): a helper that starts
// a grandchild and sleeps is killed, grandchild included, when Run's hard limit
// passes -- on its own, and when a polite stop with a long grace is under way.
//
//dsh:test DH-T-EXEC-7
func TestHardLimitEndsTheTree(t *testing.T) {
	const (
		hardLimit = 2 * time.Second
		grace     = 60 * time.Second
		bound     = 10 * time.Second
	)
	for name, o := range map[string]subprocess.RunOptions{
		"hard limit alone":            {HardLimit: hardLimit, Grace: grace},
		"past a polite stop in grace": {Timeout: 300 * time.Millisecond, HardLimit: hardLimit, Grace: grace},
		"past a revoke hook in bound": {Timeout: 300 * time.Millisecond, HardLimit: hardLimit, Grace: grace},
	} {
		t.Run(name, func(t *testing.T) {
			spec := subprocesstest.Spec(t, subprocesstest.RoleTree)
			if strings.Contains(name, "hook") {
				// A context-respecting hook that waits out its whole bound (Grace).
				spec.Stop.Revoke = func(ctx context.Context) error { <-ctx.Done(); return context.Cause(ctx) }
			}
			start := time.Now()
			res, err := subprocess.Run(context.Background(), spec, o)
			took := time.Since(start)
			if err == nil {
				t.Fatalf("Run = %+v with no error; want the run stopped", res)
			}
			if (o.Timeout == 0 || spec.Stop.Revoke != nil) && herr.Kind(err) != subprocess.ReasonHardLimit {
				t.Fatalf("Run = %v; want %s", err, subprocess.ReasonHardLimit)
			}
			if herr.Has(err, subprocess.ReasonSurvivors) {
				t.Fatalf("Run = %v; the tree was not confirmed gone", err)
			}
			if took > hardLimit+bound {
				t.Fatalf("Run returned after %v; the hard limit %v did not end the run (grace %v)", took, hardLimit, grace)
			}
			var child, grandchild int
			if _, err := fmt.Sscanf(strings.TrimSpace(string(res.Stdout.Bytes)), "tree %d %d", &child, &grandchild); err != nil {
				t.Fatalf("the tree child said %q: %v", res.Stdout.Bytes, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), bound)
			defer cancel()
			procwait.Until(ctx, t, func() bool { return subprocesstest.Exited(child) && subprocesstest.Exited(grandchild) })
		})
	}
}

// waitingStage is a sandbox.Stage whose Confirm never decides: it waits for its
// context, as a host decision that does not come.
type waitingStage struct{}

func (waitingStage) Wrap(argv []string, _ []uintptr, _ sandbox.Pass) (sandbox.Wrapped, error) {
	return sandbox.Wrapped{Argv: argv}, nil
}

func (waitingStage) Confirm(ctx context.Context) (sandbox.Confinement, error) {
	<-ctx.Done()
	return sandbox.Confinement{}, context.Cause(ctx)
}

func (waitingStage) Release(context.Context) error { return nil }

// TestHardLimitBoundsTheLaunch: Run's hard limit counts from the call, so a
// staged launch still in its Confirm is ended when it passes, and the stage's
// tree (a child and its grandchild) is killed past a long Grace and a revoke
// hook that waits out its bound.
//
//dsh:test DH-T-EXEC-7
func TestHardLimitBoundsTheLaunch(t *testing.T) {
	const (
		hardLimit = 2 * time.Second
		bound     = 10 * time.Second
	)
	out := subprocess.NewCollector(subprocess.CollectOptions{})
	t.Cleanup(func() { _ = out.Close() })
	spec := subprocesstest.Spec(t, subprocesstest.RoleTree)
	spec.Launch = sandbox.Staged(sandboxtest.Policy(sandbox.DangerFullAccess), waitingStage{}, "a stage never confirmed")
	spec.Stdout = out
	spec.Stop.Revoke = func(ctx context.Context) error { <-ctx.Done(); return context.Cause(ctx) }
	start := time.Now()
	res, err := subprocess.Run(context.Background(), spec, subprocess.RunOptions{HardLimit: hardLimit, Grace: 60 * time.Second})
	took := time.Since(start)
	if herr.Kind(err) != subprocess.ReasonHardLimit || res.Started {
		t.Fatalf("Run = %+v, %v; want a launch ended with %s", res, err, subprocess.ReasonHardLimit)
	}
	if took > hardLimit+bound {
		t.Fatalf("Run returned after %v; the hard limit %v did not bound the launch", took, hardLimit)
	}
	tail, _ := out.Since(0)
	var child, grandchild int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(tail)), "tree %d %d", &child, &grandchild); err != nil {
		t.Fatalf("the stage's tree said %q: %v", tail, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	procwait.Until(ctx, t, func() bool { return subprocesstest.Exited(child) && subprocesstest.Exited(grandchild) })
}
