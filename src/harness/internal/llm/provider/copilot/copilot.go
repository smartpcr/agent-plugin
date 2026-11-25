// Package copilot is the copilot-cli route (DH-D-LLM-12, DH-ADR-LLM-10): a
// model request is sent through the user's installed, signed-in GitHub Copilot
// CLI, started in prompt mode with JSONL output as a child process, one process
// per attempt (docs/harness/libs/github-copilot-cli.md, DH-ADR-COPILOTCLI-1;
// owner decision M1-D-PIPE-8: the CLI, not the SDK). The harness never reads a
// token: every credential and provider override is removed from the child's
// environment, so the client's own sign-in is what is used.
//
// The request is rendered to the client's standard input. The client cannot
// replace its own system prompt, so the harness system prompt is the leading
// <system> section of that input and the client's own also reaches the model.
// Every built-in tool kind is denied by rule; the harness tools are listed by
// the listing-only MCP server (--additional-mcp-config) and denied by name. The
// client has no turn limit, so the adapter stops the whole process tree as soon
// as an assistant message requests a tool, and returns the calls unexecuted as
// ToolCallDelta and Finish{ToolCalls}; the driver runs them through the guarded
// pipeline (M1-D-PIPE-6 A). A tool.execution_start line means a tool ran inside
// the client: the attempt fails with llm/client-contract.
//
// The JSONL schema is undocumented: the line types and fields mapped here are
// those of the lib record, and a line of a placed type that lacks a field the
// route needs fails closed with llm/client-contract. The fixtures under
// testdata are synthetic until the plan 16.5.9 recording replaces them.
//
// Saved sessions (M1-D-PIPE-7 A). The client has no switch that stops it saving
// the session: each attempt leaves one session under the user's Copilot home
// ($COPILOT_HOME/session-state/<id>/), which "is kept until you delete" it.
// dsh never reads, writes or deletes anything under the Copilot home, because it
// does not own it. It chooses each attempt's client session id (--session-id)
// and logs it in the attempt's invocation record, so the user can find and
// delete those sessions; the decision is revisited if the client gains a
// no-persist switch.
//
// The route is registered by whoever wants it (Route); it is never a default.
package copilot

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
const RouteID vocab.RouteID = "copilot-cli"

// Client names the client in the invocation record and in failures.
const Client = "copilot-cli"

// Program is the client's bare name, looked up on the child's PATH. On Windows
// an npm install is a copilot.cmd shim, started through the checked batch run
// (plan 16.5.1); the VS Code shim is an .exe.
const Program = "copilot"

// Efforts are the levels --reasoning-effort takes. Inference: the lib record
// names the flag but not its values; DH-E-COPILOTCLI-1 confirms them.
var Efforts = []string{"low", "medium", "high", "xhigh"}

// Models is the default catalogue; the first is the default model. Inference:
// which models an account offers is the account's; Config.Models overrides it.
var Models = []llm.Model{{Name: "claude-sonnet-4.5"}, {Name: "gpt-5", Efforts: Efforts}}

