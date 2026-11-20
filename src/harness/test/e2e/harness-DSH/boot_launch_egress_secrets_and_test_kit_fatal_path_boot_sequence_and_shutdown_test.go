//go:build e2e

package e2e

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/builtin"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/clock"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// fpbsOuterT is the suite's *testing.T: subprocesstest helpers need a testing.TB.
var fpbsOuterT *testing.T

// fpbsBuffer is a goroutine-safe diagnostic stream.
type fpbsBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *fpbsBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *fpbsBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// fpbsFakes records every boot step and fakes the tree.
type fpbsFakes struct {
	mu    sync.Mutex
	calls []string
	ready []bool
}

func (f *fpbsFakes) record(s *boot.State, name string) {
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

func (f *fpbsFakes) Facts(_ context.Context, s *boot.State) error  { f.record(s, "facts"); return nil }
func (f *fpbsFakes) Home(_ context.Context, s *boot.State) error   { f.record(s, "home"); return nil }
func (f *fpbsFakes) Guards(_ context.Context, s *boot.State) error { f.record(s, "guards"); return nil }
func (f *fpbsFakes) Layers(_ context.Context, s *boot.State) error { f.record(s, "layers"); return nil }
func (f *fpbsFakes) Compose(_ context.Context, s *boot.State) error {
	f.record(s, "compose")
	return nil
}
func (f *fpbsFakes) Tree(_ context.Context, s *boot.State) error {
	f.record(s, "tree")
	s.Host = &fpbsHost{f: f, s: s}
	return nil
}
func (f *fpbsFakes) Settle(_ context.Context, s *boot.State) error { f.record(s, "settle"); return nil }
func (f *fpbsFakes) Audit(_ context.Context, s *boot.State) error {
	f.record(s, "audit")
	s.Audit.Permits = boot.Permits(nil)
	return nil
}

func (f *fpbsFakes) snapshot() ([]string, []bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls), slices.Clone(f.ready)
}

type fpbsHost struct {
	f *fpbsFakes
	s *boot.State
}

func (h *fpbsHost) Unload()                         { h.f.record(h.s, "unload") }
func (h *fpbsHost) Stuck() []string                 { return nil }
func (h *fpbsHost) Processes() *subprocess.Registry { return nil }

type fpbsTree struct {
	p                 *subprocess.Process
	child, grandchild int
}

type fpbsState struct {
	opts    boot.Options
	homeDir string
	overlay string
	entryID string
	stderr  *fpbsBuffer
	status  int
	ran     bool

	// TC-06a
	fakes       *fpbsFakes
	beforeReady []string

	// TC-06c
	app     *launch.Control
	clk     *clock.Manual
	got     chan fpbsTree
	entered chan struct{}
	tree    fpbsTree
}

// fixture writes an overlay layer into a temp root and returns options for a
// profile that requires the entry the layer inserts; the home is under the root.
func (s *fpbsState) fixture(layer string, required ...string) error {
	root := fpbsOuterT.TempDir()
	s.homeDir = filepath.Join(root, "home")
	s.overlay = filepath.Join(root, "overlay.yml")
	if err := os.WriteFile(s.overlay, []byte(layer), 0o600); err != nil {
		return err
	}
	def := profile.Definition{Name: "probe", Bundles: []string{"core"}, Required: required}
	s.opts = boot.Options{
		Request: launch.Request{Args: []string{"dsh", "probe"}, Cwd: root, Home: s.homeDir, Profile: def.Name, Overlays: []string{s.overlay}},
		Profile: def,
	}
	s.stderr = &fpbsBuffer{}
	s.opts.Stderr = s.stderr
	return nil
}

// --- TC-06b ---

