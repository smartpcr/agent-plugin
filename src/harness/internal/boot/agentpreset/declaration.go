// Package agentpreset is the agent preset registry (DH-D-BUNDLE-4, architecture.md
// C43, bundle-preset.md §6.1): the declarations of agent presets, the revisions the
// registry mounts for them with a lease count each, the validation of `Extends`, and
// the binding of a session to one revision, recorded by the durable event
// agent-preset/selected and read back by the agentPreset fold.
//
// A declaration is configuration: a keyed map of children, each a catalogue
// component with its config. A revision is one mounted instance of a declaration:
// one scope (session/scope), one realm private for the services its children's
// catalogue items provide, and one nested loader of its own that mounts the
// children there. An edit that changes a declaration builds a new revision and
// retires the old one, which stays alive, with every child loaded, until the last
// lease on it is released. A running session therefore keeps the composition it was
// bound to, and a new session binds the current one.
//
// Locking: Registry.mu guards the declaration map, the revision list, the lease
// counts and the agent bindings; no component code, loader call or log append runs
// under it. Registry.loadMu serialises whole loads.
package agentpreset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
)

// The catalogue names of the registry and of a declaration entry (bundle-preset.md
// §6.1); profile.Spine requires RegistryName in every launchable profile.
const (
	RegistryName    = "agent.presets"
	DeclarationName = "agent.preset"
)

// Child is one child of a declaration: a catalogue component and its config. In a
// declaration's Children map a nil *Child (a JSON null) removes the child of that key
// that the declaration would inherit through Extends.
type Child struct {
	// Component is the catalogue name; empty means the map key, without any
	// "@<instance>" suffix ("tool.subagent@spawn" is component tool.subagent).
	Component string `json:"component,omitempty"`
	// Disabled keeps the child declared but unloaded.
	Disabled bool `json:"disabled,omitempty"`
	// Config is handed to the component and decoded strictly by its catalogue type.
	Config json.RawMessage `json:"config,omitempty"`
}

// Declaration is the config of one agent.preset entry (UP-CFG-dsh-agent-preset).
// Children is a map, not a list, so a layer's merge reaches one child or one field
// of it, and a null removes one.
type Declaration struct {
	ID          string            `json:"id"`
	Name        string            `json:"name,omitempty"`
	Description string            `json:"description,omitempty"`
	Order       int               `json:"order,omitempty"`
	Extends     string            `json:"extends,omitempty"`
	Children    map[string]*Child `json:"children,omitempty"`
}

// Config is the registry's config (UP-CFG-dsh-agent-preset-registry).
type Config struct {
	// Default is the preset a session gets when none is named; required, and an
	// unknown id is a static issue.
	Default string `json:"default"`
	// SelectedDefault is the saved choice; it wins over Default while it names a
	// declaration.
	SelectedDefault string `json:"selectedDefault,omitempty"`
}

// Catalogue is what the registry needs of A21's catalogue: the item of a name, to
// check a child statically and read what it provides, and the component, for each
// revision's loader. *catalog.Catalog is one.
type Catalogue interface {
	Lookup(name string) (catalog.Item, error)
	Resolve(name string) (*cordis.Component, bool)
}

// Issue is one static issue of a set of declarations: the declaration, the path in
// its config and what is wrong.
type Issue struct {
	Preset  string
	Path    string
	Message string
}

func (i Issue) String() string {
	at := i.Preset
	if i.Path != "" {
		if at != "" {
			at += "."
		}
		at += i.Path
	}
	if at == "" {
		return i.Message
	}
	return "preset " + at + ": " + i.Message
}

// resolved is a declaration with Extends applied: its own children over its base's,
// transitively, with nulls removed and every component named.
type resolved struct {
	decl     Declaration
	children map[string]Child
	digest   string
}

// ComponentOf is the component a child of key names: its Component, else the key
// without any "@<instance>" suffix.
func ComponentOf(key string, c Child) string {
	if c.Component != "" {
		return c.Component
	}
	name, _, _ := strings.Cut(key, "@")
	return name
}

// Validate checks a set of declarations and the registry config statically, as A21's
// check does before an edit is accepted: an empty or duplicate id, an Extends that
// names no declaration, an Extends cycle (one issue naming every member), a child
// whose component the catalogue does not hold or whose config does not decode
// strictly, and a Default that names no declaration. cat may be nil, which skips the
// child checks. The issues are in a stable order; none means the set loads.
func Validate(cfg Config, decls []Declaration, cat Catalogue) []Issue {
	_, issues := resolveAll(cfg, decls, cat, "")
	return issues
}

