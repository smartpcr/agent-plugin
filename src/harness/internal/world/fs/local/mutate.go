package local

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	iofs "io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

// errRelinked: a name that resolved to no link is a link now. It is treated as
// os.Root's escape refusal: resolve again and fence the new path.
var errRelinked = errors.New("a link appeared on the path after it was resolved")

func relinked(err error) bool {
	return errors.Is(err, errRelinked) || isEscape(err) || errors.Is(err, syscall.ELOOP)
}

// changed wraps an error of a rooted step that means the tree changed under the
// call after it was resolved and fenced (a directory on the path removed,
// renamed or replaced by a file): the attempt runs again from resolution.
type changed struct{ err error }

func (c changed) Error() string { return c.err.Error() }
func (c changed) Unwrap() error { return c.err }

// rooted marks err of a rooted step: a link refusal or a vanished directory is
// a change of the tree since resolution; anything else is the step's failure.
func rooted(err error) error {
	switch {
	case err == nil:
		return nil
	case relinked(err):
		return errRelinked
	case errors.Is(err, iofs.ErrNotExist), errors.Is(err, syscall.ENOTDIR), busy(err):
		return changed{err}
	}
	return err
}

// busy: on Windows, a directory on the path is being renamed, removed or made a
// link by someone else right now (ERROR_SHARING_VIOLATION, or ERROR_ACCESS_DENIED
// on a delete-pending entry); the tree is changing under the call. A lasting
// denial still ends as fs/permission-denied once mutate's budget is spent.
func busy(err error) bool {
	return runtime.GOOS == "windows" && (errors.Is(err, syscall.Errno(32)) || errors.Is(err, syscall.Errno(5)))
}

// pinned is one mutation's view of its policy: each write root canonicalized
// once, when the call starts. A root (or a directory above it) swapped for a
// link while the call runs therefore cannot move the fence with it: the next
// attempt resolves the target through the link, and the result is outside the
// pinned roots.
type pinned struct {
	pol   sandbox.Policy
	roots map[string]string // as issued -> canonical
}

func pin(pol sandbox.Policy) *pinned {
	c := &pinned{pol: pol, roots: map[string]string{}}
	for _, r := range pol.WriteRoots() {
		cr := r.Path
		if x, err := canon(r.Path); err == nil && filepath.IsAbs(x) {
			cr = filepath.Clean(x)
		}
		c.roots[r.Path] = cr
	}
	return c
}

// fence is the one fence of every mutation: the canonical path against the
// pinned write roots.
func (c *pinned) fence(op fs.Op, path string) error {
	return fs.FenceCanon(c.pol, op, path, func(root string) (string, error) {
		if cr, ok := c.roots[root]; ok {
			return cr, nil
		}
		return canon(root)
	})
}

// rootFor is the directory a mutation of the canonical path p works under: the
// deepest pinned write root containing p under workspace-write, the volume root
// under danger-full-access. The fence has admitted p already.
func (c *pinned) rootFor(p string) (string, error) {
	return c.rootOf(p)
}

// rootOf is the deepest pinned root containing every path of ps.
func (c *pinned) rootOf(ps ...string) (string, error) {
	if c.pol.Mode() == sandbox.DangerFullAccess {
		vol := filepath.VolumeName(ps[0])
		for _, p := range ps[1:] {
			if !strings.EqualFold(filepath.VolumeName(p), vol) {
				return "", herr.Newf(fs.CodeInvalidPath, "%s and %s are on different volumes", ps[0], p)
			}
		}
		return vol + string(filepath.Separator), nil
	}
	best := ""
	for issued, cr := range c.roots {
		for _, cand := range []string{issued, cr} {
			all := true
			for _, p := range ps {
				all = all && fs.Within(cand, p)
			}
			if all && len(cand) > len(best) {
				best = cand
			}
		}
	}
	if best == "" {
		return "", herr.Newf(fs.CodeSandboxDenied, "%v is not under one writable root of %s", ps, c.pol.Mode())
	}
	return best, nil
}

