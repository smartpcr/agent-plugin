package launch

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"

	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Mode is the hidden first word that selects the stage (reserved by cli/tree.go).
const Mode = "sandbox-launch"

// Kind is the plan kind the seam fixes at Prepare, before any process exists
// (architecture.md sec. 4.2), and passes to the stage.
type Kind string

// The two kinds of a plan that starts anything.
const (
	// Confined: the stage confines itself and must try the canary.
	Confined Kind = "confined"
	// Unconfined: danger-full-access, backend none; no confinement, no canary.
	Unconfined Kind = "unconfined"
)

// Plan is what the stage is told on its command line: the plan kind, the backend,
// the canary (confined only), the child's numbers of the two channel ends, and the
// command it becomes after go.
type Plan struct {
	Kind    Kind
	Backend sandbox.Backend
	// Canary is the absolute path of the file a confined stage must fail to open
	// for writing; empty for an unconfined plan.
	Canary string
	// Write are the policy's write roots (absolute, clean), in policy order: the
	// only directories a stage that confines itself (Landlock) grants writes
	// beneath, and what a stage checks the harness home against. Confined only.
	Write []string
	// Home is the harness home a confined stage keeps read-only and reports as
	// governed when it can show it (harness-home); empty when the host did not
	// name one. Confined only.
	Home string
	// Control is the stage's end the host sends go on; Status the end the stage
	// sends ready (and exec-failed) on. Both are child descriptor numbers
	// (subprocess.ChildFD).
	Control, Status uintptr
	// Argv is the command; Argv[0] is an absolute path.
	Argv []string
	// Keep are the caller's own channels (subprocess.Spec.Extra) as child
	// numbers: the stage hands them on to the command (subprocess.Become), which
	// sees them under the same numbers. Nil when there are none.
	Keep []uintptr
}

// Validate refuses a plan whose kind, backend and canary disagree, or that has no
// channel or no command; the error is sandbox/launch-failed.
func (p Plan) Validate() error {
	switch p.Kind {
	case Confined:
		if !p.Backend.Valid() || p.Backend == sandbox.BackendNone {
			return failed("a confined plan needs a confining backend, not %q", p.Backend)
		}
		if !absClean(p.Canary) {
			return failed("a confined plan needs an absolute, clean canary path, not %q", p.Canary)
		}
		for i, w := range p.Write {
			if !absClean(w) || slices.Contains(p.Write[:i], w) {
				return failed("write root %q is not a distinct, absolute, clean path", w)
			}
		}
		if p.Home != "" && !absClean(p.Home) {
			return failed("the harness home %q is not an absolute, clean path", p.Home)
		}
	case Unconfined:
		if p.Backend != sandbox.BackendNone {
			return failed("an unconfined plan runs under backend none, not %q", p.Backend)
		}
		if p.Canary != "" {
			return failed("an unconfined plan has no canary")
		}
		if len(p.Write) > 0 || p.Home != "" {
			return failed("an unconfined plan has no write roots and no harness home to keep")
		}
	default:
		return failed("plan kind %q is neither confined nor unconfined", p.Kind)
	}
	if !channelEnd(p.Control) || !channelEnd(p.Status) || p.Control == p.Status {
		return failed("the plan needs two distinct channel descriptors past stdin/stdout/stderr, got %d and %d", p.Control, p.Status)
	}
	for i, k := range p.Keep {
		if !channelEnd(k) || k == p.Control || k == p.Status || slices.Contains(p.Keep[:i], k) {
			return failed("kept channel %d is not a distinct descriptor past stdin/stdout/stderr and the stage's own", k)
		}
	}
	if len(p.Argv) == 0 || !filepath.IsAbs(p.Argv[0]) {
		return failed("the plan needs a command whose program is an absolute path")
	}
	return nil
}

func absClean(path string) bool {
	return path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}

// channelEnd reports whether n can be a channel end. On POSIX the ends are the
// child's extra descriptors (3 and up, subprocess.ChildFD), never stdin, stdout or
// stderr, which the command inherits; on Windows they are handle values, and only
// the null handle is refused.
func channelEnd(n uintptr) bool {
	if runtime.GOOS == "windows" {
		return n != 0
	}
	return n >= 3
}

