package loader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
)

// Separator joins the ids of nested entries into a tree id ("group:child").
const Separator = ":"

// EntryOptions is one entry of the declarative tree, as it appears in a config file.
type EntryOptions struct {
	// ID identifies the entry among its siblings. It must not contain Separator. An
	// entry without one is given a stable generated id.
	ID string `json:"id,omitempty"`
	// Name is resolved to a component by the loader's Resolver. Required unless Group.
	Name string `json:"name,omitempty"`
	// Config is handed to the component as JSON and decoded by its schema.
	Config json.RawMessage `json:"config,omitempty"`
	// Group makes the entry a container whose Children load inside it.
	Group bool `json:"group,omitempty"`
	// Disabled keeps the entry (and, for a group, its children) unloaded.
	Disabled bool `json:"disabled,omitempty"`
	// DisabledExpr, when set, is an expression computing Disabled at each Apply; it
	// takes precedence over Disabled. In a file it is written as the value of
	// "disabled": {"$expr": "<source>"} or "${{ <source> }}".
	DisabledExpr string `json:"-"`
	// Inject adds dependencies on top of the component's own declaration.
	Inject []string `json:"inject,omitempty"`
	// Isolate maps a service name to a realm label. A label is shared, per service, by
	// every entry that uses it; "true" (in a file, also the boolean true) makes a
	// realm private to the entry, and "" is an ordinary shared label.
	Isolate map[string]string `json:"isolate,omitempty"`
	// Intercept layers config on a service for this entry and everything it loads,
	// as cordis.Context.Intercept does; services read it as json.RawMessage.
	Intercept map[string]json.RawMessage `json:"intercept,omitempty"`
	// Children are the entries of a group.
	Children []EntryOptions `json:"children,omitempty"`

	// source is where the entry was decoded from; nil for an entry built in code.
	source *entrySource
	// issues are the faults found while decoding the entry leniently.
	issues []Issue
	// raw is the entry's text when it has decoding issues, so writing it back does
	// not lose what could not be decoded.
	raw json.RawMessage
	// fingerprint is the canonical form of the exported fields as decoded, kept with
	// raw. raw and issues describe the entry only while it still matches; an edit
	// makes them stale (see intact).
	fingerprint string
	// anonymous records that the entry was decoded without an id, so the id the
	// loader generates for it is not taken for an edit.
	anonymous bool
}

// entrySource records where an entry and its values are in a document.
type entrySource struct {
	pos  Position
	path string
	// positions of values by path relative to the entry ("name", "config.port")
	values map[string]Position
}

// Position returns where the entry was decoded from; it is unknown for an entry
// built in code.
func (o EntryOptions) Position() Position {
	if o.source == nil {
		return Position{}
	}
	return o.source.pos
}

// position returns the position and document path of the value at the path relative
// to the entry, falling back to its nearest known parent and then to the entry.
func (o EntryOptions) position(relative string) (Position, string) {
	if o.source == nil {
		return Position{}, ""
	}
	path := joinPath(o.source.path, relative)
	for key := relative; key != ""; key = parentPath(key) {
		if pos, ok := o.source.values[key]; ok {
			return pos, path
		}
	}
	return o.source.pos, path
}

func joinPath(base, relative string) string {
	switch {
	case base == "":
		return relative
	case relative == "":
		return base
	case strings.HasPrefix(relative, "["):
		return base + relative
	default:
		return base + "." + relative
	}
}

func parentPath(path string) string {
	if i := strings.LastIndexAny(path, ".["); i >= 0 {
		return path[:i]
	}
	return ""
}

// Clone returns a deep copy. Decoding issues and the raw text are kept only while
// the entry still matches what was decoded, so an edited entry is revalidated and
// written from its fields.
func (o EntryOptions) Clone() EntryOptions {
	clone := o
	if !o.intact() {
		clone.issues, clone.raw, clone.fingerprint = nil, nil, ""
	}
	clone.Config = slices.Clone(o.Config)
	clone.Inject = slices.Clone(o.Inject)
	if o.Isolate != nil {
		clone.Isolate = maps.Clone(o.Isolate)
	}
	if o.Intercept != nil {
		clone.Intercept = make(map[string]json.RawMessage, len(o.Intercept))
		for name, config := range o.Intercept {
			clone.Intercept[name] = slices.Clone(config)
		}
	}
	clone.Children = cloneAll(o.Children)
	clone.issues = slices.Clone(clone.issues)
	clone.raw = slices.Clone(clone.raw)
	return clone
}

