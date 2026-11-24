// Package permission is the one "Permissions" choice of a person (C34,
// DH-D-HUMAN-4; impl.md 12.4): a named permission preset is a pair of a sandbox
// mode and an approval policy, and nothing else (DH-ADR-HUMAN-6). The table is
// live configuration (permission.presets) whose DefaultPreset must name one of
// its entries, so a layer whose default does not resolve fails the config load
// (config/rejected, status 78).
//
// The enforcing services keep their own values: a switch appends
// permission/preset and then writes each knob through its owner, the sandbox
// resolver's SetMode and the approval service's SetPolicy, each of which writes
// only when its value changes. A switch revokes the session's grants. A fresh
// session is pinned to the default preset when its agent is created; a resumed
// one keeps its values. Current reads the last selection while its knobs still
// hold, else the first entry the knobs match, else the derived label custom.
// The command /permission [preset] is the owner's switch.
package permission

import (
	"errors"
	"fmt"
	"regexp"
	"slices"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/human/approval"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/views"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// The labels no table entry may take the place of.
const (
	// Custom is the derived label of a knob pair no entry matches; no entry may
	// be named custom.
	Custom = "custom"
	// Auto is reserved for the experimental review layer: an entry may be named
	// auto, but it can never be the default for new sessions.
	Auto = "auto"
)

// The presets shipped (interaction-credentials.md "Presets shipped").
const (
	PresetReadOnly         = "read-only"
	PresetWorkspaceWrite   = "workspace-write"
	PresetDangerFullAccess = "danger-full-access"
)

// Preset is one named pair of a sandbox mode and an approval policy.
type Preset struct {
	Name        string          `json:"name"`
	Mode        sandbox.Mode    `json:"mode"`
	Approval    approval.Policy `json:"approval"`
	Description string          `json:"description,omitempty"`
}

// matches reports whether p is the pair mode, policy.
func (p Preset) matches(mode sandbox.Mode, policy approval.Policy) bool {
	return p.Mode == mode && p.Approval == policy
}

// Config is permission.presets: the preset table, in the order Current matches
// it, and the preset a fresh session is pinned to.
type Config struct {
	Presets       []Preset `json:"presets,omitempty"`
	DefaultPreset string   `json:"defaultPreset,omitempty"`
}

// DefaultConfig is the shipped table: read-only, workspace-write (the default)
// and danger-full-access.
func DefaultConfig() Config {
	return Config{
		Presets: []Preset{
			{Name: PresetReadOnly, Mode: sandbox.ReadOnly, Approval: approval.PolicyAsk,
				Description: "Reads; every write needs an escalation a person approves"},
			{Name: PresetWorkspaceWrite, Mode: sandbox.WorkspaceWrite, Approval: approval.PolicyAsk,
				Description: "Writes inside the write roots; anything wider is asked"},
			{Name: PresetDangerFullAccess, Mode: sandbox.DangerFullAccess, Approval: approval.PolicyNever,
				Description: "Everything the user can do; nothing is asked and nothing can be escalated"},
		},
		DefaultPreset: PresetWorkspaceWrite,
	}
}

// validName is what /permission can name: the command-name alphabet.
var validName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Validate implements cordis.Validator: every entry has a valid, unique name, a
// sandbox mode and an approval policy, and DefaultPreset names an entry that is
// not auto. A rejection is a *cordis.ConfigError whose issues are placed at the
// fields at fault, so the config check reports each at its value.
func (c Config) Validate() error {
	var issues []cordis.ConfigIssue
	add := func(path, format string, args ...any) {
		issues = append(issues, cordis.ConfigIssue{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	if len(c.Presets) == 0 {
		add("presets", "the permission preset table is empty")
	}
	seen := map[string]bool{}
	for i, p := range c.Presets {
		at := fmt.Sprintf("presets.%d", i)
		switch {
		case !validName.MatchString(p.Name):
			add(at+".name", "permission preset name %q is not 1 to 32 of [a-z0-9-] starting with a letter", p.Name)
		case p.Name == Custom:
			add(at+".name", "permission preset name %q is the derived label of an unmatched pair", p.Name)
		case seen[p.Name]:
			add(at+".name", "permission preset %q is listed twice", p.Name)
		}
		seen[p.Name] = true
		if !p.Mode.Valid() {
			add(at+".mode", "permission preset %q: mode %q is not read-only, workspace-write or danger-full-access", p.Name, p.Mode)
		}
		if !p.Approval.Valid() {
			add(at+".approval", "permission preset %q: approval policy %q is not ask or never", p.Name, p.Approval)
		}
	}
	switch {
	case c.DefaultPreset == "":
		add("defaultPreset", "no default permission preset is named")
	case c.DefaultPreset == Auto:
		add("defaultPreset", "default permission preset %q is reserved and cannot be the default for new sessions", c.DefaultPreset)
	case !slices.ContainsFunc(c.Presets, func(p Preset) bool { return p.Name == c.DefaultPreset }):
		add("defaultPreset", "default permission preset %q is not a preset of the table", c.DefaultPreset)
	}
	if len(issues) > 0 {
		return &cordis.ConfigError{Issues: issues}
	}
	return nil
}

// lookup is the entry of c named name.
func (c Config) lookup(name string) (Preset, bool) {
	i := slices.IndexFunc(c.Presets, func(p Preset) bool { return p.Name == name })
	if i < 0 {
		return Preset{}, false
	}
	return c.Presets[i], true
}

// PrincipalHarness is the principal of a pin no person made.
const PrincipalHarness = approval.PrincipalHarness

// PresetSet is permission/preset: the session's selection became Preset, the
// pair Mode and Approval, set by By. Pinned marks the pin of a fresh session
// at agent creation.
type PresetSet struct {
	Preset   string          `json:"preset"`
	Mode     sandbox.Mode    `json:"mode"`
	Approval approval.Policy `json:"approval"`
	Pinned   bool            `json:"pinned,omitempty"`
	By       approval.By     `json:"by"`
}

// PresetType is the durable, log-only permission/preset event.
var PresetType = event.MustDefine(event.Default(), event.Spec[PresetSet]{Name: "permission/preset", Version: 1,
	Validate: func(p PresetSet) error {
		if p.Preset == "" {
			return errors.New("permission/preset: no preset is named")
		}
		if !p.Mode.Valid() {
			return fmt.Errorf("permission/preset: mode %q is not a sandbox mode", p.Mode)
		}
		if !p.Approval.Valid() {
			return fmt.Errorf("permission/preset: approval policy %q is not ask or never", p.Approval)
		}
		if p.By.Principal == "" {
			return errors.New("permission/preset: a selection must name its principal")
		}
		return nil
	}})

// ViewKey is the key of the permissions fold.
const ViewKey = "permissions"

// State is the permissions fold of one session (architecture.md "Permissions"):
// the last selection, the pair it implies, who made it and, once Pinned, the seq
// of the pin of the fresh session. An empty Preset means nothing was selected.
type State struct {
	Preset   string          `json:"preset,omitempty"`
	Mode     sandbox.Mode    `json:"mode,omitempty"`
	Approval approval.Policy `json:"approval,omitempty"`
	By       approval.By     `json:"by,omitzero"`
	Pinned   bool            `json:"pinned,omitempty"`
	PinnedAt event.Seq       `json:"pinnedAt,omitempty"`
}

func apply(s State, ev event.Event) (State, bool) {
	if ev.Type() != PresetType.Name() {
		return s, false
	}
	p, err := PresetType.As(ev)
	if err != nil {
		return s, false
	}
	next := State{Preset: p.Preset, Mode: p.Mode, Approval: p.Approval, By: p.By, Pinned: s.Pinned, PinnedAt: s.PinnedAt}
	if p.Pinned {
		next.Pinned, next.PinnedAt = true, ev.Seq()
	}
	return next, true
}

// View is the permissions fold (session-views registration, DH-D-VIEW-1): the
// last permission/preset of the log. It is the selection, not the authority:
// the knobs in force are the sandbox-mode and approval folds.
func View() views.Definition[State] {
	return views.Definition[State]{
		Key: ViewKey, Version: 1, Stored: true, Types: []string{PresetType.Name()},
		Init:  func(session.Header) State { return State{} },
		Apply: apply,
	}
}

// RegisterView registers the permissions fold in r.
func RegisterView(r *views.Registry) (views.Handle[State], error) {
	return views.Register(r, View())
}
