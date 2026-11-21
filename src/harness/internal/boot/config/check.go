package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
)

// Catalog is what a check needs of the catalogue: the item a name configures.
type Catalog interface {
	Lookup(name string) (catalog.Item, error)
}

// CheckOptions are the inputs of a static check beyond the layers.
type CheckOptions struct {
	// Catalog resolves entry names to their config types; nil skips the name and
	// config checks.
	Catalog Catalog
	// Evaluator type-checks the expressions of the tree; nil leaves them unchecked.
	Evaluator loader.Evaluator
	// Docs are the parsed layer files by layer label, so an issue in a config value
	// names the line of that value; a layer without one is placed at its operation.
	Docs map[string]*loader.LayerDocument
}

// Report is a composed tree that was checked without being started: the tree, the
// index of which layer supplied each value, and every issue found, each placed at
// the file, line and column of the value at fault where it is known.
type Report struct {
	Tree   []loader.EntryOptions
	Index  *loader.Index
	Issues []loader.Issue

	layers []loader.Layer
	docs   map[string]*loader.LayerDocument
}

// OK reports whether the check found no issue.
func (r *Report) OK() bool { return r != nil && len(r.Issues) == 0 }

// Err is nil for a clean report, else a *loader.ParseError carrying the issues.
func (r *Report) Err() error {
	if r.OK() {
		return nil
	}
	return &loader.ParseError{Issues: append([]loader.Issue(nil), r.Issues...)}
}

// Check composes layers and checks the result without starting anything
// (DH-D-BOOT-6, DH-ADR-BOOT-7): the composition issues, every entry name against
// the catalogue, every literal config strictly against its component's config type
// (unknown keys and wrong types are issues), then through the component's own
// schema on top of its defaults (DH-D-CORDIS-5 candidate config, on a scratch fiber
// that never loads) and, with an evaluator, every expression. A config holding an
// expression is decoded only when it loads, and an entry disabled by a literal is
// not resolved (enabling it makes it part of the check), so a value valid here may
// still fail its load. No component is constructed.
func Check(layers []loader.Layer, opts CheckOptions) *Report {
	tree, index, issues := loader.Compose(layers)
	r := &Report{Tree: tree, Index: index, Issues: issues, layers: layers, docs: opts.Docs}
	var pre preflight
	defer pre.close()
	r.walk(tree, "", func(id string, e loader.EntryOptions) {
		r.Issues = append(r.Issues, r.checkEntry(id, e, opts, &pre)...)
	})
	return r
}

// preflightHold is a service no tree provides: a scratch fiber that injects it stays
// pending, so its component is never constructed while its schema can still run.
const preflightHold = "config.preflight-hold"

// preflight is the scratch tree a check resolves candidate configs on, made on
// first use and disposed at the end of the check.
type preflight struct{ root *cordis.Context }

// resolve runs component's schema on raw as a load would, without loading it.
func (p *preflight) resolve(component *cordis.Component, raw json.RawMessage) error {
	if p.root == nil {
		p.root = cordis.New(cordis.WithExporters(), cordis.WithStrictConfig())
	}
	fiber, err := p.root.Use(component, nil, preflightHold)
	if err != nil {
		return err
	}
	defer fiber.Dispose()
	_, err = fiber.ResolveConfig(json.RawMessage(bytes.Clone(raw)))
	return err
}

func (p *preflight) close() {
	if p.root != nil {
		p.root.Dispose()
	}
}

// walk calls fn for each entry of entries and their children, with its tree id.
func (r *Report) walk(entries []loader.EntryOptions, parent string, fn func(id string, e loader.EntryOptions)) {
	for _, e := range entries {
		id := e.ID
		if parent != "" && id != "" {
			id = parent + loader.Separator + id
		}
		fn(id, e)
		if e.Group {
			r.walk(e.Children, id, fn)
		}
	}
}

func (r *Report) checkEntry(id string, e loader.EntryOptions, opts CheckOptions, pre *preflight) []loader.Issue {
	var issues []loader.Issue
	if opts.Evaluator != nil {
		if e.DisabledExpr != "" {
			if err := opts.Evaluator.Check(e.DisabledExpr, loader.DisabledScope); err != nil {
				issues = append(issues, r.issue(id, e, "disabled", fmt.Sprintf("expression %q: %v", e.DisabledExpr, err)))
			}
		}
		for _, x := range exprs(e.Config, "config") {
			if err := opts.Evaluator.Check(x.source, loader.ConfigScope); err != nil {
				issues = append(issues, r.issue(id, e, x.path, fmt.Sprintf("expression %q: %v", x.source, err)))
			}
		}
	}
	if e.Group || opts.Catalog == nil {
		return issues
	}
	item, err := opts.Catalog.Lookup(e.Name)
	if err != nil {
		return append(issues, r.issue(id, e, "name", err.Error()))
	}
	if item.Config == nil || len(bytes.TrimSpace(e.Config)) == 0 || holdsExpr(e.Config) {
		return issues
	}
	if field, msg := strictDecode(item.Config, e.Config); msg != "" {
		return append(issues, r.issue(id, e, field, msg))
	}
	if item.Component == nil || (e.Disabled && e.DisabledExpr == "") {
		return issues
	}
	if err := pre.resolve(item.Component, e.Config); err != nil {
		issues = append(issues, r.FromConfigError(id, e, err)...)
	}
	return issues
}

