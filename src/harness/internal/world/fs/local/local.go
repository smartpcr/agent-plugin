// Package local is the filesystem seam's local-disk provider (C32, DH-D-FS-4;
// filesystem.md sec. 6.1 "How the local provider writes"). It has no
// configuration of its own for the fence: the mode and the write roots of every
// mutation come only from the sandbox.Policy the call passes in.
//
// Reads are not fenced. A mutation resolves its Ref to the canonical path (each
// link, and on Windows each junction, followed component by component), fences
// that path with fs.FenceCanon, then performs every step by a name relative to
// an os.Root opened on the write root that contains it (the volume root under
// danger-full-access). The canonical name holds no link, so if a directory on it
// is swapped for a link between the check and the write, os.Root refuses to
// follow it ("path escapes from parent"); the provider resolves again, and the
// refusal stands as fs/sandbox-denied when the new path is outside the roots.
// Content is published through fsx.ReplaceIn and fsx.CreateIfAbsentIn (stage,
// write, sync, re-check the guard, rename), so a reader sees the old or the new
// bytes, never a mix.
package local

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"syscall"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
)

// filePerm is the mode of a file the provider creates; an existing file keeps
// its own mode (and on Windows its access-control list).
const filePerm iofs.FileMode = 0o644

// dirPerm is the mode of a parent directory the provider creates.
const dirPerm iofs.FileMode = 0o755

// reenter bounds how often one mutation resolves again after os.Root refused a
// link that appeared on its path (filesystem.md sec. 6.1: at most eight times).
const reenter = 8

// FS is the local-disk fs.FS. It is safe for concurrent use. The guard check and
// the change of one target run under that target's lock, held in process (a
// striped mutex) and across processes (an fsx.Lock on a lock file keyed by the
// target, in LockDir), so racing guarded writes from any number of providers or
// harness processes sharing the lock directory have one winner.
type FS struct {
	stripes [64]sync.Mutex
	lockDir string
}

var _ fs.FS = (*FS)(nil)

// Options configure a provider. They hold nothing the fence uses.
type Options struct {
	// LockDir holds the cross-process lock files; every harness process sharing a
	// workspace must use the same one. "" is DefaultLockDir().
	LockDir string `json:"lockDir,omitempty"`
}

// DefaultLockDir is the lock directory of a provider given none: one per user,
// so every harness process of the user shares it and no other user can. It is
// under the user's cache directory; when that is unknown, it is a per-uid name
// in the temporary directory, which on Linux and macOS is often the shared /tmp.
func DefaultLockDir() string {
	if d, err := os.UserCacheDir(); err == nil && d != "" {
		return filepath.Join(d, "dsh", "fs-locks")
	}
	name := "dsh-fs-locks"
	if uid := os.Getuid(); uid >= 0 {
		name = fmt.Sprintf("dsh-fs-locks-%d", uid)
	}
	return filepath.Join(os.TempDir(), name)
}

// ensureLockDir creates the lock directory (mode 0700) when missing, then
// requires it to be a real directory, not a link, owned by the current user and
// not writable by group or others, so another local user can neither deny the
// provider's writes by pre-creating it nor plant lock files in it.
func ensureLockDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return herr.Wrap(fs.CodeIO, err, "create the lock directory")
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return herr.Wrap(fs.CodeIO, err, "inspect the lock directory")
	}
	if fi.Mode()&iofs.ModeSymlink != 0 || fi.Mode()&iofs.ModeIrregular != 0 || !fi.IsDir() {
		return herr.Newf(fs.CodeIO, "lock directory %s is a link or not a directory", dir)
	}
	if uid, ok := ownerUID(fi); ok && uid != os.Geteuid() {
		return herr.Newf(fs.CodeIO, "lock directory %s is owned by uid %d, not the current user", dir, uid)
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o022 != 0 {
		return herr.Newf(fs.CodeIO, "lock directory %s has mode %#o, writable by group or others", dir, fi.Mode().Perm())
	}
	return nil
}

// ownerUID is the owner uid of fi where the platform reports one (the Uid of
// syscall.Stat_t on POSIX); ok is false on Windows, whose per-user cache and
// temporary directories are already private to the user.
func ownerUID(fi iofs.FileInfo) (int, bool) {
	v := reflect.ValueOf(fi.Sys())
	if v.Kind() == reflect.Pointer {
		if v.IsNil() {
			return 0, false
		}
		v = v.Elem()
	}
	if v.Kind() != reflect.Struct {
		return 0, false
	}
	f := v.FieldByName("Uid")
	if !f.IsValid() || !f.CanUint() {
		return 0, false
	}
	return int(f.Uint()), true
}

// New returns the local provider with the default options.
func New() *FS { return NewWithOptions(Options{}) }

