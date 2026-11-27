package yamlx

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	yaml "go.yaml.in/yaml/v4"
)

type opKind uint8

const (
	opSet opKind = iota
	opDelete
	opInsert
)

// Op is one edit of a document (DH-LIB-YAML-R6). A path names a value from the top of
// the document: a string selects a mapping key, an int a sequence index.
type Op struct {
	kind  opKind
	path  []any
	index int
	value any
}

// Set sets the value at path: it replaces the key's value or the sequence item, or
// appends the key to its mapping when it is missing. An empty path replaces the
// whole document. value is plain data (see Dump), a TaggedScalar or a Value.
func Set(path []any, value any) Op {
	return Op{kind: opSet, path: slices.Clone(path), value: value}
}

// Delete deletes the mapping key or the sequence item at path. Its comments move to
// the next key or item, or to the end of the file.
func Delete(path []any) Op {
	return Op{kind: opDelete, path: slices.Clone(path)}
}

// Insert inserts value into the sequence at path, before index; an index out of
// range (a negative one included) appends.
func Insert(path []any, index int, value any) Op {
	return Op{kind: opInsert, path: slices.Clone(path), index: index, value: value}
}

// encode renders a node tree; a variable so tests can stand in a faulty encoder.
var encode = func(n *yaml.Node) ([]byte, error) {
	return yaml.Dump(n, yaml.WithIndent(2), yaml.WithLineWidth(-1), yaml.WithCompactSeqIndent(false))
}

// ErrUnverified is returned (wrapped) by Edit when the rendered text does not read
// back as the intended data with every comment of the input (DH-LIB-YAML-R8).
var ErrUnverified = errors.New("yamlx: rendered text does not match the edit")

// Edit applies ops to a copy of the document and returns the new text. The text is
// parsed again before it is returned; if it does not hold exactly the intended data
// and every comment of the input, Edit returns an error and no text. The document
// itself is not changed.
func (d *Document) Edit(ops ...Op) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("yamlx: %s: internal YAML error: %v", d.file, r)
		}
	}()
	doc, blank := d.copyTree()
	for _, op := range ops {
		if err := apply(doc, blank, op); err != nil {
			return nil, fmt.Errorf("yamlx: %s: %w", d.file, err)
		}
	}
	want := Value{Kind: Null}
	if len(doc.Content) > 0 {
		if want, err = newWalker(d.file, nil, d.cfg).value(doc.Content[0], 1); err != nil {
			return nil, fmt.Errorf("yamlx: %s: edit makes an invalid document: %w", d.file, err)
		}
	}
	text, err := render(d.file, doc, blank)
	if err != nil {
		return nil, fmt.Errorf("yamlx: %s: %w", d.file, err)
	}
	back, err := Parse(d.file, text, d.options()...)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %v", ErrUnverified, d.file, err)
	}
	if !Equal(back.value, want) {
		return nil, fmt.Errorf("%w: %s: the data read back differs", ErrUnverified, d.file)
	}
	if missing := missingComments(comments(d.root), comments(back.root)); missing != "" {
		return nil, fmt.Errorf("%w: %s: comment %q is lost", ErrUnverified, d.file, missing)
	}
	return text, nil
}

func (d *Document) options() []Option {
	opts := []Option{WithLimits(d.cfg.maxBytes, d.cfg.maxDepth)}
	for tag := range d.cfg.tags {
		opts = append(opts, WithTag(tag))
	}
	return opts
}

// copyTree deep-copies the node tree and the blank-line marks.
func (d *Document) copyTree() (*yaml.Node, map[*yaml.Node]bool) {
	blank := map[*yaml.Node]bool{}
	if d.root == nil {
		return &yaml.Node{Kind: yaml.DocumentNode}, blank
	}
	var clone func(n *yaml.Node) *yaml.Node
	clone = func(n *yaml.Node) *yaml.Node {
		c := *n
		c.Content = make([]*yaml.Node, len(n.Content))
		for i, child := range n.Content {
			c.Content[i] = clone(child)
		}
		if d.blank[n] {
			blank[&c] = true
		}
		return &c
	}
	return clone(d.root), blank
}

