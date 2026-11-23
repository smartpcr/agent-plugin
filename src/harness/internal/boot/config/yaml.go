// Package config is the YAML codec of the loader (DH-D-LOADER-2, DH-T-LOADER-2): it
// plugs the harness YAML dialect (internal/boot/yamlx) into the loader's Codec seam,
// so a ".yaml" or ".yml" config or layer file decodes to the same entries as its JSON
// form, with positions into the YAML text, and a layer written back keeps its
// comments.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/yamlx"
)

// ExprTag is the YAML tag of an expression (DH-ADR-LOADER-3): `!expr <source>`.
const ExprTag = "!expr"

// Extensions are the file extensions the YAML codec is registered for.
var Extensions = []string{".yaml", ".yml"}

// YAML is the loader codec for YAML files. Machine-written files stay JSON
// (DH-ADR-LOADER-1); YAML is for files people write.
var YAML loader.LayerCodec = yamlCodec{}

var register sync.Once

// Register makes YAML the loader codec for Extensions. It may be called any number
// of times.
func Register() {
	register.Do(func() {
		for _, ext := range Extensions {
			loader.RegisterCodec(ext, YAML)
		}
	})
}

type yamlCodec struct{}

// Decode reads data under the harness dialect, with `!expr` as the one tag.
func (yamlCodec) Decode(file string, data []byte) (*loader.Node, error) {
	doc, err := yamlx.Parse(file, data, yamlx.WithTag(ExprTag))
	if err != nil {
		return nil, parseError(file, err)
	}
	var issues []loader.Issue
	node := toNode(file, doc.Value(), &issues)
	if len(issues) > 0 {
		return nil, &loader.ParseError{Issues: issues}
	}
	return node, nil
}

// Encode renders entries as a fresh YAML document. The loader never writes a config
// file at runtime (only a layer file, see EncodeLayer), so there is nothing to keep.
func (yamlCodec) Encode(_ *loader.Document, entries []loader.EntryOptions) ([]byte, error) {
	if entries == nil {
		entries = []loader.EntryOptions{}
	}
	want, err := fileForm(entries)
	if err != nil {
		return nil, err
	}
	return yamlx.Dump(toValue(want))
}

// ErrWriteRefused wraps every reason EncodeLayer refuses to rewrite an existing
// layer file (DH-LIB-YAML-R8): the file must be left as it was.
var ErrWriteRefused = errors.New("config: YAML layer write refused")

// edit applies ops to doc; tests replace it to stand in for a faulty encoder.
var edit = func(doc *yamlx.Document, ops ...yamlx.Op) ([]byte, error) { return doc.Edit(ops...) }

// EncodeLayer writes ops. Given the layer document last read, it edits that
// document, so unchanged operations and values keep their text and every comment
// stays. If that document cannot be read again, or the edit cannot be verified to
// give exactly ops with every comment (DH-LIB-YAML-R8), the write is refused with
// an error wrapping ErrWriteRefused and no text: the caller keeps the change in
// memory and reports it (DH-LIB-LOADER-R9). Only a layer with no previous text is
// rendered afresh.
func (c yamlCodec) EncodeLayer(previous *loader.LayerDocument, ops []loader.Op) ([]byte, error) {
	if ops == nil {
		ops = []loader.Op{}
	}
	want, err := fileForm(ops)
	if err != nil {
		return nil, err
	}
	if previous == nil || len(previous.Data) == 0 {
		return yamlx.Dump(toValue(want))
	}
	doc, err := yamlx.Parse(previous.File, previous.Data, yamlx.WithTag(ExprTag))
	if err != nil {
		return nil, fmt.Errorf("%w: %s: the previous text does not parse: %w", ErrWriteRefused, previous.File, err)
	}
	var edits []yamlx.Op
	diff(doc.Value(), want, nil, &edits)
	edited, err := edit(doc, edits...)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrWriteRefused, err)
	}
	back, err := c.Decode(previous.File, edited)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: the edited text does not decode: %w", ErrWriteRefused, previous.File, err)
	}
	if !same(back, want) {
		return nil, fmt.Errorf("%w: %s: the edited text does not hold the intended operations", ErrWriteRefused, previous.File)
	}
	return edited, nil
}

// fileForm is the node tree of v's JSON file form.
func fileForm(v any) (*loader.Node, error) {
	compact, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return loader.JSON.Decode("", compact)
}

// parseError places a YAML syntax error at its line and column, so Diagnose can
// show it in a code frame of the text; any other failure names the file only.
func parseError(file string, err error) error {
	var syntax *yamlx.SyntaxError
	if errors.As(err, &syntax) {
		return &loader.ParseError{Issues: []loader.Issue{{
			Pos:     loader.Position{File: file, Line: syntax.Line, Column: syntax.Column},
			Message: syntax.Message,
		}}}
	}
	return &loader.ParseError{Issues: []loader.Issue{{Pos: loader.Position{File: file}, Message: err.Error()}}}
}

