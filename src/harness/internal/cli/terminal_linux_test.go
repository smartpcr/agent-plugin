package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/secret"
)

// openPTY opens a pty pair; neither end becomes this process's controlling
// terminal.
func openPTY(t *testing.T) (ptm, pts *os.File) {
	t.Helper()
	ptm, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Skipf("no pseudo-terminal: %v", err)
	}
	fd := int(ptm.Fd())
	n, err := unix.IoctlGetInt(fd, unix.TIOCGPTN)
	if err == nil {
		err = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0)
	}
	if err == nil {
		pts, err = os.OpenFile("/dev/pts/"+strconv.Itoa(n), os.O_RDWR|unix.O_NOCTTY, 0)
	}
	if err != nil {
		_ = ptm.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pts.Close(); _ = ptm.Close() })
	return ptm, pts
}

// lockedBuffer is a buffer two goroutines share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// waitFor polls until s() contains want.
func waitFor(t *testing.T, what string, s func() string, want string) {
	t.Helper()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(30 * time.Second)
	for !strings.Contains(s(), want) {
		select {
		case <-deadline:
			t.Fatalf("no %q on %s; got %q", want, what, s())
		case <-tick.C:
		}
	}
}

// DH-T-CLI-16 on a pseudo-terminal: `dsh secret set` prompts on standard error,
// the value typed is not echoed, Enter ends it (the terminal stays open), the
// store holds it, and the terminal's modes are as they were, echo on again.
//
//dsh:test DH-T-CLI-16
func TestSecretSetOnPseudoTerminalHidesInput(t *testing.T) {
	ptm, pts := openPTY(t)
	before, err := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if before.Lflag&unix.ECHO == 0 || before.Lflag&unix.ICANON == 0 {
		t.Fatalf("the pty starts without echo or line input: lflag %#x", before.Lflag)
	}
	var screen lockedBuffer
	read, ran := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		// Closing both ends ends the reader and any read Main still blocks on.
		_, _ = pts.Close(), ptm.Close()
		<-read
		<-ran
	})
	go func() {
		defer close(read)
		buf := make([]byte, 256)
		for {
			n, err := ptm.Read(buf)
			_, _ = screen.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()

	root := t.TempDir()
	var out, errOut lockedBuffer
	env := Env{
		Request: launch.Request{Args: []string{"dsh", "--home", root, "secret", "set", "TEST_K"}, Cwd: t.TempDir(), OS: runtime.GOOS},
		Stdin:   pts, Stdout: &out, Stderr: &errOut,
	}
	status := make(chan int, 1)
	go func() { defer close(ran); status <- Main(context.Background(), env) }()
	waitFor(t, "standard error", errOut.String, secretPrompt)
	if _, err := ptm.WriteString("sk-typed-value\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case s := <-status:
		if s != output.OK || out.String() != "stored secret://test/k (revision 1)\n" {
			t.Fatalf("secret set: status %d, stdout %q, stderr %q", s, out.String(), errOut.String())
		}
	case <-time.After(time.Minute):
		t.Fatal("Enter did not end the value")
	}
	if !stored(t, root, secret.MustParse("secret://test/k")) {
		t.Error("the store does not hold secret://test/k")
	}

	after, err := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if *after != *before {
		t.Errorf("terminal modes after = %+v, want them restored to %+v", *after, *before)
	}
	// Echo is back: a line typed now appears on the screen, after which anything
	// the value would have echoed has been read.
	if _, err := ptm.WriteString("echo-is-back\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the terminal", screen.String, "echo-is-back")
	if strings.Contains(screen.String()+out.String()+errOut.String(), "sk-typed-value") {
		t.Errorf("the typed value was shown: screen %q, stdout %q, stderr %q", screen.String(), out.String(), errOut.String())
	}
}

// DH-T-CLI-16, interrupted, with the built binary on a pseudo-terminal that is
// its controlling terminal: Ctrl-C typed while the hidden value is half entered
// ends `dsh secret set` with status 130, the terminal's modes are as they were,
// echo on again, nothing typed was shown and nothing is stored.
//
//dsh:test DH-T-CLI-16
func TestSecretSetInterruptedOnPseudoTerminalRestoresIt(t *testing.T) {
	bin := buildDsh(t)
	ptm, pts := openPTY(t)
	before, err := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if before.Lflag&unix.ISIG == 0 || before.Cc[unix.VINTR] != 3 {
		t.Fatalf("the pty does not turn Ctrl-C into SIGINT: lflag %#x, intr %d", before.Lflag, before.Cc[unix.VINTR])
	}
	var screen lockedBuffer
	read, ran := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() {
		// Closing both ends ends the reader and any read Main still blocks on.
		_, _ = pts.Close(), ptm.Close()
		<-read
		<-ran
	})
	go func() {
		defer close(read)
		buf := make([]byte, 256)
		for {
			n, err := ptm.Read(buf)
			_, _ = screen.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()

	root := t.TempDir()
	cmd := exec.Command(bin, "--home", root, "secret", "set", "TEST_K")
	cmd.Dir = t.TempDir()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = pts, pts, pts
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	waitFor(t, "the terminal", screen.String, secretPrompt)
	if _, err := ptm.WriteString("sk-half-typed"); err != nil {
		t.Fatal(err)
	}
	if _, err := ptm.Write([]byte{before.Cc[unix.VINTR]}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-exited:
	case <-time.After(time.Minute):
		t.Fatal("Ctrl-C did not end secret set")
	}
	if code := cmd.ProcessState.ExitCode(); code != output.Interrupted {
		t.Errorf("exit status = %d, want %d; screen %q", code, output.Interrupted, screen.String())
	}
	waitFor(t, "the terminal", screen.String, "interrupted")

	after, err := unix.IoctlGetTermios(int(pts.Fd()), unix.TCGETS)
	if err != nil {
		t.Fatal(err)
	}
	if *after != *before {
		t.Errorf("terminal modes after = %+v, want them restored to %+v", *after, *before)
	}
	if _, err := ptm.WriteString("echo-is-back\n"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the terminal", screen.String, "echo-is-back")
	if strings.Contains(screen.String(), "sk-half-typed") {
		t.Errorf("the typed value was shown: %q", screen.String())
	}
	if stored(t, root, secret.MustParse("secret://test/k")) {
		t.Error("an interrupted entry stored secret://test/k")
	}
}
