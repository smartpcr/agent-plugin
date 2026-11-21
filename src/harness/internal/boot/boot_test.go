package boot

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/diag"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

func TestMain(m *testing.M) { subprocesstest.Main(m) }

// syncBuffer is a goroutine-safe diagnostic stream.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fakeSteps records every step and fakes the tree.
type fakeSteps struct {
	mu    sync.Mutex
	calls []string
	ready []bool // whether Ready was closed when each step ran
}

func (f *fakeSteps) record(s *State, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name)
	select {
	case <-s.App.Ready():
		f.ready = append(f.ready, true)
	default:
		f.ready = append(f.ready, false)
	}
}

func (f *fakeSteps) Facts(_ context.Context, s *State) error   { f.record(s, "facts"); return nil }
func (f *fakeSteps) Home(_ context.Context, s *State) error    { f.record(s, "home"); return nil }
func (f *fakeSteps) Guards(_ context.Context, s *State) error  { f.record(s, "guards"); return nil }
func (f *fakeSteps) Layers(_ context.Context, s *State) error  { f.record(s, "layers"); return nil }
func (f *fakeSteps) Compose(_ context.Context, s *State) error { f.record(s, "compose"); return nil }
func (f *fakeSteps) Tree(_ context.Context, s *State) error {
	f.record(s, "tree")
	s.Host = &fakeHost{f: f, s: s}
	return nil
}
func (f *fakeSteps) Settle(_ context.Context, s *State) error { f.record(s, "settle"); return nil }
func (f *fakeSteps) Audit(_ context.Context, s *State) error {
	f.record(s, "audit")
	s.Audit.Permits = Permits(nil)
	return nil
}

func (f *fakeSteps) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

type fakeHost struct {
	f *fakeSteps
	s *State
}

func (h *fakeHost) Unload()                         { h.f.record(h.s, "unload") }
func (h *fakeHost) Stuck() []string                 { return nil }
func (h *fakeHost) Processes() *subprocess.Registry { return nil }

// TC-06a: the steps run in the documented order, readiness comes after the audit,
// and the audit lists zero permits when none is configured.
func TestBootOrderAndAuditOnFakes(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
	}{{"clean", output.OK}, {"stop status", output.Failed}} {
		t.Run(tc.name, func(t *testing.T) {
			fakes := &fakeSteps{}
			app := launch.NewApp()
			var stderr syncBuffer
			done := make(chan int, 1)
			go func() {
				done <- Run(context.Background(), Options{Profile: profile.Definition{Name: "fake"}, Stderr: &stderr, App: app, Steps: fakes})
			}()
			select {
			case <-app.Ready():
			case status := <-done:
				t.Fatalf("Run returned %d before ready:\n%s", status, stderr.String())
			case <-time.After(10 * time.Second):
				t.Fatal("never ready")
			}
			before := fakes.Calls()
			app.Stop(launch.Completed, tc.status)
			if status := <-done; status != tc.status {
				t.Errorf("status = %d, want %d", status, tc.status)
			}
			want := []string{"facts", "home", "guards", "layers", "compose", "tree", "settle", "audit"}
			if !slices.Equal(before, want) {
				t.Errorf("before ready: %q, want %q", before, want)
			}
			if got := fakes.Calls(); !slices.Equal(got, append(want, "unload")) {
				t.Errorf("calls = %q, want %q then unload", got, want)
			}
			if slices.Contains(fakes.ready[:len(want)], true) {
				t.Errorf("Ready closed before the audit: %v", fakes.ready)
			}
			if got := stderr.String(); strings.Count(got, "dsh: audit: outbound permits: none\n") != 1 {
				t.Errorf("stderr = %q, want one audit line with no permit", got)
			}
		})
	}
}

// failing is a catalogue item whose component always fails to load.
func failing(name string) catalog.Item {
	return catalog.Item{Name: name, Component: cordis.Define(name, func(*cordis.Context, struct{}) error {
		return errors.New("probe refused to start")
	})}
}

// fixture is a temp home, an overlay layer and a request for a profile that
// requires the entry the layer inserts.
func fixture(t *testing.T, layer string, required ...string) (Options, string) {
	t.Helper()
	root := t.TempDir()
	homeDir := filepath.Join(root, "home")
	overlay := filepath.Join(root, "overlay.yml")
	if err := os.WriteFile(overlay, []byte(layer), 0o600); err != nil {
		t.Fatal(err)
	}
	def := profile.Definition{Name: "probe", Bundles: []string{"core"}, Required: required}
	return Options{
		Request: launch.Request{Args: []string{"dsh", "probe"}, Cwd: root, Home: homeDir, Profile: def.Name, Overlays: []string{overlay}},
		Profile: def,
	}, homeDir
}

