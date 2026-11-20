package cli

import (
	"fmt"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/boot/profile"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/cmdline"
	"github.com/smartpcr/agent-plugin/src/harness/internal/cli/output"
)

// Reference renders the command reference in Markdown from the running tree: the
// launcher, every visible fixed command, the built-in profiles and the exit table
// (DH-LIB-COMMANDER-R7). internal/cli/testdata/reference.golden holds it, so a stale
// reference fails the build's tests.
func Reference() string {
	var b strings.Builder
	b.WriteString("# dsh command reference\n\n")
	b.WriteString("Generated from the command tree by `dsh catalog commands`. Do not edit by hand.\n")
	section := func(title, body string) {
		fmt.Fprintf(&b, "\n## %s\n\n```text\n%s```\n", title, body)
	}
	t := newTree()
	var help strings.Builder
	_ = cmdline.Help(&help, launcherSpec(), t.commandSection(), profileSection())
	section("dsh", help.String())
	t.walk(func(c *command) {
		help.Reset()
		_ = c.help(&help)
		section(c.spec.Name, help.String())
	})
	b.WriteString("\n## Profiles\n\n| Profile | Bundles | Summary |\n|---|---|---|\n")
	for _, d := range profile.Builtins() {
		fmt.Fprintf(&b, "| `%s` | %s | %s |\n", d.Name, strings.Join(d.Bundles, ", "), d.Summary)
	}
	b.WriteString("\n## Exit statuses\n\n| Status | Name | Meaning |\n|---|---|---|\n")
	for _, s := range output.Statuses() {
		fmt.Fprintf(&b, "| %d | %s | %s |\n", s.Code, s.Name, s.Meaning)
	}
	b.WriteString("\n## Reserved words\n\nNo profile may be called: ")
	words := Reserved()
	for i, w := range words {
		words[i] = "`" + w + "`"
	}
	b.WriteString(strings.Join(words, ", ") + ".\n")
	return b.String()
}

// Reference documents of `dsh catalog commands --json`; the field names are part of
// the contract.
type (
	ReferenceDocument struct {
		Commands []CommandDoc        `json:"commands"`
		Profiles []ProfileDoc        `json:"profiles"`
		Statuses []output.StatusInfo `json:"statuses"`
		Reserved []string            `json:"reserved"`
	}
	CommandDoc struct {
		Name     string      `json:"name"`
		Summary  string      `json:"summary"`
		Usage    string      `json:"usage"`
		Options  []OptionDoc `json:"options"`
		Commands []string    `json:"commands,omitempty"`
	}
	OptionDoc struct {
		Name    string   `json:"name"`
		Short   string   `json:"short,omitempty"`
		Kind    string   `json:"kind"`
		Default string   `json:"default,omitempty"`
		Choices []string `json:"choices,omitempty"`
		Help    string   `json:"help"`
	}
	ProfileDoc struct {
		Name    string   `json:"name"`
		Summary string   `json:"summary"`
		Bundles []string `json:"bundles"`
	}
)

var kindNames = map[cmdline.Kind]string{
	cmdline.Bool: "bool", cmdline.String: "string", cmdline.Int: "int", cmdline.Strings: "strings", cmdline.Choice: "choice",
}

func commandDoc(spec cmdline.Spec, children []*command) CommandDoc {
	doc := CommandDoc{Name: spec.Name, Summary: spec.Summary, Usage: cmdline.UsageLine(spec), Options: []OptionDoc{}}
	for _, o := range append(spec.Options, cmdline.HelpOption) {
		if o.Hidden {
			continue
		}
		doc.Options = append(doc.Options, OptionDoc{Name: o.Name, Short: o.Short, Kind: kindNames[o.Kind],
			Default: o.Default, Choices: o.Choices, Help: o.Help})
	}
	for _, c := range children {
		doc.Commands = append(doc.Commands, c.word())
	}
	return doc
}

// ReferenceDoc is the JSON form of Reference.
func ReferenceDoc() ReferenceDocument {
	t := newTree()
	doc := ReferenceDocument{Statuses: output.Statuses(), Reserved: Reserved()}
	doc.Commands = append(doc.Commands, commandDoc(launcherSpec(), t.commands))
	t.walk(func(c *command) { doc.Commands = append(doc.Commands, commandDoc(c.spec, c.children)) })
	for _, d := range profile.Builtins() {
		doc.Profiles = append(doc.Profiles, ProfileDoc{Name: d.Name, Summary: d.Summary, Bundles: d.Bundles})
	}
	return doc
}
