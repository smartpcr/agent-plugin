// Package tools is shellexec.tools (C40, DH-D-EXEC-9, docs/harness/areas/
// execution.md sec. 6.2 "Tools"): the one-shot bash and pwsh tools. Each call
// builds a shellexec.Request from named arguments only -- command, timeout and
// description; the model can set neither stdin nor the environment, and
// strict decoding refuses any other argument before anything starts. The
// session's sandbox policy (C33) and working directory (C31) are resolved per
// call, the command runs through the dialect's executor (C39), and the result
// is a structured value whose fields output cannot forge: outcome, exit code,
// stop cause, each stream's tail, size, truncation and spill reference, and the
// confinement the launch plan was confirmed under.
//
// Both tools are always declared; a call whose dialect has no mounted executor
// fails with shellexec/not-found. Background runs and promotion on timeout
// need the job registry (DH-D-JOBS-1), which is not mounted in M2: a command
// runs in the foreground and its timeout ends it.
package tools

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/workdir"
	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// Name is the component's catalogue name.
const Name = "shellexec.tools"

// The model-facing tool names.
const (
	BashName = "bash"
	PwshName = "pwsh"
)

// DefaultOutputBytes is the most bytes of each stream's tail a result shows.
const DefaultOutputBytes = 32_000

// OneShotConfig configures the one-shot tools; a zero field is its default.
type OneShotConfig struct {
	// OutputBytes is the most bytes of each stream's tail one result shows;
	// the whole stream is behind the spill reference.
	OutputBytes int `json:"outputBytes,omitempty"`
}

func (c OneShotConfig) normalized() OneShotConfig {
	if c.OutputBytes <= 0 {
		c.OutputBytes = DefaultOutputBytes
	}
	return c
}

// Deps are what the tools run with.
type Deps struct {
	// Shells are the mounted executors by dialect (shellexec.Key); required.
	Shells *shellexec.Registry
	// Resolver resolves each call's sandbox policy (C33); required.
	Resolver sandbox.Resolver
	// Workdir supplies the session's working directory, the command's
	// directory; nil runs in the host's.
	Workdir workdir.Lookup
}

// In is the input of bash and pwsh: named arguments only.
type In struct {
	// Command is the shell text to run.
	Command string `json:"command" dsh:"required" jsonschema:"the command to run"`
	// Timeout is the run's budget in milliseconds; 0 is the shell's default
	// and a value above its cap is cut to the cap.
	Timeout int64 `json:"timeout,omitempty" dsh:"min=0" jsonschema:"optional timeout in milliseconds"`
	// Description says in a few words what the command does.
	Description string `json:"description,omitempty" jsonschema:"what the command does, in 5 to 10 words"`
}

// Result is the structured outcome of one command.
type Result struct {
	// Shell and Edition are the dialect and the program's build.
	Shell   string `json:"shell"`
	Edition string `json:"edition,omitempty"`
	// Command and Description are as run.
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	// Cwd is the directory the command ran in; "" is the host's.
	Cwd string `json:"cwd,omitempty"`
	// Outcome is exited, timed-out or cancelled.
	Outcome string `json:"outcome"`
	// ExitCode is the shell's status, -1 when it did not exit by itself.
	ExitCode int `json:"exit_code"`
	// Stop is why a timed-out or cancelled command was stopped.
	Stop *Stop `json:"stop,omitempty"`
	// TimeoutMs is the resolved budget; DurationMs how long the run took.
	TimeoutMs  int64 `json:"timeout_ms"`
	DurationMs int64 `json:"duration_ms"`
	// Stdout and Stderr are the streams.
	Stdout Stream `json:"stdout"`
	Stderr Stream `json:"stderr"`
	// Confinement is what the launch plan was confirmed under.
	Confinement Confinement `json:"confinement"`
	// Error is a failure after the command ran (stopping what it left running,
	// removing its private directory).
	Error string `json:"error,omitempty"`
}

// Stop is a stopped command's cause and budget.
type Stop struct {
	// Reason is timeout or cancelled.
	Reason   string `json:"reason"`
	BudgetMs int64  `json:"budget_ms"`
	Cause    string `json:"cause,omitempty"`
}

// Stream is one output stream.
type Stream struct {
	// Tail is the end of the stream, at most OneShotConfig.OutputBytes.
	Tail string `json:"tail"`
	// Bytes is the size of the whole stream.
	Bytes int64 `json:"bytes"`
	// Truncated says Tail does not hold the whole stream.
	Truncated bool `json:"truncated,omitempty"`
	// Spill is the saved whole stream, when it did not fit.
	Spill *SpillRef `json:"spill,omitempty"`
	// SpillError says why the whole stream was not saved.
	SpillError string `json:"spill_error,omitempty"`
}

