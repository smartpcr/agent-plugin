package schema

import (
	"bytes"
	"slices"
	"strconv"
)

// Document is a JSON Schema document, compact.
type Document []byte

// Profile is one provider's dialect of JSON Schema, as a keyword table its owner
// supplies (area A04 owns the model profiles; this package only applies one).
type Profile struct {
	// Name names the dialect in a Loss.
	Name string
	// Drop maps each keyword the dialect does not accept to why it is dropped;
	// Project removes exactly these keywords, wherever they are a keyword.
	Drop map[string]string
}

// Loss is one keyword a projection removed: the JSON Pointer of the schema that
// held it, the keyword, and the profile's reason.
type Loss struct {
	Path    string
	Keyword string
	Reason  string
}

// Project returns the document in p's dialect and every keyword it removed, in
// document order. A keyword is removed only where it is a keyword of a schema,
// never as a property name or inside an enum, const, default or other literal;
// removing a keyword that holds schemas (properties, items, oneOf...) removes them
// with it as one loss. The document keeps its key order and is otherwise
// unchanged; the shape itself is not: what was removed is checked, if at all, by
// Validate and Decode on this side, which still hold the whole document.
func (t *Type) Project(p Profile) (Document, []Loss) {
	doc := t.src
	if doc == nil {
		doc = t.doc
	}
	root, err := readOrdered(doc)
	if err != nil {
		return Document(bytes.Clone(doc)), nil
	}
	var losses []Loss
	root = project(root, "", p, &losses)
	var buf bytes.Buffer
	if err := writeOrdered(&buf, root); err != nil {
		return Document(bytes.Clone(doc)), nil
	}
	return Document(buf.Bytes()), losses
}

func project(node any, path string, p Profile, losses *[]Loss) any {
	obj, ok := node.(*ordered)
	if !ok {
		return node // a boolean schema, or a literal where a schema was expected
	}
	kept := &ordered{vals: map[string]any{}}
	for _, k := range obj.keys {
		v, at := obj.vals[k], pointer(path, k)
		if why, drop := p.Drop[k]; drop {
			*losses = append(*losses, Loss{Path: path, Keyword: k, Reason: why})
			continue
		}
		switch {
		case schemaKeywords[k]:
			v = project(v, at, p, losses)
		case schemaListKeywords[k]:
			if list, ok := v.([]any); ok {
				list = slices.Clone(list)
				for i, e := range list {
					list[i] = project(e, pointer(at, strconv.Itoa(i)), p, losses)
				}
				v = list
			}
		case schemaMapKeywords[k]:
			if named, ok := v.(*ordered); ok {
				out := &ordered{vals: map[string]any{}}
				for _, name := range named.keys {
					out.set(name, project(named.vals[name], pointer(at, name), p, losses))
				}
				v = out
			}
		}
		kept.set(k, v)
	}
	return kept
}
