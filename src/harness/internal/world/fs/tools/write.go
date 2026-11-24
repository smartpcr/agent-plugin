package tools

import (
	"context"
	"fmt"
	"strings"

	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// WriteIn is write's input.
type WriteIn struct {
	// FilePath is the file to create or replace.
	FilePath string `json:"file_path" dsh:"required"`
	// Content is the file's whole new content.
	Content string `json:"content"`
}

// EditIn is edit's input.
type EditIn struct {
	// FilePath is the file to edit.
	FilePath string `json:"file_path" dsh:"required"`
	// OldString is the literal text to replace.
	OldString string `json:"old_string" dsh:"required"`
	// NewString is the text to put in its place.
	NewString string `json:"new_string"`
	// ReplaceAll replaces every occurrence instead of exactly one.
	ReplaceAll bool `json:"replace_all,omitempty"`
}

func (f *files) write() *htools.Declaration {
	return htools.Define(htools.Spec[WriteIn, string]{
		Name: WriteName,
		Description: "Create a file, or replace a file's whole content. Parent directories are created. " +
			"Prefer edit or apply_patch to change part of an existing file.",
		Run:     f.runWrite,
		Overlap: func(in WriteIn) htools.Overlap { return htools.Writes(pathKey(in.FilePath)) },
		Effects: []htools.Effect{htools.EffectFiles},
	})
}

func (f *files) runWrite(ctx context.Context, in WriteIn, run htools.Run) (string, error) {
	if len(in.Content) > f.cfg.TextMaxBytes {
		return "", herr.Newf(fs.CodeTooLarge, "the content is %d bytes, more than the %d a text tool writes", len(in.Content), f.cfg.TextMaxBytes)
	}
	out, err := f.ops.Write(ctx, call(run), fs.Ref{Path: in.FilePath}, []byte(in.Content), fs.NoGuard)
	if err != nil {
		return "", err
	}
	verb := "Updated"
	if out.Created {
		verb = "Created"
	}
	return fmt.Sprintf("%s file %s (version %s)%s", verb, out.Target.Display, version(out.Version), durability(out)), nil
}

func (f *files) edit() *htools.Declaration {
	return htools.Define(htools.Spec[EditIn, string]{
		Name: EditName,
		Description: "Replace literal text in a file. old_string must occur exactly once unless replace_all is true; " +
			"include enough surrounding lines to make it unique. Line endings are matched and kept as the file has them.",
		Run:     f.runEdit,
		Overlap: func(in EditIn) htools.Overlap { return htools.Writes(pathKey(in.FilePath)) },
		Effects: []htools.Effect{htools.EffectFiles},
	})
}

func (f *files) runEdit(ctx context.Context, in EditIn, run htools.Run) (string, error) {
	if in.OldString == "" {
		return "", herr.New(fs.CodeEditNotFound, "old_string is empty; use write to create a file")
	}
	if in.OldString == in.NewString {
		return "", herr.New(fs.CodeEditNotFound, "old_string and new_string are the same; nothing to change")
	}
	c := call(run)
	ref := fs.Ref{Path: in.FilePath}
	raw, info, err := readAll(ctx, f.ops, c, ref, f.cfg.TextMaxBytes)
	if err != nil {
		return "", err
	}
	text, bom, err := checkText(info.Target.Display, raw)
	if err != nil {
		return "", err
	}
	// A literal match on the bytes as they are wins; otherwise old_string is
	// matched with line endings normalised and the file's dominant ending is
	// restored, so a model that writes LF edits a CRLF file.
	base, old, repl, eol := text, in.OldString, in.NewString, "\n"
	if !strings.Contains(text, old) {
		eol = fsx.DominantEOL(text)
		base, old, repl = fsx.NormalizeEOL(text), fsx.NormalizeEOL(old), fsx.NormalizeEOL(repl)
	}
	n := strings.Count(base, old)
	switch {
	case n == 0:
		return "", herr.Newf(fs.CodeEditNotFound, "old_string was not found in %s", info.Target.Display)
	case n > 1 && !in.ReplaceAll:
		return "", herr.Newf(fs.CodeAmbiguousEdit, "old_string occurs %d times in %s; add context to make it unique or set replace_all", n, info.Target.Display)
	}
	if in.ReplaceAll {
		base = strings.ReplaceAll(base, old, repl)
	} else {
		base = strings.Replace(base, old, repl, 1)
	}
	if eol != "\n" {
		base = fsx.RestoreEOL(base, eol)
	}
	data := []byte(base)
	if bom {
		data = append(append([]byte(nil), utf8BOM...), data...)
	}
	if len(data) > f.cfg.TextMaxBytes {
		return "", herr.Newf(fs.CodeTooLarge, "the edited %s would be %d bytes, more than the %d a text tool writes", info.Target.Display, len(data), f.cfg.TextMaxBytes)
	}
	out, err := f.ops.Write(ctx, c, ref, data, fs.ReplaceIfVersion{Version: info.Version})
	if err != nil {
		return "", err
	}
	what := "1 occurrence"
	if n > 1 {
		what = fmt.Sprintf("%d occurrences", n)
	}
	return fmt.Sprintf("Edited file %s (version %s): replaced %s%s", out.Target.Display, version(out.Version), what, durability(out)), nil
}

// durability is the note a write whose sync failed carries.
func durability(out fs.WriteOutcome) string {
	if out.DurabilityUnknown != nil {
		return "; the file is in place but its durability is unknown"
	}
	return ""
}
