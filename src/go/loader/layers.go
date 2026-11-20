package loader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
)

// OpKind is the kind of a layer operation.
type OpKind string

const (
	// OpInsert appends entries to the root, or to the group named by ID.
	OpInsert OpKind = "insert"
	// OpOverride replaces named fields of the entry with the tree id ID. Each field is
	// replaced whole.
	OpOverride OpKind = "override"
	// OpMerge deep-merges Patch into the config of the entry with the tree id ID, as a
	// JSON merge patch: an object merges key by key, null deletes a key, anything else
	// (an expression included) replaces the value.
	OpMerge OpKind = "merge"
	// OpRemove removes the entry with the tree id ID, with its children.
	OpRemove OpKind = "remove"
)

// overridable lists, in file order, the fields an override may replace. The id and
// the children are the entry's identity and structure; the name is an assertion.
var overridable = []string{"config", "disabled", "inject", "isolate", "intercept"}

// Op is one operation of a layer. In a layer file an operation is one object:
//
//	{"insert": [<entries>]}                 append to the root
//	{"id": "g", "insert": [<entries>]}      append to the group g
//	{"id": "x", "disabled": true, ...}      override fields of x
//	{"id": "x", "merge": {"port": 8080}}    merge into the config of x
//	{"id": "x", "remove": true}             remove x
//
// "name" on an override, merge or remove asserts the component name of the target;
// a mismatch skips the operation and is reported.
type Op struct {
	Kind OpKind
	// ID is the tree id of the target entry, or of the group an insert appends to
	// ("" for the root).
	ID string
	// Name, when set, must equal the target's component name.
	Name string
	// Entries are the entries an insert appends.
	Entries []EntryOptions
	// Set maps each field an override replaces to its value in file form.
	Set map[string]json.RawMessage
	// Patch is the merge patch of a merge.
	Patch json.RawMessage
	// Pos is where the operation was decoded from.
	Pos Position

	// nodes are the decoded values of Set and Patch (key "merge"), so an applied value
	// keeps its position in the layer file.
	nodes map[string]*Node
	// invalid is the decoded object of an operation that could not be decoded, and
	// faults are why; it is skipped by Compose and written back as it was read.
	invalid *Node
	faults  []Issue
}

// Valid reports whether the operation was decoded without faults.
func (op Op) Valid() bool { return op.invalid == nil }

func (op Op) clone() Op {
	clone := op
	clone.Entries = cloneAll(op.Entries)
	clone.Patch = slices.Clone(op.Patch)
	if op.Set != nil {
		clone.Set = make(map[string]json.RawMessage, len(op.Set))
		for key, value := range op.Set {
			clone.Set[key] = slices.Clone(value)
		}
	}
	clone.nodes = maps.Clone(op.nodes)
	return clone
}

// key identifies the operation within one layer, so a write-back finds the
// operation it rewrites.
func (op Op) key() string { return string(op.Kind) + "\x00" + op.ID }

