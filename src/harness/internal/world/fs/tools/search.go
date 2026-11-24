package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
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
	return htools.Define(htools.Spec[GlobIn, htools.Content]{
		Name: GlobName,
		Description: "Find files by path pattern (\"**/*.go\", \"src/*.ts\"), newest first. Hidden and ignored files are included; " +
			"version-control directories are not. At most " + strconv.Itoa(f.cfg.GlobMaxResults) + " paths are returned; use offset to page.",
		Run:     f.runGlob,
		Overlap: func(in GlobIn) htools.Overlap { return htools.Reads(pathKey(in.Path)) },
		Effects: []htools.Effect{htools.EffectNone},
	})
}

func (f *files) runGlob(ctx context.Context, in GlobIn, run htools.Run) (htools.Content, error) {
	src, root, err := f.searchRoot(ctx, call(run), in.Path)
	if err != nil {
		return htools.Content{}, err
	}
	// Every path from the offset is kept (search bounds the files it visits),
	// so an over-cap page can save the complete list.
	var all []string
	sum, err := search.Files(ctx, search.FilesQuery{
		From: src, Root: root, Pattern: in.Pattern, Hidden: true, NoIgnore: true,
		Order: search.ByTime, Offset: in.Offset,
	}, func(p string) bool {
		all = append(all, filepath.Join(src.root, filepath.FromSlash(p)))
		return true
	})
	if err != nil {
		return htools.Content{}, err
	}
	shown := all[:min(len(all), f.cfg.GlobMaxResults)]
	omitted := len(all) - len(shown)
	var b strings.Builder
	if len(shown) == 0 {
		b.WriteString("No files found")
	} else {
		b.WriteString(strings.Join(shown, "\n"))
	}
	var sp *event.Spill
	if omitted > 0 {
		fmt.Fprintf(&b, "\n\n(Showing %d of %d paths; %d omitted. Use offset=%d to see more.)",
			len(shown), len(all), omitted, in.Offset+len(shown))
		var note string
		sp, note = f.spillWhole(ctx, run, "glob-paths.txt", fmt.Sprintf("The complete list of %d paths", len(all)), []byte(strings.Join(all, "\n")+"\n"))
		b.WriteString(note)
	}
	b.WriteString(incomplete(sum))
	return textContent(b.String(), sp), nil
}

// incomplete is the footer of a search a limit other than the page size cut.
func incomplete(sum search.Summary) string {
	if !sum.Incomplete || sum.Reason == search.CutMatches || sum.Reason == search.CutStopped {
		return ""
	}
	return "\n(The search is incomplete: " + sum.Reason + ". Narrow the path or the pattern.)"
}

func (f *files) grep() *htools.Declaration {
	return htools.Define(htools.Spec[GrepIn, htools.Content]{
		Name: GrepName,
		Description: "Search file contents with a regular expression (Go RE2 syntax; start with (?i) to ignore case). " +
			"Hidden, ignored and binary files are skipped. output is lines (matching lines, grouped by file), files (matching files) or count (matches per file). " +
			"At most " + strconv.Itoa(f.cfg.GrepMaxMatches) + " results and " + strconv.Itoa(f.cfg.GrepMaxLineBytes) + " bytes a line are returned; use offset to page.",
		Run:     f.runGrep,
		Overlap: func(in GrepIn) htools.Overlap { return htools.Reads(pathKey(in.Path)) },
		Effects: []htools.Effect{htools.EffectNone},
	})
}

func (f *files) runGrep(ctx context.Context, in GrepIn, run htools.Run) (htools.Content, error) {
	src, root, err := f.searchRoot(ctx, call(run), in.Path)
	if err != nil {
		return htools.Content{}, err
	}
	out := search.OutputLines
	switch in.Output {
	case "files":
		out = search.OutputFiles
	case "count":
		out = search.OutputCounts
	}
	// With a spill store the search runs on to SpillMaxMatches, so a page that
	// does not hold every result can save the complete list.
	page, limit := f.cfg.GrepMaxMatches, f.cfg.GrepMaxMatches
	if f.canSpill(run) {
		limit = f.cfg.SpillMaxMatches
	}
	var matches []search.Match
	sum, err := search.Grep(ctx, search.GrepQuery{
		From: src, Root: root, Pattern: in.Pattern, Include: in.Include, Output: out, Offset: in.Offset,
		Limits: search.Limits{MaxMatches: limit, MaxLineBytes: f.cfg.GrepMaxLineBytes},
	}, func(m search.Match) bool {
		matches = append(matches, m)
		return true
	})
	if err != nil {
		return htools.Content{}, err
	}
	if len(matches) == 0 {
		return textContent("No matches found"+incomplete(sum), nil), nil
	}
	abs := func(p string) string { return filepath.Join(src.root, filepath.FromSlash(p)) }
	shown := matches[:min(len(matches), page)]
	var b strings.Builder
	b.WriteString(renderMatches(out, shown, abs))
	capped := sum.Incomplete && sum.Reason == search.CutMatches
	if capped || len(matches) > page {
		fmt.Fprintf(&b, "\n\n(Results capped at %d. Use offset=%d to see more.)", page, in.Offset+len(shown))
	}
	var sp *event.Spill
	if len(matches) > page {
		what := fmt.Sprintf("The complete list of %d results", len(matches))
		if capped {
			what = fmt.Sprintf("The list of the first %d results (the most saved)", len(matches))
		}
		var note string
		sp, note = f.spillWhole(ctx, run, "grep-results.txt", what, []byte(renderMatches(out, matches, abs)+"\n"))
		b.WriteString(note)
	}
	b.WriteString(incomplete(sum))
	return textContent(b.String(), sp), nil
}

// renderMatches is grep's listing of matches in output mode out, with no
// trailing newline.
func renderMatches(out search.Output, matches []search.Match, abs func(string) string) string {
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
	return strings.TrimSuffix(b.String(), "\n")
}

// canSpill reports whether a complete result of run can be saved: a spill
// store is mounted and the call has a session to own the file.
func (f *files) canSpill(run htools.Run) bool { return f.spill != nil && run.Agent.Session != "" }
