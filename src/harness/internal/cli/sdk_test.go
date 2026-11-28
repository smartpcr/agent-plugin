package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/temphome"
)

// sdkHome is a temp home whose sdk profile layer inserts the fake route, with
// extra appended to the layer.
func sdkHome(t *testing.T, m *fakeModel, route, extra string) string {
	t.Helper()
	root := temphome.New(t).Root()
	temphome.Layer(t, root, profile.SDK,
		"- insert:\n    - id: route\n      name: llm.testkit-route\n      config:\n        id: fake\n        url: "+strconv.Quote(m.url)+"\n"+route+extra)
	return root
}

// sdkProcess is a started `dsh --profile sdk` with its client's ends.
type sdkProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr strings.Builder
	done   chan struct{}
}

func startSDK(t *testing.T, ctx context.Context, bin, root string) *sdkProcess {
	t.Helper()
	p := &sdkProcess{done: make(chan struct{})}
	cmd := exec.CommandContext(ctx, bin, "--home", root, "--profile", "sdk")
	cmd.Env = os.Environ()
	cmd.Dir = t.TempDir()
	cmd.Stderr = &p.stderr
	p.cmd = cmd
	var err error
	if p.stdin, err = cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if p.stdout, err = cmd.StdoutPipe(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); <-p.done })
	go func() {
		defer close(p.done)
		_ = cmd.Wait()
	}()
	return p
}

// background runs f beside the test and joins it at cleanup, after killing the
// process f talks to so its pipes end.
func (p *sdkProcess) background(t *testing.T, f func()) {
	t.Helper()
	ran := make(chan struct{})
	t.Cleanup(func() { _ = p.cmd.Process.Kill(); <-ran })
	go func() { defer close(ran); f() }()
}

// status waits for the process and returns its exit status and how long the
// wait took.
func (p *sdkProcess) status(t *testing.T, bound time.Duration) (int, time.Duration) {
	t.Helper()
	start := time.Now()
	select {
	case <-p.done:
	case <-time.After(bound):
		t.Fatalf("dsh --profile sdk did not exit within %s:\n%s", bound, p.stderr.String())
	}
	return p.cmd.ProcessState.ExitCode(), time.Since(start)
}

// ping is one request line the server answers with an error; its long id makes
// each answer large enough to fill a pipe in a few hundred lines.
func ping(n int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":"%s-%d","method":"test/unknown"}`+"\n", strings.Repeat("x", 2048), n)
}

// TC-12b, M2-C-10: the built `dsh --profile sdk` with the keyless fake route
// ends boundedly: 0 when the client closes its input, 125 when the client stops
// reading past the write stall, 124 when the shutdown bound elapses; a refused
// configuration is 78 (tech-spec sec. 7.3). Exit 130 on an interrupt is
// the open alignment I-6.
//
//dsh:test DH-T-CLI-12
func TestSDKLauncherBoundedEndings(t *testing.T) {
	bin := buildTestDsh(t)
	m := newFakeModel(t, "hello", false)

	t.Run("input closed is 0", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		p := startSDK(t, ctx, bin, sdkHome(t, m, "", ""))
		p.background(t, func() { _, _ = io.Copy(io.Discard, p.stdout) })
		// One answered request proves the server is up before the client ends.
		if _, err := io.WriteString(p.stdin, ping(0)); err != nil {
			t.Fatal(err)
		}
		_ = p.stdin.Close()
		if got, _ := p.status(t, time.Minute); got != output.OK {
			t.Fatalf("status %d, want %d:\n%s", got, output.OK, p.stderr.String())
		}
	})

	t.Run("a client that stops reading is 125", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		p := startSDK(t, ctx, bin, sdkHome(t, m, "", "- id: wire.stdio\n  name: wire.stdio\n  merge:\n    writeStall: 1s\n"))
		// The client writes requests and never reads an answer.
		p.background(t, func() {
			for i := 0; ; i++ {
				if _, err := io.WriteString(p.stdin, ping(i)); err != nil {
					return
				}
			}
		})
		if got, _ := p.status(t, time.Minute); got != output.Stalled {
			t.Fatalf("status %d, want %d:\n%s", got, output.Stalled, p.stderr.String())
		}
	})

	t.Run("the forced deadline is 124", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		p := startSDK(t, ctx, bin, sdkHome(t, m, "        holdStop: true\n", ""))
		p.background(t, func() { _, _ = io.Copy(io.Discard, p.stdout) })
		if _, err := io.WriteString(p.stdin, ping(0)); err != nil {
			t.Fatal(err)
		}
		_ = p.stdin.Close()
		got, took := p.status(t, time.Minute)
		if got != output.Forced {
			t.Fatalf("status %d, want %d:\n%s", got, output.Forced, p.stderr.String())
		}
		if bound := profile.DefaultLimits().ShutdownTimeout; took < bound/2 {
			t.Errorf("exited 124 after %s, before the %s shutdown bound", took, bound)
		}
	})

	// A launch failure: a configuration the schema refuses is 78. Boot's fatal
	// path (70) is proved by boot's own tests.
	for name, c := range map[string]struct {
		extra string
		want  int
	}{
		"a refused configuration is 78": {"- id: wire.stdio\n  name: wire.stdio\n  merge:\n    writeStall: soon\n", output.Config},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			p := startSDK(t, ctx, bin, sdkHome(t, m, "", c.extra))
			defer p.stdin.Close()
			p.background(t, func() { _, _ = io.Copy(io.Discard, p.stdout) })
			if got, _ := p.status(t, time.Minute); got != c.want {
				t.Fatalf("status %d, want %d:\n%s", got, c.want, p.stderr.String())
			}
		})
	}
}