// beforeOpenRoot, when set (tests only), runs just before a mutation opens the
// handle of its root: the window between the fence and the rooted operation.
var beforeOpenRoot func(dir string)

// openRoot opens the handle of the canonical directory dir without trusting its
// path: from the volume root it opens one component at a time through the
// handle of its parent, refuses a component that is a link, and checks that
// the directory it opened is the one it examined (same file identity). So a
// root, or any directory above it, swapped for a link after the fence is
// errRelinked and never yields a handle on the link's target. dir is the write
// root itself: only that last component, when missing (a write root not created
// yet), is created through its parent. A missing directory above it is outside
// every write root and is never created: the call fails fs/not-found.
func openRoot(dir string) (*os.Root, error) {
	if beforeOpenRoot != nil {
		beforeOpenRoot(dir)
	}
	vol := filepath.VolumeName(dir) + string(filepath.Separator)
	cur, err := os.OpenRoot(vol)
	if err != nil {
		return nil, err
	}
	rest, err := filepath.Rel(vol, dir)
	if err != nil {
		_ = cur.Close()
		return nil, err
	}
	if rest == "." {
		return cur, nil
	}
	names := strings.Split(rest, string(filepath.Separator))
	for i, name := range names {
		next, err := openChild(cur, name, i == len(names)-1)
		_ = cur.Close()
		if err != nil {
			return nil, err
		}
		cur = next
	}
	return cur, nil
}

// openChild opens the directory name of parent as a root, creating it when
// missing only if create is set (name is the write root itself); a missing
// directory above the write root is fs/not-found. A link, or a directory that
// is not the one Lstat saw, is errRelinked.
func openChild(parent *os.Root, name string, create bool) (*os.Root, error) {
	fi, err := parent.Lstat(name)
	if errors.Is(err, iofs.ErrNotExist) {
		if !create {
			return nil, herr.Wrap(fs.CodeNotFound, err, filepath.Join(parent.Name(), name)+" does not exist and is above the write root")
		}
		if err := parent.Mkdir(name, dirPerm); err != nil && !errors.Is(err, iofs.ErrExist) {
			return nil, rooted(err)
		}
		fi, err = parent.Lstat(name)
	}
	if err != nil {
		return nil, rooted(err)
	}
	if _, isLink := linkTarget(filepath.Join(parent.Name(), name), fi); isLink || fi.Mode()&iofs.ModeSymlink != 0 {
		return nil, errRelinked
	}
	if !fi.IsDir() {
		return nil, changed{herr.Newf(fs.CodeNotDirectory, "%s is not a directory", filepath.Join(parent.Name(), name))}
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, rooted(err)
	}
	st, err := child.Stat(".")
	if err != nil || !os.SameFile(fi, st) {
		_ = child.Close()
		if err != nil {
			return nil, rooted(err)
		}
		return nil, errRelinked
	}
	return child, nil
}

