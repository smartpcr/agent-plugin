package clientroute

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
	"github.com/smartpcr/agent-plugin/src/sdk/jsonrpc"
)

// Version is a client's major.minor.patch.
type Version struct{ Major, Minor, Patch int }

func (v Version) String() string { return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch) }

// Less reports whether v is older than o.
func (v Version) Less(o Version) bool {
	if v.Major != o.Major {
		return v.Major < o.Major
	}
	if v.Minor != o.Minor {
		return v.Minor < o.Minor
	}
	return v.Patch < o.Patch
}

// Locate finds the client: configured when it is set (it must be absolute),
// else name on the PATH of the child's environment env, never the host's. A
// Windows batch file is reported in Program.Batch; the adapter starts it with
// subprocess.AllowChecked. A program that cannot be found is
// llm/client-upgrade-required, so the user is told to install the client.
func Locate(client, configured, name string, env subprocess.Env) (subprocess.Program, error) {
	if configured != "" {
		if !filepath.IsAbs(configured) {
			return subprocess.Program{}, Fail(vocab.CodeInvalidRequest, "%s: the configured program is not an absolute path", client)
		}
		name = configured
	}
	prog, err := subprocess.LookupProgram(name, env)
	if err != nil {
		return subprocess.Program{}, Fail(vocab.CodeClientUpgradeRequired, "%s: the client program %s was not found; install it or configure its path", client, filepath.Base(name))
	}
	if configured != "" {
		if fi, err := os.Stat(prog.Path); err != nil || fi.IsDir() {
			return subprocess.Program{}, Fail(vocab.CodeClientUpgradeRequired, "%s: the configured program %s does not exist; install the client", client, filepath.Base(name))
		}
	}
	return prog, nil
}

// The probe's defaults (claude-code-cli.md sec. 5.5).
const (
	DefaultProbeTimeout = 10 * time.Second
	DefaultProbeBytes   = 4 << 10
)

// ProbeSpec says how to read a client's version.
type ProbeSpec struct {
	// Client names the client in failure messages ("claude-code").
	Client  string
	Program subprocess.Program
	// Args follow the program ("--version").
	Args []string
	// Env is the run's environment, so the probed binary is the one that runs.
	Env subprocess.Env
	// Dir is the working directory; empty is the host's.
	Dir string
	// Pattern must match the first line exactly, with the major, minor and patch
	// numbers as its first three groups. Later lines are not read.
	Pattern *regexp.Regexp
	// Floor is the oldest version accepted.
	Floor Version
	// Timeout bounds the probe (DefaultProbeTimeout); MaxBytes bounds the line
	// (DefaultProbeBytes).
	Timeout  time.Duration
	MaxBytes int
	// Launch is the client's launch plan (HelperLaunch); without one nothing
	// is started (DH-D-EXEC-4).
	Launch sandbox.Launch
}

// Probe runs the client with s.Args and returns its version. A start failure, a
// deadline, a non-zero exit, a first line Pattern does not match and a version
// below Floor are each llm/client-upgrade-required, before any request text is
// written; a cancelled ctx is llm/aborted. The line is bounded and never logged.
func Probe(ctx context.Context, s ProbeSpec) (Version, error) {
	if s.Pattern == nil || s.Pattern.NumSubexp() < 3 {
		return Version{}, errors.New("clientroute: the probe has no three-group version pattern")
	}
	if !s.Launch.Valid() {
		return Version{}, errors.New("clientroute: the probe has no launch plan")
	}
	timeout, bound := s.Timeout, s.MaxBytes
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	if bound <= 0 {
		bound = DefaultProbeBytes
	}
	pctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	refuse := func(format string, args ...any) (Version, error) {
		if ctx.Err() != nil {
			return Version{}, Fail(vocab.CodeAborted, "%s: the version probe was cancelled", s.Client)
		}
		return Version{}, Fail(vocab.CodeClientUpgradeRequired, "%s: "+format, append([]any{s.Client}, args...)...)
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return Version{}, fmt.Errorf("clientroute: probe pipe: %w", err)
	}
	defer pr.Close()
	batch := subprocess.Refuse
	if s.Program.Batch {
		batch = subprocess.AllowChecked
	}
	p, err := subprocess.Start(pctx, subprocess.Spec{
		Argv: append([]string{s.Program.Path}, s.Args...), Dir: s.Dir, Env: s.Env, Stdout: pw, Batch: batch, Launch: s.Launch,
	})
	_ = pw.Close()
	if err != nil {
		return refuse("the client could not be started to read its version")
	}
	defer func() { _ = p.StopTree(context.WithoutCancel(ctx), 0) }()
	rd := jsonrpc.NewReader(pr, bound)
	line, rerr := rd.ReadLine()
	text := string(line)
	if rerr != nil && !errors.Is(rerr, io.EOF) {
		text = ""
	}
	// Later lines (an update hint) are read and dropped, so a chatty client
	// never blocks on a full pipe; the deferred Close ends this reader.
	go func() { _, _ = io.Copy(io.Discard, io.LimitReader(pr, 1<<20)) }()
	waitErr := wait(pctx, p)
	switch {
	case pctx.Err() != nil:
		return refuse("the version probe did not finish within %s", timeout)
	case waitErr != nil || p.ExitCode() != 0:
		return refuse("the version probe exited with status %d", p.ExitCode())
	}
	m := s.Pattern.FindStringSubmatch(text)
	if m == nil || m[0] != text {
		return refuse("the client's version line is not recognised; at least %s is required", s.Floor)
	}
	var v Version
	for i, dst := range []*int{&v.Major, &v.Minor, &v.Patch} {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			return refuse("the client's version line is not recognised; at least %s is required", s.Floor)
		}
		*dst = n
	}
	if v.Less(s.Floor) {
		return refuse("version %s is installed and at least %s is required; upgrade the client", v, s.Floor)
	}
	return v, nil
}

// wait waits for the direct child to exit, or ctx to end.
func wait(ctx context.Context, p *subprocess.Process) error {
	select {
	case <-p.Exited():
		return p.Wait()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