func (s *fpbsState) brokenRequiredEntry(id, name string) error {
	if err := s.fixture(fmt.Sprintf("- insert:\n    - id: %s\n      name: %s\n", id, name), id); err != nil {
		return err
	}
	s.entryID = id
	failing := catalog.Item{Name: name, Component: cordis.Define(name, func(*cordis.Context, struct{}) error {
		return errors.New("probe refused to start")
	})}
	cat, err := catalog.Build(append(builtin.Sets(), []catalog.Item{failing})...)
	if err != nil {
		return err
	}
	s.opts.Catalog = cat
	return nil
}

func (s *fpbsState) bootRuns() error {
	done := make(chan int, 1)
	go func() { done <- boot.Run(context.Background(), s.opts) }()
	select {
	case s.status = <-done:
		s.ran = true
		return nil
	case <-time.After(60 * time.Second):
		return fmt.Errorf("boot did not return:\n%s", s.stderr.String())
	}
}

func (s *fpbsState) exitsConfig(want int) error {
	if want != output.Config {
		return fmt.Errorf("feature says %d but output.Config is %d", want, output.Config)
	}
	return s.exitsWith(want)
}

func (s *fpbsState) exitsWith(want int) error {
	if !s.ran {
		return errors.New("boot has not returned")
	}
	if s.status != want {
		return fmt.Errorf("status = %d, want %d:\n%s", s.status, want, s.stderr.String())
	}
	return nil
}

func (s *fpbsState) oneFatalNaming(id string) error {
	got := s.stderr.String()
	if n := strings.Count(got, "dsh: fatal:"); n != 1 {
		return fmt.Errorf("%d fatal messages, want 1:\n%s", n, got)
	}
	if !strings.Contains(got, "required entry not active: "+id) {
		return fmt.Errorf("the fatal message does not name %q:\n%s", id, got)
	}
	return nil
}

func (s *fpbsState) reportsDir() ([]os.DirEntry, error) {
	entries, err := os.ReadDir(filepath.Join(s.homeDir, "reports"))
	if err != nil {
		return nil, err
	}
	if len(entries) != 1 {
		return nil, fmt.Errorf("reports/ holds %d files, want 1", len(entries))
	}
	return entries, nil
}

