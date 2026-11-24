package tools

import (
	"context"
	"fmt"
	"strings"

	htools "github.com/smartpcr/agent-plugin/src/harness/internal/tools"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/textdiff/patch"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
)

// ApplyPatchIn is apply_patch's input.
type ApplyPatchIn struct {
	// Input is the whole patch, from "*** Begin Patch" to "*** End Patch".
	Input string `json:"input" dsh:"required"`
}

func (f *files) applyPatch() *htools.Declaration {
	return htools.Define(htools.Spec[ApplyPatchIn, string]{
		Name: ApplyPatchName,
		Description: "Add, update, delete and move several files in one call. The input is a patch:\n" +
			"*** Begin Patch\n*** Add File: <path>\n+<line>\n*** Delete File: <path>\n*** Update File: <path>\n" +
			"*** Move to: <new path>   (optional)\n@@ <a line before the change>\n <context line>\n-<removed line>\n+<added line>\n" +
			"*** End Patch\n" +
			"Every hunk is checked before any file changes; the files are then changed in order, and a failure stops the rest without undoing what was done.",
		Run:     f.runApplyPatch,
		Overlap: patchOverlap,
		Effects: []htools.Effect{htools.EffectFiles},
	})
}

// patchOverlap declares a write on every path the patch names; a patch that
// does not parse runs alone and fails in its body.
func patchOverlap(in ApplyPatchIn) htools.Overlap {
	p, err := patch.Parse(in.Input)
	if err != nil || len(p.Hunks) == 0 {
		return htools.Exclusive
	}
	var keys []string
	for _, h := range p.Hunks {
		keys = append(keys, pathKey(h.Path))
		if h.MoveTo != "" {
			keys = append(keys, pathKey(h.MoveTo))
		}
	}
	return htools.Writes(keys...)
}

// simFile is one path's state while a patch is preflighted.
type simFile struct {
	loaded  bool // text and bom were read
	exists  bool
	text    string
	bom     bool
	version fs.Version
}

// step is one planned file change: the hunk and the bytes it publishes.
type step struct {
	h    patch.Hunk
	data []byte
}

// patchRun is one apply_patch call: the simulated state of every path it
// names, keyed as the overlap keys them.
type patchRun struct {
	f   *files
	ctx context.Context
	c   ops.Call
	sim map[string]*simFile
}

func (r *patchRun) state(path string, needText bool) (*simFile, error) {
	k := pathKey(path)
	s, ok := r.sim[k]
	if !ok {
		in, err := r.f.ops.Stat(r.ctx, r.c, fs.Ref{Path: path})
		if err != nil {
			return nil, err
		}
		if in.Exists && in.Type != fs.TypeFile {
			return nil, herr.Newf(fs.CodeNotRegularFile, "%s is not a regular file", in.Target.Display)
		}
		s = &simFile{exists: in.Exists, version: in.Version}
		r.sim[k] = s
	}
	if needText && s.exists && !s.loaded {
		raw, info, err := readAll(r.ctx, r.f.ops, r.c, fs.Ref{Path: path}, r.f.cfg.TextMaxBytes)
		if err != nil {
			return nil, err
		}
		text, bom, err := checkText(info.Target.Display, raw)
		if err != nil {
			return nil, err
		}
		s.loaded, s.text, s.bom, s.version = true, text, bom, info.Version
	}
	return s, nil
}

func (r *patchRun) encode(s *simFile, text string) []byte {
	if s.bom {
		return append(append([]byte(nil), utf8BOM...), text...)
	}
	return []byte(text)
}

// preflight applies every hunk to the simulated state; nothing is written.
func (r *patchRun) preflight(p *patch.Patch) ([]step, error) {
	var plan []step
	for _, h := range p.Hunks {
		switch h.Op {
		case patch.OpAdd:
			s, err := r.state(h.Path, false)
			if err != nil {
				return nil, err
			}
			if s.exists {
				return nil, herr.Newf(fs.CodeAlreadyExists, "Add File %s: the file already exists", h.Path)
			}
			if len(h.Contents) > r.f.cfg.TextMaxBytes {
				return nil, herr.Newf(fs.CodeTooLarge, "Add File %s: the content is %d bytes, more than the %d a text tool writes", h.Path, len(h.Contents), r.f.cfg.TextMaxBytes)
			}
			s.exists, s.loaded, s.text, s.bom = true, true, h.Contents, false
			plan = append(plan, step{h: h, data: []byte(h.Contents)})
		case patch.OpDelete:
			s, err := r.state(h.Path, false)
			if err != nil {
				return nil, err
			}
			if !s.exists {
				return nil, herr.Newf(fs.CodeNotFound, "Delete File %s: the file does not exist", h.Path)
			}
			s.exists, s.loaded, s.text = false, false, ""
			plan = append(plan, step{h: h})
		case patch.OpUpdate:
			s, err := r.state(h.Path, true)
			if err != nil {
				return nil, err
			}
			if !s.exists {
				return nil, herr.Newf(fs.CodeNotFound, "Update File %s: the file does not exist", h.Path)
			}
			next, err := h.Apply(s.text)
			if err != nil {
				return nil, err
			}
			data := r.encode(s, next)
			if len(data) > r.f.cfg.TextMaxBytes {
				return nil, herr.Newf(fs.CodeTooLarge, "Update File %s: the result is %d bytes, more than the %d a text tool writes", h.Path, len(data), r.f.cfg.TextMaxBytes)
			}
			if h.MoveTo != "" && pathKey(h.MoveTo) != pathKey(h.Path) {
				d, err := r.state(h.MoveTo, false)
				if err != nil {
					return nil, err
				}
				d.exists, d.loaded, d.text, d.bom = true, true, next, s.bom
				s.exists, s.loaded, s.text = false, false, ""
			} else {
				s.text = next
			}
			plan = append(plan, step{h: h, data: data})
		}
	}
	return plan, nil
}