// NewWithOptions returns the local provider configured by o.
func NewWithOptions(o Options) *FS {
	if o.LockDir == "" {
		o.LockDir = DefaultLockDir()
	}
	return &FS{lockDir: o.LockDir}
}

// lockFile is the cross-process lock file of the target k.
func (l *FS) lockFile(k fs.TargetKey) string {
	h := sha256.Sum256([]byte(k))
	return filepath.Join(l.lockDir, hex.EncodeToString(h[:16])+".lock")
}

// lock takes the locks of the targets, in-process then cross-process, each in a
// fixed order so two calls locking the same pair cannot deadlock. It waits for
// another holder until ctx ends.
func (l *FS) lock(ctx context.Context, keys ...fs.TargetKey) (func(), error) {
	unlock := l.lockStripes(keys...)
	files := make([]string, 0, len(keys))
	for _, k := range keys {
		if f := l.lockFile(k); !slices.Contains(files, f) {
			files = append(files, f)
		}
	}
	sort.Strings(files)
	if err := ensureLockDir(l.lockDir); err != nil {
		unlock()
		return nil, err
	}
	held := make([]*fsx.FileLock, 0, len(files))
	release := func() {
		for j := len(held) - 1; j >= 0; j-- {
			_ = held[j].Unlock()
		}
		unlock()
	}
	for _, f := range files {
		fl, err := fsx.Lock(ctx, f)
		if err != nil {
			release()
			if cerr := ctxErr(ctx); cerr != nil {
				return nil, cerr
			}
			return nil, herr.Wrap(fs.CodeIO, err, "take the target lock")
		}
		held = append(held, fl)
	}
	return release, nil
}

// lockStripes takes the in-process locks of the targets, in a fixed order.
func (l *FS) lockStripes(keys ...fs.TargetKey) func() {
	idx := make([]int, 0, len(keys))
	for _, k := range keys {
		h := fnv.New32a()
		_, _ = h.Write([]byte(k))
		i := int(h.Sum32() % uint32(len(l.stripes)))
		if !containsInt(idx, i) {
			idx = append(idx, i)
		}
	}
	sort.Ints(idx)
	for _, i := range idx {
		l.stripes[i].Lock()
	}
	return func() {
		for j := len(idx) - 1; j >= 0; j-- {
			l.stripes[idx[j]].Unlock()
		}
	}
}

func containsInt(s []int, v int) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func ctxErr(ctx context.Context) error {
	switch err := ctx.Err(); {
	case err == nil:
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		return herr.Wrap(fs.CodeTimeout, err, "filesystem call timed out")
	default:
		return herr.Wrap(fs.CodeAborted, err, "filesystem call aborted")
	}
}

// isEscape reports whether os.Root refused a name because a link on it leads
// out of the root (or is absolute, which a canonical name never holds unless a
// link was swapped in after resolution). os does not export the error.
func isEscape(err error) bool {
	return err != nil && strings.Contains(err.Error(), "path escapes from parent")
}

// mapErr gives err an fs code; an error that already has one keeps it.
func mapErr(err error, p string) error {
	if err == nil {
		return nil
	}
	if _, ok := herr.ReasonOf(err); ok {
		return err
	}
	switch {
	case isEscape(err):
		return herr.Wrap(fs.CodeSandboxDenied, err, p+" leaves its write root through a link")
	case errors.Is(err, context.Canceled):
		return herr.Wrap(fs.CodeAborted, err, "filesystem call aborted")
	case errors.Is(err, context.DeadlineExceeded):
		return herr.Wrap(fs.CodeTimeout, err, "filesystem call timed out")
	case errors.Is(err, iofs.ErrNotExist):
		return herr.Wrap(fs.CodeNotFound, err, p+" does not exist")
	case errors.Is(err, iofs.ErrExist):
		return herr.Wrap(fs.CodeAlreadyExists, err, p+" already exists")
	case errors.Is(err, iofs.ErrPermission):
		return herr.Wrap(fs.CodePermission, err, "permission denied: "+p)
	case errors.Is(err, syscall.ENOTDIR):
		return herr.Wrap(fs.CodeNotDirectory, err, p+": a parent is not a directory")
	}
	return herr.Wrap(fs.CodeIO, err, p)
}

// fileType classifies the Lstat of p.
func fileType(p string, fi iofs.FileInfo) fs.FileType {
	if _, ok := linkTarget(p, fi); ok {
		return fs.TypeSymlink
	}
	switch m := fi.Mode(); {
	case m.IsRegular():
		return fs.TypeFile
	case m.IsDir():
		return fs.TypeDir
	case m&iofs.ModeSymlink != 0:
		return fs.TypeSymlink
	}
	return fs.TypeOther
}

