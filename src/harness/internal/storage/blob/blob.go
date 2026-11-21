// Package blob is the content-addressed object store under <home>/blobs
// (DH-D-STORAGE-3, DH-ADR-STORAGE-3, storage.md §6.1 "Home layout"): a blob is a
// file of bytes kept outside the database, named by the SHA-256 of its bytes.
//
//	<home>/blobs/v1/<kind>/<2 hex>/<sha256>   objects; kind is image, file or data
//	<home>/blobs/tmp/                          staging on the same volume
//
// Put streams into a private staging file while hashing, syncs it, publishes it
// by an exclusive hard link, syncs the directory and sets the object read-only;
// an existing object of the same digest is success only once its bytes verify,
// and then gets its time updated. Each new directory level's parent is synced.
// Open verifies the length and the digest while streaming, so a changed,
// truncated or missing object is detected and never served. Who may read an
// object is the calling route's decision (area A19), not this package's.
//
// Not yet here (later steps): the shared gc.lock a saver holds against the
// collector and the collector itself (DH-D-STORAGE-9), and ReferencedBy, which
// reads the session store's blob_refs rows (DH-D-STORE-2).
package blob

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Reasons of the errors a read or a save returns (storage.md §6.2 "Errors"; the
// attachment store is this package's main user, so they carry its domain).
const (
	ReasonNotFound   herr.Reason = "attachment/not-found"
	ReasonCorrupt    herr.Reason = "attachment/corrupt"
	ReasonTooLarge   herr.Reason = "attachment/too-large"
	ReasonReadFailed herr.Reason = "attachment/read-failed"
)

// Directory names below <home>/blobs.
const (
	VersionDir = "v1"
	TmpDir     = "tmp"
)

// Kind is the kind of an object.
type Kind string

// The kinds of objects.
const (
	KindImage Kind = "image"
	KindFile  Kind = "file"
	KindData  Kind = "data"
)

// Valid reports one of the declared kinds.
func (k Kind) Valid() bool { return k == KindImage || k == KindFile || k == KindData }

// Ref names a stored object and the length it must have.
type Ref struct {
	Kind Kind `json:"kind"`
	// Digest is the lower-case hex SHA-256 of the bytes.
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}

// String is the reference's form in a blob_refs row: "<kind>:<digest>".
func (r Ref) String() string { return string(r.Kind) + ":" + r.Digest }

// Valid reports a declared kind, a well-formed digest and a non-negative size.
func (r Ref) Valid() bool {
	return r.Kind.Valid() && r.Size >= 0 && validDigest(r.Digest)
}

func validDigest(d string) bool {
	if len(d) != sha256.Size*2 {
		return false
	}
	for i := 0; i < len(d); i++ {
		if c := d[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// syncDir makes a directory entry durable; tests replace it.
var syncDir = fsx.SyncDir

// chmod sets an object's mode; tests replace it.
var chmod = os.Chmod

// Store is the blob store of one home.
type Store struct {
	root string // <home>/blobs
	now  func() time.Time
}

// New opens the blob store of the home: <home>/blobs must exist (home Ensure made
// it) and be private; its v1/ and tmp/ are created private when missing.
func New(l home.Layout) (*Store, error) {
	root := l.Blobs()
	if err := home.CheckPrivate(root); err != nil {
		return nil, fmt.Errorf("blob: the blobs directory %s: %w", root, err)
	}
	for _, d := range []string{filepath.Join(root, VersionDir), filepath.Join(root, TmpDir)} {
		if err := home.EnsurePrivateDir(d); err != nil {
			return nil, fmt.Errorf("blob: %w", err)
		}
	}
	return &Store{root: root, now: time.Now}, nil
}

// Dir is <home>/blobs/v1, below which the attachment store keeps its named links.
func (s *Store) Dir() string { return filepath.Join(s.root, VersionDir) }

// Path is the object's path on this machine; it is given only to the filesystem
// seam. A malformed reference has no path.
func (s *Store) Path(ref Ref) (string, bool) {
	if !ref.Valid() {
		return "", false
	}
	return filepath.Join(s.root, VersionDir, string(ref.Kind), ref.Digest[:2], ref.Digest), true
}

// Lookup tells whether hostPath is one of the store's objects, and which. It
// compares names only and returns the object's current size.
func (s *Store) Lookup(hostPath string) (Ref, bool) {
	rel, err := filepath.Rel(filepath.Join(s.root, VersionDir), filepath.Clean(hostPath))
	if err != nil {
		return Ref{}, false
	}
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) != 3 {
		return Ref{}, false
	}
	ref := Ref{Kind: Kind(parts[0]), Digest: parts[2]}
	if !ref.Valid() || parts[1] != ref.Digest[:2] {
		return Ref{}, false
	}
	info, err := os.Lstat(hostPath)
	if err != nil || !info.Mode().IsRegular() {
		return Ref{}, false
	}
	ref.Size = info.Size()
	return ref, true
}

// Put stores the bytes of r as an object of kind, refusing more than limit bytes
// (limit <= 0 is no limit) with attachment/too-large and leaving no object. The
// reference is returned only once the object and its directory entry are synced.
func (s *Store) Put(ctx context.Context, kind Kind, r io.Reader, limit int64) (Ref, error) {
	if !kind.Valid() {
		return Ref{}, fmt.Errorf("blob: unknown kind %q", kind)
	}
	stage, err := s.stage()
	if err != nil {
		return Ref{}, err
	}
	staged := stage.Name()
	defer func() {
		_ = stage.Close()
		_ = os.Remove(staged)
	}()
	h := sha256.New()
	src := r
	if limit > 0 {
		src = io.LimitReader(r, limit+1)
	}
	n, err := io.Copy(io.MultiWriter(stage, h), ctxReader{ctx, src})
	if err != nil {
		return Ref{}, herr.Wrap(ReasonReadFailed, err, "blob: read the bytes to store")
	}
	if limit > 0 && n > limit {
		return Ref{}, herr.Newf(ReasonTooLarge, "blob: more than %d bytes", limit)
	}
	if err := stage.Sync(); err != nil {
		return Ref{}, herr.Wrap(home.ReasonIO, err, "blob: sync the staged object")
	}
	if err := stage.Close(); err != nil {
		return Ref{}, herr.Wrap(home.ReasonIO, err, "blob: close the staged object")
	}
	ref := Ref{Kind: kind, Digest: hex.EncodeToString(h.Sum(nil)), Size: n}
	if err := s.publish(ctx, staged, ref); err != nil {
		return Ref{}, err
	}
	return ref, nil
}

// stage creates a private staging file in tmp/.
func (s *Store) stage() (*os.File, error) {
	for range 16 {
		var b [8]byte
		_, _ = rand.Read(b[:])
		f, err := home.CreatePrivateFile(filepath.Join(s.root, TmpDir, fsx.StagePrefix+hex.EncodeToString(b[:])))
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, herr.Wrap(home.ReasonIO, err, "blob: stage")
		}
		return f, nil
	}
	return nil, herr.New(home.ReasonIO, "blob: no free staging name")
}

// publish links the staged file to the object's name, unless an object of that
// digest is there already, which is then verified, gets its time updated and has
// its directory entry synced (a prior publish may have failed before its sync).
// Every new directory level syncs its parent as EnsurePrivateDir creates it.
func (s *Store) publish(ctx context.Context, staged string, ref Ref) error {
	path, _ := s.Path(ref)
	kindDir := filepath.Dir(filepath.Dir(path))
	for _, d := range []string{kindDir, filepath.Dir(path)} {
		if err := home.EnsurePrivateDir(d); err != nil {
			return fmt.Errorf("blob: %w", err)
		}
	}
	err := os.Link(staged, path)
	if errors.Is(err, fs.ErrExist) {
		if err := s.adopt(ctx, path, ref); err != nil {
			return err
		}
		if err := syncDir(filepath.Dir(path)); err != nil {
			return herr.Wrap(home.ReasonIO, err, "blob: sync the directory of "+ref.String())
		}
		return nil
	}
	if err != nil {
		return herr.Wrap(home.ReasonIO, err, "blob: publish "+ref.String())
	}
	// The staged name goes before the object is made read-only: Windows refuses
	// to remove a read-only name, and both names are one file.
	_ = os.Remove(staged)
	if err := syncDir(filepath.Dir(path)); err != nil {
		return herr.Wrap(home.ReasonIO, err, "blob: sync the directory of "+ref.String())
	}
	if err := chmod(path, 0o400); err != nil {
		return herr.Wrap(home.ReasonIO, err, "blob: make "+ref.String()+" read-only")
	}
	return nil
}

// adopt accepts an existing object of the same name only if it is a regular file
// whose length and digest are ref's, and updates its time; one that is not is
// attachment/corrupt and is never replaced automatically.
func (s *Store) adopt(ctx context.Context, path string, ref Ref) error {
	info, err := os.Lstat(path)
	if err != nil {
		return herr.Wrap(home.ReasonIO, err, "blob: "+ref.String())
	}
	if !info.Mode().IsRegular() || info.Size() != ref.Size {
		return herr.Newf(ReasonCorrupt, "blob: the stored object %s is %d bytes, want %d", ref, info.Size(), ref.Size)
	}
	if err := s.verify(ctx, ref); err != nil {
		return err
	}
	// A publish that failed after its link (on the directory sync) left the
	// object writable; an adopted object is made read-only like a new one.
	if info.Mode().Perm()&0o222 != 0 {
		if err := chmod(path, 0o400); err != nil {
			return herr.Wrap(home.ReasonIO, err, "blob: make "+ref.String()+" read-only")
		}
	}
	now := s.now()
	if err := os.Chtimes(path, now, now); err != nil {
		return herr.Wrap(home.ReasonIO, err, "blob: touch "+ref.String())
	}
	return nil
}

// verify reads the whole object through the verifying reader.
func (s *Store) verify(ctx context.Context, ref Ref) error {
	rc, err := s.Open(ctx, ref)
	if err != nil {
		return err
	}
	defer rc.Close()
	_, err = io.Copy(io.Discard, ctxReader{ctx, rc})
	return err
}

// Touch re-validates references before an event cites them: each object must be
// present with its length and digest, and gets its time updated, so the
// collector's grace period starts again.
func (s *Store) Touch(ctx context.Context, refs ...Ref) error {
	for _, ref := range refs {
		if err := ctx.Err(); err != nil {
			return err
		}
		path, ok := s.Path(ref)
		if !ok {
			return herr.Newf(ReasonNotFound, "blob: %q is not a reference", ref.String())
		}
		if _, err := os.Lstat(path); errors.Is(err, fs.ErrNotExist) {
			return herr.Newf(ReasonNotFound, "blob: no stored object %s", ref)
		}
		if err := s.adopt(ctx, path, ref); err != nil {
			return err
		}
	}
	return nil
}

// Open reads an object, verifying while streaming: a missing object is
// attachment/not-found; a length or digest that differs from ref fails the read
// with attachment/corrupt, at the latest on the read that reaches the end, so no
// reader takes the bytes for good without seeing the error.
func (s *Store) Open(ctx context.Context, ref Ref) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path, ok := s.Path(ref)
	if !ok {
		return nil, herr.Newf(ReasonNotFound, "blob: %q is not a reference", ref.String())
	}
	f, err := fsx.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, herr.Newf(ReasonNotFound, "blob: no stored object %s", ref)
	}
	if err != nil {
		return nil, herr.Wrap(ReasonReadFailed, err, "blob: open "+ref.String())
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, herr.Wrap(ReasonReadFailed, err, "blob: stat "+ref.String())
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, herr.Newf(ReasonNotFound, "blob: %s is not a regular file", ref)
	}
	if info.Size() != ref.Size {
		_ = f.Close()
		return nil, herr.Newf(ReasonCorrupt, "blob: the stored object %s is %d bytes, want %d", ref, info.Size(), ref.Size)
	}
	return &verifier{f: f, ref: ref, h: sha256.New()}, nil
}

