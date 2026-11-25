//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	spilllocal "github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill/local"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/procwait"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// bocOuterT is the test the scenarios run under; subprocesstest.Spec wants one.
var bocOuterT *testing.T

const bocSpillOwner = "session-boc-e2e"

// init plays a subprocesstest role when this e2e binary is re-executed as one
// (the stream helper, the tree child or its stubborn grandchild), so the stage
// needs no package-wide TestMain. subprocesstest.Main exits before touching m.
func init() {
	if _, ok := os.LookupEnv(subprocesstest.RoleEnv); ok {
		subprocesstest.Main(nil)
	}
}

type bocWorld struct {
	dir string

	// flood
	total   int64
	store   *spilllocal.Store
	c       *subprocess.Collector
	maxHeld int
	runRes  subprocess.Result
	runErr  error
	took    time.Duration

	// hard limit
	spec      subprocess.Spec
	hardLimit time.Duration

	// readers
	data []byte
	got  [][]byte
}

func (w *bocWorld) cleanup() {
	if w.c != nil {
		_ = w.c.Close()
	}
	if w.dir != "" {
		_ = os.RemoveAll(w.dir)
	}
}

// --- Scenario: Flood is bounded and recoverable ---

func (w *bocWorld) givenFloodHelper(mib int) error {
	w.total = int64(mib) << 20
	w.spec = subprocesstest.Spec(bocOuterT, subprocesstest.RoleStream, fmt.Sprint(w.total))
	return nil
}

func (w *bocWorld) whenRunsUnderDefaultRing() error {
	var err error
	w.dir, err = os.MkdirTemp("", "boc-e2e-")
	if err != nil {
		return err
	}
	l := home.New(w.dir)
	if err := home.Ensure(l); err != nil {
		return err
	}
	w.store, err = spilllocal.New(l, spilllocal.Config{MaxSaveBytes: w.total})
	if err != nil {
		return err
	}
	w.c = subprocess.NewCollector(subprocess.CollectOptions{Spill: w.store, SpillOwner: bocSpillOwner, SpillName: "stdout.txt", SpillBytes: w.total})
	w.spec.Stdout = w.c

	// Sample the ring while the stream flows, so the bound is checked during the
	// flood and not only after it.
	done := make(chan struct{})
	var maxHeld atomic.Int64
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tick := time.NewTicker(time.Millisecond)
		defer tick.Stop()
		for {
			if h := int64(w.c.Held()); h > maxHeld.Load() {
				maxHeld.Store(h)
			}
			select {
			case <-done:
				return
			case <-tick.C:
			}
		}
	}()
	w.runRes, w.runErr = subprocess.Run(context.Background(), w.spec, subprocess.RunOptions{Timeout: 180 * time.Second})
	close(done)
	wg.Wait()
	w.maxHeld = int(maxHeld.Load())
	if w.runErr != nil || w.runRes.ExitCode != 0 {
		return fmt.Errorf("Run = exit %d, %v; want exit 0", w.runRes.ExitCode, w.runErr)
	}
	return nil
}

func (w *bocWorld) thenRingAtBound() error {
	if w.c.Bound() != subprocess.DefaultRingBytes {
		return fmt.Errorf("ring bound %d; want the default %d", w.c.Bound(), subprocess.DefaultRingBytes)
	}
	if w.c.Held() != w.c.Bound() {
		return fmt.Errorf("the ring holds %d bytes after the flood; want exactly its bound %d", w.c.Held(), w.c.Bound())
	}
	if w.maxHeld > w.c.Bound() {
		return fmt.Errorf("the ring held %d bytes during the flood; bound is %d", w.maxHeld, w.c.Bound())
	}
	return nil
}

func (w *bocWorld) thenTailIsLastBytes() error {
	col := w.c.Collected()
	if col.Written != w.total || !col.Truncated {
		return fmt.Errorf("written %d, truncated %v; want %d written and truncated", col.Written, col.Truncated, w.total)
	}
	if len(col.Tail) < subprocess.DefaultRingBytes-3 || len(col.Tail) > subprocess.DefaultRingBytes {
		return fmt.Errorf("the tail is %d bytes; ASCII output keeps the whole %d-byte ring", len(col.Tail), subprocess.DefaultRingBytes)
	}
	if col.TailFrom != w.total-int64(len(col.Tail)) {
		return fmt.Errorf("the tail starts at %d with %d bytes; it does not end at %d", col.TailFrom, len(col.Tail), w.total)
	}
	want := make([]byte, len(col.Tail))
	subprocesstest.StreamBytes(want, col.TailFrom)
	if !bytes.Equal(col.Tail, want) {
		return errors.New("the tail differs from the last bytes the helper wrote")
	}
	return nil
}

