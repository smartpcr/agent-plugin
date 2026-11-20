package loader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// Position is a place in a source file. Line and Column are 1-based; Column counts
// characters, not bytes. The zero Position is unknown.
type Position struct {
	File   string
	Line   int
	Column int
	// Offset is the 0-based byte offset of the position in the file.
	Offset int
}

// IsValid reports whether the position names a line.
func (p Position) IsValid() bool { return p.Line > 0 }

// String formats the position as "file:line:column", or "" when it is unknown.
func (p Position) String() string {
	switch {
	case !p.IsValid():
		return p.File
	case p.File == "":
		return fmt.Sprintf("%d:%d", p.Line, p.Column)
	default:
		return fmt.Sprintf("%s:%d:%d", p.File, p.Line, p.Column)
	}
}

// Issue is one problem found in a document or an entry: where it is, the path of the
// offending value inside the document (such as "[0].children[1].name"), and what is
// wrong with it.
type Issue struct {
	Pos     Position
	Path    string
	Message string
}

// Error formats the issue as "file:line:column: path: message", leaving out the
// parts that are unknown.
func (i Issue) Error() string {
	var b strings.Builder
	if pos := i.Pos.String(); pos != "" {
		b.WriteString(pos)
		b.WriteString(": ")
	}
	if i.Path != "" {
		b.WriteString(i.Path)
		b.WriteString(": ")
	}
	b.WriteString(i.Message)
	return b.String()
}

// ParseError rejects a whole document: its bytes are not well-formed, or it is not an
// array of entries.
type ParseError struct {
	Issues []Issue
}

func (e *ParseError) Error() string {
	texts := make([]string, len(e.Issues))
	for i, issue := range e.Issues {
		texts[i] = issue.Error()
	}
	return strings.Join(texts, "; ")
}

// NodeKind is the kind of a decoded value.
type NodeKind int

const (
	NullNode NodeKind = iota
	BoolNode
	NumberNode
	StringNode
	ArrayNode
	ObjectNode
	// ExprNode is an expression: {"$expr": "<source>"} or "${{ <source> }}" in JSON,
	// a tagged scalar in a codec that has tags.
	ExprNode
)

var nodeKindNames = [...]string{"null", "boolean", "number", "string", "array", "object", "expression"}

func (k NodeKind) String() string {
	if int(k) >= 0 && int(k) < len(nodeKindNames) {
		return nodeKindNames[k]
	}
	return fmt.Sprintf("NodeKind(%d)", int(k))
}

// Node is one decoded value with the position it starts at. A codec decodes a file
// into a tree of nodes; the loader builds entries from the tree, so every issue it
// finds can name the file, line and column of the value at fault.
type Node struct {
	Kind NodeKind
	Pos  Position
	// Bool is the value of a BoolNode.
	Bool bool
	// Text is the value of a StringNode, the literal of a NumberNode and the source of
	// an ExprNode.
	Text string
	// Items are the elements of an ArrayNode.
	Items []*Node
	// Fields are the members of an ObjectNode, in document order, duplicates included.
	Fields []*Field
	// Raw is the node's own JSON text, when the codec decoded JSON; JSON uses it so a
	// value keeps the form it was written in. It is nil when the text holds comments
	// (JSONC), so JSON never returns text a JSON decoder rejects.
	Raw []byte
	// End is the 0-based byte offset just past the node's text, when the codec knows
	// it (zero otherwise). With Pos.Offset it is the span a layer write edits in place.
	End int
}

// Field is one member of an object node.
type Field struct {
	Key    string
	KeyPos Position
	Value  *Node
}

// JSON returns the node as JSON. An expression becomes {"$expr": "<source>"}, unless
// the node keeps the JSON text it was decoded from.
func (n *Node) JSON() json.RawMessage {
	if n.Raw != nil {
		return bytes.Clone(n.Raw)
	}
	var b bytes.Buffer
	n.writeJSON(&b)
	return b.Bytes()
}