// mkdirAll makes the parents d inside root. The path was resolved link-free, so
// a component that is now in the way (os.Root sees a link or a file where a
// directory was) means the tree changed since: resolve and fence again.
func mkdirAll(root *os.Root, d string) error {
	err := root.MkdirAll(d, dirPerm)
	if errors.Is(err, iofs.ErrExist) {
		return changed{err}
	}
	return rooted(err)
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
//
// A directory on the path that vanished or changed kind after the fence (the
// tree changed under the call) runs the attempt again too, within a budget of
// its own; once that budget is spent the last such error is returned.
func mutate(ctx context.Context, what string, attempt func() error) error {
	links, changes := 0, 0
	for {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		err := attempt()
		var ch changed
		switch {
		case relinked(err):
			if links++; links >= reenter {
				return herr.Newf(fs.CodeSandboxDenied, "%s: a link kept leading out of the write root", what)
			}
		case errors.As(err, &ch):
			if changes++; changes >= maxChanges {
				return mapErr(ch.err, what)
			}
			if changes > 4 {
				// Let a rename or delete in progress finish.
				time.Sleep(time.Millisecond)
			}
		default:
			return mapErr(err, what)
		}
	}
}

// maxChanges bounds how often one mutation starts again because the tree
// changed under it.
const maxChanges = 64

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

// replaceIn and createIn publish content inside a root; tests replace them to
// inject faults after publication.
var (
	replaceIn = fsx.ReplaceIn
	createIn  = fsx.CreateIfAbsentIn
)

// Write implements fs.FS: the final link is followed, the resolved path fenced,
// and the content published by a durable replace inside the write root. The
// guard is checked under the target's lock before staging and, for
// ReplaceIfVersion, again after the content is staged and synced, just before
// the rename; the lock is held until the publication is done. Cancellation or a
// deadline is observed at that same last moment: nothing is published, the
// staged file is removed, and the call fails fs/aborted or fs/timeout. A sync
// that fails after the rename is reported in WriteOutcome.DurabilityUnknown, not
// as success.
func (l *FS) Write(ctx context.Context, r fs.Ref, data []byte, g fs.Guard, pol sandbox.Policy) (fs.WriteOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.WriteOutcome{}, err
	}
	c := pin(pol)
	var out fs.WriteOutcome
	err := mutate(ctx, r.Path, func() error {
		p, fi, err := lookup(ctx, r, false)
		if err != nil {
			return err
		}
		if err := c.fence(fs.OpWrite, p); err != nil {
			return err
		}
		if fi != nil && !fi.Mode().IsRegular() {
			return herr.Newf(fs.CodeNotRegularFile, "%s is a %s", p, fileType(p, fi))
		}
		o, err := l.writeAt(ctx, c, p, data, g)
		out = o
		return err
	})
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	return out, nil
}

