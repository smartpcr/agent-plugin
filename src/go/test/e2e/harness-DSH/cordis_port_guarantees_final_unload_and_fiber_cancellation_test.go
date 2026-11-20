//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// fuTimeout bounds every blocking step so a deadlock fails instead of hanging.
const fuTimeout = 5 * time.Second

// fuSettle is how long a step waits to observe that something did not happen.
const fuSettle = 50 * time.Millisecond

// fuService is the value fiber A provides; its disposer marks it closed.
type fuService struct{ closed atomic.Bool }

type fuOrder struct {
	mu    sync.Mutex
	lines []string
}

func (o *fuOrder) add(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.lines = append(o.lines, s)
}

func (o *fuOrder) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Join(o.lines, ",")
}

type finalUnloadWorld struct {
	root *cordis.Context

	// concurrency scenario
	fiber   *cordis.Fiber
	release chan struct{}
	ran     [3]atomic.Int32
	results chan bool

	// dependency scenario
	providerFiber *cordis.Fiber
	consumerFiber *cordis.Fiber
	order         fuOrder
	sawLive       atomic.Bool
	consumerSaw   atomic.Bool

	// cancellation scenario
	cancelFiber   *cordis.Fiber
	effectExited  chan struct{}
	effectCause   atomic.Pointer[error]
	effectReturns atomic.Int64
	waitReturns   atomic.Int64
	seq           atomic.Int64

	// preservation scenario
	moduleDir   string
	goldenPath  string
	suiteOutput string
	suiteErr    error
	goldenOut   string
	goldenErr   error
}

func fuWithin(what string, fn func()) error {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
		return nil
	case <-time.After(fuTimeout):
		return fmt.Errorf("%s did not finish within %s", what, fuTimeout)
	}
}

func fuWait(f *cordis.Fiber) error {
	var err error
	if werr := fuWithin("wait "+f.Name(), func() { err = f.Wait() }); werr != nil {
		return werr
	}
	return err
}

func (w *finalUnloadWorld) newRoot() {
	if w.root != nil {
		return
	}
	root := cordis.New()
	root.Loggers().ClearExporters()
	root.Loggers().AddExporter(&cordis.MemoryExporter{})
	w.root = root
}

// --- Scenario: Unload is final under concurrency ---