func apply(doc *yaml.Node, blank map[*yaml.Node]bool, op Op) error {
	for _, step := range op.path {
		switch step.(type) {
		case string, int:
		default:
			return fmt.Errorf("path %s: element %v is not a key or an index", pathString(op.path), step)
		}
	}
	if op.kind == opSet && len(op.path) == 0 {
		n, err := build(op.value)
		if err != nil {
			return err
		}
		if len(doc.Content) > 0 {
			keepComments(doc.Content[0], n, n)
		}
		doc.Content = []*yaml.Node{n}
		return nil
	}
	if len(doc.Content) == 0 {
		return fmt.Errorf("path %s: the document is empty", pathString(op.path))
	}
	top := doc.Content[0]
	if op.kind == opInsert {
		seq, err := find(top, op.path)
		if err != nil {
			return err
		}
		if seq.Kind != yaml.SequenceNode {
			return fmt.Errorf("path %s: insert needs a sequence", pathString(op.path))
		}
		n, err := build(op.value)
		if err != nil {
			return err
		}
		index := op.index
		if index < 0 || index > len(seq.Content) {
			index = len(seq.Content)
		}
		if seq == top {
			blank[n] = true
			if index < len(seq.Content) {
				blank[seq.Content[index]] = true
			}
		}
		seq.Content = slices.Insert(seq.Content, index, n)
		return nil
	}
	parent, err := find(top, op.path[:len(op.path)-1])
	if err != nil {
		return err
	}
	last := op.path[len(op.path)-1]
	switch parent.Kind {
	case yaml.MappingNode:
		key, ok := last.(string)
		if !ok {
			return fmt.Errorf("path %s: %v indexes a mapping", pathString(op.path), last)
		}
		at := -1
		for i := 0; i+1 < len(parent.Content); i += 2 {
			if parent.Content[i].Value == key {
				at = i
				break
			}
		}
		if op.kind == opDelete {
			if at < 0 {
				return fmt.Errorf("path %s: no such key", pathString(op.path))
			}
			moved := append(commentLines(parent.Content[at]), commentLines(parent.Content[at+1])...)
			parent.Content = slices.Delete(parent.Content, at, at+2)
			var next *yaml.Node
			if at < len(parent.Content) {
				next = parent.Content[at]
			}
			moveComments(doc, top, op.path, next, moved)
			return nil
		}
		n, err := build(op.value)
		if err != nil {
			return err
		}
		if at >= 0 {
			keepComments(parent.Content[at+1], n, parent.Content[at])
			parent.Content[at+1] = n
			return nil
		}
		k := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
		if _, problem := typePlain(key); problem != "" || mustQuote(key) {
			k.Style = yaml.DoubleQuotedStyle
		}
		if parent == top {
			blank[k] = true
		}
		parent.Content = append(parent.Content, k, n)
		return nil
	case yaml.SequenceNode:
		index, ok := last.(int)
		if !ok || index < 0 || index >= len(parent.Content) {
			return fmt.Errorf("path %s: no such item", pathString(op.path))
		}
		if op.kind == opDelete {
			moved := commentLines(parent.Content[index])
			parent.Content = slices.Delete(parent.Content, index, index+1)
			var next *yaml.Node
			if index < len(parent.Content) {
				next = parent.Content[index]
			}
			moveComments(doc, top, op.path, next, moved)
			return nil
		}
		n, err := build(op.value)
		if err != nil {
			return err
		}
		old := parent.Content[index]
		keepComments(old, n, n)
		if blank[old] {
			blank[n] = true
		}
		parent.Content[index] = n
		return nil
	default:
		return fmt.Errorf("path %s: %s is not a mapping or a sequence", pathString(op.path[:len(op.path)-1]), kindOf(parent))
	}
}