// intact reports whether the entry has decoding issues and raw text that still
// describe it: its exported fields, and its children's, are unchanged since decoding.
func (o EntryOptions) intact() bool {
	if len(o.issues) == 0 || len(o.raw) == 0 || o.fingerprint == "" {
		return false
	}
	canonical, err := o.canonical()
	return err == nil && string(canonical) == o.fingerprint
}

// decodeIssues returns the faults found while decoding the entry, or nil once an
// exported field (or a child's) was changed since: an edited entry is revalidated
// from its fields. Read issues through it, never directly, so a caller that did not
// clone the entry does not report stale faults.
func (o EntryOptions) decodeIssues() []Issue {
	if !o.intact() {
		return nil
	}
	return slices.Clone(o.issues)
}

// canonical encodes the exported fields in file form for comparison with the
// fingerprint. The generated id of an anonymous entry is left out, and an intact
// child stands for its raw text, as it is written.
func (o EntryOptions) canonical() ([]byte, error) {
	id := o.ID
	if o.anonymous {
		id = ""
	}
	var children []json.RawMessage
	for _, child := range o.Children {
		if child.intact() {
			children = append(children, child.raw)
			continue
		}
		encoded, err := child.canonical()
		if err != nil {
			return nil, err
		}
		children = append(children, encoded)
	}
	var list any
	if len(children) > 0 {
		list = children
	}
	return o.fileForm(id, list)
}

// sameShape reports whether a live fiber for o can be kept for other: everything but
// the id, config and children matches.
func (o EntryOptions) sameShape(other EntryOptions) bool {
	return o.Name == other.Name &&
		o.Group == other.Group &&
		o.Disabled == other.Disabled &&
		o.DisabledExpr == other.DisabledExpr &&
		slices.Equal(o.Inject, other.Inject) &&
		maps.Equal(o.Isolate, other.Isolate) &&
		maps.EqualFunc(o.Intercept, other.Intercept, func(a, b json.RawMessage) bool { return jsonEqual(a, b) })
}

// entryJSON is the file form of an entry.
type entryJSON struct {
	ID        string                     `json:"id,omitempty"`
	Name      string                     `json:"name,omitempty"`
	Config    json.RawMessage            `json:"config,omitempty"`
	Group     bool                       `json:"group,omitempty"`
	Disabled  any                        `json:"disabled,omitempty"`
	Inject    []string                   `json:"inject,omitempty"`
	Isolate   map[string]any             `json:"isolate,omitempty"`
	Intercept map[string]json.RawMessage `json:"intercept,omitempty"`
	Children  any                        `json:"children,omitempty"`
}

// MarshalJSON writes the entry in its file form. An entry that had decoding issues is
// written as it was read, unless it was changed since.
func (o EntryOptions) MarshalJSON() ([]byte, error) {
	if o.intact() {
		return slices.Clone(o.raw), nil
	}
	var children any
	if len(o.Children) > 0 {
		children = o.Children
	}
	return o.fileForm(o.ID, children)
}

// fileForm encodes the exported fields with the given id and children.
func (o EntryOptions) fileForm(id string, children any) ([]byte, error) {
	out := entryJSON{ID: id, Name: o.Name, Config: o.Config, Group: o.Group, Inject: o.Inject, Intercept: o.Intercept, Children: children}
	switch {
	case o.DisabledExpr != "":
		out.Disabled = map[string]string{exprKey: o.DisabledExpr}
	case o.Disabled:
		out.Disabled = true
	}
	if len(o.Isolate) > 0 {
		out.Isolate = make(map[string]any, len(o.Isolate))
		for name, realm := range o.Isolate {
			if realm == "true" {
				out.Isolate[name] = true
			} else {
				out.Isolate[name] = realm
			}
		}
	}
	return json.Marshal(out)
}

