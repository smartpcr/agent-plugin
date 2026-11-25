package shellexec

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Options are what an executor is built with besides its dialect.
type Options struct {
	// Sandbox is the seam that makes the launch plan of every command (C37);
	// required.
	Sandbox sandbox.Service
	// Env builds each command's child environment (shellexec/env); required.
	Env Environment
	// Spill keeps the part of a stream that leaves its tail; nil keeps the
	// tail only.
	Spill spill.Store
}

// Executor runs one dialect's commands.
type Executor struct {
	shell  Shell
	limits Limits
	opts   Options
}

// New returns the executor of shell.
func New(shell Shell, o Options) (*Executor, error) {
	if shell == nil || !shell.Dialect().Valid() {
		return nil, herr.New(ReasonBadConfig, "an executor needs a shell of a known dialect")
	}
	if o.Sandbox == nil {
		return nil, herr.New(ReasonBadConfig, "an executor needs the sandbox seam: every command carries a launch plan")
	}
	if o.Env == nil {
		return nil, herr.New(ReasonBadConfig, "an executor needs an environment builder: the child environment is built explicitly")
	}
	limits := shell.Limits().WithDefaults()
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	if p := shell.Program(); !filepath.IsAbs(p.Path) {
		return nil, herr.Newf(ReasonBadConfig, "the %s program %q is not an absolute path", shell.Dialect(), p.Path)
	}
	return &Executor{shell: shell, limits: limits, opts: o}, nil
}

// Dialect is the executor's language.
func (e *Executor) Dialect() Dialect { return e.shell.Dialect() }

// Program is the resolved shell program, with its edition.
func (e *Executor) Program() Program { return e.shell.Program() }

// Limits are the executor's budgets.
func (e *Executor) Limits() Limits { return e.limits }

// Resolve returns req with every default and cap applied: the executor's
// dialect, the default timeout, a timeout above the cap cut to the cap, a clean
// directory and a trimmed description. It refuses a request that cannot run:
// another dialect, a blank command or one holding NUL, a relative directory, a
// negative timeout, or no policy.
func (e *Executor) Resolve(req Request) (Request, error) {
	d := e.shell.Dialect()
	switch {
	case req.Shell == "":
		req.Shell = d
	case req.Shell != d:
		return Request{}, herr.Newf(ReasonBadRequest, "a %s request was given to the %s executor", req.Shell, d)
	}
	if blank(req.Command) {
		return Request{}, herr.New(ReasonBadRequest, "the command is empty")
	}
	if strings.ContainsRune(req.Command, 0) {
		return Request{}, herr.New(ReasonBadRequest, "the command holds a NUL character")
	}
	if req.Dir != "" {
		if !filepath.IsAbs(req.Dir) {
			return Request{}, herr.Newf(ReasonBadRequest, "the working directory %q is not absolute", req.Dir)
		}
		req.Dir = filepath.Clean(req.Dir)
	}
	switch {
	case req.Timeout < 0:
		return Request{}, herr.Newf(ReasonBadRequest, "the timeout %s is negative", req.Timeout)
	case req.Timeout == 0:
		req.Timeout = e.limits.Timeout
	case req.Timeout > e.limits.MaxTimeout:
		req.Timeout = e.limits.MaxTimeout
	}
	if req.Policy.IsZero() {
		return Request{}, herr.New(ReasonBadRequest, "the request has no sandbox policy: a command runs under its policy or not at all")
	}
	if err := req.Policy.Validate(); err != nil {
		return Request{}, err
	}
	req.Description = strings.TrimSpace(req.Description)
	req.Env = slices.Clone(req.Env)
	return req, nil
}

