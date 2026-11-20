package yamlx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v4"
)

// Default limits of Parse (DH-LIB-YAML-R9).
const (
	DefaultMaxBytes = 8 << 20
	DefaultMaxDepth = 64
)

type config struct {
	tags     map[string]bool
	maxBytes int
	maxDepth int
}

// Option configures Parse.
type Option func(*config)

// WithTag registers a scalar tag, such as "!expr". A scalar carrying it is a Tagged
// value; any other tag is an error.
func WithTag(name string) Option {
	return func(c *config) {
		if c.tags == nil {
			c.tags = map[string]bool{}
		}
		c.tags[name] = true
	}
}

// WithLimits bounds the size of the input in bytes and the nesting depth of its
// values. A limit that is zero or less keeps the default.
func WithLimits(maxBytes, maxDepth int) Option {
	return func(c *config) {
		if maxBytes > 0 {
			c.maxBytes = maxBytes
		}
		if maxDepth > 0 {
			c.maxDepth = maxDepth
		}
	}
}

func newConfig(opts []Option) config {
	c := config{maxBytes: DefaultMaxBytes, maxDepth: DefaultMaxDepth}
	for _, opt := range opts {
		if opt != nil {
			opt(&c)
		}
	}
	return c
}

// Document is one parsed YAML document. It is not changed by Edit.
type Document struct {
	file  string
	text  []byte
	cfg   config
	root  *yaml.Node // the document node; nil when the input holds no document
	value Value
	// blank marks the top-level entries (sequence items, or mapping keys) that had a
	// blank line above them.
	blank map[*yaml.Node]bool
}

// File returns the file name the document was parsed under.
func (d *Document) File() string { return d.file }

// Value returns the document's data. An empty document is Null.
func (d *Document) Value() Value { return d.value }

// Parse reads one document under the strict dialect. The error is a *SyntaxError.
func Parse(file string, text []byte, opts ...Option) (doc *Document, err error) {
	defer func() {
		if r := recover(); r != nil {
			doc, err = nil, &SyntaxError{File: file, Message: fmt.Sprintf("internal YAML error: %v", r)}
		}
	}()
	cfg := newConfig(opts)
	if len(text) > cfg.maxBytes {
		return nil, &SyntaxError{File: file, Message: fmt.Sprintf("input is %d bytes, over the limit of %d", len(text), cfg.maxBytes)}
	}
	root, err := load(file, text)
	if err != nil {
		return nil, err
	}
	w := newWalker(file, text, cfg)
	doc = &Document{file: file, text: bytes.Clone(text), cfg: cfg, root: root}
	doc.value = Value{Kind: Null, Pos: w.pos(1, 1)}
	if root != nil && len(root.Content) > 0 {
		if doc.value, err = w.value(root.Content[0], 1); err != nil {
			return nil, err
		}
		doc.blank = blankAbove(root.Content[0], w.lines, text)
	}
	return doc, nil
}

// load decodes text into a document node, refusing a second document.
func load(file string, text []byte) (*yaml.Node, error) {
	loader, err := yaml.NewLoader(bytes.NewReader(text))
	if err != nil {
		return nil, &SyntaxError{File: file, Message: err.Error()}
	}
	var root yaml.Node
	if err := loader.Load(&root); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, nil
		}
		return nil, syntaxError(file, err)
	}
	var second yaml.Node
	switch err := loader.Load(&second); {
	case err == nil:
		line, column := second.Line, second.Column
		if marker := documentMarker(text, line); marker > 0 {
			line, column = marker, 1
		}
		return nil, &SyntaxError{File: file, Line: line, Column: column, Message: "a second document is not allowed"}
	case !errors.Is(err, io.EOF):
		return nil, syntaxError(file, err)
	}
	return &root, nil
}

// documentMarker returns the line of the last "---" at or before line, or 0.
func documentMarker(text []byte, line int) int {
	lines := strings.Split(string(text), "\n")
	for i := min(line, len(lines)) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "---") {
			return i + 1
		}
	}
	return 0
}

func syntaxError(file string, err error) error {
	var load *yaml.LoadError
	if errors.As(err, &load) {
		message := load.Message
		if load.ContextMsg != "" {
			message = load.ContextMsg + ": " + message
		}
		return &SyntaxError{File: file, Line: load.Mark.Line, Column: load.Mark.Column, Message: message}
	}
	return &SyntaxError{File: file, Message: err.Error()}
}

// walker turns a node tree into values, enforcing the dialect.
type walker struct {
	file  string
	text  []byte
	lines []int // byte offset of the start of each line
	cfg   config
}

func newWalker(file string, text []byte, cfg config) *walker {
	w := &walker{file: file, text: text, cfg: cfg, lines: []int{0}}
	for i, c := range text {
		if c == '\n' {
			w.lines = append(w.lines, i+1)
		}
	}
	return w
}

