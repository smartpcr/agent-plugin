package cli

import (
	"context"
	"encoding/json"
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
	"github.com/smartpcr/agent-plugin/src/harness/internal/host/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
)

// testOpened is the line the testhook opener writes in place of a browser
// (host/launch/testhook.go).
const testOpened = "testkit-opener: opened"

// webHost is a built `dsh web` running on a temp home.
type webHost struct {
	bin, root string
	port      int
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
	return (&webHost{bin: buildTestDsh(t), root: root, port: port}).start(t, ctx)
}

// start runs one more `dsh web` with args on w's home and waits until it opened
// its browser.
func (w *webHost) start(t *testing.T, ctx context.Context, args ...string) *webHost {
	t.Helper()
	w = &webHost{bin: w.bin, root: w.root, port: w.port, stderr: &testkit.Buffer{}, done: make(chan struct{})}
	w.cmd = exec.CommandContext(ctx, w.bin, append([]string{"--home", w.root, "web"}, args...)...)
	w.cmd.Env, w.cmd.Dir, w.cmd.Stderr = webEnv(), filepath.Dir(w.root), w.stderr
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

// freePort is a TCP port free on loopback just now.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return ln.Addr().(*net.TCPAddr).Port
}

// DH-ADR-CLI-4: --port and --bind choose the listener before reuse is decided.
// The running host's own port reuses it; another port, or port 0 (the system
// picks), starts a second host; the upstream --host is a usage error naming --bind.
//
//dsh:test DH-T-CLI-7
func TestWebAddressFlagsChooseTheHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	w := startWeb(t, ctx)
	if status, _, stderr := w.run(t, ctx, "web", "--port", strconv.Itoa(w.port)); status != output.OK || !strings.Contains(stderr, "opened in the browser of the host") {
		t.Fatalf("dsh web --port %d exited %d:\n%s", w.port, status, stderr)
	}
	if status, _, stderr := w.run(t, ctx, "web", "--host", "0.0.0.0"); status != output.Usage || !strings.Contains(stderr, "--bind") {
		t.Errorf("dsh web --host exited %d:\n%s", status, stderr)
	}
	for i, args := range [][]string{{"--port", "0"}, {"--port=" + strconv.Itoa(freePort(t))}} {
		second := w.start(t, ctx, args...)
		if strings.Contains(second.stderr.String(), "opened in the browser of the host") {
			t.Errorf("dsh web %q reused the host:\n%s", args, second.stderr)
		}
		if recs := records(t, w.root); len(recs) != 2+i {
			t.Errorf("dsh web %q: runtime records %v, want %d hosts", args, recs, 2+i)
		}
	}
	if n := strings.Count(w.stderr.String(), testOpened); n != 2 {
		t.Errorf("the first host opened its browser %d times, want 2:\n%s", n, w.stderr)
	}
}

// A host whose lock is held but which does not answer is reported in both
// output modes: `dsh status` lists it beside the live host, names it on stderr
// and exits 4, and --json writes one document holding both.
//
//dsh:test DH-T-CLI-7
func TestStatusReportsAnUnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	w := startWeb(t, ctx)
	run := home.New(w.root).Run()
	p := launch.PathsFor(run, "http://127.0.0.1:1")
	b, _ := json.Marshal(launch.Record{Origin: "http://127.0.0.1:1", PID: 4343, Profile: profile.Web, Socket: p.Socket})
	if err := os.WriteFile(p.Record, b, 0o600); err != nil {
		t.Fatal(err)
	}
	lock, err := fsx.TryLock(p.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lock.Unlock() }()
	live := "process " + strconv.Itoa(w.cmd.Process.Pid)

	status, stdout, stderr := w.run(t, ctx, "status")
	if status != output.Unavailable || !strings.Contains(stdout, live) || !strings.Contains(stdout, "process 4343") ||
		!strings.Contains(stdout, "unreachable") || !strings.Contains(stderr, "4343") {
		t.Errorf("dsh status exited %d:\nstdout %s\nstderr %s", status, stdout, stderr)
	}
	status, stdout, stderr = w.run(t, ctx, "status", "--json")
	var doc struct {
		Hosts []struct {
			PID   int    `json:"pid"`
			State string `json:"state"`
		} `json:"hosts"`
	}
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("dsh status --json wrote %q: %v", stdout, err)
	}
	states := map[int]string{}
	for _, h := range doc.Hosts {
		states[h.PID] = h.State
	}
	if status != output.Unavailable || states[w.cmd.Process.Pid] != "live" || states[4343] != "unreachable" || !strings.Contains(stderr, "4343") {
		t.Errorf("dsh status --json exited %d with %v:\nstderr %s", status, states, stderr)
	}
}
