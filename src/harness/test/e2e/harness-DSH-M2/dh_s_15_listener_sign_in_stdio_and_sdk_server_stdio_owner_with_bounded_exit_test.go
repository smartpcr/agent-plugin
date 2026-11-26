//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/wire/stdio"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// The helper process is this e2e test binary re-exec'd through subprocess.Start
// (the one spawn path, M2-NG-3) with these variables.
const (
	sobeHelperEnv = "GO_WANT_HELPER"
	sobeModeEnv   = "SOBE_STDIO_HELPER_MODE"
	sobeLogEnv    = "SOBE_STDIO_HELPER_LOG"
)

const (
	sobeHelperStall  = 500 * time.Millisecond
	sobeHelperForced = 2 * time.Second
	sobeExitWait     = 30 * time.Second
)

// sobeFrame is the one valid frame the stray-write helper writes.
const sobeFrame = `{"jsonrpc":"2.0","method":"stdio/ready"}` + "\n"

// sobeExitApp is the injected stop: it ends the process with the status it is given.
type sobeExitApp struct{}

func (sobeExitApp) Ready() <-chan struct{}    { return nil }
func (sobeExitApp) Stopping() <-chan struct{} { return nil }
func (sobeExitApp) Stop(_ launch.Reason, status int) {
	os.Exit(status)
}

// TestHelperStdioOwner is the helper process; it is skipped in a normal run.
func TestHelperStdioOwner(t *testing.T) {
	if os.Getenv(sobeHelperEnv) != "1" || os.Getenv(sobeModeEnv) == "" {
		t.Skip("the helper process of the stdio owner e2e scenarios")
	}
	sobeRunHelper()
}

// sobeRunHelper claims the real stdio and acts by mode: "stray" writes one frame
// and prints a stray line, then exits 0; "read" reads its input until it ends;
// "write" also writes to the client until a write stalls or fails. In "read" and
// "write" the forced deadline exits 124 when no ending came first.
func sobeRunHelper() {
	var log *os.File
	if path := os.Getenv(sobeLogEnv); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			os.Exit(3)
		}
		log = f
	}
	o, err := stdio.Claim(stdio.Config{App: sobeExitApp{}, WriteStall: sobeHelperStall, Log: log})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(4)
	}
	switch os.Getenv(sobeModeEnv) {
	case "stray":
		if _, err := o.Output().Write([]byte(sobeFrame)); err != nil {
			os.Exit(5)
		}
		fmt.Println("x")
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
		time.AfterFunc(sobeHelperForced, func() { os.Exit(output.Forced) })
		buf := make([]byte, 4096)
		for {
			if _, err := o.Input().Read(buf); err != nil {
				select {}
			}
		}
	}
	os.Exit(6)
}

type sobeStopCall struct {
	reason launch.Reason
	status int
}

type sobeWorld struct {
	cleanups []func()

	mode    string
	dir     string
	logPath string
	stdout  testkit.Buffer
	stderr  *testkit.Buffer
	proc    *subprocess.Process

	writer   *stdio.Writer
	pipeR    *io.PipeReader
	writeErr error
	stopMu   sync.Mutex
	stops    []sobeStopCall
}

func (w *sobeWorld) cleanup() {
	for i := len(w.cleanups) - 1; i >= 0; i-- {
		w.cleanups[i]()
	}
	w.cleanups = nil
}

func (w *sobeWorld) start(mode string, stdin subprocess.StdinMode, stdout subprocess.OutputMode, logPath string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	env := subprocess.Env{sobeHelperEnv + "=1", sobeModeEnv + "=" + mode, sobeLogEnv + "=" + logPath}
	if root := os.Getenv("SYSTEMROOT"); root != "" {
		env = append(env, "SYSTEMROOT="+root)
	}
	w.stderr = &testkit.Buffer{}
	p, err := subprocess.Start(context.Background(), subprocess.Spec{
		Argv:   []string{exe, "-test.run=^TestHelperStdioOwner$", "-test.count=1"},
		Env:    env,
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: w.stderr,
		Grace:  time.Second,
		Launch: sandbox.Trusted("stdio owner e2e: the test binary re-exec'd as its own helper"),
	})
	if err != nil {
		return fmt.Errorf("start the helper: %w", err)
	}
	w.proc = p
	w.cleanups = append(w.cleanups, func() {
		ctx, cancel := context.WithTimeout(context.Background(), sobeExitWait)
		defer cancel()
		_ = p.StopTree(ctx, 0)
		_ = p.Wait()
	})
	return nil
}

