//go:build scenario

package scenarios

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/testkit/launcher"
)

// dshBinary builds cmd/dsh from this checkout through the test kit's launcher
// (DH-D-UTIL-6), with tags, into a temporary directory removed at cleanup.
func dshBinary(t *testing.T, tags ...string) string {
	t.Helper()
	return launcher.Build(t, filepath.Join(repositoryRoot(t), "src", "harness"), "./cmd/dsh", tags...)
}

// dshRun is one finished run of the binary.
type dshRun struct {
	stdout, stderr string
	code           int
	took           time.Duration
}

// runDsh runs bin with args and env added to the parent environment, waits up to
// within for it to exit, and returns what it wrote and its exit code. A run that
// outlives within is killed and fails the scenario.
func runDsh(t *testing.T, within time.Duration, env []string, bin string, args ...string) dshRun {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), bin, args...)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	if err := cmd.Start(); err != nil {
		t.Fatalf("%s: %v", bin, err)
	}
	var err error
	exited := make(chan struct{})
	go func() { err = cmd.Wait(); close(exited) }()
	t.Cleanup(func() {
		select {
		case <-exited:
		default:
			_ = cmd.Process.Kill()
			<-exited
		}
	})
	select {
	case <-exited:
	case <-time.After(within):
		_ = cmd.Process.Kill()
		<-exited
		t.Fatalf("%s %v did not exit within %s\nstdout:\n%s\nstderr:\n%s", bin, args, within, stdout.String(), stderr.String())
	}
	r := dshRun{stdout: stdout.String(), stderr: stderr.String(), code: 0, took: time.Since(start)}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		r.code = exit.ExitCode()
	} else if err != nil {
		t.Fatalf("%s: %v", bin, err)
	}
	return r
}