// meta is the metadata token of the file at canonical path p, whose Lstat is fi:
// fsx's identity, size and times for a file or directory, the link's own size
// and time for a link.
func meta(p string, fi iofs.FileInfo, typ fs.FileType) string {
	if typ == fs.TypeSymlink || typ == fs.TypeOther {
		return fmt.Sprintf("local:%s:%d:%d", typ, fi.Size(), fi.ModTime().UnixNano())
	}
	v, err := fsx.Stat(p)
	if err != nil {
		return fmt.Sprintf("local:%s:%d:%d", typ, fi.Size(), fi.ModTime().UnixNano())
	}
	return "local:" + v.String()
}

// readFile reads the regular file at canonical path p with the metadata token
// unchanged across the read, so the digest and the token describe one content.
func readFile(ctx context.Context, p string) ([]byte, fs.Info, error) {
	for range 5 {
		if err := ctxErr(ctx); err != nil {
			return nil, fs.Info{}, err
		}
		before, err := os.Lstat(p)
		if err != nil {
			return nil, fs.Info{}, mapErr(err, p)
		}
		if !before.Mode().IsRegular() {
			return nil, fs.Info{}, herr.Newf(fs.CodeNotRegularFile, "%s is a %s", p, fileType(p, before))
		}
		m := meta(p, before, fs.TypeFile)
		data, err := fsx.ReadFile(p)
		if err != nil {
			return nil, fs.Info{}, mapErr(err, p)
		}
		after, err := os.Lstat(p)
		if err != nil || !after.Mode().IsRegular() || meta(p, after, fs.TypeFile) != m {
			continue
		}
		in := fs.Info{Target: target(p), Exists: true, Type: fs.TypeFile, Size: int64(len(data))}
		in.Version = fs.Version{Meta: m, Digest: sha256.Sum256(data), HasDigest: true}
		return data, in, nil
	}
	return nil, fs.Info{}, herr.Newf(fs.CodeIO, "%s kept changing while it was read", p)
}

// lookup resolves r (following the final link unless noFollowFinal) and Lstats
// the result; a missing file is a nil FileInfo.
func lookup(ctx context.Context, r fs.Ref, noFollowFinal bool) (string, iofs.FileInfo, error) {
	if err := ctxErr(ctx); err != nil {
		return "", nil, err
	}
	p, err := abs(r)
	if err != nil {
		return "", nil, err
	}
	if p, err = resolve(p, noFollowFinal); err != nil {
		return "", nil, err
	}
	fi, err := os.Lstat(p)
	if errors.Is(err, iofs.ErrNotExist) {
		return p, nil, nil
	}
	if err != nil {
		return "", nil, mapErr(err, p)
	}
	return p, fi, nil
}

// Stat implements fs.FS; the Version carries metadata only.
func (l *FS) Stat(ctx context.Context, r fs.Ref) (fs.Info, error) {
	p, fi, err := lookup(ctx, r, r.NoFollowFinal)
	if err != nil {
		return fs.Info{}, err
	}
	in := fs.Info{Target: target(p)}
	if fi == nil {
		return in, nil
	}
	in.Exists, in.Type = true, fileType(p, fi)
	if in.Type == fs.TypeFile {
		in.Size = fi.Size()
	}
	in.Version = fs.Version{Meta: meta(p, fi, in.Type)}
	return in, nil
}

// Open implements fs.FS: the content is read whole, so the Version's digest is
// that of the bytes the reader yields.
func (l *FS) Open(ctx context.Context, r fs.Ref) (io.ReadCloser, fs.Info, error) {
	p, fi, err := lookup(ctx, r, r.NoFollowFinal)
	if err != nil {
		return nil, fs.Info{}, err
	}
	if fi == nil {
		return nil, fs.Info{}, herr.Newf(fs.CodeNotFound, "%s does not exist", p)
	}
	data, in, err := readFile(ctx, p)
	if err != nil {
		return nil, fs.Info{}, err
	}
	return io.NopCloser(bytes.NewReader(data)), in, nil
}

// ReadDir implements fs.FS. Files fsx is staging are not listed.
func (l *FS) ReadDir(ctx context.Context, r fs.Ref, limit int) ([]fs.DirEntry, bool, error) {
	p, fi, err := lookup(ctx, r, false)
	if err != nil {
		return nil, false, err
	}
	if fi == nil {
		return nil, false, herr.Newf(fs.CodeNotFound, "%s does not exist", p)
	}
	if !fi.IsDir() {
		return nil, false, herr.Newf(fs.CodeNotDirectory, "%s is not a directory", p)
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		return nil, false, mapErr(err, p)
	}
	out := make([]fs.DirEntry, 0, len(ents))
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), fsx.StagePrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue // removed since the listing
		}
		d := fs.DirEntry{Name: e.Name(), Type: fileType(filepath.Join(p, e.Name()), info), ModTime: info.ModTime()}
		if d.Type == fs.TypeFile {
			d.Size = info.Size()
		}
		out = append(out, d)
	}
	if limit > 0 && len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}
