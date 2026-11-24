package agentpreset

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// CheckTree is the static rule of a composed tree's presets (bundle-preset.md §6.2,
// §6.3), a config.TreeCheck: the agent.preset entries are validated as one set with
// the agent.presets entry's config (Validate), so an Extends that names no
// declaration or closes a cycle, a bad child or a Default that names no declaration
// rejects the whole edit, each issue placed at the entry at fault. The tree is one
// set whatever its order, so a declaration may extend one that loads after it. A
// literally disabled entry (or group) takes no part.
//
// A declaration is read field by field (§6.1): a child config, name or order holding
// an expression is left to the revision's loader while the literal id, extends and
// children take part. An id, extends or Default that is an expression is computed
// with ev, as its load would compute it (the os, env, launch and home names need no
// entry context), and checked with its value. One that stays unknown (no evaluator,
// a use name, an error or a value that is not a string) only withholds the issues it
// could answer: while an id is unknown, an Extends or Default naming no literal
// declaration is left to the load; an unknown Extends makes its declaration a root;
// an unknown Default is not checked. A cycle, a duplicate id or a bad child among the
// known declarations is reported whatever else the tree holds. A config that does
// not decode is the per-entry check's issue and is skipped here. cat may be nil,
// which skips the child checks; ev may be nil, which leaves every expression unknown.
func CheckTree(cat Catalogue, ev loader.Evaluator) func(tree []loader.EntryOptions) []loader.Issue {
	return func(tree []loader.EntryOptions) []loader.Issue {
		var reg *found
		unknownIDs := 0 // declarations whose id could not be computed
		var decls []Declaration
		var at []found // the entry of each declaration
		var issues []loader.Issue
		walkTree(tree, "", func(f found) {
			switch f.entry.Name {
			case RegistryName:
				if reg != nil {
					issues = append(issues, f.issue("", fmt.Sprintf("a second %s entry (the first is %s)", RegistryName, reg.path)))
					return
				}
				reg = &f
			case DeclarationName:
				d, idKnown, ok := decodeDeclaration(f.entry.Config, ev, f.path)
				if !ok {
					return
				}
				if !idKnown {
					unknownIDs++
					return
				}
				decls = append(decls, d)
				at = append(at, f)
			}
		})
		if len(decls) == 0 && reg == nil {
			return issues
		}
		if reg == nil {
			if len(at) == 0 {
				return issues
			}
			return append(issues, at[0].issue("", fmt.Sprintf("an %s entry needs an %s entry in the tree", DeclarationName, RegistryName)))
		}
		cfg, defaultKnown, ok := decodeConfig(reg.entry.Config, ev, reg.path)
		if !ok {
			return issues
		}
		last := map[string]found{}
		for i, d := range decls {
			last[d.ID] = at[i]
		}
		for _, is := range Validate(cfg, decls, cat) {
			var i int
			switch {
			case is.Path == "default" && is.Preset == "" && (!defaultKnown || unknownIDs > 0 && cfg.Default != ""):
				// The Default is computed at load, or may name the declaration whose id is.
			case is.Path == "extends" && unknownIDs > 0 && strings.Contains(is.Message, "which is not declared"):
				// The parent may be the declaration whose id is computed at load.
			case is.Preset != "":
				issues = append(issues, last[is.Preset].issue(is.Path, is.String()))
			case strings.HasPrefix(is.Path, "[") && fmtIndex(is.Path, &i) && i < len(at):
				issues = append(issues, at[i].issue("id", is.Message))
			default:
				issues = append(issues, reg.issue(is.Path, is.String()))
			}
		}
		return issues
	}
}

// found is an entry of the tree with its tree id.
type found struct {
	path  string
	entry loader.EntryOptions
}

func (f found) issue(field, msg string) loader.Issue {
	path := f.path
	if path == "" {
		path = f.entry.Name
	}
	path += ".config"
	if field != "" {
		path += "." + field
	}
	return loader.Issue{Pos: f.entry.Position(), Path: path, Message: msg}
}

func walkTree(list []loader.EntryOptions, parent string, fn func(found)) {
	for _, e := range list {
		if e.Disabled && e.DisabledExpr == "" {
			continue
		}
		id := e.ID
		if parent != "" && id != "" {
			id = parent + loader.Separator + id
		}
		if e.Group {
			walkTree(e.Children, id, fn)
			continue
		}
		fn(found{path: id, entry: e})
	}
}

