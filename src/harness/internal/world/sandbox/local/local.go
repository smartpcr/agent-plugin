// Package local is the sandbox seam for world local (C37, DH-D-SANDBOX-3): it
// probes each backend of the platform chain with a canary at start-up, selects
// the first that passed, and is the one maker of a plan for a Policy (Prepare).
// On a stage's ready its decision computes the Confinement from the start-up
// probe and that ready (sandbox.Enforce: the weaker evidence wins), refuses a
// start whose policy requires full enforcement it cannot give, and only then lets
// the stage receive go.
//
// The backends themselves (bubblewrap, Landlock) are DH-D-SANDBOX-4 (impl.md
// 13.4). Windows has no backend in M2 (M2-C-8): a confined policy is refused with
// sandbox/unavailable and only danger-full-access runs, reported Unconfined.
package local

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox/launch"
)

// The defaults of Config (sandbox-ssh.md sec. 6.2, local.Config).
const (
	DefaultProbeTimeout  = 5 * time.Second
	DefaultLaunchTimeout = 5 * time.Second
)

// Probe is what one backend's start-up probe showed: the canary result of a
// stage it confined, and the effects that stage governed and could reach.
type Probe struct {
	Canary   launch.Canary
	Governed []sandbox.Effect
	Exposed  []sandbox.Effect
}

// Backend is one entry of the chain the service probes and selects from.
type Backend interface {
	// Name is the backend a confined plan under it names.
	Name() sandbox.Backend
	// Custom reports an operator's runner, which is never Full.
	Custom() bool
	// Probe confines a probe stage and has it try to open canary, an existing
	// file, for writing. An error is a failed probe.
	Probe(ctx context.Context, canary string) (Probe, error)
}

// Config is local.Config. The zero Config is the defaults.
type Config struct {
	// ProbeTimeout bounds each backend's probe (DefaultProbeTimeout when zero).
	ProbeTimeout time.Duration `json:"probeTimeout,omitempty"`
	// LaunchTimeout bounds each start from Confirm's call until the command has
	// started (DefaultLaunchTimeout when zero).
	LaunchTimeout time.Duration `json:"launchTimeout,omitempty"`
	// Backends, when set, reduces the chain to these names in chain order; it may
	// only remove entries.
	Backends []sandbox.Backend `json:"backends,omitempty"`
	// CanaryDir is the absolute directory the canaries are created in; empty is
	// a fresh directory under the system temp directory, removed by Close.
	CanaryDir string `json:"canaryDir,omitempty"`
}

// Validate reports a malformed Config.
func (c Config) Validate() error {
	if c.ProbeTimeout < 0 || c.LaunchTimeout < 0 {
		return herr.New(sandbox.CodeUnavailable, "sandbox time limits must be positive")
	}
	for _, b := range c.Backends {
		if !b.Valid() || b == sandbox.BackendNone {
			return herr.Newf(sandbox.CodeUnavailable, "backend %q is not a confining backend", b)
		}
	}
	if c.CanaryDir != "" && (!filepath.IsAbs(c.CanaryDir) || filepath.Clean(c.CanaryDir) != c.CanaryDir) {
		return herr.Newf(sandbox.CodeUnavailable, "canary directory %q is not an absolute, clean path", c.CanaryDir)
	}
	return nil
}

// Service is the seam for world local.
type Service struct {
	cfg       Config
	chain     []Backend
	canaryDir string
	ownDir    bool

	mu       sync.Mutex
	statuses []sandbox.Status
	probes   int
}

var _ sandbox.Service = (*Service)(nil)

// New probes chain, reduced by cfg.Backends, and returns the service. A chain in
// which no backend passes still gives a service: it reports every backend
// unavailable and refuses confined starts (sandbox-ssh.md sec. 6.3, "A load
// fails"). The production chain is Platform().
func New(ctx context.Context, cfg Config, chain []Backend) (*Service, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = DefaultProbeTimeout
	}
	if cfg.LaunchTimeout == 0 {
		cfg.LaunchTimeout = DefaultLaunchTimeout
	}
	kept, err := reduce(chain, cfg.Backends)
	if err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg, chain: kept, canaryDir: cfg.CanaryDir}
	if s.canaryDir == "" {
		dir, err := os.MkdirTemp("", "dsh-canary-")
		if err != nil {
			return nil, herr.Wrap(sandbox.CodeUnavailable, err, "create the canary directory")
		}
		s.canaryDir, s.ownDir = dir, true
	}
	s.Reprobe(ctx)
	return s, nil
}

