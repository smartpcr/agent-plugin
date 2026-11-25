// Package pwsh is the PowerShell dialect of the shell executor (shellexec.pwsh,
// DH-D-EXEC-8): `pwsh -NoLogo -NoProfile -NonInteractive -Command <preamble;
// command>`, with output pinned to UTF-8 by the preamble, and NO_COLOR=1
// PAGER=cat GIT_PAGER=cat in the child's environment. A command whose first
// statement is param, using or #requires cannot follow the preamble in one
// command string, so it is written to a private script file that the preamble
// invokes.
package pwsh

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/shellexec"
)

// Config is shellexec/pwsh.Config (UP-CFG-dsh-pwsh-local, UP-CFG-dsh-pwsh-sandbox).
// The zero Config is the defaults, except AllowWindowsPowerShell, whose default
// (true) is DefaultConfig's.
type Config struct {
	// Path is the PowerShell program; empty resolves it (Find).
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
	// AllowWindowsPowerShell lets resolution fall back to Windows PowerShell
	// 5.1 when no PowerShell 7 is found (true).
	AllowWindowsPowerShell bool `json:"allowWindowsPowerShell"`
}

// DefaultConfig is the Config of the defaults.
func DefaultConfig() Config { return Config{AllowWindowsPowerShell: true} }

// Limits are the Config's budgets, defaults applied.
func (c Config) Limits() shellexec.Limits {
	return shellexec.Limits{Timeout: c.Timeout, MaxTimeout: c.MaxTimeout, TailBytes: c.TailBytes, SpillBytes: c.SpillBytes, Grace: c.Grace}.WithDefaults()
}

// The PowerShell editions, as $PSVersionTable.PSEdition names them.
const (
	// Core is PowerShell 6 and later (pwsh).
	Core = "Core"
	// Desktop is Windows PowerShell 5.1 (powershell.exe).
	Desktop = "Desktop"
)

// Defaults are the entries PowerShell adds to the child's environment.
var Defaults = []string{"NO_COLOR=1", "PAGER=cat", "GIT_PAGER=cat"}

// Flags precede the command on every PowerShell argv.
var Flags = []string{"-NoLogo", "-NoProfile", "-NonInteractive"}

// Preamble pins the output encoding to UTF-8 before the command. It is wrapped
// in try/catch so that a shell in ConstrainedLanguage mode, where the assignment
// is refused, still runs the command (nothing relies on the language mode).
const Preamble = "try { [Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); $OutputEncoding = [Console]::OutputEncoding } catch {}"

// ScriptName is the file a first-statement command is written to.
const ScriptName = "command.ps1"

// Find resolves the PowerShell program on h: path when set; else, on Windows,
// %ProgramFiles%\PowerShell\7\pwsh.exe; else pwsh on the child's PATH; else, on
// Windows and when allow51 is set, Windows PowerShell 5.1 under %SystemRoot%.
// The edition is reported.
func Find(path string, allow51 bool, h shellexec.Host) (shellexec.Program, error) {
	if path != "" {
		p, err := h.Configured(path)
		if err != nil {
			return shellexec.Program{}, err
		}
		return shellexec.Program{Path: p, Edition: edition(p), Source: shellexec.SourceConfig}, nil
	}
	if h.Windows() {
		if pf := h.Get("ProgramFiles"); h.IsAbs(pf) {
			if p := h.Join(pf, "PowerShell", "7", "pwsh.exe"); h.Exists(p) {
				return shellexec.Program{Path: p, Edition: Core, Source: shellexec.SourceInstall}, nil
			}
		}
	}
	if p, ok := h.Search("pwsh", nil); ok {
		return shellexec.Program{Path: p, Edition: Core, Source: shellexec.SourcePath}, nil
	}
	if h.Windows() && allow51 {
		if root := h.Get("SystemRoot"); h.IsAbs(root) {
			if p := h.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe"); h.Exists(p) {
				return shellexec.Program{Path: p, Edition: Desktop, Source: shellexec.SourceWindowsPowerShell}, nil
			}
		}
	}
	return shellexec.Program{}, herr.New(shellexec.ReasonNotFound, "PowerShell is not found: set its path, install PowerShell 7, or allow Windows PowerShell")
}

