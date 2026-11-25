package launch

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
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
	// Control is the stage's end the host sends go on; Status the end the stage
	// sends ready (and exec-failed) on. Both are child descriptor numbers
	// (subprocess.ChildFD).
	Control, Status uintptr
	// Argv is the command; Argv[0] is an absolute path.
	Argv []string
}

// Validate refuses a plan whose kind, backend and canary disagree, or that has no
// channel or no command; the error is sandbox/launch-failed.
func (p Plan) Validate() error {
	switch p.Kind {
	case Confined:
		if !p.Backend.Valid() || p.Backend == sandbox.BackendNone {
			return failed("a confined plan needs a confining backend, not %q", p.Backend)
		}
		if p.Canary == "" || !filepath.IsAbs(p.Canary) || filepath.Clean(p.Canary) != p.Canary {
			return failed("a confined plan needs an absolute, clean canary path, not %q", p.Canary)
		}
	case Unconfined:
		if p.Backend != sandbox.BackendNone {
			return failed("an unconfined plan runs under backend none, not %q", p.Backend)
		}
		if p.Canary != "" {
			return failed("an unconfined plan has no canary")
		}
	default:
		return failed("plan kind %q is neither confined nor unconfined", p.Kind)
	}
	if !channelEnd(p.Control) || !channelEnd(p.Status) || p.Control == p.Status {
		return failed("the plan needs two distinct channel descriptors past stdin/stdout/stderr, got %d and %d", p.Control, p.Status)
	}
	if len(p.Argv) == 0 || !filepath.IsAbs(p.Argv[0]) {
		return failed("the plan needs a command whose program is an absolute path")
	}
	return nil
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
	args = append(args,
		"--control-fd", strconv.FormatUint(uint64(p.Control), 10),
		"--status-fd", strconv.FormatUint(uint64(p.Status), 10),
		"--")
	return append(args, p.Argv...)
}

// Parse reads the arguments after Mode back into a Plan and validates it.
func Parse(args []string) (Plan, error) {
	fs := flag.NewFlagSet(Mode, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	kind := fs.String("kind", "", "confined or unconfined")
	backend := fs.String("backend", "", "the backend the stage runs under")
	canary := fs.String("canary", "", "the canary path (confined only)")
	control := fs.Uint64("control-fd", 0, "the descriptor the host sends go on")
	status := fs.Uint64("status-fd", 0, "the descriptor the stage sends ready on")
	if err := fs.Parse(args); err != nil {
		return Plan{}, failed("%v", err)
	}
	if len(args)-fs.NArg() == 0 || args[len(args)-fs.NArg()-1] != "--" {
		return Plan{}, failed("the command must follow --")
	}
	p := Plan{
		Kind: Kind(*kind), Backend: sandbox.Backend(*backend), Canary: *canary,
		Control: uintptr(*control), Status: uintptr(*status), Argv: fs.Args(),
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
