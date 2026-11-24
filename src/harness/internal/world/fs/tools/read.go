package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/llm/vocab"
	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// readGuidance is the model-facing sentence the read guidance carries word for
// word (UP-DEC-implemented.bug-fix.2026-10-26-read-guidance-line-ranges).
const readGuidance = "Use the read tool — not shell commands like cat, sed, head, or tail — to inspect text files. " +
	"Use offset and limit to read a line range or continue a large file."

// ReadIn is read's input.
type ReadIn struct {
	// FilePath is the file or directory to read; a relative path is from the
	// session's working directory.
	FilePath string `json:"file_path" dsh:"required"`
	// Offset is the 1-based line to start at; 0 is the first line.
	Offset int `json:"offset,omitempty" dsh:"min=0"`
	// Limit is the most lines to return; 0 is the tool's most.
	Limit int `json:"limit,omitempty" dsh:"min=0"`
}

func (f *files) read() *htools.Declaration {
	return htools.Define(htools.Spec[ReadIn, htools.Content]{
		Name: ReadName,
		Description: "Read a text file with numbered lines, or list one level of a directory. " + readGuidance + " " +
			"At most " + strconv.Itoa(f.cfg.ReadLimit) + " lines, " + strconv.Itoa(f.cfg.ReadMaxLineLength) +
			" characters a line and " + strconv.Itoa(f.cfg.ReadMaxBytes) + " bytes are returned at once; the footer says how to continue.",
		Run:             f.runRead,
		Overlap:         func(in ReadIn) htools.Overlap { return htools.Reads(pathKey(in.FilePath)) },
		BoundsOwnOutput: true,
		Effects:         []htools.Effect{htools.EffectNone},
	})
}

func (f *files) runRead(ctx context.Context, in ReadIn, run htools.Run) (htools.Content, error) {
	text, sp, err := f.readText(ctx, in, run)
	return textContent(text, sp), err
}

