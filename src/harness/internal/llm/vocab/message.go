package vocab

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Role is who a message is from; the set is closed.
type Role string

// The roles.
const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

var roles = []Role{RoleSystem, RoleDeveloper, RoleUser, RoleAssistant, RoleTool}

// Source is the one open member of the vocabulary: an open kind, every
// producer that contributed (a merged message names each), and a payload its
// producer owns. A reader that does not know a kind treats the message as
// plain content.
type Source struct {
	Kind      string          `json:"kind"`
	Producers []string        `json:"producers"`
	Data      json.RawMessage `json:"data,omitempty"`
}

func (s Source) clone() Source {
	return Source{Kind: s.Kind, Producers: slices.Clone(s.Producers), Data: bytes.Clone(s.Data)}
}

// Validate checks that the source names its kind and at least one producer.
func (s Source) Validate() error {
	if s.Kind == "" || strings.TrimSpace(s.Kind) != s.Kind {
		return fmt.Errorf("vocab: source kind %q is blank or padded", s.Kind)
	}
	if len(s.Producers) == 0 || slices.Contains(s.Producers, "") {
		return errors.New("vocab: a source names every producer, none blank")
	}
	if len(s.Data) > 0 && !json.Valid(s.Data) {
		return errors.New("vocab: source data is not JSON")
	}
	return nil
}

// Message is one message of a conversation. Its fields are reachable only
// through accessors that return copies, so a message cannot change once built.
type Message struct {
	id     string
	role   Role
	blocks []Block
	source *Source
}

// NewMessage builds a message with a fresh id, copying the blocks.
func NewMessage(role Role, blocks ...Block) (Message, error) {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails
	return RestoreMessage("msg_"+hex.EncodeToString(b[:]), role, nil, blocks...)
}

// RestoreMessage rebuilds a message with a known id, as when replaying a log;
// it copies its arguments and validates the result.
func RestoreMessage(id string, role Role, source *Source, blocks ...Block) (Message, error) {
	m := Message{id: id, role: role}
	for _, b := range blocks {
		m.blocks = append(m.blocks, b.Clone())
	}
	if source != nil {
		s := source.clone()
		m.source = &s
	}
	if err := m.Validate(); err != nil {
		return Message{}, err
	}
	return m, nil
}

// WithSource is a copy of m carrying source.
func (m Message) WithSource(s Source) (Message, error) {
	return RestoreMessage(m.id, m.role, &s, m.blocks...)
}

// ID is the message id.
func (m Message) ID() string { return m.id }

// Role is who the message is from.
func (m Message) Role() Role { return m.role }

// Blocks is a copy of the message's blocks.
func (m Message) Blocks() []Block {
	out := make([]Block, len(m.blocks))
	for i, b := range m.blocks {
		out[i] = b.Clone()
	}
	return out
}

// Source is a copy of the message's source, false when it has none.
func (m Message) Source() (Source, bool) {
	if m.source == nil {
		return Source{}, false
	}
	return m.source.clone(), true
}

// IsZero reports whether m is the zero message, which no constructor returns.
func (m Message) IsZero() bool { return m.id == "" }

// Validate checks the id, the role, each block, and which kinds a role may
// carry: tool results come only from the tool role, tool calls and reasoning
// only from the assistant.
func (m Message) Validate() error {
	if m.id == "" || strings.TrimSpace(m.id) != m.id {
		return fmt.Errorf("vocab: message id %q is blank or padded", m.id)
	}
	if !slices.Contains(roles, m.role) {
		return fmt.Errorf("vocab: role %q is not one of the closed set", m.role)
	}
	for i, b := range m.blocks {
		if err := b.Validate(); err != nil {
			return fmt.Errorf("message %s block %d: %w", m.id, i, err)
		}
		switch {
		case b.Kind == KindToolResult && m.role != RoleTool:
			return fmt.Errorf("vocab: message %s: a tool result needs the tool role, not %s", m.id, m.role)
		case m.role == RoleTool && b.Kind != KindToolResult:
			return fmt.Errorf("vocab: message %s: a tool message holds only tool results", m.id)
		case (b.Kind == KindToolUse || b.Kind == KindReasoning) && m.role != RoleAssistant:
			return fmt.Errorf("vocab: message %s: a %s block needs the assistant role", m.id, b.Kind)
		}
	}
	if m.source != nil {
		return m.source.Validate()
	}
	return nil
}

// messageJSON is the persisted shape of a Message; its fingerprint is the
// message's (testdata/fingerprints.golden).
type messageJSON struct {
	ID     string  `json:"id"`
	Role   Role    `json:"role" dsh:"enum=system|developer|user|assistant|tool"`
	Blocks []Block `json:"blocks"`
	Source *Source `json:"source,omitempty"`
}

// MarshalJSON encodes the persisted shape.
func (m Message) MarshalJSON() ([]byte, error) {
	if m.IsZero() {
		return nil, errors.New("vocab: the zero message has no encoding")
	}
	blocks := m.blocks
	if blocks == nil {
		blocks = []Block{}
	}
	return json.Marshal(messageJSON{ID: m.id, Role: m.role, Blocks: blocks, Source: m.source})
}

// UnmarshalJSON decodes strictly and validates.
func (m *Message) UnmarshalJSON(data []byte) error {
	var w messageJSON
	if err := strictUnmarshal(data, &w); err != nil {
		return err
	}
	if w.Blocks == nil {
		return errors.New("vocab: a message needs its blocks list")
	}
	out, err := RestoreMessage(w.ID, w.Role, w.Source, w.Blocks...)
	if err != nil {
		return err
	}
	*m = out
	return nil
}
