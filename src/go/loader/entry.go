package loader

import (
	"bytes"
	"encoding/json"
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
	// Inject adds dependencies on top of the component's own declaration.
	Inject []string `json:"inject,omitempty"`
	// Isolate maps a service name to a realm label. A label is shared, per service, by
	// every entry that uses it; "true" makes a realm private to the entry, and "" is an
	// ordinary shared label.
	Isolate map[string]string `json:"isolate,omitempty"`
	// Children are the entries of a group.
	Children []EntryOptions `json:"children,omitempty"`
}

// Clone returns a deep copy.
func (o EntryOptions) Clone() EntryOptions {
	clone := o
	clone.Config = slices.Clone(o.Config)
	clone.Inject = slices.Clone(o.Inject)
	if o.Isolate != nil {
		clone.Isolate = maps.Clone(o.Isolate)
	}
	clone.Children = cloneAll(o.Children)
	return clone
}

// sameShape reports whether a live fiber for o can be kept for other: everything but
// the id, config and children matches.
func (o EntryOptions) sameShape(other EntryOptions) bool {
	return o.Name == other.Name &&
		o.Group == other.Group &&
		o.Disabled == other.Disabled &&
		slices.Equal(o.Inject, other.Inject) &&
		maps.Equal(o.Isolate, other.Isolate)
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
}

// ID returns the entry's tree id.
func (e *Entry) ID() string { return e.id }

// ParentID returns the tree id of the enclosing group, or "" for a top-level entry.
func (e *Entry) ParentID() string { return e.parentID }

func (e *Entry) String() string { return "Entry " + e.id }
