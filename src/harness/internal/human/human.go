// Package human is the interaction registry (DH-D-HUMAN-1): one table of pending
// approvals and questions, addressed by agent. Only the channel bound to an
// agent's root can settle its items, each item settles exactly once, and with
// nobody bound the asker gets unavailable at once.
package human

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/agent"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
)

// ID names one interaction.
type ID string

// Kind is what an interaction asks.
type Kind string

// The kinds.
const (
	KindApproval Kind = "approval"
	KindQuestion Kind = "question"
)

// Channel is a carrier's answering channel: ui, sdk, acp, cli.
type Channel string

// PeerRef names the carrier connection an answer arrived from: one browser
// session, one SDK client, the terminal that started the host. A carrier builds
// it from the peer it admitted, never from request data (DH-ADR-SECURITY-8).
type PeerRef string

// PeerInProcess is the peer of an answer given inside the host process, with no
// carrier connection behind it: a test answerer, or a compiled-in component. An
// actor that names no peer stands for it: the registry hands the actor back as
// the answerer gave it, and an audit record of it names this peer.
const PeerInProcess PeerRef = "in-process"

// Actor is who settled an interaction: the person or client (Name), the channel
// the answer came through, and the peer (connection) on that channel. Each
// answerer sets all three; the registry returns them unchanged to the asker and
// the audit records them (DH-D-HUMAN-1).
type Actor struct {
	Channel Channel
	Name    string
	Peer    PeerRef
}

// Outcome is the closed settlement vocabulary.
type Outcome string

// The outcomes. An approval is answered allowed-once, allowed-session or
// denied; a question is answered. The registry itself settles unavailable
// (nobody bound, an answerer failed or unloaded) and cancelled (the asker left).
const (
	OutcomeAllowedOnce    Outcome = "allowed-once"
	OutcomeAllowedSession Outcome = "allowed-session"
	OutcomeDenied         Outcome = "denied"
	OutcomeAnswered       Outcome = "answered"
	OutcomeUnavailable    Outcome = "unavailable"
	OutcomeCancelled      Outcome = "cancelled"
)

// Answer settles an interaction.
type Answer struct {
	Outcome Outcome
	// Text is a question's answer.
	Text string
	// Detail says why the registry settled it (unbound, answerer-failed, ...).
	Detail string
}

// Audience is who an interaction is for.
type Audience struct{ Agent agent.Ref }

// Item is one interaction.
type Item struct {
	ID       ID
	Kind     Kind
	To       Audience
	Session  session.ID
	Call     string // the tool call it belongs to, "" for none
	Payload  any
	Deadline time.Time
}

// Reason is why an item was withdrawn from an answerer.
type Reason string

// Answerer is a carrier's side: it shows an item and withdraws it once settled
// some other way. Deliver must not block on the answer.
type Answerer interface {
	Deliver(ctx context.Context, it Item) error
	Withdraw(id ID, why Reason)
}

// Binding binds an agent to a channel, or, for a child, Via the agent whose
// answerer it uses (its items go to the root's channel).
type Binding struct {
	Channel Channel
	Via     agent.Ref
}

// Code is a closed registry error code.
type Code string

// The error codes.
const (
	CodeInvalid  Code = "interaction/invalid"
	CodeSettled  Code = "interaction/settled"
	CodeUnknown  Code = "interaction/unknown"
	CodeNotYours Code = "interaction/not-yours"
	CodeTaken    Code = "interaction/taken"
)

// Error is a registry error.
type Error struct {
	Code Code
	Msg  string
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Msg }

// Is matches a sentinel by code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Msg == ""
}

// Sentinels for errors.Is.
var (
	ErrInvalid  = &Error{Code: CodeInvalid}
	ErrSettled  = &Error{Code: CodeSettled}
	ErrUnknown  = &Error{Code: CodeUnknown}
	ErrNotYours = &Error{Code: CodeNotYours}
	ErrTaken    = &Error{Code: CodeTaken}
)

func errorf(c Code, format string, args ...any) error {
	return &Error{Code: c, Msg: fmt.Sprintf(format, args...)}
}

// Registry is the interaction registry.
type Registry interface {
	// RegisterAnswerer registers a as ch's one answerer, an effect of owner's
	// fiber; unloading it settles the items delivered to it unavailable.
	RegisterAnswerer(owner *cordis.Context, ch Channel, a Answerer) (*cordis.EffectHandle, error)
	// Bind binds agent r, an effect of scope's fiber (the agent's scope); one
	// binding per agent; disposing it settles r's items unavailable.
	Bind(scope *cordis.Context, r agent.Ref, b Binding) (*cordis.EffectHandle, error)
	// Ask delivers it to the answerer of its agent's root and blocks until it
	// settles; it settles exactly once. With no binding or answerer it returns
	// unavailable at once without delivery; ctx's end settles it cancelled.
	Ask(ctx context.Context, it Item) (Answer, Actor, error)
	// Answer settles item id by actor by. A settled item gives
	// interaction/settled, an unknown one interaction/unknown, a channel other
	// than the one bound to the item's root interaction/not-yours.
	Answer(ctx context.Context, by Actor, id ID, a Answer) error
	// Pending is r's pending items, in the order they were asked.
	Pending(r agent.Ref) []Item
	// PendingAll is every pending item, in the order they were asked.
	PendingAll() []Item
	// Close settles every pending item unavailable (host-stopping); later asks
	// are unavailable.
	Close()
}

// Key is the registry's service key.
var Key = cordis.NewServiceKey[Registry]("human.interactions")

const (
	pending int32 = iota
	settled
)

type item struct {
	it      Item
	seq     uint64
	channel Channel
	ans     Answerer
	ansGen  uint64
	path    []agent.Ref // the agent, then each Via up to the root

	state  atomic.Int32
	done   chan struct{}
	cancel context.CancelFunc // cancels the context Deliver was given; called when x settles
	res    Answer
	by     Actor
}

type binding struct {
	b   Binding
	gen uint64
}

type answerer struct {
	a   Answerer
	gen uint64
}

type registry struct {
	mu        sync.Mutex
	gen       uint64
	seq       uint64
	closed    bool
	answerers map[Channel]answerer
	bindings  map[agent.Ref]binding
	items     map[ID]*item
	settled   map[ID]struct{} // ids that have settled, for interaction/settled
}

// New is an empty registry.
func New() Registry {
	return &registry{answerers: map[Channel]answerer{}, bindings: map[agent.Ref]binding{}, items: map[ID]*item{}, settled: map[ID]struct{}{}}
}
