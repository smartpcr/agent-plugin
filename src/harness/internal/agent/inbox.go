package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
)

// List is one of the inbox's two queues: next-step input is taken at the next
// step boundary of the running turn, next-turn input opens a turn of its own.
type List string

// The lists.
const (
	ListNextStep List = "next-step"
	ListNextTurn List = "next-turn"
)

func (l List) valid() bool { return l == ListNextStep || l == ListNextTurn }

// The source kinds the core knows; others are open. SourceUser is set only by a
// carrier's entry point, never by a tool or a listener.
const (
	SourceUser     = "user"
	SourceQuestion = "question-answer"
	SourceCommand  = "command"
)

// InboxMessage is one input waiting in the inbox.
type InboxMessage struct {
	// ID is unique over the session's whole history; Send mints one when "".
	ID string `json:"id"`
	// Source is the kind of producer: user, question-answer, command, ...
	Source string `json:"source"`
	// By names who sent it: the principal of a carrier, a question's answerer.
	By string `json:"by,omitempty"`
	// Call is the tool call the message answers, "" for none.
	Call string `json:"call,omitempty"`
	Text string `json:"text"`
}

// Message is m as the user message a claim appends.
func (m InboxMessage) Message() (vocab.Message, error) {
	src := vocab.Source{Kind: m.Source, Producers: []string{orElse(m.By, m.Source)}}
	if m.Call != "" {
		data, _ := json.Marshal(map[string]string{"call": m.Call})
		src.Data = data
	}
	return vocab.RestoreMessage(m.ID, vocab.RoleUser, &src, vocab.TextBlock(m.Text))
}

