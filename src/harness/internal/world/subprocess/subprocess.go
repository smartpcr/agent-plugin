// Package subprocess is the one place a harness program starts another program
// (DH-D-EXEC-1, SP-12). A program starts from an argument list, never a shell
// string, with an explicit environment: a nil Env is an empty environment, never
// the host's, so nothing the host holds (a model key, DSH_* settings) reaches a
// child unless the caller put it there (SP-11). BuildEnv is the one way to derive a
// child environment from the host's; it scrubs secrets and DSH_* names.
//
// On Windows a batch file (.bat, .cmd) is refused unless Spec.Batch is
// AllowChecked; then it runs through cmd.exe and any argument cmd.exe cannot pass
// unchanged is refused before anything starts (DH-ADR-NODEPROC-1).
// LookupProgram finds such a file by bare name and says that it is one.
//
// Every child but a detached one (Spec.Detach) is placed in a tree owner when it
// starts, and StopTree stops and awaits the whole tree (DH-D-EXEC-2): a Job Object
// on Windows, which the child is created inside in one CreateProcess call; on
// Linux a control group per command inside the host's own cgroup v2 directory
// when that is writable, otherwise a process group; a process group on other Unix
// systems. The
// owner kind is chosen once per process and reported on every handle and by
// TreeOwner, so a fallback is visible (register U-K1-1).
//
// The archtest rule "onespawn" (and the source check DH-T-EXEC-1) fails any
// non-test harness package other than this one that imports os/exec or calls a
// process-creation function.
//
// Every Spec carries a sandbox.Launch (A10): the zero Launch is refused, and until
// DH-S-13 builds confined plans each caller passes sandbox.Trusted with its reason.
// Run is the run-and-capture helper on Start (DH-LIB-EXECA): a deadline, a capture
// limit that stops the whole tree, and the tail of what passed the limit. Confirm
// asks a person and refuses when standard input is not a terminal.
//
// Spec.Extra hands the child private byte channels beside its standard streams.
//
// The cordis provider (subprocess/local), confined launch plans, PTYs and the
// output collectors of the execution area are later steps; they build on Start.
package subprocess

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Reasons the package raises.
const (
	ReasonEmptyArgv       herr.Reason = "subprocess/empty-argv"
	ReasonRelativeProgram herr.Reason = "subprocess/relative-program"
	ReasonNotFound        herr.Reason = "subprocess/not-found"
	ReasonBatchRefused    herr.Reason = "subprocess/batch-refused"
	ReasonBadArgument     herr.Reason = "subprocess/bad-argument"
	ReasonLineTooLong     herr.Reason = "subprocess/line-too-long"
	ReasonStartFailed     herr.Reason = "subprocess/start-failed"
	ReasonTreeOwner       herr.Reason = "subprocess/tree-owner"
	ReasonSurvivors       herr.Reason = "subprocess/tree-survivors"
	ReasonNoLaunch        herr.Reason = "subprocess/no-launch"
	ReasonOutputLimit     herr.Reason = "subprocess/output-limit"
	ReasonDeadline        herr.Reason = "subprocess/deadline"
	ReasonCanceled        herr.Reason = "subprocess/canceled"
	ReasonDetached        herr.Reason = "subprocess/detached"
)

// Reasons lists every reason, for the declaration test.
var Reasons = []herr.Reason{
	ReasonEmptyArgv, ReasonRelativeProgram, ReasonNotFound, ReasonBatchRefused,
	ReasonBadArgument, ReasonLineTooLong, ReasonStartFailed, ReasonTreeOwner, ReasonSurvivors,
	ReasonNoLaunch, ReasonOutputLimit, ReasonDeadline, ReasonCanceled, ReasonDetached,
}

// DefaultSettle bounds the wait for a tree to be gone after the forced stop, when
// the caller's context has no earlier deadline (subprocess/local.Config.Settle).
const DefaultSettle = 5 * time.Second

// maxWindowsLine is the longest command line CreateProcess takes, in UTF-16 units.
const maxWindowsLine = 32767

