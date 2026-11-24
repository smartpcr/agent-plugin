// Package fs is the contract of the filesystem seam (C32, DH-D-FS-1; cycle C-3 of
// impl.md sec. 10.2, architecture.md R-06): the FS provider interface, the Ref and
// Target a call names, the Version a guarded write compares, the Guard of a
// mutation, the Op the fence classifies, the fence itself and the fs error codes.
//
// A provider (world/fs/local, a remote host) implements FS; world/fs/ops is the
// one caller that combines it with a sandbox.Policy, and every tool goes through
// ops. Every mutating method takes the Policy of the call and refuses with
// fs/sandbox-denied before it touches anything; a zero Policy is refused with
// sandbox/unavailable. Every provider passes fstest.Run.
package fs

import (
	"context"
	"io"

	"github.com/smartpcr/agent-plugin/src/go/cordis"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// Ref names a path as a call gave it.
//
// Resolution is lexical first: a relative Path is joined with Dir and the result
// is cleaned (filepath.Clean: "." and ".." removed textually) before any symlink
// is followed. So for "ws/link/../secret" with link -> /outside/dir the path is
// "ws/secret", not the "/outside/secret" a POSIX kernel would reach from the raw
// string. Symlinks are then resolved on that cleaned path to give the Target,
// and a provider must fence and operate on that Target, never hand the raw Path
// to the OS.
type Ref struct {
	// Path is absolute, or relative to Dir.
	Path string
	// Dir is the absolute directory a relative Path is resolved against: the
	// session's working directory.
	Dir string
	// NoFollowFinal: the final component is not followed if it is a symlink.
	NoFollowFinal bool
}

// TargetKey identifies one resolved file of one provider: two Refs are the same
// file exactly when their keys are equal (canonical path, case-folded where the
// filesystem is).
type TargetKey string

// Target is a resolved Ref.
type Target struct {
	Key TargetKey
	// Display is the canonical path to show and to fence.
	Display string
}

// FileType is the kind of a resolved file.
type FileType string

// The file types.
const (
	TypeFile    FileType = "file"
	TypeDir     FileType = "dir"
	TypeSymlink FileType = "symlink"
	TypeOther   FileType = "other"
)

// Info describes a resolved Ref.
type Info struct {
	Target Target
	// Exists is false for a missing file; the other fields but Target are zero.
	Exists  bool
	Type    FileType
	Size    int64
	Version Version
}

// Version is what a provider knows about a file's state: provider metadata (a
// cheap token from stat) and, when the content has been read, its SHA-256.
type Version struct {
	// Meta is the provider's metadata token (size, mtime, inode, generation).
	Meta string
	// Digest is the SHA-256 of the content; meaningful when HasDigest.
	Digest    [32]byte
	HasDigest bool
}

// IsZero reports whether v carries nothing.
func (v Version) IsZero() bool { return v.Meta == "" && !v.HasDigest }

// Same reports whether v and o describe the same content: when both carry a
// digest the digests decide (so a rewrite with the same bytes is not stale);
// otherwise the metadata tokens decide. A zero Version is never Same.
func (v Version) Same(o Version) bool {
	if v.IsZero() || o.IsZero() {
		return false
	}
	if v.HasDigest && o.HasDigest {
		return v.Digest == o.Digest
	}
	return v.Meta != "" && v.Meta == o.Meta
}

// Guard is the precondition of a mutation: NoGuard, CreateIfAbsent or
// ReplaceIfVersion. The set is closed.
type Guard interface{ guard() }

type noGuard struct{}

func (noGuard) guard() {}

type createIfAbsent struct{}

func (createIfAbsent) guard() {}

// ReplaceIfVersion holds only when the file exists and its current Version is
// Same as Version.
type ReplaceIfVersion struct{ Version Version }

func (ReplaceIfVersion) guard() {}

// NoGuard always holds. CreateIfAbsent holds only when the file does not exist.
var (
	NoGuard        Guard = noGuard{}
	CreateIfAbsent Guard = createIfAbsent{}
)

// CheckGuard applies g to a file that exists (with current version cur) or not:
// fs/already-exists for CreateIfAbsent on an existing file, fs/stale-version for
// ReplaceIfVersion when the file is missing or changed. Providers call it under
// the same lock or handle as the mutation.
func CheckGuard(g Guard, exists bool, cur Version) error {
	switch g := g.(type) {
	case nil, noGuard:
		return nil
	case createIfAbsent:
		if exists {
			return errorf(CodeAlreadyExists, "the file already exists")
		}
		return nil
	case ReplaceIfVersion:
		if !exists {
			return errorf(CodeStaleVersion, "the file was deleted since it was read")
		}
		if !g.Version.Same(cur) {
			return errorf(CodeStaleVersion, "the file changed since it was read; read it again")
		}
		return nil
	default:
		return errorf(CodeIO, "unknown guard %T", g)
	}
}

// DirEntry is one entry of a directory listing.
type DirEntry struct {
	Name string
	Type FileType
	Size int64
}

// WriteOutcome is the result of a write.
type WriteOutcome struct {
	Target  Target
	Created bool
	// Version is the new version, with its digest.
	Version Version
}

// DeleteOutcome is the result of a delete.
type DeleteOutcome struct {
	Target Target
	// Existed is false when the file was already gone (no error).
	Existed bool
}

// MoveOptions are the options of a move.
type MoveOptions struct {
	// Overwrite lets the move replace an existing destination file.
	Overwrite bool
}

// MoveOutcome is the result of a move.
type MoveOutcome struct {
	Source, Destination Target
	// Replaced is true when an existing destination was overwritten.
	Replaced bool
}

// FS is a filesystem provider. Reads are not fenced; every mutation fences each
// target it writes with Fence before it touches anything, and applies its Guard
// atomically with the change. Errors carry the fs codes (herr.Kind).
type FS interface {
	// Stat resolves r. A missing file is Info{Exists: false} with no error.
	Stat(ctx context.Context, r Ref) (Info, error)
	// Open opens r for reading; the returned Info's Version carries the digest of
	// the content the reader yields.
	Open(ctx context.Context, r Ref) (io.ReadCloser, Info, error)
	// ReadDir lists the directory r, sorted by name, at most limit entries
	// (limit <= 0 means no limit); truncated reports whether entries were left out.
	ReadDir(ctx context.Context, r Ref, limit int) (entries []DirEntry, truncated bool, err error)
	// Write replaces (or creates, with missing parents) the file r with data.
	Write(ctx context.Context, r Ref, data []byte, g Guard, p sandbox.Policy) (WriteOutcome, error)
	// Delete removes the file r (never a directory); the final component is not
	// followed.
	Delete(ctx context.Context, r Ref, g Guard, p sandbox.Policy) (DeleteOutcome, error)
	// Move renames src to dst; both ends are fenced (OpMoveSource,
	// OpMoveDestination) and neither changes when either is refused.
	Move(ctx context.Context, src, dst Ref, o MoveOptions, p sandbox.Policy) (MoveOutcome, error)
}

// Key is the service a filesystem provider provides.
var Key = cordis.NewServiceKey[FS]("fs")