// reduce keeps the entries of chain named in names, in chain order; nil names
// keep the whole chain. A name the chain lacks is refused: Backends may only
// remove entries.
func reduce(chain []Backend, names []sandbox.Backend) ([]Backend, error) {
	for i, b := range chain {
		if b == nil || !b.Name().Valid() || b.Name() == sandbox.BackendNone {
			return nil, herr.Newf(sandbox.CodeUnavailable, "chain entry %d is not a confining backend", i)
		}
	}
	if names == nil {
		return slices.Clone(chain), nil
	}
	for _, n := range names {
		if !slices.ContainsFunc(chain, func(b Backend) bool { return b.Name() == n }) {
			return nil, herr.Newf(sandbox.CodeUnavailable, "backend %s is not in this platform's chain; Backends may only remove entries", n)
		}
	}
	return slices.DeleteFunc(slices.Clone(chain), func(b Backend) bool { return !slices.Contains(names, b.Name()) }), nil
}

// Close removes the canary directory New created.
func (s *Service) Close() error {
	if !s.ownDir {
		return nil
	}
	return os.RemoveAll(s.canaryDir)
}

// Describe implements sandbox.Service.
func (s *Service) Describe(context.Context) []sandbox.Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneStatuses(s.statuses)
}

// Reprobe implements sandbox.Service: every backend is probed again, in chain
// order, each bounded by ProbeTimeout.
func (s *Service) Reprobe(ctx context.Context) []sandbox.Status {
	out := make([]sandbox.Status, 0, len(s.chain))
	for _, b := range s.chain {
		out = append(out, s.probe(ctx, b))
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses = out
	s.probes++
	return cloneStatuses(out)
}

// Probes is the number of times the chain was probed: once by New, once per
// Reprobe, and once after each failed confinement.
func (s *Service) Probes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.probes
}

func cloneStatuses(in []sandbox.Status) []sandbox.Status {
	out := make([]sandbox.Status, len(in))
	for i, st := range in {
		out[i] = st.Clone()
	}
	return out
}

// probe runs b's probe against a fresh canary and turns the result into a
// Status: available only with no error, within the time limit, and the canary
// refused.
func (s *Service) probe(ctx context.Context, b Backend) sandbox.Status {
	st := sandbox.Status{Backend: b.Name(), Custom: b.Custom()}
	canary, release, err := s.newCanary("probe-" + string(b.Name()))
	if err != nil {
		st.Reason = err.Error()
		return st
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, s.cfg.ProbeTimeout)
	defer cancel()
	p, err := b.Probe(ctx, canary)
	st.Canary = string(p.Canary)
	switch {
	case err != nil:
		st.Reason = err.Error()
	case ctx.Err() != nil:
		st.Reason = "the probe passed its time limit"
	case p.Canary != launch.CanaryRefused:
		st.Reason = fmt.Sprintf("the probe stage reports canary %q; confinement failed", p.Canary)
	default:
		st.Available = true
		st.Governed = slices.Clone(p.Governed)
		st.Exposed = slices.Clone(p.Exposed)
	}
	return st
}

// newCanary creates an empty file in the canary directory and returns its path
// and the function that removes it.
func (s *Service) newCanary(prefix string) (string, func(), error) {
	f, err := os.CreateTemp(s.canaryDir, prefix+"-*")
	if err != nil {
		return "", nil, herr.Wrap(sandbox.CodeUnavailable, err, "create a canary")
	}
	path := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(path)
		return "", nil, herr.Wrap(sandbox.CodeUnavailable, err, "create a canary")
	}
	return path, func() { _ = os.Remove(path) }, nil
}

// selected is the first backend whose last probe passed, and its status.
func (s *Service) selected() (sandbox.Status, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.statuses {
		if st.Available {
			return st.Clone(), true
		}
	}
	return sandbox.Status{}, false
}

// Prepare implements sandbox.Service. The plan it returns executes one start of
// argv; its Release removes the start's canary.
func (s *Service) Prepare(ctx context.Context, p sandbox.Policy, argv []string) (sandbox.Launch, error) {
	plan, d, err := s.prepare(ctx, p, argv)
	if err != nil {
		return sandbox.Launch{}, err
	}
	l, err := launch.NewLaunch(p, plan, launch.Hooks{Confirm: d.confirm, Failed: d.failed, Release: d.release, Limit: s.cfg.LaunchTimeout})
	if err != nil {
		_ = d.release(ctx)
		return sandbox.Launch{}, err
	}
	return l, nil
}

