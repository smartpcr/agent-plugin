// Package bash is the bash dialect of the shell executor (shellexec.bash,
// DH-D-EXEC-8): `bash -c <command>` with BASH_ENV and ENV removed so no start-up
// file runs, and NO_COLOR=1 TERM=dumb PAGER=cat GIT_PAGER=cat in the child's
// environment.
package bash

import (
	"slices"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
)

// Config is shellexec/bash.Config (UP-CFG-dsh-bash-local, UP-CFG-dsh-bash-sandbox).
// The zero Config is the defaults.
type Config struct {
	// Path is the bash program; empty resolves it (Find).
	Path string `json:"path,omitempty"`
	// Timeout is the default timeout (120 s); MaxTimeout the cap (600 s).
	Timeout    time.Duration `json:"timeout,omitempty"`
	MaxTimeout time.Duration `json:"maxTimeout,omitempty"`
	// TailBytes is each stream's tail kept in memory (64,000).
	TailBytes int `json:"tailBytes,omitempty"`
	// SpillBytes caps each stream's spill file (64 MiB).
	SpillBytes int64 `json:"spillBytes,omitempty"`
	// Grace is the stop ladder's wait between rungs (3 s).
	Grace time.Duration `json:"grace,omitempty"`
}

// Limits are the Config's budgets, defaults applied.
func (c Config) Limits() shellexec.Limits {
	return shellexec.Limits{Timeout: c.Timeout, MaxTimeout: c.MaxTimeout, TailBytes: c.TailBytes, SpillBytes: c.SpillBytes, Grace: c.Grace}.WithDefaults()
}

// Defaults are the entries bash adds to the child's environment.
var Defaults = []string{"NO_COLOR=1", "TERM=dumb", "PAGER=cat", "GIT_PAGER=cat"}

// Unset are the names removed from the child's environment: each makes a
// non-interactive bash (or sh) read a start-up file.
var Unset = []string{"BASH_ENV", "ENV"}

// Find resolves the bash program on h: path when set; else, on Windows, Git for
// Windows under %ProgramFiles%\Git; else bash on the child's PATH, on Windows
// skipping %SystemRoot%\System32 and WindowsApps, whose bash.exe starts WSL, a
// different path namespace.
func Find(path string, h shellexec.Host) (shellexec.Program, error) {
	if path != "" {
		p, err := h.Configured(path)
		if err != nil {
			return shellexec.Program{}, err
		}
		return shellexec.Program{Path: p, Edition: "bash", Source: shellexec.SourceConfig}, nil
	}
	if h.Windows() {
		if pf := h.Get("ProgramFiles"); h.IsAbs(pf) {
			if p := h.Join(pf, "Git", "bin", "bash.exe"); h.Exists(p) {
				return shellexec.Program{Path: p, Edition: "bash", Source: shellexec.SourceInstall}, nil
			}
		}
	}
	if p, ok := h.Search("bash", wslLauncher(h)); ok {
		return shellexec.Program{Path: p, Edition: "bash", Source: shellexec.SourcePath}, nil
	}
	return shellexec.Program{}, herr.New(shellexec.ReasonNotFound, "bash is not found: set its path, or put it on PATH")
}

// wslLauncher rejects, on Windows, the directories whose bash.exe is the WSL
// launcher; elsewhere it rejects nothing.
func wslLauncher(h shellexec.Host) func(string) bool {
	if !h.Windows() {
		return nil
	}
	system32 := ""
	if root := h.Get("SystemRoot"); h.IsAbs(root) {
		system32 = h.Clean(h.Join(root, "System32"))
	}
	return func(dir string) bool {
		d := h.Clean(dir)
		if system32 != "" && d == system32 {
			return true
		}
		return slices.Contains(strings.Split(d, `\`), "WINDOWSAPPS")
	}
}

// Shell is the bash dialect.
type Shell struct {
	program shellexec.Program
	limits  shellexec.Limits
}

var _ shellexec.Shell = (*Shell)(nil)

// NewShell resolves cfg's program on h and returns the dialect.
func NewShell(cfg Config, h shellexec.Host) (*Shell, error) {
	limits := cfg.Limits()
	if err := limits.Validate(); err != nil {
		return nil, err
	}
	p, err := Find(cfg.Path, h)
	if err != nil {
		return nil, err
	}
	return &Shell{program: p, limits: limits}, nil
}

// New resolves the program against the child's environment on this machine and
// returns the bash executor.
func New(cfg Config, h shellexec.Host, o shellexec.Options) (*shellexec.Executor, error) {
	sh, err := NewShell(cfg, h)
	if err != nil {
		return nil, err
	}
	return shellexec.New(sh, o)
}

// Dialect is bash.
func (*Shell) Dialect() shellexec.Dialect { return shellexec.Bash }

// Program is the resolved bash.
func (s *Shell) Program() shellexec.Program { return s.program }

// Limits are the configured budgets.
func (s *Shell) Limits() shellexec.Limits { return s.limits }

// Argv is `bash -c <command>`; the command is one argv element, parsed by bash.
func (s *Shell) Argv(command string, _ func() (string, error)) ([]string, error) {
	return []string{s.program.Path, "-c", command}, nil
}

// Defaults are NO_COLOR=1 TERM=dumb PAGER=cat GIT_PAGER=cat.
func (*Shell) Defaults() []string { return slices.Clone(Defaults) }

// Unset are BASH_ENV and ENV.
func (*Shell) Unset() []string { return slices.Clone(Unset) }
