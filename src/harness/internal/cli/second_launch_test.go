package cli

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// testOpened is the line the testhook opener writes in place of a browser
// (host/launch/testhook.go).
const testOpened = "testkit-opener: opened"

// webHost is a built `dsh web` running on a temp home.
type webHost struct {
	bin, root string
	cmd       *exec.Cmd
	stderr    *testkit.Buffer
	done      chan struct{}
}

// webEnv is the process environment of a launch: a display, so the host may open
// a browser, and no SSH session.
func webEnv() []string {
	env := []string{"DISPLAY=:0"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "SSH_") && !strings.HasPrefix(kv, "DISPLAY=") {
			env = append(env, kv)
		}
	}
	return env
}

// startWeb builds dsh, writes a web layer with the keyless route and a free fixed
// port, starts `dsh web` and
// waits until it opened its browser: it is ready.
func startWeb(t *testing.T, ctx context.Context) *webHost {
	t.Helper()
	// A short home: the control socket's path is bounded (about 100 bytes), which a
	// home under the test's TempDir exceeds.
	dir, err := os.MkdirTemp("", "dshc-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	root := filepath.Join(dir, "home")
	if err := home.Ensure(home.New(root)); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	temphome.Layer(t, root, profile.Web,
		"- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: fake\n        url: \"http://127.0.0.1:1\"\n"+
			"- id: host.listen\n  name: host.listen\n  merge:\n    port: "+strconv.Itoa(port)+"\n")
	w := &webHost{bin: buildTestDsh(t), root: root, stderr: &testkit.Buffer{}, done: make(chan struct{})}
	w.cmd = exec.CommandContext(ctx, w.bin, "--home", root, "web")
	w.cmd.Env, w.cmd.Dir, w.cmd.Stderr = webEnv(), dir, w.stderr
	if err := w.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { defer close(w.done); _ = w.cmd.Wait() }()
	t.Cleanup(func() { _ = w.cmd.Process.Kill(); <-w.done })
	for !strings.Contains(w.stderr.String(), testOpened) {
		select {
		case <-w.done:
			t.Fatalf("dsh web exited %d before it was ready:\n%s", w.cmd.ProcessState.ExitCode(), w.stderr)
		case <-ctx.Done():
			t.Fatalf("dsh web never opened its browser:\n%s", w.stderr)
		case <-time.After(20 * time.Millisecond):
		}
	}
	return w
}

// run runs the built dsh on the host's home and returns its status and stderr.
func (w *webHost) run(t *testing.T, ctx context.Context, args ...string) (int, string, string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, w.bin, append([]string{"--home", w.root}, args...)...)
	stdout, stderr := &testkit.Buffer{}, &testkit.Buffer{}
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = webEnv(), filepath.Dir(w.root), stdout, stderr
	err := cmd.Run()
	if _, ok := err.(*exec.ExitError); err != nil && !ok {
		t.Fatal(err)
	}
	return cmd.ProcessState.ExitCode(), stdout.String(), stderr.String()
}

func records(t *testing.T, root string) []string {
	t.Helper()
	m, err := filepath.Glob(filepath.Join(home.New(root).Run(), "web-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TC-11b, DH-D-CLI-5: a second `dsh web` on the home of a running one calls
// launch.open on it and exits 0; the running host opened its browser again, and
// the home still holds one host.
//
//dsh:test DH-T-CLI-7
func TestSecondWebReusesTheHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	w := startWeb(t, ctx)
	status, _, stderr := w.run(t, ctx, "web")
	pid := strconv.Itoa(w.cmd.Process.Pid)
	if status != output.OK || !strings.Contains(stderr, "dsh web: opened in the browser of the host (process "+pid+")") {
		t.Fatalf("second dsh web exited %d:\n%s", status, stderr)
	}
	if strings.Contains(stderr, "#t=") {
		t.Errorf("the second launch printed a link:\n%s", stderr)
	}
	deadline := time.Now().Add(10 * time.Second)
	for strings.Count(w.stderr.String(), testOpened) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := strings.Count(w.stderr.String(), testOpened); n != 2 {
		t.Errorf("the running host opened its browser %d times, want 2:\n%s", n, w.stderr)
	}
	if recs := records(t, w.root); len(recs) != 1 {
		t.Errorf("runtime records %v, want one host", recs)
	}
	status, stdout, _ := w.run(t, ctx, "status")
	if status != output.OK || !strings.Contains(stdout, "process "+pid) {
		t.Errorf("dsh status exited %d: %q", status, stdout)
	}
}

var noticeLine = regexp.MustCompile(`did not stop cleanly; report: (\S+);.*--safe`)

// TC-11b, DH-D-CLI-5: a host killed outright (SIGKILL, TerminateProcess) leaves its
// record; `dsh status` then names the crash report written for it and suggests
// --safe, exits 4 as no host runs, and `dsh crash` prints the same report.
//
//dsh:test DH-T-CLI-7
func TestCrashExplainedAtNextStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	w := startWeb(t, ctx)
	if err := w.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-w.done
	status, _, stderr := w.run(t, ctx, "status")
	m := noticeLine.FindStringSubmatch(stderr)
	if status != output.Unavailable || m == nil || !strings.Contains(stderr, "process "+strconv.Itoa(w.cmd.Process.Pid)) {
		t.Fatalf("dsh status after a kill exited %d:\n%s", status, stderr)
	}
	if _, err := os.Stat(m[1]); err != nil {
		t.Errorf("the named report: %v", err)
	}
	status, stdout, _ := w.run(t, ctx, "crash")
	if status != output.OK || strings.TrimSpace(stdout) != m[1] {
		t.Errorf("dsh crash exited %d with %q, want %s", status, stdout, m[1])
	}
}
