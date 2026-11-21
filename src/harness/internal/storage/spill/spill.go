// Package spill is the contract of the spill store (DH-D-STORAGE-5,
// docs/harness/areas/storage.md §6.1): the full text of an oversized tool result,
// saved privately under <home>/spill so that only a bounded head enters the
// session log, and read back by its id.
//
// An id is the file's path below <home>/spill, slash-separated:
//
//	<16 hex of SHA-256(owner session id)>/<24 hex random>-<sanitised name>
//
// It is made by Reserve without touching the disk, so a caller can size the
// notice that cites it before anything is written. The first segment is the
// owner's directory, the same name the session store removes when it deletes the
// session (internal/session/store/sqlite.SpillName). The provider is spill/local.
package spill

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"time"
	"unicode/utf8"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/event"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Reasons of the errors a spill store returns (storage.md §6.2 "Errors").
const (
	ReasonNotFound herr.Reason = "spill/not-found"
	ReasonNotOwner herr.Reason = "spill/not-owner"
	ReasonTooLarge herr.Reason = "spill/too-large"
)

// Lengths of an id's parts.
const (
	// DirLen is the hex length of the owner's directory name.
	DirLen = 16
	// RandomLen is the hex length of the random part: 96 bits.
	RandomLen = 24
	// MaxNameLen bounds the sanitised name, so an id is at most MaxIDLen bytes.
	MaxNameLen = 40
	// MaxIDLen is the longest id Reserve returns.
	MaxIDLen = DirLen + 1 + RandomLen + 1 + MaxNameLen
)

// DefaultName replaces a suggested name with nothing usable left in it.
const DefaultName = "output.txt"

// Ref is a reserved or saved spill file.
type Ref struct {
	// ID is the file's slash-separated path below <home>/spill.
	ID string
	// Owner is the session the file was reserved for.
	Owner string
}

// Info describes a saved file.
type Info struct {
	ID      string
	Size    int64
	ModTime time.Time
}

// Writer saves one reserved file. A Write past the limit fails with
// spill/too-large and removes the file. Close syncs the file and makes the
// reference valid; Abort removes it. Either may follow the other, or itself.
type Writer interface {
	io.Writer
	// Written is the number of bytes accepted so far.
	Written() int64
	Close() error
	Abort() error
}

// Store is a spill store.
type Store interface {
	// Reserve returns a fresh id for owner, nothing written yet.
	Reserve(owner, name string) Ref
	// Create makes ref's file, private and exclusively, and returns its writer;
	// limit bounds its size (0, or more than the store allows, is the store's
	// MaxSaveBytes).
	Create(ctx context.Context, ref Ref, limit int64) (Writer, error)
	// Open reads a saved file for caller, which must be its owner or a session
	// forked from it (transitively); any other caller gets spill/not-owner.
	Open(ctx context.Context, caller, id string) (io.ReadSeekCloser, error)
	// Stat describes a saved file.
	Stat(ctx context.Context, id string) (Info, error)
	// Delete removes owner's files that no other session cites.
	Delete(ctx context.Context, owner string) error
	// HostPath is the file's path on this machine, for the filesystem seam only.
	HostPath(id string) (string, bool)
}

// OwnerDir is the directory name of owner's files: the first 16 hex digits of
// the SHA-256 of the session id.
func OwnerDir(owner string) string {
	sum := sha256.Sum256([]byte(owner))
	return hex.EncodeToString(sum[:])[:DirLen]
}

// NewID returns a fresh id for owner and the suggested name.
func NewID(owner, name string) string {
	var b [RandomLen / 2]byte
	_, _ = rand.Read(b[:])
	return OwnerDir(owner) + "/" + hex.EncodeToString(b[:]) + "-" + SanitizeName(name)
}

// SanitizeName makes a suggested name one safe path segment of at most
// MaxNameLen bytes by home.SafeSegment: separators, NUL and "..", or a Windows
// device name, never survive as such. Nothing usable left is DefaultName.
func SanitizeName(name string) string { return home.SafeSegment(name, MaxNameLen, DefaultName) }

// ParseID splits a well-formed id into its owner directory and file name. Any
// other string, one that could leave the spill root included, is refused with
// spill/not-found.
func ParseID(id string) (dir, file string, err error) {
	bad := herr.Newf(ReasonNotFound, "spill: %q is not a spill id", clip(id))
	if len(id) > MaxIDLen || len(id) < DirLen+1+RandomLen+2 || id[DirLen] != '/' {
		return "", "", bad
	}
	dir, file = id[:DirLen], id[DirLen+1:]
	if !isHex(dir) || !isHex(file[:RandomLen]) || file[RandomLen] != '-' {
		return "", "", bad
	}
	if name := file[RandomLen+1:]; SanitizeName(name) != name {
		return "", "", bad
	}
	return dir, file, nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func clip(s string) string {
	if len(s) > MaxIDLen {
		return s[:MaxIDLen] + "..."
	}
	return s
}

// Save keeps text within the log's bound: a text of at most headBytes is
// returned unchanged with no reference; a longer one is saved whole through s
// for owner, and the returned head is its longest prefix of at most headBytes
// that ends on a rune boundary, with the reference to log beside it (the
// tool-result event's spill field: Path is the id, Bytes the saved size). A
// failed save returns the error and no reference; the caller still bounds what it
// logs (DH-ADR-STORAGE-4).
func Save(ctx context.Context, s Store, owner, name string, text []byte, headBytes int) (head []byte, ref *event.Spill, err error) {
	if len(text) <= headBytes {
		return text, nil, nil
	}
	r := s.Reserve(owner, name)
	w, err := s.Create(ctx, r, int64(len(text)))
	if err != nil {
		return nil, nil, err
	}
	if _, err := w.Write(text); err != nil {
		_ = w.Abort()
		return nil, nil, err
	}
	if err := w.Close(); err != nil {
		return nil, nil, err
	}
	return Head(text, headBytes), &event.Spill{Path: r.ID, Bytes: int64(len(text))}, nil
}

// Head is text's longest prefix of at most n bytes that does not split a rune.
func Head(text []byte, n int) []byte {
	if n <= 0 {
		return nil
	}
	if len(text) <= n {
		return text
	}
	cut := n
	for cut > 0 && cut > n-utf8.UTFMax && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