// SpillRef names a saved stream (storage/spill).
type SpillRef struct {
	ID    string `json:"id"`
	Bytes int64  `json:"bytes"`
}

// Confinement is the policy mode and the confinement the host confirmed.
type Confinement struct {
	Mode    string   `json:"mode"`
	State   string   `json:"state"`
	Backend string   `json:"backend"`
	Missing []string `json:"missing,omitempty"`
	Exposed []string `json:"exposed,omitempty"`
}

// The stop reasons.
const (
	StopTimeout   = "timeout"
	StopCancelled = "cancelled"
)

// shell is one tool: its dialect over the shared dependencies.
type shell struct {
	name    string
	dialect shellexec.Dialect
	deps    Deps
	cfg     OneShotConfig
}

// Declarations are the bash and pwsh tools over deps.
func Declarations(deps Deps, cfg OneShotConfig) ([]*htools.Declaration, error) {
	if deps.Shells == nil {
		return nil, errors.New("shellexec.tools: the shell registry is required")
	}
	if deps.Resolver == nil {
		return nil, errors.New("shellexec.tools: the policy resolver is required")
	}
	cfg = cfg.normalized()
	ds := []*htools.Declaration{
		(&shell{name: BashName, dialect: shellexec.Bash, deps: deps, cfg: cfg}).declare(),
		(&shell{name: PwshName, dialect: shellexec.Pwsh, deps: deps, cfg: cfg}).declare(),
	}
	for _, d := range ds {
		if err := d.Err(); err != nil {
			return nil, err
		}
	}
	return ds, nil
}

// Mount registers the tools in svc for ctx's scope.
func Mount(ctx *cordis.Context, svc htools.Service, deps Deps, cfg OneShotConfig) (*cordis.EffectHandle, error) {
	ds, err := Declarations(deps, cfg)
	if err != nil {
		return nil, err
	}
	return svc.RegisterSet(ctx, ds)
}

// Component is shellexec.tools: it injects the shell registry, the policy
// resolver, the working directory and the tool registry.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg OneShotConfig) error {
	shells, err := shellexec.Key.Get(ctx)
	if err != nil {
		return err
	}
	resolver, err := sandbox.ResolverKey.Get(ctx)
	if err != nil {
		return err
	}
	wd, err := workdir.Key.Get(ctx)
	if err != nil {
		return err
	}
	svc, err := htools.Key.Get(ctx)
	if err != nil {
		return err
	}
	_, err = Mount(ctx, svc, Deps{Shells: shells, Resolver: resolver, Workdir: wd}, cfg)
	return err
}, cordis.WithInject(shellexec.Key.Name(), sandbox.ResolverKey.Name(), workdir.Key.Name(), htools.Key.Name()))

// Item is the shellexec.tools catalogue item.
func Item() catalog.Item {
	return catalog.Item{Name: Name, Component: Component, Config: reflect.TypeFor[OneShotConfig]()}
}

// limits are the budgets of the mounted executor, else the defaults.
func (s *shell) limits() (shellexec.Limits, *shellexec.Executor) {
	if e, err := s.deps.Shells.Get(s.dialect); err == nil {
		return e.Limits(), e
	}
	return shellexec.Limits{}.WithDefaults(), nil
}

func (s *shell) declare() *htools.Declaration {
	lim, e := s.limits()
	// The command's own timeout and stop ladder end it before the pipeline's
	// budget does, so the structured result, not tools/timeout, reports it.
	ladder := lim.Grace + lim.Drain + 2*time.Second
	return htools.Define(htools.Spec[In, Result]{
		Name:            s.name,
		Description:     s.description(lim, e),
		Run:             s.run,
		Timeout:         lim.MaxTimeout + ladder + 3*time.Second,
		Grace:           min(ladder, htools.MaxGrace),
		BoundsOwnOutput: true,
		Effects:         []htools.Effect{htools.EffectFiles, htools.EffectProcess, htools.EffectNetwork},
	})
}

func (s *shell) description(lim shellexec.Limits, e *shellexec.Executor) string {
	var b strings.Builder
	what := map[shellexec.Dialect]string{shellexec.Bash: "a bash", shellexec.Pwsh: "a PowerShell"}[s.dialect]
	fmt.Fprintf(&b, "Run %s command once, in the session's working directory, under the session's sandbox policy.", what)
	if e != nil {
		p := e.Program()
		fmt.Fprintf(&b, " Shell: %s (%s).", p.Path, p.Edition)
	}
	fmt.Fprintf(&b, " Nothing carries between calls: the directory and the environment are rebuilt each time, and the command reads no input."+
		" timeout is in milliseconds: default %d, at most %d.", lim.Timeout.Milliseconds(), lim.MaxTimeout.Milliseconds())
	b.WriteString(" The result is JSON: outcome, exit_code, stop, the tail of stdout and stderr (with a spill id when cut) and the confinement.")
	return b.String()
}