func (w *walker) pos(line, column int) Pos {
	if line < 1 {
		return Pos{}
	}
	p := Pos{Line: line, Column: column}
	if line > len(w.lines) || w.text == nil {
		return p
	}
	offset := w.lines[line-1]
	for i := 1; i < column && offset < len(w.text) && w.text[offset] != '\n'; i++ {
		_, size := utf8.DecodeRune(w.text[offset:])
		offset += size
	}
	p.Offset = offset
	return p
}

func (w *walker) fail(n *yaml.Node, format string, args ...any) error {
	return &SyntaxError{File: w.file, Line: n.Line, Column: n.Column, Message: fmt.Sprintf(format, args...)}
}

func (w *walker) value(n *yaml.Node, depth int) (Value, error) {
	if depth > w.cfg.maxDepth {
		return Value{}, w.fail(n, "nesting is deeper than the limit of %d", w.cfg.maxDepth)
	}
	if n.Kind == yaml.AliasNode {
		return Value{}, w.fail(n, "alias *%s is not allowed", n.Value)
	}
	if n.Anchor != "" {
		return Value{}, w.fail(n, "anchor &%s is not allowed", n.Anchor)
	}
	explicit := n.Style&yaml.TaggedStyle != 0
	at := w.pos(n.Line, n.Column)
	switch n.Kind {
	case yaml.ScalarNode:
		if n.Tag == "!!merge" {
			return Value{}, w.fail(n, "merge key << is not allowed")
		}
		if explicit {
			if !w.cfg.tags[n.Tag] {
				return Value{}, w.fail(n, "tag %s is not allowed", n.Tag)
			}
			return Value{Kind: Tagged, Pos: at, Tag: n.Tag, Text: n.Value}, nil
		}
		if n.Style&(yaml.DoubleQuotedStyle|yaml.SingleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) != 0 {
			return Value{Kind: String, Pos: at, Text: n.Value}, nil
		}
		v, problem := typePlain(n.Value)
		if problem != "" {
			return Value{}, w.fail(n, "%s", problem)
		}
		v.Pos = at
		return v, nil
	case yaml.SequenceNode:
		if explicit {
			return Value{}, w.fail(n, "tag %s is not allowed", n.Tag)
		}
		v := Value{Kind: Seq, Pos: at, Items: make([]Value, 0, len(n.Content))}
		for _, item := range n.Content {
			iv, err := w.value(item, depth+1)
			if err != nil {
				return Value{}, err
			}
			v.Items = append(v.Items, iv)
		}
		return v, nil
	case yaml.MappingNode:
		if explicit {
			return Value{}, w.fail(n, "tag %s is not allowed", n.Tag)
		}
		v := Value{Kind: Map, Pos: at, Fields: make([]Field, 0, len(n.Content)/2)}
		seen := make(map[string]bool, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, value := n.Content[i], n.Content[i+1]
			if err := w.key(key); err != nil {
				return Value{}, err
			}
			if seen[key.Value] {
				return Value{}, w.fail(key, "duplicate key %q", key.Value)
			}
			seen[key.Value] = true
			fv, err := w.value(value, depth+1)
			if err != nil {
				return Value{}, err
			}
			v.Fields = append(v.Fields, Field{Key: key.Value, KeyPos: w.pos(key.Line, key.Column), Value: fv})
		}
		return v, nil
	default:
		return Value{}, w.fail(n, "unexpected YAML node kind %d", n.Kind)
	}
}

func (w *walker) key(n *yaml.Node) error {
	switch {
	case n.Kind == yaml.AliasNode:
		return w.fail(n, "alias *%s is not allowed", n.Value)
	case n.Anchor != "":
		return w.fail(n, "anchor &%s is not allowed", n.Anchor)
	case n.Kind != yaml.ScalarNode:
		return w.fail(n, "a mapping key must be a scalar")
	case n.Tag == "!!merge":
		return w.fail(n, "merge key << is not allowed")
	case n.Style&yaml.TaggedStyle != 0:
		return w.fail(n, "tag %s is not allowed on a key", n.Tag)
	}
	return nil
}

