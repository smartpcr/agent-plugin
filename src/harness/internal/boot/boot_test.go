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
	case status := <-done:
		t.Fatalf("Run returned %d before the bound passed", status)
	default:
	}
	clk.Advance(profile.DefaultLimits().ShutdownTimeout)
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
	if !strings.Contains(out, "shutdown exceeded 20s") || !strings.Contains(out, "worker (x.tree): child tree") {
		t.Errorf("stderr does not list the stuck cleanup:\n%s", out)
	}
}