// Spec says what to start.
type Spec struct {
	// Argv is the program and its arguments. Argv[0] is an absolute path, as
	// Lookup returns; a shell command is one element of the shell's argv.
	Argv []string
	// Dir is the working directory; empty is the host's.
	Dir string
	// Env is the whole child environment. Nil is empty: nothing is inherited.
	// Start passes it as given and does not filter it: by DH-ADR-EXEC-8 it is the
	// caller's explicit choice, built by BuildEnv, whose entries and managed
	// snapshot (DSH_HOME, a lease's value) are deliberate. A caller that wants the
	// host's variables gets them through BuildEnv, never by passing os.Environ().
	Env Env
	// Stdin is the standard input (StdinMode); nil is the null device.
	Stdin StdinMode
	// Stdout and Stderr are where the standard output and error go (OutputMode);
	// nil is the null device, and Run captures a nil stream.
	Stdout, Stderr OutputMode
	// Grace is how long StopTree waits after the polite stop when the context
	// that started the process ends.
	Grace time.Duration
	// Batch says whether a Windows batch file may be started: Refuse (the zero
	// value) refuses it, AllowChecked runs it through cmd.exe with checked
	// arguments. Other systems ignore it.
	Batch BatchPolicy
	// Launch is the sandbox plan for this command (A10's sandbox.Launch). It is
	// required: the zero Launch is refused with subprocess/no-launch before
	// anything starts. Until DH-S-13 builds confined plans, every caller passes
	// sandbox.Trusted(reason), saying why its child reads nothing the model can
	// write.
	Launch sandbox.Launch
	// Extra are private byte channels the child inherits beside its standard
	// streams (ChildFD says under which number); nothing else is inherited.
	Extra []Channel
	// Detach starts a program meant to outlive the host (DH-LIB-NODEPROC-R12), such
	// as the browser opener: it gets no tree owner, a session (POSIX) or console
	// and process group (Windows) of its own, and is never stopped by the host;
	// StopTree, Kill and TreeGone refuse it with subprocess/detached, a Registry
	// does not hold it, and the end of the start context does not stop it. Its
	// streams are the null device or a file (an *os.File, Discard, Inherit), since
	// the host keeps no copy running for it. The host keeps only a waiter that
	// reaps it when it exits.
	Detach bool

	// waitDelay bounds how long Wait waits for the standard streams once the
	// direct child has exited (exec.Cmd.WaitDelay); Run sets it so a descendant
	// holding a captured stream open cannot keep the run waiting.
	waitDelay time.Duration
}

// Process is one started child and the tree it owns.
type Process struct {
	cmd      *exec.Cmd
	owner    treeOwner
	report   Owner
	detached bool

	exited  chan struct{}
	waitErr error

	stopMu  sync.Mutex
	stopped bool
	stopErr error

	// ownerMu serialises calls on owner; released is set, and releasedC closed,
	// once the whole tree has left and the owner's handles are closed.
	ownerMu   sync.Mutex
	released  bool
	releasedC chan struct{}
}

// watchEvery is how often the context watcher looks for the end of a tree whose
// direct child has already exited.
const watchEvery = 250 * time.Millisecond

// Start validates spec, starts the program inside a new tree owner and returns its
// handle. When ctx ends, the tree is stopped with spec.Grace. The caller must call
// StopTree or Wait.
func Start(ctx context.Context, spec Spec) (*Process, error) {
	if !spec.Launch.Valid() {
		return nil, herr.New(ReasonNoLaunch, "Spec.Launch is empty; the host's own child passes sandbox.Trusted(reason)")
	}
	l, err := check(spec.Argv, spec.Batch)
	if err != nil {
		return nil, err
	}
	if err := checkExtra(spec.Extra); err != nil {
		return nil, err
	}
	if spec.Detach {
		if err := checkDetachStreams(spec); err != nil {
			return nil, err
		}
	}
	if err := prepareExtra(spec.Extra); err != nil {
		return nil, herr.Wrap(ReasonStartFailed, err, "pass the channels to "+filepath.Base(spec.Argv[0]))
	}
	build := func() *exec.Cmd {
		cmd := &exec.Cmd{
			Path:      l.path,
			Args:      append([]string(nil), l.args...),
			Dir:       spec.Dir,
			Env:       spec.Env.block(),
			Stdin:     stdinOf(spec.Stdin),
			Stdout:    outputOf(spec.Stdout, os.Stdout),
			Stderr:    outputOf(spec.Stderr, os.Stderr),
			WaitDelay: spec.waitDelay,
		}
		setCmdLine(cmd, l.cmdLine)
		addExtra(cmd, spec.Extra)
		return cmd
	}
	if spec.Detach {
		return startDetachedProcess(spec, build)
	}
	owner, err := newOwner()
	if err != nil {
		return nil, herr.Wrap(ReasonTreeOwner, err, "create the tree owner")
	}
	cmd, err := owner.start(build)
	if err != nil {
		owner.release()
		return nil, herr.Wrap(ReasonStartFailed, err, "start "+filepath.Base(spec.Argv[0]))
	}
	p := &Process{cmd: cmd, owner: owner, report: owner.report(), exited: make(chan struct{}), releasedC: make(chan struct{})}
	go func() {
		err := cmd.Wait()
		if d, ok := owner.(drainer); ok {
			if derr := d.drain(); err == nil {
				err = derr
			}
		}
		p.waitErr = err
		close(p.exited)
	}()
	if ctx.Done() != nil {
		go p.watch(ctx, spec.Grace)
	}
	return p, nil
}

