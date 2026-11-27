package headless

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/launch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/command"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm"
	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
)

// maxTask bounds a task read from standard input.
const maxTask = 4 << 20

// stopped is the status of a run that the launch stopped first: the runner then
// prints nothing and leaves the status to that stop.
const stopped = -1

// runner is one `dsh headless` run: the interval from accepting the task to the
// agent and its descendants being idle and flushed (DH-ADR-CLI-7).
type runner struct {
	owner  *cordis.Context
	cfg    Config
	app    launch.App
	data   launch.Data
	agents agent.Registry
	routes *llm.Runtime
	plane  plane
	in     io.Reader
	out    io.Writer
	diag   io.Writer
	// inTTY reports a terminal on standard input: with no task word the task is
	// read only from a pipe. diagTTY decides --reasoning auto.
	inTTY, diagTTY bool
	// maxTask bounds a task read from standard input; zero is maxTask.
	maxTask int

	mu sync.Mutex
	// bound are the agents of the run's tree bound to channel cli, by session.
	bound map[session.ID]agent.Ref
}

// run waits for the launch to be ready, runs the task once and stops the launch
// with the run's status, unless the launch stopped first.
func (r *runner) run(ctx context.Context) {
	select {
	case <-r.app.Ready():
	case <-r.app.Stopping():
		return
	case <-ctx.Done():
		return
	}
	status := r.once(ctx)
	if status == stopped || r.stopping(ctx) {
		return
	}
	r.app.Stop(launch.Completed, status)
}

func (r *runner) stopping(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	select {
	case <-r.app.Stopping():
		return true
	default:
		return false
	}
}

// fail writes err as one diagnostic line and returns status.
func (r *runner) fail(err error, status int) int {
	fmt.Fprintf(r.diag, "dsh headless: %s\n", strings.Join(strings.Fields(err.Error()), " "))
	if status == output.Usage {
		fmt.Fprintln(r.diag, output.HelpHint)
	}
	return status
}

func (r *runner) once(ctx context.Context) int {
	args, err := Parse(r.data.Words)
	if err != nil {
		return r.fail(err, output.Usage)
	}
	task := args.Task
	if args.Command == "" && args.Stdin {
		if r.inTTY {
			return r.fail(output.Usagef("no task: give it as words, or pipe it on standard input"), output.Usage)
		}
		var ok bool
		if task, ok, err = r.readTask(ctx); !ok {
			return stopped
		} else if errors.Is(err, errTaskTooLong) {
			return r.fail(err, output.Usage)
		} else if err != nil {
			return r.fail(fmt.Errorf("read the task: %w", err), output.Failed)
		}
	}
	if args.Command == "" && strings.TrimSpace(task) == "" {
		return r.fail(output.Usagef("the task is empty"), output.Usage)
	}
	route, err := r.route()
	if err != nil {
		return r.fail(err, output.Failed)
	}
	h, status := r.open(ctx, args, route)
	if h == nil {
		return status
	}
	a := h.Agent()
	rs := a.Session()
	f := &follower{r: r, rs: rs, next: rs.Len(), json: args.JSON, reasoning: r.showReasoning(args.Reasoning), poke: make(chan struct{}, 1), route: route}
	if _, err := session.Committed.On(r.owner, func(_ *cordis.Context, c session.CommittedArgs) error {
		if c.Session.ID() == rs.ID() {
			f.wake()
		}
		return nil
	}); err != nil {
		return r.fail(errors.Join(err, h.Dispose(ctx)), output.Failed)
	}
	// U-K2-4: one progress line at the start, before any token; it holds no
	// model output.
	fmt.Fprintf(r.diag, "dsh headless: run %s on route %s\n", rs.ID(), route)
	final, followed := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(followed)
		f.loop(ctx, final)
	}()
	end := func() { close(final); <-followed }
	if args.ApprovalPolicy != "" {
		by := approval.By{Principal: string(ChannelCLI), Channel: string(ChannelCLI)}
		if err := r.plane.approvals.SetPolicy(ctx, a.Ref(), approval.Policy(args.ApprovalPolicy), by); err != nil {
			end()
			return r.fail(errors.Join(err, h.Dispose(ctx)), output.Failed)
		}
	}
	var cmd *command.Result
	if args.Command != "" {
		res, err := r.command(ctx, a, args.Command)
		if err != nil {
			end()
			status := output.Failed
			if errors.Is(err, command.ErrUnknown) || errors.Is(err, command.ErrInvalid) || errors.Is(err, command.ErrNotCommand) {
				status = output.Usage
			}
			return r.fail(errors.Join(err, h.Dispose(ctx)), status)
		}
		cmd = &res
	} else if _, err := agent.Send(ctx, a, agent.ListNextTurn, agent.InboxMessage{Source: agent.SourceUser, By: "cli", Text: task}); err != nil {
		end()
		if r.stopping(ctx) {
			return stopped
		}
		return r.fail(errors.Join(err, h.Dispose(ctx)), output.Failed)
	}
	if err := r.waitTree(ctx, a); err != nil {
		end()
		if r.stopping(ctx) {
			return stopped
		}
		return r.fail(errors.Join(err, h.Dispose(ctx)), output.Failed)
	}
	end()
	if r.stopping(ctx) {
		return stopped
	}
	// Dispose is the flush: what the run wrote is durable before it reports.
	if err := h.Dispose(ctx); err != nil {
		return r.fail(err, output.Failed)
	}
	if f.err != nil {
		return r.fail(f.err, output.Failed)
	}
	status = output.Failed
	if f.reason == event.ReasonCompleted {
		status = output.OK
	}
	if cmd != nil && f.turn == 0 {
		// The command handed the agent nothing: its own outcome is the run's.
		f.reason, f.text = "command/"+string(cmd.Outcome), cmd.Output
		if cmd.Outcome == command.OutcomeOK {
			status = output.OK
		}
	}
	if err := r.result(rs.ID(), f); err != nil {
		return r.fail(err, output.Failed)
	}
	return status
}

