package vocab

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/schema"
)

// RouteID names a route; it is matched exactly, with no default and no alias.
type RouteID string

// Validate checks that the id is not blank or padded.
func (r RouteID) Validate() error {
	if r == "" || strings.TrimSpace(string(r)) != string(r) {
		return fmt.Errorf("vocab: route id %q is blank or padded", string(r))
	}
	return nil
}

// ToolChoiceMode is how the model may use the tools; the set is closed.
type ToolChoiceMode string

// The tool-choice modes. ToolChoiceTool forces the tool named by ToolChoice.Name.
const (
	ToolChoiceAuto     ToolChoiceMode = "auto"
	ToolChoiceNone     ToolChoiceMode = "none"
	ToolChoiceRequired ToolChoiceMode = "required"
	ToolChoiceTool     ToolChoiceMode = "tool"
)

// ToolChoice constrains tool use; the zero value leaves it to the route.
type ToolChoice struct {
	Mode ToolChoiceMode `json:"mode,omitempty" dsh:"enum=auto|none|required|tool"`
	Name string         `json:"name,omitempty"`
}

// Validate checks that a name is given exactly when one tool is forced.
func (c ToolChoice) Validate() error {
	switch c.Mode {
	case "", ToolChoiceAuto, ToolChoiceNone, ToolChoiceRequired:
		if c.Name != "" {
			return fmt.Errorf("vocab: tool choice %q takes no name", c.Mode)
		}
	case ToolChoiceTool:
		if c.Name == "" {
			return errors.New("vocab: a forced tool choice names its tool")
		}
	default:
		return fmt.Errorf("vocab: tool choice %q is not one of the closed set", c.Mode)
	}
	return nil
}

// Controls are the per-call model controls (DH-ADR-LLM-1). A zero or nil field
// is unset and the route's default applies; a driver encodes each set field or
// fails with llm/unsupported-option before I/O. Effort is a level from the
// model's catalogue entry, which the runtime validates when preparing a call.
type Controls struct {
	Effort          string     `json:"effort,omitempty"`
	Temperature     *float64   `json:"temperature,omitempty"`
	TopP            *float64   `json:"top_p,omitempty"`
	MaxOutputTokens int        `json:"max_output_tokens,omitempty"`
	Stop            []string   `json:"stop,omitempty"`
	ToolChoice      ToolChoice `json:"tool_choice,omitzero"`
}

// Clone is a deep copy.
func (c Controls) Clone() Controls {
	c.Temperature, c.TopP, c.Stop = clonePtr(c.Temperature), clonePtr(c.TopP), slices.Clone(c.Stop)
	return c
}

// Validate checks the ranges each control has on every protocol.
func (c Controls) Validate() error {
	finite := func(p *float64, lo, hi float64, name string) error {
		if p != nil && (math.IsNaN(*p) || *p < lo || *p > hi) {
			return fmt.Errorf("vocab: %s %v is outside %v to %v", name, *p, lo, hi)
		}
		return nil
	}
	if err := finite(c.Temperature, 0, 2, "temperature"); err != nil {
		return err
	}
	if err := finite(c.TopP, 0, 1, "top_p"); err != nil {
		return err
	}
	if c.MaxOutputTokens < 0 {
		return fmt.Errorf("vocab: max_output_tokens %d is negative", c.MaxOutputTokens)
	}
	if strings.TrimSpace(c.Effort) != c.Effort {
		return fmt.Errorf("vocab: effort %q is padded", c.Effort)
	}
	if slices.Contains(c.Stop, "") {
		return errors.New("vocab: a stop sequence is empty")
	}
	return c.ToolChoice.Validate()
}

// CallConfig is a prepared call's effective controls, and which of them were
// the adapter's defaults rather than the caller's.
type CallConfig struct {
	Controls  Controls `json:"controls"`
	Defaulted []string `json:"defaulted,omitempty"`
}

// Validate checks the controls and that Defaulted names controls.
func (c CallConfig) Validate() error {
	names := []string{"effort", "temperature", "top_p", "max_output_tokens", "stop", "tool_choice"}
	for _, d := range c.Defaulted {
		if !slices.Contains(names, d) {
			return fmt.Errorf("vocab: defaulted control %q is not a control", d)
		}
	}
	return c.Controls.Validate()
}

// Meta labels a request: the session it belongs to, as an opaque label, and the
// purpose of the call (a loop step, a title, a summary, a review).
type Meta struct {
	Session string `json:"session"`
	Purpose string `json:"purpose"`
}

// Validate checks that neither label is padded and the purpose is given.
func (m Meta) Validate() error {
	if m.Purpose == "" || strings.TrimSpace(m.Purpose) != m.Purpose || strings.TrimSpace(m.Session) != m.Session {
		return fmt.Errorf("vocab: meta %+v has a blank purpose or a padded label", m)
	}
	return nil
}

// ToolSchema is one tool as the model sees it. Parameters is the canonical JSON
// Schema document, an object root inside the model-facing subset that package
// schema enforces; each driver sends a projection of it.
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Validate checks the name and that Parameters is an object schema inside the
// model-facing subset (llm.md, "Tool schemas sent to the model"); the error names
// every offending path (DH-LIB-ZOD-R3). Nothing outside the subset is weakened
// into it.
func (t ToolSchema) Validate() error {
	if t.Name == "" || strings.ContainsAny(t.Name, " \t\r\n") {
		return fmt.Errorf("vocab: tool name %q is blank or holds white space", t.Name)
	}
	dec := json.NewDecoder(bytes.NewReader(t.Parameters))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return fmt.Errorf("vocab: the parameters of tool %s are not an object schema", t.Name)
	}
	if root, _ := doc.(map[string]any); root == nil || root["type"] != "object" {
		return fmt.Errorf("vocab: the parameters of tool %s are not an object schema", t.Name)
	}
	typ, err := schema.FromJSON(t.Parameters)
	if err != nil {
		return fmt.Errorf("vocab: tool %s: %w", t.Name, err)
	}
	if issues := typ.CheckSubset(modelSubset); len(issues) > 0 {
		return fmt.Errorf("vocab: tool %s: the parameters leave the model-facing subset: %w", t.Name, issues)
	}
	return nil
}

// modelSubset is the model-facing subset: upstream's keywords
// (packages/core/tools/src/json-schema.ts:1-11) plus the constraints a dsh tag
// derives. $ref, allOf, anyOf, not, if/then/else, format and every other keyword
// are outside it; "type" names one type, additionalProperties is true or false,
// and enum and const hold scalars.
var modelSubset = schema.Subset{
	Keywords: []string{
		"type", "properties", "required", "additionalProperties",
		"items", "enum", "const", "oneOf",
		"description", "title", "default", "examples",
		"minimum", "maximum", "pattern",
		"minLength", "maxLength", "minItems", "maxItems",
		"minProperties", "maxProperties",
	},
	SingleType:        true,
	BooleanAdditional: true,
	ScalarLiterals:    true,
}

// Usage is disjoint token counts: uncached input, cache read, cache write and
// output (reasoning is inside output). Total is their sum when the provider
// reports one.
type Usage struct {
	Input      int `json:"input"`
	CacheRead  int `json:"cache_read"`
	CacheWrite int `json:"cache_write"`
	Output     int `json:"output"`
	Total      int `json:"total,omitempty"`
}

// Validate checks that no count is negative.
func (u Usage) Validate() error {
	if u.Input < 0 || u.CacheRead < 0 || u.CacheWrite < 0 || u.Output < 0 || u.Total < 0 {
		return fmt.Errorf("vocab: usage %+v has a negative count", u)
	}
	return nil
}
