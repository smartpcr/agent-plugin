package local

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"syscall"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// errRelinked: a name that resolved to no link is a link now. It is treated as
// os.Root's escape refusal: resolve again and fence the new path.
var errRelinked = errors.New("a link appeared on the path after it was resolved")

func relinked(err error) bool { return errors.Is(err, errRelinked) || isEscape(err) }

// fence is the one fence of every mutation: the canonical path against the
// policy's write roots, each root canonicalized the same way.
func fence(p sandbox.Policy, op fs.Op, path string) error {
	return fs.FenceCanon(p, op, path, canon)
}

// rootFor is the canonical directory a mutation of the canonical path p works
// under: the deepest write root containing p under workspace-write, the volume
// root under danger-full-access. Fence has admitted p already.
func rootFor(pol sandbox.Policy, p string) (string, error) {
	if pol.Mode() == sandbox.DangerFullAccess {
		return filepath.VolumeName(p) + string(filepath.Separator), nil
	}
	best := ""
	for _, r := range pol.WriteRoots() {
		cands := []string{r.Path}
		if c, err := canon(r.Path); err == nil && filepath.IsAbs(c) {
			cands = append(cands, filepath.Clean(c))
		}
		for _, c := range cands {
			if fs.Within(c, p) && len(c) > len(best) {
				best = c
			}
		}
	}
	if best == "" {
		return "", herr.Newf(fs.CodeSandboxDenied, "%s is outside the writable roots of %s", p, pol.Mode())
	}
	return best, nil
}

// openRoot opens the rooted handle of dir, creating the write root itself when
// it does not exist yet.
func openRoot(dir string) (*os.Root, error) {
	r, err := os.OpenRoot(dir)
	if errors.Is(err, iofs.ErrNotExist) {
		if err := os.MkdirAll(dir, dirPerm); err != nil {
			return nil, err
		}
		r, err = os.OpenRoot(dir)
	}
	return r, err
}

// mkdirAll makes the parents d inside root. The path was resolved link-free, so
// a component that is now in the way (os.Root sees a link or a file where a
// directory was) means the tree changed since: resolve and fence again.
func mkdirAll(root *os.Root, d string) error {
	err := root.MkdirAll(d, dirPerm)
	if errors.Is(err, iofs.ErrExist) || errors.Is(err, syscall.ENOTDIR) {
		return errRelinked
	}
	return err
}

// rel is the name of the canonical path p inside the root dir.
func rel(dir, p string) (string, error) {
	n, err := filepath.Rel(dir, p)
	if err != nil || !fs.Within(dir, p) {
		return "", herr.Newf(fs.CodeSandboxDenied, "%s is not below %s", p, dir)
	}
	return n, nil
}

// mutate runs one attempt of a mutation and runs it again, up to reenter times,
// while os.Root refuses a link that appeared on the path after it was resolved:
// each attempt resolves and fences anew, so a path that now leads outside the
// roots is fs/sandbox-denied before anything is touched.
func mutate(ctx context.Context, what string, attempt func() error) error {
	for range reenter {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		err := attempt()
		if !relinked(err) {
			return mapErr(err, what)
		}
	}
	return herr.Newf(fs.CodeSandboxDenied, "%s: a link kept leading out of the write root", what)
}

// currentIn is the version (with digest) of name in root, the canonical path
// p; exists is false for a missing file.
func currentIn(root *os.Root, name, p string) (v fs.Version, exists bool, err error) {
	fi, err := root.Lstat(name)
	if errors.Is(err, iofs.ErrNotExist) {
		return fs.Version{}, false, nil
	}
	if err != nil {
		return fs.Version{}, false, err
	}
	typ := fileType(p, fi)
	if typ != fs.TypeFile {
		return fs.Version{Meta: meta(p, fi, typ)}, true, nil
	}
	f, err := root.Open(name)
	if err != nil {
		return fs.Version{}, true, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fs.Version{}, true, err
	}
	v = fs.Version{Meta: meta(p, fi, typ), HasDigest: true}
	copy(v.Digest[:], h.Sum(nil))
	return v, true, nil
}

func options() fsx.Options { return fsx.Options{Perm: filePerm, KeepPerm: true, Durable: true} }

