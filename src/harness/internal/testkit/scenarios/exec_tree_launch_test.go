//go:build scenario

package scenarios

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestExec1LaunchSweepsHalfReadyLaunch checks that a detached launch whose fixture
// writes only some of its pids (and dsh none) returns what it found with an error,
// and that its cleanup still sends the sweep to the target.
func TestExec1LaunchSweepsHalfReadyLaunch(t *testing.T) {
	var lines []string
	m := &exec10Machine{alias: "fake", dir: "/s", home: "/s/home", shell: func(_ context.Context, line, _ string) ([]byte, error) {
		lines = append(lines, line)
		if strings.HasPrefix(line, "for f in child grandchild;") {
			return []byte("child=101\ngrandchild=\n"), nil
		}
		return nil, nil
	}}
	var (
		tree map[string]int
		err  error
	)
	t.Run("launch", func(t *testing.T) {
		tree, _, err = exec1Launch(t, m, "start", "/s/pids", 1)
	})
	if err == nil || tree["child"] != 101 || len(tree) != 1 {
		t.Fatalf("exec1Launch = %v, %v; want the child pid alone and an error", tree, err)
	}
	if len(lines) == 0 || !strings.Contains(lines[0], "setsid") || !strings.Contains(lines[0], "/s/start.pid") {
		t.Errorf("the launch line does not start a session recording its leader: %v", lines)
	}
	if want := exec1Sweep("/s", "/s/home", "/s/pids"); lines[len(lines)-1] != want {
		t.Errorf("the cleanup's last line is %q, want the sweep %q", lines[len(lines)-1], want)
	}
}

// TestExec1SweepKillsHalfReadyLaunch runs a launch on this machine that never
// becomes ready: its session leader sleeps, and a child in a session of its own
// writes its pid. After the launch's cleanup both are gone.
func TestExec1SweepKillsHalfReadyLaunch(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the sweep runs on a Linux target")
	}
	dir := t.TempDir()
	m := &exec10Machine{alias: "local", dir: dir, home: dir + "/home", shell: func(ctx context.Context, line, stdin string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, "sh", "-c", line)
		cmd.Stdin = strings.NewReader(stdin)
		return cmd.CombinedOutput()
	}}
	pidDir := dir + "/pids"
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	var tree map[string]int
	t.Run("launch", func(t *testing.T) {
		var err error
		tree, _, err = exec1Launch(t, m, "setsid sleep 300 & echo $! > "+exec1Quote(pidDir+"/child")+"; sleep 300", pidDir, 1)
		if err == nil {
			t.Error("a launch that writes no grandchild and no dsh.pid is ready")
		}
	})
	data, err := os.ReadFile(dir + "/start.pid")
	if err != nil {
		t.Fatal(err)
	}
	leader, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || tree["child"] == 0 {
		t.Fatalf("leader %q, child %v: the launch did not start", data, tree)
	}
	both := map[string]int{"leader": leader, "child": tree["child"]}
	for _, pid := range both {
		exec1Gone(t, m, pid, 5)
	}
	if alive := m.alive(t, both); len(alive) > 0 {
		t.Errorf("after the cleanup %v of the half-ready launch run on", alive)
	}
}