func (n *Node) writeJSON(b *bytes.Buffer) {
	quote := func(s string) {
		encoded, _ := json.Marshal(s)
		b.Write(encoded)
	}
	switch n.Kind {
	case NullNode:
		b.WriteString("null")
	case BoolNode:
		if n.Bool {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case NumberNode:
		b.WriteString(n.Text)
	case StringNode:
		quote(n.Text)
	case ExprNode:
		b.WriteString(`{"` + exprKey + `":`)
		quote(n.Text)
		b.WriteByte('}')
	case ArrayNode:
		b.WriteByte('[')
		for i, item := range n.Items {
			if i > 0 {
				b.WriteByte(',')
			}
			item.writeJSON(b)
		}
		b.WriteByte(']')
	case ObjectNode:
		b.WriteByte('{')
		for i, field := range n.Fields {
			if i > 0 {
				b.WriteByte(',')
			}
			quote(field.Key)
			b.WriteByte(':')
			field.Value.writeJSON(b)
		}
		b.WriteByte('}')
	}
}

// Codec is the seam between a file format and the loader. Decode turns the bytes of a
// file into nodes that carry file, line and column, and reports malformed input as a
// *ParseError with positions; Encode renders entries back into the format. previous,
// when not nil, is the document last decoded from the same file, so a codec that can
// keep formatting (comments, for example) edits it rather than starting afresh.
type Codec interface {
	Decode(file string, data []byte) (*Node, error)
	Encode(previous *Document, entries []EntryOptions) ([]byte, error)
}

// JSON is the codec for ".json" files.
var JSON Codec = jsonCodec{}

// JSONC is the codec for ".jsonc" files: JSON with "//" and "/* */" comments. A
// layer written back through it keeps its comments and formatting (see LayerCodec).
var JSONC Codec = jsonCodec{comments: true}

var codecs = struct {
	sync.RWMutex
	byExt map[string]Codec
}{byExt: map[string]Codec{".json": JSON, ".jsonc": JSONC}}

// RegisterCodec makes codec the one used for files with the extension ext (such as
// ".yaml"). It panics if codec is nil or ext is already registered.
func RegisterCodec(ext string, codec Codec) {
	if codec == nil {
		panic("loader: RegisterCodec codec is nil")
	}
	ext = strings.ToLower(ext)
	codecs.Lock()
	defer codecs.Unlock()
	if _, dup := codecs.byExt[ext]; dup {
		panic(fmt.Sprintf("loader: RegisterCodec called twice for %q", ext))
	}
	codecs.byExt[ext] = codec
}

// CodecFor returns the codec registered for the extension of path, or JSON.
func CodecFor(path string) Codec {
	codecs.RLock()
	defer codecs.RUnlock()
	if codec, ok := codecs.byExt[strings.ToLower(filepath.Ext(path))]; ok {
		return codec
	}
	return JSON
}

// Document is a decoded file: its node tree and the entries built from it. Entries are
// decoded leniently: an entry with a wrong-typed or unknown field is kept, with its
// issues, so validation can report all of them and a write-back does not lose it.
type Document struct {
	File    string
	Root    *Node
	Entries []EntryOptions
}

// Issues returns the decoding issues of every entry, in document order.
func (d *Document) Issues() []Issue {
	var issues []Issue
	var visit func(entries []EntryOptions)
	visit = func(entries []EntryOptions) {
		for _, entry := range entries {
			issues = append(issues, entry.issues...)
			visit(entry.Children)
		}
	}
	visit(d.Entries)
	return issues
}

// Parse decodes data with codec and builds its entries. The error is a *ParseError
// when the bytes are malformed or are not an array of entries; an entry's own faults
// are not errors here, they are returned by Document.Issues and Service.Validate.
func Parse(codec Codec, file string, data []byte) (*Document, error) {
	if codec == nil {
		codec = CodecFor(file)
	}
	root, err := codec.Decode(file, data)
	if err != nil {
		return nil, err
	}
	if root == nil || root.Kind != ArrayNode {
		issue := Issue{Message: "must be an array of entries"}
		if root != nil {
			issue.Pos = root.Pos
			issue.Message += ", got " + root.Kind.String()
		}
		return nil, &ParseError{Issues: []Issue{issue}}
	}
	entries := decodeEntries(root.Items, "")
	if entries == nil {
		entries = []EntryOptions{}
	}
	return &Document{File: file, Root: root, Entries: entries}, nil
}

// ------------------------------------------------------------------ JSON

type jsonCodec struct {
	comments bool
}

func (c jsonCodec) Decode(file string, data []byte) (*Node, error) {
	p := &jsonParser{file: file, data: data, line: 1, comments: c.comments}
	p.skipSpace()
	if p.i >= len(p.data) {
		return nil, p.fail("unexpected end of input")
	}
	node, err := p.value(0)
	if err != nil {
		return nil, err
	}
	p.skipSpace()
	if p.i < len(p.data) {
		return nil, p.fail("unexpected trailing data")
	}
	return node, nil
}

func (jsonCodec) Encode(_ *Document, entries []EntryOptions) ([]byte, error) {
	if entries == nil {
		entries = []EntryOptions{}
	}
	encoded, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

// maxDepth bounds nesting, so hostile input cannot exhaust the stack.
const maxDepth = 512

type jsonParser struct {
	file      string
	data      []byte
	i         int
	line      int
	lineStart int
	// colOffset and colRunes cache the column at a byte offset on the current line,
	// so pos() counts only the bytes since its previous call instead of the whole line.
	colOffset int
	colRunes  int
	// comments allows "//" and "/* */" comments wherever whitespace may appear;
	// commentCount counts the comments skipped so far.
	comments     bool
	commentCount int
}

func (p *jsonParser) pos() Position {
	if p.colOffset < p.lineStart || p.colOffset > p.i {
		p.colOffset, p.colRunes = p.lineStart, 0
	}
	p.colRunes += utf8.RuneCount(p.data[p.colOffset:p.i])
	p.colOffset = p.i
	return Position{
		File:   p.file,
		Line:   p.line,
		Column: p.colRunes + 1,
		Offset: p.i,
	}
}

func (p *jsonParser) fail(format string, args ...any) error {
	return &ParseError{Issues: []Issue{{Pos: p.pos(), Message: fmt.Sprintf(format, args...)}}}
}

func (p *jsonParser) skipSpace() {
	for p.i < len(p.data) {
		switch p.data[p.i] {
		case '\n':
			p.i++
			p.line++
			p.lineStart = p.i
		case ' ', '\t', '\r':
			p.i++
		case '/':
			if !p.comments || !p.skipComment() {
				return
			}
		default:
			return
		}
	}
}

// skipComment skips the comment at p.i, if there is one. An unterminated block
// comment runs to the end of the input, where the caller reports what is missing.
func (p *jsonParser) skipComment() bool {
	if p.i+1 >= len(p.data) {
		return false
	}
	switch p.data[p.i+1] {
	case '/':
		for p.i < len(p.data) && p.data[p.i] != '\n' {
			p.i++
		}
	case '*':
		p.i += 2
		for p.i < len(p.data) {
			if p.data[p.i] == '*' && p.i+1 < len(p.data) && p.data[p.i+1] == '/' {
				p.i += 2
				break
			}
			if p.data[p.i] == '\n' {
				p.line++
				p.lineStart = p.i + 1
			}
			p.i++
		}
	default:
		return false
	}
	p.commentCount++
	return true
}

func (p *jsonParser) value(depth int) (*Node, error) {
	if depth > maxDepth {
		return nil, p.fail("nesting exceeds %d levels", maxDepth)
	}
	if p.i >= len(p.data) {
		return nil, p.fail("unexpected end of input")
	}
	start, pos, comments := p.i, p.pos(), p.commentCount
	var node *Node
	var err error
	switch c := p.data[p.i]; {
	case c == '{':
		node, err = p.object(depth)
	case c == '[':
		node, err = p.array(depth)
	case c == '"':
		var text string
		if text, err = p.str(); err == nil {
			node = &Node{Kind: StringNode, Text: text}
			if source, ok := templateSource(text); ok {
				node = &Node{Kind: ExprNode, Text: source}
			}
		}
	case c == 't' || c == 'f' || c == 'n':
		node, err = p.literal()
	case c == '-' || (c >= '0' && c <= '9'):
		node, err = p.number()
	default:
		return nil, p.fail("unexpected character %q", rune(c))
	}
	if err != nil {
		return nil, err
	}
	node.Pos = pos
	node.End = p.i
	if p.commentCount == comments {
		node.Raw = p.data[start:p.i]
	}
	return node, nil
}

func (p *jsonParser) object(depth int) (*Node, error) {
	node := &Node{Kind: ObjectNode}
	p.i++ // {
	p.skipSpace()
	if p.i < len(p.data) && p.data[p.i] == '}' {
		p.i++
		return node, nil
	}
	for {
		p.skipSpace()
		if p.i >= len(p.data) || p.data[p.i] != '"' {
			return nil, p.fail("expected a string key")
		}
		keyPos := p.pos()
		key, err := p.str()
		if err != nil {
			return nil, err
		}
		p.skipSpace()
		if p.i >= len(p.data) || p.data[p.i] != ':' {
			return nil, p.fail("expected ':' after object key")
		}
		p.i++
		p.skipSpace()
		value, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		node.Fields = append(node.Fields, &Field{Key: key, KeyPos: keyPos, Value: value})
		p.skipSpace()
		if p.i >= len(p.data) {
			return nil, p.fail("unexpected end of input in object")
		}
		switch p.data[p.i] {
		case ',':
			p.i++
		case '}':
			p.i++
			if len(node.Fields) == 1 && node.Fields[0].Key == exprKey && node.Fields[0].Value.Kind == StringNode {
				return &Node{Kind: ExprNode, Text: node.Fields[0].Value.Text}, nil
			}
			return node, nil
		default:
			return nil, p.fail("expected ',' or '}' in object")
		}
	}
}

func (p *jsonParser) array(depth int) (*Node, error) {
	node := &Node{Kind: ArrayNode}
	p.i++ // [
	p.skipSpace()
	if p.i < len(p.data) && p.data[p.i] == ']' {
		p.i++
		return node, nil
	}
	for {
		p.skipSpace()
		item, err := p.value(depth + 1)
		if err != nil {
			return nil, err
		}
		node.Items = append(node.Items, item)
		p.skipSpace()
		if p.i >= len(p.data) {
			return nil, p.fail("unexpected end of input in array")
		}
		switch p.data[p.i] {
		case ',':
			p.i++
		case ']':
			p.i++
			return node, nil
		default:
			return nil, p.fail("expected ',' or ']' in array")
		}
	}
}

// str scans a string literal and decodes it with encoding/json, which checks escapes.
func (p *jsonParser) str() (string, error) {
	start := p.i
	j := p.i + 1
	for {
		if j >= len(p.data) {
			return "", p.fail("unterminated string")
		}
		c := p.data[j]
		if c == '"' {
			break
		}
		if c < 0x20 {
			p.i = j
			return "", p.fail("control character in string")
		}
		if c == '\\' {
			j++
		}
		j++
	}
	var text string
	if err := json.Unmarshal(p.data[start:j+1], &text); err != nil {
		return "", p.fail("invalid string: %v", err)
	}
	p.i = j + 1
	return text, nil
}

func (p *jsonParser) literal() (*Node, error) {
	for _, candidate := range []struct {
		text string
		node Node
	}{
		{"true", Node{Kind: BoolNode, Bool: true}},
		{"false", Node{Kind: BoolNode}},
		{"null", Node{Kind: NullNode}},
	} {
		end := p.i + len(candidate.text)
		if end <= len(p.data) && string(p.data[p.i:end]) == candidate.text && (end == len(p.data) || !isWordByte(p.data[end])) {
			p.i = end
			node := candidate.node
			return &node, nil
		}
	}
	return nil, p.fail("invalid literal")
}

func (p *jsonParser) number() (*Node, error) {
	start := p.i
	j := p.i
	for j < len(p.data) && strings.IndexByte("+-0123456789.eE", p.data[j]) >= 0 {
		j++
	}
	var number json.Number
	if err := json.Unmarshal(p.data[start:j], &number); err != nil {
		return nil, p.fail("invalid number %q", p.data[start:j])
	}
	p.i = j
	return &Node{Kind: NumberNode, Text: string(p.data[start:j])}, nil
}

func isWordByte(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// ---------------------------------------------------------------- layers

// LayerCodec is a Codec that can write the operations of a layer (see Layer).
// previous, when not nil, is the layer document last read from the same file; a codec
// that can keep formatting edits it, so the operations and values a write does not
// change keep their text, comments included.
type LayerCodec interface {
	Codec
	EncodeLayer(previous *LayerDocument, ops []Op) ([]byte, error)
}

// EncodeLayer writes ops as a JSON array. Given the document last read, it edits that
// document's text in place: an unchanged value keeps its bytes, a changed value is
// replaced where it stands, and operations are matched to the ones they rewrite, so
// comments (in JSONC) and formatting around them survive.
func (c jsonCodec) EncodeLayer(previous *LayerDocument, ops []Op) ([]byte, error) {
	if ops == nil {
		ops = []Op{}
	}
	compact, err := json.Marshal(ops)
	if err != nil {
		return nil, err
	}
	if previous != nil && previous.Root != nil && len(previous.Data) > 0 {
		want, err := JSON.Decode("", compact)
		if err != nil {
			return nil, err
		}
		e := &jsonEditor{data: previous.Data}
		e.edit(previous.Root, want, opIdentity)
		edited := e.result()
		// keep the edit only if it reads back as exactly the operations written
		if back, err := c.Decode("", edited); err == nil && sameJSON(back, want) {
			return edited, nil
		}
	}
	var b bytes.Buffer
	if err := json.Indent(&b, compact, "", "  "); err != nil {
		return nil, err
	}
	b.WriteByte('\n')
	return b.Bytes(), nil
}

// opIdentity names the operation a layer item is, so an edit pairs each operation
// with the one it rewrites: its kind and its target.
func opIdentity(node *Node) string {
	if node.Kind != ObjectNode {
		return ""
	}
	id, kind := "", string(OpOverride)
	for _, field := range node.Fields {
		switch field.Key {
		case "id":
			id = field.Value.Text
		case "insert", "merge", "remove":
			kind = field.Key
		}
	}
	return kind + "\x00" + id
}

// sameJSON reports whether two nodes hold the same value.
func sameJSON(a, b *Node) bool {
	if a.Kind == ExprNode || b.Kind == ExprNode {
		return a.Kind == b.Kind && a.Text == b.Text
	}
	var left, right any
	if decodeAny(a.JSON(), &left) != nil || decodeAny(b.JSON(), &right) != nil {
		return false
	}
	return reflect.DeepEqual(left, right)
}

func decodeAny(data []byte, into *any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	return decoder.Decode(into)
}

type jsonEdit struct {
	start, end int
	text       string
}

// jsonEditor collects non-overlapping edits of a document's text.
type jsonEditor struct {
	data  []byte
	edits []jsonEdit
}

func (e *jsonEditor) add(start, end int, text string) {
	e.edits = append(e.edits, jsonEdit{start: start, end: end, text: text})
}

func (e *jsonEditor) result() []byte {
	sort.SliceStable(e.edits, func(i, j int) bool { return e.edits[i].start < e.edits[j].start })
	var b bytes.Buffer
	at := 0
	for _, edit := range e.edits {
		b.Write(e.data[at:edit.start])
		b.WriteString(edit.text)
		at = edit.end
	}
	b.Write(e.data[at:])
	return b.Bytes()
}

// edit turns the text of old into the value want. identity, when not nil, pairs the
// items of an array by what they are rather than by their index.
func (e *jsonEditor) edit(old, want *Node, identity func(*Node) string) {
	if sameJSON(old, want) {
		return
	}
	switch {
	case old.Kind == ObjectNode && want.Kind == ObjectNode && len(old.Fields) > 0 && uniqueKeys(old):
		e.editObject(old, want)
	case old.Kind == ArrayNode && want.Kind == ArrayNode && len(old.Items) > 0:
		e.editArray(old, want, identity)
	default:
		e.add(old.Pos.Offset, old.End, e.render(want, e.indentOf(old.Pos.Offset), e.multiline(old)))
	}
}

func uniqueKeys(node *Node) bool {
	seen := map[string]bool{}
	for _, field := range node.Fields {
		if seen[field.Key] {
			return false
		}
		seen[field.Key] = true
	}
	return true
}

func (e *jsonEditor) editObject(old, want *Node) {
	wanted := map[string]*Node{}
	for _, field := range want.Fields {
		wanted[field.Key] = field.Value
	}
	spans := make([][2]int, len(old.Fields))
	keep := make([]bool, len(old.Fields))
	present := map[string]bool{}
	for i, field := range old.Fields {
		spans[i] = [2]int{field.KeyPos.Offset, field.Value.End}
		present[field.Key] = true
		if value, ok := wanted[field.Key]; ok {
			keep[i] = true
			e.edit(field.Value, value, nil)
		}
	}
	var added []*Field
	for _, field := range want.Fields {
		if !present[field.Key] {
			added = append(added, field)
		}
	}
	if !slices.Contains(keep, true) {
		e.add(old.Pos.Offset, old.End, e.render(want, e.indentOf(old.Pos.Offset), e.multiline(old)))
		return
	}
	e.members(old, spans, keep, len(added), func(i int, indent string, multiline bool) string {
		key, _ := json.Marshal(added[i].Key)
		return string(key) + ": " + e.render(added[i].Value, indent, multiline)
	})
}

func (e *jsonEditor) editArray(old, want *Node, identity func(*Node) string) {
	spans := make([][2]int, len(old.Items))
	keep := make([]bool, len(old.Items))
	for i, item := range old.Items {
		spans[i] = [2]int{item.Pos.Offset, item.End}
	}
	var added []*Node
	if identity == nil {
		for i, item := range want.Items {
			if i < len(old.Items) {
				keep[i] = true
				e.edit(old.Items[i], item, nil)
			} else {
				added = append(added, item)
			}
		}
	} else {
		// an identical item first, then one that is the same operation
		used := make([]bool, len(old.Items))
		pair := make([]int, len(want.Items))
		for i, item := range want.Items {
			pair[i] = -1
			for j, candidate := range old.Items {
				if !used[j] && sameJSON(candidate, item) {
					pair[i], used[j] = j, true
					break
				}
			}
		}
		for i, item := range want.Items {
			if pair[i] >= 0 {
				continue
			}
			for j, candidate := range old.Items {
				if !used[j] && identity(candidate) != "" && identity(candidate) == identity(item) {
					pair[i], used[j] = j, true
					break
				}
			}
		}
		for i, item := range want.Items {
			if j := pair[i]; j >= 0 {
				keep[j] = true
				e.edit(old.Items[j], item, nil)
			} else {
				added = append(added, item)
			}
		}
	}
	if !slices.Contains(keep, true) {
		e.add(old.Pos.Offset, old.End, e.render(want, e.indentOf(old.Pos.Offset), e.multiline(old)))
		return
	}
	e.members(old, spans, keep, len(added), func(i int, indent string, multiline bool) string {
		return e.render(added[i], indent, multiline)
	})
}

// members deletes the members of a container that are not kept and appends count new
// ones after the last kept member, in the container's own layout.
func (e *jsonEditor) members(container *Node, spans [][2]int, keep []bool, count int, render func(i int, indent string, multiline bool) string) {
	last := -1
	for i := range spans {
		if keep[i] {
			last = i
		}
	}
	for i := 0; i < last; i++ {
		if !keep[i] {
			e.deleteMember(spans[i])
		}
	}
	multiline := e.multiline(container)
	indent := e.indentOf(spans[last][0])
	separator := " "
	if multiline {
		separator = "\n" + indent
	}
	anchor := spans[last][1]
	tail := spans[len(spans)-1][1]
	if comma, commentEnd := e.trailer(anchor); multiline && commentEnd >= 0 {
		// a comment trailing the last kept member stays on its line: the comma moves
		// or goes, and new members follow the comment
		if count > 0 && comma < 0 {
			e.add(anchor, anchor, ",")
		} else if count == 0 && comma >= 0 {
			e.add(comma, comma+1, "")
		}
		var insertion strings.Builder
		for i := range count {
			if i > 0 {
				insertion.WriteString(",")
			}
			insertion.WriteString(separator)
			insertion.WriteString(render(i, indent, multiline))
		}
		end := max(commentEnd, tail)
		if insertion.Len() > 0 || end != commentEnd {
			e.add(commentEnd, end, insertion.String())
		}
		return
	}
	var insertion strings.Builder
	for i := range count {
		insertion.WriteString(",")
		insertion.WriteString(separator)
		insertion.WriteString(render(i, indent, multiline))
	}
	// the members after the last kept one go, with the comma before them
	if insertion.Len() > 0 || tail != anchor {
		e.add(anchor, tail, insertion.String())
	}
}

// trailer finds what follows a member on its line: the offset of its comma (-1 if
// none) and the end of a // comment after it (-1 if none).
func (e *jsonEditor) trailer(at int) (comma, commentEnd int) {
	comma, commentEnd = -1, -1
	skipBlanks := func() {
		for at < len(e.data) && (e.data[at] == ' ' || e.data[at] == '\t') {
			at++
		}
	}
	skipBlanks()
	if at < len(e.data) && e.data[at] == ',' {
		comma = at
		at++
		skipBlanks()
	}
	if at+1 < len(e.data) && e.data[at] == '/' && e.data[at+1] == '/' {
		for at < len(e.data) && e.data[at] != '\n' && e.data[at] != '\r' {
			at++
		}
		commentEnd = at
	}
	return comma, commentEnd
}

// deleteMember removes a member that is followed by another: its text, its comma and,
// when it stands on lines of its own, those lines with a comment that trails it.
func (e *jsonEditor) deleteMember(span [2]int) {
	start, end := span[0], span[1]
	lineStart := start
	for lineStart > 0 && (e.data[lineStart-1] == ' ' || e.data[lineStart-1] == '\t') {
		lineStart--
	}
	ownLine := lineStart == 0 || e.data[lineStart-1] == '\n'
	if ownLine {
		start = lineStart
	}
	skipBlanks := func() {
		for end < len(e.data) && (e.data[end] == ' ' || e.data[end] == '\t') {
			end++
		}
	}
	skipBlanks()
	if end < len(e.data) && e.data[end] == ',' {
		end++
	}
	skipBlanks()
	if ownLine {
		if end+1 < len(e.data) && e.data[end] == '/' && e.data[end+1] == '/' {
			for end < len(e.data) && e.data[end] != '\n' {
				end++
			}
		}
		if end < len(e.data) && e.data[end] == '\r' {
			end++
		}
		if end < len(e.data) && e.data[end] == '\n' {
			end++
		}
	}
	e.add(start, end, "")
}

// multiline reports whether the node's text spans more than one line.
func (e *jsonEditor) multiline(node *Node) bool {
	return bytes.IndexByte(e.data[node.Pos.Offset:node.End], '\n') >= 0
}

// indentOf returns the blanks that open the line holding offset.
func (e *jsonEditor) indentOf(offset int) string {
	start := offset
	for start > 0 && e.data[start-1] != '\n' {
		start--
	}
	end := start
	for end < offset && (e.data[end] == ' ' || e.data[end] == '\t') {
		end++
	}
	return string(e.data[start:end])
}

// render writes a value: compact, or indented under indent when its place is laid
// out over several lines.
func (e *jsonEditor) render(node *Node, indent string, multiline bool) string {
	compact := node.JSON()
	if !multiline || (node.Kind != ObjectNode && node.Kind != ArrayNode) {
		return string(compact)
	}
	var b bytes.Buffer
	if json.Indent(&b, compact, indent, "  ") != nil {
		return string(compact)
	}
	return b.String()
}