func (s *fpbsState) oneReportNaming(_ string, id, name string) error {
	entries, err := s.reportsDir()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(filepath.Join(s.homeDir, "reports", entries[0].Name()))
	if err != nil {
		return err
	}
	report := string(data)
	for _, want := range []string{
		id + " (" + name + ") at " + s.overlay + ":2:",
		"probe refused to start",
		"[required]",
		"[" + string(boot.ReasonRequired) + "]",
	} {
		if !strings.Contains(report, want) {
			return fmt.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	return nil
}

func (s *fpbsState) fatalNamesReport() error {
	entries, err := s.reportsDir()
	if err != nil {
		return err
	}
	if got := s.stderr.String(); !strings.Contains(got, entries[0].Name()) {
		return fmt.Errorf("the fatal message does not name the report %s:\n%s", entries[0].Name(), got)
	}
	return nil
}

// --- TC-06a ---

func (s *fpbsState) fakesForEveryStep() error {
	s.fakes = &fpbsFakes{}
	s.stderr = &fpbsBuffer{}
	s.app = launch.NewApp()
	s.opts = boot.Options{Profile: profile.Definition{Name: "fake"}, Stderr: s.stderr, App: s.app, Steps: s.fakes}
	return nil
}

func (s *fpbsState) runUntilReadyThenStop(code int) error {
	done := make(chan int, 1)
	go func() { done <- boot.Run(context.Background(), s.opts) }()
	select {
	case <-s.app.Ready():
	case status := <-done:
		return fmt.Errorf("boot returned %d before ready:\n%s", status, s.stderr.String())
	case <-time.After(10 * time.Second):
		return errors.New("boot never became ready")
	}
	s.beforeReady, _ = s.fakes.snapshot()
	s.app.Stop(launch.Completed, code)
	select {
	case s.status = <-done:
		s.ran = true
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("boot did not return after the stop")
	}
}

func (s *fpbsState) stepsInOrder(list string) error {
	want := strings.Split(list, ", ")
	if !slices.Equal(s.beforeReady, want) {
		return fmt.Errorf("steps before ready = %q, want %q", s.beforeReady, want)
	}
	return nil
}

func (s *fpbsState) readyAfterAudit() error {
	calls, ready := s.fakes.snapshot()
	n := slices.Index(calls, "audit")
	if n < 0 {
		return fmt.Errorf("audit never ran: %q", calls)
	}
	if slices.Contains(ready[:n+1], true) {
		return fmt.Errorf("Ready was closed before or during the audit: %v", ready)
	}
	return nil
}

func (s *fpbsState) unloadedAfterAudit() error {
	calls, _ := s.fakes.snapshot()
	if len(calls) == 0 || calls[len(calls)-1] != "unload" || slices.Index(calls, "unload") < slices.Index(calls, "audit") {
		return fmt.Errorf("calls = %q, want unload once after the audit", calls)
	}
	return nil
}

func (s *fpbsState) zeroPermits() error {
	a := boot.Audit{Permits: boot.Permits(nil)}
	if n := len(a.Allowed()); n != 0 {
		return fmt.Errorf("%d permits allowed with none configured", n)
	}
	got := s.stderr.String()
	if strings.Count(got, "dsh: audit: outbound permits: none\n") != 1 {
		return fmt.Errorf("stderr = %q, want one audit line with no permit", got)
	}
	return nil
}

// --- TC-06c ---

func (s *fpbsState) stubbornChildTree() error {
	s.got = make(chan fpbsTree, 1)
	s.entered = make(chan struct{})
	got, entered := s.got, s.entered
	worker := cordis.Define("x.tree", func(ctx *cordis.Context, _ struct{}) error {
		procs, err := launch.ProcessesKey.Get(ctx)
		if err != nil {
			return err
		}
		_, err = ctx.Effect("child tree", func() (cordis.Disposer, error) {
			spec := subprocesstest.Spec(fpbsOuterT, subprocesstest.RoleTree)
			pr, pw := io.Pipe()
			spec.Stdout = pw
			p, err := subprocess.Start(context.Background(), spec)
			if err != nil {
				return nil, err
			}
			remove := procs.Add(p)
			line, err := bufio.NewReader(pr).ReadString('\n')
			go func() { _, _ = io.Copy(io.Discard, pr) }()
			t := fpbsTree{p: p}
			if _, serr := fmt.Sscanf(line, "tree %d %d", &t.child, &t.grandchild); err != nil || serr != nil {
				return nil, fmt.Errorf("tree child said %q: %v %v", line, err, serr)
			}
			got <- t
			// A cleanup that never stops its tree: only the forced stop ends it.
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
		return err
	}
	if err := s.fixture("- insert:\n    - id: worker\n      name: x.tree\n", "worker"); err != nil {
		return err
	}
	s.opts.Catalog = cat
	return nil
}

func (s *fpbsState) manualClock() error {
	s.clk = clock.NewManual(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	s.app = launch.NewApp()
	s.opts.Clock, s.opts.App = s.clk, s.app
	return nil
}

func (s *fpbsState) shutdownOverruns() error {
	done := make(chan int, 1)
	go func() { done <- boot.Run(context.Background(), s.opts) }()
	select {
	case <-s.app.Ready():
	case status := <-done:
		return fmt.Errorf("boot returned %d before ready:\n%s", status, s.stderr.String())
	case <-time.After(60 * time.Second):
		return errors.New("boot never became ready")
	}
	select {
	case s.tree = <-s.got:
	case <-time.After(10 * time.Second):
		return errors.New("the child tree never reported its pids")
	}
	pending := s.clk.Waiters()
	s.app.Stop(launch.Terminated, output.OK)
	if !s.clk.BlockUntil(pending+1, 10*time.Second) {
		return errors.New("shutdown never started its bound on the clock")
	}
	select {
	case <-s.entered:
	case <-time.After(10 * time.Second):
		return errors.New("the worker's cleanup never started")
	}
	select {
	case status := <-done:
		return fmt.Errorf("boot returned %d before the bound passed", status)
	default:
	}
	// Boot keeps the end of the bound (a quarter, at most 5 s) for the forced
	// stop; passing the rest of it starts that stop, which needs no more clock.
	budget := profile.DefaultLimits().ShutdownTimeout
	s.clk.Advance(budget - min(budget/4, 5*time.Second))
	select {
	case s.status = <-done:
		s.ran = true
		return nil
	case <-time.After(30 * time.Second):
		return errors.New("boot did not return after the bound passed")
	}
}

func (s *fpbsState) treeGone() error {
	if output.Forced != 124 {
		return fmt.Errorf("output.Forced = %d, want 124", output.Forced)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.tree.p.TreeGone(ctx); err != nil {
		return fmt.Errorf("tree not gone: %w", err)
	}
	for _, pid := range []int{s.tree.child, s.tree.grandchild} {
		if subprocesstest.Alive(pid) {
			return fmt.Errorf("process %d is still alive", pid)
		}
	}
	return nil
}

func (s *fpbsState) stuckListed() error {
	out := s.stderr.String()
	if !strings.Contains(out, "worker (x.tree): child tree") {
		return fmt.Errorf("stderr does not list the stuck cleanup:\n%s", out)
	}
	return nil
}

func InitializeScenario_boot_launch_egress_secrets_and_test_kit_fatal_path_boot_sequence_and_shutdown(ctx *godog.ScenarioContext) {
	s := &fpbsState{}
	ctx.After(func(c context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.tree.p != nil {
			sc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = s.tree.p.StopTree(sc, 0)
		}
		return c, err
	})

	ctx.Step(`^a temp home and a layer with a broken required entry "([^"]*)" named "([^"]*)"$`, s.brokenRequiredEntry)
	ctx.Step(`^boot runs$`, s.bootRuns)
	ctx.Step(`^boot exits with the configuration status (\d+)$`, s.exitsConfig)
	ctx.Step(`^exactly one fatal message is printed naming the entry "([^"]*)"$`, s.oneFatalNaming)
	ctx.Step(`^exactly one file exists in "([^"]*)" naming the entry "([^"]*)" and "([^"]*)"$`, s.oneReportNaming)
	ctx.Step(`^the fatal message names the report file$`, s.fatalNamesReport)

	ctx.Step(`^in-memory fakes for every boot step with no permit configured$`, s.fakesForEveryStep)
	ctx.Step(`^boot runs until ready and is stopped with status (\d+)$`, s.runUntilReadyThenStop)
	ctx.Step(`^the steps were called in the order "([^"]*)"$`, s.stepsInOrder)
	ctx.Step(`^readiness came only after the audit$`, s.readyAfterAudit)
	ctx.Step(`^the tree was unloaded after the audit$`, s.unloadedAfterAudit)
	ctx.Step(`^the audit lists zero permits$`, s.zeroPermits)
	ctx.Step(`^boot exits with status (\d+)$`, s.exitsWith)

	ctx.Step(`^a composed tree whose child process ignores termination$`, s.stubbornChildTree)
	ctx.Step(`^a shutdown budget shortened by the test kit's manual clock$`, s.manualClock)
	ctx.Step(`^boot is ready and shutdown runs past its bound$`, s.shutdownOverruns)
	ctx.Step(`^the process tree is killed and no child remains$`, s.treeGone)
	ctx.Step(`^the stuck cleanup is listed on the diagnostic stream$`, s.stuckListed)
}

func TestE2E_boot_launch_egress_secrets_and_test_kit_fatal_path_boot_sequence_and_shutdown(t *testing.T) {
	fpbsOuterT = t
	defer func() { fpbsOuterT = nil }()
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_boot_launch_egress_secrets_and_test_kit_fatal_path_boot_sequence_and_shutdown,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"boot_launch_egress_secrets_and_test_kit_fatal_path_boot_sequence_and_shutdown.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