// strictDecode decodes raw into a new value of t, refusing unknown keys. It returns
// the path of the value at fault ("config.level") and the message, or "" when the
// config decodes.
func strictDecode(t reflect.Type, raw json.RawMessage) (field, msg string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	err := dec.Decode(reflect.New(t).Interface())
	if err == nil {
		return "", ""
	}
	var typeErr *json.UnmarshalTypeError
	switch {
	case errors.As(err, &typeErr):
		field = "config"
		if typeErr.Field != "" {
			field += "." + typeErr.Field
		}
		return field, fmt.Sprintf("cannot use a %s as %s", typeErr.Value, typeErr.Type)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		name, uerr := strconv.Unquote(strings.TrimPrefix(err.Error(), "json: unknown field "))
		if uerr != nil {
			return "config", "unknown field"
		}
		return "config." + name, fmt.Sprintf("unknown field %q", name)
	}
	return "config", strings.TrimPrefix(err.Error(), "json: ")
}

// FromConfigError turns a component's rejection of a candidate config
// (cordis.ConfigError, DH-D-CORDIS-5) into issues placed at the values at fault.
func (r *Report) FromConfigError(id string, e loader.EntryOptions, err error) []loader.Issue {
	var cfgErr *cordis.ConfigError
	if !errors.As(err, &cfgErr) || len(cfgErr.Issues) == 0 {
		return []loader.Issue{r.issue(id, e, "config", err.Error())}
	}
	issues := make([]loader.Issue, 0, len(cfgErr.Issues))
	for _, ci := range cfgErr.Issues {
		field := "config"
		if ci.Path != "" {
			field += "." + ci.Path
		}
		issues = append(issues, r.issue(id, e, field, ci.Message))
	}
	return issues
}

// issue builds the issue of the entry's field, placed at the value in the layer
// that supplied it.
func (r *Report) issue(id string, e loader.EntryOptions, field, msg string) loader.Issue {
	path := id
	if path == "" {
		path = e.Name
	}
	if field != "" {
		path += "." + field
	}
	pos := r.position(id, field)
	if !pos.IsValid() {
		pos = e.Position()
	}
	return loader.Issue{Pos: pos, Path: path, Message: msg}
}

// position finds the value of the entry's field in the layer that owns it: the last
// operation of that layer that wrote it, refined through the layer's document.
func (r *Report) position(id, field string) loader.Position {
	if id == "" {
		return loader.Position{}
	}
	label, ok := r.Index.Owner(id, field)
	if !ok {
		return loader.Position{}
	}
	for _, layer := range r.layers {
		if layer.Label != label {
			continue
		}
		doc := r.docs[label]
		for i := len(layer.Ops) - 1; i >= 0; i-- {
			op := layer.Ops[i]
			var node *loader.Node
			if doc != nil && doc.Root != nil && i < len(doc.Root.Items) {
				node = doc.Root.Items[i]
			}
			if pos, ok := locateInOp(op, node, id, field); ok {
				return pos
			}
		}
	}
	return loader.Position{}
}

// locateInOp reports where op writes the entry's field: the deepest node of the
// field's path it holds, or the operation itself without a document.
func locateInOp(op loader.Op, node *loader.Node, id, field string) (loader.Position, bool) {
	steps := splitField(field)
	switch op.Kind {
	case loader.OpMerge:
		if op.ID != id || (len(steps) > 0 && steps[0] != "config") {
			return loader.Position{}, false
		}
		if len(steps) > 0 {
			steps = steps[1:]
		}
		if !patchWrites(op.Patch, steps) {
			return loader.Position{}, false
		}
		return deepest(op.Pos, member(node, "merge"), steps), true
	case loader.OpOverride:
		if op.ID != id || len(steps) == 0 {
			return loader.Position{}, false
		}
		if _, ok := op.Set[steps[0]]; !ok {
			return loader.Position{}, false
		}
		return deepest(op.Pos, member(node, steps[0]), steps[1:]), true
	case loader.OpInsert:
		entry, found := findEntry(op.Entries, op.ID, id)
		if !found {
			return loader.Position{}, false
		}
		pos := entry.Position()
		if !pos.IsValid() {
			pos = op.Pos
		}
		if n := findEntryNode(member(node, "insert"), op.ID, id); n != nil {
			return deepest(n.Pos, n, steps), true
		}
		return pos, true
	}
	return loader.Position{}, false
}

