// Package yamlx is the harness YAML dialect (DH-ADR-YAML-1, DH-D-BOOT-4): the one
// package that imports a YAML module. It parses one document under a strict dialect
// (no anchors, aliases, merge keys, second documents, duplicate keys or unregistered
// tags), types unquoted words by the YAML 1.2 core rules itself, edits a parsed
// document without losing a comment, dumps plain data deterministically and decodes
// markdown frontmatter.
//
// Every value carries its line and column; a malformed input is a *SyntaxError with a
// position. A panic inside the YAML module is recovered and returned as an error.
//
//dsh:untrusted-parser
package yamlx

import (
	"fmt"
	"math"
	"strconv"
)

// Kind is the kind of a value.
type Kind uint8

const (
	Null Kind = iota
	Bool
	Int
	Float
	String
	// Tagged is a scalar carrying a tag the caller registered (WithTag), such as
	// "!expr"; Text is its source text.
	Tagged
	Seq
	Map
)

var kindNames = [...]string{"null", "boolean", "integer", "float", "string", "tagged", "sequence", "mapping"}

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "Kind(" + strconv.Itoa(int(k)) + ")"
}

// Pos is a place in the source text. Line and Column are 1-based and Column counts
// characters; Offset is the 0-based byte offset. The zero Pos is unknown.
type Pos struct {
	Line   int
	Column int
	Offset int
}

// Value is one value of a document with the position it starts at.
type Value struct {
	Kind Kind
	Pos  Pos
	// Bool is the value of a Bool.
	Bool bool
	// Int is the value of an Int.
	Int int64
	// Float is the value of a Float.
	Float float64
	// Text is the string of a String, the source spelling of an Int or a Float, and
	// the source text of a Tagged.
	Text string
	// Tag is the tag of a Tagged.
	Tag string
	// Items are the elements of a Seq.
	Items []Value
	// Fields are the members of a Map, in document order.
	Fields []Field
}

// Field is one member of a mapping.
type Field struct {
	Key    string
	KeyPos Pos
	Value  Value
	// Comment, when set, is written by Dump as a comment on the member's line; Parse
	// does not fill it.
	Comment string
}

// TaggedScalar is the plain form of a Tagged value, and how a caller asks Edit or
// Dump to write one.
type TaggedScalar struct {
	Tag    string
	Source string
}

// Get returns the member key of a mapping.
func (v Value) Get(key string) (Value, bool) {
	for _, field := range v.Fields {
		if field.Key == key {
			return field.Value, true
		}
	}
	return Value{}, false
}

// Plain returns the value as plain Go data: nil, bool, int64, float64, string,
// TaggedScalar, []any or map[string]any.
func (v Value) Plain() any {
	switch v.Kind {
	case Bool:
		return v.Bool
	case Int:
		return v.Int
	case Float:
		return v.Float
	case String:
		return v.Text
	case Tagged:
		return TaggedScalar{Tag: v.Tag, Source: v.Text}
	case Seq:
		items := make([]any, len(v.Items))
		for i, item := range v.Items {
			items[i] = item.Plain()
		}
		return items
	case Map:
		fields := make(map[string]any, len(v.Fields))
		for _, field := range v.Fields {
			fields[field.Key] = field.Value.Plain()
		}
		return fields
	default:
		return nil
	}
}

// Equal reports whether two values hold the same data, in the same order, ignoring
// positions and spelling.
func Equal(a, b Value) bool {
	if a.Kind != b.Kind {
		return false
	}
	switch a.Kind {
	case Bool:
		return a.Bool == b.Bool
	case Int:
		return a.Int == b.Int
	case Float:
		return a.Float == b.Float || (math.IsNaN(a.Float) && math.IsNaN(b.Float))
	case String:
		return a.Text == b.Text
	case Tagged:
		return a.Tag == b.Tag && a.Text == b.Text
	case Seq:
		if len(a.Items) != len(b.Items) {
			return false
		}
		for i := range a.Items {
			if !Equal(a.Items[i], b.Items[i]) {
				return false
			}
		}
		return true
	case Map:
		if len(a.Fields) != len(b.Fields) {
			return false
		}
		for i := range a.Fields {
			if a.Fields[i].Key != b.Fields[i].Key || !Equal(a.Fields[i].Value, b.Fields[i].Value) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// SyntaxError is input the dialect rejects, with the position of the offending token.
type SyntaxError struct {
	File    string
	Line    int
	Column  int
	Message string
}

func (e *SyntaxError) Error() string {
	switch {
	case e.Line <= 0 && e.File == "":
		return "yaml: " + e.Message
	case e.Line <= 0:
		return e.File + ": " + e.Message
	case e.File == "":
		return fmt.Sprintf("%d:%d: %s", e.Line, e.Column, e.Message)
	default:
		return fmt.Sprintf("%s:%d:%d: %s", e.File, e.Line, e.Column, e.Message)
	}
}
