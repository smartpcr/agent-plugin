package stdio_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/stdio"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess/subprocesstest"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// The helper process is this test binary, re-exec'd through subprocess.Start (the
// one spawn path, M2-NG-3) with these variables.
const (
	helperEnv = "GO_WANT_HELPER"
	modeEnv   = "STDIO_HELPER_MODE"
	logEnv    = "STDIO_HELPER_LOG"
)

// The helper's bounds: a write stall well inside the forced deadline, which stands
// in for boot's ShutdownTimeout.
const (
	helperStall  = 500 * time.Millisecond
	helperForced = 2 * time.Second
	// exitWait bounds the parent's wait for the helper's exit.
	exitWait = 30 * time.Second
)

// frame is the one valid frame the stray-write helper writes.
const frame = `{"jsonrpc":"2.0","method":"stdio/ready"}` + "\n"

// exitApp is the injected stop: it ends the process with the status it is given.
type exitApp struct{}

func (exitApp) Ready() <-chan struct{}    { return nil }
func (exitApp) Stopping() <-chan struct{} { return nil }
func (exitApp) Stop(_ launch.Reason, status int) {
	os.Exit(status)
}

// TestHelperStdioOwner is the helper process; it is skipped in a normal run. It
// claims the real stdio and acts by mode: "stray" writes one frame, prints a stray
// line and tries a second claim, then exits 0; "read" reads its input until it
// ends; "write" also writes to the client until a write stalls or fails. In "read" and
// "write" the forced deadline exits 124 when no ending came first.
func TestHelperStdioOwner(t *testing.T) {
	if os.Getenv(helperEnv) != "1" {
		t.Skip("the helper process of the stdio tests")
	}
	runHelper()
}

// runHelper is the helper process's body; it always ends the process.
func runHelper() {
	var log *os.File
	if path := os.Getenv(logEnv); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			os.Exit(3)
		}
		log = f
	}
	o, err := stdio.Claim(stdio.Config{App: exitApp{}, WriteStall: helperStall, Log: log})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	switch os.Getenv(modeEnv) {
	case "stray":
		if _, err := o.Output().Write([]byte(frame)); err != nil {
			os.Exit(5)
		}
		fmt.Println("x")
		_, err := stdio.Claim(stdio.Config{App: exitApp{}})
		fmt.Println("second claim:", err)
		os.Exit(output.OK)
	case "write":
		go func() {
			line := append(bytes.Repeat([]byte("a"), 64<<10), '\n')
			for {
				if _, err := o.Output().Write(line); err != nil {
					return
				}
			}
		}()
		fallthrough
	case "read":
		time.AfterFunc(helperForced, func() { os.Exit(output.Forced) })
		buf := make([]byte, 4096)
		for {
			if _, err := o.Input().Read(buf); err != nil {
				select {}
			}
		}
	}
	os.Exit(6)
}

// startHelper starts the helper in mode with the given streams and a log in dir.
// The helper's tree is stopped when the test ends.
func startHelper(t *testing.T, mode string, stdin subprocess.StdinMode, stdout subprocess.OutputMode, logPath string) (*subprocess.Process, *testkit.Buffer) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	env := subprocess.Env{helperEnv + "=1", modeEnv + "=" + mode, logEnv + "=" + logPath}
	if root := os.Getenv("SYSTEMROOT"); root != "" {
		env = append(env, "SYSTEMROOT="+root)
	}
	// The owner runs only in the helper: forward the coverage directory so the
	// gate's coverage, merged across processes, counts what the helper ran.
	if dir := os.Getenv(subprocesstest.CoverDirEnv); dir != "" {
		env = append(env, subprocesstest.CoverDirEnv+"="+dir)
	}
	stderr := &testkit.Buffer{}
	p, err := subprocess.Start(context.Background(), subprocess.Spec{
		Argv:   []string{exe, "-test.run=^TestHelperStdioOwner$", "-test.count=1"},
		Env:    env,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		Grace:  time.Second,
		Launch: sandbox.Trusted("stdio test: the test binary re-exec'd as its own helper"),
	})
	if err != nil {
		t.Fatalf("start the helper: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), exitWait)
		defer cancel()
		if err := p.StopTree(ctx, 0); err != nil {
			t.Errorf("stop the helper's tree: %v", err)
		}
		_ = p.Wait()
	})
	return p, stderr
}

