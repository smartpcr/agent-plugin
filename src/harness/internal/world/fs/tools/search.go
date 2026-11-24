package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/search"
)

// GlobIn is glob's input.
type GlobIn struct {
	// Pattern is a glob such as "**/*.go"; without a slash it matches base names.
	Pattern string `json:"pattern" dsh:"required"`
	// Path is the directory to search; "" is the working directory.
	Path string `json:"path,omitempty"`
	// Offset skips that many paths of the ordered result.
	Offset int `json:"offset,omitempty" dsh:"min=0"`
}

// GrepIn is grep's input.
type GrepIn struct {
	// Pattern is a regular expression in Go RE2 syntax; (?i) ignores case.
	Pattern string `json:"pattern" dsh:"required"`
	// Path is the directory or file to search; "" is the working directory.
	Path string `json:"path,omitempty"`
	// Include is a glob the searched files' paths must match, such as "*.go".
	Include string `json:"include,omitempty"`
	// Output is lines (the default), files or count.
	Output string `json:"output,omitempty" dsh:"enum=lines|files|count"`
	// Offset skips that many results.
	Offset int `json:"offset,omitempty" dsh:"min=0"`
}

func (f *files) glob() *htools.Declaration {
	return htools.Define(htools.Spec[GlobIn, string]{
		Name: GlobName,
		Description: "Find files by path pattern (\"**/*.go\", \"src/*.ts\"), newest first. Hidden and ignored files are included; " +
			"version-control directories are not. At most " + strconv.Itoa(f.cfg.GlobMaxResults) + " paths are returned; use offset to page.",
		Run:     f.runGlob,
		Overlap: func(in GlobIn) htools.Overlap { return htools.Reads(pathKey(in.Path)) },
		Effects: []htools.Effect{htools.EffectNone},
	})
}

func (f *files) runGlob(ctx context.Context, in GlobIn, run htools.Run) (string, error) {
	src, root, err := f.searchRoot(ctx, call(run), in.Path)
	if err != nil {
		return "", err
	}
	var shown []string
	omitted := 0
	sum, err := search.Files(ctx, search.FilesQuery{
		From: src, Root: root, Pattern: in.Pattern, Hidden: true, NoIgnore: true,
		Order: search.ByTime, Offset: in.Offset,
	}, func(p string) bool {
		if len(shown) < f.cfg.GlobMaxResults {
			shown = append(shown, filepath.Join(src.root, filepath.FromSlash(p)))
		} else {
			omitted++
		}
		return true
	})
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if len(shown) == 0 {
		b.WriteString("No files found")
	} else {
		b.WriteString(strings.Join(shown, "\n"))
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n\n(Showing %d of %d paths; %d omitted. Use offset=%d to see more.)",
			len(shown), len(shown)+omitted, omitted, in.Offset+len(shown))
	}
	b.WriteString(incomplete(sum))
	return b.String(), nil
}

// incomplete is the footer of a search a limit other than the page size cut.
func incomplete(sum search.Summary) string {
	if !sum.Incomplete || sum.Reason == search.CutMatches || sum.Reason == search.CutStopped {
		return ""
	}
	return "\n(The search is incomplete: " + sum.Reason + ". Narrow the path or the pattern.)"
}

func (f *files) grep() *htools.Declaration {
	return htools.Define(htools.Spec[GrepIn, string]{
		Name: GrepName,
		Description: "Search file contents with a regular expression (Go RE2 syntax; start with (?i) to ignore case). " +
			"Hidden, ignored and binary files are skipped. output is lines (matching lines, grouped by file), files (matching files) or count (matches per file). " +
			"At most " + strconv.Itoa(f.cfg.GrepMaxMatches) + " results and " + strconv.Itoa(f.cfg.GrepMaxLineBytes) + " bytes a line are returned; use offset to page.",
		Run:     f.runGrep,
		Overlap: func(in GrepIn) htools.Overlap { return htools.Reads(pathKey(in.Path)) },
		Effects: []htools.Effect{htools.EffectNone},
	})
}

func (f *files) runGrep(ctx context.Context, in GrepIn, run htools.Run) (string, error) {
	src, root, err := f.searchRoot(ctx, call(run), in.Path)
	if err != nil {
		return "", err
	}
	out := search.OutputLines
	switch in.Output {
	case "files":
		out = search.OutputFiles
	case "count":
		out = search.OutputCounts
	}
	var matches []search.Match
	sum, err := search.Grep(ctx, search.GrepQuery{
		From: src, Root: root, Pattern: in.Pattern, Include: in.Include, Output: out, Offset: in.Offset,
		Limits: search.Limits{MaxMatches: f.cfg.GrepMaxMatches, MaxLineBytes: f.cfg.GrepMaxLineBytes},
	}, func(m search.Match) bool {
		matches = append(matches, m)
		return true
	})
	if err != nil {
		return "", err
	}
	if len(matches) == 0 {
		return "No matches found" + incomplete(sum), nil
	}
	abs := func(p string) string { return filepath.Join(src.root, filepath.FromSlash(p)) }
	var b strings.Builder
	switch out {
	case search.OutputFiles:
		fmt.Fprintf(&b, "Found %d files\n", len(matches))
		for _, m := range matches {
			b.WriteString(abs(m.Path) + "\n")
		}
	case search.OutputCounts:
		fmt.Fprintf(&b, "Found matches in %d files\n", len(matches))
		for _, m := range matches {
			fmt.Fprintf(&b, "%s: %d\n", abs(m.Path), m.Count)
		}
	default:
		fmt.Fprintf(&b, "Found %d matches\n", len(matches))
		last := ""
		for _, m := range matches {
			if m.Path != last {
				fmt.Fprintf(&b, "\n%s:\n", abs(m.Path))
				last = m.Path
			}
			text := m.Text
			if m.Truncated {
				text += "... (line truncated)"
			}
			fmt.Fprintf(&b, "  Line %d: %s\n", m.Line, text)
		}
	}
	if sum.Incomplete && sum.Reason == search.CutMatches {
		fmt.Fprintf(&b, "\n(Results capped at %d. Use offset=%d to see more.)", f.cfg.GrepMaxMatches, in.Offset+len(matches))
	}
	b.WriteString(incomplete(sum))
	return strings.TrimSuffix(b.String(), "\n"), nil
}