// UnmarshalJSON reads an entry in its file form, and fails with a *ParseError that
// lists every fault of the entry.
func (o *EntryOptions) UnmarshalJSON(data []byte) error {
	node, err := JSON.Decode("", data)
	if err != nil {
		return err
	}
	decoded := decodeEntry(node, "")
	if issues := (&Document{Entries: []EntryOptions{decoded}}).Issues(); len(issues) > 0 {
		return &ParseError{Issues: issues}
	}
	entries := []EntryOptions{decoded}
	stripSources(entries)
	*o = entries[0]
	return nil
}

func stripSources(entries []EntryOptions) {
	for i := range entries {
		entries[i].source = nil
		stripSources(entries[i].Children)
	}
}

// decodeEntries builds entries from array items leniently: each fault becomes an
// issue on its entry, with the position of the value at fault.
func decodeEntries(items []*Node, path string) []EntryOptions {
	if len(items) == 0 {
		return nil
	}
	entries := make([]EntryOptions, len(items))
	for i, item := range items {
		entries[i] = decodeEntry(item, fmt.Sprintf("%s[%d]", path, i))
	}
	return entries
}

func decodeEntry(node *Node, path string) EntryOptions {
	var o EntryOptions
	o.source = &entrySource{pos: node.Pos, path: path, values: map[string]Position{}}
	fault := func(at *Node, relative, message string) {
		o.issues = append(o.issues, Issue{Pos: at.Pos, Path: joinPath(path, relative), Message: message})
	}
	wrongType := func(at *Node, relative, want string) {
		fault(at, relative, fmt.Sprintf("must be %s, got %s", want, at.Kind))
	}
	if node.Kind != ObjectNode {
		wrongType(node, "", "an object")
		o.anonymous = true
		o.raw = node.JSON()
		o.fingerprint = o.decodedFingerprint()
		return o
	}
	seen := map[string]bool{}
	for _, field := range node.Fields {
		key, value := field.Key, field.Value
		if seen[key] {
			fault(value, key, "duplicate key")
			continue
		}
		seen[key] = true
		o.source.values[key] = value.Pos
		switch key {
		case "id", "name":
			if value.Kind != StringNode {
				wrongType(value, key, "a string")
			} else if key == "id" {
				o.ID = value.Text
			} else {
				o.Name = value.Text
			}
		case "config":
			if value.Kind != NullNode {
				o.Config = value.JSON()
			}
			recordPositions(value, key, o.source.values)
		case "group":
			if value.Kind != BoolNode {
				wrongType(value, key, "a boolean")
			} else {
				o.Group = value.Bool
			}
		case "disabled":
			switch value.Kind {
			case BoolNode:
				o.Disabled = value.Bool
			case ExprNode:
				if strings.TrimSpace(value.Text) == "" {
					// an empty source is not "no expression": never let it enable the entry
					fault(value, key, "expression must not be empty")
				} else {
					o.DisabledExpr = value.Text
				}
			default:
				wrongType(value, key, "a boolean or an expression")
			}
		case "inject":
			if value.Kind != ArrayNode {
				wrongType(value, key, "an array of service names")
				break
			}
			o.Inject = []string{}
			for i, item := range value.Items {
				if item.Kind != StringNode {
					wrongType(item, fmt.Sprintf("inject[%d]", i), "a service name")
				} else {
					o.Inject = append(o.Inject, item.Text)
				}
			}
		case "isolate":
			if value.Kind != ObjectNode {
				wrongType(value, key, "an object of realm labels")
				break
			}
			o.Isolate = map[string]string{}
			for _, member := range value.Fields {
				switch {
				case member.Value.Kind == StringNode:
					o.Isolate[member.Key] = member.Value.Text
				case member.Value.Kind == BoolNode && member.Value.Bool:
					o.Isolate[member.Key] = "true"
				default:
					wrongType(member.Value, "isolate."+member.Key, "a realm label or true")
				}
			}
		case "intercept":
			if value.Kind != ObjectNode {
				wrongType(value, key, "an object of service configs")
				break
			}
			o.Intercept = map[string]json.RawMessage{}
			for _, member := range value.Fields {
				o.Intercept[member.Key] = member.Value.JSON()
			}
		case "children":
			if value.Kind != ArrayNode {
				wrongType(value, key, "an array of entries")
				break
			}
			o.Children = decodeEntries(value.Items, joinPath(path, key))
		default:
			o.issues = append(o.issues, Issue{Pos: field.KeyPos, Path: joinPath(path, key), Message: "unknown key"})
		}
	}
	o.anonymous = o.ID == ""
	if len(o.issues) > 0 {
		o.raw = node.JSON()
		o.fingerprint = o.decodedFingerprint()
	}
	return o
}

