package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Meta records which upstream commit the inventory was taken from.
type Meta struct {
	Upstream string         `json:"upstream"`
	Commit   string         `json:"commit"`
	Counts   map[string]int `json:"counts"`
}

// headCommit reads the checked-out commit of a git working tree without running git.
func headCommit(repo string) string {
	b, err := os.ReadFile(filepath.Join(repo, ".git", "HEAD"))
	if err != nil {
		return "unknown"
	}
	head := strings.TrimSpace(string(b))
	if !strings.HasPrefix(head, "ref: ") {
		return head
	}
	ref := strings.TrimPrefix(head, "ref: ")
	if b, err := os.ReadFile(filepath.Join(repo, ".git", filepath.FromSlash(ref))); err == nil {
		return strings.TrimSpace(string(b))
	}
	if b, err := os.ReadFile(filepath.Join(repo, ".git", "packed-refs")); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if f := strings.Fields(l); len(f) == 2 && f[1] == ref {
				return f[0]
			}
		}
	}
	return "unknown"
}

func writeMeta(c config, counts map[string]int) error {
	m := Meta{Upstream: "https://github.com/deepseek-ai/deepseek-harness", Commit: headCommit(c.upstream), Counts: counts}
	b, _ := json.MarshalIndent(m, "", "  ")
	return os.WriteFile(filepath.Join(c.trace, "inventory", "meta.json"), append(b, '\n'), 0o644)
}