func position(file string, p yamlx.Pos) loader.Position {
	return loader.Position{File: file, Line: p.Line, Column: p.Column, Offset: p.Offset}
}

// toNode converts a YAML value to a loader node. A number keeps a spelling JSON can
// read; a float JSON cannot hold (infinity, NaN) is an issue.
func toNode(file string, v yamlx.Value, issues *[]loader.Issue) *loader.Node {
	n := &loader.Node{Pos: position(file, v.Pos)}
	switch v.Kind {
	case yamlx.Bool:
		n.Kind, n.Bool = loader.BoolNode, v.Bool
	case yamlx.Int:
		n.Kind, n.Text = loader.NumberNode, strconv.FormatInt(v.Int, 10)
	case yamlx.Float:
		n.Kind = loader.NumberNode
		switch {
		case math.IsInf(v.Float, 0) || math.IsNaN(v.Float):
			*issues = append(*issues, loader.Issue{Pos: n.Pos, Message: v.Text + " is not a finite number; JSON cannot hold it"})
		case json.Valid([]byte(v.Text)):
			n.Text = v.Text
		default:
			n.Text = strconv.FormatFloat(v.Float, 'g', -1, 64)
		}
	case yamlx.String:
		n.Kind, n.Text = loader.StringNode, v.Text
	case yamlx.Tagged:
		n.Kind, n.Text = loader.ExprNode, v.Text
	case yamlx.Seq:
		n.Kind = loader.ArrayNode
		n.Items = make([]*loader.Node, len(v.Items))
		for i, item := range v.Items {
			n.Items[i] = toNode(file, item, issues)
		}
	case yamlx.Map:
		n.Kind = loader.ObjectNode
		n.Fields = make([]*loader.Field, len(v.Fields))
		for i, field := range v.Fields {
			n.Fields[i] = &loader.Field{Key: field.Key, KeyPos: position(file, field.KeyPos), Value: toNode(file, field.Value, issues)}
		}
	default:
		n.Kind = loader.NullNode
	}
	return n
}

// toValue converts a loader node to a YAML value, keeping the order of fields.
func toValue(n *loader.Node) yamlx.Value {
	switch n.Kind {
	case loader.BoolNode:
		return yamlx.Value{Kind: yamlx.Bool, Bool: n.Bool}
	case loader.NumberNode:
		if i, err := strconv.ParseInt(n.Text, 10, 64); err == nil {
			return yamlx.Value{Kind: yamlx.Int, Int: i, Text: n.Text}
		}
		f, _ := strconv.ParseFloat(n.Text, 64)
		return yamlx.Value{Kind: yamlx.Float, Float: f, Text: n.Text}
	case loader.StringNode:
		return yamlx.Value{Kind: yamlx.String, Text: n.Text}
	case loader.ExprNode:
		return yamlx.Value{Kind: yamlx.Tagged, Tag: ExprTag, Text: n.Text}
	case loader.ArrayNode:
		v := yamlx.Value{Kind: yamlx.Seq, Items: make([]yamlx.Value, len(n.Items))}
		for i, item := range n.Items {
			v.Items[i] = toValue(item)
		}
		return v
	case loader.ObjectNode:
		v := yamlx.Value{Kind: yamlx.Map, Fields: make([]yamlx.Field, len(n.Fields))}
		for i, field := range n.Fields {
			v.Fields[i] = yamlx.Field{Key: field.Key, Value: toValue(field.Value)}
		}
		return v
	default:
		return yamlx.Value{Kind: yamlx.Null}
	}
}

func extend(path []any, step any) []any {
	return append(append(make([]any, 0, len(path)+1), path...), step)
}

// diff appends the edits that turn old into want. Mappings are edited key by key;
// sequences of identified items (entries and layer operations, by id and kind) are
// matched by identity, others by index; anything else is replaced.
func diff(old yamlx.Value, want *loader.Node, path []any, ops *[]yamlx.Op) {
	switch {
	case old.Kind == yamlx.Map && want.Kind == loader.ObjectNode:
		wanted := map[string]bool{}
		for _, field := range want.Fields {
			wanted[field.Key] = true
		}
		for _, field := range old.Fields {
			if !wanted[field.Key] {
				*ops = append(*ops, yamlx.Delete(extend(path, field.Key)))
			}
		}
		for _, field := range want.Fields {
			if value, ok := old.Get(field.Key); ok {
				diff(value, field.Value, extend(path, field.Key), ops)
			} else {
				*ops = append(*ops, yamlx.Set(extend(path, field.Key), toValue(field.Value)))
			}
		}
	case old.Kind == yamlx.Seq && want.Kind == loader.ArrayNode:
		if diffByIdentity(old, want, path, ops) {
			return
		}
		shared := min(len(old.Items), len(want.Items))
		for i := 0; i < shared; i++ {
			diff(old.Items[i], want.Items[i], extend(path, i), ops)
		}
		for i := len(old.Items) - 1; i >= shared; i-- {
			*ops = append(*ops, yamlx.Delete(extend(path, i)))
		}
		for _, item := range want.Items[shared:] {
			*ops = append(*ops, yamlx.Insert(path, -1, toValue(item)))
		}
	case sameScalar(old, want):
	default:
		*ops = append(*ops, yamlx.Set(path, toValue(want)))
	}
}