// decodeDeclaration reads a declaration's config field by field. ok is false for
// one that does not decode; idKnown is false when its id is an expression ev cannot
// compute. An extends ev cannot compute is left empty. A name, description or order
// that is an expression is left zero; a child that is itself an expression, or whose
// component is, is left out; a child's config is kept raw whatever it holds
// (checkChild leaves an expression to the revision's loader) and a disabled that is
// an expression counts as enabled.
func decodeDeclaration(raw json.RawMessage, ev loader.Evaluator, entry string) (d Declaration, idKnown, ok bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return d, true, true
	}
	if isExpr(raw) {
		return d, false, true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return d, false, false
	}
	if d.ID, idKnown = ref(fields["id"], ev, entry); !idKnown {
		return Declaration{}, false, true
	}
	d.Extends, _ = ref(fields["extends"], ev, entry)
	_ = literal(fields["name"], &d.Name)
	_ = literal(fields["description"], &d.Description)
	_ = literal(fields["order"], &d.Order)
	children := bytes.TrimSpace(fields["children"])
	if len(children) == 0 || string(children) == "null" || isExpr(children) {
		return d, true, true
	}
	var kids map[string]json.RawMessage
	if json.Unmarshal(children, &kids) != nil {
		return d, true, true
	}
	d.Children = make(map[string]*Child, len(kids))
	for key, kr := range kids {
		kr = bytes.TrimSpace(kr)
		if string(kr) == "null" {
			d.Children[key] = nil
			continue
		}
		var cf map[string]json.RawMessage
		if isExpr(kr) || json.Unmarshal(kr, &cf) != nil {
			continue
		}
		c := &Child{Config: cf["config"]}
		if !literal(cf["component"], &c.Component) {
			continue
		}
		_ = literal(cf["disabled"], &c.Disabled)
		d.Children[key] = c
	}
	return d, true, true
}

// decodeConfig reads the registry's config; defaultKnown is false when its Default
// is an expression ev cannot compute, ok false when it does not decode.
func decodeConfig(raw json.RawMessage, ev loader.Evaluator, entry string) (cfg Config, defaultKnown, ok bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return cfg, true, true
	}
	if isExpr(raw) {
		return cfg, false, true
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return cfg, false, false
	}
	def := bytes.TrimSpace(fields["default"])
	if cfg.Default, defaultKnown = ref(def, ev, entry); !defaultKnown && !isExpr(def) {
		return cfg, false, false
	}
	cfg.SelectedDefault, _ = ref(fields["selectedDefault"], ev, entry)
	return cfg, defaultKnown, true
}

// ref is a string field's value: the literal, or what ev computes for an
// expression that is the whole value, in the config scope of entry. known is false
// for an expression that cannot be computed here or yields no string, and for a
// literal that is not a string.
func ref(raw json.RawMessage, ev loader.Evaluator, entry string) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var str string
	if !isExpr(raw) {
		return str, json.Unmarshal(raw, &str) == nil
	}
	source, ok := exprSource(raw)
	if !ok || ev == nil {
		return "", false
	}
	v, err := ev.Eval(source, loader.Scope{Kind: loader.ConfigScope, EntryID: entry})
	if err != nil {
		return "", false
	}
	str, ok = v.(string)
	return str, ok
}

// exprSource is the source of an expression that is a whole value: "${{ x }}" or
// {"$expr": "x"}.
func exprSource(raw json.RawMessage) (string, bool) {
	if raw[0] == '{' {
		var obj map[string]json.RawMessage
		var src string
		if json.Unmarshal(raw, &obj) != nil || len(obj) != 1 || json.Unmarshal(obj["$expr"], &src) != nil {
			return "", false
		}
		return strings.TrimSpace(src), src != ""
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return "", false
	}
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, "${{") || !strings.HasSuffix(text, "}}") {
		return "", false
	}
	inner := strings.TrimSpace(text[3 : len(text)-2])
	if inner == "" || strings.Contains(inner, "${{") || strings.Contains(inner, "}}") {
		return "", false
	}
	return inner, true
}

// literal decodes raw into v; true for an absent value or a literal that decodes,
// false for an expression or a value of the wrong type (v is left as it was).
func literal(raw json.RawMessage, v any) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return true
	}
	if isExpr(raw) {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

// isExpr reports whether raw is an expression: a string holding "${{" or an
// object with a "$expr" key.
func isExpr(raw json.RawMessage) bool {
	switch {
	case len(raw) == 0:
		return false
	case raw[0] == '"':
		var str string
		return json.Unmarshal(raw, &str) == nil && strings.Contains(str, "${{")
	case raw[0] == '{':
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			return false
		}
		_, ok := obj["$expr"]
		return ok
	}
	return false
}

func fmtIndex(path string, i *int) bool {
	_, err := fmt.Sscanf(path, "[%d]", i)
	return err == nil
}