// Write implements fs.FS: the final link is followed, the resolved path fenced,
// and the content published by a durable replace inside the write root. A
// ReplaceIfVersion guard is checked before staging and again after the content
// is staged, just before the rename.
func (l *FS) Write(ctx context.Context, r fs.Ref, data []byte, g fs.Guard, pol sandbox.Policy) (fs.WriteOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.WriteOutcome{}, err
	}
	var out fs.WriteOutcome
	err := mutate(ctx, r.Path, func() error {
		p, fi, err := lookup(ctx, r, false)
		if err != nil {
			return err
		}
		if err := fence(pol, fs.OpWrite, p); err != nil {
			return err
		}
		if fi != nil && !fi.Mode().IsRegular() {
			return herr.Newf(fs.CodeNotRegularFile, "%s is a %s", p, fileType(p, fi))
		}
		o, err := l.writeAt(ctx, pol, p, data, g)
		out = o
		return err
	})
	return out, err
}

func (l *FS) writeAt(ctx context.Context, pol sandbox.Policy, p string, data []byte, g fs.Guard) (fs.WriteOutcome, error) {
	dir, err := rootFor(pol, p)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	name, err := rel(dir, p)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	root, err := openRoot(dir)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	defer root.Close()
	defer l.lock(key(p))()

	cur, exists, err := currentIn(root, name, p)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	if fi, err := root.Lstat(name); err == nil && !fi.Mode().IsRegular() {
		if _, isLink := linkTarget(p, fi); isLink {
			return fs.WriteOutcome{}, errRelinked
		}
		return fs.WriteOutcome{}, herr.Newf(fs.CodeNotRegularFile, "%s is a %s", p, fileType(p, fi))
	}
	if err := fs.CheckGuard(g, exists, cur); err != nil {
		return fs.WriteOutcome{}, err
	}
	if err := ctxErr(ctx); err != nil {
		return fs.WriteOutcome{}, err
	}
	if d := filepath.Dir(name); d != "." {
		if err := mkdirAll(root, d); err != nil {
			return fs.WriteOutcome{}, err
		}
	}
	if g == fs.CreateIfAbsent {
		err = fsx.CreateIfAbsentIn(root, name, fsx.Bytes(data), options())
		if errors.Is(err, iofs.ErrExist) {
			return fs.WriteOutcome{}, herr.Wrap(fs.CodeAlreadyExists, err, "the file already exists")
		}
	} else {
		err = fsx.ReplaceIn(root, name, func(w io.Writer) error {
			if err := fsx.Bytes(data)(w); err != nil {
				return err
			}
			if _, ok := g.(fs.ReplaceIfVersion); !ok {
				return nil
			}
			now, exists, err := currentIn(root, name, p)
			if err != nil {
				return err
			}
			return fs.CheckGuard(g, exists, now)
		}, options())
	}
	if errors.Is(err, fsx.ErrDurabilityUnknown) {
		// The new content is in place; only its survival of a power loss is unknown.
		err = nil
	}
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	v := fs.Version{Digest: sha256.Sum256(data), HasDigest: true}
	if fi, err := root.Lstat(name); err == nil {
		v.Meta = meta(p, fi, fs.TypeFile)
	}
	return fs.WriteOutcome{Target: target(p), Created: !exists, Version: v}, nil
}

// Delete implements fs.FS: the final component is not followed, so deleting a
// link deletes the link; a directory is fs/not-regular-file.
func (l *FS) Delete(ctx context.Context, r fs.Ref, g fs.Guard, pol sandbox.Policy) (fs.DeleteOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.DeleteOutcome{}, err
	}
	var out fs.DeleteOutcome
	err := mutate(ctx, r.Path, func() error {
		p, _, err := lookup(ctx, r, true)
		if err != nil {
			return err
		}
		if err := fence(pol, fs.OpDelete, p); err != nil {
			return err
		}
		o, err := l.deleteAt(pol, p, g)
		out = o
		return err
	})
	return out, err
}

