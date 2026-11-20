package search

import (
	"io"
	"io/fs"
	"os"
)

// Source is the caller's view of the workspace: list a directory, open a file.
// Names are slash-separated and relative to the view's root; "." is the root.
// It is not io/fs.FS because fs.ValidPath rejects names that are not valid UTF-8,
// so such a file could be listed but never opened.
type Source interface {
	ReadDir(name string) ([]fs.DirEntry, error)
	Open(name string) (io.ReadCloser, error)
}

// DirSource is a Source over a directory of the local filesystem, confined to it by
// os.Root: no name, ".." or symbolic link reaches outside.
type DirSource struct{ root *os.Root }

// OpenDir opens dir as a Source. Close it when done.
func OpenDir(dir string) (*DirSource, error) {
	r, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	return &DirSource{root: r}, nil
}

// ReadDir lists name's entries.
func (d *DirSource) ReadDir(name string) ([]fs.DirEntry, error) {
	f, err := d.root.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// Open opens name for reading.
func (d *DirSource) Open(name string) (io.ReadCloser, error) { return d.root.Open(name) }

// Close releases the directory.
func (d *DirSource) Close() error { return d.root.Close() }
