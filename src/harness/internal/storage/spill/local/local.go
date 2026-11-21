// Package local is the spill store's provider on the home's disk (DH-D-STORAGE-5,
// storage.md §6.1 "Home layout"): files under <home>/spill/<owner dir>/, each
// created exclusively and private to its owner (0600 in a 0700 directory on
// POSIX; on Windows a protected access list naming only the current user, SYSTEM
// and Administrators), read through a directory-rooted handle and only by the
// owner session or a session forked from it.
//
// Retention (MaxAge, the unreferenced grace) is the maintenance duty's
// (DH-D-STORAGE-9); this package only records MaxAge in its Config.
package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/smartpcr/agent-plugin/src/harness/internal/session/store"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/home"
	"github.com/smartpcr/agent-plugin/src/harness/internal/storage/spill"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/fsx"
	"github.com/smartpcr/agent-plugin/src/harness/internal/util/herr"
)

// Config is spill.StoreConfig of storage.md §6.2.
type Config struct {
	// MaxAge is how long a file is kept (0 keeps it while it is referenced).
	MaxAge time.Duration
	// MaxSaveBytes bounds every file.
	MaxSaveBytes int64
}

// DefaultConfig is the documented default: 720 h and 64 MiB.
var DefaultConfig = Config{MaxAge: 720 * time.Hour, MaxSaveBytes: 64 << 20}

// maxLineage bounds the walk up a fork chain, so a cyclic answer cannot hang Open.
const maxLineage = 256

// Lineage answers a session's parent: the session it was forked from, or "" for
// a root session.
type Lineage interface {
	Parent(ctx context.Context, id string) (string, error)
}

// Statter is the part of store.Store that ForkLineage reads.
type Statter interface {
	Stat(ctx context.Context, id string) (store.Summary, error)
}

// Ancestry is a Lineage that also answers a session's whole ancestry, nearest
// first, as recorded when it was forked, so it holds after an intermediate
// session is deleted (the SQLite store's lineage table).
type Ancestry interface {
	Ancestors(ctx context.Context, id string) ([]string, error)
}

// ForkLineage reads lineage from a session store's headers (DH-D-STORE-2); when
// the store also records ancestry (Ancestry), the lineage answers it too.
func ForkLineage(s Statter) Lineage {
	if a, ok := s.(Ancestry); ok {
		return forkAncestry{forkLineage{s}, a}
	}
	return forkLineage{s}
}

type forkAncestry struct {
	forkLineage
	Ancestry
}

type forkLineage struct{ s Statter }

func (f forkLineage) Parent(ctx context.Context, id string) (string, error) {
	sum, err := f.s.Stat(ctx, id)
	if err != nil {
		return "", err
	}
	return sum.Header.ForkSource, nil
}

// Citer tells Delete that a session other than owner still cites a file.
type Citer interface {
	CitedByOther(ctx context.Context, id, owner string) (bool, error)
}

// Option configures a Store.
type Option func(*Store)

// WithLineage lets forks of a session read its files; without it only the owner
// can.
func WithLineage(l Lineage) Option { return func(s *Store) { s.lineage = l } }

// WithCiter keeps, on Delete, the files another session cites; without it Delete
// removes all of the owner's files.
func WithCiter(c Citer) Option { return func(s *Store) { s.citer = c } }

// Store is the local spill store.
type Store struct {
	root    string
	cfg     Config
	lineage Lineage
	citer   Citer
}

var _ spill.Store = (*Store)(nil)

// New opens the spill store of the home. The spill directory must exist (home
// Ensure made it) and be private: one that is not refuses to start with an error
// naming it (DH-E-STORAGE-6). A zero MaxSaveBytes is the default.
func New(l home.Layout, cfg Config, opts ...Option) (*Store, error) {
	if cfg.MaxSaveBytes <= 0 {
		cfg.MaxSaveBytes = DefaultConfig.MaxSaveBytes
	}
	if cfg.MaxAge < 0 {
		return nil, fmt.Errorf("spill: MaxAge %s is negative", cfg.MaxAge)
	}
	root := l.Spill()
	info, err := os.Lstat(root)
	if err != nil {
		return nil, herr.Wrap(home.ReasonIO, err, "spill: the spill directory "+root)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return nil, herr.Newf(home.ReasonNotDirectory, "spill: %s is not a directory", root)
	}
	if err := home.CheckPrivate(root); err != nil {
		return nil, fmt.Errorf("spill: the spill directory %s: %w", root, err)
	}
	s := &Store{root: root, cfg: cfg}
	for _, o := range opts {
		o(s)
	}
	return s, nil
}

