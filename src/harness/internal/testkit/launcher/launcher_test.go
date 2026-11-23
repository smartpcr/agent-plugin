package launcher_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// A run that outlives its bound while a grandchild holds its output returns
// promptly with ErrTimeout, and the whole tree, grandchild included, is gone.
//
//dsh:test DH-T-NODEPROC-5
func TestRunTimeoutStopsTheTreeHoldingItsOutput(t *testing.T) {
	bin := launcher.Build(t, ".", "./testdata/echo")
	start := time.Now()
	h, err := launcher.Run(t, 2*time.Second, launcher.Program{Argv: []string{bin, "tree"}})
	if !errors.Is(err, launcher.ErrTimeout) {
		t.Fatalf("Run = %v, want ErrTimeout", err)
	}
	if took := time.Since(start); took > 2*time.Second+3*launcher.Grace {
		t.Errorf("the timed-out run took %s to return", took)
	}
	grandchild, perr := strconv.Atoi(strings.TrimSpace(h.Stdout.String()))
	if perr != nil || grandchild == 0 {
		t.Fatalf("no grandchild pid in %q", h.Stdout.String())
	}
	// Exited, not !Alive: the orphaned grandchild is reaped by its reaper on its
	// own time, and kill(pid, 0) still finds the zombie until then (M1-G-BOOT-36).
	for _, pid := range []int{h.Process.Pid(), grandchild} {
		if !subprocesstest.Exited(pid) {
			t.Errorf("pid %d survives the timed-out run", pid)
		}
	}
}

// A DSH_HOME entry in Program.Env does not move the program off its private home.
func TestProgramEnvCannotOverrideTheHome(t *testing.T) {
	bin := launcher.Build(t, ".", "./testdata/echo")
	other := t.TempDir()
	s := launcher.Start(t, launcher.Program{Argv: []string{bin}, Env: []string{"DSH_HOME=" + other, "dsh_home=" + other}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var home string
	if err := s.Client.Call(ctx, "home", nil, &home); err != nil {
		t.Fatal(err)
	}
	if home != s.Home.Root() {
		t.Errorf("the program runs on %q, want its private home %q", home, s.Home.Root())
	}
}

// A launched program's environment loses every name the scenario's secret store
// names, here the non-credential-shaped GITHUB_PAT of secret://github/pat, and
// every credential-shaped name even without a store; the scenario's own entries
// and a plain host variable reach it (SP-11).
//
//dsh:test DH-T-EXEC-3
func TestStartDropsTheStoresSecretNames(t *testing.T) {
	bin := launcher.Build(t, ".", "./testdata/echo")
	t.Setenv("GITHUB_PAT", "ghp-dummy-never-leaks")
	t.Setenv("LAUNCHER_DB_PASSWD", "dummy")
	t.Setenv("LAUNCHER_PLAIN", "kept")
	store := secret.NewStore()
	store.Name(secret.MustParse("secret://github/pat"))
	names := []string{"GITHUB_PAT", "LAUNCHER_DB_PASSWD", "LAUNCHER_PLAIN", "LAUNCHER_ENTRY"}
	for name, c := range map[string]struct {
		store *secret.Store
		want  map[string]bool
	}{
		"with the store": {store, map[string]bool{"GITHUB_PAT": false, "LAUNCHER_DB_PASSWD": false, "LAUNCHER_PLAIN": true, "LAUNCHER_ENTRY": true}},
		"without one":    {nil, map[string]bool{"GITHUB_PAT": true, "LAUNCHER_DB_PASSWD": false, "LAUNCHER_PLAIN": true, "LAUNCHER_ENTRY": true}},
	} {
		t.Run(name, func(t *testing.T) {
			s := launcher.Start(t, launcher.Program{Argv: []string{bin}, Secrets: c.store, Env: []string{"LAUNCHER_ENTRY=1"}})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			var got map[string]bool
			if err := s.Client.Call(ctx, "env", names, &got); err != nil {
				t.Fatal(err)
			}
			for _, n := range names {
				if got[n] != c.want[n] {
					t.Errorf("%s set = %v in the program; want %v", n, got[n], c.want[n])
				}
			}
		})
	}
}

// TC-06b/TC-06c: a launched program answers JSON-RPC on its stdio with the temp
// home it was given; when the test that launched it ends, the program, the
// grandchild it started in the home, and the home itself are gone.
func TestLaunchedProgramAndHomeTornDownByCleanup(t *testing.T) {
	bin := launcher.Build(t, ".", "./testdata/echo")
	var root string
	var pids []int
	t.Run("launch", func(t *testing.T) {
		s := launcher.Start(t, launcher.Program{Argv: []string{bin}})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var home string
		if err := s.Client.Call(ctx, "home", nil, &home); err != nil || home != s.Home.Root() {
			t.Fatalf("home = %q (%v), want %q", home, err, s.Home.Root())
		}
		var echoed map[string]int
		if err := s.Client.Call(ctx, "echo", map[string]int{"n": 7}, &echoed); err != nil || echoed["n"] != 7 {
			t.Fatalf("echo = %v (%v)", echoed, err)
		}
		var grandchild int
		if err := s.Client.Call(ctx, "spawn", nil, &grandchild); err != nil || grandchild == 0 {
			t.Fatalf("spawn = %d (%v)", grandchild, err)
		}
		root, pids = s.Home.Root(), []int{s.Process.Pid(), grandchild}
		for _, pid := range pids {
			if !subprocesstest.Alive(pid) {
				t.Fatalf("pid %d is not running while the test runs", pid)
			}
		}
	})
	if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the home %s survives its test (%v)", root, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for _, pid := range pids {
		for !subprocesstest.Exited(pid) && time.Now().Before(deadline) {
			<-time.After(20 * time.Millisecond)
		}
		if !subprocesstest.Exited(pid) {
			t.Errorf("pid %d survives its test", pid)
		}
	}
}

// Build passes every tag to the compiler, and Run returns a program that exits
// in time with its output; Await then reports the exit.
func TestBuildPassesItsTagsAndRunReturnsAnExitedProgram(t *testing.T) {
	plain := launcher.Build(t, ".", "./testdata/echo")
	tagged := launcher.Build(t, ".", "./testdata/echo", "kitone", "kittwo")
	for bin, want := range map[string]string{plain: "", tagged: "kitone,kittwo"} {
		h, err := launcher.Run(t, 30*time.Second, launcher.Program{Argv: []string{bin, "tags"}})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := strings.TrimSpace(h.Stdout.String()); got != want {
			t.Errorf("%s was built with tags %q, want %q", bin, got, want)
		}
		if !h.Await(10*time.Second) || h.Process.ExitCode() != 0 {
			t.Errorf("Await after Run = false or exit %d, want an exited program", h.Process.ExitCode())
		}
	}
}

// A package that does not build fails its test with the compiler's output.
func TestBuildFailureFailsItsTest(t *testing.T) {
	r := &recorder{TB: t}
	if !r.run(func() { launcher.Build(r, ".", "./testdata/missing") }) {
		t.Fatal("building a missing package did not fail its test")
	}
	if got := r.failed(); len(got) != 1 || !strings.HasPrefix(got[0], "launcher: go build ./testdata/missing") {
		t.Fatalf("failures %q, want one naming the build", got)
	}
	r.cleanup()
}

// Start and Spawn fail their test for no program and for one that cannot start,
// and a failed Start leaves no home behind.
func TestStartAndSpawnRefuseWhatCannotRun(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no-such-program.exe")
	for _, tc := range []struct {
		name string
		call func(testing.TB)
		want string
	}{
		{"start, no program", func(t testing.TB) { launcher.Start(t, launcher.Program{}) }, "launcher: no program"},
		{"spawn, no program", func(t testing.TB) { launcher.Spawn(t, launcher.Program{}) }, "launcher: no program"},
		{"start, missing binary", func(t testing.TB) { launcher.Start(t, launcher.Program{Argv: []string{missing}}) }, "launcher: start " + missing},
		{"spawn, missing binary", func(t testing.TB) { launcher.Spawn(t, launcher.Program{Argv: []string{missing}}) }, "launcher: start " + missing},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &recorder{TB: t}
			if !r.run(func() { tc.call(r) }) {
				t.Fatal("the call did not fail its test")
			}
			if got := r.failed(); len(got) != 1 || !strings.HasPrefix(got[0], tc.want) {
				t.Fatalf("failures %q, want one starting %q", got, tc.want)
			}
			r.cleanup()
			if got := r.failed(); len(got) != 1 {
				t.Fatalf("the cleanup reported %q", got[1:])
			}
		})
	}
}