// MarshalJSON writes the operation in its file form.
func (op Op) MarshalJSON() ([]byte, error) {
	if op.invalid != nil {
		return op.invalid.JSON(), nil
	}
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	member := func(key string, value []byte) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		encoded, _ := json.Marshal(key)
		b.Write(encoded)
		b.WriteByte(':')
		b.Write(value)
	}
	quoted := func(s string) []byte { encoded, _ := json.Marshal(s); return encoded }
	if op.ID != "" || op.Kind != OpInsert {
		member("id", quoted(op.ID))
	}
	if op.Name != "" {
		member("name", quoted(op.Name))
	}
	switch op.Kind {
	case OpInsert:
		var entries bytes.Buffer
		entries.WriteByte('[')
		for i, entry := range op.Entries {
			if i > 0 {
				entries.WriteByte(',')
			}
			encoded, err := entry.layerForm()
			if err != nil {
				return nil, err
			}
			entries.Write(encoded)
		}
		entries.WriteByte(']')
		member("insert", entries.Bytes())
	case OpOverride:
		for _, key := range overridable {
			if value, ok := op.Set[key]; ok {
				member(key, compactJSON(value))
			}
		}
	case OpMerge:
		patch := op.Patch
		if len(patch) == 0 {
			patch = json.RawMessage("{}")
		}
		member("merge", compactJSON(patch))
	case OpRemove:
		member("remove", []byte("true"))
	default:
		return nil, fmt.Errorf("loader: unknown layer operation %q", op.Kind)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func compactJSON(raw json.RawMessage) []byte {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	return b.Bytes()
}

// layerForm is the entry as a layer writes it: as it was read while it is unchanged,
// and without the id the loader generated for an entry read without one, so an
// anonymous entry stays anonymous in the file.
func (o EntryOptions) layerForm() ([]byte, error) {
	if o.intact() {
		return slices.Clone(o.raw), nil
	}
	return o.canonical()
}

// Layer is one layer of a composition: a label (such as "bundle", "profile", "user"
// or "project"), the file it was read from, whether runtime changes are written to
// it, and its operations.
type Layer struct {
	Label    string `json:"label"`
	Path     string `json:"path,omitempty"`
	Writable bool   `json:"writable,omitempty"`
	Ops      []Op   `json:"-"`
}

// LayerDocument is a decoded layer file: its text, its node tree and its operations.
// Issues are the faults of operations that could not be decoded; such an operation
// is kept, so a write-back does not lose it.
type LayerDocument struct {
	File   string
	Data   []byte
	Root   *Node
	Ops    []Op
	Issues []Issue
}

// ParseLayer decodes a layer file: an array of operations. The error is a
// *ParseError when the bytes are malformed or are not an array; a faulty operation is
// not an error here, it is reported by LayerDocument.Issues and skipped by Compose.
func ParseLayer(codec Codec, file string, data []byte) (*LayerDocument, error) {
	if codec == nil {
		codec = CodecFor(file)
	}
	root, err := codec.Decode(file, data)
	if err != nil {
		return nil, err
	}
	if root == nil || root.Kind != ArrayNode {
		issue := Issue{Message: "must be an array of layer operations"}
		if root != nil {
			issue.Pos = root.Pos
			issue.Message += ", got " + root.Kind.String()
		}
		return nil, &ParseError{Issues: []Issue{issue}}
	}
	doc := &LayerDocument{File: file, Data: bytes.Clone(data), Root: root, Ops: []Op{}}
	for i, item := range root.Items {
		op, issues := decodeOp(item, fmt.Sprintf("[%d]", i))
		doc.Ops = append(doc.Ops, op)
		doc.Issues = append(doc.Issues, issues...)
	}
	return doc, nil
}

func decodeOp(node *Node, path string) (Op, []Issue) {
	op := Op{Pos: node.Pos}
	var issues []Issue
	fault := func(at *Node, relative, message string) {
		issues = append(issues, Issue{Pos: at.Pos, Path: joinPath(path, relative), Message: message})
	}
	if node.Kind != ObjectNode {
		fault(node, "", "must be an object, got "+node.Kind.String())
		op.invalid, op.faults = node, issues
		return op, issues
	}
	var kinds []OpKind
	seen := map[string]bool{}
	for _, field := range node.Fields {
		key, value := field.Key, field.Value
		if seen[key] {
			fault(value, key, "duplicate key")
			continue
		}
		seen[key] = true
		switch {
		case key == "id" || key == "name":
			if value.Kind != StringNode {
				fault(value, key, "must be a string, got "+value.Kind.String())
			} else if key == "id" {
				op.ID = value.Text
			} else {
				op.Name = value.Text
			}
		case key == "insert":
			kinds = append(kinds, OpInsert)
			if value.Kind != ArrayNode {
				fault(value, key, "must be an array of entries, got "+value.Kind.String())
				break
			}
			op.Entries = decodeEntries(value.Items, joinPath(path, key))
			normalize(op.Entries)
			if op.Entries == nil {
				op.Entries = []EntryOptions{}
			}
		case key == "merge":
			kinds = append(kinds, OpMerge)
			if value.Kind != ObjectNode {
				fault(value, key, "must be an object, got "+value.Kind.String())
				break
			}
			op.Patch = value.JSON()
			op.nodes = map[string]*Node{"merge": value}
		case key == "remove":
			kinds = append(kinds, OpRemove)
			if value.Kind != BoolNode || !value.Bool {
				fault(value, key, "must be true")
			}
		case slices.Contains(overridable, key):
			if !slices.Contains(kinds, OpOverride) {
				kinds = append(kinds, OpOverride)
			}
			if op.Set == nil {
				op.Set = map[string]json.RawMessage{}
			}
			if op.nodes == nil {
				op.nodes = map[string]*Node{}
			}
			op.Set[key] = value.JSON()
			op.nodes[key] = value
		default:
			issues = append(issues, Issue{Pos: field.KeyPos, Path: joinPath(path, key), Message: "unknown key"})
		}
	}
	switch {
	case len(kinds) == 0:
		fault(node, "", "names no operation (insert, merge, remove or a field to override)")
	case len(kinds) > 1:
		names := make([]string, len(kinds))
		for i, kind := range kinds {
			names[i] = string(kind)
		}
		fault(node, "", "combines operations ("+strings.Join(names, ", ")+"); write one object per operation")
	default:
		op.Kind = kinds[0]
		if op.Kind != OpInsert && op.ID == "" {
			fault(node, "id", "is required")
		}
		if op.Kind == OpInsert && op.Name != "" {
			fault(node, "name", "is not allowed on an insert")
		}
	}
	if len(issues) > 0 {
		op.invalid = node
		op.faults = issues
	}
	return op, issues
}

// ------------------------------------------------------------- composition

// Ownership names the layer that supplied a value: the entry with the tree id ID, or
// the field at the path Field of it ("config.port", "disabled"; "" for the entry).
type Ownership struct {
	ID    string
	Field string
	Label string
}

// Index records which layer supplied each entry of a composed tree and each field
// of it. An anonymous entry, which no layer can address, is not indexed.
type Index struct {
	entries map[string]string
	fields  map[string]map[string]string
}

func newIndex() *Index {
	return &Index{entries: map[string]string{}, fields: map[string]map[string]string{}}
}

// Owner returns the label of the layer that supplied the field of the entry with the
// tree id: the layer that last set that field, or a field enclosing it, or else the
// layer that inserted the entry. field "" asks for the entry itself.
func (x *Index) Owner(id, field string) (string, bool) {
	if x == nil {
		return "", false
	}
	if fields := x.fields[id]; fields != nil {
		for path := field; path != ""; path = parentPath(path) {
			if label, ok := fields[path]; ok {
				return label, true
			}
		}
	}
	label, ok := x.entries[id]
	return label, ok
}

// Owners lists every recorded ownership, sorted by id and field, for a config dump.
func (x *Index) Owners() []Ownership {
	if x == nil {
		return nil
	}
	var owners []Ownership
	for id, label := range x.entries {
		owners = append(owners, Ownership{ID: id, Label: label})
	}
	for id, fields := range x.fields {
		for field, label := range fields {
			owners = append(owners, Ownership{ID: id, Field: field, Label: label})
		}
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].ID != owners[j].ID {
			return owners[i].ID < owners[j].ID
		}
		return owners[i].Field < owners[j].Field
	})
	return owners
}