// Config is the store's configuration.
func (s *Store) Config() Config { return s.cfg }

// Reserve implements spill.Store.
func (s *Store) Reserve(owner, name string) spill.Ref {
	return spill.Ref{ID: spill.NewID(owner, name), Owner: owner}
}

// Create implements spill.Store.
func (s *Store) Create(ctx context.Context, ref spill.Ref, limit int64) (spill.Writer, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dir, file, err := spill.ParseID(ref.ID)
	if err != nil {
		return nil, err
	}
	if dir != spill.OwnerDir(ref.Owner) {
		return nil, herr.Newf(spill.ReasonNotOwner, "spill: %s was not reserved for session %q", ref.ID, ref.Owner)
	}
	if limit <= 0 || limit > s.cfg.MaxSaveBytes {
		limit = s.cfg.MaxSaveBytes
	}
	dirPath := filepath.Join(s.root, dir)
	if err := home.EnsurePrivateDir(dirPath); err != nil {
		return nil, fmt.Errorf("spill: session directory %s: %w", dir, err)
	}
	path := filepath.Join(dirPath, file)
	f, err := home.CreatePrivateFile(path)
	if err != nil {
		return nil, fmt.Errorf("spill: create %s: %w", ref.ID, err)
	}
	return &writer{f: f, path: path, dir: dirPath, id: ref.ID, limit: limit}, nil
}

// Open implements spill.Store.
func (s *Store) Open(ctx context.Context, caller, id string) (io.ReadSeekCloser, error) {
	dir, _, err := spill.ParseID(id)
	if err != nil {
		return nil, err
	}
	if err := s.owns(ctx, caller, dir); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return nil, herr.Wrap(home.ReasonIO, err, "spill: open the spill directory")
	}
	defer root.Close()
	f, err := root.Open(filepath.FromSlash(id))
	if err != nil {
		return nil, notFound(id, err)
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = fs.ErrNotExist
	}
	if err != nil {
		_ = f.Close()
		return nil, notFound(id, err)
	}
	return f, nil
}

// owns checks that caller is the owner of dir or descends from it by forks: by
// its recorded ancestry when the lineage keeps one (so a fork whose parent was
// deleted still reads what it inherited from a grandparent), and by walking the
// parents' headers. Either answer suffices; the recorded ancestry is not taken
// as complete, so a chain whose record misses a link (an older or imported
// session) is still judged by its headers.
func (s *Store) owns(ctx context.Context, caller, dir string) error {
	if spill.OwnerDir(caller) == dir {
		return nil
	}
	recorded := false
	if a, ok := s.lineage.(Ancestry); ok {
		ancestors, err := a.Ancestors(ctx, caller)
		if err != nil {
			return fmt.Errorf("spill: the ancestry of %q: %w", caller, err)
		}
		for _, id := range ancestors {
			if spill.OwnerDir(id) == dir {
				return nil
			}
		}
		recorded = len(ancestors) > 0
	}
	id := caller
	for range maxLineage {
		if spill.OwnerDir(id) == dir {
			return nil
		}
		if s.lineage == nil {
			break
		}
		parent, err := s.lineage.Parent(ctx, id)
		if err != nil {
			if recorded {
				// The walk ends at a deleted ancestor the record already covered.
				break
			}
			return fmt.Errorf("spill: the lineage of %q: %w", id, err)
		}
		if parent == "" {
			break
		}
		id = parent
	}
	return notOwner(caller)
}

func notOwner(caller string) error {
	return herr.Newf(spill.ReasonNotOwner, "spill: session %q neither owns the file nor was forked from its owner", caller)
}