// Start resolves req and starts it: it builds the dialect's argv and the child
// environment, asks the sandbox seam for the launch plan and starts the command
// through subprocess.Start, which confirms the plan before it returns. The run
// ends when the command exits, its timeout passes, ctx ends or Stop is called;
// the first cause wins (DH-ADR-EXEC-4).
//
// Start returns an error only for a request Resolve refuses. A command that
// cannot be started (no plan, a refused confinement, a failed start) is an
// Execution that is already done, with a Failed outcome carrying the error.
func (e *Executor) Start(ctx context.Context, req Request) (*Execution, error) {
	r, err := e.Resolve(req)
	if err != nil {
		return nil, err
	}
	x := &Execution{req: r, program: e.shell.Program(), started: time.Now(), done: make(chan struct{})}
	argv, err := e.shell.Argv(r.Command, x.scratchDir)
	if err != nil {
		return x.fail(err), nil
	}
	env, err := e.environ(ctx, r)
	if err != nil {
		return x.fail(err), nil
	}
	plan, err := e.opts.Sandbox.Prepare(ctx, r.Policy, argv)
	if err != nil {
		return x.fail(err), nil
	}
	collect := func(name string) *subprocess.Collector {
		return subprocess.NewCollector(subprocess.CollectOptions{
			TailBytes:  e.limits.TailBytes,
			Spill:      e.opts.Spill,
			SpillOwner: r.Owner,
			SpillName:  string(r.Shell) + "-" + name + ".txt",
			SpillBytes: e.limits.SpillBytes,
		})
	}
	x.stdout, x.stderr = collect("stdout"), collect("stderr")
	// The run's context is cut from ctx's cancellation so every stop cause
	// (ctx, the timeout, Stop) goes through x.cancel, which refuses a cause
	// once the shell has exited: a cause that arrives after the exit does not
	// relabel the run.
	run, stop := context.WithCancelCause(context.WithoutCancel(ctx))
	x.run, x.stop = run, stop
	unwatch := context.AfterFunc(ctx, func() { x.cancel(context.Cause(ctx)) })
	p, err := subprocess.Start(run, subprocess.Spec{
		Argv:   argv,
		Dir:    r.Dir,
		Env:    env,
		Stdout: x.stdout,
		Stderr: x.stderr,
		Grace:  e.limits.Grace,
		Drain:  e.limits.Drain,
		Launch: plan,
	})
	if err != nil {
		unwatch()
		stop(nil)
		return x.fail(err), nil
	}
	x.proc = p
	// The timeout runs from the confirmed start: the stage's handshake is not
	// the command's time.
	timer := time.AfterFunc(r.Timeout, func() { x.cancel(ErrTimedOut) })
	go x.watch(func() { timer.Stop(); unwatch() })
	return x, nil
}

// environ is the child environment of r: the dialect's defaults and the call's
// entries through the builder, then the dialect's unset names removed.
func (e *Executor) environ(ctx context.Context, r Request) (subprocess.Env, error) {
	env, err := e.opts.Env.Environ(ctx, append(e.shell.Defaults(), r.Env...))
	if err != nil {
		return nil, err
	}
	unset := e.shell.Unset()
	if len(unset) == 0 {
		return env, nil
	}
	out := make(subprocess.Env, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.ContainsFunc(unset, func(u string) bool { return sameName(u, name) }) {
			out = append(out, kv)
		}
	}
	return out, nil
}

// sameName compares environment names as the system does.
func sameName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Kind is how a run ended.
type Kind string

// The kinds of outcome.
const (
	// Exited is a command that ended by itself; ExitCode is its status.
	Exited Kind = "exited"
	// TimedOut is a command stopped when its timeout passed.
	TimedOut Kind = "timed-out"
	// Cancelled is a command stopped because the caller's context ended or Stop
	// was called.
	Cancelled Kind = "cancelled"
	// Failed is a command that never ran; Err says why.
	Failed Kind = "failed"
)

// Outcome is how one run ended.
type Outcome struct {
	Kind Kind
	// ExitCode is the shell's exit status, or -1 when it did not exit by itself
	// with a status (Failed, or ended by a signal).
	ExitCode int
	// Stdout and Stderr are the streams' tails, sizes, truncation and spill
	// references; empty for Failed.
	Stdout, Stderr subprocess.Collected
	// Confinement is what the launch plan was confirmed under; zero for Failed.
	Confinement sandbox.Confinement
	// Duration is from Start to the end of the run.
	Duration time.Duration
	// Timeout is the resolved timeout the run had.
	Timeout time.Duration
	// Cause is the first stop cause of a TimedOut or Cancelled run (ErrTimedOut,
	// the context's cause, or Stop's cause).
	Cause error
	// Err is why a Failed command never ran.
	Err error
}

// Execution is one started command. Its foreground result (Wait) and its
// background reads (Reader) are views of the same process.
type Execution struct {
	req     Request
	program Program
	started time.Time

	proc           *subprocess.Process
	stdout, stderr *subprocess.Collector
	run            context.Context
	stop           context.CancelCauseFunc

	// endMu orders a stop cause against the shell's exit: once ended is set,
	// cancel records no cause.
	endMu sync.Mutex
	ended bool

	scratchMu sync.Mutex
	scratch   string

	done    chan struct{}
	outcome Outcome
}

// Request is the resolved request the execution runs.
func (x *Execution) Request() Request { return x.req }

// Program is the shell program the execution runs.
func (x *Execution) Program() Program { return x.program }

// Done is closed when the outcome is known.
func (x *Execution) Done() <-chan struct{} { return x.done }

// Outcome is the run's outcome once Done is closed; ok is false before.
func (x *Execution) Outcome() (o Outcome, ok bool) {
	select {
	case <-x.done:
		return x.outcome, true
	default:
		return Outcome{}, false
	}
}

// Wait is the foreground read: it waits for the outcome, or returns ctx's error
// when ctx ends first. Ending ctx here does not stop the command, which then
// goes on as a background run; Stop stops it.
func (x *Execution) Wait(ctx context.Context) (Outcome, error) {
	select {
	case <-x.done:
		return x.outcome, nil
	case <-ctx.Done():
		return Outcome{}, context.Cause(ctx)
	}
}