// watch stops the tree when ctx ends. The direct child exiting does not end the
// watch: descendants it left behind are still the tree's, so the watch lasts until
// the owner is empty and released.
func (p *Process) watch(ctx context.Context, grace time.Duration) {
	exited := p.exited
	var tick <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			_ = p.StopTree(context.WithoutCancel(ctx), grace)
			return
		case <-p.releasedC:
			return
		case <-exited:
			exited = nil
			t := time.NewTicker(watchEvery)
			defer t.Stop()
			tick = t.C
			p.releaseOwner()
		case <-tick:
			p.releaseOwner()
		}
	}
}

// check applies the argv rules that hold on every system, then the Windows ones,
// and returns what Start creates.
func check(argv []string, batch BatchPolicy) (launch, error) {
	if len(argv) == 0 || argv[0] == "" {
		return launch{}, herr.New(ReasonEmptyArgv, "no program to start")
	}
	if !filepath.IsAbs(argv[0]) {
		return launch{}, herr.Newf(ReasonRelativeProgram, "program %q is not an absolute path; resolve it with Lookup", filepath.Base(argv[0]))
	}
	for i, a := range argv {
		if strings.IndexByte(a, 0) >= 0 {
			return launch{}, herr.Newf(ReasonBadArgument, "argument %d holds a NUL byte", i)
		}
	}
	plain := launch{path: argv[0], args: argv}
	if runtime.GOOS != "windows" {
		return plain, nil
	}
	if isBatch(argv[0]) {
		if batch != AllowChecked {
			return launch{}, herr.Newf(ReasonBatchRefused, "%s is a batch file; cmd.exe would parse its arguments (opt in with Spec.Batch = AllowChecked)", filepath.Base(argv[0]))
		}
		return batchLaunch(argv)
	}
	if n := windowsLineLength(argv); n > maxWindowsLine {
		return launch{}, herr.Newf(ReasonLineTooLong, "the command line is %d UTF-16 units, over %d; pass a script file instead", n, maxWindowsLine)
	}
	return plain, nil
}

// isBatch reports whether Windows would run path through cmd.exe. Win32 path
// normalisation drops an alternate data stream suffix (":...") and trailing dots
// and spaces, so `run.bat `, `run.bat.` and `run.cmd::$DATA` are batch files too.
func isBatch(path string) bool {
	base := path[strings.LastIndexAny(path, `\/`)+1:]
	if i := strings.IndexByte(base, ':'); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimRight(base, ". ")
	ext := strings.ToLower(filepath.Ext(base))
	return ext == ".bat" || ext == ".cmd"
}

// Pid is the direct child's process id.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Owner reports which tree owner holds this child.
func (p *Process) Owner() Owner { return p.report }

// Exited is closed when the direct child has exited and its streams are drained.
func (p *Process) Exited() <-chan struct{} { return p.exited }

// Wait waits for the direct child and returns its exit error, as exec.Cmd.Wait.
// It does not wait for the rest of the tree; StopTree and TreeGone do.
func (p *Process) Wait() error {
	<-p.exited
	p.releaseOwner()
	return p.waitErr
}

