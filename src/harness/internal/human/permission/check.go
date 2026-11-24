package permission

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/go/loader"
)

// CheckTree is the static rule of a composed tree's permission tables, a
// config.TreeCheck run on the start-up tree before anything is applied and on
// every reload: each enabled permission.presets entry whose config is literal is
// resolved onto DefaultConfig and validated, so a default preset that names no
// entry of its table stops the launch (config/rejected, status 78) with the issue
// placed at <entry>.config.defaultPreset, rather than leaving the entry waiting
// on its services. A config holding an expression is left to its load; one that
// does not decode is the per-entry check's issue and is skipped here.
func CheckTree() func(tree []loader.EntryOptions) []loader.Issue {
	return func(tree []loader.EntryOptions) []loader.Issue {
		var issues []loader.Issue
		walk(tree, "", func(id string, e loader.EntryOptions) {
			if e.Name != Name {
				return
			}
			raw := bytes.TrimSpace(e.Config)
			if bytes.Contains(raw, []byte("${{")) || bytes.Contains(raw, []byte(`"$expr"`)) {
				return
			}
			cfg := DefaultConfig()
			if len(raw) > 0 && string(raw) != "null" && json.Unmarshal(raw, &cfg) != nil {
				return
			}
			err := cfg.Validate()
			if err == nil {
				return
			}
			base := id
			if base == "" {
				base = e.Name
			}
			base += ".config"
			var ce *cordis.ConfigError
			if !errors.As(err, &ce) || len(ce.Issues) == 0 {
				issues = append(issues, loader.Issue{Pos: e.Position(), Path: base, Message: err.Error()})
				return
			}
			for _, is := range ce.Issues {
				path := base
				if is.Path != "" {
					path += "." + strings.TrimPrefix(is.Path, ".")
				}
				issues = append(issues, loader.Issue{Pos: e.Position(), Path: path, Message: is.Message})
			}
		})
		return issues
	}
}

func walk(list []loader.EntryOptions, parent string, fn func(id string, e loader.EntryOptions)) {
	for _, e := range list {
		if e.Disabled && e.DisabledExpr == "" {
			continue
		}
		id := e.ID
		if parent != "" && id != "" {
			id = parent + loader.Separator + id
		}
		if e.Group {
			walk(e.Children, id, fn)
			continue
		}
		fn(id, e)
	}
}