// keepComments carries the comments of a replaced value: onto the new scalar when a
// scalar replaces a scalar, otherwise above holder (the key, or the new item).
func keepComments(old, n, holder *yaml.Node) {
	if old.Kind == yaml.ScalarNode && n.Kind == yaml.ScalarNode {
		n.HeadComment, n.LineComment, n.FootComment = old.HeadComment, old.LineComment, old.FootComment
		return
	}
	if lines := commentLines(old); len(lines) > 0 {
		holder.HeadComment = joinComments(holder.HeadComment, strings.Join(lines, "\n"))
	}
}

// moveComments puts the comments of the key or item deleted at path above the node
// that now follows it in document order: next, its container's next sibling, and so
// up; past the last node, at the end of the file. A foot comment of a nested
// container is not used: the encoder renders it outside the container.
func moveComments(doc, top *yaml.Node, path []any, next *yaml.Node, lines []string) {
	if len(lines) == 0 {
		return
	}
	text := strings.Join(lines, "\n")
	for depth := len(path) - 1; next == nil && depth > 0; depth-- {
		container, err := find(top, path[:depth-1])
		if err != nil {
			break
		}
		next = following(container, path[depth-1])
	}
	if next == nil {
		doc.FootComment = joinComments(text, doc.FootComment)
		return
	}
	next.HeadComment = joinComments(text, next.HeadComment)
}

// following returns the node after the child step of container: the next key of a
// mapping, the next item of a sequence, or nil.
func following(container *yaml.Node, step any) *yaml.Node {
	switch container.Kind {
	case yaml.MappingNode:
		key, _ := step.(string)
		for i := 0; i+3 < len(container.Content); i += 2 {
			if container.Content[i].Value == key {
				return container.Content[i+2]
			}
		}
	case yaml.SequenceNode:
		if index, ok := step.(int); ok && index+1 < len(container.Content) {
			return container.Content[index+1]
		}
	}
	return nil
}

func joinComments(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n" + b
	}
}

func find(n *yaml.Node, path []any) (*yaml.Node, error) {
	for i, step := range path {
		switch s := step.(type) {
		case string:
			if n.Kind != yaml.MappingNode {
				return nil, fmt.Errorf("path %s: %s is not a mapping", pathString(path[:i+1]), kindOf(n))
			}
			var found *yaml.Node
			for j := 0; j+1 < len(n.Content); j += 2 {
				if n.Content[j].Value == s {
					found = n.Content[j+1]
					break
				}
			}
			if found == nil {
				return nil, fmt.Errorf("path %s: no such key", pathString(path[:i+1]))
			}
			n = found
		case int:
			if n.Kind != yaml.SequenceNode {
				return nil, fmt.Errorf("path %s: %s is not a sequence", pathString(path[:i+1]), kindOf(n))
			}
			if s < 0 || s >= len(n.Content) {
				return nil, fmt.Errorf("path %s: no such item", pathString(path[:i+1]))
			}
			n = n.Content[s]
		}
	}
	return n, nil
}

func kindOf(n *yaml.Node) string {
	switch n.Kind {
	case yaml.MappingNode:
		return "a mapping"
	case yaml.SequenceNode:
		return "a sequence"
	default:
		return "a scalar"
	}
}

func pathString(path []any) string {
	if len(path) == 0 {
		return "(document)"
	}
	var b strings.Builder
	for _, step := range path {
		switch s := step.(type) {
		case int:
			fmt.Fprintf(&b, "[%d]", s)
		default:
			if b.Len() > 0 {
				b.WriteByte('.')
			}
			fmt.Fprint(&b, s)
		}
	}
	return b.String()
}

