package local

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// BwrapArgs is what the bubblewrap argv is built from: the bwrap program, the
// host, the start's canary and the policy.
type BwrapArgs struct {
	// Bwrap is the absolute path of the bwrap program, found once.
	Bwrap string
	// Host is the harness binary the stage runs, the harness home and the
	// user's runtime directory.
	Host Host
	// Canary is the start's canary, bound read-only so the stage can try it.
	Canary string
	Policy sandbox.Policy
}

// BwrapArgv is the outside confiner of one confined start under bubblewrap
// (DH-D-SANDBOX-4): `bwrap <args> --`, the prefix the stage runs under. In order:
//
//   - --die-with-parent --unshare-pid: the stage and its command die with the
//     host and see only their own processes. No --new-session, so the host's
//     process-tree kill still reaches them.
//   - --unshare-net when the policy denies the network: only loopback is left.
//   - --ro-bind / /, --dev /dev, --proc /proc: everything is read-only.
//   - --tmpfs /tmp under workspace-write: a private /tmp for programs that
//     ignore TMPDIR, gone with the start; under read-only the host /tmp stays
//     read-only (DH-ADR-SANDBOX-4; sandbox-ssh.md sec. 3.2, the bubblewrap row).
//     Every bind below comes after it, so a root, the canary or the binary
//     under /tmp is still the host's.
//   - --tmpfs <runtime dir>: the user's session bus and systemd sockets are out of
//     reach (delegated-execution).
//   - --bind R R for each write root, in policy order (the working directory,
//     workspaces, grants, the scratch directory).
//   - --ro-bind-try H H for the harness home, then --bind again each write root
//     inside it, so a root is never narrowed by the home.
//   - --ro-bind of the canary and of the harness binary: neither is writable even
//     when it lies below a root or the runtime directory hides it.
//
// The function is pure: the same arguments give the same argv on every lane
// (TC-04a, testdata/bwrap_argv.golden).
func BwrapArgv(a BwrapArgs) ([]string, error) {
	if !filepath.IsAbs(a.Bwrap) || !filepath.IsAbs(a.Host.Exe) || !filepath.IsAbs(a.Canary) {
		return nil, herr.New(sandbox.CodeLaunchFailed, "bwrap, the harness binary and the canary must be absolute paths")
	}
	if a.Policy.IsZero() || a.Policy.Mode() == sandbox.DangerFullAccess {
		return nil, herr.New(sandbox.CodeLaunchFailed, "only a confined policy runs under bubblewrap")
	}
	argv := []string{a.Bwrap, "--die-with-parent", "--unshare-pid"}
	if !a.Policy.Network() {
		argv = append(argv, "--unshare-net")
	}
	argv = append(argv, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc")
	if a.Policy.Mode() == sandbox.WorkspaceWrite {
		argv = append(argv, "--tmpfs", "/tmp")
	}
	if a.Host.RuntimeDir != "" {
		argv = append(argv, "--tmpfs", a.Host.RuntimeDir)
	}
	roots := rootPaths(a.Policy)
	for _, r := range roots {
		argv = append(argv, "--bind", r, r)
	}
	if a.Host.Home != "" {
		argv = append(argv, "--ro-bind-try", a.Host.Home, a.Host.Home)
		for _, r := range roots {
			if within(a.Host.Home, r) {
				argv = append(argv, "--bind", r, r)
			}
		}
	}
	argv = append(argv, "--ro-bind", a.Canary, a.Canary, "--ro-bind", a.Host.Exe, a.Host.Exe, "--")
	return argv, nil
}

// rootPaths are p's write-root paths in policy order, each once.
func rootPaths(p sandbox.Policy) []string {
	var out []string
	for _, r := range p.WriteRoots() {
		if !slices.Contains(out, r.Path) {
			out = append(out, r.Path)
		}
	}
	return out
}

// bwrapBackend is bubblewrap (DH-D-SANDBOX-4): the stage runs as bwrap's command.
type bwrapBackend struct {
	find  func() (string, error)
	once  sync.Once
	path  string
	err   error
	stage stageRunner
}

// Bwrap is the bubblewrap backend. bwrap is looked up on PATH once, by absolute
// path, the first time the backend is probed or prefixes a start; a missing
// bwrap fails every probe with "bwrap not found".
func Bwrap() Backend { return newBwrap(lookBwrap, runStage) }

func newBwrap(find func() (string, error), stage stageRunner) *bwrapBackend {
	return &bwrapBackend{find: find, stage: stage}
}

func lookBwrap() (string, error) {
	p, err := subprocess.Lookup("bwrap", subprocess.Env{"PATH=" + os.Getenv("PATH")})
	if err != nil {
		return "", herr.Wrap(sandbox.CodeUnavailable, err, "bwrap not found")
	}
	if !filepath.IsAbs(p) {
		return "", herr.Newf(sandbox.CodeUnavailable, "bwrap not found: PATH gives the relative %q", p)
	}
	return p, nil
}

func (b *bwrapBackend) bwrap() (string, error) {
	b.once.Do(func() { b.path, b.err = b.find() })
	return b.path, b.err
}

func (b *bwrapBackend) Name() sandbox.Backend { return sandbox.BackendBwrap }

func (b *bwrapBackend) Custom() bool { return false }

func (b *bwrapBackend) Probe(context.Context, string) (Probe, error) {
	return Probe{}, herr.New(sandbox.CodeUnavailable, "the bubblewrap probe needs the host (ProbeOn)")
}

// ProbeOn runs a probe stage under bwrap with a read-only, network-denied policy.
func (b *bwrapBackend) ProbeOn(ctx context.Context, h Host, canary string) (Probe, error) {
	if _, err := b.bwrap(); err != nil {
		return Probe{}, err
	}
	p, err := probePolicy()
	if err != nil {
		return Probe{}, err
	}
	prefix, err := b.Prefix(h, p, canary)
	if err != nil {
		return Probe{}, err
	}
	return b.stage(ctx, h, launch.Plan{Kind: launch.Confined, Backend: sandbox.BackendBwrap, Canary: canary, Home: h.Home}, p, prefix)
}

// Prefix is BwrapArgv for one start.
func (b *bwrapBackend) Prefix(h Host, p sandbox.Policy, canary string) ([]string, error) {
	path, err := b.bwrap()
	if err != nil {
		return nil, err
	}
	return BwrapArgv(BwrapArgs{Bwrap: path, Host: h, Canary: canary, Policy: p})
}