func (s *shell) run(ctx context.Context, in In, run htools.Run) (Result, error) {
	e, err := s.deps.Shells.Get(s.dialect)
	if err != nil {
		return Result{}, err
	}
	sess := run.Agent.Session
	if sess == "" {
		return Result{}, herr.New(sandbox.CodeUnavailable, "a shell command needs a session")
	}
	pol, err := s.deps.Resolver.Resolve(ctx, sandbox.Request{Session: sess})
	if err != nil {
		return Result{}, err
	}
	if err := pol.Validate(); err != nil {
		return Result{}, err
	}
	var dir string
	if s.deps.Workdir != nil {
		a, err := s.deps.Workdir.Access(sess)
		if err != nil {
			return Result{}, err
		}
		if dir, err = a.Path(); err != nil {
			return Result{}, err
		}
	}
	x, err := e.Start(ctx, shellexec.Request{
		Shell:       s.dialect,
		Command:     in.Command,
		Dir:         dir,
		Timeout:     time.Duration(in.Timeout) * time.Millisecond,
		Description: in.Description,
		Policy:      pol,
		Owner:       string(sess),
	})
	if err != nil {
		return Result{}, err
	}
	o, err := x.Wait(ctx)
	if err != nil {
		// The call's context ended: Start's watch stops the command; wait for
		// its stop ladder so nothing it started outlives the call.
		lim := e.Limits()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lim.Grace+lim.Drain+time.Second)
		defer cancel()
		if o, err = x.Stop(sctx, context.Cause(ctx)); err != nil {
			return Result{}, err
		}
	}
	if o.Kind == shellexec.Failed {
		return Result{}, o.Err
	}
	return s.result(x, pol, o), nil
}

// result is the structured form of a run that started.
func (s *shell) result(x *shellexec.Execution, pol sandbox.Policy, o shellexec.Outcome) Result {
	req, prog := x.Request(), x.Program()
	r := Result{
		Shell:       string(req.Shell),
		Edition:     prog.Edition,
		Command:     req.Command,
		Description: req.Description,
		Cwd:         req.Dir,
		Outcome:     string(o.Kind),
		ExitCode:    o.ExitCode,
		TimeoutMs:   o.Timeout.Milliseconds(),
		DurationMs:  o.Duration.Milliseconds(),
		Stdout:      stream(o.Stdout, s.cfg.OutputBytes),
		Stderr:      stream(o.Stderr, s.cfg.OutputBytes),
		Confinement: confinement(pol, o.Confinement),
		Error:       errText(o.Err),
	}
	switch o.Kind {
	case shellexec.TimedOut:
		r.Stop = &Stop{Reason: StopTimeout, BudgetMs: o.Timeout.Milliseconds(), Cause: errText(o.Cause)}
	case shellexec.Cancelled:
		r.Stop = &Stop{Reason: StopCancelled, BudgetMs: o.Timeout.Milliseconds(), Cause: errText(o.Cause)}
	}
	return r
}

// stream is c with its tail cut to its last limit bytes, starting on a whole
// UTF-8 character, and invalid bytes replaced so the text is valid.
func stream(c subprocess.Collected, limit int) Stream {
	tail, cut := c.Tail, false
	if len(tail) > limit {
		tail, cut = tail[len(tail)-limit:], true
		for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
			tail = tail[1:]
		}
	}
	st := Stream{
		Tail:       strings.ToValidUTF8(string(tail), "\uFFFD"),
		Bytes:      c.Written,
		Truncated:  c.Truncated || cut,
		SpillError: errText(c.SpillErr),
	}
	if c.Spill != nil {
		st.Spill = &SpillRef{ID: c.Spill.Path, Bytes: c.Spill.Bytes}
	}
	return st
}

func confinement(pol sandbox.Policy, c sandbox.Confinement) Confinement {
	out := Confinement{Mode: string(pol.Mode()), State: string(c.State), Backend: string(c.Backend)}
	for _, m := range c.Missing {
		out.Missing = append(out.Missing, string(m))
	}
	for _, m := range c.Exposed {
		out.Exposed = append(out.Exposed, string(m))
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
