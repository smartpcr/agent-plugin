package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// Preview composes and checks layers without starting anything (DH-D-BOOT-6). It
// is Check under the name the design gives it; no component is constructed.
// `dsh config show` and `dsh config validate` print it (boot.PreviewLaunch).
func Preview(layers []loader.Layer, opts CheckOptions) *Report { return Check(layers, opts) }

// FieldOwner is one field of a previewed entry and the layer that supplied it.
type FieldOwner struct {
	Field string `json:"field"`
	Layer string `json:"layer"`
}

// PreviewEntry is one entry of a previewed tree: its tree id, component, whether
// it is disabled, its composed config, the layer that inserted it and the layers
// that supplied its fields.
type PreviewEntry struct {
	ID       string          `json:"id"`
	Name     string          `json:"name,omitempty"`
	Group    bool            `json:"group,omitempty"`
	Disabled bool            `json:"disabled,omitempty"`
	Config   json.RawMessage `json:"config,omitempty"`
	Layer    string          `json:"layer,omitempty"`
	Fields   []FieldOwner    `json:"fields,omitempty"`
	At       string          `json:"at,omitempty"`
}

// Entries lists the composed entries in tree order, children after their group,
// each with its owning layers.
func (r *Report) Entries() []PreviewEntry {
	fields := map[string][]FieldOwner{}
	for _, o := range r.Index.Owners() {
		if o.Field != "" {
			fields[o.ID] = append(fields[o.ID], FieldOwner{Field: o.Field, Layer: o.Label})
		}
	}
	var out []PreviewEntry
	r.walk(r.Tree, "", func(id string, e loader.EntryOptions) {
		pe := PreviewEntry{ID: id, Name: e.Name, Group: e.Group, Disabled: e.Disabled, Config: compact(e.Config), Fields: fields[id]}
		if label, ok := r.Index.Owner(id, ""); ok && id != "" {
			pe.Layer = label
		}
		if pos := e.Position(); pos.IsValid() {
			pe.At = pos.String()
		}
		out = append(out, pe)
	})
	return out
}

// Explanation says where one value of the composed tree comes from.
type Explanation struct {
	ID    string          `json:"id"`
	Field string          `json:"field,omitempty"`
	Layer string          `json:"layer"`
	Value json.RawMessage `json:"value,omitempty"`
	At    string          `json:"at,omitempty"`
}

// Explain names the layer that supplied the entry's field ("" for the entry,
// "config.level", "disabled"), the composed value and where it was written.
func (r *Report) Explain(id, field string) (Explanation, error) {
	var entry *loader.EntryOptions
	r.walk(r.Tree, "", func(eid string, e loader.EntryOptions) {
		if eid == id && entry == nil {
			e := e
			entry = &e
		}
	})
	if entry == nil {
		return Explanation{}, fmt.Errorf("no entry %q in the composed tree", id)
	}
	label, ok := r.Index.Owner(id, field)
	if !ok {
		return Explanation{}, fmt.Errorf("no layer supplied %s", joinField(id, field))
	}
	ex := Explanation{ID: id, Field: field, Layer: label}
	value, err := fieldValue(*entry, field)
	if err != nil {
		return Explanation{}, err
	}
	ex.Value = value
	if pos := r.position(id, field); pos.IsValid() {
		ex.At = pos.String()
	} else if pos := entry.Position(); pos.IsValid() {
		ex.At = pos.String()
	}
	return ex, nil
}

// Dump is the deterministic JSON form of the report: the entries with their owners
// and the issues, so two dumps of the same layers are byte-identical.
func (r *Report) Dump() ([]byte, error) {
	issues := make([]string, len(r.Issues))
	for i, issue := range r.Issues {
		issues[i] = issue.Error()
	}
	return json.MarshalIndent(struct {
		Entries []PreviewEntry `json:"entries"`
		Issues  []string       `json:"issues"`
	}{r.Entries(), issues}, "", "  ")
}

// fieldValue extracts the composed value of the entry's field.
func fieldValue(e loader.EntryOptions, field string) (json.RawMessage, error) {
	switch {
	case field == "":
		return json.Marshal(e)
	case field == "disabled":
		return json.Marshal(e.Disabled)
	case field == "config":
		return compact(e.Config), nil
	case strings.HasPrefix(field, "config."):
		var v any
		if len(bytes.TrimSpace(e.Config)) == 0 {
			return nil, fmt.Errorf("%s: entry has no config", field)
		}
		if err := json.Unmarshal(e.Config, &v); err != nil {
			return nil, err
		}
		for _, step := range strings.Split(strings.TrimPrefix(field, "config."), ".") {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s: not set", field)
			}
			if v, ok = m[step]; !ok {
				return nil, fmt.Errorf("%s: not set", field)
			}
		}
		return json.Marshal(v)
	}
	var whole map[string]json.RawMessage
	raw, err := json.Marshal(e)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &whole); err != nil {
		return nil, err
	}
	return whole[field], nil
}

func compact(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var b bytes.Buffer
	if err := json.Compact(&b, raw); err != nil {
		return raw
	}
	return b.Bytes()
}

func joinField(id, field string) string {
	if field == "" {
		return id
	}
	return id + "." + field
}