func (w *bocWorld) thenSpillHoldsFullStream() error {
	col := w.c.Collected()
	if col.SpillErr != nil || col.Spill == nil || col.Spill.Bytes != w.total {
		return fmt.Errorf("spill %+v (error %v); want the whole %d-byte stream saved", col.Spill, col.SpillErr, w.total)
	}
	f, err := w.store.Open(context.Background(), bocSpillOwner, col.Spill.Path)
	if err != nil {
		return fmt.Errorf("open the spill: %w", err)
	}
	defer f.Close()
	got := make([]byte, 1<<20)
	want := make([]byte, len(got))
	var off int64
	for {
		k, rerr := io.ReadFull(f, got)
		if k > 0 {
			subprocesstest.StreamBytes(want[:k], off)
			if !bytes.Equal(got[:k], want[:k]) {
				return fmt.Errorf("the spill differs from the stream in the %d bytes at offset %d", k, off)
			}
			off += int64(k)
		}
		if errors.Is(rerr, io.EOF) || errors.Is(rerr, io.ErrUnexpectedEOF) {
			break
		}
		if rerr != nil {
			return rerr
		}
	}
	if off != w.total {
		return fmt.Errorf("the spill holds %d bytes; want %d", off, w.total)
	}
	return nil
}

// --- Scenario: Hard limit ends the tree ---

func (w *bocWorld) givenTreeHelper() error {
	w.spec = subprocesstest.Spec(bocOuterT, subprocesstest.RoleTree)
	return nil
}

func (w *bocWorld) whenHardLimitFires(hardSec, graceSec int) error {
	w.hardLimit = time.Duration(hardSec) * time.Second
	start := time.Now()
	w.runRes, w.runErr = subprocess.Run(context.Background(), w.spec, subprocess.RunOptions{
		HardLimit: w.hardLimit,
		Grace:     time.Duration(graceSec) * time.Second,
	})
	w.took = time.Since(start)
	return nil
}

func (w *bocWorld) thenHardLimitReportedWithin(sec int) error {
	if w.runErr == nil {
		return fmt.Errorf("Run = exit %d with no error; want %s", w.runRes.ExitCode, subprocess.ReasonHardLimit)
	}
	if herr.Kind(w.runErr) != subprocess.ReasonHardLimit {
		return fmt.Errorf("Run = %v; want %s", w.runErr, subprocess.ReasonHardLimit)
	}
	if herr.Has(w.runErr, subprocess.ReasonSurvivors) {
		return fmt.Errorf("Run = %v; the tree was not confirmed gone", w.runErr)
	}
	if w.took < w.hardLimit {
		return fmt.Errorf("Run returned after %v, before the %v hard limit", w.took, w.hardLimit)
	}
	if bound := time.Duration(sec) * time.Second; w.took > bound {
		return fmt.Errorf("Run returned after %v; want within %v despite the long grace", w.took, bound)
	}
	return nil
}

func (w *bocWorld) thenBothPidsGone(sec int) error {
	var child, grandchild int
	line := strings.TrimSpace(string(w.runRes.Stdout.Bytes))
	if _, err := fmt.Sscanf(line, "tree %d %d", &child, &grandchild); err != nil {
		return fmt.Errorf("the tree child said %q: %v", line, err)
	}
	if child <= 0 || grandchild <= 0 || child == grandchild {
		return fmt.Errorf("bad pids child=%d grandchild=%d", child, grandchild)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(sec)*time.Second)
	defer cancel()
	if err := procwait.Poll(ctx, func() bool {
		return subprocesstest.Exited(child) && subprocesstest.Exited(grandchild)
	}); err != nil {
		return fmt.Errorf("child %d exited=%v, grandchild %d exited=%v: %w",
			child, subprocesstest.Exited(child), grandchild, subprocesstest.Exited(grandchild), err)
	}
	return nil
}

