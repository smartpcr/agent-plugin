// Package claudecode is the claude-code route (DH-D-LLM-12, DH-ADR-LLM-10): a
// model request is sent through the user's installed, signed-in Claude Code
// client, started in print mode as a child process, one process per attempt
// (docs/harness/libs/claude-code-cli.md, DH-ADR-CLAUDECODECLI-1). The harness
// never reads a token: every credential and provider override is removed from
// the child's environment, so the client's own sign-in is what is used.
//
// The request is rendered to the client's standard input with the harness
// system prompt replacing the client's (--system-prompt-file); built-in tools
// are off, the harness tools are listed by the listing-only MCP server, every
// call to them is denied by the client's own rules, and the client stops after
// one turn. A tool call comes back unexecuted as ToolCallDelta and
// Finish{ToolCalls}, and the driver runs it through the guarded pipeline
// (M1-D-PIPE-6 A). The stream_event lines are decoded by the Messages decoder
// of wire/anthropic; the result line settles the finish.
//
// The route is registered by whoever wants it (Route); it is never a default.
package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"iter"
	"os"
	"slices"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/provider/clientroute/listing"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/subprocess"
)

// RouteID is the route this package registers.
const RouteID vocab.RouteID = "claude-code"

// Client names the client in the invocation record and in failures.
const Client = "claude-code"

// Program is the client's bare name, looked up on the child's PATH.
const Program = "claude"

// Efforts are the reasoning-effort levels --effort takes.
var Efforts = []string{"low", "medium", "high", "xhigh", "max"}

// Models is the default catalogue: the client's model aliases. The first is the
// default model.
var Models = []llm.Model{{Name: "sonnet", Efforts: Efforts}, {Name: "opus", Efforts: Efforts}, {Name: "haiku"}}

// SignInKeySources are the apiKeySource values of a run that uses the client's
// own sign-in. Inference: "none" is what a signed-in run reports
// (claude-code-cli.md sec. 5.4); DH-E-CLAUDECODECLI-1 (plan 16.5.9) records the
// real value and pins it.
var SignInKeySources = []string{"none"}

// unknownOption is the client's own marker for an argument it refuses at start.
const unknownOption = "unknown option"

// Config configures the route.
type Config struct {
	// Program is the client's absolute path; empty looks up "claude" on the
	// child's PATH.
	Program string
	// Models overrides Models.
	Models []llm.Model
	// Env are extra NAME=value entries for the child (a configuration home for
	// the signed-out check, a test's stand-in selector). An entry naming a
	// variable the route removes is refused by Route.
	Env []string
	// Host is the host environment the child's is derived from; nil is
	// os.Environ.
	Host func() []string
	// Secrets is the secret store (*secret.Store): every name it reports is
	// removed from the child's environment.
	Secrets subprocess.SecretNames
	// TempDir is the parent of the attempt directories; empty is os.TempDir().
	TempDir string
	// Listing is the executable that serves the listing-only MCP server in its
	// hidden mode; empty is os.Executable() (dsh, or a test binary that serves
	// it from its TestMain).
	Listing string
	// KeySources overrides SignInKeySources.
	KeySources []string
	// MaxConcurrent caps the route's concurrent attempts; zero is uncapped.
	MaxConcurrent int
	// Retry is the route's retry policy; zero is llm.DefaultRetry. A signed-out
	// or rejected sign-in is Permanent, so it is never retried.
	Retry llm.RetryPolicy
	// ProbeTimeout bounds the version probe (clientroute.DefaultProbeTimeout);
	// Grace is the cancelled tree's grace (clientroute.DefaultGrace).
	ProbeTimeout time.Duration
	Grace        time.Duration
	// Tap, when set, sees each line of the client's standard output as it is read,
	// before the route places it: a diagnostic for the operator's manual check
	// (plan 16.5.6), never set by the harness. It must not keep the slice; the
	// route itself never logs a line.
	Tap func(line []byte)
	// Sandbox is the resolver whose ForHelper policy plans the client's launch:
	// the explicit unconfined helper plan of kind RouteID (clientroute.HelperLaunch,
	// DH-D-EXEC-4). It is required, and must let RouteID run unconfined.
	Sandbox sandbox.Resolver
}

