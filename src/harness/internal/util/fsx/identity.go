package fsx

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// FileID names a file independently of the path used to reach it: device and inode
// on POSIX, volume serial number and file index on Windows (DH-LIB-NODEFS-R4). Two
// paths with equal IDs reach the same file.
type FileID struct {
	Dev uint64
	Ino uint64
}

// ID returns the identity of the file path names, following symbolic links.
func ID(path string) (FileID, error) {
	v, err := Stat(path)
	return v.ID, err
}

// Version is the token a reader records and a writer compares before it replaces
// (DH-LIB-NODEFS-R5): identity, size and times, and optionally a content digest.
type Version struct {
	ID       FileID
	Size     int64
	ModNs    int64 // modification time, nanoseconds since the epoch
	ChangeNs int64 // status-change time; zero on Windows, which has none
	Digest   string
}

// Stat returns the version of the file path names, following symbolic links. The
// digest is left empty; WithDigest fills it.
func Stat(path string) (Version, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Version{}, err
	}
	id, change, err := identity(path)
	if err != nil {
		return Version{}, err
	}
	return Version{ID: id, Size: info.Size(), ModNs: info.ModTime().UnixNano(), ChangeNs: change}, nil
}

// String is the metadata token "dev:ino:size:mtime:ctime".
func (v Version) String() string {
	return fmt.Sprintf("%d:%d:%d:%d:%d", v.ID.Dev, v.ID.Ino, v.Size, v.ModNs, v.ChangeNs)
}

// WithDigest returns v with Digest set to the digest of content.
func (v Version) WithDigest(content io.Reader) (Version, error) {
	d, err := Digest(content)
	v.Digest = d
	return v, err
}

// Same reports whether v and o name the same content. When both carry a digest,
// identity, size and digest decide, so a touch that changed only the times is not
// a change; otherwise the whole metadata token decides.
func (v Version) Same(o Version) bool {
	if v.Digest != "" && o.Digest != "" {
		return v.ID == o.ID && v.Size == o.Size && v.Digest == o.Digest
	}
	return v.ID == o.ID && v.Size == o.Size && v.ModNs == o.ModNs && v.ChangeNs == o.ChangeNs
}

// Digest is the hex SHA-256 of everything r yields.
func Digest(r io.Reader) (string, error) {
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// Canonical returns the absolute path with every symbolic link in its existing part
// resolved; the part that does not exist yet is appended unresolved, so a path a tool
// is about to create is canonical too (DH-LIB-NODEFS-R7).
func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var rest []string
	for cur := abs; ; {
		resolved, err := filepath.EvalSymlinks(cur)
		if err == nil {
			return filepath.Join(append([]string{resolved}, rest...)...), nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs, nil
		}
		rest = append([]string{filepath.Base(cur)}, rest...)
		cur = parent
	}
}

// Inside reports whether path is root or under it, comparing cleaned components
// (case-insensitively on Windows and macOS), never string prefixes: /a/bc is not
// inside /a/b. Both paths should be canonical.
func Inside(root, path string) bool {
	r, p := components(root), components(path)
	if len(p) < len(r) {
		return false
	}
	for i := range r {
		if !sameName(r[i], p[i]) {
			return false
		}
	}
	return true
}

func components(path string) []string {
	path = filepath.Clean(path)
	vol := filepath.VolumeName(path)
	rest := strings.Trim(filepath.ToSlash(path[len(vol):]), "/")
	parts := []string{vol}
	if rest != "" {
		parts = append(parts, strings.Split(rest, "/")...)
	}
	return parts
}

func sameName(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