// Config configures the route.
type Config struct {
	// Program is the client's absolute path; empty looks up "copilot" on the
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
	// MaxConcurrent caps the route's concurrent attempts; zero is uncapped.
	MaxConcurrent int
	// Retry is the route's retry policy; zero is llm.DefaultRetry. A signed-out
	// client and a contract breach are Permanent, so they are never retried.
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

// Route is the copilot-cli route over cfg.
func Route(cfg Config) (llm.Route, error) {
	if cfg.Sandbox == nil {
		return llm.Route{}, errors.New("copilot: the route needs a sandbox resolver to plan the client's launch")
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
			return llm.Route{}, errors.New("copilot: the harness executable cannot be found for the listing server")
		}
		cfg.Listing = exe
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
// is written: a missing or too-old client is llm/client-upgrade-required. Only
// the model and the reasoning effort can be set; any other control is
// llm/unsupported-option.
func (a *adapter) Prepare(ctx context.Context, route vocab.RouteID, model string, cfg vocab.CallConfig) (llm.Call, error) {
	if route != RouteID {
		return nil, clientroute.Fail(vocab.CodeNoRoute, "copilot: asked for route %q", route)
	}
	c := cfg.Controls
	switch {
	case c.Temperature != nil, c.TopP != nil, len(c.Stop) > 0, c.MaxOutputTokens != 0:
		return nil, clientroute.Fail(vocab.CodeUnsupportedOption, "copilot-cli: the client takes no temperature, top_p, stop sequence or output cap")
	case c.ToolChoice != (vocab.ToolChoice{}) && c.ToolChoice.Mode != vocab.ToolChoiceAuto:
		return nil, clientroute.Fail(vocab.CodeUnsupportedOption, "copilot-cli: the client takes no tool choice")
	case c.Effort != "" && !slices.Contains(Efforts, c.Effort):
		return nil, clientroute.Fail(vocab.CodeUnsupportedReasoningEffort, "copilot-cli: the client takes no effort %q", c.Effort)
	}
	env, err := a.env.Build(a.cfg.Host())
	if err != nil {
		return nil, clientroute.Fail(vocab.CodeInvalidRequest, "copilot-cli: %v", err)
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
		Client: Client, Program: prog, Args: ProbeArgs, Env: env,
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

// mcpConfig is the attempt's MCP configuration: the one listing server, every
// tool of it offered (the client's rule --deny-tool dsh still denies each
// call). Inference: the client's own configuration names a stdio server
// "local" and lists its enabled tools; DH-E-COPILOTCLI-1 confirms the form.
func mcpConfig(listingExe, tools, record string) ([]byte, error) {
	type server struct {
		Type    string            `json:"type"`
		Command string            `json:"command"`
		Args    []string          `json:"args"`
		Env     map[string]string `json:"env"`
		Tools   []string          `json:"tools"`
	}
	return json.Marshal(map[string]any{"mcpServers": map[string]server{
		listing.ServerName: {Type: "local", Command: listingExe, Args: listing.Args(tools, record), Env: map[string]string{}, Tools: []string{"*"}},
	}})
}

// Attempt renders the request, writes the attempt files, runs the client once
// and maps its lines to chunks. The attempt directory and the whole process
// tree are gone before the Finish is yielded.
func (c *call) Attempt(ctx context.Context, req session.Request) iter.Seq2[vocab.Chunk, error] {
	return func(yield func(vocab.Chunk, error) bool) {
		r, err := clientroute.Render(req, clientroute.RenderOptions{SystemInPrompt: true})
		if err != nil {
			yield(nil, err)
			return
		}
		dir, err := clientroute.NewAttemptDir(c.cfg.TempDir)
		if err != nil {
			yield(nil, clientroute.Fail(vocab.CodeTransport, "copilot-cli: the attempt directory cannot be made"))
			return
		}
		defer func() { _ = dir.Remove() }()
		if len(r.ToolNames) > 0 {
			cfg, err := mcpConfig(c.cfg.Listing, dir.Join(ToolsFile), dir.Join(CallsFile))
			if err == nil {
				_, err = dir.Write(ToolsFile, r.Tools)
			}
			if err == nil {
				_, err = dir.Write(MCPFile, cfg)
			}
			if err != nil {
				yield(nil, clientroute.Fail(vocab.CodeTransport, "copilot-cli: the attempt files cannot be written"))
				return
			}
		}
		sid := clientroute.NewSessionID()
		args := Args(ArgsSpec{Dir: dir.Path(), Model: c.model, Effort: c.effort, SessionID: sid, Tools: r.ToolNames})
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
		m := newMapper(r.ToolNames)
		// At a tool request the tree is stopped at once, before the calls are
		// yielded: the client cannot act on its denial or send another request.
		stop := func() { _ = run.Stop(0) }
		if !m.stream(clientroute.Tapped(run.ReadLine, c.cfg.Tap), stop, func(ch vocab.Chunk) bool { return yield(ch, nil) }) {
			return
		}
		// The output has ended (or the attempt cannot continue): the client gets
		// a moment to exit and report its status, then whatever is left of the
		// tree is stopped before the listing server's record is read.
		exit := -1
		if m.fatal == nil && m.toolCalls == 0 {
			exit = run.Wait(grace)
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
		c.Update(func(inv *event.ClientInvocation) { inv.Retries = m.retries })
		if ctx.Err() != nil {
			yield(vocab.Finish{Reason: vocab.FinishAborted,
				Failure: clientroute.Fail(vocab.CodeAborted, "copilot-cli: the attempt was cancelled: %v", context.Cause(ctx))}, nil)
			return
		}
		yield(m.decide(exit, calls), nil)
	}
}