// Route is the claude-code route over cfg.
func Route(cfg Config) (llm.Route, error) {
	if cfg.Sandbox == nil {
		return llm.Route{}, errors.New("claudecode: the route needs a sandbox resolver to plan the client's launch")
	}
	spec := Env()
	spec.Extra, spec.Secrets = cfg.Env, cfg.Secrets
	if err := spec.Check(); err != nil {
		return llm.Route{}, err
	}
	if cfg.Host == nil {
		cfg.Host = os.Environ
	}
	if cfg.Listing == "" {
		exe, err := os.Executable()
		if err != nil {
			return llm.Route{}, errors.New("claudecode: the harness executable cannot be found for the listing server")
		}
		cfg.Listing = exe
	}
	if len(cfg.KeySources) == 0 {
		cfg.KeySources = SignInKeySources
	}
	if cfg.Retry == (llm.RetryPolicy{}) {
		cfg.Retry = llm.DefaultRetry()
	}
	models := cfg.Models
	if len(models) == 0 {
		models = Models
	}
	return llm.Route{
		ID:            RouteID,
		Models:        slices.Clone(models),
		MaxConcurrent: cfg.MaxConcurrent,
		Retry:         cfg.Retry,
		Adapter:       &adapter{cfg: cfg, env: spec},
	}, nil
}

type adapter struct {
	cfg Config
	env clientroute.EnvSpec
}

// Prepare checks the controls, builds the child environment, finds the client
// and probes its version, once per prepared call and before any request text
// is written: a missing or too-old client is llm/client-upgrade-required.
// Only the model and the reasoning effort can be set; any other control is
// llm/unsupported-option.
func (a *adapter) Prepare(ctx context.Context, route vocab.RouteID, model string, cfg vocab.CallConfig) (llm.Call, error) {
	if route != RouteID {
		return nil, clientroute.Fail(vocab.CodeNoRoute, "claudecode: asked for route %q", route)
	}
	c := cfg.Controls
	switch {
	case c.Temperature != nil, c.TopP != nil, len(c.Stop) > 0, c.MaxOutputTokens != 0:
		return nil, clientroute.Fail(vocab.CodeUnsupportedOption, "claude-code: the client takes no temperature, top_p, stop sequence or output cap")
	case c.ToolChoice != (vocab.ToolChoice{}) && c.ToolChoice.Mode != vocab.ToolChoiceAuto:
		return nil, clientroute.Fail(vocab.CodeUnsupportedOption, "claude-code: the client takes no tool choice")
	case c.Effort != "" && !slices.Contains(Efforts, c.Effort):
		return nil, clientroute.Fail(vocab.CodeUnsupportedReasoningEffort, "claude-code: the client takes no effort %q", c.Effort)
	}
	env, err := a.env.Build(a.cfg.Host())
	if err != nil {
		return nil, clientroute.Fail(vocab.CodeInvalidRequest, "claude-code: %v", err)
	}
	prog, err := clientroute.Locate(Client, a.cfg.Program, Program, env)
	if err != nil {
		return nil, err
	}
	plan, err := clientroute.HelperLaunch(ctx, a.cfg.Sandbox, Client, string(RouteID))
	if err != nil {
		return nil, err
	}
	v, err := clientroute.Probe(ctx, clientroute.ProbeSpec{
		Client: Client, Program: prog, Args: []string{"--version"}, Env: env,
		Pattern: VersionPattern, Floor: Floor, Timeout: a.cfg.ProbeTimeout, Launch: plan,
	})
	if err != nil {
		return nil, err
	}
	return &call{cfg: a.cfg, prog: prog, env: env, plan: plan, version: v, model: model, effort: c.Effort}, nil
}

// call is one prepared call; its attempt's invocation record is the
// llm.InvocationReporter the runtime settles.
type call struct {
	clientroute.Recorder
	cfg     Config
	prog    subprocess.Program
	env     subprocess.Env
	plan    sandbox.Launch
	version clientroute.Version
	model   string
	effort  string
}

var _ llm.InvocationReporter = (*call)(nil)

