package launcher_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

// A run that outlives its bound while a grandchild holds its output returns
// promptly with ErrTimeout, and the whole tree, grandchild included, is gone.
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
	for _, pid := range []int{h.Process.Pid(), grandchild} {
		if subprocesstest.Alive(pid) {
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
		for subprocesstest.Alive(pid) && time.Now().Before(deadline) {
			<-time.After(20 * time.Millisecond)
		}
		if subprocesstest.Alive(pid) {
			t.Errorf("pid %d survives its test", pid)
		}
	}
}