func (l *FS) deleteAt(pol sandbox.Policy, p string, g fs.Guard) (fs.DeleteOutcome, error) {
	dir, err := rootFor(pol, p)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	name, err := rel(dir, p)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	root, err := openRoot(dir)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	defer root.Close()
	defer l.lock(key(p))()

	cur, exists, err := currentIn(root, name, p)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	if exists {
		fi, err := root.Lstat(name)
		if err != nil {
			return fs.DeleteOutcome{}, err
		}
		if fileType(p, fi) == fs.TypeDir {
			return fs.DeleteOutcome{}, herr.Newf(fs.CodeNotRegularFile, "%s is a directory", p)
		}
	}
	if err := fs.CheckGuard(g, exists, cur); err != nil {
		return fs.DeleteOutcome{}, err
	}
	if !exists {
		return fs.DeleteOutcome{Target: target(p)}, nil
	}
	if err := root.Remove(name); err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			return fs.DeleteOutcome{Target: target(p)}, nil
		}
		return fs.DeleteOutcome{}, err
	}
	return fs.DeleteOutcome{Target: target(p), Existed: true}, nil
}

// Move implements fs.FS: both ends are resolved without following the final
// component and fenced before anything changes; a directory moves with its tree.
// When both ends are under one write root the rename is a rooted one.
func (l *FS) Move(ctx context.Context, src, dst fs.Ref, o fs.MoveOptions, pol sandbox.Policy) (fs.MoveOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.MoveOutcome{}, err
	}
	var out fs.MoveOutcome
	err := mutate(ctx, src.Path, func() error {
		sp, sfi, err := lookup(ctx, src, true)
		if err != nil {
			return err
		}
		dp, dfi, err := lookup(ctx, dst, true)
		if err != nil {
			return err
		}
		if err := fence(pol, fs.OpMoveSource, sp); err != nil {
			return err
		}
		if err := fence(pol, fs.OpMoveDestination, dp); err != nil {
			return err
		}
		if sfi == nil {
			return herr.Newf(fs.CodeNotFound, "%s does not exist", sp)
		}
		if key(sp) == key(dp) {
			return herr.Newf(fs.CodeSameTarget, "%s and %s are the same file", sp, dp)
		}
		if filepath.Dir(sp) == sp || fs.Within(sp, dp) {
			return herr.Newf(fs.CodeInvalidPath, "cannot move %s into itself", sp)
		}
		if dfi != nil {
			if !o.Overwrite {
				return herr.Newf(fs.CodeAlreadyExists, "%s already exists", dp)
			}
			if fileType(sp, sfi) == fs.TypeDir || fileType(dp, dfi) == fs.TypeDir {
				return herr.Newf(fs.CodeNotRegularFile, "cannot overwrite %s with %s", dp, sp)
			}
		}
		if err := ctxErr(ctx); err != nil {
			return err
		}
		replaced, err := l.moveAt(pol, sp, dp, o)
		out = fs.MoveOutcome{Source: target(sp), Destination: target(dp), Replaced: replaced}
		return err
	})
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	return out, nil
}

func (l *FS) moveAt(pol sandbox.Policy, sp, dp string, o fs.MoveOptions) (replaced bool, err error) {
	sdir, err := rootFor(pol, sp)
	if err != nil {
		return false, err
	}
	ddir, err := rootFor(pol, dp)
	if err != nil {
		return false, err
	}
	sname, err := rel(sdir, sp)
	if err != nil {
		return false, err
	}
	dname, err := rel(ddir, dp)
	if err != nil {
		return false, err
	}
	sroot, err := openRoot(sdir)
	if err != nil {
		return false, err
	}
	defer sroot.Close()
	droot := sroot
	if ddir != sdir {
		if droot, err = openRoot(ddir); err != nil {
			return false, err
		}
		defer droot.Close()
	}
	defer l.lock(key(sp), key(dp))()

	if _, err := sroot.Lstat(sname); err != nil {
		return false, err
	}
	if _, err := droot.Lstat(dname); err == nil {
		if !o.Overwrite {
			return false, herr.Newf(fs.CodeAlreadyExists, "%s already exists", dp)
		}
		replaced = true
	} else if !errors.Is(err, iofs.ErrNotExist) {
		return false, err
	}
	if d := filepath.Dir(dname); d != "." {
		if err := mkdirAll(droot, d); err != nil {
			return false, err
		}
	}
	if droot == sroot {
		return replaced, sroot.Rename(sname, dname)
	}
	// Two roots: os.Root cannot rename across handles. Both ends were fenced and
	// re-checked through their roots just now; the rename itself is by path.
	return replaced, os.Rename(sp, dp)
}