// patchWrites reports whether a merge patch supplies the config field at steps: each
// step is a key of the patch, or the patch replaces a value above it whole.
func patchWrites(patch json.RawMessage, steps []string) bool {
	if len(steps) == 0 {
		return true
	}
	var v any
	if err := json.Unmarshal(patch, &v); err != nil {
		return true
	}
	for _, step := range steps {
		if i := strings.IndexByte(step, '['); i > 0 {
			step = step[:i]
		}
		m, ok := v.(map[string]any)
		if !ok {
			return true
		}
		if v, ok = m[step]; !ok {
			return false
		}
	}
	return true
}

func findEntry(entries []loader.EntryOptions, parent, id string) (loader.EntryOptions, bool) {
	for _, e := range entries {
		if e.ID == "" {
			continue
		}
		own := e.ID
		if parent != "" {
			own = parent + loader.Separator + e.ID
		}
		if own == id {
			return e, true
		}
		if found, ok := findEntry(e.Children, own, id); ok {
			return found, true
		}
	}
	return loader.EntryOptions{}, false
}

func findEntryNode(list *loader.Node, parent, id string) *loader.Node {
	if list == nil || list.Kind != loader.ArrayNode {
		return nil
	}
	for _, item := range list.Items {
		idNode := member(item, "id")
		if idNode == nil || idNode.Kind != loader.StringNode {
			continue
		}
		own := idNode.Text
		if parent != "" {
			own = parent + loader.Separator + own
		}
		if own == id {
			return item
		}
		if n := findEntryNode(member(item, "children"), own, id); n != nil {
			return n
		}
	}
	return nil
}

// deepest follows steps from node and returns the position of the deepest node
// reached, or fallback when node is nil.
func deepest(fallback loader.Position, node *loader.Node, steps []string) loader.Position {
	if node == nil {
		return fallback
	}
	pos := node.Pos
	for _, step := range steps {
		next := member(node, step)
		if next == nil {
			break
		}
		node, pos = next, next.Pos
	}
	return pos
}

// member returns the value of the last field key of an object node.
func member(node *loader.Node, key string) *loader.Node {
	if node == nil || node.Kind != loader.ObjectNode {
		return nil
	}
	var found *loader.Node
	for _, f := range node.Fields {
		if f.Key == key {
			found = f.Value
		}
	}
	return found
}

func splitField(field string) []string {
	if field == "" {
		return nil
	}
	return strings.Split(field, ".")
}

// holdsExpr reports whether a config may hold an expression, which is decoded only
// once evaluated at load.
func holdsExpr(raw json.RawMessage) bool {
	return bytes.Contains(raw, []byte("$expr")) || bytes.Contains(raw, []byte("${{"))
}

// computed lists the fields an expression computes in the composed tree that ops
// would replace (DH-ADR-BOOT-7): a merge whose patch writes an expression's field
// or a value above it, an override of an expression-valued disabled or of a config
// holding an expression.
func (r *Report) computed(ops []loader.Op) []string {
	var found []string
	for _, op := range ops {
		if op.Kind != loader.OpMerge && op.Kind != loader.OpOverride {
			continue
		}
		e, ok := findEntry(r.Tree, "", op.ID)
		if !ok {
			continue
		}
		if op.Kind == loader.OpOverride {
			if _, ok := op.Set["disabled"]; ok && e.DisabledExpr != "" {
				found = append(found, joinField(op.ID, "disabled"))
			}
			if _, ok := op.Set["config"]; !ok {
				continue
			}
		}
		for _, x := range exprs(e.Config, "config") {
			if op.Kind == loader.OpOverride || patchWrites(op.Patch, splitField(x.path)[1:]) {
				found = append(found, joinField(op.ID, x.path))
			}
		}
	}
	return found
}

type foundExpr struct{ source, path string }

// exprs lists the expressions of a config value with their paths.
func exprs(raw json.RawMessage, path string) []foundExpr {
	if !holdsExpr(raw) {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	var found []foundExpr
	var walk func(v any, path string)
	walk = func(v any, path string) {
		switch x := v.(type) {
		case map[string]any:
			if src, ok := x["$expr"].(string); ok && len(x) == 1 {
				found = append(found, foundExpr{source: src, path: path})
				return
			}
			keys := make([]string, 0, len(x))
			for k := range x {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				walk(x[k], path+"."+k)
			}
		case []any:
			for i, child := range x {
				walk(child, fmt.Sprintf("%s[%d]", path, i))
			}
		case string:
			if s := strings.TrimSpace(x); strings.HasPrefix(s, "${{") && strings.HasSuffix(s, "}}") {
				found = append(found, foundExpr{source: strings.TrimSpace(s[3 : len(s)-2]), path: path})
			}
		}
	}
	walk(v, path)
	return found
}
