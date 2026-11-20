package loader

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
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
	// value keeps the form it was written in.
	Raw []byte
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

var codecs = struct {
	sync.RWMutex
	byExt map[string]Codec
}{byExt: map[string]Codec{".json": JSON}}

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

type jsonCodec struct{}

func (jsonCodec) Decode(file string, data []byte) (*Node, error) {
	p := &jsonParser{file: file, data: data, line: 1}
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
}

func (p *jsonParser) pos() Position {
	return Position{
		File:   p.file,
		Line:   p.line,
		Column: utf8.RuneCount(p.data[p.lineStart:p.i]) + 1,
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
		default:
			return
		}
	}
}

func (p *jsonParser) value(depth int) (*Node, error) {
	if depth > maxDepth {
		return nil, p.fail("nesting exceeds %d levels", maxDepth)
	}
	if p.i >= len(p.data) {
		return nil, p.fail("unexpected end of input")
	}
	start, pos := p.i, p.pos()
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
	node.Raw = p.data[start:p.i]
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
