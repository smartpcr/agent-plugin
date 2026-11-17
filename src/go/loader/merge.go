package loader

import (
	"bytes"
	"encoding/json"
)

// node is one entry of a flattened tree, for merging.
type node struct {
	options  EntryOptions
	parentID string
	shape    string
}

// shape is the entry's content without its id and children: what a merge compares.
func shape(options EntryOptions) string {
	copy := options.Clone()
	copy.ID = ""
	copy.Children = nil
	encoded, _ := json.Marshal(copy)
	return string(encoded)
}

// flattenTree indexes a tree by tree id; the slice keeps tree order.
func flattenTree(entries []EntryOptions) (map[string]node, []string) {
	nodes := map[string]node{}
	var order []string
	var visit func(level []EntryOptions, parentID string)
	visit = func(level []EntryOptions, parentID string) {
		for _, options := range level {
			id := compose(parentID, options.ID)
			if _, seen := nodes[id]; !seen {
				nodes[id] = node{options: options, parentID: parentID, shape: shape(options)}
				order = append(order, id)
			}
			if len(options.Children) > 0 {
				visit(options.Children, id)
			}
		}
	}
	visit(entries, "")
	return nodes, order
}

// assignStableIDs gives each anonymous entry the id of an entry of previous with the
// same parent and shape that is not taken, or a new id, so an anonymous entry keeps
// its identity across reloads.
func assignStableIDs(entries []EntryOptions, parentID string, previous []EntryOptions) {
	nodes, order := flattenTree(previous)
	taken := map[string]bool{}
	for _, options := range entries {
		if options.ID != "" {
			taken[options.ID] = true
		}
	}
	for i := range entries {
		options := &entries[i]
		if options.ID == "" {
			target := shape(*options)
			for _, id := range order {
				candidate := nodes[id]
				if candidate.parentID == parentID && !taken[candidate.options.ID] && candidate.shape == target {
					options.ID = candidate.options.ID
					break
				}
			}
			if options.ID == "" {
				options.ID = newID()
			}
			taken[options.ID] = true
		}
		if len(options.Children) > 0 {
			assignStableIDs(options.Children, compose(parentID, options.ID), previous)
		}
	}
}

// merge is a three-way merge per entry of the file's version (theirs) and the
// runtime's (ours) against the last version synced with the file (base). An entry
// changed on one side takes that side; changed on both sides differently, the file
// wins and the id is reported as a conflict.
func merge(base, ours, theirs []EntryOptions) (merged []EntryOptions, conflicts []string) {
	b, _ := flattenTree(base)
	o, ourOrder := flattenTree(ours)
	t, theirOrder := flattenTree(theirs)

	ids := append([]string{}, theirOrder...)
	for _, id := range ourOrder {
		if _, ok := t[id]; !ok {
			ids = append(ids, id)
		}
	}

	chosen := map[string]node{}
	for _, id := range ids {
		baseShape, ourShape, theirShape := b[id].shape, o[id].shape, t[id].shape
		var pick node
		var ok bool
		switch {
		case ourShape == baseShape:
			pick, ok = t[id]
		case theirShape == baseShape || ourShape == theirShape:
			pick, ok = o[id]
		default:
			conflicts = append(conflicts, id)
			pick, ok = t[id]
		}
		if ok {
			chosen[id] = pick
		}
	}

	type built struct {
		options  EntryOptions
		children []*built
	}
	var roots []*built
	index := map[string]*built{}
	for _, id := range ids {
		pick, ok := chosen[id]
		if !ok {
			continue
		}
		item := &built{options: pick.options.withoutChildren()}
		if pick.parentID == "" {
			roots = append(roots, item)
		} else if parent, ok := index[pick.parentID]; ok {
			parent.children = append(parent.children, item)
		} else {
			continue // the enclosing group was removed
		}
		index[id] = item
	}

	var materialise func(items []*built) []EntryOptions
	materialise = func(items []*built) []EntryOptions {
		if len(items) == 0 {
			return nil
		}
		result := make([]EntryOptions, len(items))
		for i, item := range items {
			result[i] = item.options
			result[i].Children = materialise(item.children)
		}
		return result
	}
	merged = materialise(roots)
	if merged == nil {
		merged = []EntryOptions{}
	}
	return merged, conflicts
}

// parse reads a JSON array of entries; ok is false for anything else.
func parse(text string) ([]EntryOptions, bool) {
	trimmed := bytes.TrimSpace([]byte(text))
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, false
	}
	var entries []EntryOptions
	if err := json.Unmarshal(trimmed, &entries); err != nil {
		return nil, false
	}
	if entries == nil {
		entries = []EntryOptions{}
	}
	normalize(entries)
	return entries, true
}

func serialize(entries []EntryOptions) string {
	if entries == nil {
		entries = []EntryOptions{}
	}
	encoded, _ := json.MarshalIndent(entries, "", "  ")
	return string(encoded) + "\n"
}