// Args is the stage's argument list after Mode: the flags, "--" and the command.
func (p Plan) Args() []string {
	args := []string{"--kind", string(p.Kind), "--backend", string(p.Backend)}
	if p.Canary != "" {
		args = append(args, "--canary", p.Canary)
	}
	for _, w := range p.Write {
		args = append(args, "--write", w)
	}
	if p.Home != "" {
		args = append(args, "--home", p.Home)
	}
	args = append(args,
		"--control-fd", strconv.FormatUint(uint64(p.Control), 10),
		"--status-fd", strconv.FormatUint(uint64(p.Status), 10))
	for _, k := range p.Keep {
		args = append(args, "--keep-fd", strconv.FormatUint(uint64(k), 10))
	}
	args = append(args, "--")
	return append(args, p.Argv...)
}

// keepFlags collects the repeated --keep-fd flag.
type keepFlags []uintptr

func (k *keepFlags) String() string { return fmt.Sprint([]uintptr(*k)) }

func (k *keepFlags) Set(s string) error {
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || uint64(uintptr(n)) != n {
		return fmt.Errorf("kept channel %q is not a descriptor", s)
	}
	*k = append(*k, uintptr(n))
	return nil
}

// pathFlags collects a repeated path flag.
type pathFlags []string

func (f *pathFlags) String() string { return fmt.Sprint([]string(*f)) }

func (f *pathFlags) Set(s string) error {
	*f = append(*f, s)
	return nil
}

// Parse reads the arguments after Mode back into a Plan and validates it.
func Parse(args []string) (Plan, error) {
	fs := flag.NewFlagSet(Mode, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	kind := fs.String("kind", "", "confined or unconfined")
	backend := fs.String("backend", "", "the backend the stage runs under")
	canary := fs.String("canary", "", "the canary path (confined only)")
	home := fs.String("home", "", "the harness home kept read-only (confined only)")
	var write pathFlags
	fs.Var(&write, "write", "a write root (confined only, repeated)")
	control := fs.Uint64("control-fd", 0, "the descriptor the host sends go on")
	status := fs.Uint64("status-fd", 0, "the descriptor the stage sends ready on")
	var keep keepFlags
	fs.Var(&keep, "keep-fd", "a caller's channel the command inherits (repeated)")
	if err := fs.Parse(args); err != nil {
		return Plan{}, failed("%v", err)
	}
	if len(args)-fs.NArg() == 0 || args[len(args)-fs.NArg()-1] != "--" {
		return Plan{}, failed("the command must follow --")
	}
	p := Plan{
		Kind: Kind(*kind), Backend: sandbox.Backend(*backend), Canary: *canary,
		Write: []string(write), Home: *home,
		Control: uintptr(*control), Status: uintptr(*status), Argv: fs.Args(), Keep: []uintptr(keep),
	}
	if uint64(p.Control) != *control || uint64(p.Status) != *status {
		return Plan{}, failed("channel descriptor out of range")
	}
	if err := p.Validate(); err != nil {
		return Plan{}, err
	}
	return p, nil
}

// String names the plan for a diagnostic.
func (p Plan) String() string {
	return fmt.Sprintf("%s plan, backend %s", p.Kind, p.Backend)
}

// Agrees is the first two steps of the host's decision on a ready (architecture.md
// sec. 4.2): a confined plan's stage must report its plan's backend and canary
// refused (written or not-tried is a failed confinement); an unconfined plan's
// stage must report backend none and canary not-tried. Any disagreement is
// sandbox/launch-failed and the host closes the pair. Enforcement and require:
// full are the seam's later steps (DH-D-SANDBOX-3).
func (r Ready) Agrees(p Plan) error {
	if r.Backend != p.Backend {
		return failed("the stage reports backend %s, the plan is %s", r.Backend, p)
	}
	switch p.Kind {
	case Confined:
		if r.Canary != CanaryRefused {
			return failed("a confined stage reports canary %s; confinement failed", r.Canary)
		}
	case Unconfined:
		if r.Canary != CanaryNotTried {
			return failed("an unconfined stage reports canary %s, want %s", r.Canary, CanaryNotTried)
		}
	default:
		return failed("plan kind %q is neither confined nor unconfined", p.Kind)
	}
	return nil
}