func readMeta(c config) Meta {
	var m Meta
	if b, err := os.ReadFile(filepath.Join(c.trace, "inventory", "meta.json")); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

// reMdLinkFull matches a Markdown link. Upstream titles and agent reasons carry links
// relative to the file they were written in, which do not resolve from trace/, so a
// matrix cell keeps only the link text.
var reMdLinkFull = regexp.MustCompile(`\[([^\]]*)\]\([^)\s]*\)`)

func cell(s string) string {
	s = reMdLinkFull.ReplaceAllString(s, "$1")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.ReplaceAll(s, "|", "\\|")
}

var typeNames = map[string]string{
	"PKG": "Packages and directories", "DEP": "Dependencies", "DEC": "Upstream decisions",
	"DEBT": "Tech debt", "TOOL": "Model-facing tools", "EVT": "Events", "CFG": "Plugin config",
	"SVC": "Services", "CLI": "CLI commands", "PERSIST": "Persisted session events",
}

// runRender writes traceability.md and one matrix per unit type. Both are generated;
// edit the fragments, not these files.
func runRender(c config) error {
	units, err := loadInventory(c)
	if err != nil {
		return err
	}
	led, _, err := runMerge(c, true)
	if err != nil {
		return err
	}
	meta := readMeta(c)
	for id, d := range led.Defined {
		units = append(units, Unit{ID: id, Type: d.Type, Title: d.Title, Attrs: map[string]any{"evidence": d.Evidence, "foundBy": d.Agent}})
	}
	byType := map[string][]Unit{}
	for _, u := range units {
		byType[u.Type] = append(byType[u.Type], u)
	}

	var sb strings.Builder
	sb.WriteString("# Traceability: deepseek-harness to the Go design\n\n")
	sb.WriteString("> **Generated** by `tools/harness-trace render` from the inventory and the ledger\n")
	sb.WriteString("> fragments under [trace/](trace/README.md). Do not edit by hand.\n\n")
	fmt.Fprintf(&sb, "Upstream: %s at commit `%s`.\n\n", meta.Upstream, meta.Commit)
	sb.WriteString("Every upstream unit the inventory found carries one disposition. A unit links to the\n")
	sb.WriteString("Go decision, deliverable or library that accounts for it, and each deliverable links\n")
	sb.WriteString("to the tests and scenarios that prove it. `tools/harness-trace verify` fails if any\n")
	sb.WriteString("unit, link or proof is missing.\n\n")
	sb.WriteString("## Coverage\n\n| Unit type | Units | With disposition | Dispositions | Matrix |\n|---|---|---|---|---|\n")
	total, done := 0, 0
	for _, t := range unitTypes {
		rows := byType[t]
		counts := map[string]int{}
		n := 0
		for _, u := range rows {
			if r, ok := led.Units[u.ID]; ok {
				counts[r.Disposition]++
				n++
			}
		}
		var parts []string
		for _, d := range sortedKeys(counts) {
			parts = append(parts, fmt.Sprintf("%s %d", d, counts[d]))
		}
		total += len(rows)
		done += n
		file := "matrix-" + strings.ToLower(t) + ".md"
		fmt.Fprintf(&sb, "| %s (`UP-%s`) | %d | %d | %s | [%s](trace/%s) |\n", typeNames[t], t, len(rows), n, strings.Join(parts, ", "), file, file)

		var mb strings.Builder
		fmt.Fprintf(&mb, "# %s: disposition matrix\n\n> **Generated.** Do not edit by hand. See [../traceability.md](../traceability.md).\n\n", typeNames[t])
		mb.WriteString("| Unit | What it is | Disposition | Reason | Links | Spec | By |\n|---|---|---|---|---|---|---|\n")
		for _, u := range rows {
			what := u.Title
			if what == "" {
				what = u.Name
			}
			r, ok := led.Units[u.ID]
			if !ok {
				fmt.Fprintf(&mb, "| `%s` | %s | **open** | | | | |\n", u.ID, cell(clip(what, 160)))
				continue
			}
			spec := ""
			if r.Spec != "" {
				spec = fmt.Sprintf("[%s](../%s)", strings.SplitN(r.Spec, "#", 2)[0], r.Spec)
			}
			disp := r.Disposition
			if r.Class != "" {
				disp += " (" + r.Class + ")"
			}
			fmt.Fprintf(&mb, "| `%s` | %s | %s | %s | %s | %s | %s |\n", u.ID, cell(clip(what, 160)), disp,
				cell(r.Reason), strings.Join(r.Links, ", "), spec, r.Agent)
		}
		if err := os.WriteFile(filepath.Join(c.trace, file), []byte(mb.String()), 0o644); err != nil {
			return err
		}
	}
	fmt.Fprintf(&sb, "| **Total** | **%d** | **%d** | | |\n\n", total, done)

	classes := map[string]int{}
	for _, r := range led.Units {
		if r.Disposition == "carried" {
			classes[r.Class]++
		}
	}
	sb.WriteString("## Carried tech debt by class\n\n")
	sb.WriteString("Only `unresolved` is remaining debt. A `platform-limit` is a fact no design removes; a\n")
	sb.WriteString("`scope-boundary` is a deliberate boundary stated by a decision record.\n\n| Class | Units |\n|---|---|\n")
	for _, k := range sortedKeys(classes) {
		name := k
		if name == "" {
			name = "(no class)"
		}
		fmt.Fprintf(&sb, "| %s | %d |\n", name, classes[k])
	}
	sb.WriteString("\n")

	kinds := map[string]int{}
	for _, g := range led.Go {
		kinds[g.Kind]++
	}
	sb.WriteString("## Go-side items\n\n| Kind | Count |\n|---|---|\n")
	for _, k := range sortedKeys(kinds) {
		fmt.Fprintf(&sb, "| %s | %d |\n", k, kinds[k])
	}
	sb.WriteString("\n## How to read a row\n\n")
	sb.WriteString("- **ported**: the behaviour exists in the Go design, in Go form.\n")
	sb.WriteString("- **replaced**: a different mechanism gives the same result; the link names the decision.\n")
	sb.WriteString("- **merged**: folded into another Go component; the link names it.\n")
	sb.WriteString("- **dropped**: deliberately not built; the linked decision record says why.\n")
	sb.WriteString("- **not-applicable**: exists only because of JavaScript, Node, npm or the upstream build; the reason says which.\n")
	sb.WriteString("- **resolved** (tech debt): the Go design removes the cause; the link shows where.\n")
	sb.WriteString("- **carried** (tech debt): still present, with a written justification and an owning stage.\n")
	return os.WriteFile(filepath.Join(c.docs, "traceability.md"), []byte(sb.String()), 0o644)
}
