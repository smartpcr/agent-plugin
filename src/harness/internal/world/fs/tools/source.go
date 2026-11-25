package tools

import (
	"context"
	"io"
	iofs "io/fs"
	"path/filepath"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/search"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs/ops"
)

// source is util/search's view of one directory through the operation service:
// every listing and every open of a glob or grep is an ops call, so a search
// sees what the session's provider sees and nothing else.
type source struct {
	ctx  context.Context
	ops  *ops.Service
	c    ops.Call
	root string // the directory's canonical path
}

var _ search.Source = source{}

func (s source) path(name string) string {
	if name == "" || name == "." {
		return s.root
	}
	return filepath.Join(s.root, filepath.FromSlash(name))
}

// ReadDir lists name with no limit: the walker bounds the files it visits.
func (s source) ReadDir(name string) ([]iofs.DirEntry, error) {
	ents, _, err := s.ops.ReadDir(s.ctx, s.c, fs.Ref{Path: s.path(name)}, 0)
	if err != nil {
		return nil, err
	}
	out := make([]iofs.DirEntry, len(ents))
	for i, e := range ents {
		out[i] = dirEntry{e}
	}
	return out, nil
}

// Open opens name for reading.
func (s source) Open(name string) (io.ReadCloser, error) {
	rc, _, err := s.ops.Open(s.ctx, s.c, fs.Ref{Path: s.path(name)})
	return rc, err
}

// dirEntry is an fs.DirEntry as io/fs sees one.
type dirEntry struct{ e fs.DirEntry }

func (d dirEntry) Name() string                 { return d.e.Name }
func (d dirEntry) IsDir() bool                  { return d.e.Type == fs.TypeDir }
func (d dirEntry) Type() iofs.FileMode          { return mode(d.e.Type) }
func (d dirEntry) Info() (iofs.FileInfo, error) { return fileInfo(d), nil }

func mode(t fs.FileType) iofs.FileMode {
	switch t {
	case fs.TypeDir:
		return iofs.ModeDir
	case fs.TypeSymlink:
		return iofs.ModeSymlink
	case fs.TypeOther:
		return iofs.ModeIrregular
	}
	return 0
}

type fileInfo struct{ e fs.DirEntry }

func (f fileInfo) Name() string        { return f.e.Name }
func (f fileInfo) Size() int64         { return f.e.Size }
func (f fileInfo) Mode() iofs.FileMode { return mode(f.e.Type) }
func (f fileInfo) ModTime() time.Time  { return f.e.ModTime }
func (f fileInfo) IsDir() bool         { return f.e.Type == fs.TypeDir }
func (f fileInfo) Sys() any            { return nil }

// searchRoot resolves a search's path argument ("" is the working directory):
// the source over its directory and the root to search in it ("." for the
// directory itself, a file's base name for a file).
func (f *files) searchRoot(ctx context.Context, c ops.Call, path string) (source, string, error) {
	if path == "" {
		path = "."
	}
	st, err := f.ops.Stat(ctx, c, fs.Ref{Path: path})
	if err != nil {
		return source{}, "", err
	}
	if !st.Exists {
		return source{}, "", herr.Newf(fs.CodeNotFound, "%s does not exist", st.Target.Display)
	}
	if st.Type == fs.TypeDir {
		return source{ctx: ctx, ops: f.ops, c: c, root: st.Target.Display}, ".", nil
	}
	return source{ctx: ctx, ops: f.ops, c: c, root: filepath.Dir(st.Target.Display)}, filepath.Base(st.Target.Display), nil
}