// exitStatus waits for p to exit and returns its status.
func exitStatus(t *testing.T, p *subprocess.Process, stderr *testkit.Buffer) int {
	t.Helper()
	select {
	case <-p.Exited():
	case <-time.After(exitWait):
		t.Fatalf("the helper did not exit within %v; stderr:\n%s", exitWait, stderr.String())
	}
	_ = p.Wait() // a non-zero exit is an error; the status is what the test checks
	return p.ExitCode()
}

// A stray print after the claim lands in the log, never on standard output, which
// carries the one frame only; a second claim in the process fails.
//
//dsh:test DH-T-SDK-21
func TestStrayWriteGoesToTheLog(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "helper.log")
	var stdout testkit.Buffer
	p, stderr := startHelper(t, "stray", nil, &stdout, logPath)
	if status := exitStatus(t, p, stderr); status != output.OK {
		t.Fatalf("helper exit %d, want 0; stderr:\n%s", status, stderr.String())
	}
	if got := stdout.String(); got != frame {
		t.Fatalf("stdout %q, want only the frame %q", got, frame)
	}
	if _, err := jsonrpc.Decode([]byte(strings.TrimSuffix(stdout.String(), "\n"))); err != nil {
		t.Fatalf("stdout is not a frame: %v", err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(log, []byte("x\n")) && !bytes.HasPrefix(log, []byte("x\r\n")) {
		t.Fatalf("log %q does not start with the stray line", log)
	}
	if !bytes.Contains(log, []byte(string(stdio.ReasonClaimed))) {
		t.Fatalf("log %q does not report the refused second claim", log)
	}
}

// The endings of a helper that owns real stdio: its input closed exits 0, a client
// that stops reading exits 125, a client that closed its output exits 0, and an
// open input past the forced deadline exits 124.
//
//dsh:test DH-T-SDK-22
func TestBoundedEndings(t *testing.T) {
	t.Run("input closed", func(t *testing.T) {
		inR, inW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		p, stderr := startHelper(t, "read", inR, subprocess.Discard, "")
		if err := inR.Close(); err != nil {
			t.Fatal(err)
		}
		if err := inW.Close(); err != nil {
			t.Fatal(err)
		}
		if status := exitStatus(t, p, stderr); status != output.OK {
			t.Fatalf("exit %d, want %d; stderr:\n%s", status, output.OK, stderr.String())
		}
	})
	t.Run("client stalled", func(t *testing.T) {
		inR, inW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		outR, outW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = inW.Close()
			_ = outR.Close()
		})
		p, stderr := startHelper(t, "write", inR, outW, "")
		if err := inR.Close(); err != nil {
			t.Fatal(err)
		}
		if err := outW.Close(); err != nil {
			t.Fatal(err)
		}
		if status := exitStatus(t, p, stderr); status != output.Stalled {
			t.Fatalf("exit %d, want %d; stderr:\n%s", status, output.Stalled, stderr.String())
		}
	})
	// A client that closed its end of the output is gone even while its input stays
	// open: the failed write is the InputClosed ending, exit 0, not a SIGPIPE death.
	t.Run("output closed", func(t *testing.T) {
		inR, inW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		outR, outW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = inW.Close() })
		if err := outR.Close(); err != nil {
			t.Fatal(err)
		}
		p, stderr := startHelper(t, "write", inR, outW, "")
		if err := inR.Close(); err != nil {
			t.Fatal(err)
		}
		if err := outW.Close(); err != nil {
			t.Fatal(err)
		}
		if status := exitStatus(t, p, stderr); status != output.OK {
			t.Fatalf("exit %d, want %d; stderr:\n%s", status, output.OK, stderr.String())
		}
	})
	t.Run("forced deadline", func(t *testing.T) {
		inR, inW, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = inW.Close() })
		p, stderr := startHelper(t, "read", inR, subprocess.Discard, "")
		if err := inR.Close(); err != nil {
			t.Fatal(err)
		}
		if status := exitStatus(t, p, stderr); status != output.Forced {
			t.Fatalf("exit %d, want %d; stderr:\n%s", status, output.Forced, stderr.String())
		}
	})
}