// edition is the edition of a configured program, by its file name.
func edition(p string) string {
	base := strings.ToLower(p[strings.LastIndexAny(p, `\/`)+1:])
	if base == "powershell.exe" || base == "powershell" {
		return Desktop
	}
	return Core
}

// FirstStatement reports a command whose first statement is param, using or
// #requires, case-insensitively and after leading white space, a byte-order
// mark and comments (`# ...` to the end of the line, `<# ... #>`). Such a
// statement must open a script, so the command cannot follow the preamble in
// one command string.
func FirstStatement(command string) bool {
	s := strings.ToLower(strings.TrimPrefix(command, "\uFEFF"))
	for {
		s = strings.TrimLeftFunc(s, unicode.IsSpace)
		switch {
		case strings.HasPrefix(s, "#requires"):
			return true
		case strings.HasPrefix(s, "<#"):
			_, rest, ok := strings.Cut(s[2:], "#>")
			if !ok {
				return false
			}
			s = rest
			continue
		case strings.HasPrefix(s, "#"):
			_, rest, ok := strings.Cut(s, "\n")
			if !ok {
				return false
			}
			s = rest
			continue
		}
		break
	}
	for _, kw := range []string{"param", "using"} {
		if rest, ok := strings.CutPrefix(s, kw); ok && (rest == "" || !wordChar(rest[0])) {
			return true
		}
	}
	return false
}

func wordChar(c byte) bool {
	return c == '_' || c == '-' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
}

// quote is s as a single-quoted PowerShell string.
func quote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Shell is the PowerShell dialect.
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
	p, err := Find(cfg.Path, cfg.AllowWindowsPowerShell, h)
	if err != nil {
		return nil, err
	}
	return &Shell{program: p, limits: limits}, nil
}

// New resolves the program on h and returns the PowerShell executor.
func New(cfg Config, h shellexec.Host, o shellexec.Options) (*shellexec.Executor, error) {
	sh, err := NewShell(cfg, h)
	if err != nil {
		return nil, err
	}
	return shellexec.New(sh, o)
}

// Dialect is pwsh.
func (*Shell) Dialect() shellexec.Dialect { return shellexec.Pwsh }

// Program is the resolved PowerShell, with its edition.
func (s *Shell) Program() shellexec.Program { return s.program }

// Limits are the configured budgets.
func (s *Shell) Limits() shellexec.Limits { return s.limits }

// Argv is the flags and `-Command <preamble; command>`. A first-statement
// command is written, with a byte-order mark so Windows PowerShell reads it as
// UTF-8, to the private script file, which the command string invokes after the
// preamble with the script's exit code carried out; the execution policy is
// bypassed for that file only.
func (s *Shell) Argv(command string, scratch func() (string, error)) ([]string, error) {
	argv := append([]string{s.program.Path}, Flags...)
	if !FirstStatement(command) {
		return append(argv, "-Command", Preamble+"; "+command), nil
	}
	dir, err := scratch()
	if err != nil {
		return nil, err
	}
	script := filepath.Join(dir, ScriptName)
	if err := os.WriteFile(script, append([]byte("\uFEFF"), command...), 0o600); err != nil {
		return nil, herr.Wrap(shellexec.ReasonScript, err, "write the command's script file")
	}
	return append(argv, "-ExecutionPolicy", "Bypass", "-Command",
		Preamble+"; & "+quote(script)+"; if ($LASTEXITCODE) { exit $LASTEXITCODE }"), nil
}

// Defaults are NO_COLOR=1 PAGER=cat GIT_PAGER=cat.
func (*Shell) Defaults() []string { return slices.Clone(Defaults) }

// Unset is nothing.
func (*Shell) Unset() []string { return nil }
