package cmdline

import (
	"fmt"
	"io"
	"strings"
)

// UsageLine returns "Usage: <name> [options] <args>".
func UsageLine(spec Spec) string {
	var b strings.Builder
	b.WriteString(spec.Name)
	b.WriteString(" [options]")
	if a := spec.Args; a.Max != 0 {
		name := "<" + argName(a) + ">"
		switch {
		case a.Min == 0 && a.Max == 1:
			name = "[" + argName(a) + "]"
		case a.Max < 0 && a.Min == 0:
			name = "[" + argName(a) + "...]"
		case a.Max < 0 || a.Max > 1:
			name += "..."
		}
		b.WriteString(" " + name)
	}
	return b.String()
}

// OptionLabel renders an option's left column: "-h, --help" or "    --home <dir>".
func OptionLabel(o Option) string {
	label := "    --" + o.Name
	if o.Short != "" {
		label = "-" + o.Short + ", --" + o.Name
	}
	switch o.Kind {
	case Bool:
	case Choice:
		label += " <" + strings.Join(o.Choices, "|") + ">"
	default:
		v := o.Value
		if v == "" {
			v = "value"
		}
		label += " <" + v + ">"
	}
	return label
}

// OptionHelp renders an option's right column: its help, then what it repeats or
// defaults to.
func OptionHelp(o Option) string {
	text := o.Help
	if o.Kind == Strings {
		text += " (repeatable)"
	}
	if o.Default != "" && o.Kind != Bool {
		text += fmt.Sprintf(" (default %s)", o.Default)
	}
	return text
}

func visible(options []Option) []Option {
	var out []Option
	for _, o := range options {
		if !o.Hidden {
			out = append(out, o)
		}
	}
	return out
}

// Section is one extra block of a help text, such as the commands of a group.
type Section struct {
	Title string
	Rows  [][2]string
}

// Help writes the help of spec: usage line, description, arguments, options, the
// extra sections and the examples.
func Help(w io.Writer, spec Spec, sections ...Section) error {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: %s\n", UsageLine(spec))
	if spec.Summary != "" {
		fmt.Fprintf(&b, "\n%s\n", spec.Summary)
	}
	if spec.Description != "" {
		fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(spec.Description))
	}
	for _, s := range sections {
		if len(s.Rows) > 0 {
			table(&b, s.Title, s.Rows)
		}
	}
	if spec.Args.Max != 0 && spec.Args.Help != "" {
		table(&b, "Arguments", [][2]string{{argName(spec.Args), spec.Args.Help}})
	}
	var rows [][2]string
	for _, o := range append(visible(spec.Options), HelpOption) {
		rows = append(rows, [2]string{OptionLabel(o), OptionHelp(o)})
	}
	table(&b, "Options", rows)
	if len(spec.Examples) > 0 {
		b.WriteString("\nExamples:\n")
		for _, e := range spec.Examples {
			fmt.Fprintf(&b, "  %s\n", e.Command)
			if e.Help != "" {
				fmt.Fprintf(&b, "      %s\n", e.Help)
			}
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

func table(b *strings.Builder, title string, rows [][2]string) {
	width := 0
	for _, r := range rows {
		width = max(width, len(r[0]))
	}
	fmt.Fprintf(b, "\n%s:\n", title)
	for _, r := range rows {
		fmt.Fprintf(b, "  %-*s  %s\n", width, r[0], r[1])
	}
}
