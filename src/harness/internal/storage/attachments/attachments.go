// Package attachments keeps the files a person attaches, durably and read back
// verified (DH-D-STORAGE-3, storage.md §6.1), on top of the blob store: a file is
// one object of kind file plus a read-only named link to it,
//
//	<home>/blobs/v1/names/<2 hex>/<sha256>/<name>
//
// so a file tool sees it under its own name. The size limit is enforced while
// streaming. A read streams through blob's verifier, so a changed, truncated or
// missing object fails with attachment/corrupt or attachment/not-found and is
// never served.
//
// Not yet here: image admission, request images and Crop (DH-D-STORAGE-4), the
// draft record and quarantine (DH-D-STORAGE-10).
package attachments

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/blob"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// NamesDir is the directory below <home>/blobs/v1 that holds the named links.
const NamesDir = "names"

// MaxNameLen bounds a stored file's display name.
const MaxNameLen = 120

// DefaultName replaces a suggested name with nothing usable left in it.
const DefaultName = "file"

// Config is the part of attachments.Config (storage.md §6.2) this package uses.
type Config struct {
	// MaxFileBytes bounds one file.
	MaxFileBytes int64
}

// DefaultConfig is the documented default: 512 MiB per file.
var DefaultConfig = Config{MaxFileBytes: 512 << 20}

// FileRef is a stored file: the object and its display name.
type FileRef struct {
	blob.Ref
	Name string `json:"name"`
}

// Store is the attachment store of one home.
type Store struct {
	blobs *blob.Store
	cfg   Config
}

// New is the attachment store over blobs. A zero MaxFileBytes is the default.
func New(blobs *blob.Store, cfg Config) (*Store, error) {
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = DefaultConfig.MaxFileBytes
	}
	if err := home.EnsurePrivateDir(filepath.Join(blobs.Dir(), NamesDir)); err != nil {
		return nil, fmt.Errorf("attachments: %w", err)
	}
	return &Store{blobs: blobs, cfg: cfg}, nil
}

// SaveFile stores the bytes of r verbatim as a file named name (sanitised to one
// safe segment). Equal bytes are one object; each distinct name gets its link.
func (s *Store) SaveFile(ctx context.Context, name string, r io.Reader) (FileRef, error) {
	ref, err := s.blobs.Put(ctx, blob.KindFile, r, s.cfg.MaxFileBytes)
	if err != nil {
		return FileRef{}, err
	}
	fr := FileRef{Ref: ref, Name: home.SafeSegment(name, MaxNameLen, DefaultName)}
	if err := s.link(fr); err != nil {
		return FileRef{}, err
	}
	return fr, nil
}

// link makes the read-only named link of a stored file and syncs its directory;
// an existing link to the same object is success once that directory is synced
// (a prior link may have failed before its sync).
func (s *Store) link(fr FileRef) error {
	object, _ := s.blobs.Path(fr.Ref)
	dir := s.nameDir(fr.Ref)
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := home.EnsurePrivateDir(d); err != nil {
			return fmt.Errorf("attachments: %w", err)
		}
	}
	path := filepath.Join(dir, fr.Name)
	err := os.Link(object, path)
	if errors.Is(err, fs.ErrExist) {
		a, aerr := os.Lstat(object)
		b, berr := os.Lstat(path)
		if aerr != nil || berr != nil || !os.SameFile(a, b) {
			return herr.Newf(blob.ReasonCorrupt, "attachments: %s is not a link to %s", fr.Name, fr.Ref)
		}
	} else if err != nil {
		return herr.Wrap(home.ReasonIO, err, "attachments: link "+fr.Name)
	}
	if err := syncDir(dir); err != nil {
		return herr.Wrap(home.ReasonIO, err, "attachments: sync the link "+fr.Name)
	}
	return nil
}

// syncDir makes a directory entry durable; tests replace it.
var syncDir = fsx.SyncDir

func (s *Store) nameDir(ref blob.Ref) string {
	return filepath.Join(s.blobs.Dir(), NamesDir, ref.Digest[:2], ref.Digest)
}

// Open reads a stored file, verified against its reference.
func (s *Store) Open(ctx context.Context, ref blob.Ref) (io.ReadCloser, error) {
	return s.blobs.Open(ctx, ref)
}

// HostPath is the path a file tool reads a stored file by: its named link when
// present, else the object. It is given only to the filesystem seam.
func (s *Store) HostPath(fr FileRef) (string, bool) {
	if !fr.Valid() {
		return "", false
	}
	if fr.Kind == blob.KindFile && fr.Name != "" && home.SafeSegment(fr.Name, MaxNameLen, DefaultName) == fr.Name {
		path := filepath.Join(s.nameDir(fr.Ref), fr.Name)
		if _, err := os.Lstat(path); err == nil {
			return path, true
		}
	}
	return s.blobs.Path(fr.Ref)
}

// LookupPath tells a file tool that hostPath is already a stored object (an
// object's own path or a named link), so it is not admitted twice.
func (s *Store) LookupPath(hostPath string) (blob.Ref, bool) {
	if ref, ok := s.blobs.Lookup(hostPath); ok {
		return ref, true
	}
	rel, err := filepath.Rel(filepath.Join(s.blobs.Dir(), NamesDir), filepath.Clean(hostPath))
	if err != nil {
		return blob.Ref{}, false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 || len(parts[1]) < 2 || parts[0] != parts[1][:2] {
		return blob.Ref{}, false
	}
	object, ok := s.blobs.Path(blob.Ref{Kind: blob.KindFile, Digest: parts[1]})
	if !ok {
		return blob.Ref{}, false
	}
	ref, ok := s.blobs.Lookup(object)
	if !ok {
		return blob.Ref{}, false
	}
	a, aerr := os.Lstat(object)
	b, berr := os.Lstat(hostPath)
	if aerr != nil || berr != nil || !os.SameFile(a, b) {
		return blob.Ref{}, false
	}
	return ref, true
}