// TC-06b: a broken required entry fails the boot once: one fatal message and one
// report naming the entry and where it is.
func TestBrokenRequiredEntryFailsBootOnce(t *testing.T) {
	opts, homeDir := fixture(t, "- insert:\n    - id: probe\n      name: x.probe\n", "probe")
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{failing("x.probe")})...)
	if err != nil {
		t.Fatal(err)
	}
	var stderr syncBuffer
	opts.Catalog, opts.Stderr = cat, &stderr
	if status := Run(context.Background(), opts); status != output.Config {
		t.Errorf("status = %d, want %d", status, output.Config)
	}
	got := stderr.String()
	if n := strings.Count(got, "dsh: fatal:"); n != 1 {
		t.Fatalf("%d fatal messages, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "required entry not active: probe") {
		t.Errorf("the message does not name the entry:\n%s", got)
	}
	entries, err := os.ReadDir(filepath.Join(homeDir, "reports"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("reports/ holds %d files (%v), want 1", len(entries), err)
	}
	data, err := os.ReadFile(filepath.Join(homeDir, "reports", entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	report := string(data)
	overlay := opts.Request.Overlays[0]
	for _, want := range []string{"probe (x.probe) at " + overlay + ":2:", "probe refused to start", "[required]", "[boot/required]"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if !strings.Contains(got, entries[0].Name()) {
		t.Errorf("the message does not name the report %s:\n%s", entries[0].Name(), got)
	}
}

// TC-06c: a child that ignores termination holds shutdown past its bound; the
// tree is force-stopped, the status is 124 and no child remains.
func TestShutdownOverrunExits124(t *testing.T) {
	type started struct {
		p                 *subprocess.Process
		child, grandchild int
	}
	got := make(chan started, 1)
	// entered closes when the worker's cleanup starts, so the bound passes while it runs.
	entered := make(chan struct{})
	worker := cordis.Define("x.tree", func(ctx *cordis.Context, _ struct{}) error {
		procs, err := launch.ProcessesKey.Get(ctx)
		if err != nil {
			return err
		}
		_, err = ctx.Effect("child tree", func() (cordis.Disposer, error) {
			spec := subprocesstest.Spec(t, subprocesstest.RoleTree)
			pr, pw := io.Pipe()
			spec.Stdout = pw
			p, err := subprocess.Start(context.Background(), spec)
			if err != nil {
				return nil, err
			}
			remove := procs.Add(p)
			line, err := bufio.NewReader(pr).ReadString('\n')
			go func() { _, _ = io.Copy(io.Discard, pr) }()
			var s started
			s.p = p
			if _, serr := fmt.Sscanf(line, "tree %d %d", &s.child, &s.grandchild); err != nil || serr != nil {
				return nil, fmt.Errorf("tree child said %q: %v %v", line, err, serr)
			}
			got <- s
			// A cleanup that never stops its tree: it waits for an exit that only
			// the forced stop brings.
			return func() error {
				close(entered)
				<-p.Exited()
				remove()
				return nil
			}, nil
		})
		return err
	})
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{{Name: "x.tree", Component: worker}})...)
	if err != nil {
		t.Fatal(err)
	}
	opts, _ := fixture(t, "- insert:\n    - id: worker\n      name: x.tree\n", "worker")
	clk := clock.NewManual(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	app := launch.NewApp()
	var stderr syncBuffer
	opts.Catalog, opts.Stderr, opts.App, opts.Clock = cat, &stderr, app, clk
	done := make(chan int, 1)
	go func() { done <- Run(context.Background(), opts) }()

	select {
	case <-app.Ready():
	case status := <-done:
		t.Fatalf("Run returned %d before ready:\n%s", status, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatal("never ready")
	}
	s := <-got
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = s.p.StopTree(ctx, 0)
	})
	pending := clk.Waiters()
	app.Stop(launch.Terminated, output.OK)
	if !clk.BlockUntil(pending+1, 10*time.Second) {
		t.Fatal("shutdown never started its bound")
	}
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the worker's cleanup never started")
	}
	select {
	case status := <-done:
		t.Fatalf("Run returned %d before the bound passed", status)
	default:
	}
	// The forced stop starts a reserve before the bound and needs no more clock.
	budget := profile.DefaultLimits().ShutdownTimeout
	clk.Advance(budget - forceReserve(budget))
	select {
	case status := <-done:
		if status != output.Forced {
			t.Errorf("status = %d, want %d", status, output.Forced)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after the bound passed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.p.TreeGone(ctx); err != nil {
		t.Errorf("tree not gone: %v", err)
	}
	for _, pid := range []int{s.child, s.grandchild} {
		if subprocesstest.Alive(pid) {
			t.Errorf("process %d is still alive", pid)
		}
	}
	out := stderr.String()
	if !strings.Contains(out, "shutdown not done 15s into its 20s bound") || !strings.Contains(out, "worker (x.tree): child tree") ||
		strings.Contains(out, "bound 20s passed") {
		t.Errorf("stderr does not list the stuck cleanup:\n%s", out)
	}
}

// hungSteps fake every step; the tree's unload never returns and its registry
// holds a stubborn process tree.
type hungSteps struct {
	fakeSteps
	release chan struct{}
	procs   *subprocess.Registry
}

func (h *hungSteps) Tree(_ context.Context, s *State) error {
	h.record(s, "tree")
	s.Host = &hungHost{release: h.release, procs: h.procs}
	return nil
}

type hungHost struct {
	release chan struct{}
	procs   *subprocess.Registry
}

func (h *hungHost) Unload()                         { <-h.release }
func (h *hungHost) Stuck() []string                 { return []string{"hung (x.hung): never returns"} }
func (h *hungHost) Processes() *subprocess.Registry { return h.procs }

// A fatal stop whose shutdown overruns exits 124 within its one bound: the forced
// stop starts a reserve before it, kills a tree whose graceful StopTree is still
// running, and Run returns at the bound although the unload never ends.
func TestFatalShutdownOverrunIsBounded(t *testing.T) {
	p := subprocesstest.Start(t, subprocesstest.Spec(t, subprocesstest.RoleStubborn))
	gctx, cancelGrace := context.WithCancel(context.Background())
	t.Cleanup(cancelGrace)
	graceful := make(chan error, 1)
	go func() { graceful <- p.StopTree(gctx, time.Hour) }()
	procs := subprocess.NewRegistry()
	procs.Add(p)
	steps := &hungSteps{release: make(chan struct{}), procs: procs}
	t.Cleanup(func() { close(steps.release) })
	clk := clock.NewManual(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	app := launch.NewApp()
	var stderr syncBuffer
	done := make(chan int, 1)
	go func() {
		done <- Run(context.Background(), Options{Profile: profile.Definition{Name: "fake"}, Stderr: &stderr, App: app, Clock: clk, Steps: steps})
	}()
	select {
	case <-app.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("never ready")
	}
	pending := clk.Waiters()
	app.Stop(launch.Fatal, output.Fatal)
	budget := profile.DefaultLimits().FatalReleaseTimeout
	reserve := forceReserve(budget)
	if !clk.BlockUntil(pending+1, 10*time.Second) {
		t.Fatal("shutdown never started its bound")
	}
	clk.Advance(budget - reserve)
	// The forced stop kills the tree although the graceful stop holds it.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := p.TreeGone(ctx); err != nil {
		t.Fatalf("tree not killed past a running StopTree: %v", err)
	}
	if !clk.BlockUntil(pending+1, 10*time.Second) {
		t.Fatal("the forced stop is not bounded by the clock")
	}
	select {
	case status := <-done:
		t.Fatalf("Run returned %d before the bound with the unload still running", status)
	case <-time.After(50 * time.Millisecond):
	}
	clk.Advance(reserve)
	select {
	case status := <-done:
		if status != output.Forced {
			t.Errorf("status = %d, want %d", status, output.Forced)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return at the bound")
	}
	if got := clk.Now().Sub(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)); got != budget {
		t.Errorf("shutdown took %s on the clock, want the %s bound", got, budget)
	}
	out := stderr.String()
	for _, want := range []string{"hung (x.hung): never returns", "shutdown bound 2s passed"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr lacks %q:\n%s", want, out)
		}
	}
}

// stopper is a component that stops the app, or reports a fatal error, while it
// initialises.
func stopper(name string, fatal bool) catalog.Item {
	return catalog.Item{Name: name, Component: cordis.Define(name, func(ctx *cordis.Context, _ struct{}) error {
		if fatal {
			d, err := diag.Key.Get(ctx)
			if err != nil {
				return err
			}
			d.Fatal(name, errors.New("cannot continue"))
			return nil
		}
		app, err := launch.AppKey.Get(ctx)
		if err != nil {
			return err
		}
		app.Stop(launch.Completed, output.Failed)
		return nil
	})}
}

// A stop during start-up, ordinary or fatal, ends the settle wait, skips the
// audit and readiness, and exits with the stop's status.
func TestStopDuringStartupIsNeverReady(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fatal  bool
		status int
	}{{"ordinary", false, output.Failed}, {"fatal", true, output.Fatal}} {
		t.Run(tc.name, func(t *testing.T) {
			opts, homeDir := fixture(t, "- insert:\n    - id: stopper\n      name: x.stop\n", "stopper")
			cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{stopper("x.stop", tc.fatal)})...)
			if err != nil {
				t.Fatal(err)
			}
			app := launch.NewApp()
			var stderr syncBuffer
			// The manual clock never reaches StartupTimeout: only the stop ends the wait.
			opts.Catalog, opts.Stderr, opts.App = cat, &stderr, app
			opts.Clock = clock.NewManual(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
			done := make(chan int, 1)
			go func() { done <- Run(context.Background(), opts) }()
			select {
			case status := <-done:
				if status != tc.status {
					t.Errorf("status = %d, want %d", status, tc.status)
				}
			case <-time.After(30 * time.Second):
				t.Fatal("Run kept waiting after a stop during start-up")
			}
			select {
			case <-app.Ready():
				t.Error("Ready closed after a stop during start-up")
			default:
			}
			out := stderr.String()
			if strings.Contains(out, "outbound permits") {
				t.Errorf("the audit ran after a stop during start-up:\n%s", out)
			}
			entries, _ := os.ReadDir(filepath.Join(homeDir, "reports"))
			want := 0
			if tc.fatal {
				want = 1
			}
			if n := strings.Count(out, "dsh: fatal:"); n != want || len(entries) != want {
				t.Errorf("%d fatal messages and %d reports, want %d:\n%s", n, len(entries), want, out)
			}
		})
	}
}