func (f *files) readText(ctx context.Context, in ReadIn, run htools.Run) (string, *event.Spill, error) {
	c := call(run)
	ref := fs.Ref{Path: in.FilePath}
	st, err := f.ops.Stat(ctx, c, ref)
	if err != nil {
		return "", nil, err
	}
	if !st.Exists {
		return "", nil, herr.Newf(fs.CodeNotFound, "%s does not exist", st.Target.Display)
	}
	if st.Type == fs.TypeDir {
		text, err := f.listDir(ctx, run, ref, st.Target.Display)
		return text, nil, err
	}
	raw, info, err := readAll(ctx, f.ops, c, ref, f.cfg.TextMaxBytes)
	if err != nil {
		return "", nil, err
	}
	text, _, err := checkText(info.Target.Display, raw)
	if err != nil {
		return "", nil, err
	}
	lines := strings.Split(text, "\n")
	if lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	total := len(lines)
	start := max(in.Offset, 1)
	limit := f.cfg.ReadLimit
	if in.Limit > 0 {
		limit = min(in.Limit, limit)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<path>%s</path>\n<type>file</type>\n<content>\n", info.Target.Display)
	if total > 0 && start > total {
		fmt.Fprintf(&b, "(Offset %d is beyond the end of the file - total %d lines)\n</content>", start, total)
		return b.String(), nil, nil
	}
	// Two bounds cut what is shown, independently: the line and byte caps omit
	// lines, and the character cap clips a long line in place.
	shown, used, byteCut, clipped, firstCut := 0, 0, false, false, false
	for i := start - 1; i < total && shown < limit; i++ {
		line, cut := clipLine(strings.TrimSuffix(lines[i], "\r"), f.cfg.ReadMaxLineLength)
		entry := strconv.Itoa(i+1) + ": " + line + "\n"
		if used+len(entry) > f.cfg.ReadMaxBytes {
			byteCut = true
			if shown > 0 {
				break
			}
			// At least one line is always shown, cut to the byte cap, so paging
			// with offset always advances.
			body, _ := clipBytes(strings.TrimSuffix(entry, "\n"), max(f.cfg.ReadMaxBytes-1, 0))
			b.WriteString(body + "\n")
			shown, firstCut = 1, true
			clipped = clipped || cut
			break
		}
		b.WriteString(entry)
		used += len(entry)
		shown++
		clipped = clipped || cut
	}
	end := start + shown - 1
	b.WriteString("\n")
	switch {
	case end >= total:
		fmt.Fprintf(&b, "(End of file - total %d lines)", total)
	default:
		if byteCut {
			fmt.Fprintf(&b, "(Output capped at %d bytes. ", f.cfg.ReadMaxBytes)
		} else {
			b.WriteString("(")
		}
		fmt.Fprintf(&b, "Showing lines %d-%d of %d. Use offset=%d to continue.)", start, end, total, end+1)
	}
	if clipped {
		fmt.Fprintf(&b, "\n(Lines longer than %d characters were truncated.)", f.cfg.ReadMaxLineLength)
	}
	if firstCut {
		fmt.Fprintf(&b, "\n(Line %d was cut to fit the %d-byte cap.)", start, f.cfg.ReadMaxBytes)
	}
	// The whole file is saved when the tool's own bounds cut a read of it from
	// the start, by omitted lines or clipped ones; a caller paging with offset
	// and limit has already chosen its range.
	var sp *event.Spill
	if (end < total || clipped || firstCut) && in.Offset == 0 && in.Limit == 0 {
		var note string
		sp, note = f.spillWhole(ctx, run, "read-"+filepath.Base(info.Target.Display), "The whole file", raw)
		b.WriteString(note)
	}
	b.WriteString("\n</content>")
	return b.String(), sp, nil
}

// textContent is text as a body's content, citing sp, the saved complete
// result, when there is one.
func textContent(text string, sp *event.Spill) htools.Content {
	return htools.Content{Parts: []vocab.Part{vocab.TextPart(text)}, Spill: sp}
}

// spillWhole saves raw, a complete result named name, through the spill store
// for the calling session; it returns the reference and the footer line that
// cites it. With no store, or no session, there is nothing to cite.
func (f *files) spillWhole(ctx context.Context, run htools.Run, name, what string, raw []byte) (*event.Spill, string) {
	if !f.canSpill(run) {
		return nil, ""
	}
	_, ref, err := spill.Save(ctx, f.spill, string(run.Agent.Session), name, raw, 0)
	if err != nil || ref == nil {
		return nil, fmt.Sprintf("\n(%s could not be saved: %v)", what, err)
	}
	return ref, fmt.Sprintf("\n(%s, %d bytes, was saved to spill %s.)", what, ref.Bytes, ref.Path)
}

// clipLine keeps at most n characters of line and reports whether it cut.
func clipLine(line string, n int) (string, bool) {
	if utf8.RuneCountInString(line) <= n {
		return line, false
	}
	i, count := 0, 0
	for i < len(line) && count < n {
		_, size := utf8.DecodeRuneInString(line[i:])
		i += size
		count++
	}
	return line[:i] + fmt.Sprintf("... (line truncated to %d characters)", n), true
}

// listDir is read on a directory: one level, sorted, directories marked with
// a trailing slash, at most ReadLimit entries.
func (f *files) listDir(ctx context.Context, run htools.Run, ref fs.Ref, display string) (string, error) {
	ents, cut, err := f.ops.ReadDir(ctx, call(run), ref, f.cfg.ReadLimit)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		n := e.Name
		if e.Type == fs.TypeDir {
			n += "/"
		}
		names = append(names, n)
	}
	slices.Sort(names)
	var b strings.Builder
	fmt.Fprintf(&b, "<path>%s</path>\n<type>directory</type>\n<entries>\n", display)
	for _, n := range names {
		b.WriteString(n + "\n")
	}
	b.WriteString("\n")
	if cut {
		fmt.Fprintf(&b, "(Showing %d entries; the directory has more. Use glob to find a file in it.)", len(names))
	} else {
		fmt.Fprintf(&b, "(%d entries)", len(names))
	}
	b.WriteString("\n</entries>")
	return b.String(), nil
}