func (w *finalUnloadWorld) aFiberWithThreeBlockingEffects() error {
	w.newRoot()
	w.release = make(chan struct{})
	fiber, err := w.root.Apply("blocking", func(ctx *cordis.Context) error {
		for i := range w.ran {
			if _, err := ctx.Effect(fmt.Sprintf("block-%d", i), func() (cordis.Disposer, error) {
				return func() error {
					<-w.release
					w.ran[i].Add(1)
					return nil
				}, nil
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	w.fiber = fiber
	if err := fuWait(fiber); err != nil {
		return fmt.Errorf("fiber failed to load: %w", err)
	}
	if n := len(fiber.Effects()); n != 3 {
		return fmt.Errorf("want 3 effects registered, got %d", n)
	}
	return nil
}

func (w *finalUnloadWorld) allRanOnce() bool {
	for i := range w.ran {
		if w.ran[i].Load() != 1 {
			return false
		}
	}
	return true
}

func (w *finalUnloadWorld) twoGoroutinesDispose() error {
	w.results = make(chan bool, 2)
	start := make(chan struct{})
	for range 2 {
		go func() {
			<-start
			w.fiber.Dispose()
			w.results <- w.allRanOnce()
		}()
	}
	close(start)
	return nil
}

func (w *finalUnloadWorld) neitherDisposeReturnsWhileBlocked() error {
	select {
	case <-w.results:
		return errors.New("Dispose returned while disposers were still blocked")
	case <-time.After(fuSettle):
		return nil
	}
}

func (w *finalUnloadWorld) disposersReleased() error {
	close(w.release)
	return nil
}

func (w *finalUnloadWorld) bothDisposeReturnAfterAllRanOnce() error {
	for i := range 2 {
		select {
		case ok := <-w.results:
			if !ok {
				return fmt.Errorf("Dispose call %d returned before every disposer ran exactly once", i+1)
			}
		case <-time.After(fuTimeout):
			return fmt.Errorf("Dispose call %d did not return within %s", i+1, fuTimeout)
		}
	}
	if err := fuWait(w.fiber); err != nil {
		return fmt.Errorf("Wait after dispose: %w", err)
	}
	for i := range w.ran {
		if n := w.ran[i].Load(); n != 1 {
			return fmt.Errorf("disposer %d ran %d times, want exactly 1", i, n)
		}
	}
	if s := w.fiber.State(); s != cordis.Disposed {
		return fmt.Errorf("fiber state = %v, want Disposed", s)
	}
	return nil
}

func (w *finalUnloadWorld) laterEffectReturnsDisposedError() error {
	_, err := w.fiber.Ctx().Effect("after", func() (cordis.Disposer, error) { return nil, nil })
	if !errors.Is(err, cordis.ErrInactive) {
		return fmt.Errorf("Effect on disposed fiber returned %v, want %v", err, cordis.ErrInactive)
	}
	return nil
}

// --- Scenario: Dependents unload before their provider ---

func (w *finalUnloadWorld) fiberAProviding(name string) error {
	w.newRoot()
	fiber, err := w.root.Apply("A", func(ctx *cordis.Context) error {
		svc := &fuService{}
		if _, err := ctx.Provide(name, svc); err != nil {
			return err
		}
		_, err := ctx.Effect("A-resource", func() (cordis.Disposer, error) {
			return func() error {
				svc.closed.Store(true)
				w.order.add("A")
				return nil
			}, nil
		})
		return err
	})
	if err != nil {
		return err
	}
	w.providerFiber = fiber
	return fuWait(fiber)
}

func (w *finalUnloadWorld) fiberBInjecting(name string) error {
	fiber, err := w.root.Apply("B", func(ctx *cordis.Context) error {
		value, err := ctx.Get(name)
		if err != nil {
			return err
		}
		svc, ok := value.(*fuService)
		if !ok {
			return fmt.Errorf("injected %q is %T, want *fuService", name, value)
		}
		_, err = ctx.Effect("B-use", func() (cordis.Disposer, error) {
			return func() error {
				w.consumerSaw.Store(true)
				w.sawLive.Store(!svc.closed.Load())
				w.order.add("B")
				return nil
			}, nil
		})
		return err
	}, name)
	if err != nil {
		return err
	}
	w.consumerFiber = fiber
	if err := fuWait(fiber); err != nil {
		return err
	}
	if s := fiber.State(); s != cordis.Active {
		return fmt.Errorf("fiber B state = %v, want Active", s)
	}
	return nil
}

func (w *finalUnloadWorld) fiberAUnloaded() error {
	return fuWithin("dispose A", w.providerFiber.Dispose)
}

func (w *finalUnloadWorld) bSawServiceLive() error {
	if !w.consumerSaw.Load() {
		return errors.New("fiber B's disposer did not run when its provider unloaded")
	}
	if !w.sawLive.Load() {
		return errors.New("fiber B's disposer observed fiber A's service already released")
	}
	return nil
}

func (w *finalUnloadWorld) aRanAfterB() error {
	if got := w.order.String(); got != "B,A" {
		return fmt.Errorf("disposer order = %q, want \"B,A\"", got)
	}
	return nil
}

// --- Scenario: Fiber context is cancelled by unload with a cause ---

func (w *finalUnloadWorld) loadedFiberWithBlockingEffect() error {
	w.newRoot()
	w.effectExited = make(chan struct{})
	fiber, err := w.root.Apply("watcher", func(ctx *cordis.Context) error {
		life := ctx.Lifetime()
		_, err := ctx.Effect("watch", func() (cordis.Disposer, error) {
			exited := w.effectExited
			go func() {
				<-life.Done()
				cause := context.Cause(life)
				w.effectCause.Store(&cause)
				w.effectReturns.Store(w.seq.Add(1))
				close(exited)
			}()
			return func() error { <-exited; return nil }, nil
		})
		return err
	})
	if err != nil {
		return err
	}
	w.cancelFiber = fiber
	if err := fuWait(fiber); err != nil {
		return err
	}
	if s := fiber.State(); s != cordis.Active {
		return fmt.Errorf("fiber state = %v, want Active", s)
	}
	if err := fiber.Lifetime().Err(); err != nil {
		return fmt.Errorf("lifetime of a loaded fiber is already done: %v", err)
	}
	select {
	case <-w.effectExited:
		return errors.New("effect returned before the fiber was unloaded")
	case <-time.After(fuSettle):
		return nil
	}
}

func (w *finalUnloadWorld) fiberUnloaded() error {
	return fuWithin("dispose", w.cancelFiber.Dispose)
}

func (w *finalUnloadWorld) blockedEffectReturns() error {
	select {
	case <-w.effectExited:
		return nil
	case <-time.After(fuTimeout):
		return fmt.Errorf("effect did not return within %s of unload", fuTimeout)
	}
}

func (w *finalUnloadWorld) causeIsUnloadCause() error {
	p := w.effectCause.Load()
	if p == nil {
		return errors.New("effect recorded no cause")
	}
	if !errors.Is(*p, cordis.ErrInactive) {
		return fmt.Errorf("context.Cause seen by the effect = %v, want %v", *p, cordis.ErrInactive)
	}
	if got := context.Cause(w.cancelFiber.Ctx().Lifetime()); !errors.Is(got, cordis.ErrInactive) {
		return fmt.Errorf("context.Cause(fiber lifetime) = %v, want %v", got, cordis.ErrInactive)
	}
	return nil
}

func (w *finalUnloadWorld) waitReturnsAfterEffect() error {
	if err := fuWait(w.cancelFiber); err != nil {
		return fmt.Errorf("Wait: %w", err)
	}
	w.waitReturns.Store(w.seq.Add(1))
	e, r := w.effectReturns.Load(), w.waitReturns.Load()
	if e == 0 || r <= e {
		return fmt.Errorf("Wait returned (seq %d) before the effect returned (seq %d)", r, e)
	}
	if s := w.cancelFiber.State(); s != cordis.Disposed {
		return fmt.Errorf("fiber state = %v, want Disposed", s)
	}
	return nil
}

// --- Scenario: Existing cordis behaviour preserved ---

func fuModuleDir() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if data, err := os.ReadFile(filepath.Join(dir, "go.mod")); err == nil &&
			strings.Contains(string(data), "module github.com/smartpcr/agent-plugin/src/go\n") {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("module github.com/smartpcr/agent-plugin/src/go not found above the working directory")
		}
		dir = parent
	}
}

func (w *finalUnloadWorld) committedGolden(rel string) error {
	dir, err := fuModuleDir()
	if err != nil {
		return err
	}
	w.moduleDir = dir
	w.goldenPath = filepath.Join(dir, filepath.FromSlash(rel))
	data, err := os.ReadFile(w.goldenPath)
	if err != nil {
		return fmt.Errorf("committed golden missing: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return fmt.Errorf("committed golden %s is empty", rel)
	}
	return nil
}

// fuRaceSupported reports whether this host can build with -race (cgo and a C compiler).
func fuRaceSupported() bool {
	out, err := exec.Command("go", "env", "CGO_ENABLED", "CC").Output()
	if err != nil {
		return false
	}
	fields := strings.Fields(string(out))
	if len(fields) < 2 || fields[0] != "1" {
		return false
	}
	_, err = exec.LookPath(fields[1])
	return err == nil
}

func (w *finalUnloadWorld) goTest(args ...string) (string, error) {
	full := []string{"test", "-count=1"}
	if fuRaceSupported() {
		full = append(full, "-race")
	}
	full = append(full, args...)
	cmd := exec.Command("go", full...)
	cmd.Dir = w.moduleDir
	cmd.Env = append(os.Environ(), "GOFLAGS=")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (w *finalUnloadWorld) existingSuitesRun() error {
	w.suiteOutput, w.suiteErr = w.goTest("-shuffle=on", "./cordis/...", "./cmd/cordis-sample")
	w.goldenOut, w.goldenErr = w.goTest("-count=20", "-run", "^TestOutputMatchesGolden$", "./cmd/cordis-sample")
	return nil
}

func (w *finalUnloadWorld) existingCordisTestsPass() error {
	if w.suiteErr != nil {
		return fmt.Errorf("existing cordis tests failed: %v\n%s", w.suiteErr, w.suiteOutput)
	}
	for _, pkg := range []string{"/src/go/cordis", "/src/go/cmd/cordis-sample"} {
		if !strings.Contains(w.suiteOutput, pkg) {
			return fmt.Errorf("package %s was not tested:\n%s", pkg, w.suiteOutput)
		}
	}
	return nil
}

func (w *finalUnloadWorld) sampleMatchesGolden() error {
	if w.goldenErr != nil {
		return fmt.Errorf("cordis-sample output differs from %s: %v\n%s", w.goldenPath, w.goldenErr, w.goldenOut)
	}
	if !strings.Contains(w.goldenOut, "ok") {
		return fmt.Errorf("golden comparison did not report ok:\n%s", w.goldenOut)
	}
	return nil
}

func InitializeScenario_cordis_port_guarantees_final_unload_and_fiber_cancellation(ctx *godog.ScenarioContext) {
	var w *finalUnloadWorld
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		w = &finalUnloadWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if w != nil && w.root != nil {
			if w.release != nil {
				select {
				case <-w.release:
				default:
					close(w.release)
				}
			}
			_ = fuWithin("dispose root", w.root.Dispose)
		}
		runtime.Gosched()
		return c, nil
	})

	ctx.Step(`^a fiber with three effects whose disposers block until released$`, func() error { return w.aFiberWithThreeBlockingEffects() })
	ctx.Step(`^two goroutines call Dispose at once$`, func() error { return w.twoGoroutinesDispose() })
	ctx.Step(`^neither Dispose returns while the disposers are blocked$`, func() error { return w.neitherDisposeReturnsWhileBlocked() })
	ctx.Step(`^the disposers are released$`, func() error { return w.disposersReleased() })
	ctx.Step(`^both Dispose calls return only after all three disposers ran exactly once$`, func() error { return w.bothDisposeReturnAfterAllRanOnce() })
	ctx.Step(`^a later Effect call on the fiber returns the disposed error$`, func() error { return w.laterEffectReturnsDisposedError() })

	ctx.Step(`^fiber A providing the service "([^"]+)"$`, func(name string) error { return w.fiberAProviding(name) })
	ctx.Step(`^fiber B injecting the service "([^"]+)"$`, func(name string) error { return w.fiberBInjecting(name) })
	ctx.Step(`^fiber A is unloaded$`, func() error { return w.fiberAUnloaded() })
	ctx.Step(`^fiber B's disposer observed fiber A's service still live$`, func() error { return w.bSawServiceLive() })
	ctx.Step(`^fiber A's disposer ran after fiber B's$`, func() error { return w.aRanAfterB() })

	ctx.Step(`^a loaded fiber whose effect blocks on the fiber lifetime being done$`, func() error { return w.loadedFiberWithBlockingEffect() })
	ctx.Step(`^the fiber is unloaded$`, func() error { return w.fiberUnloaded() })
	ctx.Step(`^the blocked effect returns$`, func() error { return w.blockedEffectReturns() })
	ctx.Step(`^the context cause of the fiber lifetime is the unload cause$`, func() error { return w.causeIsUnloadCause() })
	ctx.Step(`^Wait on the fiber returns after the effect returned$`, func() error { return w.waitReturnsAfterEffect() })

	ctx.Step(`^the committed cordis-sample golden "([^"]+)"$`, func(rel string) error { return w.committedGolden(rel) })
	ctx.Step(`^the existing cordis and cordis-sample test suites run$`, func() error { return w.existingSuitesRun() })
	ctx.Step(`^the existing cordis tests pass$`, func() error { return w.existingCordisTestsPass() })
	ctx.Step(`^the cordis-sample output matches the committed golden$`, func() error { return w.sampleMatchesGolden() })
}

func TestE2E_cordis_port_guarantees_final_unload_and_fiber_cancellation(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "cordis_port_guarantees_final_unload_and_fiber_cancellation",
		ScenarioInitializer: InitializeScenario_cordis_port_guarantees_final_unload_and_fiber_cancellation,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"cordis_port_guarantees_final_unload_and_fiber_cancellation.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("godog scenarios failed")
	}
}