// Reader is a background read of stream s: a non-consuming reader from the
// start of what the tail still holds. Several readers keep their own offsets.
// It is nil for a Failed execution.
func (x *Execution) Reader(s subprocess.Stream) *subprocess.OutputReader {
	c := x.stdout
	if s == subprocess.StreamStderr {
		c = x.stderr
	}
	if c == nil || x.proc == nil {
		return nil
	}
	return c.Reader()
}

// Stop stops the command with cause (nil is ErrStopped) unless an earlier cause
// won, and waits for the outcome or the end of ctx.
func (x *Execution) Stop(ctx context.Context, cause error) (Outcome, error) {
	if cause == nil {
		cause = ErrStopped
	}
	x.cancel(cause)
	return x.Wait(ctx)
}

// cancel stops the run with cause unless the shell has already exited, in
// which case the run ended by itself and cause is not its cause.
func (x *Execution) cancel(cause error) {
	if x.stop == nil {
		return
	}
	x.endMu.Lock()
	defer x.endMu.Unlock()
	if !x.ended {
		x.stop(cause)
	}
}

// exit marks the shell as exited and returns the run's stop cause as it stood
// then; no later cause is recorded.
func (x *Execution) exit() error {
	x.endMu.Lock()
	defer x.endMu.Unlock()
	x.ended = true
	return context.Cause(x.run)
}

// scratchDir creates the execution's private directory on first call, where
// the command sees it (privateParent).
func (x *Execution) scratchDir() (string, error) {
	x.scratchMu.Lock()
	defer x.scratchMu.Unlock()
	if x.scratch == "" {
		parent, prefix := privateParent(x.req.Policy)
		dir, err := os.MkdirTemp(parent, prefix)
		if err != nil {
			return "", herr.Wrap(ReasonScript, err, "create the command's private directory")
		}
		x.scratch = dir
	}
	return x.scratch, nil
}

// privateParent is where a command's private directory (the PowerShell script
// file) is made: a place the command reads under its policy, chosen without
// widening it. A workspace-write confiner may hide the host's temporary
// directory (bubblewrap mounts a private /tmp and binds back only the write
// roots), so under workspace-write the directory goes below the policy's
// scratch root, else, hidden, below its first write root. A read-only or
// unconfined command reads the host's temporary directory as it is.
func privateParent(p sandbox.Policy) (dir, prefix string) {
	if p.Mode() == sandbox.WorkspaceWrite {
		roots := p.WriteRoots()
		for _, r := range roots {
			if r.Source == sandbox.SourceScratch {
				return r.Path, "dsh-shell-"
			}
		}
		if len(roots) > 0 {
			return roots[0].Path, ".dsh-shell-"
		}
	}
	return "", "dsh-shell-"
}

// cleanup removes the private directory; its error joins the outcome's only
// when the outcome has none of its own to report.
func (x *Execution) cleanup() error {
	x.scratchMu.Lock()
	defer x.scratchMu.Unlock()
	if x.scratch == "" {
		return nil
	}
	err := os.RemoveAll(x.scratch)
	x.scratch = ""
	if err != nil {
		return herr.Wrap(ReasonScript, err, "remove the command's private directory")
	}
	return nil
}

// fail settles x as Failed with err: the command never ran.
func (x *Execution) fail(err error) *Execution {
	if cerr := x.cleanup(); cerr != nil {
		err = errors.Join(err, cerr)
	}
	x.outcome = Outcome{Kind: Failed, ExitCode: -1, Duration: time.Since(x.started), Timeout: x.req.Timeout, Err: err}
	close(x.done)
	return x
}

// watch waits for the command, classifies the end by its first stop cause,
// stops what is left of the tree and settles the outcome. The cause is fixed
// when Wait returns (exit): a timeout, context end or Stop that comes later
// does not turn a finished run into TimedOut or Cancelled. Wait returns at most
// Limits.Drain after the shell exits even when a descendant still holds a
// stream, so a finished shell's outcome is not held back by what it left
// running; that is stopped here.
func (x *Execution) watch(cancelTimer context.CancelFunc) {
	p := x.proc
	_ = p.Wait() //lint:ignore errdiscard the exit error is the exit code and the stop cause, both read below
	cause := x.exit()
	cancelTimer()
	o := Outcome{Kind: Exited, ExitCode: p.ExitCode(), Confinement: p.Confinement(), Timeout: x.req.Timeout}
	switch {
	case cause == nil:
	case errors.Is(cause, ErrTimedOut):
		o.Kind, o.Cause = TimedOut, cause
	default:
		o.Kind, o.Cause = Cancelled, cause
	}
	// A one-shot command owns nothing after its shell exits: what it left
	// running is stopped with it.
	if err := p.StopTree(context.Background(), 0); err != nil {
		o.Err = err
	}
	x.stop(nil)
	o.Stdout, o.Stderr = x.stdout.Collected(), x.stderr.Collected()
	if err := x.cleanup(); err != nil && o.Err == nil {
		o.Err = err
	}
	o.Duration = time.Since(x.started)
	x.outcome = o
	close(x.done)
}
