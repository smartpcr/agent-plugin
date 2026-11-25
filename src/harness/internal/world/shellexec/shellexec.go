// Package shellexec is the one-shot shell executor contract (C39, DH-D-EXEC-8):
// a Request with every default and cap made explicit by Resolve, and an Executor
// whose Start builds the dialect's argv, asks the sandbox seam (C37) for the
// launch plan and starts the command through the one starter (C38,
// world/subprocess). It never starts a program itself.
//
// One Execution serves the foreground result (Wait) and background reads
// (Reader) of the same process. A command that could not be started has an
// explicit Failed outcome with its error, apart from any output.
//
// The dialects live in shellexec/bash and shellexec/pwsh; the child environment
// is built by shellexec/env.
package shellexec

import (
	"context"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// The reasons this package fails with.
const (
	// ReasonBadRequest is a Request Resolve refuses: no command, a dialect the
	// executor does not run, a relative directory, a negative timeout, no policy.
	ReasonBadRequest herr.Reason = "shellexec/bad-request"
	// ReasonNotFound is a shell program that is not found by the rules of the
	// dialect (execution.md sec. 6.2, "Finding the program").
	ReasonNotFound herr.Reason = "shellexec/not-found"
	// ReasonBadConfig is a dialect Config that is refused before anything runs.
	ReasonBadConfig herr.Reason = "shellexec/bad-config"
	// ReasonScript is a failure to write the private script file of a command
	// that must run from a file (the PowerShell first-statement rule).
	ReasonScript herr.Reason = "shellexec/script-failed"
	// ReasonTimedOut is the stop cause of a command that outlived its timeout.
	ReasonTimedOut herr.Reason = "shellexec/timed-out"
	// ReasonStopped is the stop cause of Execution.Stop without a cause.
	ReasonStopped herr.Reason = "shellexec/stopped"
)

// Reasons lists every reason this package declares.
var Reasons = []herr.Reason{ReasonBadRequest, ReasonNotFound, ReasonBadConfig, ReasonScript, ReasonTimedOut, ReasonStopped}

// ErrTimedOut is the cause recorded when a command's timeout passes; ErrStopped
// when Execution.Stop is called without a cause.
var (
	ErrTimedOut error = herr.New(ReasonTimedOut, "the command outlived its timeout")
	ErrStopped  error = herr.New(ReasonStopped, "the command was stopped")
)

// Key is the registry of executors by dialect (DH-ADR-EXEC-7); both dialects may
// be mounted at once. The name shell.Key belongs to the page shell (C1).
var Key = cordis.NewServiceKey[*Registry]("shellexec")

// Dialect is a shell language.
type Dialect string

// The dialects.
const (
	Bash Dialect = "bash"
	Pwsh Dialect = "pwsh"
)

// Valid reports a known dialect.
func (d Dialect) Valid() bool { return d == Bash || d == Pwsh }

// Default budgets of both dialects (execution.md sec. 6.2, Config).
const (
	DefaultTimeout    = 120 * time.Second
	DefaultMaxTimeout = 600 * time.Second
	DefaultTailBytes  = 64_000
	DefaultSpillBytes = 64 << 20
	DefaultGrace      = 3 * time.Second
)

// Limits are a dialect's budgets: the default and the largest timeout, the bytes
// of each stream's tail kept in memory, the cap of each stream's spill file, and
// the stop ladder's grace.
type Limits struct {
	Timeout    time.Duration
	MaxTimeout time.Duration
	TailBytes  int
	SpillBytes int64
	Grace      time.Duration
}

// WithDefaults fills every zero field with its default.
func (l Limits) WithDefaults() Limits {
	if l.Timeout == 0 {
		l.Timeout = DefaultTimeout
	}
	if l.MaxTimeout == 0 {
		l.MaxTimeout = DefaultMaxTimeout
	}
	if l.TailBytes == 0 {
		l.TailBytes = DefaultTailBytes
	}
	if l.SpillBytes == 0 {
		l.SpillBytes = DefaultSpillBytes
	}
	if l.Grace == 0 {
		l.Grace = DefaultGrace
	}
	return l
}

// Validate refuses negative budgets and a default timeout above the cap.
func (l Limits) Validate() error {
	switch {
	case l.Timeout < 0, l.MaxTimeout < 0, l.TailBytes < 0, l.SpillBytes < 0, l.Grace < 0:
		return herr.New(ReasonBadConfig, "shell budgets must not be negative")
	case l.MaxTimeout > 0 && l.Timeout > l.MaxTimeout:
		return herr.Newf(ReasonBadConfig, "the default timeout %s is above the cap %s", l.Timeout, l.MaxTimeout)
	}
	return nil
}

// Request is one command to run.
type Request struct {
	// Shell is the dialect; empty is the executor's.
	Shell Dialect
	// Command is the shell text, handed to the shell as one argv element.
	Command string
	// Dir is the absolute working directory (the session's current directory
	// joined with the call's workdir, C31); empty is the host's.
	Dir string
	// Timeout bounds the run; zero is the dialect's default, and a value above
	// the dialect's cap is cut to the cap.
	Timeout time.Duration
	// Description says in a few words what the command does.
	Description string
	// Policy is the resolved sandbox policy the command runs under (C33); it is
	// required: a command runs under its policy or does not run (DH-O-7).
	Policy sandbox.Policy
	// Owner is the session the command's spill files belong to.
	Owner string
	// Env are extra "NAME=value" entries for this call (the script environment
	// of a hosting tool); the environment builder still drops secret names.
	Env []string
}

// Program is a resolved shell program.
type Program struct {
	// Path is the absolute path the command starts.
	Path string
	// Edition names the build: "bash", or PowerShell's "Core" or "Desktop".
	Edition string
	// Source says which rule found it: SourceConfig, SourceInstall, SourcePath
	// or SourceWindowsPowerShell.
	Source string
}

// The rules that find a program.
const (
	SourceConfig            = "config"
	SourceInstall           = "install"
	SourcePath              = "path"
	SourceWindowsPowerShell = "windows-powershell"
)

// Shell is a dialect: its program, its budgets, its argv and its environment
// rules. shellexec/bash and shellexec/pwsh provide one each.
type Shell interface {
	// Dialect is the language this shell runs.
	Dialect() Dialect
	// Program is the resolved program.
	Program() Program
	// Limits are the dialect's budgets, defaults applied.
	Limits() Limits
	// Argv is the whole argv for command; Argv[0] is Program().Path. scratch
	// returns a private directory, created on first call and removed after the
	// run, for a command that must run from a file.
	Argv(command string, scratch func() (string, error)) ([]string, error)
	// Defaults are the "NAME=value" entries the dialect adds to the child's
	// environment (NO_COLOR=1, PAGER=cat, ...).
	Defaults() []string
	// Unset are the names removed from the child's environment after it is
	// built, so no start-up file runs (bash: BASH_ENV, ENV).
	Unset() []string
}

// Environment builds the child environment of one command from the dialect's
// defaults and the call's entries: shellexec/env's Builder. It must drop every
// name the secret store holds (M2-C-11).
type Environment interface {
	Environ(ctx context.Context, entries []string) (subprocess.Env, error)
}

// blank reports whether s holds nothing but white space.
func blank(s string) bool { return strings.TrimSpace(s) == "" }
