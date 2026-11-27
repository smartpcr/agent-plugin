package cli

// dsh config show and dsh config validate (DH-D-BOOT-6, C50, closing H-21): the
// boot preview offered as admin read-only commands. Each composes a profile's
// layers and checks the tree as a launch would (boot.PreviewLaunch) and mounts no
// tree at all, so the component trace is empty and nothing is written to the home.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/smartpcr/agent-plugin/src/go/loader"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/config"
	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/cmdline"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

func profileOption() cmdline.Option {
	return cmdline.Option{Name: "profile", Kind: cmdline.String, Value: "name", Help: "The profile whose composition is read"}
}

// configShowDoc is the JSON form of `dsh config show --json`; its fields are part
// of the contract.
type configShowDoc struct {
	Profile string                `json:"profile"`
	Layers  []string              `json:"layers"`
	Entries []config.PreviewEntry `json:"entries"`
	Issues  []string              `json:"issues"`
	Notes   []string              `json:"notes"`
}

// configValidateDoc is the JSON form of a valid `dsh config validate --json`.
type configValidateDoc struct {
	Valid    bool     `json:"valid"`
	Profiles []string `json:"profiles"`
}

// preview composes the layers of def under --home with files as overlays above
// the user layer, in a tree that is never mounted.
func (st *state) preview(def profile.Definition, files []string, defaults bool) (*boot.Preview, error) {
	if st.env.probeErr != nil {
		return nil, st.env.probeErr
	}
	req := st.env.Request
	req.Args = slices.Clone(req.Args)
	req.Home, req.Profile, req.Overlays, req.Words, req.Safe = st.home, def.Name, slices.Clone(files), nil, false
	return boot.PreviewLaunch(st.ctx, boot.Options{Request: req, Profile: def, Stderr: st.env.Stderr}, defaults)
}

// previewTrace prints the component trace of a preview when the trace switch is
// on in its launch environment: "none", as no tree is mounted.
func (st *state) previewTrace(p *boot.Preview) {
	if p == nil || p.Env == nil {
		return
	}
	if _, _, ok := p.Env.Lookup(TraceEnv); ok {
		(&adminTree{state: &boot.State{}, trace: true}).close(st.env.Stderr)
	}
}

// configProfile is the profile named by --profile or by the one argument; both
// at once is a usage error and neither is "".
func configProfile(command string, v *cmdline.Values, positional bool) (string, error) {
	name := v.String("profile")
	if positional && len(v.Args()) > 0 {
		if name != "" {
			return "", cmdline.Usagef(command, "the profile is given twice: --profile %s and %s", name, v.Args()[0])
		}
		name = v.Args()[0]
	}
	return name, nil
}

func runConfigShow(st *state, v *cmdline.Values) error {
	name, err := configProfile("dsh config show", v, true)
	if err != nil {
		return err
	}
	if name == "" {
		return cmdline.Usagef("dsh config show", "missing <profile> (one of %s)", strings.Join(profile.Names(), ", "))
	}
	def, err := profile.Lookup(name)
	if err != nil {
		return err
	}
	format := v.String("format")
	switch format {
	case "", "text", "yaml", "json":
	default:
		return cmdline.Usagef("dsh config show", "--format %s: want text, yaml or json", format)
	}
	// --json makes the result one JSON value, so only --format json agrees with it.
	if st.res.JSON() && format != "" && format != "json" {
		return cmdline.Usagef("dsh config show", "--format %s conflicts with --json", format)
	}
	p, err := st.preview(def, nil, v.Bool("defaults"))
	st.previewTrace(p)
	if p == nil || p.Report == nil {
		return err
	}
	if format == "yaml" {
		// The dump is the tree alone; the issues and notes go to stderr.
		out, derr := p.Report.DumpYAML()
		if derr != nil {
			return derr
		}
		for _, issue := range p.Report.Issues {
			fmt.Fprintf(st.env.Stderr, "dsh: issue: %s\n", issue.Error())
		}
		for _, note := range p.Notes {
			fmt.Fprintf(st.env.Stderr, "dsh: note: %s\n", note.Error())
		}
		return st.res.Text(string(out))
	}
	// A tree that breaks its Required list is still shown, with the issue.
	doc := configShowDoc{Profile: def.Name, Layers: p.Layers, Entries: p.Report.Entries(), Issues: issueTexts(p.Report.Issues), Notes: issueTexts(p.Notes)}
	if doc.Entries == nil {
		doc.Entries = []config.PreviewEntry{}
	}
	if st.res.JSON() {
		return st.res.Value(doc)
	}
	if format == "json" {
		out, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return err
		}
		return st.res.Line(string(out))
	}
	return st.res.Text(configReport(doc, p.Report.Index))
}