func (w *sobeWorld) pipe() (*os.File, *os.File, error) {
	r, wr, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	w.cleanups = append(w.cleanups, func() { _ = r.Close(); _ = wr.Close() })
	return r, wr, nil
}

// --- Scenario: Stray write redirected ---

func (w *sobeWorld) aStrayWriteHelper(s string) error {
	if s != "x" {
		return fmt.Errorf("the helper prints %q, not %q", "x", s)
	}
	w.mode = "stray"
	dir, err := os.MkdirTemp("", "sobe-stdio-*")
	if err != nil {
		return err
	}
	w.dir = dir
	w.cleanups = append(w.cleanups, func() { _ = os.RemoveAll(dir) })
	w.logPath = filepath.Join(dir, "helper.log")
	return nil
}

func (w *sobeWorld) parentStartsWithBufferedStdout() error {
	return w.start(w.mode, nil, &w.stdout, w.logPath)
}

func (w *sobeWorld) stdoutHoldsOnlyTheFrame() error {
	got := w.stdout.String()
	if got != sobeFrame {
		return fmt.Errorf("stdout %q, want only the frame %q", got, sobeFrame)
	}
	if strings.Contains(got, "x\n") {
		return errors.New("stdout carries the stray x")
	}
	if _, err := jsonrpc.Decode([]byte(strings.TrimSuffix(got, "\n"))); err != nil {
		return fmt.Errorf("stdout is not a frame: %w", err)
	}
	return nil
}

func (w *sobeWorld) logHolds(s string) error {
	log, err := os.ReadFile(w.logPath)
	if err != nil {
		return err
	}
	if !bytes.HasPrefix(log, []byte(s+"\n")) && !bytes.HasPrefix(log, []byte(s+"\r\n")) {
		return fmt.Errorf("log %q does not hold the stray line %q", log, s)
	}
	return nil
}

// --- Scenario: Bounded writer logic ---

func (w *sobeWorld) boundedWriterOverStalledPipe(ms int) error {
	pr, pw := io.Pipe()
	w.pipeR = pr
	w.cleanups = append(w.cleanups, func() { _ = pr.Close() })
	w.writer = stdio.NewWriter(pw, time.Duration(ms)*time.Millisecond, func(r launch.Reason, status int) {
		w.stopMu.Lock()
		defer w.stopMu.Unlock()
		w.stops = append(w.stops, sobeStopCall{r, status})
	})
	return nil
}

func (w *sobeWorld) writeAttemptedUntilDeadline() error {
	done := make(chan error, 1)
	go func() {
		_, err := w.writer.Write([]byte("{}\n"))
		done <- err
	}()
	select {
	case w.writeErr = <-done:
	case <-time.After(sobeExitWait):
		return errors.New("the bounded write did not return after its deadline")
	}
	// A second write after the stall must also fail and must not ask stop again.
	if _, err := w.writer.Write([]byte("{}\n")); !herr.Has(err, stdio.ReasonClientStalled) {
		return fmt.Errorf("write after the stall: %v, want %s", err, stdio.ReasonClientStalled)
	}
	return nil
}

func (w *sobeWorld) writerReportsClientStalled() error {
	if !herr.Has(w.writeErr, stdio.ReasonClientStalled) {
		return fmt.Errorf("stalled write: %v, want %s", w.writeErr, stdio.ReasonClientStalled)
	}
	select {
	case <-w.writer.Ended():
	default:
		return errors.New("Ended is open after a stall")
	}
	return nil
}