func resolveAll(cfg Config, decls []Declaration, cat Catalogue, version string) ([]resolved, []Issue) {
	var issues []Issue
	byID := map[string]Declaration{}
	var order []string
	for i, d := range decls {
		switch {
		case d.ID == "":
			issues = append(issues, Issue{Path: fmt.Sprintf("[%d].id", i), Message: "a declaration needs an id"})
			continue
		case strings.Contains(d.ID, ":"):
			issues = append(issues, Issue{Preset: d.ID, Path: "id", Message: "an id cannot contain \":\""})
			continue
		}
		if _, dup := byID[d.ID]; dup {
			issues = append(issues, Issue{Preset: d.ID, Path: "id", Message: fmt.Sprintf("duplicate id %q", d.ID)})
			continue
		}
		byID[d.ID] = d
		order = append(order, d.ID)
	}
	sorted := slices.Sorted(maps.Keys(byID))

	// bad holds the ids that cannot resolve: an unknown base, a cycle, or a base that
	// cannot resolve.
	bad := map[string]bool{}
	for _, id := range sorted {
		if ext := byID[id].Extends; ext != "" {
			if _, ok := byID[ext]; !ok {
				issues = append(issues, Issue{Preset: id, Path: "extends",
					Message: fmt.Sprintf("extends %q, which is not declared (declared: %s)", ext, strings.Join(sorted, ", "))})
				bad[id] = true
			}
		}
	}
	reported := map[string]bool{}
	for _, start := range sorted {
		var path []string
		at := map[string]int{}
		for id := start; id != ""; id = byID[id].Extends {
			if _, ok := byID[id]; !ok {
				break
			}
			if i, seen := at[id]; seen {
				cycle := path[i:]
				key := strings.Join(slices.Sorted(slices.Values(cycle)), ",")
				if !reported[key] {
					reported[key] = true
					// The chain from the cycle's least id, so the message is stable.
					first := slices.Index(cycle, slices.Min(cycle))
					chain := append(append(slices.Clone(cycle[first:]), cycle[:first]...), cycle[first])
					issues = append(issues, Issue{Preset: cycle[first], Path: "extends",
						Message: "extends cycle: " + strings.Join(chain, " -> ")})
				}
				for _, m := range cycle {
					bad[m] = true
				}
				break
			}
			at[id] = len(path)
			path = append(path, id)
		}
	}
	// A declaration whose chain reaches a bad one cannot resolve either.
	for changed := true; changed; {
		changed = false
		for _, id := range sorted {
			if ext := byID[id].Extends; !bad[id] && ext != "" && bad[ext] {
				bad[id] = true
				changed = true
			}
		}
	}

	if cat != nil {
		for _, id := range sorted {
			d := byID[id]
			for _, key := range slices.Sorted(maps.Keys(d.Children)) {
				if c := d.Children[key]; c != nil {
					if msg := checkChild(cat, key, *c); msg != "" {
						issues = append(issues, Issue{Preset: id, Path: "children." + key, Message: msg})
					}
				}
			}
		}
	}

	if cfg.Default == "" {
		issues = append(issues, Issue{Path: "default", Message: "the registry needs a default preset"})
	} else if _, ok := byID[cfg.Default]; !ok {
		issues = append(issues, Issue{Path: "default",
			Message: fmt.Sprintf("default %q is not declared (declared: %s)", cfg.Default, strings.Join(sorted, ", "))})
	}

	memo := map[string]map[string]Child{}
	var children func(id string) map[string]Child
	children = func(id string) map[string]Child {
		if m, ok := memo[id]; ok {
			return m
		}
		d := byID[id]
		out := map[string]Child{}
		if d.Extends != "" {
			maps.Copy(out, children(d.Extends))
		}
		for k, c := range d.Children {
			if c == nil {
				delete(out, k)
				continue
			}
			cc := *c
			cc.Component = ComponentOf(k, cc)
			cc.Config = canonical(cc.Config)
			out[k] = cc
		}
		memo[id] = out
		return out
	}
	var out []resolved
	for _, id := range order {
		if bad[id] {
			continue
		}
		ch := children(id)
		out = append(out, resolved{decl: byID[id], children: ch, digest: digest(ch, version)})
	}
	return out, issues
}

// checkChild returns why child key cannot mount, or "".
func checkChild(cat Catalogue, key string, c Child) string {
	name := ComponentOf(key, c)
	item, err := cat.Lookup(name)
	if err != nil {
		return fmt.Sprintf("component %q: %v", name, err)
	}
	raw := bytes.TrimSpace(c.Config)
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	if item.Config == nil {
		if string(raw) == "{}" {
			return ""
		}
		return fmt.Sprintf("component %q takes no config", name)
	}
	// An expression is computed by the revision's loader; its value is checked there.
	if bytes.Contains(raw, []byte("${{")) || bytes.Contains(raw, []byte(`"$expr"`)) {
		return ""
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(reflect.New(item.Config).Interface()); err != nil {
		return fmt.Sprintf("component %q: config: %v", name, err)
	}
	return ""
}

// canonical is raw re-encoded with no insignificant whitespace and object keys
// sorted (encoding/json sorts map keys), so two spellings of one config, including
// ones that differ only in key order, have one digest. Numbers keep their literal
// text (UseNumber). raw unchanged when it is not a single valid JSON value.
func canonical(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return slices.Clone(raw)
	}
	if _, err := dec.Token(); err != io.EOF {
		return slices.Clone(raw)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return slices.Clone(raw)
	}
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

// digest is "sha256:" and the hex SHA-256 of the resolved children and the harness
// version (bundle-preset.md §2 "Digest"). encoding/json writes map keys sorted, so
// the text is canonical.
func digest(children map[string]Child, version string) string {
	b, _ := json.Marshal(struct {
		Version  string           `json:"version"`
		Children map[string]Child `json:"children"`
	}{version, children})
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