func orElse(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// SpliceOutcome is how messages left the inbox: claimed by a turn or discarded.
type SpliceOutcome struct {
	Kind  string `json:"kind"` // claimed | discarded
	Turn  int    `json:"turn,omitempty"`
	Cause string `json:"cause,omitempty"`
}

// The outcome kinds.
const (
	OutcomeClaimed   = "claimed"
	OutcomeDiscarded = "discarded"
)

// Splice is agent/inbox/spliced: at Position of List, Removed items left (with
// Outcome) and Inserted items arrived. A send inserts at the end; a claim or a
// discard removes.
type Splice struct {
	List     List           `json:"list"`
	Position int            `json:"position"`
	Removed  int            `json:"removed,omitempty"`
	Inserted []InboxMessage `json:"inserted,omitempty"`
	Outcome  *SpliceOutcome `json:"outcome,omitempty"`
}

func validSplice(s Splice) error {
	switch {
	case !s.List.valid():
		return fmt.Errorf("agent/inbox/spliced: list %q is not next-step or next-turn", s.List)
	case s.Position < 0 || s.Removed < 0:
		return errors.New("agent/inbox/spliced: position and removed are not negative")
	case s.Removed == 0 && len(s.Inserted) == 0:
		return errors.New("agent/inbox/spliced: a splice removes or inserts")
	case s.Removed > 0 && (s.Outcome == nil || (s.Outcome.Kind != OutcomeClaimed && s.Outcome.Kind != OutcomeDiscarded)):
		return errors.New("agent/inbox/spliced: a removal is claimed or discarded")
	case s.Removed > 0 && s.Outcome.Kind == OutcomeClaimed && s.Outcome.Turn <= 0:
		return errors.New("agent/inbox/spliced: a claim names its turn")
	}
	for _, m := range s.Inserted {
		if m.ID == "" || strings.TrimSpace(m.ID) != m.ID || m.Source == "" {
			return errors.New("agent/inbox/spliced: an inserted message has an id and a source")
		}
	}
	return nil
}

// SplicedType is the inbox's one log-only, state-carrying event type.
var SplicedType = event.MustDefine(event.Default(), event.Spec[Splice]{Name: "agent/inbox/spliced", Version: 1, Validate: validSplice})

// InboxLog is the session the inbox lives in.
type InboxLog interface {
	Append(ctx context.Context, d event.Draft) (event.Receipt, error)
	AppendGroup(ctx context.Context, ds []event.Draft) ([]event.Receipt, error)
	Flush(ctx context.Context) (event.Offset, error)
	Len() event.Offset
	Read(ctx context.Context, from event.Offset, limit int, opts ...session.ReadOption) ([]event.Event, error)
}

// Receipt proves admission: the message id and the seq of its splice. It is not
// an answer.
type Receipt struct {
	MessageID string
	Seq       event.Seq
}

// The inbox error codes.
const (
	CodeDuplicateMessage Code = "agent/duplicate-message"
	CodeInboxInvalid     Code = "agent/invalid-message"
)

// DuplicateError is a Send whose id the session has admitted before; First is
// that admission's receipt.
type DuplicateError struct{ First Receipt }

func (e *DuplicateError) Error() string {
	return fmt.Sprintf("%s: message %q was admitted at seq %d", CodeDuplicateMessage, e.First.MessageID, e.First.Seq)
}

// Is matches ErrDuplicateMessage.
func (e *DuplicateError) Is(target error) bool { return target == ErrDuplicateMessage }

// ErrDuplicateMessage matches a refused duplicate send with errors.Is.
var ErrDuplicateMessage = &Error{Code: CodeDuplicateMessage}

// Inbox is an agent's logged input queue (DH-D-AGENT-2). Send is accepted at any
// time and is durable before it returns; items are taken only at a turn
// boundary (ClaimTurn) or a step boundary (ClaimStep); every change is one
// agent/inbox/spliced event, and the state is the fold of those events, so a
// claimed item is never claimed again, also after a crash and a resume.
type Inbox struct {
	log  InboxLog
	wake chan struct{}

	mu      sync.Mutex
	pending map[List][]InboxMessage
	// seen is the consumedWork fold: every id ever admitted, whatever became
	// of it, with its first receipt.
	seen map[string]Receipt
}

// OpenInbox folds log's splices into a fresh inbox. A resumed agent starts idle:
// restored input stays parked until a wake.
func OpenInbox(ctx context.Context, log InboxLog) (*Inbox, error) {
	if log == nil {
		return nil, errorf(CodeInboxInvalid, "an inbox needs its session")
	}
	b := &Inbox{log: log, wake: make(chan struct{}, 1), pending: map[List][]InboxMessage{}, seen: map[string]Receipt{}}
	const page = 256
	for from, n := event.Offset(0), log.Len(); from < n; from += page {
		evs, err := log.Read(ctx, from, page)
		if err != nil {
			return nil, fmt.Errorf("agent: fold the inbox: %w", err)
		}
		for _, ev := range evs {
			if ev.Type() != SplicedType.Name() {
				continue
			}
			s, err := SplicedType.As(ev)
			if err != nil {
				return nil, fmt.Errorf("agent: fold the inbox at seq %d: %w", ev.Seq(), err)
			}
			b.apply(s, ev.Seq())
		}
	}
	return b, nil
}

// apply folds one splice; the caller holds mu or owns b.
func (b *Inbox) apply(s Splice, at event.Seq) {
	l := b.pending[s.List]
	pos := min(s.Position, len(l))
	end := min(pos+s.Removed, len(l))
	l = slices.Concat(l[:pos], s.Inserted, l[end:])
	b.pending[s.List] = l
	for _, m := range s.Inserted {
		if _, ok := b.seen[m.ID]; !ok {
			b.seen[m.ID] = Receipt{MessageID: m.ID, Seq: at}
		}
	}
}

// Wake signals when next-turn input arrives; the driver selects on it.
func (b *Inbox) Wake() <-chan struct{} { return b.wake }

// Pending is a copy of list's items in order.
func (b *Inbox) Pending(list List) []InboxMessage {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.pending[list])
}