// --- Scenario: Two readers see the same tail ---

func (w *bocWorld) givenRingWithWritten(ringKiB, writtenKiB int) error {
	w.c = subprocess.NewCollector(subprocess.CollectOptions{TailBytes: ringKiB << 10})
	w.data = make([]byte, writtenKiB<<10)
	subprocesstest.StreamBytes(w.data, 0)
	if _, err := w.c.Write(w.data); err != nil {
		return err
	}
	return nil
}

func (w *bocWorld) whenTwoReadersRead() error {
	var wg sync.WaitGroup
	start := make(chan struct{})
	w.got = make([][]byte, 2)
	for i := range w.got {
		r := w.c.Reader()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			w.got[i], _ = r.Next()
		}()
	}
	close(start)
	wg.Wait()
	return nil
}

func (w *bocWorld) bocTail() []byte {
	return w.data[max(0, len(w.data)-w.c.Bound()):]
}

func (w *bocWorld) thenReadersIdentical() error {
	want := w.bocTail()
	if len(want) == len(w.data) {
		return fmt.Errorf("the ring (%d) holds all %d bytes; the scenario needs a wrapped ring", w.c.Bound(), len(w.data))
	}
	if !bytes.Equal(w.got[0], w.got[1]) {
		return fmt.Errorf("readers saw %d and %d bytes that differ", len(w.got[0]), len(w.got[1]))
	}
	if !bytes.Equal(w.got[0], want) {
		return fmt.Errorf("readers saw %d bytes; want the %d-byte tail of the stream", len(w.got[0]), len(want))
	}
	return nil
}

func (w *bocWorld) thenRingNotDrained() error {
	want := w.bocTail()
	col := w.c.Collected()
	if !bytes.Equal(col.Tail, want) || col.Written != int64(len(w.data)) || !col.Truncated {
		return fmt.Errorf("after two reads: tail %d bytes, written %d, truncated %v; the ring was drained", len(col.Tail), col.Written, col.Truncated)
	}
	if w.c.Held() != w.c.Bound() {
		return fmt.Errorf("the ring holds %d bytes after reading; want its bound %d", w.c.Held(), w.c.Bound())
	}
	third, skipped := w.c.Reader().Next()
	if !bytes.Equal(third, want) || skipped != int64(len(w.data)-len(want)) {
		return fmt.Errorf("a third reader saw %d bytes, skipped %d; want the same tail", len(third), skipped)
	}
	return nil
}

func InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bounded_output_capture(ctx *godog.ScenarioContext) {
	w := &bocWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = bocWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		w.cleanup()
		return c, err
	})

	ctx.Step(`^a re-exec helper that writes (\d+) MiB to stdout$`, w.givenFloodHelper)
	ctx.Step(`^it runs under the default ring size with a spill store in a temp dir$`, w.whenRunsUnderDefaultRing)
	ctx.Step(`^memory held by the ring stays at its bound$`, w.thenRingAtBound)
	ctx.Step(`^the tail equals the last bytes written$`, w.thenTailIsLastBytes)
	ctx.Step(`^the spill file holds the full stream$`, w.thenSpillHoldsFullStream)

	ctx.Step(`^a helper that spawns a grandchild and sleeps$`, w.givenTreeHelper)
	ctx.Step(`^Run's hard limit of (\d+) seconds fires with a (\d+) second grace$`, w.whenHardLimitFires)
	ctx.Step(`^Run reports the hard limit within (\d+) seconds$`, w.thenHardLimitReportedWithin)
	ctx.Step(`^both the child and grandchild pids are gone within (\d+) seconds$`, w.thenBothPidsGone)

	ctx.Step(`^a ring of (\d+) KiB with (\d+) KiB written$`, w.givenRingWithWritten)
	ctx.Step(`^two readers read concurrently$`, w.whenTwoReadersRead)
	ctx.Step(`^both readers observe identical bytes equal to the ring's tail$`, w.thenReadersIdentical)
	ctx.Step(`^the ring is not drained$`, w.thenRingNotDrained)
}

func TestE2E_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bounded_output_capture(t *testing.T) {
	bocOuterT = t
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bounded_output_capture,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_13_sandbox_launch_linux_backends_and_shell_tools_bounded_output_capture.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("bounded output capture scenarios failed")
	}
}