func (x *Index) insert(parentID string, entries []EntryOptions, label string) {
	for _, entry := range entries {
		if entry.ID == "" {
			continue
		}
		id := compose(parentID, entry.ID)
		x.entries[id] = label
		x.insert(id, entry.Children, label)
	}
}

func (x *Index) remove(id string) {
	delete(x.entries, id)
	delete(x.fields, id)
	prefix := id + Separator
	for key := range x.entries {
		if strings.HasPrefix(key, prefix) {
			delete(x.entries, key)
		}
	}
	for key := range x.fields {
		if strings.HasPrefix(key, prefix) {
			delete(x.fields, key)
		}
	}
}

// own records label as the supplier of the field and drops the records below it,
// which the new value replaced; deleted only drops them.
func (x *Index) own(id, field, label string, deleted bool) {
	fields := x.fields[id]
	if fields == nil {
		fields = map[string]string{}
		x.fields[id] = fields
	}
	for path := range fields {
		if path == field || strings.HasPrefix(path, field+".") || strings.HasPrefix(path, field+"[") {
			delete(fields, path)
		}
	}
	if !deleted {
		fields[field] = label
	}
}

// Compose applies the layers in order to an empty tree, and returns the tree, an
// index of which layer supplied each value, and the issues found: an operation that
// is invalid or matches nothing is reported with its layer label and position and
// skipped. A later layer wins over an earlier one. The layers are not modified, so
// a preview composes with the same function as the running system.
func Compose(layers []Layer) (tree []EntryOptions, index *Index, issues []Issue) {
	tree = []EntryOptions{}
	index = newIndex()
	for _, layer := range layers {
		for _, op := range layer.Ops {
			if issue := applyOp(&tree, index, layer.Label, op.clone()); issue != nil {
				issues = append(issues, *issue)
			}
		}
	}
	return tree, index, issues
}