// ExitCode is the direct child's exit code, or -1 while it runs or when a signal
// ended it.
func (p *Process) ExitCode() int {
	select {
	case <-p.exited:
		return p.cmd.ProcessState.ExitCode()
	default:
		return -1
	}
}

// TreeGone waits until no process of the tree is left, or ctx ends.
func (p *Process) TreeGone(ctx context.Context) error {
	if p.detached {
		return errDetached("TreeGone")
	}
	if err := p.await(ctx); err != nil {
		return p.survivors(err)
	}
	return nil
}

// StopTree stops the whole tree and returns only once every member has exited:
// the polite stop (SIGTERM to every member on Unix; none for a pipe child on
// Windows), up to grace for the tree to leave, then the forced stop (the Job
// Object or control group is killed; SIGKILL to the process group), then a wait
// bounded by ctx and DefaultSettle. When members survive it returns
// subprocess/tree-survivors and a later call tries again. It is idempotent.
func (p *Process) StopTree(ctx context.Context, grace time.Duration) error {
	if p.detached {
		return errDetached("StopTree")
	}
	p.stopMu.Lock()
	defer p.stopMu.Unlock()
	if p.stopped {
		return p.stopErr
	}
	if !p.gone() {
		_ = p.withOwner(treeOwner.polite)
		if grace > 0 {
			gctx, cancel := context.WithTimeout(ctx, grace)
			_ = p.await(gctx)
			cancel()
		}
	}
	if !p.gone() {
		_ = p.withOwner(treeOwner.kill)
	}
	sctx, cancel := settle(ctx)
	defer cancel()
	if err := p.await(sctx); err != nil {
		// One more forced stop: a member may have started after the first.
		_ = p.withOwner(treeOwner.kill)
		return p.survivors(err)
	}
	p.stopped = true
	p.releaseOwner()
	return nil
}

// Kill is the forced stop alone: it kills every member now and returns without
// waiting. It does not wait for a StopTree in progress either, so a forced
// shutdown escalates past a graceful stop still in its grace period. TreeGone or a
// later StopTree observe the exit.
func (p *Process) Kill() error {
	if p.detached {
		return errDetached("Kill")
	}
	if p.gone() {
		return nil
	}
	return p.withOwner(treeOwner.kill)
}

func settle(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return context.WithCancel(ctx)
	}
	return context.WithTimeout(ctx, DefaultSettle)
}

// gone reports whether the direct child has been reaped and the owner is empty.
func (p *Process) gone() bool {
	select {
	case <-p.exited:
	default:
		return false
	}
	p.ownerMu.Lock()
	defer p.ownerMu.Unlock()
	if p.released {
		return true
	}
	empty, err := p.owner.empty()
	return err == nil && empty
}

func (p *Process) withOwner(f func(treeOwner) error) error {
	p.ownerMu.Lock()
	defer p.ownerMu.Unlock()
	if p.released {
		return nil
	}
	return f(p.owner)
}

// pollEvery is how often await looks at the owner; owners have no portable
// "became empty" event that every rung supports.
const pollEvery = 10 * time.Millisecond

func (p *Process) await(ctx context.Context) error {
	t := time.NewTicker(pollEvery)
	defer t.Stop()
	// exited wakes the loop once when the direct child exits; after that only the
	// ticker and ctx do, so a closed channel cannot spin the loop.
	exited := p.exited
	for !p.gone() {
		select {
		case <-ctx.Done():
			return context.Cause(ctx)
		case <-exited:
			exited = nil
		case <-t.C:
		}
	}
	return nil
}

func (p *Process) survivors(cause error) error {
	direct := "exited"
	select {
	case <-p.exited:
	default:
		direct = fmt.Sprintf("pid %d running", p.Pid())
	}
	desc := "owner released"
	_ = p.withOwner(func(o treeOwner) error { desc = o.describe(); return nil })
	return herr.Wrap(ReasonSurvivors, cause, fmt.Sprintf("the tree is not gone (direct child %s; %s: %s)", direct, p.report.Kind, desc))
}

// releaseOwner closes the owner's handles once the whole tree is gone; until then
// the handle stays registered for the next StopTree.
func (p *Process) releaseOwner() {
	if !p.gone() {
		return
	}
	p.ownerMu.Lock()
	defer p.ownerMu.Unlock()
	if !p.released {
		p.owner.release()
		p.released = true
		close(p.releasedC)
	}
}