// open creates the run's agent, or resumes the session --session names, and
// binds it in its Setup, before it goes live: to its agent preset (the
// preset's tools and prompt sections reach its scope) and to channel cli. A
// fresh run uses the agent preset registry's default unless --agent-preset
// names one; a resumed run binds the session's recorded preset, refused with
// status 5 when it no longer exists or is broken, with no fallback. It returns a
// nil handle and the status on failure.
func (r *runner) open(ctx context.Context, args Args, route vocab.RouteID) (agent.Handle, int) {
	presets, hasPresets := agentpreset.Key.Lookup(r.owner)
	o := agent.CreateOptions{Model: agent.ModelSelection{Route: route, Model: r.cfg.Model}}
	o.Setup = func(sc *cordis.Context, a agent.Agent) (func() error, error) {
		if hasPresets {
			if err := r.bindPreset(ctx, presets, sc, a, args); err != nil {
				return nil, err
			}
		}
		return nil, r.bindChannel(a, human.Binding{Channel: ChannelCLI})
	}
	var h agent.Handle
	var err error
	if args.Session != "" {
		o.Session = session.ID(args.Session)
		h, err = r.agents.Resume(r.owner, o)
	} else {
		o.Session, o.Preset, o.Cwd = newID(), args.AgentPreset, r.data.Cwd
		if hasPresets && o.Preset == "" {
			o.Preset = presets.Default()
		}
		h, err = r.agents.Create(r.owner, o)
	}
	if err != nil {
		return nil, r.fail(err, statusOf(err))
	}
	return h, output.OK
}

// bindPreset binds a to its preset: --agent-preset for a fresh agent ("" is the
// default), the recorded one (the preset fold, else the header) for a resumed
// one.
func (r *runner) bindPreset(ctx context.Context, presets *agentpreset.Registry, sc *cordis.Context, a agent.Agent, args Args) error {
	own, ok := a.(interface{ OwnScope() *scope.Scope })
	s, live := r.plane.sessions.Get(a.Ref().Session)
	if !ok || !live {
		return errors.New("headless: the agent's scope or session cannot be bound to a preset")
	}
	id, cause := args.AgentPreset, agentpreset.CauseCreate
	if args.Session != "" {
		cause = agentpreset.CauseResume
		b, err := agentpreset.BindingOf(ctx, a.Session())
		if err != nil {
			return err
		}
		if id = b.Preset; id == "" {
			id = a.Session().Header().Preset
		}
	}
	if _, err := presets.Bind(ctx, agentpreset.Target{Session: s, Scope: own.OwnScope(), Owner: sc}, id, cause); err != nil {
		return fmt.Errorf("agent preset %q: %w", id, err)
	}
	return nil
}

// bindChannel binds a for the life of its scope and records it in the run's
// tree.
func (r *runner) bindChannel(a agent.Agent, b human.Binding) error {
	if _, err := r.plane.interactions.Bind(a.Scope(), a.Ref(), b); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bound == nil {
		r.bound = map[session.ID]agent.Ref{}
	}
	r.bound[a.Ref().Session] = a.Ref()
	return nil
}

// bindTree binds each subagent whose creator is in the run's tree via its
// creator, before it goes live, so its approvals are asked on the same terminal.
func (r *runner) bindTree() error {
	_, err := agent.Created.On(r.owner, func(_ *cordis.Context, n agent.CreatedNotice) error {
		hd := n.Agent.Session().Header()
		if hd.Origin != "subagent" || hd.Creator == "" {
			return nil
		}
		r.mu.Lock()
		via, ok := r.bound[session.ID(hd.Creator)]
		r.mu.Unlock()
		if !ok {
			return nil
		}
		return r.bindChannel(n.Agent, human.Binding{Via: via})
	})
	return err
}