// mcpConfig is the attempt's MCP configuration: the one listing server.
func mcpConfig(listingExe, tools, record string) ([]byte, error) {
	type server struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
	}
	return json.Marshal(map[string]any{"mcpServers": map[string]server{
		listing.ServerName: {Type: "stdio", Command: listingExe, Args: listing.Args(tools, record), Env: map[string]string{}},
	}})
}

// Attempt renders the request, writes the attempt files, runs the client once
// and maps its lines to chunks. The attempt directory and the whole process
// tree are gone before the Finish is yielded.
func (c *call) Attempt(ctx context.Context, req session.Request) iter.Seq2[vocab.Chunk, error] {
	return func(yield func(vocab.Chunk, error) bool) {
		r, err := clientroute.Render(req, clientroute.RenderOptions{})
		if err != nil {
			yield(nil, err)
			return
		}
		dir, err := clientroute.NewAttemptDir(c.cfg.TempDir)
		if err != nil {
			yield(nil, clientroute.Fail(vocab.CodeTransport, "claude-code: the attempt directory cannot be made"))
			return
		}
		defer func() { _ = dir.Remove() }()
		tools := len(r.ToolNames) > 0
		write := func(name string, data []byte) bool {
			if _, err := dir.Write(name, data); err != nil {
				yield(nil, clientroute.Fail(vocab.CodeTransport, "claude-code: the attempt file %s cannot be written", name))
				return false
			}
			return true
		}
		if !write(SystemFile, r.System) {
			return
		}
		if tools {
			cfg, err := mcpConfig(c.cfg.Listing, dir.Join(ToolsFile), dir.Join(CallsFile))
			if err != nil || !write(ToolsFile, r.Tools) || !write(MCPFile, cfg) {
				return
			}
		}
		sid := clientroute.NewSessionID()
		args := Args(ArgsSpec{Dir: dir.Path(), Model: c.model, Effort: c.effort, SessionID: sid, Tools: tools})
		c.Set(clientroute.NewInvocation(clientroute.Invocation{
			Client: Client, Version: c.version, Program: c.prog.Path, Args: args, Dir: dir.Path(),
			Stdin: r.Prompt, Files: dir.Files(), Env: c.env, SessionID: sid,
		}))
		grace := c.cfg.Grace
		if grace <= 0 {
			grace = clientroute.DefaultGrace
		}
		run, err := clientroute.Start(ctx, clientroute.RunSpec{
			Client: Client, Program: c.prog, Args: args, Dir: dir.Path(), Env: c.env, Stdin: r.Prompt, Grace: grace, Launch: c.plan,
		})
		if err != nil {
			yield(nil, err)
			return
		}
		defer func() { _ = run.Stop(0) }()
		m := newMapper(expect{sessionID: sid, version: c.version.String(), tools: tools, keySources: c.cfg.KeySources})
		out, reading := m.stream(clientroute.Tapped(run.ReadLine, c.cfg.Tap), func(ch vocab.Chunk) bool { return yield(ch, nil) })
		if !reading {
			return
		}
		// The output has ended (or the attempt cannot continue): the client
		// gets a moment to exit, then whatever is left of the tree is stopped
		// before the listing server's record is read.
		if m.fatal == nil {
			run.Wait(grace)
		}
		_ = run.Stop(0)
		calls, cerr := listing.Calls(dir.Join(CallsFile))
		if cerr != nil {
			calls = -1
		}
		// The directory goes before the Finish is yielded, as Attempt promises: a
		// consumer that settles on the Finish never finds it left behind. The
		// deferred Remove is then a no-op.
		_ = dir.Remove()
		c.Update(func(inv *event.ClientInvocation) {
			inv.Model, inv.Retries, inv.Denials = m.model, m.retries, len(m.denied)
		})
		if ctx.Err() != nil {
			yield(vocab.Finish{Reason: vocab.FinishAborted,
				Failure: clientroute.Fail(vocab.CodeAborted, "claude-code: the attempt was cancelled: %v", context.Cause(ctx))}, nil)
			return
		}
		fin := m.decide(out, calls, run.StderrHas(unknownOption))
		if u := m.usage(out); u != nil {
			if !yield(vocab.UsageChunk{Usage: *u}, nil) {
				return
			}
		}
		yield(fin, nil)
	}
}
