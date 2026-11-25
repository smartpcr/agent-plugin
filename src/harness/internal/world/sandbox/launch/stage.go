package launch

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// The stage's own exit codes. They are a courtesy for a person reading a shell's
// status: the host decides from the channel, never from the exit code or the
// output (execution.md, "the launcher failed" is never guessed).
const (
	// ExitFailed: the stage ended before the command started (bad arguments, a
	// confinement or canary failure, a rejection or end of file before go).
	ExitFailed = 125
	// ExitCannotRun: after go, the program exists but could not be started.
	ExitCannotRun = 126
	// ExitNotFound: after go, the program does not exist.
	ExitNotFound = 127
)

// TestHookCanaryEnv is read only by a binary built with the testhook tag: when it
// is "written", the stage reports canary written whatever its attempt showed, so a
// host test can prove a confined plan with a writable canary is refused (TC-04b).
// A build without the tag ignores it (testhook.go).
const TestHookCanaryEnv = "DSH_TESTHOOK_CANARY"

// canaryHook adjusts the canary result the stage reports. It is the identity
// except in a testhook build.
var canaryHook = func(c Canary) Canary { return c }

// confiner confines the running stage for its backend and returns the effects it
// governs. A backend that confines the stage from outside (bubblewrap wraps the
// stage as its command) has no step of its own and claims nothing here; what it
// governs comes from the start-up probe and the canary.
type confiner func(Plan) ([]sandbox.Effect, error)

// confiners are the backends this build can run a confined stage under.
// Landlock's self-restriction is DH-D-SANDBOX-4 (impl.md 13.4); until it lands a
// landlock plan ends the stage before ready.
var confiners = map[sandbox.Backend]confiner{
	sandbox.BackendBwrap: func(Plan) ([]sandbox.Effect, error) { return nil, nil },
}

// ServeIfAsked runs the stage when args (os.Args[1:]) start with Mode and returns
// its exit status and true; otherwise it returns false at once. cmd/dsh calls it
// before anything else, so nothing of the harness boots inside the stage.
func ServeIfAsked(args []string, stderr io.Writer) (int, bool) {
	if len(args) == 0 || args[0] != Mode {
		return 0, false
	}
	return Main(args[1:], stderr), true
}

// Main is the stage: it parses the plan, proves it, sends ready, waits for go and
// becomes the command. It returns only when the command did not start (with
// ExitFailed, ExitCannotRun or ExitNotFound) or, on Windows, with the command's
// own exit code. Diagnostics go to stderr, prefixed "dsh sandbox-launch: failed:".
func Main(args []string, stderr io.Writer) int {
	p, err := Parse(args)
	if err != nil {
		return report(stderr, err, ExitFailed)
	}
	control, err := openChannel(p.Control, "control")
	if err != nil {
		return report(stderr, err, ExitFailed)
	}
	defer control.Close()
	status, err := openChannel(p.Status, "status")
	if err != nil {
		return report(stderr, err, ExitFailed)
	}
	defer status.Close()
	if err := closeOnExec(control, status); err != nil {
		return report(stderr, herr.Wrap(sandbox.CodeLaunchFailed, err, "mark the channel close-on-exec"), ExitFailed)
	}
	ready, err := prove(p)
	if err != nil {
		return report(stderr, err, ExitFailed)
	}
	b, err := EncodeReady(ready)
	if err != nil {
		return report(stderr, err, ExitFailed)
	}
	if _, err := status.Write(b); err != nil {
		return report(stderr, herr.Wrap(sandbox.CodeLaunchFailed, err, "send ready"), ExitFailed)
	}
	f, err := readFrame(control)
	switch {
	case closed(err):
		return report(stderr, failed("the host closed the channel before go; no command ran"), ExitFailed)
	case err != nil:
		return report(stderr, err, ExitFailed)
	case f.Msg != msgGo || f.Backend != nil || f.Governed != nil || f.Canary != nil || f.Error != nil:
		return report(stderr, failed("the host sent %q, not go; no command ran", f.Msg), ExitFailed)
	}
	_ = control.Close()
	code, err := subprocess.Become(p.Argv, os.Environ(), p.Keep, func() { _ = status.Close() })
	if err != nil {
		msg := err.Error()
		_ = writeFrame(status, frame{Msg: msgExecFailed, Error: &msg})
		code := ExitCannotRun
		if errors.Is(err, fs.ErrNotExist) {
			code = ExitNotFound
		}
		return report(stderr, herr.Wrap(sandbox.CodeExecFailed, err, "start the command"), code)
	}
	return code
}

// openChannel wraps the inherited descriptor fd and proves it is open. os.NewFile
// does not look at the descriptor, so Stat (fstat on unix, GetFileType and
// friends on Windows) is what turns a closed or never-inherited descriptor into
// a precise failure before anything is written to it.
func openChannel(fd uintptr, which string) (*os.File, error) {
	f := os.NewFile(fd, "sandbox-launch "+which)
	if f == nil {
		return nil, failed("the %s descriptor %d is not valid", which, fd)
	}
	if _, err := f.Stat(); err != nil {
		_ = f.Close()
		return nil, herr.Wrap(sandbox.CodeLaunchFailed, err, fmt.Sprintf("the %s descriptor %d is not open", which, fd))
	}
	return f, nil
}

// prove does what the plan's kind requires before ready: nothing for an
// unconfined plan; for a confined one, confine, then try the canary.
func prove(p Plan) (Ready, error) {
	if p.Kind == Unconfined {
		return Ready{Backend: sandbox.BackendNone, Governed: []sandbox.Effect{}, Canary: canaryHook(CanaryNotTried)}, nil
	}
	confine, ok := confiners[p.Backend]
	if !ok {
		return Ready{}, failed("backend %s cannot confine the stage in this build", p.Backend)
	}
	governed, err := confine(p)
	if err != nil {
		return Ready{}, herr.Wrap(sandbox.CodeLaunchFailed, err, "confine the stage under "+string(p.Backend))
	}
	c, err := tryCanary(p.Canary)
	if err != nil {
		return Ready{}, err
	}
	if governed == nil {
		governed = []sandbox.Effect{}
	}
	return Ready{Backend: p.Backend, Governed: governed, Canary: canaryHook(c)}, nil
}

// tryCanary opens path for writing without creating or truncating it. Opening is
// written; a permission or read-only refusal is refused; any other failure (the
// canary is missing) proves nothing and ends the stage.
func tryCanary(path string) (Canary, error) {
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err == nil {
		_ = f.Close()
		return CanaryWritten, nil
	}
	if errors.Is(err, fs.ErrPermission) || readOnly(err) {
		return CanaryRefused, nil
	}
	return "", herr.Wrap(sandbox.CodeLaunchFailed, err, "try the canary")
}

// report writes err to stderr and returns code.
func report(stderr io.Writer, err error, code int) int {
	if stderr != nil {
		fmt.Fprintf(stderr, "dsh %s: failed: %v\n", Mode, err)
	}
	return code
}