func applyOp(tree *[]EntryOptions, index *Index, label string, op Op) *Issue {
	fail := func(format string, args ...any) *Issue {
		return &Issue{Pos: op.Pos, Path: "layer " + label, Message: fmt.Sprintf(format, args...)}
	}
	if op.invalid != nil {
		issue := fail("operation is invalid and was skipped")
		if len(op.faults) > 0 {
			texts := make([]string, len(op.faults))
			for i, fault := range op.faults {
				texts[i] = fault.Error()
			}
			issue.Message += ": " + strings.Join(texts, "; ")
		}
		return issue
	}
	if op.Kind == OpInsert {
		siblings := tree
		if op.ID != "" {
			group := locate(tree, op.ID)
			if group == nil {
				return fail("insert into %q matches no entry", op.ID)
			}
			if !group.Group {
				return fail("insert into %q: the entry is not a group", op.ID)
			}
			siblings = &group.Children
		}
		var skipped []string
		for _, entry := range op.Entries {
			if entry.ID != "" && slices.ContainsFunc(*siblings, func(o EntryOptions) bool { return o.ID == entry.ID }) {
				skipped = append(skipped, entry.ID)
				continue
			}
			*siblings = append(*siblings, entry)
			index.insert(op.ID, []EntryOptions{entry}, label)
		}
		if len(skipped) > 0 {
			return fail("insert: entry %q already exists", strings.Join(skipped, `", "`))
		}
		return nil
	}

	target := locate(tree, op.ID)
	if target == nil {
		return fail("%s %q matches no entry", op.Kind, op.ID)
	}
	if op.Name != "" && op.Name != target.Name {
		return fail("%s %q expects name %q, found %q", op.Kind, op.ID, op.Name, target.Name)
	}
	switch op.Kind {
	case OpRemove:
		parent, id := splitID(op.ID)
		siblings := tree
		if parent != "" {
			siblings = &locate(tree, parent).Children
		}
		*siblings = slices.DeleteFunc(*siblings, func(o EntryOptions) bool { return o.ID == id })
		index.remove(op.ID)
	case OpOverride:
		var failures []string
		for _, key := range overridable {
			raw, ok := op.Set[key]
			if !ok {
				continue
			}
			value := op.nodes[key]
			if value == nil {
				decoded, err := JSON.Decode(op.Pos.File, raw)
				if err != nil {
					failures = append(failures, key+": "+err.Error())
					continue
				}
				decoded.Pos = op.Pos
				value = decoded
			}
			faults := setField(target, key, value)
			for _, fault := range faults {
				failures = append(failures, fault.Error())
			}
			if len(faults) == 0 {
				index.own(op.ID, key, label, false)
			}
		}
		if len(failures) > 0 {
			return fail("override %q: %s", op.ID, strings.Join(failures, "; "))
		}
	case OpMerge:
		patch := op.nodes["merge"]
		if patch == nil {
			decoded, err := JSON.Decode(op.Pos.File, op.Patch)
			if err != nil {
				return fail("merge %q: %v", op.ID, err)
			}
			patch = decoded
		}
		var target0 *Node
		if len(target.Config) > 0 {
			target0, _ = JSON.Decode("", target.Config)
		}
		var b bytes.Buffer
		mergeNodes(&b, target0, patch, "config", func(field string, deleted bool) {
			index.own(op.ID, field, label, deleted)
		})
		target.Config = b.Bytes()
		if target.source != nil {
			target.privateSource()
			for _, field := range patch.Fields {
				recordPositions(field.Value, "config."+field.Key, target.source.values)
			}
		}
	default:
		return fail("unknown operation %q", op.Kind)
	}
	return nil
}

