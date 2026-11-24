package profile

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// SafeOps returns the operations safe mode adds to the launcher layer
// (DH-ADR-BOOT-12): `disabled: true` on the PluginsGroup and on the group of every
// optional bundle among layers. Each is added only when the layers compose that id
// as a group, so the switch never adds an operation that matches nothing. Nothing
// else is touched: no other entry, no other field, no layer file, and nothing is
// removed. Because the launcher layer is the top layer, no file can re-enable what
// it disables, and the ownership index names LauncherLabel for each `disabled`.
func SafeOps(layers []loader.Layer) []loader.Op {
	tree, _, _ := loader.Compose(layers)
	ids := []string{PluginsGroup}
	for _, l := range layers {
		for _, id := range OptionalGroups(l) {
			if !slices.Contains(ids, id) {
				ids = append(ids, id)
			}
		}
	}
	var ops []loader.Op
	for _, id := range ids {
		if e := find(tree, id); e != nil && e.Group {
			ops = append(ops, loader.Op{Kind: loader.OpOverride, ID: id,
				Set: map[string]json.RawMessage{"disabled": json.RawMessage("true")}})
		}
	}
	return ops
}

// OptionalGroups returns the tree ids of the optional groups a bundle layer
// inserts: each inserted group whose id starts with OptionalPrefix. A layer that
// is not a bundle layer (its label does not start with BundleLabel) has none, so a
// person's own "x." group is not an optional bundle.
func OptionalGroups(layer loader.Layer) []string {
	if !strings.HasPrefix(layer.Label, BundleLabel) {
		return nil
	}
	var ids []string
	for _, op := range layer.Ops {
		if op.Kind != loader.OpInsert {
			continue
		}
		for _, e := range op.Entries {
			if !e.Group || !strings.HasPrefix(e.ID, OptionalPrefix) {
				continue
			}
			id := e.ID
			if op.ID != "" {
				id = op.ID + loader.Separator + e.ID
			}
			ids = append(ids, id)
		}
	}
	return ids
}

// find returns the entry of tree with the tree id ("group:child"), or nil.
func find(tree []loader.EntryOptions, id string) *loader.EntryOptions {
	var found *loader.EntryOptions
	for _, segment := range strings.Split(id, loader.Separator) {
		i := slices.IndexFunc(tree, func(e loader.EntryOptions) bool { return e.ID == segment })
		if i < 0 {
			return nil
		}
		found = &tree[i]
		tree = found.Children
	}
	return found
}