// prepare fixes the plan's kind, backend and canary for p (architecture.md sec.
// 4.2, "Launch plan Kind") and returns the decision that confirms its start.
func (s *Service) prepare(ctx context.Context, p sandbox.Policy, argv []string) (launch.Plan, *decision, error) {
	if err := p.Validate(); err != nil {
		return launch.Plan{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return launch.Plan{}, nil, herr.Wrap(sandbox.CodeUnavailable, err, "prepare")
	}
	if len(argv) == 0 || !filepath.IsAbs(argv[0]) {
		return launch.Plan{}, nil, herr.New(sandbox.CodeLaunchFailed, "the command's program must be an absolute path")
	}
	d := &decision{svc: s, policy: p, argv: slices.Clone(argv)}
	if p.Mode() == sandbox.DangerFullAccess {
		return launch.Plan{Kind: launch.Unconfined, Backend: sandbox.BackendNone}, d, nil
	}
	st, ok := s.selected()
	if !ok {
		return launch.Plan{}, nil, herr.Newf(sandbox.CodeUnavailable,
			"no sandbox backend passed the start-up probe on %s; a %s command cannot be confined and was not started", runtime.GOOS, p.Mode())
	}
	for _, r := range p.WriteRoots() {
		if within(r.Path, s.canaryDir) {
			return launch.Plan{}, nil, herr.Newf(sandbox.CodeUnavailable,
				"the canary directory %s lies below write root %s, so a refused canary would prove nothing", s.canaryDir, r.Path)
		}
		if r.Source == sandbox.SourceScratch && d.scratch == "" {
			d.scratch = r.Path
		}
	}
	canary, release, err := s.newCanary("start")
	if err != nil {
		return launch.Plan{}, nil, err
	}
	d.probe, d.releaseCanary = st, release
	return launch.Plan{Kind: launch.Confined, Backend: st.Backend, Canary: canary}, d, nil
}

// decision is the host's decision on one prepared start.
type decision struct {
	svc    *Service
	policy sandbox.Policy
	argv   []string
	// probe is the start-up probe of a confined plan's backend, taken when the
	// plan was prepared.
	probe   sandbox.Status
	scratch string

	once          sync.Once
	releaseCanary func()
	reprobed      sync.Once
}

// reprobe re-probes the chain once per start, after a failed confined start
// (sandbox-ssh.md sec. 6.1: "the command never ran; the seam re-probes once").
// The command itself is never retried.
func (d *decision) reprobe(ctx context.Context, p launch.Plan) {
	if p.Kind != launch.Confined {
		return
	}
	d.reprobed.Do(func() { d.svc.Reprobe(context.WithoutCancel(ctx)) })
}

// failed is the stage's end before ready: it died, sent a malformed frame or
// ran out of the launch limit, so the command never ran and the backend's
// start-up probe may be stale.
func (d *decision) failed(ctx context.Context, p launch.Plan, _ error) {
	d.reprobe(ctx, p)
}

// confirm is the four-step host decision (architecture.md sec. 4.2): (1) a
// confined plan whose stage did not report the canary refused, or named another
// backend, is a failed confinement: sandbox/launch-failed and one re-probe; (2)
// an unconfined plan whose stage reports anything but backend none and canary
// not-tried disagrees with its plan: sandbox/launch-failed; (3) a policy that
// requires full enforcement and a computed state other than Full:
// sandbox/enforcement-insufficient, for either kind; (4) otherwise the
// Confinement the command runs under, after which the stage receives go.
func (d *decision) confirm(ctx context.Context, p launch.Plan, r launch.Ready) (sandbox.Confinement, error) {
	if !slices.Equal(p.Argv, d.argv) {
		return sandbox.Confinement{}, herr.New(sandbox.CodeLaunchFailed, "the started command is not the one the plan was prepared for")
	}
	if err := r.Agrees(p); err != nil {
		d.reprobe(ctx, p)
		return sandbox.Confinement{}, err
	}
	e := sandbox.Evidence{Backend: p.Backend}
	if p.Kind == launch.Confined {
		e = sandbox.Evidence{
			Backend: p.Backend, Custom: d.probe.Custom,
			Probe: d.probe.Governed, Start: r.Governed, Exposed: d.probe.Exposed,
			Scratch: d.scratch,
		}
	}
	c := sandbox.Enforce(d.policy, e)
	if !c.Valid() {
		return sandbox.Confinement{}, herr.Newf(sandbox.CodeLaunchFailed, "a %s policy cannot run under a %s", d.policy.Mode(), p)
	}
	if d.policy.Require() == sandbox.RequireFull && c.State != sandbox.StateFull {
		return sandbox.Confinement{}, herr.Newf(sandbox.CodeEnforcementInsufficient,
			"the policy requires full enforcement and the start is %s (backend %s, missing %s)", c.State, c.Backend, effects(c.Missing))
	}
	return c, nil
}

// release removes the start's canary, once.
func (d *decision) release(context.Context) error {
	d.once.Do(func() {
		if d.releaseCanary != nil {
			d.releaseCanary()
		}
	})
	return nil
}

func effects(es []sandbox.Effect) string {
	if len(es) == 0 {
		return "nothing"
	}
	s := make([]string, len(es))
	for i, e := range es {
		s[i] = string(e)
	}
	return strings.Join(s, ", ")
}

// within reports whether path is root or below it, case-insensitively on Windows.
func within(root, path string) bool {
	if runtime.GOOS == "windows" {
		root, path = strings.ToLower(root), strings.ToLower(path)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
