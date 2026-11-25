// Package persona is agent.persona (C43, DH-D-BUNDLE-5, docs/harness/areas/
// bundle-preset.md, UP-CFG-dsh-persona): the persona of one agent preset. Mounted
// as a child of an agent.preset declaration it registers, in the preset revision's
// scope, a persona-prefix and a persona-suffix section that shadow tools.prompt's
// global ones for the agents bound to that preset only. Complete makes the persona
// the whole system prompt (one complete section); IncludeRuntimeContext false and
// SuppressContexts remove optional runtime contexts for the preset.
//
// It refuses to load anywhere but in a preset (DH-ADR-BUNDLE-4): the persona of
// every agent is tools.prompt's personaPrefix and personaSuffix.
package persona

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/agentpreset"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/catalog"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/scope"
	"github.com/smartpcr/agent-plugin/src/harness/internal/tools/prompt"
)

// Name is the component's catalogue name.
const Name = agentpreset.PersonaName

// Config is UP-CFG-dsh-persona.
type Config struct {
	// Prefix is the persona's opening text; required.
	Prefix string `json:"prefix"`
	// Suffix is the text after the tools and instructions.
	Suffix string `json:"suffix,omitempty"`
	// Complete makes prefix and suffix the whole system prompt of the preset's
	// agents: every other section is dropped.
	Complete bool `json:"complete,omitempty"`
	// IncludeRuntimeContext false removes every optional runtime context for the
	// preset's agents; nil is true.
	IncludeRuntimeContext *bool `json:"includeRuntimeContext,omitempty"`
	// SuppressContexts removes the named optional runtime contexts; an unknown or
	// non-optional name fails the load.
	SuppressContexts []string `json:"suppressContexts,omitempty"`
}

// Validate is the static check of the config.
func (c Config) Validate() error {
	var issues []cordis.ConfigIssue
	if strings.TrimSpace(c.Prefix) == "" {
		issues = append(issues, cordis.ConfigIssue{Path: "prefix", Message: "prefix is required"})
	}
	seen := map[string]bool{}
	for i, n := range c.SuppressContexts {
		at := fmt.Sprintf("suppressContexts[%d]", i)
		switch {
		case strings.TrimSpace(n) == "" || strings.TrimSpace(n) != n || n == prompt.AllContexts:
			issues = append(issues, cordis.ConfigIssue{Path: at, Message: fmt.Sprintf("%q is not a runtime context name", n)})
		case seen[n]:
			issues = append(issues, cordis.ConfigIssue{Path: at, Message: fmt.Sprintf("%q is listed twice", n)})
		}
		seen[n] = true
	}
	if len(issues) > 0 {
		return &cordis.ConfigError{Issues: issues}
	}
	return nil
}

// ErrOutsidePreset is the load error of a persona mounted outside a preset.
var ErrOutsidePreset = errors.New(agentpreset.PersonaOutsidePreset)

// Mount registers cfg's persona in svc for ctx's scope, which must be a preset
// revision's; what it registers goes when ctx's fiber does.
func Mount(ctx *cordis.Context, svc *prompt.Service, cfg Config) error {
	if _, ok := agentpreset.PresetOf(ctx); !ok {
		return ErrOutsidePreset
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	k := scope.Of(ctx)
	for _, n := range cfg.SuppressContexts {
		optional, ok := svc.RuntimeContextOf(k, n)
		switch {
		case !ok:
			return fmt.Errorf("%s: suppressContexts: no runtime context %q is registered", Name, n)
		case !optional:
			return fmt.Errorf("%s: suppressContexts: runtime context %q is required and cannot be suppressed", Name, n)
		}
	}
	var secs []prompt.Section
	if cfg.Complete {
		text := cfg.Prefix
		if cfg.Suffix != "" {
			text += "\n\n" + cfg.Suffix
		}
		secs = []prompt.Section{{Name: prompt.SectionPersonaPrefix, Order: prompt.OrderPersonaPrefix, Text: text, Complete: true}}
	} else {
		secs = []prompt.Section{
			{Name: prompt.SectionPersonaPrefix, Order: prompt.OrderPersonaPrefix, Text: cfg.Prefix},
			// An empty suffix still shadows the global one: the preset's persona is
			// the whole persona.
			{Name: prompt.SectionPersonaSuffix, Order: prompt.OrderPersonaSuffix, Text: cfg.Suffix},
		}
	}
	for _, s := range secs {
		if _, err := svc.Section(ctx, s); err != nil {
			return err
		}
	}
	suppress := cfg.SuppressContexts
	if cfg.IncludeRuntimeContext != nil && !*cfg.IncludeRuntimeContext {
		suppress = []string{prompt.AllContexts}
	}
	for _, n := range suppress {
		if _, err := svc.Suppress(ctx, n); err != nil {
			return err
		}
	}
	return nil
}

// Component is agent.persona: it injects tools.prompt.
var Component = cordis.Define(Name, func(ctx *cordis.Context, cfg Config) error {
	svc, err := prompt.Key.Get(ctx)
	if err != nil {
		return err
	}
	return Mount(ctx, svc, cfg)
}, cordis.WithInject(prompt.Key.Name()))

// Item is the agent.persona catalogue item.
func Item() catalog.Item {
	return catalog.Item{Name: Name, Component: Component, Config: reflect.TypeFor[Config]()}
}