// reloadProbe is a config whose level only its Validate checks.
type reloadProbe struct {
	Level string `json:"level,omitempty"`
}

func (c reloadProbe) Validate() error {
	switch c.Level {
	case "", "debug", "info", "warn":
		return nil
	}
	return &cordis.ConfigError{Issues: []cordis.ConfigIssue{{Path: "level", Message: fmt.Sprintf("level %q is not debug, info or warn", c.Level)}}}
}

// waitFor polls cond until it holds, failing after 20 s.
func waitFor(t *testing.T, what string, log fmt.Stringer, cond func() bool) {
	t.Helper()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	limit := time.After(20 * time.Second)
	for !cond() {
		select {
		case <-tick.C:
		case <-limit:
			t.Fatalf("timed out waiting for %s:\n%s", what, log)
		}
	}
}

// DH-D-BOOT-7: once ready, a profile that reloads applies a valid layer change to
// the running tree and rejects an invalid one with a positioned diagnostic, the
// running component keeping its config; shutdown stops the watch first.
func TestReloadAfterReadyAppliesValidAndRejectsInvalidLayers(t *testing.T) {
	opts, homeDir := fixture(t, "", "probe")
	if err := home.Ensure(home.New(homeDir)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(profile.LayerPath(homeDir, opts.Profile.Name)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(profile.LayerPath(homeDir, opts.Profile.Name), []byte("- insert:\n    - id: probe\n      name: x.reload\n      config:\n        level: info\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var levels []string
	level := func() (string, int) {
		mu.Lock()
		defer mu.Unlock()
		if len(levels) == 0 {
			return "", 0
		}
		return levels[len(levels)-1], len(levels)
	}
	probe := catalog.Item{Name: "x.reload", Config: reflect.TypeFor[reloadProbe](), Component: cordis.Define("x.reload", func(_ *cordis.Context, cfg reloadProbe) error {
		mu.Lock()
		defer mu.Unlock()
		levels = append(levels, cfg.Level)
		return nil
	})}
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{probe})...)
	if err != nil {
		t.Fatal(err)
	}
	app := launch.NewApp()
	var stderr syncBuffer
	opts.Catalog, opts.Stderr, opts.App = cat, &stderr, app
	opts.Profile.Reload = 10 * time.Millisecond
	done := make(chan int, 1)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	wg.Add(1)
	go func() {
		defer wg.Done()
		done <- Run(context.Background(), opts)
	}()
	t.Cleanup(func() { app.Stop(launch.Requested, output.OK) })
	select {
	case <-app.Ready():
	case status := <-done:
		t.Fatalf("Run ended with %d before ready:\n%s", status, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatal("never ready")
	}
	if l, _ := level(); l != "info" {
		t.Fatalf("level at boot = %q", l)
	}

	user := profile.UserLayerPath(homeDir)
	if err := os.WriteFile(user, []byte("- id: probe\n  merge:\n    level: debug\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the valid change to apply", &stderr, func() bool { l, _ := level(); return l == "debug" })
	_, loads := level()

	if err := os.WriteFile(user, []byte("# bad\n- id: probe\n  merge:\n    level: loud\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the rejection", &stderr, func() bool { return strings.Contains(stderr.String(), "reload rejected") })
	out := stderr.String()
	if !strings.Contains(out, user+":4:") || !strings.Contains(out, "probe.config.level") {
		t.Errorf("the rejection is not positioned at %s:4:\n%s", user, out)
	}
	if l, n := level(); l != "debug" || n != loads {
		t.Errorf("after the rejection level = %q after %d loads, want debug after %d", l, n, loads)
	}

	app.Stop(launch.Requested, output.OK)
	select {
	case status := <-done:
		if status != output.OK {
			t.Errorf("status = %d:\n%s", status, stderr.String())
		}
	case <-time.After(30 * time.Second):
		t.Fatal("shutdown did not end")
	}
	if n := strings.Count(stderr.String(), "reload rejected"); n != 1 {
		t.Errorf("rejection told %d times, want once:\n%s", n, stderr.String())
	}
}

// A reload still in flight at shutdown spends part of the one shutdown bound, not
// time on top of it: the forced stop still starts a reserve before the bound and
// Run returns at the bound on the clock.
func TestStuckReloadDrainSharesTheShutdownBound(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	holder := catalog.Item{Name: "x.hold", Component: cordis.Define("x.hold", func(ctx *cordis.Context, _ struct{}) error {
		_, err := ctx.Effect("held", func() (cordis.Disposer, error) {
			return func() error { <-release; return nil }, nil
		})
		return err
	})}
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{holder})...)
	if err != nil {
		t.Fatal(err)
	}
	opts, homeDir := fixture(t, "- insert:\n    - id: hold\n      name: x.hold\n", "hold")
	if err := home.Ensure(home.New(homeDir)); err != nil {
		t.Fatal(err)
	}
	var armed sync.Mutex
	gate := false
	entered := make(chan struct{})
	var enter sync.Once
	opts.Request.ReadFile = func(path string) ([]byte, error) {
		armed.Lock()
		blocked := gate
		armed.Unlock()
		if blocked {
			enter.Do(func() { close(entered) })
			<-release
		}
		return os.ReadFile(path)
	}
	clk := clock.NewManual(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	app := launch.NewApp()
	var stderr syncBuffer
	opts.Catalog, opts.Stderr, opts.App, opts.Clock = cat, &stderr, app, clk
	opts.Profile.Reload = 5 * time.Millisecond
	done := make(chan int, 1)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)
	// runs before wg.Wait: on a failure, let Run reach any bound it still waits for
	t.Cleanup(func() {
		app.Stop(launch.Requested, output.OK)
		once.Do(func() { close(release) })
		for range 5 {
			clk.Advance(time.Hour)
			time.Sleep(10 * time.Millisecond)
		}
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		done <- Run(context.Background(), opts)
	}()
	select {
	case <-app.Ready():
	case status := <-done:
		t.Fatalf("Run returned %d before ready:\n%s", status, stderr.String())
	case <-time.After(30 * time.Second):
		t.Fatal("never ready")
	}
	armed.Lock()
	gate = true
	armed.Unlock()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the reload never read the layers")
	}
	start := clk.Now()
	budget := profile.DefaultLimits().ShutdownTimeout
	reserve := forceReserve(budget)
	pending := clk.Waiters()
	app.Stop(launch.Requested, output.OK)
	step := func(what string, d time.Duration) {
		t.Helper()
		if !clk.BlockUntil(pending+1, 10*time.Second) {
			t.Fatalf("shutdown never waited for %s:\n%s", what, stderr.String())
		}
		clk.Advance(d)
	}
	// the stuck reload's drain takes its reserve; the unload then has only what
	// is left of the bound before the forced stop
	step("the reload", reserve)
	step("the unload", budget-2*reserve)
	waitFor(t, "the forced stop", &stderr, func() bool {
		return strings.Contains(stderr.String(), "shutdown not done 15s into its 20s bound")
	})
	step("the forced stop", reserve)
	select {
	case status := <-done:
		if status != output.Forced {
			t.Errorf("status = %d, want %d", status, output.Forced)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return at the bound")
	}
	if got := clk.Now().Sub(start); got != budget {
		t.Errorf("shutdown took %s on the clock, want the %s bound", got, budget)
	}
	out := stderr.String()
	for _, want := range []string{"a reload still running at shutdown", "stuck cleanups:"} {
		if !strings.Contains(out, want) {
			t.Errorf("stderr lacks %q:\n%s", want, out)
		}
	}
	once.Do(func() { close(release) })
}