func (w *sobeWorld) stopCalledOnce(status int) error {
	w.stopMu.Lock()
	defer w.stopMu.Unlock()
	want := sobeStopCall{launch.ClientStalled, status}
	if len(w.stops) != 1 || w.stops[0] != want {
		return fmt.Errorf("stop calls %v, want exactly [%v]", w.stops, want)
	}
	return nil
}

// --- Scenario Outline: Bounded endings ---

func (w *sobeWorld) owningHelper() error { return nil }

func (w *sobeWorld) parentActs(action string) error {
	switch action {
	case "closes the child's stdin":
		inR, inW, err := w.pipe()
		if err != nil {
			return err
		}
		if err := w.start("read", inR, subprocess.Discard, ""); err != nil {
			return err
		}
		_ = inR.Close()
		return inW.Close()
	case "stops reading while the child writes":
		inR, _, err := w.pipe()
		if err != nil {
			return err
		}
		// outR stays open and is never read, so the child's writes stall.
		_, outW, err := w.pipe()
		if err != nil {
			return err
		}
		if err := w.start("write", inR, outW, ""); err != nil {
			return err
		}
		_ = inR.Close()
		return outW.Close()
	case "waits past the deadline with stdin open":
		inR, _, err := w.pipe()
		if err != nil {
			return err
		}
		if err := w.start("read", inR, subprocess.Discard, ""); err != nil {
			return err
		}
		return inR.Close()
	}
	return fmt.Errorf("unknown action %q", action)
}

func (w *sobeWorld) helperExitsWith(status int) error {
	if w.proc == nil {
		return errors.New("no helper was started")
	}
	select {
	case <-w.proc.Exited():
	case <-time.After(sobeExitWait):
		return fmt.Errorf("the helper did not exit within %v; stderr:\n%s", sobeExitWait, w.stderr.String())
	}
	_ = w.proc.Wait()
	if got := w.proc.ExitCode(); got != status {
		return fmt.Errorf("helper exit %d, want %d; stderr:\n%s", got, status, w.stderr.String())
	}
	return nil
}

func InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_stdio_owner_with_bounded_exit(ctx *godog.ScenarioContext) {
	w := &sobeWorld{}
	ctx.Before(func(c context.Context, _ *godog.Scenario) (context.Context, error) {
		*w = sobeWorld{}
		return c, nil
	})
	ctx.After(func(c context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		w.cleanup()
		return c, nil
	})

	ctx.Step(`^a stdio helper process that writes one valid frame and then prints "([^"]*)"$`, w.aStrayWriteHelper)
	ctx.Step(`^the parent starts it through subprocess\.Start with stdout bound to a buffer and a log in a temp dir$`, w.parentStartsWithBufferedStdout)
	ctx.Step(`^stdout holds only the one frame$`, w.stdoutHoldsOnlyTheFrame)
	ctx.Step(`^the helper log holds "([^"]*)"$`, w.logHolds)

	ctx.Step(`^an in-process bounded writer over a pipe whose reader never reads, with a (\d+) ms write deadline and a stop fake$`, w.boundedWriterOverStalledPipe)
	ctx.Step(`^a write is attempted and the write deadline elapses$`, w.writeAttemptedUntilDeadline)
	ctx.Step(`^the writer reports ClientStalled$`, w.writerReportsClientStalled)
	ctx.Step(`^the stop fake was called exactly once with ClientStalled and status (\d+)$`, w.stopCalledOnce)

	ctx.Step(`^a stdio helper process owning real stdio with an exiting stop and a 2 s forced deadline$`, w.owningHelper)
	ctx.Step(`^the parent (closes the child's stdin|stops reading while the child writes|waits past the deadline with stdin open)$`, w.parentActs)
	ctx.Step(`^the helper exits with status (\d+)$`, w.helperExitsWith)
}

func TestE2E_dh_s_15_listener_sign_in_stdio_and_sdk_server_stdio_owner_with_bounded_exit(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario_dh_s_15_listener_sign_in_stdio_and_sdk_server_stdio_owner_with_bounded_exit,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"dh_s_15_listener_sign_in_stdio_and_sdk_server_stdio_owner_with_bounded_exit.feature"},
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