// Stat implements spill.Store.
func (s *Store) Stat(ctx context.Context, id string) (spill.Info, error) {
	if err := ctx.Err(); err != nil {
		return spill.Info{}, err
	}
	if _, _, err := spill.ParseID(id); err != nil {
		return spill.Info{}, err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return spill.Info{}, herr.Wrap(home.ReasonIO, err, "spill: open the spill directory")
	}
	defer root.Close()
	info, err := root.Lstat(filepath.FromSlash(id))
	if err == nil && !info.Mode().IsRegular() {
		err = fs.ErrNotExist
	}
	if err != nil {
		return spill.Info{}, notFound(id, err)
	}
	return spill.Info{ID: id, Size: info.Size(), ModTime: info.ModTime()}, nil
}

// Delete implements spill.Store. The owner's directory is never followed if it
// is a link; files another session cites (WithCiter) are kept.
func (s *Store) Delete(ctx context.Context, owner string) error {
	name := spill.OwnerDir(owner)
	dir := filepath.Join(s.root, name)
	info, err := os.Lstat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return herr.Wrap(home.ReasonIO, err, "spill: delete "+name)
	}
	if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
		return herr.Newf(home.ReasonNotDirectory, "spill: %s is not a directory", name)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return herr.Wrap(home.ReasonIO, err, "spill: delete "+name)
	}
	var errs []error
	kept := 0
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		id := name + "/" + e.Name()
		if s.citer != nil {
			cited, err := s.citer.CitedByOther(ctx, id, owner)
			if err != nil {
				errs = append(errs, err)
				kept++
				continue
			}
			if cited {
				kept++
				continue
			}
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			kept++
		}
	}
	if kept == 0 {
		if err := os.Remove(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	if err := errors.Join(errs...); err != nil {
		return herr.Wrap(home.ReasonIO, err, "spill: delete "+name)
	}
	return nil
}

// HostPath implements spill.Store.
func (s *Store) HostPath(id string) (string, bool) {
	if _, _, err := spill.ParseID(id); err != nil {
		return "", false
	}
	return filepath.Join(s.root, filepath.FromSlash(id)), true
}

func notFound(id string, err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return herr.Newf(spill.ReasonNotFound, "spill: no saved file %s", id)
	}
	return herr.Wrap(home.ReasonIO, err, "spill: "+id)
}

// writer is one file being saved.
type writer struct {
	f     *os.File
	path  string
	dir   string
	id    string
	limit int64
	n     int64
	done  bool
	err   error
}

func (w *writer) Written() int64 { return w.n }

func (w *writer) Write(p []byte) (int, error) {
	if w.done {
		if w.err != nil {
			return 0, w.err
		}
		return 0, os.ErrClosed
	}
	if w.n+int64(len(p)) > w.limit {
		_ = w.Abort()
		w.err = herr.Newf(spill.ReasonTooLarge, "spill: %s passes its limit of %d bytes", w.id, w.limit)
		return 0, w.err
	}
	n, err := w.f.Write(p)
	w.n += int64(n)
	if err != nil {
		_ = w.Abort()
		w.err = herr.Wrap(home.ReasonIO, err, "spill: write "+w.id)
		return n, w.err
	}
	return n, nil
}

// syncDir makes a directory entry durable; tests replace it.
var syncDir = fsx.SyncDir

// Close syncs the file and its directory entry (the session directory's own
// entry in spill/ was synced when EnsurePrivateDir created it); on failure the
// file is removed.
func (w *writer) Close() error {
	if w.done {
		return w.err
	}
	w.done = true
	err := w.f.Sync()
	if cerr := w.f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = syncDir(w.dir)
	}
	if err != nil {
		_ = os.Remove(w.path)
		w.err = herr.Wrap(home.ReasonIO, err, "spill: save "+w.id)
	}
	return w.err
}

// Abort removes the file; after a successful Close it does nothing.
func (w *writer) Abort() error {
	if w.done {
		return nil
	}
	w.done = true
	w.err = herr.Newf(spill.ReasonNotFound, "spill: %s was aborted", w.id)
	_ = w.f.Close()
	if err := os.Remove(w.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