// A spawned tree still running when its test ends is logged and stopped by the
// cleanup, which reports no failure; Await on a running program reports false.
func TestSpawnCleanupStopsASurvivingTree(t *testing.T) {
	bin := launcher.Build(t, ".", "./testdata/echo")
	r := &recorder{TB: t}
	h := launcher.Spawn(r, launcher.Program{Argv: []string{bin, "sleep"}})
	// The program sleeps for an hour: Await with no time left reports it running.
	if h.Await(0) {
		t.Fatal("Await reported a sleeping program as exited")
	}
	r.cleanup()
	if got := r.failed(); len(got) != 0 {
		t.Fatalf("the cleanup reported %q", got)
	}
	if !h.Await(10 * time.Second) {
		t.Fatal("the surviving program was not reported exited after its cleanup")
	}
	if !subprocesstest.Exited(h.Process.Pid()) {
		t.Fatalf("pid %d outlived its cleanup", h.Process.Pid())
	}
}

// A launched program that does not exit when its standard input closes is
// given Grace, then stopped, when its test ends.
func TestStartCleanupStopsAProgramThatIgnoresItsInput(t *testing.T) {
	bin := launcher.Build(t, ".", "./testdata/echo")
	r := &recorder{TB: t}
	s := launcher.Start(r, launcher.Program{Argv: []string{bin, "sleep"}})
	pid := s.Process.Pid()
	start := time.Now()
	r.cleanup()
	if got := r.failed(); len(got) != 0 {
		t.Fatalf("the cleanup reported %q", got)
	}
	if took := time.Since(start); took < launcher.Grace {
		t.Errorf("the cleanup stopped the program after %s, before its Grace of %s", took, launcher.Grace)
	}
	if !subprocesstest.Exited(pid) {
		t.Fatalf("pid %d outlived its cleanup", pid)
	}
}
