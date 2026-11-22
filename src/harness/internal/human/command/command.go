// Package command runs a person's commands against an agent from any client
// (DH-D-HUMAN-6): "/name arguments" typed in the browser, the terminal or sent
// by the SDK. Commands live in a scoped registry on the session's layered tables:
// one registered on an agent's scope shadows a global one of the same name.
// Execute parses the line, decodes typed arguments through package schema,
// checks the actor, appends command/run, calls the handler with a deadline and
// appends command/done; both events are attributed. A command never starts a
// model turn by itself: what a handler hands the agent is delivered through the
// agent's inbox, attributed to the person who ran the command.
package command

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human"
	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
)

// DefaultTimeout bounds a handler when the command sets no timeout.
const DefaultTimeout = 30 * time.Second

// Command is one registered command.
type Command struct {
	// Name is what follows the slash: 1 to 32 of [a-z0-9-], starting with a
	// letter.
	Name    string
	Summary string
	// Args is the shape of typed arguments, given as a JSON object after the
	// name; nil passes the rest of the line as text.
	Args *schema.Type
	// OwnerOnly refuses everyone but the agent's owner.
	OwnerOnly bool
	// Timeout bounds the handler; zero is DefaultTimeout.
	Timeout time.Duration
	// Run is the handler; its output is logged in command/done.
	Run func(ctx context.Context, c Call) (string, error)
}

var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Call is what a handler receives.
type Call struct {
	ID    string
	Agent agent.Ref
	By    human.Actor
	Name  string
	// Rest is the line after the name, trimmed.
	Rest string
	// Args is the validated argument object, nil for a command without Args.
	Args  json.RawMessage
	inbox Inbox
	state *callState
}

// callState is shared by every copy of a Call: it numbers sends and records
// that run has settled the command.
type callState struct {
	sends   atomic.Uint64
	settled atomic.Bool
}

// ErrSettled is a Send after the command settled, typically from a handler that
// outlived its deadline and was recorded aborted.
var ErrSettled = errors.New("command/settled")

// Send delivers text to the agent through its inbox, attributed to the person
// who ran the command; list next-turn wakes an idle agent. Each send gets its
// own message ID (msg-<command>-<n>), and a send after the command settled is
// refused with ErrSettled.
func (c Call) Send(ctx context.Context, list agent.List, text string) (agent.Receipt, error) {
	if c.inbox == nil {
		return agent.Receipt{}, errors.New("command: the agent has no inbox")
	}
	if c.state == nil {
		c.state = &callState{}
	}
	if c.state.settled.Load() {
		return agent.Receipt{}, fmt.Errorf("%w: %s", ErrSettled, c.ID)
	}
	n := c.state.sends.Add(1)
	return c.inbox.Send(ctx, list, agent.InboxMessage{ID: fmt.Sprintf("msg-%s-%d", c.ID, n), Source: agent.SourceCommand, By: principal(c.By), Text: text})
}

// Inbox is the target agent's inbox (*agent.Inbox).
type Inbox interface {
	Send(ctx context.Context, list agent.List, m agent.InboxMessage) (agent.Receipt, error)
}

// Log is the target agent's session.
type Log interface {
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
}

// Target is the agent a command runs against.
type Target struct {
	Agent agent.Ref
	// Scope is the agent's scope, for the registry's shadowing.
	Scope scope.Key
	Log   Log
	Inbox Inbox
	// Owner is the principal who owns the agent, for owner-only commands.
	Owner string
}

// Outcome is how a command ended.
type Outcome string

// The outcomes.
const (
	OutcomeOK      Outcome = "ok"
	OutcomeFailed  Outcome = "failed"
	OutcomeAborted Outcome = "aborted"
)

// Run is command/run.
type Run struct {
	ID    string          `json:"id"`
	Agent string          `json:"agent"`
	Name  string          `json:"name"`
	Rest  string          `json:"rest,omitempty"`
	Args  json.RawMessage `json:"args,omitempty"`
	By    By              `json:"by"`
}

// Done is command/done.
type Done struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Outcome Outcome `json:"outcome"`
	Output  string  `json:"output,omitempty"`
	Detail  string  `json:"detail,omitempty"`
}

// By attributes a command.
type By struct {
	Principal string `json:"principal"`
	Channel   string `json:"channel,omitempty"`
}

// The command events, log-only.
var (
	RunType = event.MustDefine(event.Default(), event.Spec[Run]{Name: "command/run", Version: 1,
		Validate: func(r Run) error {
			if r.ID == "" || r.Agent == "" || r.Name == "" || r.By.Principal == "" {
				return errors.New("command/run: id, agent, name and principal are required")
			}
			return nil
		}})
	DoneType = event.MustDefine(event.Default(), event.Spec[Done]{Name: "command/done", Version: 1,
		Validate: func(d Done) error {
			if d.ID == "" || d.Name == "" {
				return errors.New("command/done: id and name are required")
			}
			if d.Outcome != OutcomeOK && d.Outcome != OutcomeFailed && d.Outcome != OutcomeAborted {
				return fmt.Errorf("command/done: outcome %q is not in the closed set", d.Outcome)
			}
			return nil
		}})
)

// The errors. None of them logs anything.
var (
	// ErrNotCommand is a line that is not a command: the caller sends it as a
	// message.
	ErrNotCommand = errors.New("command/not-a-command")
	// ErrUnknown is a name no visible command has; it is never a model message.
	ErrUnknown    = errors.New("command/unknown")
	ErrInvalid    = errors.New("command/invalid-arguments")
	ErrForbidden  = errors.New("command/forbidden")
	ErrNoTarget   = errors.New("command/no-target")
	ErrBadCommand = errors.New("command/invalid-command")
)