// verifier streams an object and checks its length and digest.
type verifier struct {
	f   *os.File
	ref Ref
	h   hash.Hash
	n   int64
	err error
}

func (v *verifier) Read(p []byte) (int, error) {
	if v.err != nil {
		return 0, v.err
	}
	n, err := v.f.Read(p)
	v.n += int64(n)
	v.h.Write(p[:n])
	if v.n > v.ref.Size {
		v.err = herr.Newf(ReasonCorrupt, "blob: the stored object %s grew past %d bytes", v.ref, v.ref.Size)
		return 0, v.err
	}
	if errors.Is(err, io.EOF) {
		if v.n != v.ref.Size {
			v.err = herr.Newf(ReasonCorrupt, "blob: the stored object %s is %d bytes, want %d", v.ref, v.n, v.ref.Size)
		} else if got := hex.EncodeToString(v.h.Sum(nil)); got != v.ref.Digest {
			v.err = herr.Newf(ReasonCorrupt, "blob: digest mismatch: the stored object %s hashes to %s", v.ref, got)
		} else {
			v.err = io.EOF
		}
		return n, v.err
	}
	if err != nil {
		v.err = herr.Wrap(ReasonReadFailed, err, "blob: read "+v.ref.String())
		return n, v.err
	}
	return n, nil
}

func (v *verifier) Close() error { return v.f.Close() }

// ctxReader stops a copy when ctx ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