// diffByIdentity matches the items of two sequences by identity. It reports false,
// emitting nothing, when an item has no identity or the shared items are reordered.
func diffByIdentity(old yamlx.Value, want *loader.Node, path []any, ops *[]yamlx.Op) bool {
	oldIDs, ok := identities(len(old.Items), func(i int) string { return identityOfValue(old.Items[i]) })
	if !ok {
		return false
	}
	wantIDs, ok := identities(len(want.Items), func(i int) string { return identityOfNode(want.Items[i]) })
	if !ok {
		return false
	}
	inWant := map[string]bool{}
	for _, id := range wantIDs {
		inWant[id] = true
	}
	var kept []int
	for i, id := range oldIDs {
		if inWant[id] {
			kept = append(kept, i)
		}
	}
	j := 0
	for _, id := range wantIDs {
		if j < len(kept) && oldIDs[kept[j]] == id {
			j++
		}
	}
	if j != len(kept) {
		return false
	}
	for i := len(oldIDs) - 1; i >= 0; i-- {
		if !inWant[oldIDs[i]] {
			*ops = append(*ops, yamlx.Delete(extend(path, i)))
		}
	}
	j = 0
	for i, id := range wantIDs {
		if j < len(kept) && oldIDs[kept[j]] == id {
			diff(old.Items[kept[j]], want.Items[i], extend(path, i), ops)
			j++
			continue
		}
		*ops = append(*ops, yamlx.Insert(path, i, toValue(want.Items[i])))
	}
	return true
}

// identities returns the identity of each of n items, or false when one has none or
// two share one.
func identities(n int, of func(int) string) ([]string, bool) {
	ids := make([]string, n)
	seen := map[string]bool{}
	for i := range ids {
		ids[i] = of(i)
		if ids[i] == "" || seen[ids[i]] {
			return nil, false
		}
		seen[ids[i]] = true
	}
	return ids, true
}

func identityOfValue(v yamlx.Value) string {
	if v.Kind != yamlx.Map {
		return ""
	}
	keys := make([]string, len(v.Fields))
	id := ""
	for i, field := range v.Fields {
		keys[i] = field.Key
		if field.Key == "id" && field.Value.Kind == yamlx.String {
			id = field.Value.Text
		}
	}
	return identity(id, keys)
}

func identityOfNode(n *loader.Node) string {
	if n.Kind != loader.ObjectNode {
		return ""
	}
	keys := make([]string, len(n.Fields))
	id := ""
	for i, field := range n.Fields {
		keys[i] = field.Key
		if field.Key == "id" && field.Value.Kind == loader.StringNode {
			id = field.Value.Text
		}
	}
	return identity(id, keys)
}

// identity names an entry or a layer operation: its id and, for an operation, its
// kind, as the JSON codec pairs them.
func identity(id string, keys []string) string {
	kind := "override"
	for _, key := range keys {
		switch key {
		case "insert", "merge", "remove":
			kind = key
		}
	}
	if id == "" && kind == "override" {
		return ""
	}
	return kind + "\x00" + id
}

func sameScalar(old yamlx.Value, want *loader.Node) bool {
	switch want.Kind {
	case loader.NullNode:
		return old.Kind == yamlx.Null
	case loader.BoolNode:
		return old.Kind == yamlx.Bool && old.Bool == want.Bool
	case loader.StringNode:
		return old.Kind == yamlx.String && old.Text == want.Text
	case loader.ExprNode:
		return old.Kind == yamlx.Tagged && old.Tag == ExprTag && old.Text == want.Text
	case loader.NumberNode:
		w := toValue(want)
		return old.Kind == w.Kind && old.Int == w.Int && old.Float == w.Float
	}
	return false
}

// same reports whether two node trees hold the same data; object members compare
// by key, as JSON does.
func same(a, b *loader.Node) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case loader.BoolNode:
		return a.Bool == b.Bool
	case loader.NumberNode:
		x, y := toValue(a), toValue(b)
		return x.Kind == y.Kind && x.Int == y.Int && x.Float == y.Float
	case loader.StringNode, loader.ExprNode:
		return a.Text == b.Text
	case loader.ArrayNode:
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i := range a.Items {
			if !same(a.Items[i], b.Items[i]) {
				return false
			}
		}
		return true
	case loader.ObjectNode:
		if len(a.Fields) != len(b.Fields) {
			return false
		}
		byKey := make(map[string]*loader.Node, len(b.Fields))
		for _, field := range b.Fields {
			byKey[field.Key] = field.Value
		}
		for _, field := range a.Fields {
			other, ok := byKey[field.Key]
			if !ok || !same(field.Value, other) {
				return false
			}
		}
		return true
	}
	return true
}