func (l *FS) writeAt(ctx context.Context, c *pinned, p string, data []byte, g fs.Guard) (fs.WriteOutcome, error) {
	dir, err := c.rootFor(p)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	name, err := rel(dir, p)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	unlock, err := l.lock(ctx, key(p))
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	defer unlock()
	root, err := openRoot(dir)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	defer root.Close()

	cur, exists, err := currentIn(root, name, p)
	if err != nil {
		return fs.WriteOutcome{}, rooted(err)
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
	// The last check before the staged file takes the name: a cancelled call or
	// a passed deadline publishes nothing (the staged file is removed), and a
	// ReplaceIfVersion guard is checked again against what is there now.
	opt := options()
	opt.BeforePublish = func() error {
		if err := ctxErr(ctx); err != nil {
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
	}
	if g == fs.CreateIfAbsent {
		err = createIn(root, name, fsx.Bytes(data), opt)
		if errors.Is(err, iofs.ErrExist) {
			return fs.WriteOutcome{}, herr.Wrap(fs.CodeAlreadyExists, err, "the file already exists")
		}
	} else {
		err = replaceIn(root, name, fsx.Bytes(data), opt)
	}
	var unknown error
	if errors.Is(err, fsx.ErrDurabilityUnknown) {
		// The new content is in place; only its survival of a power loss is unknown.
		unknown, err = err, nil
	}
	if err != nil {
		if _, coded := herr.ReasonOf(err); coded {
			return fs.WriteOutcome{}, err
		}
		return fs.WriteOutcome{}, rooted(err)
	}
	v := fs.Version{Digest: sha256.Sum256(data), HasDigest: true}
	if fi, err := root.Lstat(name); err == nil {
		v.Meta = meta(p, fi, fs.TypeFile)
	}
	return fs.WriteOutcome{Target: target(p), Created: !exists, Version: v, DurabilityUnknown: unknown}, nil
}

// Delete implements fs.FS: the final component is not followed, so deleting a
// link deletes the link; a directory is fs/not-regular-file.
func (l *FS) Delete(ctx context.Context, r fs.Ref, g fs.Guard, pol sandbox.Policy) (fs.DeleteOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.DeleteOutcome{}, err
	}
	c := pin(pol)
	var out fs.DeleteOutcome
	err := mutate(ctx, r.Path, func() error {
		p, _, err := lookup(ctx, r, true)
		if err != nil {
			return err
		}
		if err := c.fence(fs.OpDelete, p); err != nil {
			return err
		}
		o, err := l.deleteAt(ctx, c, p, g)
		out = o
		return err
	})
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	return out, nil
}

func (l *FS) deleteAt(ctx context.Context, c *pinned, p string, g fs.Guard) (fs.DeleteOutcome, error) {
	dir, err := c.rootFor(p)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	name, err := rel(dir, p)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	unlock, err := l.lock(ctx, key(p))
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	defer unlock()
	root, err := openRoot(dir)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	defer root.Close()

	cur, exists, err := currentIn(root, name, p)
	if err != nil {
		return fs.DeleteOutcome{}, rooted(err)
	}
	if exists {
		fi, err := root.Lstat(name)
		if err != nil {
			return fs.DeleteOutcome{}, rooted(err)
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
	if err := ctxErr(ctx); err != nil {
		return fs.DeleteOutcome{}, err
	}
	if err := root.Remove(name); err != nil {
		if errors.Is(err, iofs.ErrNotExist) {
			return fs.DeleteOutcome{Target: target(p)}, nil
		}
		return fs.DeleteOutcome{}, rooted(err)
	}
	return fs.DeleteOutcome{Target: target(p), Existed: true}, nil
}

// Move implements fs.FS: both ends are resolved without following the final
// component and fenced before anything changes; a directory moves with its tree.
// The rename is always a rooted one, inside the one write root that holds both
// ends; a move between two write roots that share no root is refused with
// fs/invalid-path before anything changes, because no single handle could
// confine it.
func (l *FS) Move(ctx context.Context, src, dst fs.Ref, o fs.MoveOptions, pol sandbox.Policy) (fs.MoveOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.MoveOutcome{}, err
	}
	c := pin(pol)
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
		if err := c.fence(fs.OpMoveSource, sp); err != nil {
			return err
		}
		if err := c.fence(fs.OpMoveDestination, dp); err != nil {
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
		replaced, err := l.moveAt(ctx, c, sp, dp, o)
		out = fs.MoveOutcome{Source: target(sp), Destination: target(dp), Replaced: replaced}
		return err
	})
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	return out, nil
}

func (l *FS) moveAt(ctx context.Context, c *pinned, sp, dp string, o fs.MoveOptions) (replaced bool, err error) {
	dir, err := c.rootOf(sp, dp)
	if herr.Kind(err) == fs.CodeSandboxDenied {
		return false, herr.Newf(fs.CodeInvalidPath, "cannot move %s to %s: they are under different write roots, and a move is only made inside one", sp, dp)
	}
	if err != nil {
		return false, err
	}
	sname, err := rel(dir, sp)
	if err != nil {
		return false, err
	}
	dname, err := rel(dir, dp)
	if err != nil {
		return false, err
	}
	unlock, err := l.lock(ctx, key(sp), key(dp))
	if err != nil {
		return false, err
	}
	defer unlock()
	root, err := openRoot(dir)
	if err != nil {
		return false, err
	}
	defer root.Close()

	if _, err := root.Lstat(sname); err != nil {
		return false, rooted(err)
	}
	if _, err := root.Lstat(dname); err == nil {
		if !o.Overwrite {
			return false, herr.Newf(fs.CodeAlreadyExists, "%s already exists", dp)
		}
		replaced = true
	} else if !errors.Is(err, iofs.ErrNotExist) {
		return false, rooted(err)
	}
	if d := filepath.Dir(dname); d != "." {
		if err := mkdirAll(root, d); err != nil {
			return false, err
		}
	}
	if err := ctxErr(ctx); err != nil {
		return false, err
	}
	return replaced, rooted(root.Rename(sname, dname))
}