// guard is the version check of a change to path: the version last read or
// written, or none when the path was never seen with one.
func (r *patchRun) guard(path string) fs.Guard {
	if s, ok := r.sim[pathKey(path)]; ok && !s.version.IsZero() {
		return fs.ReplaceIfVersion{Version: s.version}
	}
	return fs.NoGuard
}

func (r *patchRun) wrote(path string, out fs.WriteOutcome) {
	r.sim[pathKey(path)] = &simFile{exists: true, version: out.Version}
}

// commit publishes one step through the operation service and returns its
// report line.
func (r *patchRun) commit(st step) (string, error) {
	h := st.h
	switch h.Op {
	case patch.OpAdd:
		out, err := r.f.ops.Write(r.ctx, r.c, fs.Ref{Path: h.Path}, st.data, fs.CreateIfAbsent)
		if err != nil {
			return "", err
		}
		r.wrote(h.Path, out)
		return fmt.Sprintf("A %s (version %s)%s", out.Target.Display, version(out.Version), durability(out)), nil
	case patch.OpDelete:
		out, err := r.f.ops.Delete(r.ctx, r.c, fs.Ref{Path: h.Path}, r.guard(h.Path))
		if err != nil {
			return "", err
		}
		delete(r.sim, pathKey(h.Path))
		return "D " + out.Target.Display, nil
	}
	if h.MoveTo == "" || pathKey(h.MoveTo) == pathKey(h.Path) {
		out, err := r.f.ops.Write(r.ctx, r.c, fs.Ref{Path: h.Path}, st.data, r.guard(h.Path))
		if err != nil {
			return "", err
		}
		r.wrote(h.Path, out)
		return fmt.Sprintf("M %s (version %s)%s", out.Target.Display, version(out.Version), durability(out)), nil
	}
	// A move is two effects (DH-ADR-FS-11 (c)): the destination is published,
	// then the source removed.
	out, err := r.f.ops.Write(r.ctx, r.c, fs.Ref{Path: h.MoveTo}, st.data, fs.NoGuard)
	if err != nil {
		return "", err
	}
	r.wrote(h.MoveTo, out)
	del, err := r.f.ops.Delete(r.ctx, r.c, fs.Ref{Path: h.Path}, r.guard(h.Path))
	if err != nil {
		return "", herr.Wrap(kindOr(err, fs.CodeIO), err, fmt.Sprintf("%s was written (version %s%s) but the source could not be removed", out.Target.Display, version(out.Version), durability(out)))
	}
	delete(r.sim, pathKey(h.Path))
	return fmt.Sprintf("R %s -> %s (version %s)%s", del.Target.Display, out.Target.Display, version(out.Version), durability(out)), nil
}

func hunkLabel(h patch.Hunk) string {
	switch h.Op {
	case patch.OpAdd:
		return "Add File " + h.Path
	case patch.OpDelete:
		return "Delete File " + h.Path
	}
	if h.MoveTo != "" {
		return "Update File " + h.Path + " (move to " + h.MoveTo + ")"
	}
	return "Update File " + h.Path
}

func (f *files) runApplyPatch(ctx context.Context, in ApplyPatchIn, run htools.Run) (string, error) {
	p, err := patch.Parse(in.Input)
	if err != nil {
		return "", err
	}
	if len(p.Hunks) == 0 {
		return "", herr.New(patch.ReasonSyntax, "the patch has no hunks")
	}
	r := &patchRun{f: f, ctx: ctx, c: call(run), sim: map[string]*simFile{}}
	plan, err := r.preflight(p)
	if err != nil {
		return "", err
	}
	// The preflight read the files; the commit starts from what it saw.
	var done []string
	for i, st := range plan {
		line, err := r.commit(st)
		if err != nil {
			var b strings.Builder
			b.WriteString("The patch stopped part-way; nothing was rolled back.\n")
			if len(done) > 0 {
				b.WriteString("Completed:\n" + strings.Join(done, "\n") + "\n")
			}
			fmt.Fprintf(&b, "Failed:\n%s: %v\n", hunkLabel(st.h), err)
			if rest := plan[i+1:]; len(rest) > 0 {
				b.WriteString("Not executed:\n")
				for _, s := range rest {
					b.WriteString(hunkLabel(s.h) + "\n")
				}
			}
			b.WriteString("Re-read the files before patching again.")
			return "", herr.New(kindOr(err, fs.CodeIO), b.String())
		}
		done = append(done, line)
	}
	return "Success. Updated the following files:\n" + strings.Join(done, "\n"), nil
}