// render encodes the tree and restores the blank line above each marked top-level
// entry (DH-LIB-YAML-R7).
func render(file string, doc *yaml.Node, blank map[*yaml.Node]bool) ([]byte, error) {
	raw, err := encode(doc)
	if err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 || len(blank) == 0 {
		return raw, nil
	}
	back, err := load(file, raw)
	if err != nil || back == nil || len(back.Content) == 0 {
		return raw, nil
	}
	want, got := topEntries(doc.Content[0]), topEntries(back.Content[0])
	if len(want) != len(got) {
		return raw, nil
	}
	lines := strings.SplitAfter(string(raw), "\n")
	before := map[int]bool{}
	for i, entry := range want {
		if !blank[entry] {
			continue
		}
		line := firstLine(got[i])
		if line > 1 && line <= len(lines) && strings.TrimSpace(lines[line-2]) != "" {
			before[line] = true
		}
	}
	var b bytes.Buffer
	for i, line := range lines {
		if before[i+1] {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	return b.Bytes(), nil
}

// comments returns every comment line of a tree.
func comments(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	return commentLines(n)
}

func commentLines(n *yaml.Node) []string {
	var lines []string
	add := func(text string) {
		for _, line := range strings.Split(text, "\n") {
			if line = strings.TrimSpace(line); line != "" {
				lines = append(lines, line)
			}
		}
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		add(n.HeadComment)
		add(n.LineComment)
		for _, child := range n.Content {
			walk(child)
		}
		add(n.FootComment)
	}
	walk(n)
	return lines
}

// missingComments returns a comment line of want that got does not hold as often.
func missingComments(want, got []string) string {
	count := map[string]int{}
	for _, line := range got {
		count[line]++
	}
	for _, line := range want {
		if count[line] == 0 {
			return line
		}
		count[line]--
	}
	return ""
}

// Dump renders plain data with no source document (DH-LIB-YAML-R10): mapping keys in
// sorted order (a Value keeps its own order), two-space indentation, no anchors.
func Dump(v any) (out []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = nil, fmt.Errorf("yamlx: internal YAML error: %v", r)
		}
	}()
	n, err := build(v)
	if err != nil {
		return nil, err
	}
	return encode(&yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{n}})
}

// build turns plain data, a TaggedScalar or a Value into a node.
func build(v any) (*yaml.Node, error) {
	switch x := v.(type) {
	case nil:
		return scalar("!!null", "null"), nil
	case Value:
		return buildValue(x)
	case TaggedScalar:
		return &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.TaggedStyle, Tag: x.Tag, Value: x.Source}, nil
	case bool:
		return scalar("!!bool", strconv.FormatBool(x)), nil
	case string:
		return stringNode(x), nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return scalar("!!int", strconv.FormatInt(rv.Int(), 10)), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return scalar("!!int", strconv.FormatUint(rv.Uint(), 10)), nil
	case reflect.Float32, reflect.Float64:
		return scalar("!!float", formatFloat(rv.Float())), nil
	case reflect.String:
		return stringNode(rv.String()), nil
	case reflect.Bool:
		return scalar("!!bool", strconv.FormatBool(rv.Bool())), nil
	case reflect.Slice, reflect.Array:
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for i := 0; i < rv.Len(); i++ {
			item, err := build(rv.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, item)
		}
		return seq, nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("yamlx: cannot write %T: keys must be strings", v)
		}
		keys := make([]string, 0, rv.Len())
		for _, k := range rv.MapKeys() {
			keys = append(keys, k.String())
		}
		sort.Strings(keys)
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, k := range keys {
			value, err := build(rv.MapIndex(reflect.ValueOf(k).Convert(rv.Type().Key())).Interface())
			if err != nil {
				return nil, err
			}
			m.Content = append(m.Content, keyNode(k), value)
		}
		return m, nil
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return scalar("!!null", "null"), nil
		}
		return build(rv.Elem().Interface())
	}
	return nil, fmt.Errorf("yamlx: cannot write %T", v)
}