type layer = *scope.Named[Command]

// Registry is the scoped command registry.
type Registry struct{ layers *scope.Layers[layer] }

// NewRegistry is an empty registry over t.
func NewRegistry(t scope.Table) *Registry {
	return &Registry{layers: scope.NewLayers(t, func() layer { return scope.NewNamed[Command]() })}
}

// Register adds c to the layer of ctx's scope (global for an untagged context)
// as an effect of ctx's fiber; disposing the handle or unloading the fiber
// removes it.
func (r *Registry) Register(ctx *cordis.Context, c Command) (*cordis.EffectHandle, error) {
	if !validName.MatchString(c.Name) || c.Run == nil {
		return nil, fmt.Errorf("%w: %q needs a valid name and a handler", ErrBadCommand, c.Name)
	}
	return r.layers.Add(ctx, "command:"+c.Name, func(l layer) func() { return l.Set(c.Name, c) })
}

// Lookup is the command name visible at k: the nearest scope's wins.
func (r *Registry) Lookup(k scope.Key, name string) (Command, bool) {
	for _, l := range r.layers.Chain(k) {
		if c, ok := l.Get(name); ok {
			return c, true
		}
	}
	return Command{}, false
}

// List is every command visible at k, sorted by name.
func (r *Registry) List(k scope.Key) []Command {
	var out []Command
	for _, e := range scope.Merged(r.layers, k, func(l layer) *scope.Named[Command] { return l }) {
		out = append(out, e.Value)
	}
	return out
}

// Parse splits "/name rest" into the name and the trimmed rest.
func Parse(line string) (name, rest string, err error) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "/") || strings.HasPrefix(line, "//") {
		return "", "", ErrNotCommand
	}
	name, rest, _ = strings.Cut(line[1:], " ")
	if !validName.MatchString(name) {
		return "", "", fmt.Errorf("%w: %q", ErrUnknown, name)
	}
	return name, strings.TrimSpace(rest), nil
}

// Result is a command's settlement.
type Result struct {
	ID      string
	Outcome Outcome
	Output  string
	Detail  string
}

// Execute runs line against t on behalf of by. A line that is not a command, an
// unknown name, invalid arguments or a refused actor return an error and log
// nothing. Otherwise command/run is appended before the handler runs and
// command/done after it settles; a handler that outlives its deadline settles
// aborted.
func (r *Registry) Execute(ctx context.Context, t Target, by human.Actor, line string) (Result, error) {
	if t.Agent.IsZero() || t.Log == nil {
		return Result{}, ErrNoTarget
	}
	name, rest, err := Parse(line)
	if err != nil {
		return Result{}, err
	}
	c, ok := r.Lookup(t.Scope, name)
	if !ok {
		return Result{}, fmt.Errorf("%w: /%s", ErrUnknown, name)
	}
	var args json.RawMessage
	if c.Args != nil {
		raw := rest
		if raw == "" {
			raw = "{}"
		}
		var v any
		if issues := c.Args.Decode([]byte(raw), &v); len(issues) > 0 {
			return Result{}, fmt.Errorf("%w: /%s: %v", ErrInvalid, name, issues)
		}
		args = json.RawMessage(raw)
	}
	who := principal(by)
	if c.OwnerOnly && (t.Owner == "" || who != t.Owner) {
		return Result{}, fmt.Errorf("%w: /%s is the owner's", ErrForbidden, name)
	}
	id := mintID()
	d, err := RunType.Draft(Run{ID: id, Agent: t.Agent.String(), Name: name, Rest: rest, Args: args, By: By{Principal: who, Channel: string(by.Channel)}})
	if err == nil {
		_, err = t.Log.Append(ctx, d)
	}
	if err != nil {
		return Result{}, fmt.Errorf("command: append command/run: %w", err)
	}

	res := run(ctx, c, Call{ID: id, Agent: t.Agent, By: by, Name: name, Rest: rest, Args: args, inbox: t.Inbox, state: &callState{}})
	res.ID = id
	d, err = DoneType.Draft(Done{ID: id, Name: name, Outcome: res.Outcome, Output: res.Output, Detail: res.Detail})
	if err == nil {
		_, err = t.Log.Append(context.WithoutCancel(ctx), d)
	}
	if err != nil {
		return res, fmt.Errorf("command: append command/done: %w", err)
	}
	return res, nil
}

// run calls the handler under its deadline; a handler that ignores cancellation
// is left behind and the command settles aborted.
func run(ctx context.Context, c Command, call Call) Result {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	hctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if call.state == nil {
		call.state = &callState{}
	}
	defer call.state.settled.Store(true)
	type settled struct {
		out string
		err error
	}
	ch := make(chan settled, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				ch <- settled{err: fmt.Errorf("panic: %v", p)}
			}
		}()
		out, err := c.Run(hctx, call)
		ch <- settled{out, err}
	}()
	select {
	case s := <-ch:
		switch {
		case s.err == nil:
			return Result{Outcome: OutcomeOK, Output: s.out}
		case hctx.Err() != nil:
			return Result{Outcome: OutcomeAborted, Output: s.out, Detail: s.err.Error()}
		default:
			return Result{Outcome: OutcomeFailed, Output: s.out, Detail: s.err.Error()}
		}
	case <-hctx.Done():
		return Result{Outcome: OutcomeAborted, Detail: context.Cause(hctx).Error()}
	}
}

func principal(a human.Actor) string {
	if a.Name != "" {
		return a.Name
	}
	if a.Channel != "" {
		return string(a.Channel)
	}
	return "unknown"
}

func mintID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "cmd-" + hex.EncodeToString(b[:])
}
