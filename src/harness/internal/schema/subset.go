package schema

import (
	"cmp"
	"encoding/json"
	"slices"
	"strconv"
)

// Subset is a keyword subset a schema document must stay inside, such as the one
// a model provider is sent (DH-LIB-ZOD-R3). The caller owns the table; this
// package only applies it. A keyword outside it is reported, never weakened into
// it, so a document promises nothing nobody checks.
type Subset struct {
	// Keywords are the admitted keywords; any other is an issue at its path.
	Keywords []string
	// SingleType requires "type" to name one type, not a list.
	SingleType bool
	// BooleanAdditional requires additionalProperties to be true or false, never
	// a schema.
	BooleanAdditional bool
	// ScalarLiterals requires enum and const values to be scalars: null, a
	// boolean, a number or a string.
	ScalarLiterals bool
}

// CheckSubset returns every place the document leaves s, each with its JSON
// Pointer, sorted by path; nil when it stays inside. A document loaded by
// FromJSON is checked as it was supplied. Boolean schemas are reported too: a
// subset is written in keywords, and true or false holds none.
func (t *Type) CheckSubset(s Subset) Issues {
	doc := t.src
	if doc == nil {
		doc = t.doc
	}
	root, err := parse(doc)
	if err != nil {
		return Issues{{Path: "", Message: err.Error()}}
	}
	c := subsetCheck{s: s, admitted: map[string]bool{}}
	for _, k := range s.Keywords {
		c.admitted[k] = true
	}
	c.schema(root, "")
	slices.SortStableFunc(c.issues, func(a, b Issue) int {
		return cmp.Or(cmp.Compare(a.Path, b.Path), cmp.Compare(a.Message, b.Message))
	})
	return c.issues
}

type subsetCheck struct {
	s        Subset
	admitted map[string]bool
	issues   Issues
}

func (c *subsetCheck) add(path, msg string) {
	c.issues = append(c.issues, Issue{Path: path, Message: msg})
}

func (c *subsetCheck) schema(node any, path string) {
	obj, ok := node.(map[string]any)
	if !ok {
		c.add(path, "a schema must be an object")
		return
	}
	for _, k := range sortedKeys(obj) {
		v, p := obj[k], pointer(path, k)
		if !c.admitted[k] {
			c.add(p, "the keyword is not admitted")
			continue
		}
		switch {
		case k == "type":
			c.typeKeyword(v, p)
		case k == "additionalProperties":
			if _, isBool := v.(bool); isBool {
				break
			}
			if c.s.BooleanAdditional {
				c.add(p, "must be true or false")
				break
			}
			c.schema(v, p)
		case k == "required":
			if !stringList(v) {
				c.add(p, "must be a list of names")
			}
		case k == "enum":
			list, ok := v.([]any)
			for _, e := range list {
				ok = ok && (!c.s.ScalarLiterals || scalar(e))
			}
			if !ok {
				c.add(p, "must be a list of scalars")
			}
		case k == "const":
			if c.s.ScalarLiterals && !scalar(v) {
				c.add(p, "must be a scalar")
			}
		case schemaMapKeywords[k]:
			named, ok := v.(map[string]any)
			if !ok {
				c.add(p, "must map names to schemas")
				break
			}
			for _, name := range sortedKeys(named) {
				c.schema(named[name], pointer(p, name))
			}
		case schemaListKeywords[k]:
			list, ok := v.([]any)
			if !ok {
				c.add(p, "must be a list of schemas")
				break
			}
			for i, sub := range list {
				c.schema(sub, pointer(p, strconv.Itoa(i)))
			}
		case schemaKeywords[k]:
			c.schema(v, p)
		}
	}
}

func (c *subsetCheck) typeKeyword(v any, p string) {
	if _, ok := v.(string); ok {
		return
	}
	if c.s.SingleType {
		c.add(p, "must be one type name")
		return
	}
	if !stringList(v) {
		c.add(p, "must be a type name or a list of them")
	}
}

func stringList(v any) bool {
	list, ok := v.([]any)
	for _, e := range list {
		if _, s := e.(string); !s {
			return false
		}
	}
	return ok
}

func scalar(v any) bool {
	switch v.(type) {
	case nil, bool, string, json.Number:
		return true
	}
	return false
}
