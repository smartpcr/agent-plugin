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
// literally disabled entry (or group) takes no part; a config that holds an
// expression, or does not decode (the per-entry check reports that), is skipped.
// cat may be nil, which skips the child checks.
func CheckTree(cat Catalogue) func(tree []loader.EntryOptions) []loader.Issue {
	return func(tree []loader.EntryOptions) []loader.Issue {
		var reg *found
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
				var d Declaration
				if !decodes(f.entry.Config, &d) {
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
			return append(issues, at[0].issue("", fmt.Sprintf("an %s entry needs an %s entry in the tree", DeclarationName, RegistryName)))
		}
		var cfg Config
		if !decodes(reg.entry.Config, &cfg) {
			return issues
		}
		last := map[string]found{}
		for i, d := range decls {
			last[d.ID] = at[i]
		}
		for _, is := range Validate(cfg, decls, cat) {
			var i int
			switch {
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

// decodes decodes a literal config into v; false for one holding an expression or
// that does not decode. An omitted config decodes as the zero value.
func decodes(raw json.RawMessage, v any) bool {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return true
	}
	if bytes.Contains(raw, []byte("${{")) || bytes.Contains(raw, []byte(`"$expr"`)) {
		return false
	}
	return json.Unmarshal(raw, v) == nil
}

func fmtIndex(path string, i *int) bool {
	_, err := fmt.Sscanf(path, "[%d]", i)
	return err == nil
}