// typePlain types an unquoted word by the YAML 1.2 core rules. A word older YAML
// reads as a number in another base, or a decimal with a leading zero, is a problem.
func typePlain(s string) (Value, string) {
	switch s {
	case "", "~", "null", "Null", "NULL":
		return Value{Kind: Null}, ""
	case "true", "True", "TRUE":
		return Value{Kind: Bool, Bool: true}, ""
	case "false", "False", "FALSE":
		return Value{Kind: Bool}, ""
	case ".inf", ".Inf", ".INF", "+.inf", "+.Inf", "+.INF":
		return Value{Kind: Float, Float: math.Inf(1), Text: s}, ""
	case "-.inf", "-.Inf", "-.INF":
		return Value{Kind: Float, Float: math.Inf(-1), Text: s}, ""
	case ".nan", ".NaN", ".NAN":
		return Value{Kind: Float, Float: math.NaN(), Text: s}, ""
	}
	if len(s) > 2 && s[0] == '0' && (s[1] == 'o' || s[1] == 'x') {
		base := 8
		if s[1] == 'x' {
			base = 16
		}
		if !allDigits(s[2:], base) {
			return Value{Kind: String, Text: s}, ""
		}
		n, err := strconv.ParseInt(s[2:], base, 64)
		if err != nil {
			return Value{}, fmt.Sprintf("integer %s is out of range", s)
		}
		return Value{Kind: Int, Int: n, Text: s}, ""
	}
	body := strings.TrimLeft(s, "+-")
	if len(s)-len(body) > 1 {
		return Value{Kind: String, Text: s}, ""
	}
	if len(body) > 2 && body[0] == '0' && body[1] == 'b' && allDigits(body[2:], 2) {
		return Value{}, fmt.Sprintf("binary number %s is not YAML 1.2: write it in decimal, 0o or 0x, or quote it", s)
	}
	switch {
	case allDigits(body, 10):
		if len(body) > 1 && body[0] == '0' {
			return Value{}, fmt.Sprintf("number %s has a leading zero: write 0o%s for octal, or quote it", s, strings.TrimLeft(body, "0"))
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Value{}, fmt.Sprintf("integer %s is out of range", s)
		}
		return Value{Kind: Int, Int: n, Text: s}, ""
	case isFloat(body):
		if len(body) > 1 && body[0] == '0' && body[1] >= '0' && body[1] <= '9' {
			return Value{}, fmt.Sprintf("number %s has a leading zero; quote it if it is text", s)
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return Value{}, fmt.Sprintf("number %s is out of range", s)
		}
		return Value{Kind: Float, Float: f, Text: s}, ""
	}
	return Value{Kind: String, Text: s}, ""
}

func allDigits(s string, base int) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		var d int
		switch {
		case c >= '0' && c <= '9':
			d = int(c - '0')
		case c >= 'a' && c <= 'f':
			d = int(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = int(c-'A') + 10
		default:
			return false
		}
		if d >= base {
			return false
		}
	}
	return true
}

// isFloat matches the unsigned YAML 1.2 core float: ( \.[0-9]+ | [0-9]+ ( \.[0-9]* )? ) ( [eE] [-+]? [0-9]+ )?
func isFloat(s string) bool {
	i, digits := 0, 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i, digits = i+1, digits+1
	}
	if i < len(s) && s[i] == '.' {
		i++
		fraction := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i, fraction = i+1, fraction+1
		}
		if digits == 0 && fraction == 0 {
			return false
		}
	} else if digits == 0 {
		return false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		exp := 0
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i, exp = i+1, exp+1
		}
		if exp == 0 {
			return false
		}
	}
	return i == len(s)
}

// blankAbove records which top-level entries of top had a blank line above them (above
// their comment, when they have one).
func blankAbove(top *yaml.Node, lines []int, text []byte) map[*yaml.Node]bool {
	blank := map[*yaml.Node]bool{}
	for _, entry := range topEntries(top) {
		if isBlankLine(text, lines, firstLine(entry)-1) {
			blank[entry] = true
		}
	}
	return blank
}

// topEntries are the nodes that start the top-level entries: sequence items, or
// mapping keys.
func topEntries(top *yaml.Node) []*yaml.Node {
	switch top.Kind {
	case yaml.SequenceNode:
		if top.Style&yaml.FlowStyle != 0 {
			return nil
		}
		return top.Content
	case yaml.MappingNode:
		if top.Style&yaml.FlowStyle != 0 {
			return nil
		}
		keys := make([]*yaml.Node, 0, len(top.Content)/2)
		for i := 0; i < len(top.Content); i += 2 {
			keys = append(keys, top.Content[i])
		}
		return keys
	}
	return nil
}

// firstLine is the first line of an entry: its own line less its head comment.
func firstLine(entry *yaml.Node) int {
	line := entry.Line
	if entry.HeadComment != "" {
		line -= strings.Count(entry.HeadComment, "\n") + 1
	}
	return line
}

func isBlankLine(text []byte, lines []int, line int) bool {
	if line < 1 || line > len(lines) {
		return false
	}
	end := len(text)
	if line < len(lines) {
		end = lines[line] - 1
	}
	return len(bytes.TrimSpace(text[lines[line-1]:end])) == 0
}