// configReport is the text form of a preview: the profile and its layers, then
// each entry with the layer that inserted it and each value of it, a leaf of its
// config or its disabled flag, with the layer that supplied that value; then the
// issues.
func configReport(d configShowDoc, index *loader.Index) string {
	var b strings.Builder
	fmt.Fprintf(&b, "profile: %s\n", d.Profile)
	fmt.Fprintf(&b, "layers:  %s\n", strings.Join(d.Layers, ", "))
	b.WriteString("entries:\n")
	for _, e := range d.Entries {
		kind := e.Name
		if e.Group {
			kind = "group"
		}
		layer := e.Layer
		if layer == "" {
			layer = "unindexed"
		}
		fmt.Fprintf(&b, "  %s (%s)  # %s\n", e.ID, kind, layer)
		owner := func(field string) string {
			if l, ok := index.Owner(e.ID, field); ok {
				return l
			}
			return layer
		}
		switch {
		case e.DisabledExpr != "":
			fmt.Fprintf(&b, "    disabled = !expr %s  # %s\n", e.DisabledExpr, owner("disabled"))
		case e.Disabled:
			fmt.Fprintf(&b, "    disabled = true  # %s\n", owner("disabled"))
		}
		for _, leaf := range leaves("config", e.Config) {
			fmt.Fprintf(&b, "    %s = %s  # %s\n", leaf[0], leaf[1], owner(leaf[0]))
		}
		if len(e.Inject) > 0 {
			fmt.Fprintf(&b, "    inject = [%s]  # %s\n", strings.Join(e.Inject, ", "), owner("inject"))
		}
		for _, svc := range sortedKeys(e.Isolate) {
			fmt.Fprintf(&b, "    isolate.%s = %s  # %s\n", svc, e.Isolate[svc], owner("isolate"))
		}
		for _, svc := range sortedKeys(e.Intercept) {
			fmt.Fprintf(&b, "    intercept.%s = %s  # %s\n", svc, e.Intercept[svc], owner("intercept"))
		}
	}
	section(&b, "issues", d.Issues)
	section(&b, "notes", d.Notes)
	return b.String()
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func section(b *strings.Builder, title string, lines []string) {
	if len(lines) == 0 {
		fmt.Fprintf(b, "%s:  none\n", title)
		return
	}
	fmt.Fprintf(b, "%s:\n", title)
	for _, line := range lines {
		fmt.Fprintf(b, "  %s\n", line)
	}
}

func issueTexts(issues []loader.Issue) []string {
	out := make([]string, 0, len(issues))
	for _, issue := range issues {
		out = append(out, issue.Error())
	}
	return out
}

// leaves flattens a config into its leaf values, path first, keys in order: an
// object is walked, anything else (an array, a scalar, an expression object) is
// one leaf in compact JSON.
func leaves(path string, raw json.RawMessage) [][2]string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil || len(obj) == 0 {
		return [][2]string{{path, string(raw)}}
	}
	if _, isExpr := obj["$expr"]; isExpr {
		return [][2]string{{path, string(raw)}}
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out [][2]string
	for _, k := range keys {
		out = append(out, leaves(path+"."+k, obj[k])...)
	}
	return out
}

// runConfigValidate checks the composition of the profile given, or of every
// built-in profile, with the files as overlays above the user layer: a clean
// check prints the profiles checked; any issue is config/rejected (status 78)
// naming each issue at its file, line and field, per profile.
func runConfigValidate(st *state, v *cmdline.Values) error {
	name, err := configProfile("dsh config validate", v, false)
	if err != nil {
		return err
	}
	defs := profile.Builtins()
	if name != "" {
		def, err := profile.Lookup(name)
		if err != nil {
			return err
		}
		defs = []profile.Definition{def}
	}
	var fails []error
	var names []string
	for i, def := range defs {
		names = append(names, def.Name)
		p, err := st.preview(def, v.Args(), false)
		if i == 0 {
			st.previewTrace(p)
		}
		if p != nil {
			for _, note := range p.Notes {
				fmt.Fprintf(st.env.Stderr, "dsh: note: profile %s: %s\n", def.Name, note.Error())
			}
		}
		switch {
		case err != nil && (p == nil || p.Report == nil):
			// A layer that cannot be read fails every profile alike.
			return err
		case err != nil:
			// The Required failure names the profile and the ids.
			fails = append(fails, err)
		case !p.Report.OK():
			fails = append(fails, herr.Wrap(config.ReasonRejected, p.Report.Err(), "profile "+def.Name+": the configuration is invalid"))
		}
	}
	if len(fails) > 0 {
		return errors.Join(fails...)
	}
	if st.res.JSON() {
		return st.res.Value(configValidateDoc{Valid: true, Profiles: names})
	}
	return st.res.Line("valid: " + strings.Join(names, ", "))
}