// command runs line against a through the command registry, as the terminal's
// person: it appends command/run and command/done, and starts no model turn
// unless its handler hands the agent a message.
func (r *runner) command(ctx context.Context, a agent.Agent, line string) (command.Result, error) {
	s, ok := r.plane.sessions.Get(a.Ref().Session)
	if !ok {
		return command.Result{}, command.ErrNoTarget
	}
	t := command.Target{Agent: a.Ref(), Scope: a.ScopeKey(), Log: s, Inbox: inbox{a}, Owner: string(ChannelCLI)}
	return r.plane.commands.Execute(ctx, t, human.Actor{Channel: ChannelCLI, Name: string(ChannelCLI), Peer: peerTerminal}, line)
}

// inbox is an agent's inbox for a command's sends.
type inbox struct{ a agent.Agent }

func (i inbox) Send(ctx context.Context, list agent.List, m agent.InboxMessage) (agent.Receipt, error) {
	return agent.Send(ctx, i.a, list, m)
}

// statusOf is the status of a create or resume failure: 5 for an unknown
// session or an unknown or broken agent preset, 6 for one another process
// holds, else 1.
func statusOf(err error) int {
	switch {
	case errors.Is(err, store.ErrNotFound), errors.Is(err, agentpreset.ErrNotFound), errors.Is(err, agentpreset.ErrInvalid):
		return output.Invalid
	case errors.Is(err, store.ErrOwned):
		return output.Busy
	}
	return output.Failed
}

// route is the model route of the run: the configured one, else the one route
// the runtime serves.
func (r *runner) route() (vocab.RouteID, error) {
	if r.cfg.Route != "" {
		return vocab.RouteID(r.cfg.Route), nil
	}
	ids := r.routes.Routes()
	switch len(ids) {
	case 1:
		return ids[0], nil
	case 0:
		return "", errors.New("no model route is configured")
	}
	return "", fmt.Errorf("%d model routes are configured; name one in cli.headless's route", len(ids))
}

// errTaskTooLong is a task on standard input longer than the bound: refused
// whole, never run cut.
var errTaskTooLong = errors.New("the task on standard input is too long")

// readTask reads the task from standard input, at most the bound; one byte more
// is errTaskTooLong. ok is false when the launch stopped first.
func (r *runner) readTask(ctx context.Context) (task string, ok bool, err error) {
	limit := r.maxTask
	if limit <= 0 {
		limit = maxTask
	}
	type read struct {
		b   []byte
		err error
	}
	done := make(chan read, 1)
	go func() {
		b, err := io.ReadAll(io.LimitReader(r.in, int64(limit)+1))
		if err == nil && len(b) > limit {
			b, err = nil, fmt.Errorf("%w: the bound is %d bytes", errTaskTooLong, limit)
		}
		done <- read{b, err}
	}()
	select {
	case got := <-done:
		return string(got.b), true, got.err
	case <-r.app.Stopping():
	case <-ctx.Done():
	}
	return "", false, nil
}

// waitTree returns once the root is idle and its live descendants have finished,
// as session/wait does.
func (r *runner) waitTree(ctx context.Context, root agent.Agent) error {
	for settled := false; !settled; {
		settled = true
		for _, x := range r.tree(root) {
			if err := agent.WaitIdle(ctx, x); err != nil {
				return err
			}
		}
		for _, x := range r.tree(root) {
			settled = settled && x.Status() == agent.StatusIdle
		}
	}
	return nil
}

// tree is root and the live subagents it created, transitively.
func (r *runner) tree(root agent.Agent) []agent.Agent {
	out := []agent.Agent{root}
	in := map[string]bool{string(root.Session().ID()): true}
	all := r.agents.List()
	for grew := true; grew; {
		grew = false
		for _, x := range all {
			h := x.Session().Header()
			if h.Origin == "subagent" && in[h.Creator] && !in[h.ID] {
				in[h.ID], grew = true, true
				out = append(out, x)
			}
		}
	}
	return out
}

func (r *runner) showReasoning(mode string) bool {
	switch mode {
	case "show":
		return true
	case "hide":
		return false
	}
	return r.diagTTY
}

// runResult is the last line under --json.
type runResult struct {
	Type    string `json:"type"`
	Session string `json:"session"`
	Turn    int    `json:"turn"`
	Reason  string `json:"reason"`
	Text    string `json:"text"`
}

// result writes the run's result: the last assistant text of its interval, or
// under --json the run/result line.
func (r *runner) result(id session.ID, f *follower) error {
	var b []byte
	if f.json {
		line, err := json.Marshal(runResult{Type: "run/result", Session: string(id), Turn: f.turn, Reason: f.reason, Text: f.text})
		if err != nil {
			return err
		}
		b = append(line, '\n')
	} else if f.text != "" {
		b = []byte(f.text + "\n")
	}
	if len(b) == 0 {
		return nil
	}
	if _, err := r.out.Write(b); err != nil {
		return fmt.Errorf("%s: write result: %w", output.ReasonClosed, err)
	}
	return nil
}

func newID() session.ID {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return session.ID("ses_" + hex.EncodeToString(b[:]))
}

// isTerminal reports whether f is a character device.
func isTerminal(f *os.File) bool {
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