// Seen is the first receipt of id, if the session ever admitted it.
func (b *Inbox) Seen(id string) (Receipt, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r, ok := b.seen[id]
	return r, ok
}

// Send admits m to list and returns once its splice is durable. An id the
// session admitted before, pending or long since claimed, appends nothing and
// returns a *DuplicateError carrying the first receipt. Send opens no turn: a
// next-turn send signals Wake.
func (b *Inbox) Send(ctx context.Context, list List, m InboxMessage) (Receipt, error) {
	return b.SendWith(ctx, list, m)
}

// SendWith is Send whose splice and also are one group: all of them are in the
// log or none is, so a producer's own record of the send (an answer's
// question/answered) can never be cut from it by a crash.
func (b *Inbox) SendWith(ctx context.Context, list List, m InboxMessage, also ...event.Draft) (Receipt, error) {
	if !list.valid() {
		return Receipt{}, errorf(CodeInboxInvalid, "list %q", list)
	}
	m, err := b.admissible(m)
	if err != nil {
		return Receipt{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if first, ok := b.seen[m.ID]; ok {
		return first, &DuplicateError{First: first}
	}
	s := Splice{List: list, Position: len(b.pending[list]), Inserted: []InboxMessage{m}}
	r, err := b.groupLocked(ctx, "send", []Splice{s}, also)
	if err != nil {
		return r, err
	}
	if list == ListNextTurn {
		select {
		case b.wake <- struct{}{}:
		default:
		}
	}
	return Receipt{MessageID: m.ID, Seq: r.Seq}, nil
}

// Hand admits m and hands it straight to a caller waiting for it: the admission
// on next-step, its discard with cause and also are one group. The message is in
// the session's history of input (its id is taken) and never runs as a turn.
func (b *Inbox) Hand(ctx context.Context, m InboxMessage, cause string, also ...event.Draft) (Receipt, error) {
	m, err := b.admissible(m)
	if err != nil {
		return Receipt{}, err
	}
	if cause == "" {
		cause = "discarded"
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if first, ok := b.seen[m.ID]; ok {
		return first, &DuplicateError{First: first}
	}
	at := len(b.pending[ListNextStep])
	in := Splice{List: ListNextStep, Position: at, Inserted: []InboxMessage{m}}
	out := Splice{List: ListNextStep, Position: at, Removed: 1, Outcome: &SpliceOutcome{Kind: OutcomeDiscarded, Cause: cause}}
	r, err := b.groupLocked(ctx, "hand", []Splice{in, out}, also)
	if err != nil {
		return r, err
	}
	return Receipt{MessageID: m.ID, Seq: r.Seq}, nil
}

func (b *Inbox) admissible(m InboxMessage) (InboxMessage, error) {
	if m.ID == "" {
		m.ID = mintMessageID()
	}
	if m.Source == "" {
		return m, errorf(CodeInboxInvalid, "message %q names no source", m.ID)
	}
	return m, nil
}

// groupLocked appends splices then also as one group, folds the splices and
// flushes; the receipt is the first splice's. A failed append folds nothing; a
// failed flush leaves the group appended and folded (the caller can tell by Seen).
func (b *Inbox) groupLocked(ctx context.Context, op string, splices []Splice, also []event.Draft) (Receipt, error) {
	group := make([]event.Draft, 0, len(splices)+len(also))
	for _, s := range splices {
		d, err := SplicedType.Draft(s)
		if err != nil {
			return Receipt{}, errorf(CodeInboxInvalid, "%v", err)
		}
		group = append(group, d)
	}
	group = append(group, also...)
	var rs []event.Receipt
	var err error
	if len(group) == 1 {
		var r event.Receipt
		r, err = b.log.Append(ctx, group[0])
		rs = []event.Receipt{r}
	} else {
		rs, err = b.log.AppendGroup(ctx, group)
	}
	if err != nil {
		return Receipt{}, fmt.Errorf("agent: %s: %w", op, err)
	}
	for i, s := range splices {
		b.apply(s, rs[i].Seq)
	}
	r := Receipt{Seq: rs[0].Seq}
	if _, err := b.log.Flush(ctx); err != nil {
		return r, fmt.Errorf("agent: %s: flush: %w", op, err)
	}
	return r, nil
}

// ClaimTurn opens turn with the first next-turn item: turn/start, the claim and
// the item's user/message are one group, durable before it returns. With nothing
// pending it appends nothing and returns false: a wake that finds nothing opens
// no turn.
func (b *Inbox) ClaimTurn(ctx context.Context, turn int) (InboxMessage, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := b.pending[ListNextTurn]
	if len(l) == 0 {
		return InboxMessage{}, false, nil
	}
	m := l[0]
	start, err := event.TurnStartType.Draft(event.TurnStart{Turn: turn})
	if err != nil {
		return InboxMessage{}, false, err
	}
	if err := b.claimLocked(ctx, ListNextTurn, 1, turn, start); err != nil {
		return InboxMessage{}, false, err
	}
	return m, true, nil
}

// ClaimStep takes every next-step item into the open turn at a step boundary:
// one group of the claim and the items' user messages, durable before it
// returns. With nothing pending it appends nothing.
func (b *Inbox) ClaimStep(ctx context.Context, turn int) ([]InboxMessage, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	l := slices.Clone(b.pending[ListNextStep])
	if len(l) == 0 {
		return nil, nil
	}
	if err := b.claimLocked(ctx, ListNextStep, len(l), turn); err != nil {
		return nil, err
	}
	return l, nil
}

func (b *Inbox) claimLocked(ctx context.Context, list List, n, turn int, lead ...event.Draft) error {
	s := Splice{List: list, Position: 0, Removed: n, Outcome: &SpliceOutcome{Kind: OutcomeClaimed, Turn: turn}}
	d, err := SplicedType.Draft(s)
	if err != nil {
		return err
	}
	group := append(slices.Clone(lead), d)
	for _, m := range b.pending[list][:n] {
		msg, err := m.Message()
		if err != nil {
			return fmt.Errorf("agent: claim %q: %w", m.ID, err)
		}
		um, err := event.UserMessage.Draft(event.Append(), event.UserMessagePayload{Message: msg})
		if err != nil {
			return fmt.Errorf("agent: claim %q: %w", m.ID, err)
		}
		group = append(group, um)
	}
	rs, err := b.log.AppendGroup(ctx, group)
	if err != nil {
		return fmt.Errorf("agent: claim: %w", err)
	}
	b.apply(s, rs[len(lead)].Seq)
	if _, err := b.log.Flush(ctx); err != nil {
		return fmt.Errorf("agent: claim: flush: %w", err)
	}
	return nil
}

// Discard removes the pending item id from list with cause, logged; it never
// runs. False when id is not pending there.
func (b *Inbox) Discard(ctx context.Context, list List, id, cause string) (bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	i := slices.IndexFunc(b.pending[list], func(m InboxMessage) bool { return m.ID == id })
	if i < 0 {
		return false, nil
	}
	if cause == "" {
		cause = "discarded"
	}
	s := Splice{List: list, Position: i, Removed: 1, Outcome: &SpliceOutcome{Kind: OutcomeDiscarded, Cause: cause}}
	d, err := SplicedType.Draft(s)
	if err != nil {
		return false, err
	}
	r, err := b.log.Append(ctx, d)
	if err != nil {
		return false, fmt.Errorf("agent: discard: %w", err)
	}
	b.apply(s, r.Seq)
	if _, err := b.log.Flush(ctx); err != nil {
		return false, fmt.Errorf("agent: discard: flush: %w", err)
	}
	return true, nil
}

func mintMessageID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "in_" + hex.EncodeToString(b[:])
}
