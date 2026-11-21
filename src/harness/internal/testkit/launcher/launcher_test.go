package launcher_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
)

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