func buildValue(v Value) (*yaml.Node, error) {
	switch v.Kind {
	case Null:
		return build(nil)
	case Bool:
		return build(v.Bool)
	case Int:
		return build(v.Int)
	case Float:
		return build(v.Float)
	case String:
		return build(v.Text)
	case Tagged:
		return build(TaggedScalar{Tag: v.Tag, Source: v.Text})
	case Seq:
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, item := range v.Items {
			n, err := buildValue(item)
			if err != nil {
				return nil, err
			}
			seq.Content = append(seq.Content, n)
		}
		return seq, nil
	case Map:
		m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for _, field := range v.Fields {
			n, err := buildValue(field.Value)
			if err != nil {
				return nil, err
			}
			key := keyNode(field.Key)
			if field.Comment != "" {
				// A scalar or an empty (flow) collection carries the comment after
				// itself; a block collection carries it after its key.
				target := n
				if (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && len(n.Content) > 0 {
					target = key
				}
				target.LineComment = "# " + strings.ReplaceAll(field.Comment, "\n", " ")
			}
			m.Content = append(m.Content, key, n)
		}
		return m, nil
	}
	return nil, fmt.Errorf("yamlx: cannot write a value of kind %s", v.Kind)
}

func scalar(tag, text string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: text}
}

// stringNode quotes a string the dialect would not read back as that string.
func stringNode(s string) *yaml.Node {
	n := scalar("!!str", s)
	if v, problem := typePlain(s); problem != "" || v.Kind != String || mustQuote(s) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

func keyNode(k string) *yaml.Node {
	n := scalar("!!str", k)
	if _, problem := typePlain(k); problem != "" || mustQuote(k) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

// mustQuote reports a word the YAML module would read as a merge key.
func mustQuote(s string) bool { return s == "<<" }

func formatFloat(f float64) string {
	switch {
	case math.IsNaN(f):
		return ".nan"
	case math.IsInf(f, 1):
		return ".inf"
	case math.IsInf(f, -1):
		return "-.inf"
	}
	s := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(s, ".e") {
		s += ".0"
	}
	return s
}

// DecodeFrontmatter splits markdown into the fields of its "---" frontmatter and its
// body, under the strict dialect with the default limits. Markdown with no
// frontmatter has an empty mapping of fields and is all body.
func DecodeFrontmatter(markdown []byte) (fields Value, body []byte, err error) {
	text := markdown
	open := 0
	switch {
	case bytes.HasPrefix(text, []byte("---\n")):
		open = 4
	case bytes.HasPrefix(text, []byte("---\r\n")):
		open = 5
	default:
		return Value{Kind: Map, Pos: Pos{Line: 1, Column: 1}}, markdown, nil
	}
	rest := text[open:]
	end, after := -1, 0
	for i := 0; i <= len(rest); {
		j := bytes.IndexByte(rest[i:], '\n')
		line := rest[i:]
		if j >= 0 {
			line = rest[i : i+j]
		}
		if t := bytes.TrimRight(line, "\r"); string(t) == "---" || string(t) == "..." {
			end = i
			after = i + len(line)
			if j >= 0 {
				after++
			}
			break
		}
		if j < 0 {
			break
		}
		i += j + 1
	}
	if end < 0 {
		return Value{}, nil, &SyntaxError{Line: 1, Column: 1, Message: "frontmatter is not closed by ---"}
	}
	// keep the lines and byte offsets of the markdown file: the opening "---" line
	// becomes a blank line of the same length to YAML
	src := make([]byte, 0, open+end)
	src = append(src, bytes.Repeat([]byte(" "), open-1)...)
	src = append(src, '\n')
	src = append(src, rest[:end]...)
	doc, err := Parse("", src)
	if err != nil {
		return Value{}, nil, err
	}
	fields = doc.Value()
	if fields.Kind == Null {
		fields = Value{Kind: Map, Pos: fields.Pos}
	}
	if fields.Kind != Map {
		return Value{}, nil, &SyntaxError{Line: fields.Pos.Line, Column: fields.Pos.Column, Message: "frontmatter must be a mapping, got " + fields.Kind.String()}
	}
	return fields, rest[after:], nil
}