// OwnerKind names a tree owner.
type OwnerKind string

// The owner kinds, strongest first per system.
const (
	JobObject    OwnerKind = "job-object"
	ControlGroup OwnerKind = "control-group"
	ProcessGroup OwnerKind = "process-group"
)

// Owner reports how a tree is owned.
type Owner struct {
	Kind OwnerKind
	// Exact is true when membership is exact (job object, control group) and false
	// for the best-effort process group, which a member can leave.
	Exact bool
	// Fallback says why a stronger owner was not used; empty when none was skipped.
	Fallback string
}

func (o Owner) String() string {
	if o.Fallback == "" {
		return string(o.Kind)
	}
	return string(o.Kind) + " (fallback: " + o.Fallback + ")"
}

// TreeOwner reports the owner kind chosen for this process, once, on first use.
func TreeOwner() Owner {
	chooseOnce.Do(func() { chosen = choose() })
	return chosen
}

var (
	chooseOnce sync.Once
	chosen     Owner
)

// treeOwner is one tree's owner, per system.
type treeOwner interface {
	// start builds a command with build, configures it and starts it inside the
	// owner; it may build again to retry with a fallback owner.
	start(build func() *exec.Cmd) (*exec.Cmd, error)
	report() Owner
	// polite asks every member to stop.
	polite() error
	// kill forces every member to stop.
	kill() error
	// empty reports whether no member is left.
	empty() (bool, error)
	describe() string
	release()
}

// Lookup resolves a bare program name against the PATH of the child's environment
// env, never the host's. A name with a path separator must already be absolute.
// On Windows, PATHEXT applies, and a name that resolves to a .bat or .cmd file is
// refused (ReasonBatchRefused). A candidate is tested by its attributes and never
// opened.
func Lookup(name string, env Env) (string, error) {
	prog, err := LookupProgram(name, env)
	if err != nil {
		return "", err
	}
	if prog.Batch {
		return "", herr.Newf(ReasonBatchRefused, "%s resolves to a batch file (LookupProgram reports it for a caller that opts in)", name)
	}
	return prog.Path, nil
}

// LookupProgram resolves name as Lookup does, but a batch file is not refused: it
// is reported in Program.Batch, so a caller that accepts the checked batch run
// sets Spec.Batch to AllowChecked and starts Program.Path. Batch is only ever
// true on Windows. An absolute name is returned as given, classified the same way.
func LookupProgram(name string, env Env) (Program, error) {
	if name == "" {
		return Program{}, herr.New(ReasonEmptyArgv, "no program name")
	}
	if strings.ContainsAny(name, `/\`) {
		if !filepath.IsAbs(name) {
			return Program{}, herr.Newf(ReasonRelativeProgram, "program %q is a relative path", name)
		}
		return classify(name), nil
	}
	exts := []string{""}
	if runtime.GOOS == "windows" {
		exts = windowsExts(name, env)
	}
	for _, dir := range filepath.SplitList(env.Get("PATH")) {
		if dir == "" || !filepath.IsAbs(dir) {
			continue
		}
		for _, ext := range exts {
			cand := filepath.Join(dir, name+ext)
			if executable(cand) {
				return classify(cand), nil
			}
		}
	}
	return Program{}, herr.Newf(ReasonNotFound, "%s is not on the child's PATH", name)
}

func classify(path string) Program {
	return Program{Path: path, Batch: runtime.GOOS == "windows" && isBatch(path)}
}

func windowsExts(name string, env Env) []string {
	pathext := env.Get("PATHEXT")
	if pathext == "" {
		pathext = ".COM;.EXE;.BAT;.CMD"
	}
	var exts []string
	if filepath.Ext(name) != "" {
		exts = append(exts, "")
	}
	for _, e := range strings.Split(pathext, ";") {
		if e = strings.TrimSpace(e); e != "" {
			exts = append(exts, strings.ToLower(e))
		}
	}
	return exts
}

func executable(path string) bool {
	fi, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		if fi, err = os.Stat(path); err != nil {
			return false
		}
	}
	if fi.IsDir() {
		return false
	}
	return runtime.GOOS == "windows" || fi.Mode()&0o111 != 0
}