// decodedFingerprint is the canonical form of a freshly decoded entry; "" (never
// intact) if it cannot be encoded.
func (o EntryOptions) decodedFingerprint() string {
	canonical, err := o.canonical()
	if err != nil {
		return ""
	}
	return string(canonical)
}

// recordPositions records the position of node and of every value inside it.
func recordPositions(node *Node, path string, positions map[string]Position) {
	positions[path] = node.Pos
	switch node.Kind {
	case ObjectNode:
		for _, field := range node.Fields {
			recordPositions(field.Value, joinPath(path, field.Key), positions)
		}
	case ArrayNode:
		for i, item := range node.Items {
			recordPositions(item, fmt.Sprintf("%s[%d]", path, i), positions)
		}
	}
}

// EntryError reports every fault of one entry.
type EntryError struct {
	ID     string
	Issues []Issue
}

func (e *EntryError) Error() string {
	if len(e.Issues) == 1 {
		return "loader: " + e.Issues[0].Error()
	}
	texts := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		texts[i] = "\t" + issue.Error()
	}
	return fmt.Sprintf("loader: entry %q has %d issues:\n%s", e.ID, len(e.Issues), strings.Join(texts, "\n"))
}

func (o EntryOptions) withoutChildren() EntryOptions {
	clone := o.Clone()
	clone.Children = nil
	return clone
}

func cloneAll(entries []EntryOptions) []EntryOptions {
	if entries == nil {
		return nil
	}
	clones := make([]EntryOptions, len(entries))
	for i, entry := range entries {
		clones[i] = entry.Clone()
	}
	return clones
}

// normalize drops a JSON null config, recursively.
func normalize(entries []EntryOptions) {
	for i := range entries {
		if bytes.Equal(bytes.TrimSpace(entries[i].Config), []byte("null")) {
			entries[i].Config = nil
		}
		normalize(entries[i].Children)
	}
}

func jsonEqual(left, right json.RawMessage) bool {
	if len(left) == 0 || len(right) == 0 {
		return len(left) == 0 && len(right) == 0
	}
	var a, b bytes.Buffer
	if json.Compact(&a, left) != nil || json.Compact(&b, right) != nil {
		return bytes.Equal(left, right)
	}
	return bytes.Equal(a.Bytes(), b.Bytes())
}

func compose(parentID, id string) string {
	if parentID == "" {
		return id
	}
	return parentID + Separator + id
}

// patch replaces the entry at the tree id with replacement, keeping its children.
func patch(entries []EntryOptions, id string, replacement EntryOptions) bool {
	segments := strings.Split(id, Separator)
	cursor := entries
	for i, segment := range segments {
		index := slices.IndexFunc(cursor, func(o EntryOptions) bool { return o.ID == segment })
		if index < 0 {
			return false
		}
		if i == len(segments)-1 {
			replacement.Children = cursor[index].Children
			cursor[index] = replacement
			return true
		}
		cursor = cursor[index].Children
	}
	return false
}

// Entry is the loader's record of one entry of the tree.
type Entry struct {
	id       string
	parentID string

	// guarded by Service.mu
	options EntryOptions
	fiber   *cordis.Fiber
	scope   *cordis.Context
	order   int
	applied json.RawMessage
	// labels of the shared realm keys the live fiber holds
	realms []string
}

// ID returns the entry's tree id.
func (e *Entry) ID() string { return e.id }

// ParentID returns the tree id of the enclosing group, or "" for a top-level entry.
func (e *Entry) ParentID() string { return e.parentID }

func (e *Entry) String() string { return "Entry " + e.id }