// setField replaces one overridable field of o with value, checked as the entry
// decoder checks it; the issues are the value's faults, and a faulty value leaves
// the field as it was.
func setField(o *EntryOptions, key string, value *Node) []Issue {
	part := decodeEntry(&Node{Kind: ObjectNode, Pos: value.Pos, Fields: []*Field{{Key: key, KeyPos: value.Pos, Value: value}}}, "")
	if len(part.issues) > 0 {
		return part.issues
	}
	switch key {
	case "config":
		o.Config = part.Config
		if bytes.Equal(bytes.TrimSpace(o.Config), []byte("null")) {
			o.Config = nil
		}
	case "disabled":
		o.Disabled, o.DisabledExpr = part.Disabled, part.DisabledExpr
	case "inject":
		o.Inject = part.Inject
	case "isolate":
		o.Isolate = part.Isolate
	case "intercept":
		o.Intercept = part.Intercept
	}
	o.forgetValues(key)
	if o.source != nil {
		recordPositions(value, key, o.source.values)
	}
	return nil
}

// forgetValues drops the recorded positions of the field and everything below it,
// on a private copy of the entry's source, so the layer the entry came from keeps
// its own.
func (o *EntryOptions) forgetValues(field string) {
	if o.source == nil {
		return
	}
	o.privateSource()
	for path := range o.source.values {
		if path == field || strings.HasPrefix(path, field+".") || strings.HasPrefix(path, field+"[") {
			delete(o.source.values, path)
		}
	}
}

// privateSource gives the entry its own copy of its source record, which Clone
// shares, so recording a position does not change the entry it was cloned from.
func (o *EntryOptions) privateSource() {
	source := *o.source
	source.values = maps.Clone(o.source.values)
	if source.values == nil {
		source.values = map[string]Position{}
	}
	o.source = &source
}

// mergeNodes writes the JSON merge patch of patch onto target (nil: absent) to b,
// keeping the order of the target's keys, and reports each field the patch set or
// deleted, below the path at.
func mergeNodes(b *bytes.Buffer, target, patch *Node, at string, own func(field string, deleted bool)) {
	if patch.Kind != ObjectNode {
		b.Write(patch.JSON())
		own(at, false)
		return
	}
	if target == nil || target.Kind != ObjectNode {
		// a merge into a non-object replaces it with the patch, nulls removed
		target = &Node{Kind: ObjectNode}
		own(at, false)
	}
	patched := map[string]*Node{}
	for _, field := range patch.Fields {
		patched[field.Key] = field.Value
	}
	b.WriteByte('{')
	first := true
	member := func(key string) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		encoded, _ := json.Marshal(key)
		b.Write(encoded)
		b.WriteByte(':')
	}
	written := map[string]bool{}
	for _, field := range target.Fields {
		if written[field.Key] {
			continue
		}
		written[field.Key] = true
		path := at + "." + field.Key
		value, ok := patched[field.Key]
		switch {
		case !ok:
			member(field.Key)
			b.Write(field.Value.JSON())
		case value.Kind == NullNode:
			own(path, true)
		default:
			member(field.Key)
			mergeNodes(b, field.Value, value, path, own)
		}
	}
	for _, field := range patch.Fields {
		if written[field.Key] || field.Value.Kind == NullNode {
			continue
		}
		written[field.Key] = true
		member(field.Key)
		mergeNodes(b, nil, field.Value, at+"."+field.Key, own)
	}
	b.WriteByte('}')
}

// locate returns the entry with the tree id, or nil.
func locate(tree *[]EntryOptions, id string) *EntryOptions {
	cursor := tree
	segments := strings.Split(id, Separator)
	for i, segment := range segments {
		index := slices.IndexFunc(*cursor, func(o EntryOptions) bool { return o.ID == segment })
		if index < 0 {
			return nil
		}
		if i == len(segments)-1 {
			return &(*cursor)[index]
		}
		cursor = &(*cursor)[index].Children
	}
	return nil
}

// splitID splits a tree id into the id of the enclosing group and the entry's own id.
func splitID(id string) (parent, own string) {
	if i := strings.LastIndex(id, Separator); i >= 0 {
		return id[:i], id[i+len(Separator):]
	}
	return "", id
}
