// Package fstest is the fake filesystem seam and the conformance suite every
// provider must pass (DH-T-FS-8). MemFS keeps files in memory with the seam's
// semantics: canonical targets, symlink resolution before the fence, guards
// applied atomically with the change, fs codes on every error. Run is the suite.
// Only tests and other fakes import this package (archtest).
package fstest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/fs"
	"github.com/smartpcr/agent-plugin/src/harness/internal/world/sandbox"
)

const maxHops = 40

type node struct {
	typ    fs.FileType
	data   []byte
	link   string // symlink target, absolute
	gen    uint64
	parent string
}

// MemFS is an in-memory fs.FS. It is safe for concurrent use; every call holds
// one lock, so a guard and its mutation are atomic.
type MemFS struct {
	mu    sync.Mutex
	nodes map[string]*node // key: canonical key of the path
	names map[string]string
	gen   uint64
}

var _ fs.FS = (*MemFS)(nil)

// NewMemFS returns an empty MemFS: every absolute path's volume root exists.
func NewMemFS() *MemFS { return &MemFS{nodes: map[string]*node{}, names: map[string]string{}} }

func key(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

func isRoot(p string) bool { return filepath.Dir(p) == p }

// Symlink makes link a symlink to target, creating link's parents. A relative
// target is kept relative, so it resolves against the link's directory wherever
// that directory is moved, as a real symlink does.
func (m *MemFS) Symlink(target, link string) error {
	l, err := abs(fs.Ref{Path: link})
	if err != nil {
		return err
	}
	if target == "" {
		return herr.New(fs.CodeInvalidPath, "empty symlink target")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	parent, err := m.mkdirs(filepath.Dir(l))
	if err != nil {
		return err
	}
	name := filepath.Join(parent, filepath.Base(l))
	if _, ok := m.nodes[key(name)]; ok {
		return herr.Newf(fs.CodeAlreadyExists, "%s already exists", name)
	}
	m.put(name, &node{typ: fs.TypeSymlink, link: filepath.Clean(target)})
	return nil
}

// Mkdir creates the directory dir and its parents.
func (m *MemFS) Mkdir(dir string) error {
	d, err := abs(fs.Ref{Path: dir})
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	_, err = m.mkdirs(d)
	return err
}

func abs(r fs.Ref) (string, error) {
	p := r.Path
	if p == "" {
		return "", herr.New(fs.CodeInvalidPath, "empty path")
	}
	if !filepath.IsAbs(p) {
		if r.Dir == "" || !filepath.IsAbs(r.Dir) {
			return "", herr.Newf(fs.CodeInvalidPath, "relative path %q with no absolute working directory", p)
		}
		p = filepath.Join(r.Dir, p)
	}
	return filepath.Clean(p), nil
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

func (m *MemFS) put(p string, n *node) {
	m.gen++
	n.gen = m.gen
	n.parent = filepath.Dir(p)
	m.nodes[key(p)] = n
	m.names[key(p)] = p
}

func (m *MemFS) del(p string) {
	delete(m.nodes, key(p))
	delete(m.names, key(p))
}

// resolve returns the canonical path of p: every symlink followed (the final one
// too, unless noFollowFinal). The result may not exist.
func (m *MemFS) resolve(p string, noFollowFinal bool) (string, error) {
	hops := 0
	for {
		vol := filepath.VolumeName(p)
		rest := strings.TrimPrefix(p[len(vol):], string(filepath.Separator))
		parts := []string{}
		if rest != "" {
			parts = strings.Split(rest, string(filepath.Separator))
		}
		cur := vol + string(filepath.Separator)
		restarted := false
		for i, part := range parts {
			next := filepath.Join(cur, part)
			n, ok := m.nodes[key(next)]
			if ok {
				next = m.names[key(next)]
			}
			last := i == len(parts)-1
			if ok && n.typ == fs.TypeSymlink && !(last && noFollowFinal) {
				hops++
				if hops > maxHops {
					return "", herr.Newf(fs.CodeIO, "too many symlinks resolving %s", p)
				}
				base := n.link
				if !filepath.IsAbs(base) {
					// cur is the link's canonical parent at this moment.
					base = filepath.Join(cur, base)
				}
				p = filepath.Join(append([]string{base}, parts[i+1:]...)...)
				restarted = true
				break
			}
			if ok && n.typ != fs.TypeDir && !last {
				return "", herr.Newf(fs.CodeNotDirectory, "%s is not a directory", next)
			}
			cur = next
		}
		if !restarted {
			return cur, nil
		}
	}
}

func (m *MemFS) mkdirs(d string) (string, error) {
	d, err := m.resolve(d, false)
	if err != nil {
		return "", err
	}
	if isRoot(d) {
		return d, nil
	}
	if n, ok := m.nodes[key(d)]; ok {
		if n.typ != fs.TypeDir {
			return "", herr.Newf(fs.CodeNotDirectory, "%s is not a directory", d)
		}
		return m.names[key(d)], nil
	}
	parent, err := m.mkdirs(filepath.Dir(d))
	if err != nil {
		return "", err
	}
	name := filepath.Join(parent, filepath.Base(d))
	m.put(name, &node{typ: fs.TypeDir})
	return name, nil
}

func target(p string) fs.Target { return fs.Target{Key: fs.TargetKey(key(p)), Display: p} }

func version(n *node, digest bool) fs.Version {
	v := fs.Version{Meta: fmt.Sprintf("mem:%d:%d", n.gen, len(n.data))}
	if digest && n.typ == fs.TypeFile {
		v.Digest, v.HasDigest = sha256.Sum256(n.data), true
	}
	return v
}

func info(p string, n *node, digest bool) fs.Info {
	in := fs.Info{Target: target(p)}
	if n == nil {
		return in
	}
	in.Exists, in.Type, in.Version = true, n.typ, version(n, digest)
	if n.typ == fs.TypeFile {
		in.Size = int64(len(n.data))
	}
	return in
}

// lookup resolves r and returns its canonical path and node (nil if missing).
func (m *MemFS) lookup(ctx context.Context, r fs.Ref) (string, *node, error) {
	if err := ctxErr(ctx); err != nil {
		return "", nil, err
	}
	p, err := abs(r)
	if err != nil {
		return "", nil, err
	}
	p, err = m.resolve(p, r.NoFollowFinal)
	if err != nil {
		return "", nil, err
	}
	if isRoot(p) {
		return p, &node{typ: fs.TypeDir}, nil
	}
	return p, m.nodes[key(p)], nil
}

// Stat implements fs.FS; the Version carries metadata only.
func (m *MemFS) Stat(ctx context.Context, r fs.Ref) (fs.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, n, err := m.lookup(ctx, r)
	if err != nil {
		return fs.Info{}, err
	}
	return info(p, n, false), nil
}

// Open implements fs.FS; the Version carries the digest of the content.
func (m *MemFS) Open(ctx context.Context, r fs.Ref) (io.ReadCloser, fs.Info, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, n, err := m.lookup(ctx, r)
	if err != nil {
		return nil, fs.Info{}, err
	}
	if n == nil {
		return nil, fs.Info{}, herr.Newf(fs.CodeNotFound, "%s does not exist", p)
	}
	if n.typ != fs.TypeFile {
		return nil, fs.Info{}, herr.Newf(fs.CodeNotRegularFile, "%s is a %s", p, n.typ)
	}
	return io.NopCloser(bytes.NewReader(slices.Clone(n.data))), info(p, n, true), nil
}

// ReadDir implements fs.FS.
func (m *MemFS) ReadDir(ctx context.Context, r fs.Ref, limit int) ([]fs.DirEntry, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, n, err := m.lookup(ctx, r)
	if err != nil {
		return nil, false, err
	}
	if n == nil {
		return nil, false, herr.Newf(fs.CodeNotFound, "%s does not exist", p)
	}
	if n.typ != fs.TypeDir {
		return nil, false, herr.Newf(fs.CodeNotDirectory, "%s is not a directory", p)
	}
	var out []fs.DirEntry
	for k, c := range m.nodes {
		if key(c.parent) != key(p) {
			continue
		}
		e := fs.DirEntry{Name: filepath.Base(m.names[k]), Type: c.typ}
		if c.typ == fs.TypeFile {
			e.Size = int64(len(c.data))
		}
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b fs.DirEntry) int { return strings.Compare(a.Name, b.Name) })
	if limit > 0 && len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}

// Write implements fs.FS: the final symlink is followed, and the resolved path
// is fenced.
func (m *MemFS) Write(ctx context.Context, r fs.Ref, data []byte, g fs.Guard, pol sandbox.Policy) (fs.WriteOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.WriteOutcome{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, n, err := m.lookup(ctx, r)
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	if err := fs.Fence(pol, fs.OpWrite, p); err != nil {
		return fs.WriteOutcome{}, err
	}
	if n != nil && n.typ != fs.TypeFile {
		return fs.WriteOutcome{}, herr.Newf(fs.CodeNotRegularFile, "%s is a %s", p, n.typ)
	}
	var cur fs.Version
	if n != nil {
		cur = version(n, true)
	}
	if err := fs.CheckGuard(g, n != nil, cur); err != nil {
		return fs.WriteOutcome{}, err
	}
	parent, err := m.mkdirs(filepath.Dir(p))
	if err != nil {
		return fs.WriteOutcome{}, err
	}
	name := filepath.Join(parent, filepath.Base(p))
	if n != nil {
		name = m.names[key(p)]
	}
	nn := &node{typ: fs.TypeFile, data: slices.Clone(data)}
	m.put(name, nn)
	return fs.WriteOutcome{Target: target(name), Created: n == nil, Version: version(nn, true)}, nil
}

// Delete implements fs.FS: the final component is not followed, so deleting a
// link deletes the link; a directory is fs/not-regular-file.
func (m *MemFS) Delete(ctx context.Context, r fs.Ref, g fs.Guard, pol sandbox.Policy) (fs.DeleteOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.DeleteOutcome{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r.NoFollowFinal = true
	p, n, err := m.lookup(ctx, r)
	if err != nil {
		return fs.DeleteOutcome{}, err
	}
	if err := fs.Fence(pol, fs.OpDelete, p); err != nil {
		return fs.DeleteOutcome{}, err
	}
	if n != nil && n.typ == fs.TypeDir {
		return fs.DeleteOutcome{}, herr.Newf(fs.CodeNotRegularFile, "%s is a directory", p)
	}
	var cur fs.Version
	if n != nil {
		cur = version(n, true)
	}
	if err := fs.CheckGuard(g, n != nil, cur); err != nil {
		return fs.DeleteOutcome{}, err
	}
	if n == nil {
		return fs.DeleteOutcome{Target: target(p)}, nil
	}
	name := m.names[key(p)]
	m.del(name)
	return fs.DeleteOutcome{Target: target(name), Existed: true}, nil
}

// Move implements fs.FS: both ends are resolved without following the final
// component and fenced before anything changes; a directory moves with its tree.
func (m *MemFS) Move(ctx context.Context, src, dst fs.Ref, o fs.MoveOptions, pol sandbox.Policy) (fs.MoveOutcome, error) {
	if err := pol.Validate(); err != nil {
		return fs.MoveOutcome{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	src.NoFollowFinal, dst.NoFollowFinal = true, true
	sp, sn, err := m.lookup(ctx, src)
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	dp, dn, err := m.lookup(ctx, dst)
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	if err := fs.Fence(pol, fs.OpMoveSource, sp); err != nil {
		return fs.MoveOutcome{}, err
	}
	if err := fs.Fence(pol, fs.OpMoveDestination, dp); err != nil {
		return fs.MoveOutcome{}, err
	}
	if sn == nil {
		return fs.MoveOutcome{}, herr.Newf(fs.CodeNotFound, "%s does not exist", sp)
	}
	if key(sp) == key(dp) {
		return fs.MoveOutcome{}, herr.Newf(fs.CodeSameTarget, "%s and %s are the same file", sp, dp)
	}
	if isRoot(sp) || fs.Within(sp, dp) {
		return fs.MoveOutcome{}, herr.Newf(fs.CodeInvalidPath, "cannot move %s into itself", sp)
	}
	if dn != nil {
		if !o.Overwrite {
			return fs.MoveOutcome{}, herr.Newf(fs.CodeAlreadyExists, "%s already exists", dp)
		}
		if dn.typ == fs.TypeDir || sn.typ == fs.TypeDir {
			return fs.MoveOutcome{}, herr.Newf(fs.CodeNotRegularFile, "cannot overwrite %s with %s", dp, sp)
		}
	}
	parent, err := m.mkdirs(filepath.Dir(dp))
	if err != nil {
		return fs.MoveOutcome{}, err
	}
	sname := m.names[key(sp)]
	dname := filepath.Join(parent, filepath.Base(dp))
	if dn != nil {
		m.del(m.names[key(dp)])
	}
	type moved struct {
		name string
		n    *node
	}
	var tree []moved
	for k, n := range m.nodes {
		if name := m.names[k]; fs.Within(sname, name) {
			tree = append(tree, moved{name, n})
		}
	}
	for _, t := range tree {
		m.del(t.name)
	}
	for _, t := range tree {
		rel, _ := filepath.Rel(sname, t.name)
		to := filepath.Clean(filepath.Join(dname, rel))
		gen := t.n.gen
		m.put(to, t.n)
		if to != dname {
			t.n.gen = gen
		}
	}
	return fs.MoveOutcome{Source: target(sname), Destination: target(dname), Replaced: dn != nil}, nil
}
